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
// After the batch, the derived index is regenerated ONCE: each archival
// delete leaves symbols.jsonl with dangling references, and a scenario
// invariant would otherwise flag the drift.
//
// Opportunistic and non-fatal: an ArchiveThread or regenerate error is
// logged and swallowed, never aborting the turn (closure is the precedent).
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
			_ = state.Ops.Log(ctx, "archive", "error",
				"thr="+rec.ID+" err="+sanitizeDetail(err.Error()))
			continue
		}
		archived++
	}
	if archived == 0 {
		return nil
	}

	// Regenerate the derived index ONCE after the batch: each delete left
	// symbols.jsonl with dangling references until now.
	if err := state.Ops.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		_ = state.Ops.Log(ctx, "archive", "regen-error", sanitizeDetail(err.Error()))
	}
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
