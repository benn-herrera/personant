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

	// Limit caps the returned candidates — the MINIMUM floor of the
	// relevance-sized net (#111 Finding A), not a hard cap. ProposeChunks
	// returns the top-Limit candidates PLUS every additional candidate at or
	// above ClearlyRelated (the cast net), bounded only by NetCap. 0 →
	// DefaultChunkLimit; negative → unbounded (Limit alone governs; the
	// clearly-related union is a no-op when everything is already returned).
	Limit int

	// Counter, when non-nil, tallies the cosine comparisons this scan
	// performs — the MEASURED recall_query_cosine_ops instrument (design
	// §7.2). nil on the production hot path. See CosineCounter.
	Counter *CosineCounter

	// NetCapHits, when non-nil, is incremented once if the NetCap backstop
	// truncated the returned set BELOW the clearly-related candidates it
	// would otherwise have kept (a pathological-density tail, never the
	// normal path; #111 Finding A). The caller logs the recall.net-cap-hit
	// line — scoring stays pure and does not reach the logger. nil on the
	// production hot path (the cap still applies; only the signal is dropped).
	NetCapHits *int
}

// ProposeChunks is the §4 fine-pass matcher: it ranks a thread's chunk
// vectors by cosine similarity to the query embedding and returns a
// RELEVANCE-SIZED net (#111 Finding A) — the top-Limit candidates (the
// minimum floor) PLUS every additional candidate whose cosine is at or
// above ClearlyRelated (the cast net), bounded by NetCap. A fixed top-Limit
// arbitrarily drops clearly-related candidates that tie or barely trail the
// Limit-th; sizing the net by relevance keeps them. In a sparse region
// (few or no candidates clear ClearlyRelated) the net collapses to the old
// top-Limit, so behaviour is unchanged where the cluster is thin.
//
// It is the chunk-granularity sibling of ProposeEmbedding and reuses
// cosineSimilarity. Used by BOTH the flat scan and the descent terminal
// rank, so both get the identical net policy → the W1 descent-vs-flat
// comparison stays like-for-like.
//
// Pure function: no I/O. The caller supplies the query vector and the
// chunk vectors of one thread (live-FIFO-window excerpts already
// removed, I6).
func ProposeChunks(query []float64, chunks []ChunkVector, opts ChunkOptions) []ChunkCandidate {
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultChunkLimit
	}
	return capNet(ScanChunks(query, chunks, opts.Threshold, opts.Counter), limit, opts.NetCapHits)
}

// ScanChunks is the exhaustive scan-and-sort primitive underneath
// ProposeChunks: it cosine-scores EVERY chunk against the query, filters to
// those at or above threshold, and returns them sorted score-desc / turn-asc
// — the FULL candidate set, with NO relevance-net cap (capNet/NetCap) applied.
// This is the exact-semantic tier's primitive (#117 ExhaustiveIntraScan):
// every chunk ≥ threshold, nothing dropped. ProposeChunks layers the
// approximate-recall net policy on top of this; the exact tier calls
// ScanChunks directly so capNet/NetCap never truncates an exhaustive result.
//
// threshold == 0 → DefaultCosineThreshold (the same fine-pass operating
// point). counter, when non-nil, tallies one cosine comparison per chunk
// scanned (the MEASURED recall_query_cosine_ops instrument) — the tally lives
// here so it is counted EXACTLY ONCE, regardless of caller. An empty query or
// empty chunk slice returns nil.
func ScanChunks(query []float64, chunks []ChunkVector, threshold float64, counter *CosineCounter) []ChunkCandidate {
	if threshold == 0 {
		threshold = DefaultCosineThreshold
	}
	if len(query) == 0 || len(chunks) == 0 {
		return nil
	}

	out := make([]ChunkCandidate, 0, len(chunks))
	counter.add(len(chunks)) // one cosine comparison per chunk scanned
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
	return out
}

// capNet applies the relevance-sized net cut (#111 Finding A) to a
// score-descending candidate slice: keep the top-limit (the minimum floor)
// PLUS every further candidate at or above ClearlyRelated (the cast net),
// then bound the whole set at NetCap. With limit <= 0 (unbounded) the slice
// is returned whole (subject only to NetCap). Because the input is sorted
// score-desc, the kept set is a prefix: the cut index is the larger of
// limit and the count of clearly-related candidates, capped at NetCap.
//
// netCapHits (nil-safe) is bumped once iff NetCap actually dropped a
// clearly-related candidate the net would otherwise have kept — the
// pathological-density signal the caller logs (recall.net-cap-hit). A cut
// that lands on already-below-ClearlyRelated tail leaves is the normal
// path and is NOT a cap hit.
func capNet(sorted []ChunkCandidate, limit int, netCapHits *int) []ChunkCandidate {
	if limit < 0 {
		limit = len(sorted) // unbounded floor; NetCap still bounds it
	}
	clearlyRelated := 0
	for _, c := range sorted {
		if c.Score >= ClearlyRelated {
			clearlyRelated++
		} else {
			break // sorted desc — no clearly-related candidate past here
		}
	}
	keep := netCapKeep(limit, clearlyRelated, len(sorted), netCapHits)
	if keep >= len(sorted) {
		return sorted
	}
	return sorted[:keep]
}

// netCapKeep is the shared relevance-sized-net cut count (#111 Finding A),
// used by BOTH the terminal leaf rank (capNet) and the per-level beam step
// (descend.go topKByCosine): the kept count is the larger of the floor
// (min-k / Limit) and the clearlyRelated count (the cast net), clamped to the
// candidate total and bounded by NetCap. Because the input is score-desc in
// both callers, the kept set is always a prefix, so a single count governs the
// cut. netCapHits (nil-safe) is bumped once iff NetCap actually dropped a
// clearly-related candidate the net would otherwise have kept — the
// pathological-density signal the caller logs (recall.net-cap-hit). A cut that
// lands on already-below-ClearlyRelated leaves is the normal path, not a hit.
func netCapKeep(floor, clearlyRelated, total int, netCapHits *int) int {
	keep := floor
	if clearlyRelated > keep {
		keep = clearlyRelated // the cast net widens past the floor
	}
	if keep > total {
		keep = total
	}
	if keep > NetCap {
		// Backstop: a degenerate dense tail beyond NetCap. Only a cap hit if
		// we are dropping candidates that cleared ClearlyRelated (genuine high
		// density returning many is the feature working; the cap guards the
		// pathological case). NOT the normal path — document + signal.
		if clearlyRelated > NetCap {
			bumpNetCap(netCapHits)
		}
		keep = NetCap
	}
	return keep
}

// bumpNetCap increments a nil-safe net-cap-hit counter (mirrors
// CosineCounter.add's nil-safety) so call sites need no per-call nil check.
func bumpNetCap(netCapHits *int) {
	if netCapHits != nil {
		*netCapHits++
	}
}
