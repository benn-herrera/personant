package turn

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if len(entries) != 1 {
		t.Fatalf("logs dir entries: got %d want 1 (%v)", len(entries), entries)
	}
	data, err := os.ReadFile(filepath.Join(paths.LogsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

// seedThreadWithAnchors writes a minimal-but-valid thread file plus
// matching spine record for thrID with caller-provided anchors. Used
// by recall tests that need specific anchor sets per thread.
func seedThreadWithAnchors(t *testing.T, paths store.PersonantPaths, project, thrID string, anchors []string) {
	t.Helper()
	rec := store.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      anchors,
		Summary:      thrID,
		State:        store.ThreadActive,
		Created:      "2026-04-01T00:00:00Z",
		LastEngaged:  "2026-04-01T00:00:00Z",
		StateChanged: "2026-04-01T00:00:00Z",
		TurnCount:    1,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", thrID, err)
	}
	thr := store.Thread{
		Frontmatter: store.ThreadFrontmatter{
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
	if err := store.SaveThread(paths, thr); err != nil {
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

	state := NewState(paths, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("alpha", "alpha", store.SourceUser)
	state.coalesce.addSymbol("beta", "beta", store.SourceUser)

	if err := surfaceRecallCandidates(state, map[string]struct{}{}); err != nil {
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

	state := NewState(paths, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("alpha", "alpha", store.SourceUser)
	state.coalesce.addSymbol("beta", "beta", store.SourceUser)

	engaged := map[string]struct{}{"thr_1": {}}
	if err := surfaceRecallCandidates(state, engaged); err != nil {
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

	state := NewState(paths, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
	state.coalesce.addSymbol("nope1", "nope1", store.SourceUser)
	state.coalesce.addSymbol("nope2", "nope2", store.SourceUser)

	if err := surfaceRecallCandidates(state, map[string]struct{}{}); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	// No match-fire entries — but also no log file at all is acceptable
	// (eventlog only creates the day file when something gets logged).
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
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
	state := NewState(paths, meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

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
	state := NewState(paths, meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	if _, err := Run(context.Background(), state, "hey there", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), "spine.match-fire") {
			t.Errorf("unexpected spine.match-fire entry:\n%s", string(data))
		}
	}
}
