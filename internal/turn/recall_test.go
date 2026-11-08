package turn

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
)

// readDayLog reads the single day-log file produced by tests; fails
// when the directory does not contain exactly one entry. Returns the
// log body as a string.
func readDayLog(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	// LogsDir holds the YYYY-MM-DD.log files plus a logs/archive/ rotation
	// subdir (created by store.Init). Read every .log file, skipping the
	// subdir — counting raw entries would over-count the archive dir.
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

// seedThreadWithAnchors writes a minimal-but-valid thread file plus
// matching spine record for thrID with caller-provided anchors. Used
// by recall tests that need specific anchor sets per thread.
func seedThreadWithAnchors(t *testing.T, paths store.PersonantPaths, project, thrID string, anchors []string) {
	t.Helper()
	rec := memops.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      anchors,
		Summary:      thrID,
		State:        memops.ThreadActive,
		Created:      "2026-04-01T00:00:00Z",
		LastEngaged:  "2026-04-01T00:00:00Z",
		StateChanged: "2026-04-01T00:00:00Z",
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
		Body: "# " + thrID + "\n\n## Turn 1 · 2026-04-01T00:00:00Z · [" + strings.Join(anchors, ", ") + "]\n\n**user:** seed\n\n**agent:** seed reply\n",
	}
	if err := store.SeedThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// TestSurfaceRecallCandidates_LogsMatchFire — two threads share two
// query symbols each (Jaccard 2/4 = 0.5, above default 0.4). Both
// fire one spine.match-fire log line apiece.
func TestSurfaceRecallCandidates_LogsMatchFire(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "zeta", "eta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	logBody := readDayLog(t, paths)
	for _, want := range []string{
		"spine.match-fire thr_1 score=0.50 matched=alpha,beta query_size=2",
		"spine.match-fire thr_2 score=0.50 matched=alpha,beta query_size=2",
	} {
		if !strings.Contains(logBody, want) {
			t.Errorf("log missing %q\nlog body:\n%s", want, logBody)
		}
	}
	// Two and only two match-fire entries.
	if got := strings.Count(logBody, "spine.match-fire"); got != 2 {
		t.Errorf("spine.match-fire count: got %d want 2", got)
	}
}

// TestSurfaceRecallCandidates_ExcludesEngaged — passing thr_1 in the
// engaged set drops it from results; only thr_2 fires.
func TestSurfaceRecallCandidates_ExcludesEngaged(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "zeta", "eta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)

	engaged := map[string]struct{}{"thr_1": {}}
	if err := surfaceRecallCandidates(context.Background(), state, "", engaged, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	logBody := readDayLog(t, paths)
	if strings.Contains(logBody, "spine.match-fire thr_1") {
		t.Errorf("thr_1 was excluded but still fired:\n%s", logBody)
	}
	if !strings.Contains(logBody, "spine.match-fire thr_2 score=0.50 matched=alpha,beta query_size=2") {
		t.Errorf("thr_2 expected to fire:\n%s", logBody)
	}
	if got := strings.Count(logBody, "spine.match-fire"); got != 1 {
		t.Errorf("spine.match-fire count: got %d want 1", got)
	}
}

// TestSurfaceRecallCandidates_NoCandidatesIsQuiet — query has no
// overlap with any thread's symbols; no spine.match-fire entries.
func TestSurfaceRecallCandidates_NoCandidatesIsQuiet(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil))
	state.coalesce.addSymbol("nope1", "nope1", memops.SourceUser)
	state.coalesce.addSymbol("nope2", "nope2", memops.SourceUser)

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	// No match-fire entries — but also no log file at all is acceptable
	// (eventlog only creates the day file when something gets logged).
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
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), "spine.match-fire") {
			t.Errorf("unexpected spine.match-fire entry in %s:\n%s", e.Name(), string(data))
		}
	}
}

