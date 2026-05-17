//go:build integration

// Live integration tests for the embeddings client. Build-tag isolated
// (`integration`) and run via `make integration-test`; they require the
// `reaper` provider reachable. A connection failure skips (reaper not
// running); any other error fails (a real bug in the client).
//
// reaper coordinates default to the dev-machine address and are
// overridable via PERSONANT_REAPER_URL. The API key is the local
// non-metered placeholder `dummy` — no secret involved.

package model

import (
	"context"
	"os"
	"strings"
	"testing"

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

// skipIfUnreachable skips the test on a network-level failure (reaper
// not running) and fails on anything else.
func skipIfUnreachable(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "timeout") {
		t.Skipf("reaper unreachable — integration test needs it running: %v", err)
	}
	t.Fatalf("embed: %v", err)
}

func TestHTTPEmbedder_Live(t *testing.T) {
	emb := NewHTTPEmbedder(reaperProvider())
	vecs, err := emb.Embed(context.Background(), []string{
		"how does shear thinning affect emulsion viscosity",
		"rheology of emulsions, pseudoplastic flow and droplet coalescence",
		"the trefoil knot is the simplest nontrivial knot in topology",
	})
	if err != nil {
		skipIfUnreachable(t, err)
		return
	}
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
