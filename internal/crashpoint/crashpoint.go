// Package crashpoint is the deterministic kill-point seam for
// crash-stability testing (#94, Wave R1). It places named hooks at every
// point the recovery matrix (Wave R4) needs to simulate a process kill,
// and lets a test "crash" execution at exactly one of those points and
// then drive recovery against the resulting on-disk state.
//
// # Production cost
//
// Unarmed — which is always the case in production, since nothing but a
// test ever calls Arm — At and Armed cost a single atomic.Bool load and
// return immediately. There is no map lookup, no lock, no allocation on
// the hot path. This is why the seam is a *runtime* opt-in rather than a
// build-tagged one: per AGENTS.md, //go:build-gated code is excluded from
// the normal compile and bit-rots silently. Keeping the hooks in the
// always-compiled path means a refactor that breaks a call site fails the
// build immediately, and the negligible unarmed cost buys that safety.
//
// # Crash shape (for the R4 RestartWithCrash harness)
//
// An armed At(name) panics with a *Crash sentinel. The harness recovers
// it (see recover + errors.As on *Crash), discards in-memory state, and
// re-opens the on-disk home through the recovery path — modeling a real
// process kill at that instruction. In-process limitation: Go runs
// deferred functions as the panic unwinds the stack, so any cleanup
// defer between the crashpoint and the recover WILL execute. A real crash
// would not run defers. Call sites that must leave forensic on-disk
// residue for recovery (e.g. a partial temp file) therefore arrange that
// residue *before* calling At, and must not rely on a "crash skips my
// defer" assumption. See store.WriteFileAtomic's torn-write variant.
//
// # Coverage gate
//
// Every call site registers its name (typically via a package-level
// Register(...) var, which runs at import time). RegisteredNames()
// enumerates them so the R4 mechanical coverage gate can fail the suite
// if a registered point has no crash scenario.
package crashpoint

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Crash is the sentinel value an armed crashpoint panics with. The R4
// harness recovers it to model a process kill at Point.
type Crash struct {
	Point string
}

func (c *Crash) Error() string { return "crashpoint: simulated crash at " + c.Point }

var (
	mu sync.Mutex
	// armed maps an armed point to its remaining hit countdown: N means
	// "fire on the Nth At from now" (ArmOnHit); the default is 1 (fire on
	// the next At, the original behavior). The countdown never drops below
	// 1 — once it reaches 1 the point fires on that hit and every later
	// one until Disarm'd, so the default single-hit semantics are the
	// degenerate n=1 case, not a separate mode.
	armed    = map[string]int{}
	registry = map[string]struct{}{} // every Register'd point name
	// anyArmed is the fast-path gate: false ⇒ no point is armed, so At and
	// Armed return without taking mu. Written under mu, read locklessly.
	anyArmed atomic.Bool
)

// Register records name as a known crashpoint site and returns it, so a
// site can declare its name inline as a package-level var:
//
//	var cpPreRename = crashpoint.Register("store.WriteFileAtomic.preRename")
//
// Registration is what the R4 coverage gate enumerates via
// RegisteredNames; a point that is never Register'd is invisible to the
// gate. Registering the same name twice is idempotent.
func Register(name string) string {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = struct{}{}
	return name
}

// RegisteredNames returns every Register'd crashpoint name, sorted for
// deterministic enumeration by the coverage gate.
func RegisteredNames() []string {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// At is the kill-point hook. When name is armed and its hit countdown is
// exhausted it panics with a *Crash sentinel; otherwise it is a no-op
// (decrementing the countdown when armed for a later hit). Unarmed cost
// is one atomic load.
func At(name string) {
	if !anyArmed.Load() {
		return
	}
	if fire(name) {
		panic(&Crash{Point: name})
	}
}

// fire consumes one hit of name's countdown and reports whether this hit
// is the one that crashes.
func fire(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	n, ok := armed[name]
	if !ok {
		return false
	}
	if n > 1 {
		armed[name] = n - 1
		return false
	}
	return true
}

// Armed reports whether the NEXT At(name) will panic, for call sites that
// must branch into a crash-specific code path (e.g. a torn-write variant)
// so they can stage on-disk residue before the At that panics. A point
// armed for a later hit (ArmOnHit(n>1) with hits remaining) reports
// false — its next At is still a no-op. Unarmed cost is one atomic load.
func Armed(name string) bool {
	if !anyArmed.Load() {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	return armed[name] == 1
}

// ArmOption customizes Arm. The zero configuration (no options) fires on
// the next At — the original single-hit behavior.
type ArmOption func(*armConfig)

type armConfig struct{ hit int }

// ArmOnHit arms the point to fire on the nth At(name) from now (1-based;
// n=1 is the default next-hit behavior, n<1 is clamped to 1). This is the
// multi-write-prefix knob: a call site that passes the same point several
// times per operation (e.g. between successive canonical renames) can be
// killed at exactly the kth crossing.
func ArmOnHit(n int) ArmOption {
	return func(c *armConfig) { c.hit = n }
}

// Arm marks name so a subsequent At(name) panics — the next one by
// default, the nth with ArmOnHit(n). It is test-only by convention —
// nothing in production arms a point. It returns a disarm closure; a
// test should defer it so the arm never leaks into a sibling test.
func Arm(name string, opts ...ArmOption) (disarm func()) {
	cfg := armConfig{hit: 1}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.hit < 1 {
		cfg.hit = 1
	}
	mu.Lock()
	armed[name] = cfg.hit
	anyArmed.Store(true)
	mu.Unlock()
	return func() { Disarm(name) }
}

// Disarm removes name from the armed set. Disarming an un-armed name is a
// no-op.
func Disarm(name string) {
	mu.Lock()
	delete(armed, name)
	anyArmed.Store(len(armed) > 0)
	mu.Unlock()
}
