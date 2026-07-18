package scenarios

import (
	"context"
	"fmt"
	"testing"

	"personant/internal/crashpoint"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/recovery"
	"personant/internal/store"
	"personant/internal/turn"
)

// RestartWithCrash is the crash-injection sibling of the clean-relaunch
// model (Step.RestartSession + turn.LoadSession, whose congruence
// AssertSessionRestored checks). It arms a named crashpoint, runs drive —
// which MUST reach that point and panic with a *crashpoint.Crash — then
// models a hard process kill + relaunch: it discards every in-memory
// handle, constructs a FRESH adapter over the same on-disk home, and drives
// its Reconcile against whatever state the crash left. The returned
// RecoveryReport is what the crash matrix asserts on (via AssertPostRecovery).
//
// opts thread crashpoint.ArmOnHit for a point crossed several times per
// operation — e.g. the kth turn.betweenCanonicalRenames. A drive that does
// NOT fire the armed point fails the test: an un-fired crashpoint means the
// scenario no longer reaches the window it claims, which is exactly the
// silent-rot the mechanical coverage gate exists to catch.
//
// It is the convergent-path helper: Reconcile completing is the expected
// outcome, so a Reconcile error is fatal. The two persistent-verify-failure
// fixtures (§2.7 (i)/(j)) whose designed terminal is refuse-to-open drive
// Reconcile directly and assert the error themselves.
func RestartWithCrash(t testing.TB, paths store.PersonantPaths, point string, drive func(), opts ...crashpoint.ArmOption) memops.RecoveryReport {
	t.Helper()
	func() {
		disarm := crashpoint.Arm(point, opts...)
		defer disarm()
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("RestartWithCrash: armed crashpoint %q did not fire — the drive no longer reaches that window", point)
			}
			if _, ok := r.(*crashpoint.Crash); !ok {
				panic(r) // a real bug, never swallow it as if it were the injected kill
			}
		}()
		drive()
	}()
	// Relaunch: a fresh adapter over the same home, nothing carried across
	// the (simulated) process boundary.
	a := fileadapter.NewFileAdapter(paths)
	rep, err := a.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("RestartWithCrash: Reconcile after crash at %q: %v", point, err)
	}
	return rep
}

// TurnOracle is a pre-crash canonical snapshot used to bound structural
// loss to ≤1 turn across a crash+recovery (the design's durability bar).
type TurnOracle struct {
	// SpineLen is the spine record count at snapshot time.
	SpineLen int
}

// SnapshotTurnOracle captures the pre-crash canonical state a crash matrix
// scenario measures loss against. Cheap: one spine read.
func SnapshotTurnOracle(t testing.TB, paths store.PersonantPaths) TurnOracle {
	t.Helper()
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("SnapshotTurnOracle: read spine: %v", err)
	}
	return TurnOracle{SpineLen: len(recs)}
}

