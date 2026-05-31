// Live integration test for embedding recall through the Recaller. It
// ALWAYS COMPILES (no build tag) and gates EXECUTION at runtime: without
// the live opt-in (testsupport.LiveTestsEnv) it skips, so bare `make test`
// compiles and skips it, hitting no endpoint. Run it via
// `make integration-test`, which sets the opt-in; it needs the `reaper`
// provider reachable. Under the opt-in an unreachable/misconfigured
// endpoint is a FAILURE, not a skip.
//
// It exercises the real HTTPEmbedder end to end — measure.Service.Prepare
// builds the index over seeded threads, then Recall cosine-matches —
// confirming the runtime path works against an actual embedding model, not
// the mock.

package turn

import (
	"context"
	"testing"

	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/testsupport"
)

// TestEmbeddingRecall_Live builds the Recaller's embedding index over
// two disjoint-topic threads with the real embedder, then checks that
// a knot-theory query recalls the knot thread as the top candidate.
func TestEmbeddingRecall_Live(t *testing.T) {
	testsupport.RequireLive(t)
	provider := testsupport.ReaperProvider()

	// Fail fast if the configured embedding model is not actually served —
	// a stale model ref otherwise surfaces as an opaque mid-run error.
	client, ok := model.NewHTTPClient(provider).(*model.HTTPClient)
	if !ok {
		t.Fatalf("NewHTTPClient did not return *HTTPClient (cannot verify model presence)")
	}
	testsupport.RequireModelPresent(t, func(ctx context.Context) ([]string, error) {
		infos, err := client.ListModels(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(infos))
		for i, m := range infos {
			ids[i] = m.ID
		}
		return ids, nil
	}, testsupport.ReaperEmbeddingModel)

	paths, meta := newTestHome(t)
	seedThreadWithBody(t, paths, meta.ID, "thr_1",
		"The trefoil knot is the simplest nontrivial knot in knot theory. "+
			"It has three crossings and a crossing number of three; it is chiral.")
	seedThreadWithBody(t, paths, meta.ID, "thr_2",
		"Neutrino oscillation is the quantum phenomenon in which a neutrino "+
			"changes lepton flavor as it propagates, implying nonzero neutrino mass.")
	ops := fileadapter.NewFileAdapter(paths)

	svc := measure.NewService(ops, model.NewHTTPEmbedder(provider, testsupport.ReaperEmbeddingModel, 0))
	err := svc.Prepare(context.Background())
	testsupport.FailOnErr(t, "embedding recall prepare", err)

	results, err := svc.Recall(context.Background(), measure.Request{
		QueryText: "tell me about knots and their crossing number",
	})
	testsupport.FailOnErr(t, "embedding recall", err)
	t.Logf("recall results: %+v", results)
	if len(results) == 0 {
		t.Fatal("no recall results — knot query matched nothing")
	}
	if results[0].ThreadID != "thr_1" || results[0].Embedding == nil {
		t.Errorf("knot query: top result %s (embedding=%v), want thr_1 with an embedding hit",
			results[0].ThreadID, results[0].Embedding)
	}
}
