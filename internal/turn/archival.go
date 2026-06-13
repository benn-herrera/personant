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
// The coldest-first retired ids are gathered into one slice and handed to
// ArchiveThreads in a SINGLE call (design §4 batched cost): one spine
// rewrite + bounded commits per drain, not O(K). The adapter regenerates
// the derived index INSIDE that batch, atomically with the deletion commit
// — the drain no longer regenerates separately (design §8: "REMOVE the
// drain's separate regen").
//
// Atomicity: ArchiveThreads is atomic-per-call — its pre-flag runs on the
// CAPTURE commit BEFORE any spine/disk mutation, so a failed batch leaves
// the spine and worktree intact. The drain can therefore swallow the error
// (opportunistic, non-fatal) without risking silent thread loss.
//
// Opportunistic and non-fatal: an ArchiveThreads error is logged and
// swallowed, never aborting the turn (closure is the precedent).
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

	// Gather the coldest retired ids until we have enough to drain the
	// spine to the low-water mark (or we run out of retired threads).
	target := len(recs) - archiveLowWater
	ids := make([]string, 0, target)
	for _, rec := range retired {
		if len(ids) >= target {
			break
		}
		ids = append(ids, rec.ID)
	}

	// One batched call: spine rewrite + index + derived regen + commit, all
	// atomic per the adapter. A failure here is logged and swallowed (the
	// pre-flag fails before any mutation, so the spine is left intact —
	// nothing is silently lost), and the turn proceeds.
	res, err := state.Ops.ArchiveThreads(ctx, ids)
	if err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryArchive, "error",
			fmt.Sprintf("batch=%d err=%s", len(ids), memops.SanitizeDetail(err.Error())))
		return nil
	}

	// Tally the actual archive outcomes; log any per-thread skip (a spine
	// record with no thread to archive) as a forensic breadcrumb.
	archived := 0
	for _, o := range res.Outcomes {
		if o.Archived {
			archived++
			// P2-1 dead-zone fix: an archived thread is gone from spine+disk,
			// so it must also leave the working-set LRU. Without this, the ID
			// lingers in ActiveThreads/DormantThreads as a phantom that
			// SaveWorkingSet (turn.go step 5d) persists across sessions,
			// pointing at a thread that no longer exists. Mirror the closure
			// path's eviction (closure.go) with the same removeString helper.
			state.ActiveThreads = removeString(state.ActiveThreads, o.ThrID)
			state.DormantThreads = removeString(state.DormantThreads, o.ThrID)
			continue
		}
		if o.Skipped {
			_ = state.Ops.Log(ctx, memops.LogCategoryArchive, "skip",
				"thr="+o.ThrID+" reason="+memops.SanitizeDetail(o.Reason))
		}
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
