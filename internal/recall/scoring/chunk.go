package scoring

import "sort"

// DefaultChunkLimit caps the chunk candidates ProposeChunks returns per
// fine pass — the §4.2 Kf default. Three is enough to surface the
// relevant passage plus its immediate neighbours without flooding the
// merge; it is a §9 calibration window, not a frozen constant. The
// cosine threshold reuses DefaultCosineThreshold (the fine pass is the
// same cosine operating point as the coarse pass).
const DefaultChunkLimit = 3

// ChunkVector pairs a turn-excerpt's number with its content embedding —
// one entry of the fine tier (spec §3.4 / design §11.1). One chunk
// vector per scrolled-out turn-excerpt of a thread.
type ChunkVector struct {
	TurnNumber int
	Vector     []float64
}

// ChunkCandidate is one fine-tier match: a turn-excerpt and the cosine
// similarity of its content embedding to the query embedding. The
// sibling of EmbeddingCandidate at chunk granularity.
type ChunkCandidate struct {
	TurnNumber int
	Score      float64
}

// ChunkOptions governs ProposeChunks. Zero-valued fields fall back to the
// documented defaults. Mirrors EmbeddingOptions; there is no Exclude
// analog because the fine pass runs over a single thread's chunks and
// the live-FIFO-window exclusion (I6) is applied by the caller when it
// assembles the chunk slice, not here.
type ChunkOptions struct {
	// Threshold is the minimum cosine similarity. 0 → DefaultCosineThreshold.
	Threshold float64

	// Limit caps the returned candidates (the §4.2 Kf cap). 0 →
	// DefaultChunkLimit; negative → unbounded.
	Limit int
}

// ProposeChunks is the §4 fine-pass matcher: it ranks a thread's chunk
// vectors by cosine similarity to the query embedding and returns the
// candidates at or above the threshold, top-Limit. It is the chunk-
// granularity sibling of ProposeEmbedding and reuses cosineSimilarity.
//
// Pure function: no I/O. The caller supplies the query vector and the
// chunk vectors of one thread (live-FIFO-window excerpts already
// removed, I6).
func ProposeChunks(query []float64, chunks []ChunkVector, opts ChunkOptions) []ChunkCandidate {
	threshold := opts.Threshold
	if threshold == 0 {
		threshold = DefaultCosineThreshold
	}
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultChunkLimit
	}
	if len(query) == 0 || len(chunks) == 0 {
		return nil
	}

	out := make([]ChunkCandidate, 0, len(chunks))
	for _, cv := range chunks {
		score := cosineSimilarity(query, cv.Vector)
		if score < threshold {
			continue
		}
		out = append(out, ChunkCandidate{TurnNumber: cv.TurnNumber, Score: score})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].TurnNumber < out[j].TurnNumber
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
