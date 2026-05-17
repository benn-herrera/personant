//go:build integration

// Live integration test for embedding recall through the Recaller.
// Build-tag isolated (`integration`), run via `make integration-test`;
// needs the `reaper` provider reachable. It exercises the real
// HTTPEmbedder end to end — recall.Service.Prepare builds the index
// over seeded threads, then Recall cosine-matches — confirming the
// runtime path works against an actual embedding model, not the mock.

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
		Name:    "reaper",
		BaseURL: url,
		APIKey:  "dummy",
	}
}

// reaperEmbeddingModel is the embedding model id served by reaper.
const reaperEmbeddingModel = "nomicai-embed"

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

// TestEmbeddingRecall_Live builds the Recaller's embedding index over
// two disjoint-topic threads with the real embedder, then checks that
// a knot-theory query recalls the knot thread as the top candidate.
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

	svc := recall.NewService(ops, model.NewHTTPEmbedder(provider, reaperEmbeddingModel, 0))
	if err := svc.Prepare(context.Background()); err != nil {
		skipIfUnreachable(t, err)
		return
	}

	results, err := svc.Recall(context.Background(), recall.Request{
		QueryText: "tell me about knots and their crossing number",
	})
	if err != nil {
		skipIfUnreachable(t, err)
		return
	}
	t.Logf("recall results: %+v", results)
	if len(results) == 0 {
		t.Fatal("no recall results — knot query matched nothing")
	}
	if results[0].ThreadID != "thr_1" || results[0].Embedding == nil {
		t.Errorf("knot query: top result %s (embedding=%v), want thr_1 with an embedding hit",
			results[0].ThreadID, results[0].Embedding)
	}
}
