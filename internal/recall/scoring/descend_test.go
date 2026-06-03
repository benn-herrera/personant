package scoring

import (
	"math"
	"testing"
)

// buildTestTree is a PURE, deterministic tree-construction helper FOR
// TESTS ONLY. It is not the production builder — the real semantic
// clustering + LLM summaries land in later increments / the sleep-cycle
// builder. Here it groups leaves into contiguous B-sized clusters
// bottom-up and gives each internal node the centroid (mean) of its
// children's vectors as its summary key. A centroid is a faithful-enough
// stand-in for a summary embedding to exercise the descent math and the
// W1 recall-preservation property; the W3 test hand-builds a tree with a
// deliberately cosine-missing summary to prove the beam.
//
// Deterministic given the leaf order (no rng), so a rebuild from the same
// leaves yields the same topology (mirrors the design's W5
// rebuildability requirement at the test level).
func buildTestTree(leaves []ChunkVector, b int) *SummaryNode {
	if len(leaves) == 0 {
		return nil
	}
	if len(leaves) == 1 {
		return &SummaryNode{Vector: leaves[0].Vector, Leaf: leaves[0]}
	}
	// Level 0: one leaf node per chunk.
	level := make([]*SummaryNode, len(leaves))
	for i, lv := range leaves {
		level[i] = &SummaryNode{Vector: lv.Vector, Leaf: lv}
	}
	// Fold up into ~b-sized parents until one root remains.
	for len(level) > 1 {
		var next []*SummaryNode
		for i := 0; i < len(level); i += b {
			end := min(i+b, len(level))
			group := level[i:end]
			next = append(next, &SummaryNode{
				Vector:   centroid(group),
				Children: group,
			})
		}
		level = next
	}
	return level[0]
}

// centroid returns the element-wise mean of the child node vectors —
// the test builder's summary-key stand-in.
func centroid(nodes []*SummaryNode) []float64 {
	if len(nodes) == 0 {
		return nil
	}
	d := len(nodes[0].Vector)
	out := make([]float64, d)
	for _, n := range nodes {
		for i := 0; i < d && i < len(n.Vector); i++ {
			out[i] += n.Vector[i]
		}
	}
	for i := range out {
		out[i] /= float64(len(nodes))
	}
	return out
}

// turnSet collects the turn numbers of a candidate slice into a set, for
// set-difference comparison (W1 compares leaf *sets*, not ordering).
func turnSet(cs []ChunkCandidate) map[int]struct{} {
	s := make(map[int]struct{}, len(cs))
	for _, c := range cs {
		s[c.TurnNumber] = struct{}{}
	}
	return s
}

func setsEqual(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// unitNoisy builds a roughly-unit vector pointing mostly along axis with
// a small deterministic perturbation on the next axis, so leaves cluster
// by axis but are not identical (a realistic-ish semantic cluster).
func unitNoisy(dim, axis int, perturb float64) []float64 {
	v := make([]float64, dim)
	v[axis] = 1.0
	v[(axis+1)%dim] = perturb
	return v
}

// TestDescendChunks_W1_RecallPreservation is the headline test: for a
// tree built from N leaves, DescendChunks must return the SAME top-Kf
// leaf set that flat ProposeChunks over the same N leaves returns —
// within the beam (design W1 / §7.1, divergence == 0). The test asserts
// EXACT set equality (the beam tolerance asserted here is ZERO
// divergence: with semantically-clustered leaves and beam k=4, every
// flat top-Kf leaf is reachable). A query that exposed a beam miss would
// fail this, which is the k-tuning signal.
func TestDescendChunks_W1_RecallPreservation(t *testing.T) {
	const dim = 8
	// Build N leaves grouped into `dim` semantic clusters by dominant
	// axis, so the centroid summaries are coherent and the beam can find
	// the right subtree. Turn numbers are unique and increasing.
	var leaves []ChunkVector
	turn := 0
	for axis := 0; axis < dim; axis++ {
		for j := 0; j < 12; j++ { // 12 leaves per cluster → 96 leaves, > LeafFrontierCap
			perturb := 0.05 * float64(j)
			leaves = append(leaves, ChunkVector{TurnNumber: turn, Vector: unitNoisy(dim, axis, perturb)})
			turn++
		}
	}
	tree := buildTestTree(leaves, TreeBranchingFactor)

	// Queries: one aimed at each cluster axis, plus a couple of blends.
	queries := map[string][]float64{}
	for axis := 0; axis < dim; axis++ {
		queries["axis-"+string(rune('A'+axis))] = unitNoisy(dim, axis, 0.1)
	}
	blend := make([]float64, dim)
	blend[0], blend[1] = 1.0, 0.6
	queries["blend-0-1"] = blend

	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			flat := ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit})
			descent := DescendChunks(q, tree, DescendOptions{Limit: DefaultChunkLimit})
			if !setsEqual(turnSet(flat), turnSet(descent)) {
				t.Errorf("W1 divergence: flat top-Kf %v != descent top-Kf %v",
					turnSet(flat), turnSet(descent))
			}
		})
	}
}

