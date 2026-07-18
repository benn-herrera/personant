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

// #94 R3b: the §3.8 archival drain is BARRIER-ONLY — it fires inside the
// day barrier's B1, which the turn pipeline triggers via the pre-turn
// MaybeDayBarrier poll (top of RunWithInfo). These tests drive that path
// end-to-end: seed pressure, advance the pinned clock past a day
// boundary, run ONE turn, and assert the drain + working-set eviction.
// The selection policy itself (coldest-retired, watermarks) is unit-
// tested in internal/memops (SelectArchivalCandidates); the barrier's
// crash windows are fixture-tested in internal/memops/fileadapter.

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
		Meta: memops.ThreadMeta{
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

// barrierDay0 / barrierDay1 pin the two sides of a day boundary for the
// pre-turn poll: the home is Init'd (daily born) under day0, and the
// turn runs under day1 — one completed day, one barrier.
var (
	barrierDay0 = time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	barrierDay1 = time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
)

// TestDayBarrier_PreTurnDrainsColdestRetired seeds a spine over the
// high-water mark, crosses a day boundary, and drives ONE tag-less turn.
// The pre-turn MaybeDayBarrier must fire the barrier, whose B1 drains
// the coldest retired threads to the low-water mark — proving the
// trigger placement (before the turn's recovery scope opens) and the
// barrier-homed drain end-to-end. Also asserts the day-commit evidence:
// exactly one day-commit on primary and a reborn daily.
func TestDayBarrier_PreTurnDrainsColdestRetired(t *testing.T) {
	pinClock(t, barrierDay0)
	paths, meta := newTestHome(t)

	const liveCount = 40
	const retiredCount = 180
	const total = liveCount + retiredCount

	// Live threads thr_1..thr_40 (active/wip) — never archival-eligible,
	// despite deliberately OLDER timestamps than every retired thread.
	liveStates := []memops.ThreadState{memops.ThreadActive, memops.ThreadWIP}
	for i := 0; i < liveCount; i++ {
		id := fmt.Sprintf("thr_%d", i+1)
		seedArchivalThread(t, paths, meta.ID, id, liveStates[i%2], "2026-01-15T00:00:00Z")
	}
	retiredStates := []memops.ThreadState{
		memops.ThreadResolved, memops.ThreadDecided, memops.ThreadAbandoned,
	}
	retiredID := func(rank int) string { return fmt.Sprintf("thr_%d", liveCount+1+rank) }
	for rank := 0; rank < retiredCount; rank++ {
		seedArchivalThread(t, paths, meta.ID, retiredID(rank),
			retiredStates[rank%3], monotonicTS(rank))
	}

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock([]model.Response{
		{Content: "A plain reply with no topic tag."},
	}, nil))
	if err := state.Ops.RegenerateDerivedState(context.Background(), memops.IndexBuildOptions{Quiet: true}); err != nil {
		t.Fatalf("seed RegenerateDerivedState: %v", err)
	}

	// Cross the day boundary; the next turn's pre-turn poll fires the
	// barrier for the completed day.
	pinClock(t, barrierDay1)
	if _, err := RunWithDeltas(context.Background(), state, nil, "hello", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}

	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine after barrier: %v", err)
	}
	if len(recs) != memops.ArchiveLowWater {
		t.Errorf("spine count after barrier = %d, want %d (low-water)", len(recs), memops.ArchiveLowWater)
	}
	survivors := make(map[string]bool, len(recs))
	for _, rec := range recs {
		survivors[rec.ID] = true
	}
	for i := 0; i < liveCount; i++ {
		id := fmt.Sprintf("thr_%d", i+1)
		if !survivors[id] {
			t.Errorf("live thread %s was archived — only retired threads are eligible", id)
		}
	}
	archivedCount := total - memops.ArchiveLowWater
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

	// Derived index regenerated inside the batch + barrier: no drift.
	check, err := state.Ops.CheckDerivedState(context.Background(), memops.IndexBuildOptions{Quiet: true})
	if err != nil {
		t.Fatalf("CheckDerivedState: %v", err)
	}
	if !check.OK() {
		t.Errorf("derived index has drift after barrier: %+v", check.Drifts)
	}

	// Barrier evidence in the forensic log.
	log := readArchivalEventLog(t, paths)
	for _, want := range []string{"barrier.begin", "barrier.day-committed", "barrier.reborn", "barrier.complete"} {
		if !strings.Contains(log, want) {
			t.Errorf("event log missing %s line", want)
		}
	}
}

