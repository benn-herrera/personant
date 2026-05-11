package turn

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
	"personant/internal/workset"
)

// newTestHome scaffolds the minimum home layout the turn package needs:
// logs/, projects/<id>/meta.json, and an empty spine file.
func newTestHome(t *testing.T) (store.PersonantPaths, store.ProjectMeta) {
	t.Helper()
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	for _, dir := range []string{paths.ThreadsDir, paths.ProjectsDir, paths.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := store.WriteSpine(paths.Spine, nil); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	meta := store.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: tmp}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	return paths, meta
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestRunNewTopicCreatesSpineRecord(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{
			Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
		},
	}, nil)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	var out bytes.Buffer
	body, err := Run(context.Background(), state, "test prompt", &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(body, "Hello world") {
		t.Errorf("body missing greeting: %q", body)
	}
	if strings.Contains(body, "*topic:") {
		t.Errorf("topic tag not stripped from return: %q", body)
	}
	if !strings.Contains(out.String(), "Hello world") {
		t.Errorf("streamed body missing from out: %q", out.String())
	}
	if strings.Contains(out.String(), "*topic:") {
		t.Errorf("topic tag leaked into streamed out: %q", out.String())
	}

	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 spine record; got %d", len(records))
	}
	r := records[0]
	if r.ID != "thr_1" {
		t.Errorf("new id: got %q want thr_1", r.ID)
	}
	if r.Project != "prj_1" {
		t.Errorf("project: got %q want prj_1", r.Project)
	}
	if r.State != store.ThreadWIP {
		t.Errorf("state: got %q want wip", r.State)
	}
	if r.TurnCount != 1 {
		t.Errorf("turn_count: got %d want 1", r.TurnCount)
	}
	if len(r.Anchors) < 4 || len(r.Anchors) > 8 {
		t.Errorf("anchor cardinality: got %d", len(r.Anchors))
	}
}

func TestRunUpdatesExistingThread(t *testing.T) {
	paths, meta := newTestHome(t)

	existing := store.SpineRecord{
		ID:           "thr_42",
		Project:      meta.ID,
		Anchors:      []string{"trefoil", "unknot", "body-topology", "electron-shape"},
		Summary:      "topology",
		State:        store.ThreadActive,
		Created:      "2026-04-01T00:00:00Z",
		LastEngaged:  "2026-04-01T00:00:00Z",
		StateChanged: "2026-04-01T00:00:00Z",
		TurnCount:    7,
	}
	if err := store.AppendSpineRecord(paths, existing); err != nil {
		t.Fatalf("append: %v", err)
	}

	mock := model.NewScriptedMock([]model.Response{
		{
			Content: "*topic: thr_42 [trefoil, unknot, body-topology, electron-shape]*\nMore on the trefoil.",
		},
	}, nil)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	state.SetClock(fixedClock(now))

	if _, err := Run(context.Background(), state, "tell me more about #trefoil", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, found, err := store.FindSpineRecord(paths, "thr_42")
	if err != nil || !found {
		t.Fatalf("find: %v found=%v", err, found)
	}
	if got.TurnCount != 8 {
		t.Errorf("turn_count: got %d want 8", got.TurnCount)
	}
	if got.LastEngaged != now.Format(time.RFC3339) {
		t.Errorf("last_engaged: got %q want %q", got.LastEngaged, now.Format(time.RFC3339))
	}
	// Existing record's other fields should be preserved.
	if got.Created != existing.Created {
		t.Errorf("created changed: got %q want %q", got.Created, existing.Created)
	}
}

func TestRunCoalescesEngagement(t *testing.T) {
	paths, meta := newTestHome(t)

	existing := store.SpineRecord{
		ID:        "thr_42",
		Project:   meta.ID,
		Anchors:   []string{"alpha", "beta", "gamma", "delta"},
		Summary:   "test thread",
		State:     store.ThreadActive,
		TurnCount: 0,
	}
	if err := store.AppendSpineRecord(paths, existing); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Simulate per-turn coalescing by hand: directly drive two deltas
	// that both reference thr_42 (one user.prompt with #-tags, one
	// model.response with the topic tag), then close the turn. The
	// engagement update must fire exactly once.
	mock := model.NewScriptedMock(nil, nil) // unused
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	if err := onContextDelta(context.Background(), state, Delta{Source: "user.prompt", Content: "asking about #alpha and #beta"}); err != nil {
		t.Fatalf("user.prompt: %v", err)
	}
	if err := onContextDelta(context.Background(), state, Delta{Source: "model.response", Content: "*topic: thr_42 [alpha, beta, gamma, delta]*\nResponse."}); err != nil {
		t.Fatalf("model.response: %v", err)
	}
	if err := closeTurnAndUpdateEngagement(context.Background(), state, "asking about #alpha and #beta", "Response."); err != nil {
		t.Fatalf("close: %v", err)
	}

	got, found, err := store.FindSpineRecord(paths, "thr_42")
	if err != nil || !found {
		t.Fatalf("find: %v found=%v", err, found)
	}
	if got.TurnCount != 1 {
		t.Errorf("coalesced turn_count: got %d want 1 (one turn touched thread, regardless of delta count)", got.TurnCount)
	}
}

func TestRunNoTopicTagIsNonFatal(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "Just a plain response with no topic tag."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	body, err := Run(context.Background(), state, "hello", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(body, "plain response") {
		t.Errorf("body: %q", body)
	}
	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("no engagement should fire without a topic tag; got %d records", len(records))
	}
}

func TestRunNewTopicAnchorCardinalityOutOfRange(t *testing.T) {
	paths, meta := newTestHome(t)

	// Two anchors only — under the [4, 8] hard range.
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [only-two, anchors]*\nBrief reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	if _, err := Run(context.Background(), state, "ping", io.Discard); err != nil {
		// Note: prompt.Parse rejects this tag because anchor count <4 emits
		// a warning but is still a valid match. The package treats the count
		// as a warning, not a parse failure, so we should still get a
		// new-thread create.
		t.Fatalf("Run: %v", err)
	}
	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 spine record; got %d", len(records))
	}
	r := records[0]
	if got := len(r.Anchors); got != 4 {
		t.Errorf("padded anchor count: got %d want 4", got)
	}
}

