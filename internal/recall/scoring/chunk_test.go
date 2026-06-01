package scoring

import (
	"math"
	"testing"
)

func TestProposeChunks(t *testing.T) {
	chunks := []ChunkVector{
		{TurnNumber: 1, Vector: []float64{1, 0, 0}},     // cos 1.00 to query
		{TurnNumber: 2, Vector: []float64{0, 1, 0}},     // cos 0.00
		{TurnNumber: 3, Vector: []float64{0.8, 0.6, 0}}, // cos 0.80
		{TurnNumber: 4, Vector: []float64{0.6, 0.8, 0}}, // cos 0.60
	}
	query := []float64{1, 0, 0}

	t.Run("ranks by cosine, applies threshold", func(t *testing.T) {
		got := ProposeChunks(query, chunks, ChunkOptions{Threshold: 0.7})
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
		}
		if got[0].TurnNumber != 1 || got[1].TurnNumber != 3 {
			t.Errorf("order: got %d,%d want 1,3", got[0].TurnNumber, got[1].TurnNumber)
		}
	})

	t.Run("identical vector scores ~1.0 and carries its turn number", func(t *testing.T) {
		got := ProposeChunks(query, chunks, ChunkOptions{Threshold: 0.7})
		if len(got) == 0 {
			t.Fatal("got no candidates")
		}
		if got[0].TurnNumber != 1 {
			t.Errorf("top candidate TurnNumber = %d, want 1", got[0].TurnNumber)
		}
		if math.Abs(got[0].Score-1.0) > 1e-9 {
			t.Errorf("turn 1 score %v want 1.0", got[0].Score)
		}
	})

	t.Run("limit caps results", func(t *testing.T) {
		got := ProposeChunks(query, chunks, ChunkOptions{Threshold: 0.1, Limit: 2})
		if len(got) != 2 {
			t.Errorf("got %d, want 2", len(got))
		}
		// Top-2 by cosine are turns 1 (1.00) and 3 (0.80).
		if got[0].TurnNumber != 1 || got[1].TurnNumber != 3 {
			t.Errorf("order: got %d,%d want 1,3", got[0].TurnNumber, got[1].TurnNumber)
		}
	})

	t.Run("default limit caps at DefaultChunkLimit", func(t *testing.T) {
		got := ProposeChunks(query, chunks, ChunkOptions{Threshold: -2})
		if len(got) != DefaultChunkLimit {
			t.Errorf("got %d, want DefaultChunkLimit=%d", len(got), DefaultChunkLimit)
		}
	})

	t.Run("threshold filters everything below", func(t *testing.T) {
		got := ProposeChunks(query, chunks, ChunkOptions{Threshold: 0.99})
		if len(got) != 1 || got[0].TurnNumber != 1 {
			t.Errorf("got %+v, want only turn 1", got)
		}
	})

	t.Run("empty query yields nil", func(t *testing.T) {
		if got := ProposeChunks(nil, chunks, ChunkOptions{}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("empty chunks yields nil", func(t *testing.T) {
		if got := ProposeChunks(query, nil, ChunkOptions{}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
}
