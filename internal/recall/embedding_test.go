package recall

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", []float64{1, 0, 0}, []float64{1, 0, 0}, 1},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, 0},
		{"opposite", []float64{1, 0}, []float64{-1, 0}, -1},
		{"unnormalized-identical-direction", []float64{2, 0}, []float64{5, 0}, 1},
		{"length-mismatch", []float64{1, 0, 0}, []float64{1, 0}, 0},
		{"zero-vector", []float64{0, 0}, []float64{1, 1}, 0},
		{"empty", []float64{}, []float64{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cosineSimilarity(c.a, c.b)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("cosineSimilarity(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestProposeEmbedding(t *testing.T) {
	threads := []ThreadVector{
		{ThreadID: "thr_1", Vector: []float64{1, 0, 0}},     // cos 1.00 to query
		{ThreadID: "thr_2", Vector: []float64{0, 1, 0}},     // cos 0.00
		{ThreadID: "thr_3", Vector: []float64{0.8, 0.6, 0}}, // cos 0.80
		{ThreadID: "thr_4", Vector: []float64{0.6, 0.8, 0}}, // cos 0.60
	}
	query := []float64{1, 0, 0}

	t.Run("ranks by cosine, applies threshold", func(t *testing.T) {
		got := ProposeEmbedding(query, threads, EmbeddingOptions{Threshold: 0.7})
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
		}
		if got[0].ThreadID != "thr_1" || got[1].ThreadID != "thr_3" {
			t.Errorf("order: got %s,%s want thr_1,thr_3", got[0].ThreadID, got[1].ThreadID)
		}
		if math.Abs(got[0].Score-1.0) > 1e-9 {
			t.Errorf("thr_1 score %v want 1.0", got[0].Score)
		}
	})

	t.Run("exclude omits a thread", func(t *testing.T) {
		got := ProposeEmbedding(query, threads, EmbeddingOptions{
			Threshold: 0.7,
			Exclude:   map[string]struct{}{"thr_1": {}},
		})
		if len(got) != 1 || got[0].ThreadID != "thr_3" {
			t.Errorf("got %+v, want only thr_3", got)
		}
	})

	t.Run("limit caps results", func(t *testing.T) {
		got := ProposeEmbedding(query, threads, EmbeddingOptions{Threshold: 0.1, Limit: 2})
		if len(got) != 2 {
			t.Errorf("got %d, want 2", len(got))
		}
	})

	t.Run("empty query yields nil", func(t *testing.T) {
		if got := ProposeEmbedding(nil, threads, EmbeddingOptions{}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
}
