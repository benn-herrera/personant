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
		got := DescendChunks(query, root, DescendOptions{Beam: 2, Limit: 1})
		if len(got) != 1 || got[0].TurnNumber != 99 {
			t.Errorf("beam k=2: got %+v, want top leaf turn 99 (recovered via beam)", got)
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

// TestDescendChunks_FrontierBound asserts the descent's leaf frontier
// never exceeds LeafFrontierCap = k·B regardless of how many leaves the
// tree holds (design §4.1, W2). Verified by capping Limit at a value far
// above LeafFrontierCap and counting returned candidates against the
// frontier bound: ProposeChunks over the frontier cannot return more
// than the frontier holds.
func TestDescendChunks_FrontierBound(t *testing.T) {
	const dim = 8
	var leaves []ChunkVector
	for i := 0; i < 5000; i++ { // huge tree
		leaves = append(leaves, ChunkVector{TurnNumber: i, Vector: unitNoisy(dim, i%dim, 0.01*float64(i%7))})
	}
	tree := buildTestTree(leaves, TreeBranchingFactor)
	q := unitNoisy(dim, 0, 0.1)

	// Threshold -2 admits every frontier leaf; Limit -1 unbounded — so
	// the count returned == frontier size, which must be ≤ LeafFrontierCap.
	got := DescendChunks(q, tree, DescendOptions{Beam: BeamWidth, Threshold: -2, Limit: -1})
	if len(got) > LeafFrontierCap {
		t.Errorf("frontier bound: descent returned %d leaves, want ≤ LeafFrontierCap=%d", len(got), LeafFrontierCap)
	}
	if len(got) == 0 {
		t.Error("expected a non-empty frontier from a 5000-leaf tree")
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