// TestDayBarrier_EvictsFromWorkingSet is the P2-1 dead-zone guard under
// the barrier-homed drain: an archived thread must be removed from the
// working-set LRU (ActiveThreads / DormantThreads) by the pre-turn
// poll's eviction, not just from spine+disk, and the eviction must
// survive a SaveWorkingSet → LoadSession round-trip.
func TestDayBarrier_EvictsFromWorkingSet(t *testing.T) {
	pinClock(t, barrierDay0)
	paths, meta := newTestHome(t)

	const total = memops.ArchiveHighWater + 30
	for rank := 0; rank < total; rank++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", rank+1),
			memops.ThreadResolved, monotonicTS(rank))
	}
	archivedBand := total - memops.ArchiveLowWater
	if archivedBand < 2 {
		t.Fatalf("test premise broken: archived band %d < 2 needed seed ids", archivedBand)
	}
	const archivedActiveID = "thr_1"           // coldest — archived; seeded into ActiveThreads
	const archivedDormantID = "thr_2"          // archived; seeded into DormantThreads
	survivorID := fmt.Sprintf("thr_%d", total) // warmest retired thread, survives

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock([]model.Response{
		{Content: "A plain reply with no topic tag."},
	}, nil))
	state.ActiveThreads = []string{archivedActiveID, survivorID}
	state.DormantThreads = []string{archivedDormantID}

	pinClock(t, barrierDay1)
	if _, err := RunWithDeltas(context.Background(), state, nil, "hello", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}

	assertAbsent := func(label string, list []string, id string) {
		t.Helper()
		for _, v := range list {
			if v == id {
				t.Errorf("%s still contains archived thread %s — phantom entry not evicted", label, id)
			}
		}
	}
	assertPresent := func(label string, list []string, id string) {
		t.Helper()
		for _, v := range list {
			if v == id {
				return
			}
		}
		t.Errorf("%s no longer contains live thread %s — survivor was wrongly evicted", label, id)
	}
	assertAbsent("ActiveThreads", state.ActiveThreads, archivedActiveID)
	assertAbsent("DormantThreads", state.DormantThreads, archivedDormantID)
	assertPresent("ActiveThreads", state.ActiveThreads, survivorID)

	// The turn's own SaveWorkingSet already persisted the evicted lists;
	// reload and assert the phantom does not resurface.
	reloaded, err := LoadSession(context.Background(), fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	assertAbsent("reloaded ActiveThreads", reloaded.ActiveThreads, archivedActiveID)
	assertAbsent("reloaded DormantThreads", reloaded.DormantThreads, archivedDormantID)
	assertPresent("reloaded ActiveThreads", reloaded.ActiveThreads, survivorID)
}

// TestDayBarrier_NoOpSameDay: with no completed day pending, the
// pre-turn poll is a no-op — over-pressure spine or not, no drain and
// no primary commit happen mid-day (archival is barrier-only).
func TestDayBarrier_NoOpSameDay(t *testing.T) {
	pinClock(t, barrierDay0)
	paths, meta := newTestHome(t)
	const total = memops.ArchiveHighWater + 30
	for rank := 0; rank < total; rank++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", rank+1),
			memops.ThreadResolved, monotonicTS(rank))
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock([]model.Response{
		{Content: "A plain reply with no topic tag."},
	}, nil))

	// SAME day as the daily's birth: nothing completed, no barrier.
	if _, err := RunWithDeltas(context.Background(), state, nil, "hello", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(recs) != total {
		t.Errorf("spine count = %d, want %d (no drain without a day barrier)", len(recs), total)
	}
	if strings.Contains(readArchivalEventLog(t, paths), "barrier.begin") {
		t.Error("barrier fired on a same-day poll")
	}
}
