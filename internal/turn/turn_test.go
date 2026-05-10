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
	if err := closeTurnAndUpdateEngagement(state, "Response."); err != nil {
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
