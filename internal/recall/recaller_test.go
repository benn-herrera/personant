package recall_test

import (
	"context"
	"testing"

	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall"
	"personant/internal/store"
)

// newRecallHome initialises an isolated personant home and returns a
// fileadapter over it. External test package (recall_test) so it may
// import the fileadapter, which itself imports internal/recall.
func newRecallHome(t *testing.T) (store.PersonantPaths, *fileadapter.FileAdapter) {
	t.Helper()
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return paths, fileadapter.NewFileAdapter(paths)
}

// seedThread writes a spine record + thread file with the given
// anchors and body.
func seedThread(t *testing.T, paths store.PersonantPaths, id string, anchors []string, body string) {
	t.Helper()
	ts := "2026-04-01T00:00:00Z"
	rec := store.SpineRecord{
		ID: id, Project: "prj_1", Anchors: anchors, Summary: id,
		State: store.ThreadActive, Created: ts, LastEngaged: ts,
		StateChanged: ts, TurnCount: 1,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", id, err)
	}
	thr := store.Thread{
		Frontmatter: store.ThreadFrontmatter{
			ID: id, Project: "prj_1", Anchors: anchors, Summary: id,
			State: store.ThreadActive, Created: ts, LastEngaged: ts,
			StateChanged: ts, TurnCount: 1,
		},
		Body: body,
	}
	if err := store.SaveThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", id, err)
	}
}

func findResult(results []recall.Result, id string) (recall.Result, bool) {
	for _, r := range results {
		if r.ThreadID == id {
			return r, true
		}
	}
	return recall.Result{}, false
}

// TestService_SymbolicOnly: a Service with no embedder runs layer 1
// only — symbolic hits, no embedding hits.
func TestService_SymbolicOnly(t *testing.T) {
	paths, ops := newRecallHome(t)
	seedThread(t, paths, "thr_1", []string{"alpha", "beta", "gamma", "delta"}, "# thr_1")

	svc := recall.NewService(ops, nil)
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	results, err := svc.Recall(context.Background(), recall.Request{
		QuerySymbols: []string{"alpha", "beta", "gamma", "delta"},
		QueryText:    "anything",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	r, ok := findResult(results, "thr_1")
	if !ok {
		t.Fatalf("thr_1 not recalled; results=%+v", results)
	}
	if r.Symbolic == nil {
		t.Errorf("thr_1: symbolic hit missing")
	}
	if r.Embedding != nil {
		t.Errorf("thr_1: embedding hit present with no embedder: %+v", r.Embedding)
	}
}

// TestService_EmbeddingFires: a Service with an embedder runs layer 2 —
// a topically-matching thread gets an embedding hit.
func TestService_EmbeddingFires(t *testing.T) {
	paths, ops := newRecallHome(t)
	seedThread(t, paths, "thr_1", []string{"thr_1"}, "trefoil knot topology invariant chirality")
	seedThread(t, paths, "thr_2", []string{"thr_2"}, "neutrino oscillation flavor lepton boson")

	svc := recall.NewService(ops, model.NewMockEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	results, err := svc.Recall(context.Background(), recall.Request{
		QueryText: "trefoil knot topology invariant chirality",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	r, ok := findResult(results, "thr_1")
	if !ok || r.Embedding == nil {
		t.Fatalf("thr_1 should have an embedding hit; results=%+v", results)
	}
	if r2, ok := findResult(results, "thr_2"); ok && r2.Embedding != nil {
		t.Errorf("thr_2 (disjoint topic) should not get an embedding hit: %+v", r2)
	}
}

// TestService_MergesLayers: when a thread matches on both symbols and
// content, the merged Result carries both layer hits.
func TestService_MergesLayers(t *testing.T) {
	paths, ops := newRecallHome(t)
	seedThread(t, paths, "thr_1",
		[]string{"trefoil", "knot", "topology", "invariant"},
		"trefoil knot topology invariant chirality")

	svc := recall.NewService(ops, model.NewMockEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	results, err := svc.Recall(context.Background(), recall.Request{
		QuerySymbols: []string{"trefoil", "knot", "topology", "invariant"},
		QueryText:    "trefoil knot topology invariant chirality",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	r, ok := findResult(results, "thr_1")
	if !ok {
		t.Fatalf("thr_1 not recalled; results=%+v", results)
	}
	if r.Symbolic == nil || r.Embedding == nil {
		t.Errorf("thr_1 should fire both layers; got symbolic=%v embedding=%v",
			r.Symbolic, r.Embedding)
	}
	if len(r.Layers()) != 2 {
		t.Errorf("Layers() = %v, want both", r.Layers())
	}
}

// TestService_Exclude: an excluded thread never appears.
func TestService_Exclude(t *testing.T) {
	paths, ops := newRecallHome(t)
	seedThread(t, paths, "thr_1", []string{"alpha", "beta", "gamma", "delta"}, "alpha beta gamma delta")

	svc := recall.NewService(ops, model.NewMockEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	results, err := svc.Recall(context.Background(), recall.Request{
		QuerySymbols: []string{"alpha", "beta", "gamma", "delta"},
		QueryText:    "alpha beta gamma delta",
		Exclude:      map[string]struct{}{"thr_1": {}},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if _, ok := findResult(results, "thr_1"); ok {
		t.Errorf("excluded thr_1 appeared in results: %+v", results)
	}
}
