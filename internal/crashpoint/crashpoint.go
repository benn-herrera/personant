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
	mu       sync.Mutex
	armed    = map[string]struct{}{} // currently-armed points (test-only)
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

// At is the kill-point hook. When name is armed it panics with a *Crash
// sentinel; otherwise it is a no-op. Unarmed cost is one atomic load.
func At(name string) {
	if !anyArmed.Load() {
		return
	}
	if isArmed(name) {
		panic(&Crash{Point: name})
	}
}

// Armed reports whether name is currently armed, for call sites that must
// branch into a crash-specific code path (e.g. a torn-write variant) so
// they can stage on-disk residue before the At that panics. Unarmed cost
// is one atomic load.
func Armed(name string) bool {
	if !anyArmed.Load() {
		return false
	}
	return isArmed(name)
}

func isArmed(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := armed[name]
	return ok
}

// Arm marks name so the next At(name) panics (and Armed(name) reports
// true). It is test-only by convention — nothing in production arms a
// point. It returns a disarm closure; a test should defer it so the arm
// never leaks into a sibling test.
func Arm(name string) (disarm func()) {
	mu.Lock()
	armed[name] = struct{}{}
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