func TestRunReturnsErrorOnNilClient(t *testing.T) {
	paths, meta := newTestHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)
	if _, err := Run(context.Background(), state, "x", io.Discard); err == nil {
		t.Fatalf("expected error for nil client")
	}
}

// TestRunPopulatesActiveThreadsOnNewTopic — after a *new-topic* turn,
// the new thread enters ActiveThreads at index 0.
func TestRunPopulatesActiveThreadsOnNewTopic(t *testing.T) {
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHi."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))
	if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(state.ActiveThreads) != 1 || state.ActiveThreads[0] != "thr_1" {
		t.Errorf("ActiveThreads after new-topic: got %v want [thr_1]", state.ActiveThreads)
	}
}

// TestRunActiveThreadsRefreshOnRepeatEngagement — repeating a turn on
// thr_1 should leave ActiveThreads = [thr_1] (no duplicate; LRU front).
func TestRunActiveThreadsRefreshOnRepeatEngagement(t *testing.T) {
	paths, meta := newTestHome(t)
	if err := store.AppendSpineRecord(paths, store.SpineRecord{
		ID: "thr_1", Project: meta.ID,
		Anchors: []string{"a", "b", "c", "d"},
		Summary: "thr_1", State: store.ThreadActive,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)
	state.ActiveThreads = []string{"thr_1"}
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))
	state.Client = model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1 [a, b, c, d]*\nFollow-up."},
	}, nil)
	if _, err := Run(context.Background(), state, "more", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(state.ActiveThreads) != 1 || state.ActiveThreads[0] != "thr_1" {
		t.Errorf("ActiveThreads: got %v want [thr_1]", state.ActiveThreads)
	}
}

