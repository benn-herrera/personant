//go:build integration

// Live integration test for embedding recall through the runtime path.
// Build-tag isolated (`integration`), run via `make integration-test`;
// needs the `reaper` provider reachable. It exercises the real
// HTTPEmbedder end to end — BuildEmbeddingIndex over seeded threads,
// then a cosine match — confirming the runtime wiring works against an
// actual embedding model, not just the mock.

package turn

import (
	"context"
	"os"
	"strings"
	"testing"

	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall"
	"personant/internal/store"
)

func reaperProvider() store.Provider {
	url := os.Getenv("PERSONANT_REAPER_URL")
	if url == "" {
		url = "http://reaper.local:4000/v1"
	}
	return store.Provider{
		Name:           "reaper",
		BaseURL:        url,
		APIKey:         "dummy",
		EmbeddingModel: "nomicai-embed",
	}
}

func skipIfUnreachable(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "timeout") {
		t.Skipf("reaper unreachable — integration test needs it running: %v", err)
	}
	t.Fatalf("embedding recall: %v", err)
}

// TestEmbeddingRecall_Live builds the embedding index over two
// disjoint-topic threads with the real embedder, then checks that a
// knot-theory query ranks the knot thread above the neutrino thread.
//
// The assertion is on ranking, not on clearing the cosine threshold —
// ranking is robust to the absolute-similarity calibration, which the
// C.6 sweep already covers.
func TestEmbeddingRecall_Live(t *testing.T) {
	provider := reaperProvider()
	paths, meta := newTestHome(t)
	seedThreadWithBody(t, paths, meta.ID, "thr_1",
		"The trefoil knot is the simplest nontrivial knot in knot theory. "+
			"It has three crossings and a crossing number of three; it is chiral.")
	seedThreadWithBody(t, paths, meta.ID, "thr_2",
		"Neutrino oscillation is the quantum phenomenon in which a neutrino "+
			"changes lepton flavor as it propagates, implying nonzero neutrino mass.")
	ops := fileadapter.NewFileAdapter(paths)

	state := NewState(ops, meta, provider, model.NewScriptedMock(nil, nil))
	state.Embedder = model.NewHTTPEmbedder(provider)
	if err := state.BuildEmbeddingIndex(context.Background()); err != nil {
		skipIfUnreachable(t, err)
		return
	}
	if len(state.embedIndex) != 2 {
		t.Fatalf("embedIndex len %d, want 2", len(state.embedIndex))
	}

	qvecs, err := state.Embedder.Embed(context.Background(),
		[]string{"tell me about knots and their crossing number"})
	if err != nil {
		skipIfUnreachable(t, err)
		return
	}
	// Tiny non-zero threshold → rank everything (0 would mean "default").
	cands := recall.ProposeEmbedding(qvecs[0], state.embedIndex,
		recall.EmbeddingOptions{Threshold: 1e-6})
	if len(cands) == 0 {
		t.Fatal("no candidates ranked")
	}
	t.Logf("ranked candidates: %+v", cands)
	if cands[0].ThreadID != "thr_1" {
		t.Errorf("knot query ranked %s first, want thr_1 (the knot thread)", cands[0].ThreadID)
	}
}
