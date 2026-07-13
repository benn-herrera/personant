package turn

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/store"
)

// BD-7 / decision D2: a topic tag naming a nonexistent thread
// (engaged-miss) or another project's thread (cross-project decline) is a
// phantom. It must never enter `engaged`, ActiveThreads, or the persisted
// working set; if it was the would-be owner, ownership falls back to the
// most-recent valid engaged thread, else a new thread is created — the
// turn's excerpt is never dropped (spec §3.2: the tag is advisory,
// ownership assignment is a runtime decision).

// assertNoThread fails if thrID has a spine record.
func assertNoThread(t *testing.T, paths store.PersonantPaths, thrID string) {
	t.Helper()
	_, found, err := store.FindSpineRecord(paths, thrID)
	if err != nil {
		t.Fatalf("find %s: %v", thrID, err)
	}
	if found {
		t.Errorf("phantom %s has a spine record; must never be created", thrID)
	}
}

// assertNotMember fails if thrID appears in list.
func assertNotMember(t *testing.T, what string, list []string, thrID string) {
	t.Helper()
	for _, id := range list {
		if id == thrID {
			t.Errorf("phantom %s present in %s %v", thrID, what, list)
		}
	}
}

// TestPhantomOwnerFallsBackToValidEngaged — engaged-miss as would-be
// owner with a valid co-engaged thread (D2 fallback i): the tag
// [thr_1, thr_2] names nonexistent thr_1 (lowest id → would-be owner);
// valid thr_2 owns instead, and thr_1 never reaches ActiveThreads or the
// persisted working set.
func TestPhantomOwnerFallsBackToValidEngaged(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, meta.ID, "thr_2", 3, anchors)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1, thr_2 [alpha, beta, gamma, delta]*\nPhantom-owner reply."},
	}, nil)
	adapter := fileadapter.NewFileAdapter(paths)
	state := NewState(adapter, meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_2"} // suppress §5.5 fetch for the valid thread
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "phantom owner", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Fallback owner thr_2: turn_count 3 → 4, excerpt appended.
	owner, found, err := store.FindSpineRecord(paths, "thr_2")
	if err != nil || !found {
		t.Fatalf("find thr_2: %v found=%v", err, found)
	}
	if owner.TurnCount != 4 {
		t.Errorf("fallback owner turn_count: got %d want 4", owner.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_2"); got != 2 {
		t.Errorf("fallback owner turn files: got %d want 2 (seed + this turn)", got)
	}

	// The phantom is nowhere: no spine record, no working-set membership,
	// no persisted membership.
	assertNoThread(t, paths, "thr_1")
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_1")
	assertNotMember(t, "DormantThreads", state.DormantThreads, "thr_1")
	active, dormant, err := adapter.LoadWorkingSet(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingSet: %v", err)
	}
	assertNotMember(t, "persisted active", active, "thr_1")
	assertNotMember(t, "persisted dormant", dormant, "thr_1")
}

// TestPhantomOwnerNoValidCreatesNewThread — engaged-miss as owner with NO
// valid co-engaged thread (D2 fallback ii): a new thread is created from
// the turn exactly as a *new-topic* tag would be — excerpt present,
// turn_count 1, description from the prompt (spec §2.3) — and the phantom
// never persists anywhere.
func TestPhantomOwnerNoValidCreatesNewThread(t *testing.T) {
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_5 [foo, bar, baz, qux]*\nPhantom-only reply."},
	}, nil)
	adapter := fileadapter.NewFileAdapter(paths)
	state := NewState(adapter, meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	const utterance = "turn that must not be lost"
	if _, err := Run(context.Background(), state, utterance, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Fallback-created thread owns the turn: excerpt, turn_count 1,
	// description from the prompt.
	rec, found, err := store.FindSpineRecord(paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("find fallback thread thr_1: %v found=%v", err, found)
	}
	if rec.TurnCount != 1 {
		t.Errorf("fallback thread turn_count: got %d want 1", rec.TurnCount)
	}
	if rec.Description != utterance {
		t.Errorf("fallback thread Description: got %q want %q", rec.Description, utterance)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 1 {
		t.Errorf("fallback thread turn files: got %d want 1 (excerpt never dropped)", got)
	}

	assertNoThread(t, paths, "thr_5")
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_5")
	active, dormant, err := adapter.LoadWorkingSet(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingSet: %v", err)
	}
	assertNotMember(t, "persisted active", active, "thr_5")
	assertNotMember(t, "persisted dormant", dormant, "thr_5")
}

// TestPhantomNonOwnerDropped — a pure engaged-miss NON-owner: valid thr_1
// owns normally; phantom thr_9 is simply dropped from engaged — no spine
// record, no ActiveThreads entry, no persisted membership, and no
// fallback thread is created.
func TestPhantomNonOwnerDropped(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, meta.ID, "thr_1", 5, anchors)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1, thr_9 [alpha, beta, gamma, delta]*\nNon-owner phantom reply."},
	}, nil)
	adapter := fileadapter.NewFileAdapter(paths)
	state := NewState(adapter, meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_1"}
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "non-owner phantom", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	owner, _, _ := store.FindSpineRecord(paths, "thr_1")
	if owner.TurnCount != 6 {
		t.Errorf("owner turn_count: got %d want 6", owner.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 2 {
		t.Errorf("owner turn files: got %d want 2", got)
	}

	assertNoThread(t, paths, "thr_9")
	// No fallback thread either — the owner was valid.
	assertNoThread(t, paths, "thr_2")
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_9")
	active, dormant, err := adapter.LoadWorkingSet(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingSet: %v", err)
	}
	assertNotMember(t, "persisted active", active, "thr_9")
	assertNotMember(t, "persisted dormant", dormant, "thr_9")
}

// TestCrossProjectOwnerFallsBackToValidEngaged — cross-project decline as
// would-be owner with a valid co-engaged thread (D2 fallback i). Driven
// through closeTurnAndUpdateEngagement directly so the assertion targets
// the engagement path itself (the §5.5 mid-turn fetch is out of scope).
func TestCrossProjectOwnerFallsBackToValidEngaged(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, "prj_other", "thr_1", 8, anchors)
	seedActiveThread(t, paths, meta.ID, "thr_2", 3, anchors)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.TurnNumber = 7
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.coalesce.addThread("thr_1")
	state.coalesce.addThread("thr_2")

	if err := closeTurnAndUpdateEngagement(context.Background(), state, "cross project input", "cross project reply"); err != nil {
		t.Fatalf("closeTurnAndUpdateEngagement: %v", err)
	}

	// Valid thr_2 owns: turn_count 3 → 4, excerpt appended.
	owner, _, _ := store.FindSpineRecord(paths, "thr_2")
	if owner.TurnCount != 4 {
		t.Errorf("fallback owner turn_count: got %d want 4", owner.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_2"); got != 2 {
		t.Errorf("fallback owner turn files: got %d want 2", got)
	}

	// The declined cross-project record is untouched: no count bump, no
	// excerpt, recency unchanged.
	other, _, _ := store.FindSpineRecord(paths, "thr_1")
	if other.TurnCount != 8 {
		t.Errorf("cross-project turn_count: got %d want 8 (untouched)", other.TurnCount)
	}
	if other.LastEngaged != "2026-04-01T00:00:00Z" {
		t.Errorf("cross-project last_engaged mutated: got %q", other.LastEngaged)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 1 {
		t.Errorf("cross-project turn files: got %d want 1 (seed only)", got)
	}
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_1")
	assertNotMember(t, "DormantThreads", state.DormantThreads, "thr_1")
}

// TestCrossProjectOwnerNoValidCreatesNewThread — cross-project decline as
// owner with NO valid co-engaged thread (D2 fallback ii): a new thread in
// the ACTIVE project owns the turn; the other project's record is never
// mutated.
func TestCrossProjectOwnerNoValidCreatesNewThread(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, "prj_other", "thr_1", 8, anchors)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.TurnNumber = 4
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.coalesce.addThread("thr_1")

	const utterance = "declined owner input"
	if err := closeTurnAndUpdateEngagement(context.Background(), state, utterance, "declined owner reply"); err != nil {
		t.Fatalf("closeTurnAndUpdateEngagement: %v", err)
	}

	// NextThreadID scans the full spine, so the fallback thread is thr_2.
	rec, found, err := store.FindSpineRecord(paths, "thr_2")
	if err != nil || !found {
		t.Fatalf("find fallback thread thr_2: %v found=%v", err, found)
	}
	if rec.Project != meta.ID {
		t.Errorf("fallback thread project: got %q want %q", rec.Project, meta.ID)
	}
	if rec.TurnCount != 1 {
		t.Errorf("fallback thread turn_count: got %d want 1", rec.TurnCount)
	}
	if rec.Description != utterance {
		t.Errorf("fallback thread Description: got %q want %q", rec.Description, utterance)
	}
	if got := turnFileCount(t, paths, "thr_2"); got != 1 {
		t.Errorf("fallback thread turn files: got %d want 1 (excerpt never dropped)", got)
	}

	other, _, _ := store.FindSpineRecord(paths, "thr_1")
	if other.TurnCount != 8 {
		t.Errorf("cross-project turn_count: got %d want 8 (untouched)", other.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 1 {
		t.Errorf("cross-project turn files: got %d want 1 (seed only)", got)
	}
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_1")
}

// TestPhantomOwnerFallbackPicksMostRecentValid — D2 fallback (i) pins
// "most-recent": with phantom thr_1 as would-be owner and two valid
// co-engaged threads, the one with the higher LastEngagedTurn owns —
// NOT the lower-id one.
func TestPhantomOwnerFallbackPicksMostRecentValid(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, meta.ID, "thr_2", 3, anchors)
	seedActiveThread(t, paths, meta.ID, "thr_3", 5, anchors)
	// Stamp recency: thr_3 engaged more recently than thr_2.
	stampLastEngagedTurn(t, paths, "thr_2", 3)
	stampLastEngagedTurn(t, paths, "thr_3", 7)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.TurnNumber = 9
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.coalesce.addThread("thr_1") // phantom, lowest id → would-be owner
	state.coalesce.addThread("thr_2")
	state.coalesce.addThread("thr_3")

	if err := closeTurnAndUpdateEngagement(context.Background(), state, "recency pick", "recency reply"); err != nil {
		t.Fatalf("closeTurnAndUpdateEngagement: %v", err)
	}

	// Most-recent valid thread thr_3 owns.
	rec3, _, _ := store.FindSpineRecord(paths, "thr_3")
	if rec3.TurnCount != 6 {
		t.Errorf("most-recent thr_3 turn_count: got %d want 6 (owner)", rec3.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_3"); got != 2 {
		t.Errorf("most-recent thr_3 turn files: got %d want 2 (owns excerpt)", got)
	}
	// thr_2 is engaged-but-not-owner: no count bump, no excerpt.
	rec2, _, _ := store.FindSpineRecord(paths, "thr_2")
	if rec2.TurnCount != 3 {
		t.Errorf("thr_2 turn_count: got %d want 3 (non-owner)", rec2.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_2"); got != 1 {
		t.Errorf("thr_2 turn files: got %d want 1 (no excerpt)", got)
	}
	assertNoThread(t, paths, "thr_1")
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_1")
}

// logsContain reports whether any *.log file under LogsDir contains marker.
func logsContain(t *testing.T, paths store.PersonantPaths, marker string) bool {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), marker) {
			return true
		}
	}
	return false
}

// TestCrossProjectDeclinedAtRepromptFetch_FullRun is the BD-7 end-to-end
// proof the reviews demanded: a topic tag naming another project's thread
// NOT in Layer B triggers the §5.5 mid-turn fetch, which DECLINES it at the
// fetch seam (crossProjectDecline, thread.fetch-cross-project) — no
// promotion, no re-prompt (nothing was fetched) — and the turn-close
// resolution declines it again, so after the full Run the foreign id is in
// NEITHER ActiveThreads nor DormantThreads of the PERSISTED working set,
// and the foreign record is never mutated.
func TestCrossProjectDeclinedAtRepromptFetch_FullRun(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, "prj_other", "thr_1", 8, anchors)
	seedActiveThread(t, paths, meta.ID, "thr_2", 3, anchors)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1, thr_2 [alpha, beta, gamma, delta]*\nCross-project fetch reply."},
	}, nil)
	adapter := fileadapter.NewFileAdapter(paths)
	state := NewState(adapter, meta, memops.Provider{}, mock)
	// thr_2 resident in Layer B; thr_1 absent → the tag triggers the §5.5
	// fetch for exactly the cross-project thread.
	state.ActiveThreads = []string{"thr_2"}
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "cross project fetch", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The §5.5 seam was exercised and declined (not silently skipped).
	if !logsContain(t, paths, "fetch-cross-project") {
		t.Errorf("no thread.fetch-cross-project log line: the §5.5 fetch seam did not decline the foreign thread")
	}

	// Valid thr_2 owns: turn_count 3 → 4, excerpt appended.
	owner, _, _ := store.FindSpineRecord(paths, "thr_2")
	if owner.TurnCount != 4 {
		t.Errorf("owner turn_count: got %d want 4", owner.TurnCount)
	}

	// The foreign record is untouched.
	other, _, _ := store.FindSpineRecord(paths, "thr_1")
	if other.TurnCount != 8 || other.LastEngaged != "2026-04-01T00:00:00Z" {
		t.Errorf("cross-project record mutated: turn_count=%d last_engaged=%q", other.TurnCount, other.LastEngaged)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 1 {
		t.Errorf("cross-project turn files: got %d want 1 (seed only)", got)
	}

	// The foreign id is nowhere in the live OR persisted working set.
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_1")
	assertNotMember(t, "DormantThreads", state.DormantThreads, "thr_1")
	active, dormant, err := adapter.LoadWorkingSet(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingSet: %v", err)
	}
	assertNotMember(t, "persisted active", active, "thr_1")
	assertNotMember(t, "persisted dormant", dormant, "thr_1")
}

