package turn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		Frontmatter: memops.ThreadMeta{
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
		"trefoil knot topology invariant", map[string]struct{}{}); err != nil {
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

// TestSurfaceRecall_NoEmbedder confirms graceful absence: the default
// symbolic-only Recaller produces no embedding matches. With an empty
// coalesce buffer there is no symbolic match either, so nothing logs.
func TestSurfaceRecall_NoEmbedder(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithBody(t, paths, meta.ID, "thr_1", "trefoil knot topology")
	ops := fileadapter.NewFileAdapter(paths)

	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	if err := surfaceRecallCandidates(context.Background(), state,
		"trefoil knot topology", map[string]struct{}{}); err != nil {
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