// TestRunFiresRecallAtTurnClose — full Run integration. thr_42 is
// seeded with anchors that overlap with the turn's coalesced symbol
// set; the *new-topic* engagement creates thr_1 (excluded from
// recall as engaged), and thr_42 fires a spine.match-fire log line.
func TestRunFiresRecallAtTurnClose(t *testing.T) {
	paths, meta := newTestHome(t)
	// Seed thr_42 with anchors whose four symbols all appear in the
	// turn's coalesced set — Q ∩ T = full 4, score = 1.0.
	seedThreadWithAnchors(t, paths, meta.ID, "thr_42",
		[]string{"alpha", "beta", "gamma", "delta"})

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [alpha, beta, gamma, delta]*\nNew thread."},
	})
	state := NewState(fileadapter.NewFileAdapter(paths), meta, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	var out bytes.Buffer
	if _, err := Run(context.Background(), state, "talking about #alpha and #beta", &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	logBody := readDayLog(t, paths)
	wantLine := "spine.match-fire thr_42 score=1.00 matched=alpha,beta,delta,gamma query_size=4"
	if !strings.Contains(logBody, wantLine) {
		t.Errorf("expected match-fire for thr_42:\n  want substring: %q\n  log body:\n%s", wantLine, logBody)
	}
	// The newly-created thread (thr_1) is in engaged-set → must NOT
	// appear as a recall candidate.
	if strings.Contains(logBody, "spine.match-fire thr_1 ") {
		t.Errorf("engaged thread thr_1 leaked into recall fires:\n%s", logBody)
	}
	if got := strings.Count(logBody, "spine.match-fire"); got != 1 {
		t.Errorf("spine.match-fire count: got %d want 1", got)
	}
}

// TestRunNoRecallWhenNoSymbols — prompt has no extractable identifiers
// and the model emits no topic tag, so coalesce is empty and
// closeTurnAndUpdateEngagement returns early before recall runs. No
// spine.match-fire entries.
func TestRunNoRecallWhenNoSymbols(t *testing.T) {
	paths, meta := newTestHome(t)
	// Seed a thread so a Propose call would have something to match
	// against IF it ran — confirms the absence of fires is because of
	// the early-return path, not lack of corpus.
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1",
		[]string{"alpha", "beta", "gamma", "delta"})

	mock := model.NewScriptedMock([]model.Response{
		{Content: "Just a plain reply, nothing to see."},
	})
	state := NewState(fileadapter.NewFileAdapter(paths), meta, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "hey there", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

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
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), "spine.match-fire") {
			t.Errorf("unexpected spine.match-fire entry:\n%s", string(data))
		}
	}
}

// recallBandState seeds thr_1 at symbolic score 1.0 (anchors = the query,
// at or above the 0.65 auto-fetch bar) and thr_2 at 0.5 (below it), and
// returns a State with no resolver of any kind whose query is alpha+beta.
func recallBandState(t *testing.T) (store.PersonantPaths, *State) {
	t.Helper()
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "gamma", "delta"})
	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)
	return paths, state
}

// recallFires reads thrID's §2.2 recall_fires counter off the spine.
func recallFires(t *testing.T, paths store.PersonantPaths, thrID string) int {
	t.Helper()
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	for _, rec := range recs {
		if rec.ID == thrID {
			return rec.RecallFires
		}
	}
	t.Fatalf("%s not on the spine", thrID)
	return 0
}

// TestRecall_AutoBandCandidateIsFetched — with no resolver installed, a
// candidate at or above its tier's auto-fetch bar is fetched into Layer B
// through the accept path: promoted, marked recallSurfaced (the §2.7.3
// derived_from source), counted in recall_fires, logged recall.accept, and
// fetched through the §3.0 chain (exactly one thread.fetched delta). The
// below-bar thr_2 gets none of it.
func TestRecall_AutoBandCandidateIsFetched(t *testing.T) {
	paths, state := recallBandState(t)

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	if !slices.Contains(state.ActiveThreads, "thr_1") {
		t.Errorf("auto-band thr_1 not fetched into Layer B: %v", state.ActiveThreads)
	}
	if _, ok := state.recallSurfaced["thr_1"]; !ok {
		t.Errorf("fetched thr_1 not marked recallSurfaced: %v", state.recallSurfaced)
	}
	if got := recallFires(t, paths, "thr_1"); got != 1 {
		t.Errorf("thr_1 recall_fires = %d, want 1", got)
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "recall.accept thr=thr_1 layers=symbolic\n") {
		t.Errorf("want recall.accept thr=thr_1 layers=symbolic; log:\n%s", logBody)
	}
	if got := strings.Count(logBody, "context.modified source=thread.fetched"); got != 1 {
		t.Errorf("thread.fetched delta count = %d, want 1 (thr_1 only); log:\n%s", got, logBody)
	}
}