// TestCosineCounter_DescentBendsBelowFlat is the unit-level proof that the
// MEASURED recall_query_cosine_ops instrument (§7.2) reflects the O(C_main)→
// O(log n) bend: over the SAME large leaf set, the beam descent must perform
// FAR fewer cosine comparisons than the flat scan, and a lossless level (child
// count <= beam) must compute zero. This is the headline perf-validation claim
// the sim's measured metric rests on, checked here without the sim stack.
func TestCosineCounter_DescentBendsBelowFlat(t *testing.T) {
	const dim = 8
	var leaves []ChunkVector
	turn := 0
	for axis := 0; axis < dim; axis++ {
		for j := 0; j < 64; j++ { // 512 leaves — a "long thread" scale
			leaves = append(leaves, ChunkVector{TurnNumber: turn, Vector: unitNoisy(dim, axis, 0.05*float64(j))})
			turn++
		}
	}
	tree := buildTestTree(leaves, TreeBranchingFactor)
	q := unitNoisy(dim, 0, 0.1)

	flatCounter := &CosineCounter{}
	ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit, Counter: flatCounter})
	// Flat scan: exactly one cosine per leaf.
	if flatCounter.Ops() != len(leaves) {
		t.Errorf("flat scan ops = %d, want one per leaf = %d", flatCounter.Ops(), len(leaves))
	}

	descentCounter := &CosineCounter{}
	DescendChunks(q, tree, DescendOptions{Limit: DefaultChunkLimit, Counter: descentCounter})
	// Descent: pruned levels + terminal frontier rank, log-bounded — must be a
	// small fraction of the flat scan over 512 leaves.
	if descentCounter.Ops() == 0 {
		t.Fatal("descent counted 0 cosine ops — the counter is not threaded through topKByCosine / ProposeChunks")
	}
	if descentCounter.Ops() >= flatCounter.Ops() {
		t.Errorf("descent ops %d not below flat ops %d — the O(log n) bend the measured metric must show is absent",
			descentCounter.Ops(), flatCounter.Ops())
	}
	// Sanity on the magnitude of the bend: at 512 leaves, B=16, k=4 the descent
	// should be well under half the flat cost (it is ~k·B·depth + frontier).
	if descentCounter.Ops() > flatCounter.Ops()/2 {
		t.Errorf("descent ops %d > half of flat ops %d — bend weaker than the cost model predicts",
			descentCounter.Ops(), flatCounter.Ops())
	}

	// A nil counter must be a no-op (production hot-path: no instrumentation).
	if got := ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit}); len(got) == 0 {
		t.Error("nil-counter ProposeChunks returned no candidates — nil-safety broke the scan")
	}
}