// TestRunActiveThreadsLRUInsert — engaging thr_2 with thr_1 already
// active produces [thr_2, thr_1].
func TestRunActiveThreadsLRUInsert(t *testing.T) {
	paths, meta := newTestHome(t)
	for _, id := range []string{"thr_1", "thr_2"} {
		if err := store.AppendSpineRecord(paths, store.SpineRecord{
			ID: id, Project: meta.ID,
			Anchors: []string{"a", "b", "c", "d"},
			Summary: id, State: store.ThreadActive,
		}); err != nil {
			t.Fatalf("seed spine %s: %v", id, err)
		}
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)
	state.ActiveThreads = []string{"thr_1"}
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))
	state.Client = model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_2 [a, b, c, d]*\nNow on thr_2."},
	}, nil)
	if _, err := Run(context.Background(), state, "switch", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"thr_2", "thr_1"}
	if !sliceEqual(state.ActiveThreads, want) {
		t.Errorf("ActiveThreads: got %v want %v", state.ActiveThreads, want)
	}
}

// TestRunActiveThreadsBTopKOverflow — with BTopK=3 and three threads
// already active, engaging a fourth bumps the oldest into
// DormantThreads.
func TestRunActiveThreadsBTopKOverflow(t *testing.T) {
	paths, meta := newTestHome(t)
	for _, id := range []string{"thr_1", "thr_2", "thr_3", "thr_4"} {
		if err := store.AppendSpineRecord(paths, store.SpineRecord{
			ID: id, Project: meta.ID,
			Anchors: []string{"a", "b", "c", "d"},
			Summary: id, State: store.ThreadActive,
		}); err != nil {
			t.Fatalf("seed spine %s: %v", id, err)
		}
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)
	state.ActiveThreads = []string{"thr_3", "thr_2", "thr_1"} // index 0 = most recent
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))
	state.Client = model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_4 [a, b, c, d]*\nFourth thread."},
	}, nil)
	if _, err := Run(context.Background(), state, "fourth", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sliceEqual(state.ActiveThreads, []string{"thr_4", "thr_3", "thr_2"}) {
		t.Errorf("ActiveThreads: got %v want [thr_4, thr_3, thr_2]", state.ActiveThreads)
	}
	if !sliceEqual(state.DormantThreads, []string{"thr_1"}) {
		t.Errorf("DormantThreads: got %v want [thr_1]", state.DormantThreads)
	}
}

// TestUpdateLayerLRUDormantPromotionDeduplication — re-engaging a
// thread that was previously demoted to DormantThreads pulls it back
// up to ActiveThreads (and not to both layers simultaneously).
func TestUpdateLayerLRUDormantPromotionDeduplication(t *testing.T) {
	state := &State{
		Budget:         workset.DefaultBudget(),
		ActiveThreads:  []string{"thr_3", "thr_2"},
		DormantThreads: []string{"thr_1"},
	}
	updateLayerLRU(state, []string{"thr_1"})
	if !sliceEqual(state.ActiveThreads, []string{"thr_1", "thr_3", "thr_2"}) {
		t.Errorf("ActiveThreads: got %v", state.ActiveThreads)
	}
	if len(state.DormantThreads) != 0 {
		t.Errorf("DormantThreads: got %v want empty", state.DormantThreads)
	}
}

// TestUpdateLayerLRUDormantCap — DormantThreads must not exceed
// dormantThreadsCap.
func TestUpdateLayerLRUDormantCap(t *testing.T) {
	state := &State{
		Budget: workset.Budget{BTopK: 3},
	}
	// Engage many threads in sequence, far exceeding the cap.
	count := dormantThreadsCap + 10
	for i := 1; i <= count; i++ {
		updateLayerLRU(state, []string{itoaThreadID(i)})
	}
	if len(state.DormantThreads) > dormantThreadsCap {
		t.Errorf("DormantThreads exceeded cap: got %d want <= %d", len(state.DormantThreads), dormantThreadsCap)
	}
}

