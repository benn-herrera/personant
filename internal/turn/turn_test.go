package turn

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/model"
	"personant/internal/store"
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

	state := NewState(paths, meta, store.Provider{}, mock)
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

	state := NewState(paths, meta, store.Provider{}, mock)
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
	state := NewState(paths, meta, store.Provider{}, mock)
	state.SetClock(fixedClock(time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)))

	if err := onContextDelta(state, Delta{Source: "user.prompt", Content: "asking about #alpha and #beta"}); err != nil {
		t.Fatalf("user.prompt: %v", err)
	}
	if err := onContextDelta(state, Delta{Source: "model.response", Content: "*topic: thr_42 [alpha, beta, gamma, delta]*\nResponse."}); err != nil {
		t.Fatalf("model.response: %v", err)
	}
	if err := closeTurnAndUpdateEngagement(state, "asking about #alpha and #beta", "Response."); err != nil {
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
	state := NewState(paths, meta, store.Provider{}, mock)
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
	state := NewState(paths, meta, store.Provider{}, mock)
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
	state := NewState(paths, meta, store.Provider{}, nil)
	if _, err := Run(context.Background(), state, "x", io.Discard); err == nil {
		t.Fatalf("expected error for nil client")
	}
}

// TestRunNewTopicWritesThreadFile verifies that *new-topic* creation
// produces a thread file at <Home>/threads/thr_<n>.md with the expected
// frontmatter and a single turn excerpt (spec §2.3).
func TestRunNewTopicWritesThreadFile(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nA brief greeting."},
	}, nil)
	state := NewState(paths, meta, store.Provider{}, mock)
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
	state := NewState(paths, meta, store.Provider{}, mock)
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
	state := NewState(paths, meta, store.Provider{}, mock1)
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
	for i := 0; i < historyCapPerThread; i++ {
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
	for i := 0; i < historyCapPerThread; i++ {
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