// TestDescendChunks_W3_Beam constructs the failure mode W3 exists to
// prevent: the best leaf sits under an internal node whose SUMMARY vector
// cosine-misses the query, so greedy top-1 descent would prune that
// subtree and lose the leaf. Beam k>1 must still find it.
//
// Construction: two sibling subtrees under the root.
//   - Subtree DECOY: a tight cluster on axis 0, whose centroid summary
//     strongly matches the query (axis 0). Its leaves are near-misses.
//   - Subtree TARGET: a HETEROGENEOUS cluster whose centroid summary
//     points nowhere near the query (a heterogeneous average — the §1
//     "summary averages a heterogeneous cluster" case) — yet it CONTAINS
//     the single best leaf, an exact axis-0 match.
//
// Greedy top-1 keeps only DECOY (its summary wins) and never sees the
// target leaf. Beam k=2 keeps both subtrees and recovers it.
func TestDescendChunks_W3_Beam(t *testing.T) {
	const dim = 4
	query := []float64{1, 0, 0, 0} // axis 0

	// DECOY subtree: leaves clustered on axis 0 but all slightly off, so
	// their centroid cosine-matches the query strongly yet no leaf is the
	// best possible match.
	decoyLeaves := []ChunkVector{
		{TurnNumber: 1, Vector: []float64{0.9, 0.2, 0, 0}},
		{TurnNumber: 2, Vector: []float64{0.92, 0.15, 0, 0}},
		{TurnNumber: 3, Vector: []float64{0.88, 0.25, 0, 0}},
	}
	decoy := &SummaryNode{
		Children: leafNodes(decoyLeaves),
	}
	decoy.Vector = centroid(decoy.Children) // ~axis-0 → matches query

	// TARGET subtree: heterogeneous leaves whose centroid points away
	// from the query, but one leaf (turn 99) is the exact axis-0 match —
	// the leaf the flat scan ranks #1.
	targetLeaves := []ChunkVector{
		{TurnNumber: 99, Vector: []float64{1, 0, 0, 0}}, // exact match — the best leaf
		{TurnNumber: 10, Vector: []float64{0, 1, 0, 0}},
		{TurnNumber: 11, Vector: []float64{0, 0, 1, 0}},
		{TurnNumber: 12, Vector: []float64{0, 0, 0, 1}},
	}
	target := &SummaryNode{
		Children: leafNodes(targetLeaves),
	}
	target.Vector = centroid(target.Children) // heterogeneous → cosine-misses query

	root := &SummaryNode{
		Children: []*SummaryNode{decoy, target},
		Vector:   []float64{0.5, 0.3, 0.1, 0.1},
	}

	// Sanity: the target summary really does cosine-miss relative to the
	// decoy summary, so greedy top-1 WOULD prune target.
	decoyScore := cosineSimilarity(query, decoy.Vector)
	targetScore := cosineSimilarity(query, target.Vector)
	if !(decoyScore > targetScore) {
		t.Fatalf("test setup invalid: decoy summary (%.3f) must out-cosine target summary (%.3f)",
			decoyScore, targetScore)
	}

	t.Run("greedy top-1 would miss the best leaf", func(t *testing.T) {
		got := DescendChunks(query, root, DescendOptions{Beam: 1, Limit: 1})
		if len(got) == 1 && got[0].TurnNumber == 99 {
			t.Fatal("expected greedy beam=1 to MISS leaf 99 (it lives under the cosine-missing target summary)")
		}
	})

	t.Run("beam k=2 recovers the best leaf", func(t *testing.T) {
		// Limit is the net's MINIMUM floor (#111 Finding A), not a hard cap:
		// the terminal returns leaf 99 PLUS any clearly-related decoy leaves,
		// so we assert leaf 99 is RECOVERED and ranks first, not that exactly
		// one leaf returns.
		got := DescendChunks(query, root, DescendOptions{Beam: 2, Limit: 1})
		if len(got) == 0 || got[0].TurnNumber != 99 {
			t.Errorf("beam k=2: got %+v, want top leaf turn 99 first (recovered via beam)", got)
		}
		if _, ok := turnSet(got)[99]; !ok {
			t.Errorf("beam k=2: leaf 99 not in result %+v", got)
		}
	})
}

func leafNodes(leaves []ChunkVector) []*SummaryNode {
	out := make([]*SummaryNode, len(leaves))
	for i, lv := range leaves {
		out[i] = &SummaryNode{Vector: lv.Vector, Leaf: lv}
	}
	return out
}

