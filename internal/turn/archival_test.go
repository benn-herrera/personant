package turn

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// readArchivalEventLog returns the concatenated contents of every *.log
// file under the home's LogsDir.
func readArchivalEventLog(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

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
		Frontmatter: memops.ThreadMeta{
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
	if err := store.SeedThread(paths, thr); err != nil {
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

// TestSurfaceArchival_FiresAtTurnClose drives the trigger through a real
// turn via RunWithDeltas — not by calling surfaceArchivalCandidates
// directly — proving the §3.8 archival scan genuinely fires at turn close
// (step 5c) even on a no-engagement turn (no topic tag → closeTurn early
// returns), so the trigger placement outside that early return is locked
// against regression.
func TestSurfaceArchival_FiresAtTurnClose(t *testing.T) {
	paths, meta := newTestHome(t)

	// Seed a spine over the high-water mark, all retired so the whole
	// over-budget surplus is archival-eligible.
	const total = archiveHighWater + 30
	for rank := 0; rank < total; rank++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", rank+1),
			memops.ThreadResolved, monotonicTS(rank))
	}

	// A response with NO topic tag: closeTurnAndUpdateEngagement sees an
	// empty coalesce buffer and early-returns. Archival (step 5c) must
	// still fire because it sits outside that early return.
	mock := model.NewScriptedMock([]model.Response{
		{Content: "A plain reply with no topic tag."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := RunWithDeltas(context.Background(), state, nil, "hello", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}

	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine after turn: %v", err)
	}
	if len(recs) != archiveLowWater {
		t.Errorf("spine count after turn = %d, want %d (archival fired at turn close)",
			len(recs), archiveLowWater)
	}
}

// TestSurfaceArchival_UnderDrain seeds a spine over the high-water mark
// whose retired threads are fewer than the drain target. Archival cannot
// reach low-water — it settles ABOVE archiveLowWater — and emits an
// archive.under-drain log line carrying the standing-pressure detail.
func TestSurfaceArchival_UnderDrain(t *testing.T) {
	paths, meta := newTestHome(t)

	// Over high-water, but mostly live threads. target = total -
	// archiveLowWater; retiredCount is deliberately smaller than target.
	const total = archiveHighWater + 40    // target = 90
	const retiredCount = 20                // < target
	const liveCount = total - retiredCount // 220

	liveStates := []memops.ThreadState{memops.ThreadActive, memops.ThreadWIP}
	for i := 0; i < liveCount; i++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", i+1),
			liveStates[i%2], "2026-01-15T00:00:00Z")
	}
	for j := 0; j < retiredCount; j++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", liveCount+1+j),
			memops.ThreadResolved, monotonicTS(j))
	}

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
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
	// All retired threads archived; the spine settles above low-water.
	wantSpine := total - retiredCount
	if len(recs) != wantSpine {
		t.Errorf("spine count after under-drain = %d, want %d", len(recs), wantSpine)
	}
	if len(recs) <= archiveLowWater {
		t.Errorf("spine drained to %d <= low-water %d; under-drain expected to settle ABOVE low-water",
			len(recs), archiveLowWater)
	}

	log := readArchivalEventLog(t, paths)
	if !strings.Contains(log, "archive.under-drain") {
		t.Errorf("event log missing archive.under-drain line\n%s", log)
	}
}