func itoaThreadID(n int) string { return "thr_" + itoa(n) }

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRunNewTopicWritesThreadFile verifies that *new-topic* creation
// produces a thread file at <Home>/threads/thr_<n>.md with the expected
// frontmatter and a single turn excerpt (spec §2.3).
func TestRunNewTopicWritesThreadFile(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nA brief greeting."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	thr, err := store.LoadThread(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Frontmatter.ID != "thr_1" {
		t.Errorf("frontmatter id: got %q want thr_1", thr.Frontmatter.ID)
	}
	if thr.Frontmatter.Project != meta.ID {
		t.Errorf("frontmatter project: got %q want %q", thr.Frontmatter.Project, meta.ID)
	}
	if thr.Frontmatter.State != store.ThreadWIP {
		t.Errorf("frontmatter state: got %q want wip", thr.Frontmatter.State)
	}
	if thr.Frontmatter.TurnCount != 1 {
		t.Errorf("frontmatter turn_count: got %d want 1", thr.Frontmatter.TurnCount)
	}
	if !strings.Contains(thr.Body, "## Turn 1") {
		t.Errorf("body missing turn 1 excerpt:\n%s", thr.Body)
	}
	if !strings.Contains(thr.Body, "**user:** hello") {
		t.Errorf("body missing user line:\n%s", thr.Body)
	}
	if !strings.Contains(thr.Body, "**agent:** A brief greeting.") {
		t.Errorf("body missing agent line:\n%s", thr.Body)
	}
	if strings.Contains(thr.Body, "*topic:") {
		t.Errorf("topic tag leaked into thread body:\n%s", thr.Body)
	}
	// All four anchors should be reflected in history_symbols.
	gotHist := map[string]store.SymbolSource{}
	for _, h := range thr.Frontmatter.HistorySymbols {
		gotHist[h.Normalized] = h.Source
	}
	for _, want := range []string{"foo", "bar", "baz", "qux"} {
		src, ok := gotHist[want]
		if !ok {
			t.Errorf("history_symbols missing %q: got %v", want, gotHist)
			continue
		}
		if src != store.SourceModel {
			t.Errorf("history_symbols %q source: got %q want model", want, src)
		}
	}
}