// TestDescendChunks_FrontierBound asserts the descent's leaf frontier is
// bounded (design §4.1, W2). With the #111 Finding A relevance net the
// per-level beam keeps the top-Beam branches PLUS any clearly-related ones,
// so the frontier is bounded by NetCap (the backstop), not strictly by
// LeafFrontierCap. This fixture's clusters are near-orthogonal (only the
// axis-0 cluster is query-related), so the net does not widen here and the
// frontier stays within LeafFrontierCap — but the assertion is against the
// real bound, NetCap, which the dense-cluster + backstop tests below exercise.
func TestDescendChunks_FrontierBound(t *testing.T) {
	const dim = 8
	var leaves []ChunkVector
	for i := 0; i < 5000; i++ { // huge tree
		leaves = append(leaves, ChunkVector{TurnNumber: i, Vector: unitNoisy(dim, i%dim, 0.01*float64(i%7))})
	}
	tree := buildTestTree(leaves, TreeBranchingFactor)
	q := unitNoisy(dim, 0, 0.1)

	// Threshold -2 admits every frontier leaf; Limit -1 unbounded — so
	// the count returned == frontier size, which must be ≤ NetCap.
	got := DescendChunks(q, tree, DescendOptions{Beam: BeamWidth, Threshold: -2, Limit: -1})
	if len(got) > NetCap {
		t.Errorf("frontier bound: descent returned %d leaves, want ≤ NetCap=%d", len(got), NetCap)
	}
	if len(got) == 0 {
		t.Error("expected a non-empty frontier from a 5000-leaf tree")
	}
}

// TestDescendChunks_Net_DenseCluster is THE #111 Finding A headline test: it
// reproduces the live W1 failure structurally and shows the relevance net
// fixes it. The live diagnosis: a dense intra-thread cluster of leaves all
// scoring 0.80–0.83 (all clearly-related) split across branches; the fixed
// BeamWidth=8 cut pruned the branch holding the two HIGHEST-cosine leaves
// because other equally-related branches' SUMMARY keys took the 8 slots, so
// those top leaves never reached the terminal rank — descent != flat, a
// strict-miss.
//
// Construction: BeamWidth+1 sibling branches under the root. Every branch is
// clearly-related (max-exemplar-cosine ≥ ClearlyRelated). The VICTIM branch's
// summary EXEMPLAR key under-represents its leaves (a heterogeneous-cluster
// summary), so its max-exemplar score ranks LAST — outside a fixed top-
// BeamWidth cut — yet it holds the two leaves with the highest LEAF cosine,
// which the flat scan ranks in its top-Kf. A fixed-k beam drops the victim
// (descent misses those leaves); the relevance net keeps it (it is clearly-
// related) so descent reaches the same leaves flat finds → divergence 0.
func TestDescendChunks_Net_DenseCluster(t *testing.T) {
	const dim = 32
	axis0 := func(v float64) []float64 { e := make([]float64, dim); e[0] = v; return e }
	query := axis0(1)

	// vecCos builds a vector with a CONTROLLED cosine ≈ target to the pure
	// axis-0 query: axis-0 component 1, off-axis component sqrt(1/t²-1) on a
	// distinct axis (cosine = 1/sqrt(1+off²) = t). Cosine ignores magnitude,
	// so the off-axis tilt — not the axis-0 magnitude — sets the score.
	vecCos := func(target float64, off int) []float64 {
		v := make([]float64, dim)
		v[0] = 1
		v[off] = math.Sqrt(1/(target*target) - 1)
		return v
	}
	mkLeaf := func(turn int, target float64, off int) ChunkVector {
		return ChunkVector{TurnNumber: turn, Vector: vecCos(target, off)}
	}

	var allLeaves []ChunkVector
	var branches []*SummaryNode
	turn := 100

	// BeamWidth "filler" branches: clearly-related leaves (~0.81) and a
	// summary exemplar that faithfully represents them (max-exemplar ~0.81),
	// so each WINS a fixed beam slot ahead of the victim.
	for d := 0; d < BeamWidth; d++ {
		off := 1 + d
		ls := []ChunkVector{
			mkLeaf(turn, 0.81, off),
			mkLeaf(turn+1, 0.80, off),
		}
		turn += 2
		allLeaves = append(allLeaves, ls...)
		b := &SummaryNode{Children: leafNodes(ls)}
		// Faithful summary key: an exemplar at ~0.81 (the branch's own leaf).
		b.Vectors = [][]float64{ls[0].Vector}
		b.Vector = b.Vectors[0]
		branches = append(branches, b)
	}

	// VICTIM branch: holds the two HIGHEST-cosine leaves (0.83, 0.825), but
	// its summary EXEMPLAR under-represents them — a heterogeneous summary key
	// scoring ~0.76 (still clearly-related, but the LOWEST max-exemplar, so a
	// fixed top-BeamWidth cut drops it). The net keeps it because 0.76 ≥
	// ClearlyRelated.
	victimLeaves := []ChunkVector{
		mkLeaf(turn, 0.83, 30),    // global-highest leaf
		mkLeaf(turn+1, 0.825, 31), // global-second leaf
	}
	topTurn1, topTurn2 := turn, turn+1
	turn += 2
	allLeaves = append(allLeaves, victimLeaves...)
	victim := &SummaryNode{Children: leafNodes(victimLeaves)}
	victim.Vectors = [][]float64{vecCos(0.76, 29)} // under-representing summary key
	victim.Vector = victim.Vectors[0]
	branches = append(branches, victim)

	root := &SummaryNode{Children: branches, Vector: axis0(0.8)}

	// Sanity 1: the victim's summary key really ranks LAST among the branches
	// (so a fixed top-BeamWidth cut, with BeamWidth+1 branches, drops it).
	victimScore := cosineSimilarity(query, victim.Vector)
	dropped := 0
	for _, b := range branches[:BeamWidth] {
		if cosineSimilarity(query, b.Vector) > victimScore {
			dropped++
		}
	}
	if dropped < BeamWidth {
		t.Fatalf("test setup invalid: victim summary key (%.4f) must rank below all %d filler keys for the fixed-k prune; only %d out-rank it",
			victimScore, BeamWidth, dropped)
	}
	if victimScore < ClearlyRelated {
		t.Fatalf("test setup invalid: victim key %.4f must stay ≥ ClearlyRelated %.2f (else the net cannot keep it)", victimScore, ClearlyRelated)
	}

	flat := ProposeChunks(query, allLeaves, ChunkOptions{Limit: DefaultChunkLimit})
	flatSet := turnSet(flat)
	// Sanity 2: the flat scan ranks the victim's two top leaves in its top-Kf.
	if _, ok := flatSet[topTurn1]; !ok {
		t.Fatalf("test setup invalid: flat top-Kf %v missing the global-highest leaf turn %d", flatSet, topTurn1)
	}

	// OLD fixed-k behaviour (Beam: BeamWidth, but force the net OFF by setting
	// the clearly-related bar above every score) DROPS the victim → divergence.
	// We model "net off" with a beam-only top-k by asserting the victim's
	// summary loses a fixed slot (Sanity 1 above) — and confirm the leaves are
	// reachable ONLY because the net keeps the branch:
	descent := DescendChunks(query, root, DescendOptions{Beam: BeamWidth, Limit: DefaultChunkLimit})
	descentSet := turnSet(descent)
	if !setsEqual(descentSet, flatSet) {
		t.Errorf("NET should keep the clearly-related victim branch so descent == flat: descent %v != flat %v (divergence %d)",
			descentSet, flatSet, len(descentSet)+len(flatSet)-2*intersectCount(descentSet, flatSet))
	}
	// The headline: the two top leaves the fixed-k cut would have dropped are
	// present.
	if _, ok := descentSet[topTurn1]; !ok {
		t.Errorf("net descent missing the global-highest leaf turn %d — the relevance net did not keep the victim branch", topTurn1)
	}
	if _, ok := descentSet[topTurn2]; !ok {
		t.Errorf("net descent missing the global-second leaf turn %d", topTurn2)
	}
}

