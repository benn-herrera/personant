package memops

import (
	"sort"
	"time"
)

// Day-barrier port types + the §3.8 archival-pressure policy
// (#94 R3b). The policy moved here from internal/turn when the drain
// was re-sequenced to barrier-only (B1): the barrier is adapter-homed,
// and the selection is pure domain logic over spine records, so it
// belongs beside the domain model — callable by any adapter, testable
// without a substrate.

// DayBarrierResult reports what a MaybeDayBarrier poll did. A no-op
// poll (no completed day pending) returns the zero value.
type DayBarrierResult struct {
	// DaysSealed are the day indices the poll sealed (one barrier each),
	// in ascending order. Empty on the no-op poll.
	DaysSealed []int
	// ArchivedThreads are the thread ids the sealed barriers' B1
	// archival drains removed from the live spine — the caller evicts
	// them from any in-memory working set (the P2-1 phantom guard).
	ArchivedThreads []string
	// Duration gauges (§6.3), summed across the sealed barriers.
	BarrierDuration     time.Duration // B0→B6 wall time
	DayCommitDuration   time.Duration // B2 portion
	MorningInitDuration time.Duration // B5+B6 daily re-init portion
	// DayCommitBytes is the primary object-store growth across the
	// sealed barriers (the day_commit_bytes gauge).
	DayCommitBytes int64
	// DailyLooseObjects is the daily DB's loose-object count sampled
	// just before the barrier nuked the day's accrual — the §6.2
	// re-baseline-knob observable (daily_loose_object_count gauge).
	DailyLooseObjects int
}

// Archival cardinality-pressure watermarks (§3.8). When the total spine
// record count exceeds ArchiveHighWater, the coldest retired threads
// are drained (at the day barrier's B1) until the count drops to
// ArchiveLowWater. SEED values, to be calibrated on the rung walk.
const (
	ArchiveHighWater = 200
	ArchiveLowWater  = 150
)

// SelectArchivalCandidates applies the §3.8 pressure policy to a full
// spine snapshot: nil when the spine is at or under ArchiveHighWater;
// otherwise the coldest retired threads (state ∈ {resolved, decided,
// abandoned} only — never active/wip/paused/blocked), oldest
// StateChanged first (LastEngaged fallback), enough to drain the spine
// to ArchiveLowWater or every retired thread, whichever is fewer.
//
// Ordering is by PARSED time, not lexical compare — RFC3339 stamps at
// different UTC offsets sort wrong lexically. An unparseable/empty
// stamp sorts LAST (treated as newest → never preferentially archived
// on a corrupt timestamp).
func SelectArchivalCandidates(recs []SpineRecord) []string {
	if len(recs) <= ArchiveHighWater {
		return nil
	}
	var retired []SpineRecord
	for _, rec := range recs {
		if IsRetiredState(rec.State) {
			retired = append(retired, rec)
		}
	}
	sort.SliceStable(retired, func(i, j int) bool {
		ti, oki := coldnessKey(retired[i])
		tj, okj := coldnessKey(retired[j])
		if oki != okj {
			return oki // parseable stamp sorts before an unparseable one
		}
		if !oki {
			return false // both unparseable: hold input order (stable sort)
		}
		return ti.Before(tj)
	})
	target := len(recs) - ArchiveLowWater
	if target > len(retired) {
		target = len(retired)
	}
	ids := make([]string, 0, target)
	for _, rec := range retired[:target] {
		ids = append(ids, rec.ID)
	}
	return ids
}

// IsRetiredState reports whether a thread state is archival-eligible.
// Only resolved / decided / abandoned threads — the §3.5 retire-class
// states — may be archived; active/wip/paused/blocked are live and stay
// on the spine.
func IsRetiredState(s ThreadState) bool {
	switch s {
	case ThreadResolved, ThreadDecided, ThreadAbandoned:
		return true
	default:
		return false
	}
}

// coldnessKey returns the parsed timestamp used to order retired
// threads coldest-first, and whether it was parseable. StateChanged is
// the primary signal (when the thread was retired); LastEngaged is the
// fallback when StateChanged is empty.
func coldnessKey(rec SpineRecord) (time.Time, bool) {
	stamp := rec.StateChanged
	if stamp == "" {
		stamp = rec.LastEngaged
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
