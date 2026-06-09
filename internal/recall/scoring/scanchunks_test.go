package scoring

import "testing"

// highCosineVecs builds n chunk vectors all closely aligned to axis 0 with a
// tiny, index-graded off-axis perturbation — so every cosine with an axis-0
// query is well above threshold (and above ClearlyRelated) yet strictly
// decreasing with index, giving a clean monotone score ladder. Used where the
// only cut under test is NetCap (with Limit<0, ProposeChunks keeps the whole
// score-desc set bounded by NetCap regardless of the clearly-related count).
func highCosineVecs(n int) []ChunkVector {
	const dim = 8
	out := make([]ChunkVector, n)
	for i := 0; i < n; i++ {
		v := make([]float64, dim)
		v[0] = 1.0
		v[1] = 0.0001 * float64(i+1) // tiny graded off-axis: monotone-decreasing cosine, all near 1.0
		out[i] = ChunkVector{TurnNumber: i, Vector: v}
	}
	return out
}

// gradedBandVecs builds n chunk vectors whose cosine with an axis-0 query is
// strictly decreasing with index and lands in (DefaultCosineThreshold,
// ClearlyRelated) — the band where the relevance-net does NOT widen past its
// Limit floor, so ProposeChunks collapses to a top-Limit prefix of the full
// ScanChunks set. The on-axis weight varies only slightly around a base
// chosen with a fixed off-axis magnitude so the whole ladder stays inside the
// band; intended for small n.
func gradedBandVecs(n int) []ChunkVector {
	const dim = 8
	const off = 1.2 // fixed off-axis magnitude: base cosine ~0.64, inside (0.55, 0.75)
	out := make([]ChunkVector, n)
	for i := 0; i < n; i++ {
		v := make([]float64, dim)
		v[0] = 1.0 - 0.0001*float64(i) // tiny decreasing on-axis: monotone-decreasing cosine, all in-band
		v[1] = off
		out[i] = ChunkVector{TurnNumber: i, Vector: v}
	}
	return out
}

// TestScanChunks_ExhaustiveNoCap: ScanChunks returns EVERY chunk at or above
// threshold with NO NetCap truncation — the exhaustive primitive. With more
// than NetCap chunks above threshold, ProposeChunks (even Limit<0) caps at
// NetCap while ScanChunks returns the full set. This is the behavior the
// exact-recall tier (#117) depends on.
func TestScanChunks_ExhaustiveNoCap(t *testing.T) {
	const n = NetCap + 100
	chunks := highCosineVecs(n)
	q := []float64{1, 0, 0, 0, 0, 0, 0, 0}
	const threshold = 0.1 // every chunk clears it (all near cosine 1.0)

	scanned := ScanChunks(q, chunks, threshold, nil)
	if len(scanned) != n {
		t.Fatalf("ScanChunks returned %d, want all %d above threshold (no cap)", len(scanned), n)
	}
	// Score-descending, all above threshold.
	for i, c := range scanned {
		if c.Score < threshold {
			t.Errorf("candidate[%d] score %.4f below threshold %.4f", i, c.Score, threshold)
		}
		if i > 0 && scanned[i-1].Score < c.Score {
			t.Errorf("not score-descending at %d: %.4f then %.4f", i, scanned[i-1].Score, c.Score)
		}
	}

	// ProposeChunks(Limit:-1) over the same input still caps at NetCap —
	// confirming ScanChunks is the cap-free primitive and ProposeChunks is
	// the net-policy wrapper.
	capped := ProposeChunks(q, chunks, ChunkOptions{Limit: -1, Threshold: threshold})
	if len(capped) != NetCap {
		t.Fatalf("ProposeChunks(Limit:-1) returned %d, want NetCap=%d", len(capped), NetCap)
	}
}

// TestScanChunks_DefaultsAndGuards: threshold 0 → DefaultCosineThreshold, and
// empty query / empty chunks → nil.
func TestScanChunks_DefaultsAndGuards(t *testing.T) {
	q := []float64{1, 0, 0, 0}
	chunks := []ChunkVector{
		{TurnNumber: 0, Vector: []float64{1, 0, 0, 0}},    // cosine 1.0 — clears default
		{TurnNumber: 1, Vector: []float64{0.05, 1, 0, 0}}, // cosine ~0.05 — below default
	}
	got := ScanChunks(q, chunks, 0, nil)
	if len(got) != 1 || got[0].TurnNumber != 0 {
		t.Fatalf("threshold 0 should apply DefaultCosineThreshold, keeping only the on-axis chunk, got %+v", got)
	}
	if got := ScanChunks(nil, chunks, 0, nil); got != nil {
		t.Errorf("empty query should return nil, got %+v", got)
	}
	if got := ScanChunks(q, nil, 0, nil); got != nil {
		t.Errorf("empty chunks should return nil, got %+v", got)
	}
}

// TestScanChunks_CounterTalliedOnce: the cosine tally happens exactly once,
// inside ScanChunks. Routing through ProposeChunks (the wrapper) must NOT
// double-count — ProposeChunks delegates the tally to ScanChunks.
func TestScanChunks_CounterTalliedOnce(t *testing.T) {
	q := []float64{1, 0, 0, 0}
	chunks := []ChunkVector{
		{TurnNumber: 0, Vector: []float64{1, 0, 0, 0}},
		{TurnNumber: 1, Vector: []float64{0, 1, 0, 0}},
		{TurnNumber: 2, Vector: []float64{0, 0, 1, 0}},
	}

	var direct CosineCounter
	ScanChunks(q, chunks, 0.1, &direct)
	if direct.Ops() != len(chunks) {
		t.Errorf("ScanChunks tallied %d, want %d (one per chunk)", direct.Ops(), len(chunks))
	}

	var viaPropose CosineCounter
	ProposeChunks(q, chunks, ChunkOptions{Counter: &viaPropose, Threshold: 0.1})
	if viaPropose.Ops() != len(chunks) {
		t.Errorf("ProposeChunks tallied %d, want %d — must count exactly once (no double-count)", viaPropose.Ops(), len(chunks))
	}
}

// TestProposeChunks_BehaviorPreserved: the extraction is behavior-preserving.
// On an input that does NOT exceed NetCap, ProposeChunks returns exactly the
// score-descending set ScanChunks produces, then trimmed by the net policy —
// here, all in-band so the relevance net never widens past the Limit floor,
// giving the top-Limit prefix of the full scan.
func TestProposeChunks_BehaviorPreserved(t *testing.T) {
	const n = 20
	chunks := gradedBandVecs(n) // cosines in (threshold, ClearlyRelated): net = Limit floor
	q := []float64{1, 0, 0, 0, 0, 0, 0, 0}
	const threshold = 0.1

	full := ScanChunks(q, chunks, threshold, nil)
	if len(full) != n {
		t.Fatalf("ScanChunks returned %d, want %d (test premise)", len(full), n)
	}

	got := ProposeChunks(q, chunks, ChunkOptions{Limit: DefaultChunkLimit, Threshold: threshold})
	if len(got) != DefaultChunkLimit {
		t.Fatalf("ProposeChunks returned %d, want top-Limit=%d (in-band net collapses to floor)", len(got), DefaultChunkLimit)
	}
	for i := range got {
		if got[i] != full[i] {
			t.Errorf("ProposeChunks[%d]=%+v, want the ScanChunks prefix %+v", i, got[i], full[i])
		}
	}
}
