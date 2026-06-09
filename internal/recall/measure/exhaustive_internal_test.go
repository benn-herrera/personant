package measure

import (
	"testing"

	"personant/internal/recall/scoring"
)

// This is the #117 SEMANTIC-EXACT tier internal test. It reaches the private
// exhaustiveIntraScan seam and the private indexSnapshot directly (the
// external _test package cannot), driving the scan with a hand-built query
// vector and synthetic fine vectors — fully deterministic, no live embedder
// — exactly as the #111 intra_tree_internal_test drives intraChunks.
//
// The headline assertion: the exhaustive scan returns STRICTLY MORE chunks
// than the truncated top-Kf path the normal recall path uses, every one at
// or above the threshold, ordered by score descending.

// gradedVecs builds n vectors whose query-axis component decreases with
// index over a large fixed off-axis magnitude, so a query along axis 0 has a
// strictly decreasing, all-positive cosine with each — a clean monotone
// score ladder. The off-axis magnitude is sized so every cosine lands in
// the band (threshold, ClearlyRelated): above the floor so all clear the
// scan threshold, BELOW scoring.ClearlyRelated (0.75) so the relevance-net
// does NOT widen the truncated path past its top-Kf floor — which is what
// makes "exhaustive returns strictly more than truncated" a real assertion
// rather than an artifact of the cast-net union.
func gradedVecs(n int) [][]float64 {
	const dim = 8
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		v := make([]float64, dim)
		v[0] = float64(n - i) // decreasing on-axis weight (1..n)
		v[1] = float64(n)     // fixed off-axis magnitude pulls cosines into (threshold, ClearlyRelated)
		out[i] = v
	}
	return out
}

func TestExhaustiveIntraScan_ReturnsAllAboveThreshold(t *testing.T) {
	const nChunks = 9 // > Kf (3): the truncated path keeps 3, exhaustive keeps all
	fine := chunksFromVectors(gradedVecs(nChunks))
	snap := snapWithFine("thr_eng", fine, nil) // nil tree: the exact tier never descends a tree
	q := []float64{1, 0, 0, 0, 0, 0, 0, 0}     // along axis 0

	const threshold = 0.1 // low enough that every graded chunk clears it

	exhaustive := exhaustiveIntraScan(q, snap, "thr_eng", threshold)
	if len(exhaustive) != nChunks {
		t.Fatalf("exhaustive scan returned %d candidates, want all %d above threshold", len(exhaustive), nChunks)
	}

	// The truncated path (the normal intra/coarse-fine policy) keeps only Kf.
	truncated := scoring.ProposeChunks(q, fine, scoring.ChunkOptions{Limit: Kf, Threshold: threshold})
	if len(truncated) != Kf {
		t.Fatalf("truncated path returned %d, want Kf=%d (test premise)", len(truncated), Kf)
	}
	if len(exhaustive) <= len(truncated) {
		t.Fatalf("exhaustive (%d) must be strictly more than truncated (%d)", len(exhaustive), len(truncated))
	}

	// Score-descending order, and every candidate clears the threshold.
	for i, c := range exhaustive {
		if c.Score < threshold {
			t.Errorf("candidate[%d] score %.4f below threshold %.4f", i, c.Score, threshold)
		}
		if i > 0 && exhaustive[i-1].Score < c.Score {
			t.Errorf("not score-descending at %d: %.4f then %.4f", i, exhaustive[i-1].Score, c.Score)
		}
	}

	// The exhaustive set is a superset of the truncated top-Kf set (the same
	// highest-scoring leaves, plus the rest).
	have := make(map[int]struct{}, len(exhaustive))
	for _, c := range exhaustive {
		have[c.TurnNumber] = struct{}{}
	}
	for _, c := range truncated {
		if _, ok := have[c.TurnNumber]; !ok {
			t.Errorf("truncated turn %d missing from exhaustive set", c.TurnNumber)
		}
	}
}