// TestRunExistingThreadAppendsExcerpt verifies that engagement on an
// existing thread loads the prior file, appends a new turn excerpt, and
// preserves earlier content. Spine and frontmatter must agree on
// turn_count and last_engaged after the write.
func TestRunExistingThreadAppendsExcerpt(t *testing.T) {
	paths, meta := newTestHome(t)

	// Seed: a thread file from a prior turn, plus its spine record.
	prior := store.Thread{
		Frontmatter: store.ThreadFrontmatter{
			ID:           "thr_42",
			Project:      meta.ID,
			Anchors:      []string{"trefoil", "unknot", "body-topology", "electron-shape"},
			Summary:      "topology",
			State:        store.ThreadActive,
			Created:      "2026-04-01T00:00:00Z",
			LastEngaged:  "2026-04-01T00:00:00Z",
			StateChanged: "2026-04-01T00:00:00Z",
			TurnCount:    7,
			HistorySymbols: []store.HistorySymbol{
				{Raw: "trefoil", Normalized: "trefoil", FirstSeenTurn: 1, Count: 5, Source: store.SourceModel},
			},
		},
		Body: "# trefoil\n\n## Turn 7 · 2026-04-01T00:00:00Z · [trefoil, unknot, body-topology, electron-shape]\n\n**user:** earlier prompt\n\n**agent:** earlier reply\n",
	}
	if err := store.SaveThread(paths, prior); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	if err := store.AppendSpineRecord(paths, store.SpineRecord{
		ID:           prior.Frontmatter.ID,
		Project:      prior.Frontmatter.Project,
		Anchors:      prior.Frontmatter.Anchors,
		Summary:      prior.Frontmatter.Summary,
		State:        prior.Frontmatter.State,
		Created:      prior.Frontmatter.Created,
		LastEngaged:  prior.Frontmatter.LastEngaged,
		StateChanged: prior.Frontmatter.StateChanged,
		TurnCount:    prior.Frontmatter.TurnCount,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [trefoil, unknot, body-topology, electron-shape]*\nFollow-up reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	// Pre-populate ActiveThreads so the §5.5 mid-turn fetch does not
	// fire — this test is about the close-time engagement-update path,
	// not the re-prompt path.
	state.ActiveThreads = []string{"thr_42"}
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	state.SetClock(fixedClock(now))

	if _, err := Run(context.Background(), state, "tell me more about #trefoil", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	thr, err := store.LoadThread(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Frontmatter.TurnCount != 8 {
		t.Errorf("frontmatter turn_count: got %d want 8", thr.Frontmatter.TurnCount)
	}
	if thr.Frontmatter.LastEngaged != now.Format(time.RFC3339) {
		t.Errorf("frontmatter last_engaged: got %q want %q", thr.Frontmatter.LastEngaged, now.Format(time.RFC3339))
	}
	if !strings.Contains(thr.Body, "**user:** earlier prompt") {
		t.Errorf("prior excerpt lost from body:\n%s", thr.Body)
	}
	if !strings.Contains(thr.Body, "**agent:** Follow-up reply.") {
		t.Errorf("new excerpt missing from body:\n%s", thr.Body)
	}
	if !strings.Contains(thr.Body, "## Turn 8") {
		t.Errorf("body missing turn 8 header:\n%s", thr.Body)
	}
	priorIdx := strings.Index(thr.Body, "Turn 7")
	newIdx := strings.Index(thr.Body, "Turn 8")
	if priorIdx < 0 || newIdx < 0 || priorIdx >= newIdx {
		t.Errorf("expected Turn 7 to precede Turn 8: priorIdx=%d newIdx=%d", priorIdx, newIdx)
	}

	// Spine and frontmatter must stay in sync.
	rec, found, err := store.FindSpineRecord(paths, "thr_42")
	if err != nil || !found {
		t.Fatalf("find: %v found=%v", err, found)
	}
	if rec.TurnCount != thr.Frontmatter.TurnCount {
		t.Errorf("spine turn_count %d != frontmatter %d", rec.TurnCount, thr.Frontmatter.TurnCount)
	}
	if rec.LastEngaged != thr.Frontmatter.LastEngaged {
		t.Errorf("spine last_engaged %q != frontmatter %q", rec.LastEngaged, thr.Frontmatter.LastEngaged)
	}
}

// TestRunHistorySymbolsAccumulation drives a sequence of turns where
// the same symbol is emitted under different sources; verifies that
// the history_symbols entry is incremented and its source upgrades per
// the §2.7.3 dominance rule (curator > user > model > deterministic).
func TestRunHistorySymbolsAccumulation(t *testing.T) {
	paths, meta := newTestHome(t)

	// Turn 1: model emits "alpha" (and three other anchors so the
	// new-topic flow accepts the cardinality).
	mock1 := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [alpha, beta, gamma, delta]*\nFirst."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock1)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))
	if _, err := Run(context.Background(), state, "first", io.Discard); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2: user supplies #alpha (source=user), model also tags alpha.
	state.Client = model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1 [alpha, beta, gamma, delta]*\nSecond."},
	}, nil)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 5, 0, 0, time.UTC)))
	if _, err := Run(context.Background(), state, "more on #alpha", io.Discard); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	thr, err := store.LoadThread(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	var alpha *store.HistorySymbol
	for i := range thr.Frontmatter.HistorySymbols {
		h := &thr.Frontmatter.HistorySymbols[i]
		if h.Normalized == "alpha" {
			alpha = h
			break
		}
	}
	if alpha == nil {
		t.Fatalf("history_symbols missing alpha; got %+v", thr.Frontmatter.HistorySymbols)
	}
	if alpha.Count != 2 {
		t.Errorf("alpha count: got %d want 2", alpha.Count)
	}
	// User-tag in turn 2 wins the dominance contest (user > model).
	if alpha.Source != store.SourceUser {
		t.Errorf("alpha source: got %q want user", alpha.Source)
	}
	if alpha.FirstSeenTurn != 1 {
		t.Errorf("alpha first_seen_turn: got %d want 1", alpha.FirstSeenTurn)
	}
}

