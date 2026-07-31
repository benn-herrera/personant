package shell

import (
	"sync"
	"sync/atomic"
)

// Test-only synchronization seam.
//
// # What it is for
//
// The interrupt-ownership invariant on Runner is about an INSTANT — the
// span between Run committing to a child and that child's process group
// existing. A test that tries to hit that instant by racing a real
// goroutine against it is a load-dependent test: it passes on a quiet
// machine and fails on a busy one, which is the exact failure mode this
// seam exists to retire. An armed hook lets a test stand precisely inside
// the window and drive a SIGINT into it, every run, on any machine.
//
// # Why not internal/crashpoint
//
// The shape is copied from crashpoint deliberately (named point, runtime
// Arm returning a disarm closure, an atomic-gated no-op when unarmed, and
// ALWAYS COMPILED rather than build-tagged so a refactor breaks the build
// instead of bit-rotting). The verb had to differ on two counts:
//
//   - crashpoint.At PANICS. This seam needs the opposite: run the test's
//     code at a precise instruction and then let execution CONTINUE, so
//     the rest of the window can be observed.
//   - crashpoint.RegisteredNames feeds the #94 R4 mechanical coverage
//     gate, which fails the suite for a registered point that has no
//     crash scenario. A shell rendezvous registered there would break
//     that gate for a point that models no crash at all.
//
// # Production cost
//
// Unarmed — always, since nothing but a test calls ArmHook — at() is one
// atomic.Bool load. No map lookup, no lock, no allocation.

// HookChildStarting fires inside Run after the interrupt claim is
// registered and immediately BEFORE cmd.Start(). An armed hook therefore
// runs with the command committed and no process group in existence: the
// window an interrupt must survive.
const HookChildStarting = "shell.Run.childStarting"

var (
	hookMu sync.Mutex
	hooks  = map[string]func(){}
	// anyHook is the fast-path gate: false ⇒ nothing is armed, so at()
	// returns without taking hookMu. Written under hookMu, read locklessly.
	anyHook atomic.Bool
)

// ArmHook installs fn at the named synchronization point and returns a
// disarm closure. Test-only by convention — nothing in production arms a
// point. A test should defer the disarm so an arm never leaks into a
// sibling test.
func ArmHook(name string, fn func()) (disarm func()) {
	hookMu.Lock()
	hooks[name] = fn
	anyHook.Store(true)
	hookMu.Unlock()
	return func() {
		hookMu.Lock()
		delete(hooks, name)
		anyHook.Store(len(hooks) > 0)
		hookMu.Unlock()
	}
}

// at runs the hook armed at name, if any. The hook is looked up under the
// lock and called WITHOUT it, so a hook may re-enter this package (the
// interrupt-window test calls straight back into Runner.Interrupt).
func at(name string) {
	if !anyHook.Load() {
		return
	}
	hookMu.Lock()
	fn := hooks[name]
	hookMu.Unlock()
	if fn != nil {
		fn()
	}
}