// TestExhaustiveIntraScan_ExceedsNetCap proves the exact tier no longer
// inherits the approximate-recall net cap (scoring.NetCap, 512). With MORE
// than NetCap chunks above threshold, the scan must return EVERY one — the
// old ProposeChunks(Limit:-1) path truncated to NetCap, silently breaking the
// "exhaustive" contract on exactly the dense multi-year thread this tier
// exists for. gradedVecs keeps every cosine in (threshold, ClearlyRelated),
// so the relevance-net cast does not widen/shrink the set independently — the
// only thing that could cut it here is NetCap, which ScanChunks bypasses.
func TestExhaustiveIntraScan_ExceedsNetCap(t *testing.T) {
	const nChunks = 600 // > scoring.NetCap (512): the old capNet path truncated here
	if nChunks <= scoring.NetCap {
		t.Fatalf("test premise: nChunks=%d must exceed NetCap=%d", nChunks, scoring.NetCap)
	}
	// All chunks near-aligned to axis 0 (cosine ~1.0) with a tiny graded
	// off-axis perturbation: every one clears the threshold, with a clean
	// monotone score ladder. (gradedVecs' fixed-large off-axis magnitude
	// drops the small-v[0] tail below a low threshold at this n; here every
	// chunk stays above it, isolating NetCap as the only possible cut.)
	highCosine := make([][]float64, nChunks)
	for i := range highCosine {
		highCosine[i] = []float64{1, 0.0001 * float64(i+1), 0, 0, 0, 0, 0, 0}
	}
	fine := chunksFromVectors(highCosine)
	snap := snapWithFine("thr_eng", fine, nil)
	q := []float64{1, 0, 0, 0, 0, 0, 0, 0}

	const threshold = 0.1 // every chunk clears it (all near cosine 1.0)

	exhaustive := exhaustiveIntraScan(q, snap, "thr_eng", threshold)
	if len(exhaustive) != nChunks {
		t.Fatalf("exhaustive scan returned %d candidates, want all %d above threshold (NetCap must not truncate)", len(exhaustive), nChunks)
	}

	// Demonstrate the old behavior the fix corrects: ProposeChunks(Limit:-1)
	// still caps at NetCap, which is exactly why the exact tier no longer
	// routes through it.
	capped := scoring.ProposeChunks(q, fine, scoring.ChunkOptions{Limit: -1, Threshold: threshold})
	if len(capped) != scoring.NetCap {
		t.Fatalf("ProposeChunks(Limit:-1) returned %d, want NetCap=%d (the truncation the exact tier must avoid)", len(capped), scoring.NetCap)
	}
	if len(exhaustive) <= len(capped) {
		t.Fatalf("exhaustive (%d) must exceed the NetCap-truncated path (%d)", len(exhaustive), len(capped))
	}

	for i, c := range exhaustive {
		if c.Score < threshold {
			t.Errorf("candidate[%d] score %.4f below threshold %.4f", i, c.Score, threshold)
		}
		if i > 0 && exhaustive[i-1].Score < c.Score {
			t.Errorf("not score-descending at %d: %.4f then %.4f", i, exhaustive[i-1].Score, c.Score)
		}
	}
}

// TestExhaustiveIntraScan_ThresholdFilters: candidates below the threshold
// are excluded — the scan is exhaustive over the ABOVE-threshold set, not a
// dump of every chunk.
func TestExhaustiveIntraScan_ThresholdFilters(t *testing.T) {
	const dim = 4
	// Two chunks strongly along axis 0 (high cosine with the query), two
	// strongly along axis 1 (near-zero cosine). A mid threshold keeps only
	// the axis-0 pair.
	fine := chunksFromVectors([][]float64{
		{1, 0, 0, 0},
		{0.9, 0.1, 0, 0},
		{0, 1, 0, 0},
		{0.05, 1, 0, 0},
	})
	snap := snapWithFine("thr_eng", fine, nil)
	q := []float64{1, 0, 0, 0}

	got := exhaustiveIntraScan(q, snap, "thr_eng", 0.5)
	if len(got) != 2 {
		t.Fatalf("want 2 above-threshold candidates, got %d: %+v", len(got), got)
	}
	for _, c := range got {
		if c.TurnNumber != 0 && c.TurnNumber != 1 {
			t.Errorf("unexpected above-threshold turn %d (axis-1 chunks should be filtered)", c.TurnNumber)
		}
	}
}

// TestExhaustiveIntraScan_NoFineVectors: a thread with nothing indexed
// returns nil (not an error, not a panic).
func TestExhaustiveIntraScan_NoFineVectors(t *testing.T) {
	snap := emptySnapshot()
	if got := exhaustiveIntraScan([]float64{1, 0}, snap, "thr_absent", 0.5); got != nil {
		t.Errorf("unindexed thread should return nil, got %+v", got)
	}
}

// TestExhaustiveIntraScan_DefaultThreshold: threshold 0 falls back to
// scoring.DefaultCosineThreshold (the same operating point the rest of the
// fine pass uses).
func TestExhaustiveIntraScan_DefaultThreshold(t *testing.T) {
	fine := chunksFromVectors([][]float64{
		{1, 0, 0, 0},       // cosine 1.0 with the query — clears the default
		{0.05, 1, 0, 0},    // cosine ~0.05 — below DefaultCosineThreshold (0.55)
	})
	snap := snapWithFine("thr_eng", fine, nil)
	q := []float64{1, 0, 0, 0}

	got := exhaustiveIntraScan(q, snap, "thr_eng", 0)
	if len(got) != 1 || got[0].TurnNumber != 0 {
		t.Fatalf("default threshold should keep only the on-axis chunk, got %+v", got)
	}
}
