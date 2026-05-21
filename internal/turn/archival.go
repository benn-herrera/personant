package turn

import (
	"context"
	"fmt"
	"sort"

	"personant/internal/memops"
)

// archiveHighWater / archiveLowWater are the §3.8 cardinality-pressure
// watermarks. When the total spine record count exceeds archiveHighWater,
// the coldest retired threads are archived until the count drops to
// archiveLowWater.
//
// These are SEED values, to be calibrated on the rung walk. At 1-day
// simulation scale the spine is ~80–90 threads — below archiveHighWater —
// so archival does not fire there; it first fires on the longer rungs.
// That is intended.
const (
	archiveHighWater = 200 // seed: archival arms above this spine count
	archiveLowWater  = 150 // seed: archival drains the spine down to this
)

// surfaceArchivalCandidates runs the §3.8 cardinality-pressure archival
// scan at turn close. It is a sibling of surfaceClosureCandidates and runs
// on every turn, after closure.
//
// Policy: when the total spine record count exceeds archiveHighWater,
// archive the coldest retired threads — state ∈ {resolved, decided,
// abandoned} only, never active/wip/paused/blocked — until the count drops
// to archiveLowWater. "Coldest" is oldest by StateChanged (falling back to
// LastEngaged when StateChanged is empty).
//
// Scope: archival is GLOBAL — the ThreadFilter{} below lists the entire
// spine across every project, deliberately diverging from
// surfaceClosureCandidates, which scopes to state.ActiveProject.ID. The
// divergence is intentional. Archival relieves SPINE CARDINALITY pressure,
// and per ARCHITECTURE.md the spine is one unified store across all
// projects, so the cardinality budget is genuinely substrate-wide, not
// per-project. And archival only ever touches ALREADY-RETIRED threads, so
// the hazard that makes closure project-scoped — disturbing a sibling
// project's live work — does not apply here. A global drain reaching into
// a dormant project's retired threads is the correct behavior, not a leak.
//
// After the batch, the derived index is regenerated ONCE: each archival
// delete leaves symbols.jsonl with dangling references. (A failed regen is
// NOT caught by a scenario invariant — see the derivedIndexStale handling
// below, which forces a retry on the next turn.)
//
// Opportunistic and non-fatal: an ArchiveThread or regenerate error is
// logged and swallowed, never aborting the turn (closure is the precedent).
//
// Cross-turn ordering: archival is coldest-first, so a thread retired by
// closure earlier THIS turn (step 5b) has the NEWEST StateChanged and is
// therefore the LAST archival candidate. A same-turn retire+archive of the
// same thread effectively cannot happen — it would require every older
// retired thread to have already been drained, which contradicts the
// thread being freshly retired. No minimum-age guard is needed.
func surfaceArchivalCandidates(ctx context.Context, state *State) error {
	recs, err := state.Ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return fmt.Errorf("archival: list threads: %w", err)
	}
	if len(recs) <= archiveHighWater {
		return nil
	}

	// Collect retired threads — the only archival-eligible class.
	var retired []memops.SpineRecord
	for _, rec := range recs {
		if isRetiredState(rec.State) {
			retired = append(retired, rec)
		}
	}

	// Coldest first: oldest StateChanged, falling back to LastEngaged.
	sort.SliceStable(retired, func(i, j int) bool {
		return coldnessKey(retired[i]) < coldnessKey(retired[j])
	})

	// Archive coldest retired threads until the spine drains to the
	// low-water mark (or we run out of retired threads).
	target := len(recs) - archiveLowWater
	archived := 0
	for _, rec := range retired {
		if archived >= target {
			break
		}
		if err := state.Ops.ArchiveThread(ctx, rec.ID); err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryArchive, "error",
				"thr="+rec.ID+" err="+memops.SanitizeDetail(err.Error()))
			continue
		}
		archived++
	}

	// Under-drain: the spine was over the high-water mark but retired
	// threads were too scarce to drain to low-water — it settles ABOVE
	// archiveLowWater. This is correct (live threads cannot be archived)
	// but is a standing cardinality-pressure condition archival cannot
	// relieve, exactly what the six-month sim must detect, so it gets a
	// distinct log line.
	if archived < target {
		_ = state.Ops.Log(ctx, memops.LogCategoryArchive, "under-drain",
			fmt.Sprintf("spine=%d low-water=%d wanted=%d archived=%d retired-exhausted",
				len(recs)-archived, archiveLowWater, target, archived))
	}

	// Regenerate the derived index if this batch archived anything OR a
	// prior turn's regen failed (derivedIndexStale). Each archival delete
	// leaves symbols.jsonl with dangling references; a clean regen clears
	// the staleness, a failed one re-arms it so the next turn retries even
	// when it archives nothing.
	if archived == 0 && !state.derivedIndexStale {
		return nil
	}
	if err := state.Ops.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		state.derivedIndexStale = true
		_ = state.Ops.Log(ctx, memops.LogCategoryArchive, "regen-error", memops.SanitizeDetail(err.Error()))
		return nil
	}
	state.derivedIndexStale = false
	return nil
}

// isRetiredState reports whether a thread state is archival-eligible.
// Only resolved / decided / abandoned threads — the §3.5 retire-class
// states — may be archived; active/wip/paused/blocked are live and stay
// on the spine.
func isRetiredState(s memops.ThreadState) bool {
	switch s {
	case memops.ThreadResolved, memops.ThreadDecided, memops.ThreadAbandoned:
		return true
	default:
		return false
	}
}

// coldnessKey returns the timestamp string used to order retired threads
// coldest-first. StateChanged is the primary signal (when a thread was
// retired); LastEngaged is the fallback when StateChanged is empty.
func coldnessKey(rec memops.SpineRecord) string {
	if rec.StateChanged != "" {
		return rec.StateChanged
	}
	return rec.LastEngaged
}
