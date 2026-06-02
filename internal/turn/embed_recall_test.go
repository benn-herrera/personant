package turn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
)

// seedThreadWithBody writes a thread (spine record + file) with a
// caller-controlled body — the embedding tests need the embedded text
// to be exactly what they specify, free of the boilerplate
// seedThreadWithAnchors injects.
func seedThreadWithBody(t *testing.T, paths store.PersonantPaths, project, thrID, body string) {
	t.Helper()
	rec := memops.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      []string{thrID},
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
			ID: rec.ID, Project: rec.Project, Anchors: rec.Anchors,
			Summary: rec.Summary, State: rec.State, Created: rec.Created,
			LastEngaged: rec.LastEngaged, StateChanged: rec.StateChanged,
			TurnCount: rec.TurnCount,
		},
		Body: body,
	}
	if err := store.SeedThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// embeddingState builds a turn State whose Recaller is embedding-enabled
// (a measure.Service over a MockEmbedder), with its index prepared.
func embeddingState(t *testing.T, paths store.PersonantPaths, meta memops.ProjectMeta) *State {
	t.Helper()
	ops := fileadapter.NewFileAdapter(paths)
	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.Recaller = measure.NewService(ops, model.NewMockEmbedder())
	if err := state.Recaller.Prepare(context.Background()); err != nil {
		t.Fatalf("Recaller.Prepare: %v", err)
	}
	return state
}

// TestSurfaceRecall_EmbeddingFires covers the §3.4 layer-2 turn-close
// hook through the Recaller: surfaceRecallCandidates logs
// spine.embed-match-fire for a cosine-similar thread and stays silent
// for a dissimilar one.
func TestSurfaceRecall_EmbeddingFires(t *testing.T) {
	paths, meta := newTestHome(t)
	// Disjoint topic vocabularies — the mock embedder is a feature
	// hasher, so token overlap drives cosine.
	seedThreadWithBody(t, paths, meta.ID, "thr_1", "trefoil knot topology invariant chirality")
	seedThreadWithBody(t, paths, meta.ID, "thr_2", "neutrino oscillation flavor lepton boson")
	state := embeddingState(t, paths, meta)

	if err := surfaceRecallCandidates(context.Background(), state,
		"trefoil knot topology invariant", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	log := readDayLog(t, paths)
	if !strings.Contains(log, "spine.embed-match-fire thr_1") {
		t.Errorf("expected embed-match-fire for thr_1; log:\n%s", log)
	}
	if strings.Contains(log, "spine.embed-match-fire thr_2") {
		t.Errorf("thr_2 (disjoint topic) should not fire; log:\n%s", log)
	}
}

// TestSurfaceRecall_IntraThreadFires covers the §7 turn-close emission of
// spine.intra-match-fire: when the engaged thread has a scrolled-out
// early-content chunk that matches the query, surfaceRecallCandidates logs
// the additive intra-match-fire line for it (the §8 oracle's observable).
// It drives the real Recaller end-to-end — the engaged thread is passed as
// engagedOwner, so the §4.1 step-3 fine pass admits its chunks by ID.
func TestSurfaceRecall_IntraThreadFires(t *testing.T) {
	paths, meta := newTestHome(t)
	// Per-turn body: an early turn topically distinct from the rest, so a
	// probe for it retrieves that scrolled-out chunk.
	body := "## Turn 1\ntrefoil knot topology invariant chirality\n\n" +
		"## Turn 2\nmonsoon humidity precipitation tropics\n\n" +
		"## Turn 3\nledger reconciliation accrual depreciation\n"
	seedThreadWithBody(t, paths, meta.ID, "thr_1", body)

	ops := fileadapter.NewFileAdapter(paths)
	svc := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Recaller.Prepare: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.Recaller = svc

	// Flush the thread's chunks into the fine tier and wait for the async
	// indexer to publish a snapshot that surfaces the early chunk — a stuck
	// indexer is a real defect, not flakiness.
	svc.EnqueueFlush("thr_1", 3)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		results, err := svc.Recall(context.Background(), measure.Request{
			QueryText: "trefoil knot topology invariant chirality",
			Engaged:   "thr_1",
			Exclude:   map[string]struct{}{"thr_1": {}},
		})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if r, ok := findResultByID(results, "thr_1"); ok && r.IntraThread != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Now run the turn-close emission path with the engaged thread excluded
	// at the thread level but passed as engagedOwner (the #109 bypass).
	if err := surfaceRecallCandidates(context.Background(), state,
		"trefoil knot topology invariant chirality",
		map[string]struct{}{"thr_1": {}}, "thr_1"); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	log := readDayLog(t, paths)
	if !strings.Contains(log, "spine.intra-match-fire thr_1") {
		t.Errorf("expected intra-match-fire for thr_1; log:\n%s", log)
	}
}

// findResultByID returns the result for threadID among results.
func findResultByID(results []measure.Result, threadID string) (measure.Result, bool) {
	for _, r := range results {
		if r.ThreadID == threadID {
			return r, true
		}
	}
	return measure.Result{}, false
}

// TestSurfaceRecall_NoEmbedder confirms graceful absence: the default
// symbolic-only Recaller produces no embedding matches. With an empty
// coalesce buffer there is no symbolic match either, so nothing logs.
func TestSurfaceRecall_NoEmbedder(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithBody(t, paths, meta.ID, "thr_1", "trefoil knot topology")
	ops := fileadapter.NewFileAdapter(paths)

	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	if err := surfaceRecallCandidates(context.Background(), state,
		"trefoil knot topology", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}

	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if strings.Contains(string(data), "embed-match-fire") {
			t.Errorf("embedding recall ran without an embedder:\n%s", data)
		}
	}
}