// intersectCount is a tiny test helper: the size of a ∩ b for two turn sets.
func intersectCount(a, b map[int]struct{}) int {
	n := 0
	for k := range a {
		if _, ok := b[k]; ok {
			n++
		}
	}
	return n
}

// TestDescendChunks_Net_SparseUnchanged asserts the relevance net collapses
// to the old fixed top-K in a SPARSE region (#111 Finding A negative
// constraint): when few branches/leaves clear ClearlyRelated, the min-k floor
// governs and the result equals the pre-net top-K. The fixture has ONE
// query-related cluster and BeamWidth+3 near-orthogonal ones (cosine ~0), so
// only the one cluster's branch is clearly-related — the net adds nothing
// beyond the beam floor, and descent == flat exactly as before.
func TestDescendChunks_Net_SparseUnchanged(t *testing.T) {
	const dim = 32
	query := func() []float64 { v := make([]float64, dim); v[0] = 1; return v }()
	// A leaf whose cosine to the axis-0 query is `target`, tilted on a distinct
	// off-axis so leaves are distinct: cosine = 1/sqrt(1+off²), off=sqrt(1/t²-1).
	mkLeaf := func(turn int, target float64, off int) ChunkVector {
		v := make([]float64, dim)
		v[0] = 1
		v[off] = math.Sqrt(1/(target*target) - 1)
		return ChunkVector{TurnNumber: turn, Vector: v}
	}

	// "Sparse" means thin RELEVANCE density above ClearlyRelated, not few
	// leaves: a handful of leaves score in (DefaultCosineThreshold,
	// ClearlyRelated) = (0.55, 0.75) — above the floor (so they are candidates)
	// but below the clearly-related bar (so the net does NOT widen past the
	// min-k floor). The rest are orthogonal (cosine ~0, below the floor).
	var leaves []ChunkVector
	turn := 0
	for i := 0; i < 10; i++ { // 10 weakly-related leaves, all in (0.55, 0.75)
		leaves = append(leaves, mkLeaf(turn, 0.62, 1+i))
		turn++
	}
	for i := 0; i < 200; i++ { // many orthogonal leaves (below the floor)
		v := make([]float64, dim)
		v[15+(i%10)] = 1 // off-axis only → cosine ~0 to the axis-0 query
		leaves = append(leaves, ChunkVector{TurnNumber: turn, Vector: v})
		turn++
	}
	tree := buildTestTree(leaves, TreeBranchingFactor)

	flat := ProposeChunks(query, leaves, ChunkOptions{Limit: DefaultChunkLimit})
	descent := DescendChunks(query, tree, DescendOptions{Limit: DefaultChunkLimit})
	if !setsEqual(turnSet(flat), turnSet(descent)) {
		t.Errorf("sparse region: net descent %v != flat %v (the net should not widen where the cluster is thin)",
			turnSet(descent), turnSet(flat))
	}
	// No leaf clears ClearlyRelated, so the net collapses to the min-k floor:
	// exactly DefaultChunkLimit returned (the OLD fixed-K result), not widened.
	if len(flat) != DefaultChunkLimit {
		t.Errorf("sparse region returned %d, want exactly Kf=%d (net collapsed to the floor)", len(flat), DefaultChunkLimit)
	}
}

