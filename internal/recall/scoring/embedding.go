package scoring

import (
	"math"
	"sort"
)

// DefaultCosineThreshold is the minimum cosine similarity for an
// embedding-recall candidate to be surfaced (spec §3.4 layer 2). The
// C.6 embedding calibration sweep over the Wikipedia corpus put a
// balanced operating point around 0.55 for nomic-style embeddings;
// this is the provisional default until directive-driven calibration
// tunes it per deployment.
const DefaultCosineThreshold = 0.55

// ThreadVector pairs a thread ID with its content embedding — one
// entry of the in-memory embedding index.
type ThreadVector struct {
	ThreadID string
	Vector   []float64
}

// EmbeddingCandidate is one embedding-recall match: a thread and the
// cosine similarity of its content embedding to the query embedding.
type EmbeddingCandidate struct {
	ThreadID string
	Score    float64
}

// EmbeddingOptions governs ProposeEmbedding. Zero-valued fields fall
// back to the documented defaults.
type EmbeddingOptions struct {
	// Threshold is the minimum cosine similarity. 0 → DefaultCosineThreshold.
	Threshold float64

	// Limit caps the returned candidates. 0 → DefaultLimit; negative →
	// unbounded.
	Limit int

	// Exclude is the set of thread IDs to omit — typically threads
	// already engaged this turn. Nil/empty → no exclusion.
	Exclude map[string]struct{}

	// Counter, when non-nil, tallies the cosine comparisons this scan
	// performs — the MEASURED recall_query_cosine_ops instrument (design
	// §7.2). nil on the production hot path. See CosineCounter.
	Counter *CosineCounter
}

// ProposeEmbedding is the §3.4 layer-2 matcher: it ranks thread
// embedding vectors by cosine similarity to the query embedding and
// returns the candidates at or above the threshold, top-Limit.
//
// Unlike symbolic Jaccard (layer 1), this is the primary recall scan,
// not a refinement of a pre-filtered set — the C.6 calibration showed
// Jaccard's recall collapses under vocabulary drift, so gating
// embedding behind it would discard most genuine matches. Cosine over
// a few hundred in-memory vectors is microseconds; the cost is the one
// embedding call per turn to vectorize the query.
//
// Pure function: no I/O. The caller supplies the query vector and the
// in-memory thread index.
func ProposeEmbedding(query []float64, threads []ThreadVector, opts EmbeddingOptions) []EmbeddingCandidate {
	threshold := opts.Threshold
	if threshold == 0 {
		threshold = DefaultCosineThreshold
	}
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if len(query) == 0 {
		return nil
	}

	out := make([]EmbeddingCandidate, 0, len(threads))
	for _, tv := range threads {
		if _, skip := opts.Exclude[tv.ThreadID]; skip {
			continue
		}
		opts.Counter.add(1) // one cosine comparison per scanned coarse vector
		score := cosineSimilarity(query, tv.Vector)
		if score < threshold {
			continue
		}
		out = append(out, EmbeddingCandidate{ThreadID: tv.ThreadID, Score: score})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ThreadID < out[j].ThreadID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// cosineSimilarity returns the cosine of two vectors. Mismatched
// lengths or a zero-magnitude vector yield 0 — the correct "no signal"
// result. It does not assume pre-normalized inputs.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
