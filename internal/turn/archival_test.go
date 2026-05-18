package turn

import (
	"context"
	"fmt"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// seedArchivalThread writes a thread + spine record with an explicit
// state and a state_changed timestamp used to order coldest-first. IDs
// must be canonical thr_<n> — the derived-index builder skips any file
// whose name does not match that pattern, which would silently hollow
// out the post-archival drift check.
func seedArchivalThread(t *testing.T, paths store.PersonantPaths, project, thrID string, state memops.ThreadState, stateChanged string) {
	t.Helper()
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	rec := memops.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      anchors,
		Summary:      thrID,
		State:        state,
		Created:      "2026-01-01T00:00:00Z",
		LastEngaged:  stateChanged,
		StateChanged: stateChanged,
		TurnCount:    1,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", thrID, err)
	}
	thr := memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID:           rec.ID,
			Project:      rec.Project,
			Anchors:      rec.Anchors,
			Summary:      rec.Summary,
			State:        rec.State,
			Created:      rec.Created,
			LastEngaged:  rec.LastEngaged,
			StateChanged: rec.StateChanged,
			TurnCount:    rec.TurnCount,
		},
		Body: "# " + thrID + "\n\nbody\n",
	}
	if err := store.SaveThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// monotonicTS returns a minute-grained RFC3339 timestamp ordered by rank
// — rank 0 is the coldest (earliest) value.
func monotonicTS(rank int) string {
	return fmt.Sprintf("2026-03-%02dT%02d:%02d:00Z", (rank/1440)+1, (rank/60)%24, rank%60)
}

// TestSurfaceArchival_WatermarkDrainsColdestRetired seeds a spine over
// the high-water mark and asserts the cardinality trigger archives the
// coldest retired threads down to the low-water mark, leaves live
// threads untouched, and produces a derived index with no drift.
func TestSurfaceArchival_WatermarkDrainsColdestRetired(t *testing.T) {
	paths, meta := newTestHome(t)

	const liveCount = 40
	const retiredCount = 180
	const total = liveCount + retiredCount

	// Live threads thr_1..thr_40 (active/wip) — never archival-eligible.
	// Their state_changed timestamps are deliberately OLDER than every
	// retired thread, so a state-blind coldest policy would wrongly pick
	// them; the retired-only filter must exclude them.
	liveStates := []memops.ThreadState{memops.ThreadActive, memops.ThreadWIP}
	for i := 0; i < liveCount; i++ {
		id := fmt.Sprintf("thr_%d", i+1)
		seedArchivalThread(t, paths, meta.ID, id, liveStates[i%2], "2026-01-15T00:00:00Z")
	}
	// Retired threads thr_41..thr_220. retiredRank(j) gives thr_(41+j) a
	// coldness rank of j, so thr_41 is coldest and thr_220 warmest.
	retiredStates := []memops.ThreadState{
		memops.ThreadResolved, memops.ThreadDecided, memops.ThreadAbandoned,
	}
	retiredID := func(rank int) string { return fmt.Sprintf("thr_%d", liveCount+1+rank) }
	for rank := 0; rank < retiredCount; rank++ {
		seedArchivalThread(t, paths, meta.ID, retiredID(rank),
			retiredStates[rank%3], monotonicTS(rank))
	}

	if recs, err := store.ReadSpine(paths.Spine); err != nil {
		t.Fatalf("read spine: %v", err)
	} else if len(recs) != total {
		t.Fatalf("seeded spine count = %d, want %d", len(recs), total)
	}

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))

	// Materialize the derived index so the post-archival drift check is
	// meaningful (a regenerate-then-check on a never-built index would
	// trivially agree).
	if err := state.Ops.RegenerateDerivedState(context.Background(), memops.IndexBuildOptions{Quiet: true}); err != nil {
		t.Fatalf("seed RegenerateDerivedState: %v", err)
	}

	if err := surfaceArchivalCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceArchivalCandidates: %v", err)
	}

	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine after archival: %v", err)
	}
	if len(recs) != archiveLowWater {
		t.Errorf("spine count after archival = %d, want %d (low-water)", len(recs), archiveLowWater)
	}

	survivors := make(map[string]bool, len(recs))
	for _, rec := range recs {
		survivors[rec.ID] = true
	}

	// No live thread may be archived, despite their old timestamps.
	for i := 0; i < liveCount; i++ {
		id := fmt.Sprintf("thr_%d", i+1)
		if !survivors[id] {
			t.Errorf("live thread %s was archived — only retired threads are eligible", id)
		}
	}

	// archivedCount coldest retired threads gone; the rest survive.
	archivedCount := total - archiveLowWater
	for rank := 0; rank < archivedCount; rank++ {
		if survivors[retiredID(rank)] {
			t.Errorf("coldest retired thread %s (rank %d) should have been archived", retiredID(rank), rank)
		}
	}
	for rank := archivedCount; rank < retiredCount; rank++ {
		if !survivors[retiredID(rank)] {
			t.Errorf("warmer retired thread %s (rank %d) should have survived", retiredID(rank), rank)
		}
	}

	// The batch regenerates the derived index once; it must show no drift.
	check, err := state.Ops.CheckDerivedState(context.Background(), memops.IndexBuildOptions{Quiet: true})
	if err != nil {
		t.Fatalf("CheckDerivedState: %v", err)
	}
	if !check.OK() {
		t.Errorf("derived index has drift after archival: %+v", check.Drifts)
	}
}

// TestSurfaceArchival_NoOpBelowHighWater — a spine below the high-water
// mark triggers no archival.
func TestSurfaceArchival_NoOpBelowHighWater(t *testing.T) {
	paths, meta := newTestHome(t)
	for i := 0; i < 50; i++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", i+1),
			memops.ThreadResolved, monotonicTS(i))
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))

	if err := surfaceArchivalCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceArchivalCandidates: %v", err)
	}
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(recs) != 50 {
		t.Errorf("spine count = %d, want 50 (no archival below high-water)", len(recs))
	}
}
