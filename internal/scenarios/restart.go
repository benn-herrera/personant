package scenarios

import (
	"fmt"

	"personant/internal/turn"
)

// AssertSessionRestored is the canonical congruence check for a simulated
// application shutdown→relaunch: it verifies that a turn.State rebuilt
// from the substrate (via turn.LoadSession) reproduces the should-survive
// subset of the pre-shutdown State.
//
// This is the SINGLE SOURCE OF TRUTH for "what must be congruent between
// a pre-shutdown State and its reconstruction." A new persisted
// session-state field belongs in exactly one place on the check side:
// here. Do not open-code an equivalent comparison elsewhere.
//
// The should-survive subset:
//
//   - ActiveThreads — Layer B membership, equal as ordered slices.
//   - DormantThreads — Layer C membership, equal as ordered slices.
//   - ActiveProject.ID — the active project, equal.
//
// What it deliberately does NOT check: TurnNumber, the coalesce/staging
// buffers, and closureDeferUntil are session-volatile — they legitimately
// reset on restart and asserting on them would be wrong.
//
// On a mismatch it returns a descriptive error naming the differing field
// and both values; on full congruence it returns nil. The error is
// non-nil so callers can route it through t.Errorf (collecting every
// problem) rather than halting.
func AssertSessionRestored(before, after *turn.State) error {
	if before == nil || after == nil {
		return fmt.Errorf("AssertSessionRestored: nil State (before=%v after=%v)", before == nil, after == nil)
	}
	if !equalStringSlices(before.ActiveThreads, after.ActiveThreads) {
		return fmt.Errorf("AssertSessionRestored: ActiveThreads not restored: before=%v after=%v",
			before.ActiveThreads, after.ActiveThreads)
	}
	if !equalStringSlices(before.DormantThreads, after.DormantThreads) {
		return fmt.Errorf("AssertSessionRestored: DormantThreads not restored: before=%v after=%v",
			before.DormantThreads, after.DormantThreads)
	}
	if before.ActiveProject.ID != after.ActiveProject.ID {
		return fmt.Errorf("AssertSessionRestored: ActiveProject.ID not restored: before=%q after=%q",
			before.ActiveProject.ID, after.ActiveProject.ID)
	}
	return nil
}

// equalStringSlices reports whether two string slices are equal as
// ordered sequences. A nil slice and an empty slice compare equal — a
// fresh-launch working set may be reloaded as either.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
