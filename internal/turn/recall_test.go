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

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
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

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
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

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
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
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
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
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
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

// TestSurfaceRecall_AcceptPromotesToLayerB — with a resolver installed,
// accepting exactly one of two offered candidates promotes that thread
// into Layer B (ActiveThreads) and leaves the declined one out. Proves
// the accept branch of applyRecallResolution wires through to
// promoteToLayerB and that the offer is logged with its candidate count.
func TestSurfaceRecall_AcceptPromotesToLayerB(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "zeta", "eta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)

	// Accept only the candidate whose ThreadID is thr_1 — its index in
	// offer.Candidates is not assumed, it is located by scanning.
	state.RecallResolver = func(_ context.Context, offer RecallOffer) (RecallResolution, error) {
		for i, c := range offer.Candidates {
			if c.ThreadID == "thr_1" {
				return RecallResolution{Accept: []int{i}, Reason: DeclineNotRelevant}, nil
			}
		}
		t.Errorf("thr_1 not present in offer.Candidates: %v", offer.Candidates)
		return RecallResolution{Reason: DeclineNotRelevant}, nil
	}

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	if !slices.Contains(state.ActiveThreads, "thr_1") {
		t.Errorf("thr_1 accepted but not in ActiveThreads: %v", state.ActiveThreads)
	}
	if slices.Contains(state.ActiveThreads, "thr_2") {
		t.Errorf("thr_2 declined but present in ActiveThreads: %v", state.ActiveThreads)
	}

	// The §3.4 accept-path recallSurfaced marking now flows through the
	// shared promoteToLayerB chokepoint (no longer an explicit block in
	// applyRecallResolution). Accepted thr_1 is marked; declined thr_2 is not.
	if _, ok := state.recallSurfaced["thr_1"]; !ok {
		t.Errorf("accepted thr_1 must be marked recallSurfaced via promoteToLayerB; got %v", state.recallSurfaced)
	}
	if _, ok := state.recallSurfaced["thr_2"]; ok {
		t.Errorf("declined thr_2 must not be marked recallSurfaced; got %v", state.recallSurfaced)
	}

	logBody := readDayLog(t, paths)
	for _, want := range []string{
		"recall.offer count=2",
		"recall.accept thr=thr_1",
		"recall.decline thr=thr_2",
	} {
		if !strings.Contains(logBody, want) {
			t.Errorf("log missing %q\nlog body:\n%s", want, logBody)
		}
	}
}

// TestSurfaceRecall_AcceptFiresThreadFetchedDelta — the §3.0.5 "no gaps"
// fix (M3 / guest G-F3): a recall-accept promotion mutates Layer B, so it
// must go through the §3.0 hook chain like every other working-window
// fetch — parity with fetchThreadForReprompt (§5.5). Accepting thr_1 must
// fire a thread.fetched delta through onContextDelta, whose observable
// effect is the chain's step-5 substrate event (context.modified
// source=thread.fetched). The declined thr_2 fires no such delta, so
// exactly one is expected.
func TestSurfaceRecall_AcceptFiresThreadFetchedDelta(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "zeta", "eta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)

	state.RecallResolver = func(_ context.Context, offer RecallOffer) (RecallResolution, error) {
		for i, c := range offer.Candidates {
			if c.ThreadID == "thr_1" {
				return RecallResolution{Accept: []int{i}, Reason: DeclineNotRelevant}, nil
			}
		}
		t.Errorf("thr_1 not present in offer.Candidates: %v", offer.Candidates)
		return RecallResolution{Reason: DeclineNotRelevant}, nil
	}

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	logBody := readDayLog(t, paths)
	// The chain's step-5 event is the observable proof the recall-accept
	// promotion ran through onContextDelta. The fetched body is non-empty,
	// so bytes>0; assert on the source token (the bytes count is incidental).
	const fetchedEvent = "context.modified source=thread.fetched"
	if !strings.Contains(logBody, fetchedEvent) {
		t.Errorf("accepted promotion did not fire a thread.fetched delta through the chain;\nlog body:\n%s", logBody)
	}
	// Exactly one — the accepted thr_1 fires it; the declined thr_2 does not.
	if got := strings.Count(logBody, fetchedEvent); got != 1 {
		t.Errorf("thread.fetched delta count: got %d want 1 (only the accepted thread)\nlog body:\n%s", got, logBody)
	}
	// Sanity: the promotion still happened and the accept was logged.
	if !slices.Contains(state.ActiveThreads, "thr_1") {
		t.Errorf("thr_1 accepted but not promoted to ActiveThreads: %v", state.ActiveThreads)
	}
	if !strings.Contains(logBody, "recall.accept thr=thr_1") {
		t.Errorf("recall.accept thr=thr_1 not logged:\n%s", logBody)
	}
}

// TestSurfaceRecall_DeclineAllLogsReason — a resolver that accepts
// nothing declines every offered candidate, each decline carrying the
// resolution's Reason. Proves Layer B is untouched on a full decline
// and that the supplied DeclineReason is recorded verbatim.
func TestSurfaceRecall_DeclineAllLogsReason(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "zeta", "eta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)

	state.RecallResolver = func(_ context.Context, _ RecallOffer) (RecallResolution, error) {
		return RecallResolution{Reason: DeclineWrongProject}, nil
	}

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	if len(state.ActiveThreads) != 0 {
		t.Errorf("nothing accepted but ActiveThreads non-empty: %v", state.ActiveThreads)
	}

	logBody := readDayLog(t, paths)
	for _, want := range []string{
		"recall.decline thr=thr_1 reason=wrong-project",
		"recall.decline thr=thr_2 reason=wrong-project",
	} {
		if !strings.Contains(logBody, want) {
			t.Errorf("log missing %q\nlog body:\n%s", want, logBody)
		}
	}
	if strings.Contains(logBody, "recall.accept") {
		t.Errorf("no candidate accepted but recall.accept logged:\n%s", logBody)
	}
}

// TestSurfaceRecall_NoResolverStaysLogOnly — with no RecallResolver
// installed, recall still runs and logs its per-layer matches, but no
// offer is surfaced and no accept/decline verdict is recorded. Proves
// the nil-resolver path is purely observational.
func TestSurfaceRecall_NoResolverStaysLogOnly(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"alpha", "beta", "zeta", "eta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)
	// RecallResolver deliberately left nil.

	if err := surfaceRecallCandidates(context.Background(), state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "spine.match-fire") {
		t.Errorf("recall expected to run and log match-fire:\n%s", logBody)
	}
	for _, unwanted := range []string{"recall.offer", "recall.accept", "recall.decline"} {
		if strings.Contains(logBody, unwanted) {
			t.Errorf("nil resolver should stay log-only but log contains %q:\n%s", unwanted, logBody)
		}
	}
}
