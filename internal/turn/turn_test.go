package turn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// newTestHome scaffolds the minimum home layout the turn package needs:
// logs/, projects/<id>/meta.json, and an empty spine file.
func newTestHome(t *testing.T) (store.PersonantPaths, memops.ProjectMeta) {
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
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: tmp}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	return paths, meta
}

// pinClock installs a fixed clock.Timeline override for the test and
// registers a cleanup that restores the previous source. The override is
// process-global, so the cleanup is what keeps runs isolated.
func pinClock(t *testing.T, when time.Time) {
	t.Helper()
	restore := clock.SetTimeline(func() time.Time { return when })
	t.Cleanup(restore)
}

func TestRunNewTopicCreatesSpineRecord(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{
			Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
		},
	}, nil)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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
	if r.State != memops.ThreadActive {
		t.Errorf("state: got %q want active", r.State)
	}
	if r.TurnCount != 1 {
		t.Errorf("turn_count: got %d want 1", r.TurnCount)
	}
	if len(r.Anchors) < memops.MinAnchorsPerThread || len(r.Anchors) > memops.MaxAnchorsPerThread {
		t.Errorf("anchor cardinality: got %d", len(r.Anchors))
	}
}

func TestRunUpdatesExistingThread(t *testing.T) {
	paths, meta := newTestHome(t)

	existing := memops.SpineRecord{
		ID:           "thr_42",
		Project:      meta.ID,
		Anchors:      []string{"trefoil", "unknot", "body-topology", "electron-shape"},
		Summary:      "topology",
		State:        memops.ThreadActive,
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

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	pinClock(t, now)

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

	existing := memops.SpineRecord{
		ID:        "thr_42",
		Project:   meta.ID,
		Anchors:   []string{"alpha", "beta", "gamma", "delta"},
		Summary:   "test thread",
		State:     memops.ThreadActive,
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
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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

	// Two anchors only — under the §2.2 hard range
	// [memops.MinAnchorsPerThread, memops.MaxAnchorsPerThread].
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [only-two, anchors]*\nBrief reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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
	if got := len(r.Anchors); got != memops.MinAnchorsPerThread {
		t.Errorf("padded anchor count: got %d want %d", got, memops.MinAnchorsPerThread)
	}
}

func TestRunReturnsErrorOnNilClient(t *testing.T) {
	paths, meta := newTestHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
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
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
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
	if err := store.AppendSpineRecord(paths, memops.SpineRecord{
		ID: "thr_1", Project: meta.ID,
		Anchors: []string{"a", "b", "c", "d"},
		Summary: "thr_1", State: memops.ThreadActive,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	state.ActiveThreads = []string{"thr_1"}
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
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
		if err := store.AppendSpineRecord(paths, memops.SpineRecord{
			ID: id, Project: meta.ID,
			Anchors: []string{"a", "b", "c", "d"},
			Summary: id, State: memops.ThreadActive,
		}); err != nil {
			t.Fatalf("seed spine %s: %v", id, err)
		}
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	state.ActiveThreads = []string{"thr_1"}
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
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
		if err := store.AppendSpineRecord(paths, memops.SpineRecord{
			ID: id, Project: meta.ID,
			Anchors: []string{"a", "b", "c", "d"},
			Summary: id, State: memops.ThreadActive,
		}); err != nil {
			t.Fatalf("seed spine %s: %v", id, err)
		}
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	state.ActiveThreads = []string{"thr_3", "thr_2", "thr_1"} // index 0 = most recent
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
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
		Budget:         memops.DefaultBudget(),
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
		Budget: memops.Budget{BTopK: 3},
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

// TestRunHistoryCapTurnPairFIFO drives the State through more than
// sessionHistoryCapTurns turn pairs and asserts:
//   - History length stays at or below 2*sessionHistoryCapTurns
//   - the most-recent sessionHistoryCapTurns pairs are present
//   - oldest pairs evicted FIFO (in order), and the slice still
//     alternates user/assistant with no orphan
//
// Per MAD architecture-review burn-down item B1 (= T1-2).
func TestRunHistoryCapTurnPairFIFO(t *testing.T) {
	paths, meta := newTestHome(t)

	// Seed an existing thread so every scripted turn engages the same
	// thread tag — keeps the turn loop on the steady-state path
	// (existing-thread update) without exercising new-topic anchor
	// validation for every iteration.
	existing := memops.SpineRecord{
		ID:           "thr_1",
		Project:      meta.ID,
		Anchors:      []string{"alpha", "beta", "gamma", "delta"},
		Summary:      "history-cap-fixture",
		State:        memops.ThreadActive,
		Created:      "2026-04-01T00:00:00Z",
		LastEngaged:  "2026-04-01T00:00:00Z",
		StateChanged: "2026-04-01T00:00:00Z",
		TurnCount:    1,
	}
	if err := store.AppendSpineRecord(paths, existing); err != nil {
		t.Fatalf("append: %v", err)
	}

	mock := model.NewScriptedMock(nil, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	const turns = sessionHistoryCapTurns + 5
	for i := 0; i < turns; i++ {
		mock.SetResponse(model.Response{
			Content: "*topic: thr_1 [alpha, beta, gamma, delta]*\nassistant-" + itoa(i),
		})
		userInput := "user-" + itoa(i)
		if _, err := Run(context.Background(), state, userInput, io.Discard); err != nil {
			t.Fatalf("Run turn %d: %v", i, err)
		}
	}

	wantLen := 2 * sessionHistoryCapTurns
	if got := len(state.History); got != wantLen {
		t.Fatalf("History len: got %d want %d", got, wantLen)
	}

	// History should hold the LAST sessionHistoryCapTurns pairs, in
	// order, alternating user/assistant. After `turns` total runs, the
	// oldest surviving pair is turn index (turns - sessionHistoryCapTurns).
	firstSurviving := turns - sessionHistoryCapTurns
	for p := 0; p < sessionHistoryCapTurns; p++ {
		idx := turn2idx(firstSurviving + p)
		userMsg := state.History[2*p]
		asstMsg := state.History[2*p+1]
		if userMsg.Role != "user" {
			t.Errorf("pair %d user role: got %q want user", p, userMsg.Role)
		}
		if asstMsg.Role != "assistant" {
			t.Errorf("pair %d assistant role: got %q want assistant", p, asstMsg.Role)
		}
		if want := "user-" + itoa(idx); userMsg.Content != want {
			t.Errorf("pair %d user content: got %q want %q", p, userMsg.Content, want)
		}
		// The assistant Content includes the topic-tag preamble; check
		// the body suffix to confirm FIFO ordering.
		if want := "assistant-" + itoa(idx); !strings.HasSuffix(asstMsg.Content, want) {
			t.Errorf("pair %d assistant content: got %q want suffix %q", p, asstMsg.Content, want)
		}
	}

	// Sanity: the oldest evicted pair (turn 0) must not be present
	// anywhere in History.
	for i, m := range state.History {
		if strings.Contains(m.Content, "user-0") || strings.Contains(m.Content, "assistant-0") {
			t.Errorf("evicted turn 0 leaked at index %d: %q", i, m.Content)
		}
	}
}

// turn2idx is an identity helper that exists only to make the index
// math in TestRunHistoryCapTurnPairFIFO read as intent rather than
// arithmetic.
func turn2idx(n int) int { return n }

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
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	thr, err := store.LoadThread(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Meta.ID != "thr_1" {
		t.Errorf("frontmatter id: got %q want thr_1", thr.Meta.ID)
	}
	if thr.Meta.Project != meta.ID {
		t.Errorf("frontmatter project: got %q want %q", thr.Meta.Project, meta.ID)
	}
	if thr.Meta.State != memops.ThreadActive {
		t.Errorf("frontmatter state: got %q want active", thr.Meta.State)
	}
	if thr.Meta.TurnCount != 1 {
		t.Errorf("frontmatter turn_count: got %d want 1", thr.Meta.TurnCount)
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
	gotHist := map[string]memops.SymbolSource{}
	for _, h := range thr.Meta.HistorySymbols {
		gotHist[h.Normalized] = h.Source
	}
	for _, want := range []string{"foo", "bar", "baz", "qux"} {
		src, ok := gotHist[want]
		if !ok {
			t.Errorf("history_symbols missing %q: got %v", want, gotHist)
			continue
		}
		if src != memops.SourceModel {
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
	prior := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:           "thr_42",
			Project:      meta.ID,
			Anchors:      []string{"trefoil", "unknot", "body-topology", "electron-shape"},
			Summary:      "topology",
			State:        memops.ThreadActive,
			Created:      "2026-04-01T00:00:00Z",
			LastEngaged:  "2026-04-01T00:00:00Z",
			StateChanged: "2026-04-01T00:00:00Z",
			TurnCount:    7,
			HistorySymbols: []memops.HistorySymbol{
				{Raw: "trefoil", Normalized: "trefoil", FirstSeenTurn: 1, Count: 5, Source: memops.SourceModel},
			},
		},
		Body: "# trefoil\n\n## Turn 7 · 2026-04-01T00:00:00Z · [trefoil, unknot, body-topology, electron-shape]\n\n**user:** earlier prompt\n\n**agent:** earlier reply\n",
	}
	if err := store.SeedThread(paths, prior); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	if err := store.AppendSpineRecord(paths, memops.SpineRecord{
		ID:           prior.Meta.ID,
		Project:      prior.Meta.Project,
		Anchors:      prior.Meta.Anchors,
		Summary:      prior.Meta.Summary,
		State:        prior.Meta.State,
		Created:      prior.Meta.Created,
		LastEngaged:  prior.Meta.LastEngaged,
		StateChanged: prior.Meta.StateChanged,
		TurnCount:    prior.Meta.TurnCount,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [trefoil, unknot, body-topology, electron-shape]*\nFollow-up reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	// Pre-populate ActiveThreads so the §5.5 mid-turn fetch does not
	// fire — this test is about the close-time engagement-update path,
	// not the re-prompt path.
	state.ActiveThreads = []string{"thr_42"}
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	pinClock(t, now)

	if _, err := Run(context.Background(), state, "tell me more about #trefoil", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	thr, err := store.LoadThread(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Meta.TurnCount != 8 {
		t.Errorf("frontmatter turn_count: got %d want 8", thr.Meta.TurnCount)
	}
	if thr.Meta.LastEngaged != now.Format(time.RFC3339) {
		t.Errorf("frontmatter last_engaged: got %q want %q", thr.Meta.LastEngaged, now.Format(time.RFC3339))
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
	if rec.TurnCount != thr.Meta.TurnCount {
		t.Errorf("spine turn_count %d != frontmatter %d", rec.TurnCount, thr.Meta.TurnCount)
	}
	if rec.LastEngaged != thr.Meta.LastEngaged {
		t.Errorf("spine last_engaged %q != frontmatter %q", rec.LastEngaged, thr.Meta.LastEngaged)
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
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock1)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	if _, err := Run(context.Background(), state, "first", io.Discard); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2: user supplies #alpha (source=user), model also tags alpha.
	state.Client = model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1 [alpha, beta, gamma, delta]*\nSecond."},
	}, nil)
	pinClock(t, time.Date(2026, 5, 9, 12, 5, 0, 0, time.UTC))
	if _, err := Run(context.Background(), state, "more on #alpha", io.Discard); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	thr, err := store.LoadThread(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	var alpha *memops.HistorySymbol
	for i := range thr.Meta.HistorySymbols {
		h := &thr.Meta.HistorySymbols[i]
		if h.Normalized == "alpha" {
			alpha = h
			break
		}
	}
	if alpha == nil {
		t.Fatalf("history_symbols missing alpha; got %+v", thr.Meta.HistorySymbols)
	}
	if alpha.Count != 2 {
		t.Errorf("alpha count: got %d want 2", alpha.Count)
	}
	// User-tag in turn 2 wins the dominance contest (user > model).
	if alpha.Source != memops.SourceUser {
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
	existing := make([]memops.HistorySymbol, historyCapPerThread)
	for i := range historyCapPerThread {
		existing[i] = memops.HistorySymbol{
			Raw:           "s" + itoa(i+1),
			Normalized:    "s" + itoa(i+1),
			FirstSeenTurn: i + 1,
			Count:         1,
			Source:        memops.SourceModel,
		}
	}
	// Push two new symbols; they will tip the list to 42 entries, so
	// two of the lowest-count must be evicted. With all counts == 1,
	// the tie-break is lowest first_seen_turn — i.e. s1 and s2 should
	// be evicted.
	turn := []coalescedSymbol{
		{Normalized: "new1", Raw: "new1", Source: memops.SourceModel},
		{Normalized: "new2", Raw: "new2", Source: memops.SourceModel},
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
	existing := []memops.HistorySymbol{
		{Raw: "old-popular", Normalized: "old-popular", FirstSeenTurn: 1, Count: 50, Source: memops.SourceModel},
	}
	for i := range historyCapPerThread {
		existing = append(existing, memops.HistorySymbol{
			Raw:           "young" + itoa(i),
			Normalized:    "young" + itoa(i),
			FirstSeenTurn: 100 + i,
			Count:         1,
			Source:        memops.SourceModel,
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
	rec := memops.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      []string{"a", "b", "c", "d"},
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
		Body: "# " + thrID + "\n\n## Turn 1 · 2026-04-01T00:00:00Z · [a, b, c, d]\n\n**user:** seed\n\n**agent:** seed reply\n",
	}
	if err := store.SeedThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// TestRunRePromptFiresForMissingThread — when the model's first-stream
// topic tag references a thread not in Layer B but loadable from disk,
// the runtime aborts the stream, fetches the thread, and re-issues the
// request. The scripted mock serves the same response per turn (the
// §5.5 re-prompt re-issues the same request, so the model emits the
// same tag + body), so the assertion is that the body appears exactly
// once — the aborted first stream must not leak a duplicate.
func TestRunRePromptFiresForMissingThread(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nBODY."},
	}, nil)
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	var out bytes.Buffer
	body, err := Run(context.Background(), state, "ask about thr_42", &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Calls()); got != 2 {
		t.Fatalf("mock call count: got %d want 2 (first stream + re-prompt)", got)
	}
	// The re-prompt re-issues the request; the aborted first stream must
	// not leak, so the body appears exactly once.
	if n := strings.Count(body, "BODY."); n != 1 {
		t.Errorf("body should contain BODY. exactly once (aborted stream leaked?): got %d in %q", n, body)
	}
	if n := strings.Count(out.String(), "BODY."); n != 1 {
		t.Errorf("streamed out should contain BODY. exactly once: got %d in %q", n, out.String())
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
	if err := store.AppendSpineRecord(paths, memops.SpineRecord{
		ID: "thr_99", Project: meta.ID,
		Anchors: []string{"a", "b", "c", "d"},
		Summary: "thr_99", State: memops.ThreadActive,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_99 [a, b, c, d]*\nFIRST."},
	}, nil)
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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

	// Per-consult mock: the cap test needs the re-prompt to see a
	// DIFFERENT response than the aborted first stream — response 2's
	// tag names yet another missing thread, and the cap must still hold.
	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "*topic: thr_99 [a, b, c, d]*\nFIRST."},
		{Content: "*topic: thr_88 [a, b, c, d]*\nSECOND."},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_42"}
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

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

// TestRunBuffersFileEditsIntoThreadStore — the §3.9 checkpoint 5c path:
// fs.read / fs.write / fs.commit PreEvents are buffered during the turn
// and applied to the engaged thread's tracked-file sidecar at close.
func TestRunBuffersFileEditsIntoThreadStore(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nDone."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))

	pre := []Delta{
		{Source: "fs.read", Content: "v0", Meta: map[string]string{"path": "src/a.go"}},
		{Source: "fs.write", Content: "v1", Meta: map[string]string{"path": "src/a.go"}},
		{Source: "fs.commit", Meta: map[string]string{"path": "src/a.go", "hash": "deadbeef"}},
	}
	if _, err := RunWithDeltas(context.Background(), state, pre, "edit it", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}

	tf, err := store.LoadThreadFiles(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	e, ok := tf.Entry("src/a.go")
	if !ok {
		t.Fatal("src/a.go not tracked after turn close")
	}
	// fs.read seeds the chain with "v0"; fs.write appends "v1" → len 2.
	if e.Chain.Len() != 2 {
		t.Errorf("Chain.Len = %d, want 2", e.Chain.Len())
	}
	if e.Chain.Current() != "v1" {
		t.Errorf("Chain.Current = %q, want v1", e.Chain.Current())
	}
	// fs.commit lands after the write (the write is "v1"); the commit
	// pointer is the synthetic hash, stamped by the adapter's clock.
	if e.LastCommit != "deadbeef" {
		t.Errorf("LastCommit = %q, want deadbeef", e.LastCommit)
	}
	if e.CommittedAt == "" {
		t.Error("CommittedAt empty after fs.commit")
	}
}

// TestRunFileCommitUntrackedIsNonFatal — an fs.commit with no preceding
// write hits an untracked path; RecordFileCommit errors, but the turn
// must still complete (the error is logged, not propagated).
func TestRunFileCommitUntrackedIsNonFatal(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nDone."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))

	pre := []Delta{
		{Source: "fs.commit", Meta: map[string]string{"path": "never-written.go", "hash": "h"}},
	}
	if _, err := RunWithDeltas(context.Background(), state, pre, "commit it", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas must not fail on untracked commit: %v", err)
	}

	tf, err := store.LoadThreadFiles(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if _, ok := tf.Entry("never-written.go"); ok {
		t.Error("untracked commit should not have created a file entry")
	}
}

// TestRunFileEditWithoutTopicTagFailsLoud — MAD B2 / T1-3 fail-loud
// branch. When fs.write is buffered but the model emits NO topic tag,
// the turn must abort with ErrFileEditWithoutTopicTag, no spine record
// must be appended, no thread sidecar must be created, and the
// unsynced workspace paths must be logged via internal/log.
//
// The workspace file itself is out of scope here — fs.write at the
// tool-call layer is OS-level and irreversible; the substrate's
// contract is only that the canonical record does not silently absorb
// the edit.
func TestRunFileEditWithoutTopicTagFailsLoud(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "Just a plain response with no topic tag."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC))

	pre := []Delta{
		{Source: "fs.write", Content: "v1", Meta: map[string]string{"path": "src/a.go"}},
		{Source: "fs.write", Content: "v1", Meta: map[string]string{"path": "src/b.go"}},
	}
	_, err := RunWithDeltas(context.Background(), state, pre, "edit it", io.Discard)
	if err == nil {
		t.Fatal("RunWithDeltas must return an error when fs.write is buffered without a topic tag")
	}
	if !errors.Is(err, ErrFileEditWithoutTopicTag) {
		t.Errorf("error = %v; want errors.Is ErrFileEditWithoutTopicTag", err)
	}
	// The error message must name the unsynced paths so an upstream
	// tutoring/diagnostic layer can teach the model what it omitted.
	msg := err.Error()
	for _, want := range []string{"src/a.go", "src/b.go"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing path %q", msg, want)
		}
	}

	// Substrate state must NOT have advanced: no spine records, no
	// thread sidecar for either path. The workspace file itself is not
	// the substrate's responsibility — only the canonical record is
	// asserted here.
	records, rerr := store.ReadSpine(paths.Spine)
	if rerr != nil {
		t.Fatalf("read spine: %v", rerr)
	}
	if len(records) != 0 {
		t.Errorf("spine must not advance on protocol-violation abort; got %d records", len(records))
	}

	// Log surface: one unsynced-no-topic-tag line per distinct path,
	// so a human can reconcile the workspace if they care.
	entries, lerr := os.ReadDir(paths.LogsDir)
	if lerr != nil {
		t.Fatalf("read logs dir: %v", lerr)
	}
	if len(entries) != 1 {
		t.Fatalf("logs dir entries: got %d want 1 (%v)", len(entries), entries)
	}
	data, rerr := os.ReadFile(paths.LogsDir + "/" + entries[0].Name())
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	logBody := string(data)
	for _, want := range []string{
		"unsynced-no-topic-tag path=src/a.go",
		"unsynced-no-topic-tag path=src/b.go",
	} {
		if !strings.Contains(logBody, want) {
			t.Errorf("log missing %q:\n%s", want, logBody)
		}
	}
}

// TestRunFileEditWithTopicTagSucceeds — companion to the fail-loud
// path above. With a valid topic tag in the same turn as an fs.write,
// the turn must succeed and the §3.9 sidecar must absorb the edit.
// This is the baseline that TestRunBuffersFileEditsIntoThreadStore
// already exercises end-to-end; we re-assert the contract here in the
// vocabulary of the MAD B2 fix (error nil, sidecar populated) so a
// regression on the fail-loud branch cannot silently flip this case.
func TestRunFileEditWithTopicTagSucceeds(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nDone."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC))

	pre := []Delta{
		{Source: "fs.write", Content: "v1", Meta: map[string]string{"path": "src/a.go"}},
	}
	if _, err := RunWithDeltas(context.Background(), state, pre, "edit it", io.Discard); err != nil {
		t.Fatalf("RunWithDeltas with topic tag must succeed: %v", err)
	}
	tf, err := store.LoadThreadFiles(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if _, ok := tf.Entry("src/a.go"); !ok {
		t.Error("src/a.go must be tracked when fs.write rides a valid topic tag")
	}
}
