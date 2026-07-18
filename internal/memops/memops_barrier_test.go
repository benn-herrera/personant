package memops

import (
	"fmt"
	"reflect"
	"testing"
)

func policyRec(id string, state ThreadState, stateChanged string) SpineRecord {
	return SpineRecord{ID: id, State: state, StateChanged: stateChanged}
}

func policyTS(rank int) string {
	return fmt.Sprintf("2026-03-%02dT%02d:%02d:00Z", (rank/1440)+1, (rank/60)%24, rank%60)
}

// TestSelectArchivalCandidates_BelowHighWater: at or under the high-water
// mark the policy selects nothing.
func TestSelectArchivalCandidates_BelowHighWater(t *testing.T) {
	recs := make([]SpineRecord, 0, ArchiveHighWater)
	for i := 0; i < ArchiveHighWater; i++ {
		recs = append(recs, policyRec(fmt.Sprintf("thr_%d", i+1), ThreadResolved, policyTS(i)))
	}
	if got := SelectArchivalCandidates(recs); got != nil {
		t.Errorf("SelectArchivalCandidates at high-water = %v, want nil", got)
	}
}

// TestSelectArchivalCandidates_ColdestRetiredOnly: over pressure, the
// coldest RETIRED threads are selected down to low-water; live threads
// are never selected even with older timestamps.
func TestSelectArchivalCandidates_ColdestRetiredOnly(t *testing.T) {
	const liveCount = 40
	const retiredCount = 180
	var recs []SpineRecord
	liveStates := []ThreadState{ThreadActive, ThreadWIP, ThreadPaused, ThreadBlocked}
	for i := 0; i < liveCount; i++ {
		// Deliberately the OLDEST timestamps: a state-blind policy would pick these.
		recs = append(recs, policyRec(fmt.Sprintf("live_%d", i+1), liveStates[i%4], "2026-01-15T00:00:00Z"))
	}
	retiredStates := []ThreadState{ThreadResolved, ThreadDecided, ThreadAbandoned}
	for rank := 0; rank < retiredCount; rank++ {
		recs = append(recs, policyRec(fmt.Sprintf("thr_%d", rank+1), retiredStates[rank%3], policyTS(rank)))
	}

	got := SelectArchivalCandidates(recs)
	wantN := len(recs) - ArchiveLowWater
	if len(got) != wantN {
		t.Fatalf("selected %d, want %d (drain to low-water)", len(got), wantN)
	}
	want := make([]string, 0, wantN)
	for rank := 0; rank < wantN; rank++ {
		want = append(want, fmt.Sprintf("thr_%d", rank+1)) // coldest-first by rank
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("selection = %v, want coldest-first %v", got[:5], want[:5])
	}
}

// TestSelectArchivalCandidates_UnderDrain: fewer retired threads than
// the drain target → every retired thread is selected, nothing more.
func TestSelectArchivalCandidates_UnderDrain(t *testing.T) {
	const total = ArchiveHighWater + 40 // target = 90
	const retiredCount = 20             // < target
	var recs []SpineRecord
	for i := 0; i < total-retiredCount; i++ {
		recs = append(recs, policyRec(fmt.Sprintf("live_%d", i+1), ThreadActive, "2026-01-15T00:00:00Z"))
	}
	for j := 0; j < retiredCount; j++ {
		recs = append(recs, policyRec(fmt.Sprintf("thr_%d", j+1), ThreadResolved, policyTS(j)))
	}
	got := SelectArchivalCandidates(recs)
	if len(got) != retiredCount {
		t.Errorf("selected %d, want all %d retired (under-drain)", len(got), retiredCount)
	}
}

// TestSelectArchivalCandidates_UnparseableStampSortsLast: a corrupt
// timestamp must never make a thread PREFERENTIALLY archived.
func TestSelectArchivalCandidates_UnparseableStampSortsLast(t *testing.T) {
	var recs []SpineRecord
	// One corrupt-stamp retired thread + enough parseable ones that only
	// part of the retired set is selected.
	recs = append(recs, policyRec("thr_corrupt", ThreadResolved, "not-a-timestamp"))
	for i := 0; i < ArchiveHighWater+10; i++ {
		recs = append(recs, policyRec(fmt.Sprintf("thr_%d", i+1), ThreadResolved, policyTS(i)))
	}
	got := SelectArchivalCandidates(recs)
	for _, id := range got {
		if id == "thr_corrupt" {
			t.Error("corrupt-stamp thread selected while parseable colder threads exist (must sort LAST)")
		}
	}
}

// TestSelectArchivalCandidates_LastEngagedFallback: an empty
// StateChanged falls back to LastEngaged for the coldness key.
func TestSelectArchivalCandidates_LastEngagedFallback(t *testing.T) {
	var recs []SpineRecord
	old := SpineRecord{ID: "thr_old", State: ThreadResolved, LastEngaged: "2025-01-01T00:00:00Z"}
	recs = append(recs, old)
	for i := 0; i < ArchiveHighWater+1; i++ {
		recs = append(recs, policyRec(fmt.Sprintf("thr_%d", i+1), ThreadResolved, policyTS(i)))
	}
	got := SelectArchivalCandidates(recs)
	if len(got) == 0 || got[0] != "thr_old" {
		t.Errorf("selection head = %v, want thr_old first (LastEngaged fallback makes it coldest)", got[:min(3, len(got))])
	}
}