// TestPhantomWithNewTopicOwnsNewThread pins the all-phantom + *new-topic*
// branch (engage.go): when every referenced thr_<n> is a phantom AND the
// tag carries *new-topic*, the new thread is created as this turn's OWNER
// via the hasNewTopic branch (excerpt, turn_count 1, description from the
// prompt) — and exactly ONE thread is created (the len(engaged)==0
// fallback must not fire a second one). The phantom persists nowhere.
func TestPhantomWithNewTopicOwnsNewThread(t *testing.T) {
	paths, meta := newTestHome(t)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.TurnNumber = 3
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.coalesce.addThread("thr_9") // phantom: not in the spine
	state.coalesce.addThread(prompt.NewTopicLiteral)

	const utterance = "phantom plus new topic input"
	if err := closeTurnAndUpdateEngagement(context.Background(), state, utterance, "phantom plus new topic reply"); err != nil {
		t.Fatalf("closeTurnAndUpdateEngagement: %v", err)
	}

	// The new thread (empty spine → thr_1) owns the turn.
	rec, found, err := store.FindSpineRecord(paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("find new thread thr_1: %v found=%v", err, found)
	}
	if rec.TurnCount != 1 {
		t.Errorf("new thread turn_count: got %d want 1 (owner)", rec.TurnCount)
	}
	if rec.Description != utterance {
		t.Errorf("new thread Description: got %q want %q", rec.Description, utterance)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 1 {
		t.Errorf("new thread turn files: got %d want 1 (owns the excerpt)", got)
	}

	// Exactly one thread created; the phantom persists nowhere.
	assertNoThread(t, paths, "thr_2")
	assertNoThread(t, paths, "thr_9")
	assertNotMember(t, "ActiveThreads", state.ActiveThreads, "thr_9")
	assertNotMember(t, "DormantThreads", state.DormantThreads, "thr_9")
}

// TestMostRecentEngagedID_TieBreakLowestID pins the D2 fallback-(i)
// deterministic tie-break: equal LastEngagedTurn → lowest thr_<n> id,
// independent of input order; unequal recency always wins over id.
func TestMostRecentEngagedID_TieBreakLowestID(t *testing.T) {
	recs := map[string]memops.SpineRecord{
		"thr_2": {ID: "thr_2", LastEngagedTurn: 3},
		"thr_3": {ID: "thr_3", LastEngagedTurn: 5},
		"thr_7": {ID: "thr_7", LastEngagedTurn: 5},
		"thr_9": {ID: "thr_9", LastEngagedTurn: 8},
	}
	cases := []struct {
		name     string
		validIDs []string
		want     string
	}{
		{name: "tie broken by lowest id", validIDs: []string{"thr_7", "thr_3"}, want: "thr_3"},
		{name: "tie break order-independent", validIDs: []string{"thr_3", "thr_7"}, want: "thr_3"},
		{name: "recency beats lower id", validIDs: []string{"thr_2", "thr_9"}, want: "thr_9"},
		{name: "empty valid set", validIDs: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mostRecentEngagedID(tc.validIDs, recs); got != tc.want {
				t.Errorf("mostRecentEngagedID(%v) = %q, want %q", tc.validIDs, got, tc.want)
			}
		})
	}
}

// stampLastEngagedTurn rewrites thrID's spine record with the given
// LastEngagedTurn so fallback-(i) recency selection has distinct inputs.
func stampLastEngagedTurn(t *testing.T, paths store.PersonantPaths, thrID string, lastEngagedTurn int) {
	t.Helper()
	rec, found, err := store.FindSpineRecord(paths, thrID)
	if err != nil || !found {
		t.Fatalf("find %s: %v found=%v", thrID, err, found)
	}
	rec.LastEngagedTurn = lastEngagedTurn
	if err := store.UpdateSpineRecord(paths, rec); err != nil {
		t.Fatalf("update spine %s: %v", thrID, err)
	}
}