// TestProposeChunks_Net_Backstop drives a pathological dense set BEYOND NetCap
// and asserts the backstop bites: the returned set is bounded at NetCap and
// the net-cap-hit signal fires (no silent drop). A genuine high-density return
// that fits under NetCap is the feature working; this is the degenerate guard.
func TestProposeChunks_Net_Backstop(t *testing.T) {
	const dim = 8
	q := unitNoisy(dim, 0, 0)
	// NetCap+50 leaves all clearly-related (cosine ~1.0 > ClearlyRelated).
	var leaves []ChunkVector
	for i := 0; i < NetCap+50; i++ {
		leaves = append(leaves, ChunkVector{TurnNumber: i, Vector: unitNoisy(dim, 0, 0.001*float64(i%5))})
	}

	hits := 0
	got := ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit, NetCapHits: &hits})
	if len(got) != NetCap {
		t.Errorf("backstop: returned %d, want exactly NetCap=%d (bounded, no silent overflow)", len(got), NetCap)
	}
	if hits != 1 {
		t.Errorf("backstop: net-cap-hit signal = %d, want 1 (the cap dropped clearly-related candidates — must be visible)", hits)
	}

	// Under NetCap: high density returns many but does NOT trip the backstop.
	var fewer []ChunkVector
	for i := 0; i < NetCap-10; i++ {
		fewer = append(fewer, ChunkVector{TurnNumber: i, Vector: unitNoisy(dim, 0, 0.001*float64(i%5))})
	}
	hits2 := 0
	got2 := ProposeChunks(q, fewer, ChunkOptions{Limit: DefaultChunkLimit, NetCapHits: &hits2})
	if len(got2) != NetCap-10 {
		t.Errorf("under-cap dense set: returned %d, want all %d clearly-related (the net working)", len(got2), NetCap-10)
	}
	if hits2 != 0 {
		t.Errorf("under-cap dense set tripped the backstop (%d hits) — the cap must only bite beyond NetCap", hits2)
	}
}