// TestMergeHistorySymbolsCapAndEvict exercises the cap-and-evict rule
// directly (the Run-level integration would require ~40 distinct turns
// to push past the cap; the helper-level test is faster and gives a
// precise check on tie-breaking).
func TestMergeHistorySymbolsCapAndEvict(t *testing.T) {
	// Build a history that already sits at the cap: 40 entries, all
	// count=1, distinct first_seen_turn from 1..40.
	existing := make([]store.HistorySymbol, historyCapPerThread)
	for i := range historyCapPerThread {
		existing[i] = store.HistorySymbol{
			Raw:           "s" + itoa(i+1),
			Normalized:    "s" + itoa(i+1),
			FirstSeenTurn: i + 1,
			Count:         1,
			Source:        store.SourceModel,
		}
	}
	// Push two new symbols; they will tip the list to 42 entries, so
	// two of the lowest-count must be evicted. With all counts == 1,
	// the tie-break is lowest first_seen_turn — i.e. s1 and s2 should
	// be evicted.
	turn := []coalescedSymbol{
		{Normalized: "new1", Raw: "new1", Source: store.SourceModel},
		{Normalized: "new2", Raw: "new2", Source: store.SourceModel},
	}
	merged := mergeHistorySymbols(existing, turn, 41)
	if len(merged) != historyCapPerThread {
		t.Fatalf("len after merge: got %d want %d", len(merged), historyCapPerThread)
	}
	present := map[string]struct{}{}
	for _, h := range merged {
		present[h.Normalized] = struct{}{}
	}
	for _, dropped := range []string{"s1", "s2"} {
		if _, ok := present[dropped]; ok {
			t.Errorf("expected %s evicted (oldest among lowest-count)", dropped)
		}
	}
	for _, kept := range []string{"new1", "new2", "s40", "s39"} {
		if _, ok := present[kept]; !ok {
			t.Errorf("expected %s kept; merged=%v", kept, present)
		}
	}
}

// TestMergeHistorySymbolsCountWeightedEviction verifies that when
// counts differ, lower count is evicted regardless of recency.
func TestMergeHistorySymbolsCountWeightedEviction(t *testing.T) {
	// A high-count old entry must survive against many low-count newer
	// entries when overflow kicks in.
	existing := []store.HistorySymbol{
		{Raw: "old-popular", Normalized: "old-popular", FirstSeenTurn: 1, Count: 50, Source: store.SourceModel},
	}
	for i := range historyCapPerThread {
		existing = append(existing, store.HistorySymbol{
			Raw:           "young" + itoa(i),
			Normalized:    "young" + itoa(i),
			FirstSeenTurn: 100 + i,
			Count:         1,
			Source:        store.SourceModel,
		})
	}
	merged := mergeHistorySymbols(existing, nil, 200)
	if len(merged) != historyCapPerThread {
		t.Fatalf("len after merge: got %d want %d", len(merged), historyCapPerThread)
	}
	found := false
	for _, h := range merged {
		if h.Normalized == "old-popular" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("high-count entry was evicted; expected to survive")
	}
}

// seedThreadAndSpine writes a minimal-but-valid thread file plus the
// matching spine record for thrID. Returns nothing — t.Fatalf on any
// error so tests fail fast at setup.
func seedThreadAndSpine(t *testing.T, paths store.PersonantPaths, project, thrID string) {
	t.Helper()
	rec := store.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      []string{"a", "b", "c", "d"},
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
		Body: "# " + thrID + "\n\n## Turn 1 · 2026-04-01T00:00:00Z · [a, b, c, d]\n\n**user:** seed\n\n**agent:** seed reply\n",
	}
	if err := store.SaveThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// TestRunRePromptFiresForMissingThread — when the model's first-stream
// topic tag references a thread not in Layer B but loadable from disk,
// the runtime aborts the stream, fetches the thread, re-issues the
// request, and the user sees only the second response.
func TestRunRePromptFiresForMissingThread(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nFIRST."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nSECOND."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	var out bytes.Buffer
	body, err := Run(context.Background(), state, "ask about thr_42", &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Calls()); got != 2 {
		t.Fatalf("mock call count: got %d want 2", got)
	}
	if !strings.Contains(body, "SECOND") {
		t.Errorf("body missing SECOND: %q", body)
	}
	if strings.Contains(body, "FIRST") {
		t.Errorf("body unexpectedly contains FIRST: %q", body)
	}
	if !strings.Contains(out.String(), "SECOND") {
		t.Errorf("streamed out missing SECOND: %q", out.String())
	}
	if strings.Contains(out.String(), "FIRST") {
		t.Errorf("streamed out leaked aborted FIRST: %q", out.String())
	}
	if len(state.ActiveThreads) == 0 || state.ActiveThreads[0] != "thr_42" {
		t.Errorf("ActiveThreads: got %v want [thr_42, ...]", state.ActiveThreads)
	}
}