// TestRecall_BelowBarCandidateIsNotFetched — a candidate under its tier's
// auto-fetch bar leaves its match-fire record and nothing else: no Layer-B
// promotion, no recallSurfaced mark, no recall_fires bump, no recall.accept.
func TestRecall_BelowBarCandidateIsNotFetched(t *testing.T) {
	paths, state := recallBandState(t)

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	if slices.Contains(state.ActiveThreads, "thr_2") {
		t.Errorf("below-bar thr_2 fetched into Layer B: %v", state.ActiveThreads)
	}
	if _, ok := state.recallSurfaced["thr_2"]; ok {
		t.Errorf("below-bar thr_2 marked recallSurfaced: %v", state.recallSurfaced)
	}
	if got := recallFires(t, paths, "thr_2"); got != 0 {
		t.Errorf("thr_2 recall_fires = %d, want 0", got)
	}
	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "spine.match-fire thr_2 ") {
		t.Errorf("below-bar thr_2 must still leave its match-fire record; log:\n%s", logBody)
	}
	if strings.Contains(logBody, "recall.accept thr=thr_2") {
		t.Errorf("below-bar thr_2 logged recall.accept; log:\n%s", logBody)
	}
}

// TestRecall_ResidentCandidateIsSkipped — an auto-band non-intra candidate
// already resident in Layer B is already in context, so it is not fetched
// again: no re-promotion (Layer-B order unchanged), no recall_fires bump, no
// recall.accept. Without the skip it would re-fire every turn the topic
// continued.
func TestRecall_ResidentCandidateIsSkipped(t *testing.T) {
	paths, state := recallBandState(t)
	state.ActiveThreads = []string{"thr_9", "thr_1"}

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	if want := []string{"thr_9", "thr_1"}; !slices.Equal(state.ActiveThreads, want) {
		t.Errorf("resident thr_1 re-promoted: ActiveThreads = %v, want %v", state.ActiveThreads, want)
	}
	if got := recallFires(t, paths, "thr_1"); got != 0 {
		t.Errorf("resident thr_1 recall_fires = %d, want 0", got)
	}
	if logBody := readDayLog(t, paths); strings.Contains(logBody, "recall.accept") {
		t.Errorf("resident candidate logged recall.accept; log:\n%s", logBody)
	}
}

// TestRecall_AutoFetchBarPerSignal — each signal is judged on its own
// scale: symbolic at 0.65, cosine (embedding or a scored intra hit) at 0.75,
// inclusive at the bar. A Score-0 completeness-only intra hit never clears.
func TestRecall_AutoFetchBarPerSignal(t *testing.T) {
	sym := &measure.SymbolicHit{}
	emb := &measure.EmbeddingHit{}
	tests := []struct {
		name string
		r    measure.Result
		want bool
	}{
		{"symbolic at bar", measure.Result{Score: 0.65, Symbolic: sym}, true},
		{"symbolic below bar", measure.Result{Score: 0.64, Symbolic: sym}, false},
		{"embedding at bar", measure.Result{Score: 0.75, Embedding: emb}, true},
		{"embedding below bar", measure.Result{Score: 0.74, Embedding: emb}, false},
		{"embedding between bars is cosine-judged", measure.Result{Score: 0.70, Embedding: emb, Symbolic: sym}, false},
		{"scored intra at bar", measure.Result{Score: 0.75, IntraThread: &measure.IntraThreadHit{Score: 0.75}}, true},
		{"scored intra below bar", measure.Result{Score: 0.74, IntraThread: &measure.IntraThreadHit{Score: 0.74}}, false},
		{"completeness-only intra", measure.Result{IntraThread: &measure.IntraThreadHit{Turns: []int{3}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoAccepts(tt.r); got != tt.want {
				t.Errorf("autoAccepts(%+v) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

// TestRecall_FetchesAtMostThree — four candidates clear the bar; only the
// top three are fetched.
func TestRecall_FetchesAtMostThree(t *testing.T) {
	paths, meta := newTestHome(t)
	for _, id := range []string{"thr_1", "thr_2", "thr_3", "thr_4"} {
		seedThreadWithAnchors(t, paths, meta.ID, id, []string{"alpha", "beta"})
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	logBody := readDayLog(t, paths)
	if got := strings.Count(logBody, "spine.match-fire "); got != 4 {
		t.Fatalf("precondition: %d match-fires, want 4; log:\n%s", got, logBody)
	}
	if got := strings.Count(logBody, "recall.accept "); got != 3 {
		t.Errorf("fetched %d candidates, want 3; log:\n%s", got, logBody)
	}
}