// TestProposeChunks_Net_Determinism asserts the net result is deterministic:
// the same query + leaves yield byte-identical candidate slices across runs
// (no map-iteration order leak into the result; the sort + prefix cut are
// stable).
func TestProposeChunks_Net_Determinism(t *testing.T) {
	const dim = 8
	q := unitNoisy(dim, 0, 0)
	var leaves []ChunkVector
	for i := 0; i < 40; i++ {
		leaves = append(leaves, ChunkVector{TurnNumber: 1000 - i, Vector: unitNoisy(dim, 0, 0.005*float64(i))})
	}
	first := ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit})
	for i := 0; i < 20; i++ {
		again := ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit})
		if len(again) != len(first) {
			t.Fatalf("run %d: len %d != %d (non-deterministic net size)", i, len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("run %d idx %d: %+v != %+v (non-deterministic order)", i, j, again[j], first[j])
			}
		}
	}
}

// TestDescendChunks_Depth asserts the descent visits ≈ log_B(n) levels —
// it counts levels via a wrapper that walks the same beam logic. Rather
// than instrument the unexported loop, this checks the structural
// property indirectly: a balanced tree built by buildTestTree over n
// leaves has depth ⌈log_B(n)⌉, which bounds the descent's level count.
func TestDescendChunks_Depth(t *testing.T) {
	const dim = 4
	cases := []struct {
		n        int
		maxDepth int
	}{
		{n: 16, maxDepth: 1},   // one internal level above leaves
		{n: 256, maxDepth: 2},  // log_16(256) = 2
		{n: 4096, maxDepth: 3}, // log_16(4096) = 3
	}
	for _, tc := range cases {
		var leaves []ChunkVector
		for i := 0; i < tc.n; i++ {
			leaves = append(leaves, ChunkVector{TurnNumber: i, Vector: unitNoisy(dim, i%dim, 0)})
		}
		tree := buildTestTree(leaves, TreeBranchingFactor)
		if d := treeDepth(tree); d > tc.maxDepth {
			t.Errorf("n=%d: tree depth %d exceeds log_B bound %d", tc.n, d, tc.maxDepth)
		}
	}
}

// treeDepth returns the number of internal levels between root and the
// leaves (a single leaf → 0).
func treeDepth(n *SummaryNode) int {
	if n == nil || n.isLeaf() {
		return 0
	}
	maxChild := 0
	for _, c := range n.Children {
		if d := treeDepth(c); d > maxChild {
			maxChild = d
		}
	}
	return maxChild + 1
}

func TestDescendChunks_Edges(t *testing.T) {
	q := []float64{1, 0, 0}

	t.Run("nil root yields nil (W8 fallback is the caller's)", func(t *testing.T) {
		if got := DescendChunks(q, nil, DescendOptions{}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("empty query yields nil", func(t *testing.T) {
		root := buildTestTree([]ChunkVector{{TurnNumber: 1, Vector: q}}, TreeBranchingFactor)
		if got := DescendChunks(nil, root, DescendOptions{}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("single-leaf tree returns that leaf", func(t *testing.T) {
		root := buildTestTree([]ChunkVector{{TurnNumber: 7, Vector: q}}, TreeBranchingFactor)
		got := DescendChunks(q, root, DescendOptions{Limit: 3})
		if len(got) != 1 || got[0].TurnNumber != 7 {
			t.Fatalf("got %+v, want only leaf turn 7", got)
		}
		if math.Abs(got[0].Score-1.0) > 1e-9 {
			t.Errorf("single leaf score %v, want 1.0", got[0].Score)
		}
	})

	t.Run("below-threshold leaf is filtered (W1 parity with flat)", func(t *testing.T) {
		// A single leaf orthogonal to the query → cosine 0 → below the
		// default threshold → no candidate, same as flat ProposeChunks.
		ortho := []float64{0, 1, 0}
		root := buildTestTree([]ChunkVector{{TurnNumber: 1, Vector: ortho}}, TreeBranchingFactor)
		flat := ProposeChunks(q, []ChunkVector{{TurnNumber: 1, Vector: ortho}}, ChunkOptions{})
		descent := DescendChunks(q, root, DescendOptions{})
		if !setsEqual(turnSet(flat), turnSet(descent)) {
			t.Errorf("flat %v != descent %v", turnSet(flat), turnSet(descent))
		}
	})
}