// AssertPostRecovery is the shared post-recovery assertion suite every crash
// matrix scenario runs. It asserts, on the on-disk home after recovery:
//
//   - the substrate invariant set — spine integrity, project references,
//     archive resolvability, dedup consistency, and the STRENGTHENED
//     VerifyThreadMetaMatchesSpine (spine↔frontmatter turn_count/state
//     agreement, the torn-turn signature the id/project-only check was blind
//     to);
//   - idempotence: further independent reconciles converge to the clean
//     terminal (cell 1) without re-running destructive recovery — which is
//     the load-bearing derived assertion for an intra-day recovery, since
//     clean-open certifies daily-WT-clean + watermark-match;
//   - if oracle != nil, the ≤1-turn structural-loss bound: the spine grew
//     or shrank by at most one record across the crash (a torn turn is
//     rolled back whole; a committed turn survives whole).
//
// It deliberately does NOT run VerifyIndexFresh (a rebuild-and-compare):
// per-turn commits do not rebuild derived (that is the once-a-day barrier's
// job, R3b §1.1), so intra-day symbols.jsonl is legitimately stale-vs-spine
// but clean-vs-HEAD; forcing a rebuild here would dirty the worktree and
// mis-read a correctly-recovered home as a cell-3 hand-edit. Post-barrier
// derived-freshness is asserted by the barrier fixtures' assertBarrierTerminal.
//
// It reuses the invariant functions verbatim (single source of truth) via a
// minimal validator Harness bound to the recovered paths.
func AssertPostRecovery(t testing.TB, paths store.PersonantPaths, ops memops.MemoryOps, oracle *TurnOracle) {
	t.Helper()
	h := &Harness{T: t, Paths: paths, Ops: ops}
	invariants := []struct {
		name string
		fn   InvariantCheck
	}{
		{"VerifySpineIntegrity", VerifySpineIntegrity},
		{"VerifyProjectReferences", VerifyProjectReferences},
		{"VerifyThreadMetaMatchesSpine", VerifyThreadMetaMatchesSpine},
		{"VerifyArchiveResolvable", VerifyArchiveResolvable},
		{"VerifyDedupConsistency", VerifyDedupConsistency},
	}
	for _, inv := range invariants {
		if err := inv.fn(h); err != nil {
			t.Errorf("AssertPostRecovery: %s: %v", inv.name, err)
		}
	}

	// Idempotence / convergence: further independent reconciles must never
	// re-run recovery of the crash (no reset, no pending completion, and none
	// of the marker/torn/adopt cells that mean an unrecovered in-flight op),
	// and must settle to a STABLE, non-destructive fixed point.
	//
	// The fixed point is cell-1 (clean) for a torn turn rolled back to daily
	// HEAD, or a repeating cell-3 for a committed-turn crash: the turn path
	// never stamps the derived watermark (that is the once-a-day barrier's
	// job, R3b §1.1), so a committed-turn restart re-derives on a stale
	// watermark (cell-2) and leaves the freshly-rebuilt-but-uncommitted
	// derived as a markerless-dirty worktree — which cell-3 NEVER resets and
	// absorbs at the next full sweep. A repeating cell-3 is therefore a
	// correct settled state, not a recovery failure. What must hold is
	// non-destructiveness + stability (two identical consecutive passes).
	forbidden := map[string]bool{
		recovery.Cell4TornTurn: true, recovery.Cell5TurnCommitted: true,
		recovery.Cell6TurnNoWrites: true, recovery.Cell8Archival: true,
		recovery.Cell9StampRepair: true, recovery.Cell11Reentry: true,
		recovery.Cell12LegacyAdopt: true, recovery.CellBarrier: true,
	}
	const maxPasses = 4
	var prevCells []string
	stable := false
	for i := 0; i < maxPasses; i++ {
		rep, err := fileadapter.NewFileAdapter(paths).Reconcile(context.Background())
		if err != nil {
			t.Fatalf("AssertPostRecovery: convergence Reconcile %d: %v", i+1, err)
		}
		if rep.ResetPerformed {
			t.Errorf("AssertPostRecovery: convergence Reconcile %d reset %v — recovery not idempotent",
				i+1, rep.RevertedPaths)
		}
		if rep.Pending != nil {
			t.Errorf("AssertPostRecovery: convergence Reconcile %d left a pending completion — unsettled", i+1)
		}
		for _, c := range rep.CellsHit {
			if forbidden[c] {
				t.Errorf("AssertPostRecovery: convergence Reconcile %d re-entered unrecovered cell %q — recovery not idempotent",
					i+1, c)
			}
		}
		if prevCells != nil && equalStringSlices(prevCells, rep.CellsHit) {
			stable = true
			break
		}
		prevCells = rep.CellsHit
	}
	if !stable {
		t.Errorf("AssertPostRecovery: home did not reach a stable reconcile classification within %d passes (last: %v)",
			maxPasses, prevCells)
	}

	if oracle != nil {
		recs, rerr := store.ReadSpine(paths.Spine)
		if rerr != nil {
			t.Fatalf("AssertPostRecovery: read spine for loss bound: %v", rerr)
		}
		if delta := len(recs) - oracle.SpineLen; delta < -1 || delta > 1 {
			t.Errorf("AssertPostRecovery: spine changed by %d records across the crash (was %d, now %d) — exceeds the ≤1-turn structural-loss bar",
				delta, oracle.SpineLen, len(recs))
		}
	}
}

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