// TestRunRePromptSkippedWhenFetchFails — when the missing thread has no
// thread file on disk, fetch fails, no re-prompt is issued, and the
// user sees the first stream's body.
func TestRunRePromptSkippedWhenFetchFails(t *testing.T) {
	paths, meta := newTestHome(t)
	// Spine record exists but no thread file — LoadThread returns
	// ErrThreadFileNotFound.
	if err := store.AppendSpineRecord(paths, store.SpineRecord{
		ID: "thr_99", Project: meta.ID,
		Anchors: []string{"a", "b", "c", "d"},
		Summary: "thr_99", State: store.ThreadActive,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_99 [a, b, c, d]*\nFIRST."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	body, err := Run(context.Background(), state, "ask", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Calls()); got != 1 {
		t.Fatalf("mock call count: got %d want 1 (no re-prompt for unfetchable miss)", got)
	}
	if !strings.Contains(body, "FIRST") {
		t.Errorf("body missing FIRST: %q", body)
	}
}

// TestRunRePromptCappedAtOnePerTurn — even if the second response's tag
// references yet another missing thread (with a loadable file), the
// re-prompt cap holds: total stream attempts == 2, and the third
// thread is not fetched mid-turn (it would enter ActiveThreads at
// close via the LRU fallback path, not via §5.5).
func TestRunRePromptCappedAtOnePerTurn(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_99")
	seedThreadAndSpine(t, paths, meta.ID, "thr_88")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_99 [a, b, c, d]*\nFIRST."},
		{Content: "*topic: thr_88 [a, b, c, d]*\nSECOND."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	body, err := Run(context.Background(), state, "ask", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Calls()); got != 2 {
		t.Fatalf("mock call count: got %d want 2 (re-prompt capped at 1)", got)
	}
	if !strings.Contains(body, "SECOND") {
		t.Errorf("body missing SECOND: %q", body)
	}
	// thr_88 was referenced in response 2 — it enters ActiveThreads via
	// the close-time LRU update (not via mid-turn fetch).
	if len(state.ActiveThreads) == 0 || state.ActiveThreads[0] != "thr_88" {
		t.Errorf("ActiveThreads front: got %v want [thr_88, ...]", state.ActiveThreads)
	}
}

// TestRunNoRePromptWhenTagThreadsAlreadyActive — when every thread in
// the model's tag is already in Layer B, the runtime drains the stream
// without re-prompting.
func TestRunNoRePromptWhenTagThreadsAlreadyActive(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nbody."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.ActiveThreads = []string{"thr_42"}
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	body, err := Run(context.Background(), state, "ask", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Calls()); got != 1 {
		t.Fatalf("mock call count: got %d want 1 (no re-prompt; tag's threads already active)", got)
	}
	if !strings.Contains(body, "body") {
		t.Errorf("body: %q", body)
	}
}

// TestRunRePromptLogsThreadFetchedDelta — the §5.5 fetch must fire a
// thread.fetched context delta (logged as context.modified) and a
// topic.re-prompt event line.
func TestRunRePromptLogsThreadFetchedDelta(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nFIRST."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nSECOND."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	if _, err := Run(context.Background(), state, "ask", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The eventlog uses its own clock (real time.Now at write); read the
	// only file that exists in LogsDir.
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("logs dir entries: got %d want 1 (%v)", len(entries), entries)
	}
	data, err := os.ReadFile(paths.LogsDir + "/" + entries[0].Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logBody := string(data)
	if !strings.Contains(logBody, "context.modified source=thread.fetched") {
		t.Errorf("log missing thread.fetched context-modified line:\n%s", logBody)
	}
	if !strings.Contains(logBody, "topic.re-prompt") {
		t.Errorf("log missing topic.re-prompt line:\n%s", logBody)
	}
}
