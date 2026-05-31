// Live integration tests for the embeddings client. They ALWAYS COMPILE
// (no build tag) and gate EXECUTION at runtime: without the live opt-in
// (testsupport.LiveTestsEnv) they skip, so bare `make test` compiles and
// skips them, hitting no endpoint. Run them via `make integration-test`,
// which sets the opt-in; they require the `reaper` provider reachable.
// Under the opt-in an unreachable/misconfigured endpoint is a FAILURE, not
// a skip — you explicitly asked for the live path.
//
// reaper coordinates default to the dev-machine address and are
// overridable via PERSONANT_REAPER_URL. The API key is the local
// non-metered placeholder `dummy` — no secret involved.

package model

import (
	"context"
	"testing"

	"personant/internal/testsupport"
)

func TestHTTPEmbedder_Live(t *testing.T) {
	testsupport.RequireLive(t)
	emb := NewHTTPEmbedder(testsupport.ReaperProvider(), testsupport.ReaperEmbeddingModel, 0)
	vecs, err := emb.Embed(context.Background(), []string{
		"how does shear thinning affect emulsion viscosity",
		"rheology of emulsions, pseudoplastic flow and droplet coalescence",
		"the trefoil knot is the simplest nontrivial knot in topology",
	})
	testsupport.FailOnErr(t, "embed", err)
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vecs))
	}
	for i, v := range vecs {
		if len(v) == 0 {
			t.Fatalf("vector %d is empty", i)
		}
	}
	// Semantic sanity: the related pair must out-score the unrelated one.
	related := dot(vecs[0], vecs[1])
	unrelated := dot(vecs[0], vecs[2])
	if related <= unrelated {
		t.Errorf("related cosine %.4f not greater than unrelated %.4f", related, unrelated)
	}
	t.Logf("dim=%d related=%.4f unrelated=%.4f", len(vecs[0]), related, unrelated)
}

func dot(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
