package turn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// seedThreadWithBody writes a thread (spine record + file) with a
// caller-controlled body — the embedding tests need the embedded text
// to be exactly what they specify, free of the boilerplate
// seedThreadWithAnchors injects.
func seedThreadWithBody(t *testing.T, paths store.PersonantPaths, project, thrID, body string) {
	t.Helper()
	rec := store.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      []string{thrID},
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
			ID: rec.ID, Project: rec.Project, Anchors: rec.Anchors,
			Summary: rec.Summary, State: rec.State, Created: rec.Created,
			LastEngaged: rec.LastEngaged, StateChanged: rec.StateChanged,
			TurnCount: rec.TurnCount,
		},
		Body: body,
	}
	if err := store.SaveThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// TestBuildEmbeddingIndex covers the index build: no-op without an
// Embedder, one ThreadVector per thread with one.
func TestBuildEmbeddingIndex(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithBody(t, paths, meta.ID, "thr_1", "trefoil knot topology invariant")
	seedThreadWithBody(t, paths, meta.ID, "thr_2", "neutrino oscillation flavor lepton")
	ops := fileadapter.NewFileAdapter(paths)

	t.Run("no embedder is a no-op", func(t *testing.T) {
		state := NewState(ops, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
		if err := state.BuildEmbeddingIndex(context.Background()); err != nil {
			t.Fatalf("BuildEmbeddingIndex: %v", err)
		}
		if state.embedIndex != nil {
			t.Errorf("embedIndex non-nil without an Embedder: %v", state.embedIndex)
		}
	})

	t.Run("with embedder, one vector per thread", func(t *testing.T) {
		state := NewState(ops, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
		state.Embedder = model.NewMockEmbedder()
		if err := state.BuildEmbeddingIndex(context.Background()); err != nil {
			t.Fatalf("BuildEmbeddingIndex: %v", err)
		}
		if len(state.embedIndex) != 2 {
			t.Fatalf("embedIndex len %d, want 2", len(state.embedIndex))
		}
		for _, tv := range state.embedIndex {
			if len(tv.Vector) == 0 {
				t.Errorf("%s: empty vector", tv.ThreadID)
			}
		}
	})
}

// TestSurfaceEmbeddingRecall covers the §3.4 layer-2 turn-close hook:
// it logs spine.embed-match-fire for cosine-similar threads and stays
// silent for dissimilar ones and when no Embedder is configured.
func TestSurfaceEmbeddingRecall(t *testing.T) {
	paths, meta := newTestHome(t)
	// Bodies are disjoint topic vocabularies — the mock embedder is a
	// feature-hasher, so token overlap drives cosine.
	seedThreadWithBody(t, paths, meta.ID, "thr_1", "trefoil knot topology invariant chirality")
	seedThreadWithBody(t, paths, meta.ID, "thr_2", "neutrino oscillation flavor lepton boson")
	ops := fileadapter.NewFileAdapter(paths)

	state := NewState(ops, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
	state.Embedder = model.NewMockEmbedder()
	if err := state.BuildEmbeddingIndex(context.Background()); err != nil {
		t.Fatalf("BuildEmbeddingIndex: %v", err)
	}

	surfaceEmbeddingRecall(context.Background(), state,
		"trefoil knot topology invariant", map[string]struct{}{})

	log := readDayLog(t, paths)
	if !strings.Contains(log, "spine.embed-match-fire thr_1") {
		t.Errorf("expected embed-match-fire for thr_1; log:\n%s", log)
	}
	if strings.Contains(log, "spine.embed-match-fire thr_2") {
		t.Errorf("thr_2 (disjoint topic) should not fire; log:\n%s", log)
	}
}

// TestSurfaceEmbeddingRecall_NoEmbedder confirms graceful absence: no
// Embedder → no embedding recall, no log noise. surfaceEmbeddingRecall
// returns before any log write, so the logs dir stays empty (the
// eventlog only creates a day file on first write).
func TestSurfaceEmbeddingRecall_NoEmbedder(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithBody(t, paths, meta.ID, "thr_1", "trefoil knot topology")
	ops := fileadapter.NewFileAdapter(paths)

	state := NewState(ops, meta, store.Provider{}, model.NewScriptedMock(nil, nil))
	surfaceEmbeddingRecall(context.Background(), state, "trefoil knot topology", map[string]struct{}{})

	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if strings.Contains(string(data), "embed-match-fire") {
			t.Errorf("embedding recall ran without an Embedder:\n%s", data)
		}
	}
}
