package scoring

import (
	"math"
	"reflect"
	"testing"
)

// clusteredLeaves builds n leaves arranged in `axes` semantic clusters: each
// leaf points mostly along one axis with a small per-leaf perturbation (via
// the existing unitNoisy helper), so leaves of the same axis are cosine-close
// and different axes are cosine-far. Turn numbers are sequential, so the
// recency tie-break is exercised but the dominant signal is semantic.
func clusteredLeaves(dim, axes, perAxis int) []ChunkVector {
	out := make([]ChunkVector, 0, axes*perAxis)
	turn := 0
	for a := 0; a < axes; a++ {
		for j := 0; j < perAxis; j++ {
			out = append(out, ChunkVector{TurnNumber: turn, Vector: unitNoisy(dim, a%dim, 0.03*float64(j))})
			turn++
		}
	}
	return out
}

// maxFanIn returns the largest Children count of any internal node.
func maxFanIn(n *SummaryNode) int {
	if n == nil || n.isLeaf() {
		return 0
	}
	max := len(n.Children)
	for _, c := range n.Children {
		if f := maxFanIn(c); f > max {
			max = f
		}
	}
	return max
}

// collectLeafTurns returns the turn numbers of all leaves under n (DFS).
func collectLeafTurns(n *SummaryNode) []int {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return []int{n.Leaf.TurnNumber}
	}
	var out []int
	for _, c := range n.Children {
		out = append(out, collectLeafTurns(c)...)
	}
	return out
}

// topologySig is a structural signature of a tree (per-node child counts +
// leaf turn numbers, DFS): two trees with identical signatures are the same
// topology — the W4 determinism check.
func topologySig(n *SummaryNode) []int {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return []int{-1, n.Leaf.TurnNumber} // -1 marks a leaf
	}
	sig := []int{len(n.Children)}
	for _, c := range n.Children {
		sig = append(sig, topologySig(c)...)
	}
	return sig
}

// TestBuildTree_Deterministic is the W4 gate: the same leaves yield an
// identical topology across independent builds.
func TestBuildTree_Deterministic(t *testing.T) {
	leaves := clusteredLeaves(8, 8, 16) // 128 leaves
	a := BuildTree(leaves, TreeBranchingFactor, nil)
	b := BuildTree(leaves, TreeBranchingFactor, nil)
	if !reflect.DeepEqual(topologySig(a), topologySig(b)) {
		t.Fatal("BuildTree is non-deterministic: same leaves produced different topologies")
	}
}

// TestBuildTree_DepthAndFanIn checks the structural-health properties: depth
// ≈ log_B(n) and fan-in ≤ B (design §3 / §7.2).
func TestBuildTree_DepthAndFanIn(t *testing.T) {
	cases := []struct{ axes, perAxis int }{
		{8, 16},  // 128 leaves
		{16, 16}, // 256 leaves
		{32, 16}, // 512 leaves
	}
	for _, tc := range cases {
		leaves := clusteredLeaves(16, tc.axes, tc.perAxis)
		n := len(leaves)
		tree := BuildTree(leaves, TreeBranchingFactor, nil)

		if got := len(collectLeafTurns(tree)); got != n {
			t.Errorf("n=%d: tree covers %d leaves, want %d", n, got, n)
		}
		if fan := maxFanIn(tree); fan > TreeBranchingFactor {
			t.Errorf("n=%d: max fan-in %d exceeds B=%d", n, fan, TreeBranchingFactor)
		}
		// internal depth (levels above the leaf row) ≈ log_B(n), ±1 for the
		// per-level ceil. treeDepth counts the leaf level too, hence -1.
		wantDepth := int(math.Ceil(math.Log(float64(n)) / math.Log(float64(TreeBranchingFactor))))
		depth := treeDepth(tree) - 1
		if depth < wantDepth-1 || depth > wantDepth+1 {
			t.Errorf("n=%d: internal depth %d, want ≈ log_B(n)=%d (±1)", n, depth, wantDepth)
		}
	}
}

// TestBuildTree_W1_MatchesFlat is the headline W1 end-to-end (design §7.1):
// build a tree from N leaves, then DescendChunks over it returns the SAME
// top-Kf leaf set the flat ProposeChunks scan returns, for every cluster's
// query — divergence 0. Centroid summary keys + semantic clustering preserve
// recall. (Distinct from descend_test's W1: that one builds the tree with the
// contiguous test helper; this one builds it with the PRODUCTION BuildTree
// clusterer — proving the clusterer itself, not just the descent, is W1-sound.)
func TestBuildTree_W1_MatchesFlat(t *testing.T) {
	const dim, axes, perAxis = 8, 8, 16
	leaves := clusteredLeaves(dim, axes, perAxis)
	tree := BuildTree(leaves, TreeBranchingFactor, nil)

	for a := 0; a < axes; a++ {
		q := unitNoisy(dim, a%dim, 0.03*float64(perAxis/2)) // aimed at cluster a
		descent := DescendChunks(q, tree, DescendOptions{Limit: DefaultChunkLimit})
		flat := ProposeChunks(q, leaves, ChunkOptions{Limit: DefaultChunkLimit})
		if !setsEqual(turnSet(descent), turnSet(flat)) {
			t.Errorf("axis %d: descent top-Kf %v diverges from flat %v", a, turnSet(descent), turnSet(flat))
		}
	}
}

// TestBuildTree_Degenerate covers the empty and single-leaf inputs.
func TestBuildTree_Degenerate(t *testing.T) {
	if BuildTree(nil, TreeBranchingFactor, nil) != nil {
		t.Error("empty leaf set should build a nil tree")
	}
	one := []ChunkVector{{TurnNumber: 7, Vector: []float64{1, 0}}}
	tree := BuildTree(one, TreeBranchingFactor, nil)
	if tree == nil || !tree.isLeaf() || tree.Leaf.TurnNumber != 7 {
		t.Errorf("single leaf should build a one-leaf tree, got %+v", tree)
	}
}
