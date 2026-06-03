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

// centroidOnlySummarizer is the N=1 (single-centroid) descent key the
// exemplar set replaces — the degenerate medoid-only route, modeled here as
// the literal cluster centroid so the W1-failure-mode test can contrast it
// against the exemplar spread over the IDENTICAL topology. It returns a
// one-element set {centroid}, so descent routes each node by a single point.
func centroidOnlySummarizer(children []*SummaryNode) [][]float64 {
	if len(children) == 0 {
		return nil
	}
	return [][]float64{centroidOf(children)}
}

// TestExemplarSet_RecoversCentroidMiss is THE #111 Finding A headline test:
// it constructs the exact W1 failure mode the exemplar set fixes — a query
// whose best leaf sits under a HETEROGENEOUS cluster whose CENTROID cosine-
// misses the query (so a single-centroid key prunes that branch at the beam
// and loses the leaf), and proves that
//
//   - single-centroid routing (N=1, centroidOnlySummarizer) MISSES the leaf
//     (descent top-Kf != flat top-Kf — a real W1 divergence), while
//   - exemplar-set routing (ExemplarSummarizer, N spanning the spread)
//     RECOVERS it (descent top-Kf == flat top-Kf — divergence 0).
//
// Construction: BeamWidth tight DECOY clusters on distinct axes whose
// centroids each out-cosine the target cluster's centroid against the query,
// plus ONE heterogeneous TARGET cluster that contains the single exact-match
// leaf but whose centroid points nowhere near the query. With a beam of
// BeamWidth, the BeamWidth decoy centroids fill every beam slot and the
// target's centroid is pruned (N=1 loses the leaf). The target's EXEMPLAR
// SET, by spanning the cluster, includes the exact-match leaf's vector as an
// exemplar, so the target node's MAX-cosine score wins a beam slot and the
// leaf survives (the spread closes the gap the centroid left).
func TestExemplarSet_RecoversCentroidMiss(t *testing.T) {
	const dim = 16
	axis0 := func(v float64) []float64 { e := make([]float64, dim); e[0] = v; return e }
	query := axis0(1) // pure axis 0

	var allLeaves []ChunkVector
	var clusters []*SummaryNode
	turn := 0

	// BeamWidth decoy clusters. Each leaf points MOSTLY at a distinct off-axis
	// direction but carries a moderate, consistent axis-0 component, so the
	// decoy's CENTROID has a healthy axis-0 cosine (it out-cosines the
	// heterogeneous target's centroid) yet NO single decoy leaf is the exact
	// axis-0 match the flat scan wants. Distinct off-axis directions keep the
	// decoy leaves from themselves ranking in the flat top-Kf.
	for d := 0; d < BeamWidth; d++ {
		offAxis := 1 + d // axes 1..BeamWidth, never axis 0
		var leaves []ChunkVector
		for j := 0; j < 4; j++ {
			v := make([]float64, dim)
			v[0] = 0.6                          // shared axis-0 pull → strong centroid cosine
			v[offAxis] = 1.0 + 0.05*float64(j)  // dominant off-axis direction, slight per-leaf spread
			leaves = append(leaves, ChunkVector{TurnNumber: turn, Vector: v})
			turn++
		}
		allLeaves = append(allLeaves, leaves...)
		clusters = append(clusters, &SummaryNode{Children: leafNodes(leaves)})
	}

	// The heterogeneous TARGET cluster: ONE exact axis-0 match (the best leaf)
	// plus distinct off-axis leaves carrying a NEGATIVE axis-0 component, so
	// the cluster's mean cancels most of the axis-0 signal — its centroid
	// cosine-misses the query (below every decoy centroid), yet it CONTAINS
	// the leaf the flat scan ranks #1.
	bestTurn := turn
	mk := func(off int, a0 float64) []float64 {
		v := make([]float64, dim)
		v[0] = a0
		if off >= 0 {
			v[off] = 1.0
		}
		return v
	}
	targetLeaves := []ChunkVector{
		{TurnNumber: turn, Vector: mk(-1, 1.0)},     // exact axis-0 match — the best leaf
		{TurnNumber: turn + 1, Vector: mk(5, -0.5)}, // off-axis, negative axis-0 → dilutes centroid
		{TurnNumber: turn + 2, Vector: mk(9, -0.5)},
		{TurnNumber: turn + 3, Vector: mk(13, -0.5)},
	}
	allLeaves = append(allLeaves, targetLeaves...)
	target := &SummaryNode{Children: leafNodes(targetLeaves)}
	clusters = append(clusters, target)

	// Build two roots over the SAME topology, differing only in the summary
	// key: N=1 centroid vs the exemplar spread.
	rootN1 := &SummaryNode{Children: clusters}
	rootSpread := &SummaryNode{Children: cloneClusters(clusters)}
	applySummaryKey(rootN1, centroidOnlySummarizer)
	applySummaryKey(rootSpread, ExemplarSummarizer)

	flat := ProposeChunks(query, allLeaves, ChunkOptions{Limit: DefaultChunkLimit})
	if _, ok := turnSet(flat)[bestTurn]; !ok {
		t.Fatalf("test setup invalid: flat scan did not rank the exact-match leaf (turn %d); got %v", bestTurn, turnSet(flat))
	}

	// Sanity: the target centroid really does cosine-MISS relative to the
	// weakest decoy centroid, so a single-centroid beam prunes the target.
	targetCentroid := centroidOf(target.Children)
	targetScore := cosineSimilarity(query, targetCentroid)
	for _, c := range clusters[:BeamWidth] {
		if s := cosineSimilarity(query, centroidOf(c.Children)); !(s > targetScore) {
			t.Fatalf("test setup invalid: a decoy centroid (%.4f) must out-cosine the target centroid (%.4f) for the N=1 prune", s, targetScore)
		}
	}

	n1 := DescendChunks(query, rootN1, DescendOptions{Limit: DefaultChunkLimit})
	if setsEqual(turnSet(n1), turnSet(flat)) {
		t.Fatalf("N=1 centroid routing should MISS the leaf under the cosine-missing target centroid: descent %v == flat %v (no divergence — the failure mode is not reproduced)",
			turnSet(n1), turnSet(flat))
	}
	if _, ok := turnSet(n1)[bestTurn]; ok {
		t.Fatalf("N=1 centroid routing unexpectedly recovered the best leaf (turn %d): %v", bestTurn, turnSet(n1))
	}

	spread := DescendChunks(query, rootSpread, DescendOptions{Limit: DefaultChunkLimit})
	if !setsEqual(turnSet(spread), turnSet(flat)) {
		t.Errorf("exemplar-set routing should RECOVER the leaf the centroid missed: descent %v != flat %v (W1 divergence the spread must close)",
			turnSet(spread), turnSet(flat))
	}
}

// cloneClusters deep-copies a slice of internal-node subtrees (children +
// leaves) so two roots can carry independent summary keys over the same
// topology without aliasing.
func cloneClusters(clusters []*SummaryNode) []*SummaryNode {
	out := make([]*SummaryNode, len(clusters))
	for i, c := range clusters {
		out[i] = cloneNode(c)
	}
	return out
}

func cloneNode(n *SummaryNode) *SummaryNode {
	if n.isLeaf() {
		return &SummaryNode{Vector: n.Vector, Vectors: n.Vectors, Leaf: n.Leaf}
	}
	children := make([]*SummaryNode, len(n.Children))
	for i, c := range n.Children {
		children[i] = cloneNode(c)
	}
	return &SummaryNode{Children: children}
}

// applySummaryKey populates every node's exemplar set bottom-up: a leaf's set
// is its own chunk vector; an internal node's set is summarize(children). It
// mirrors what BuildTree does, applied to a hand-built topology so a test can
// swap the summary-key strategy over a fixed tree shape.
func applySummaryKey(n *SummaryNode, summarize Summarizer) {
	if n.isLeaf() {
		n.Vectors = [][]float64{n.Leaf.Vector}
		n.Vector = n.Leaf.Vector
		return
	}
	for _, c := range n.Children {
		applySummaryKey(c, summarize)
	}
	exemplars := summarize(n.Children)
	n.Vectors = exemplars
	if len(exemplars) > 0 {
		n.Vector = exemplars[0]
	}
}

// TestExemplarSet_ComposesUpTree is the #111 Finding A RESIDUAL W1 gate: the
// exemplar-spread coverage property must COMPOSE up a multi-level tree, not
// just hold one level. The earlier summarizer spread over each child's PRIMARY
// vector only (its medoid), so an extreme leaf under a central-medoid level-1
// node was averaged away at the level ABOVE it — the branch holding the single
// highest-cosine leaf for a query was pruned at the top of a deep tree (a real
// W1 divergence on a thread deep enough to need ≥2 internal levels). Pooling
// the children's FULL exemplar sets keeps that extreme leaf a candidate at
// every level, so it propagates to the root and its branch stays alive.
//
// The construction targets the SECOND-level prune precisely (a hand-built
// 3-level topology, so the failure mode is controlled, not luck-of-the-
// clusterer): BeamWidth decoy level-2 subtrees whose level-1 medoids all out-
// cosine the query, plus ONE target level-2 subtree containing the exact-match
// extreme leaf — but the extreme leaf sits inside a target level-1 node whose
// MEDOID points elsewhere. Under child-primary pooling, the target level-2
// node's exemplar set is its level-1 children's MEDOIDS (the extreme leaf's
// own vector never propagates past its level-1 node), so the target level-2
// node cosine-misses the query, the BeamWidth decoys fill the top-level beam,
// and the branch is pruned. Under full-pool propagation the extreme leaf
// remains an exemplar of its level-1 node AND therefore a candidate for its
// level-2 node's set, so the target level-2 node scores ~1.0 and survives. The
// test contrasts the two summary keys over the SAME hand-built topology.
func TestExemplarSet_ComposesUpTree(t *testing.T) {
	const dim = 64
	axis := func(a int, mag float64) []float64 { v := make([]float64, dim); v[a] = mag; return v }
	query := axis(0, 1) // pure axis 0 — the extreme leaf's defining direction

	turn := 0
	mkLeaf := func(v []float64) ChunkVector { lv := ChunkVector{TurnNumber: turn, Vector: v}; turn++; return lv }

	// l1 builds a level-1 internal node over `count` leaves, each pointing
	// mostly at `dom` (so the node's medoid points at `dom`) with a tiny
	// per-leaf perturbation. Returns the node and the leaves it owns.
	l1 := func(dom int, count int) (*SummaryNode, []ChunkVector) {
		var lvs []ChunkVector
		for j := 0; j < count; j++ {
			v := axis(dom, 1.0)
			v[(dom+1)%dim] += 0.01 * float64(j)
			lvs = append(lvs, mkLeaf(v))
		}
		return &SummaryNode{Children: leafNodes(lvs)}, lvs
	}

	var allLeaves []ChunkVector
	var level2 []*SummaryNode

	// BeamWidth DECOY level-2 subtrees. Each holds two level-1 nodes whose
	// leaves carry a moderate axis-0 component, so the decoy's level-1 medoids
	// (and thus its level-2 exemplar set under EITHER pooling) out-cosine the
	// query — they legitimately fill the top beam. Decoy off-axes are distinct
	// per level-2 subtree (so decoys don't all collapse together) but reused
	// within it; none is axis 0, and the axis-0 pull (0.7) is below the extreme
	// leaf's (1.0) so no decoy leaf itself ranks in the flat top-Kf.
	for d := 0; d < BeamWidth; d++ {
		off := 1 + d // axes 1..BeamWidth
		var l1nodes []*SummaryNode
		for s := 0; s < 2; s++ {
			var lvs []ChunkVector
			for j := 0; j < 2; j++ {
				v := axis(off, 1.0)
				v[0] = 0.7 // shared axis-0 pull → medoid out-cosines the query
				lvs = append(lvs, mkLeaf(v))
			}
			allLeaves = append(allLeaves, lvs...)
			l1nodes = append(l1nodes, &SummaryNode{Children: leafNodes(lvs)})
		}
		level2 = append(level2, &SummaryNode{Children: l1nodes})
	}

	// The TARGET level-2 subtree. Its level-1 children all point at off-axes
	// with NO axis-0 component (their medoids cosine-MISS the query), EXCEPT
	// one level-1 node into which we plant the single exact axis-0 extreme leaf
	// among off-axis siblings — so that node's MEDOID is off-axis too. The
	// extreme leaf is the only axis-0 signal in the entire target subtree.
	var targetL1 []*SummaryNode
	for s := 0; s < 3; s++ {
		node, lvs := l1(20+s, 3) // off-axes 20..22, no axis-0 component
		allLeaves = append(allLeaves, lvs...)
		targetL1 = append(targetL1, node)
	}
	// Plant the extreme leaf into the FIRST target level-1 node, as a minority
	// among its off-axis siblings (so the node's medoid stays off-axis).
	extreme := mkLeaf(axis(0, 1.0))
	allLeaves = append(allLeaves, extreme)
	targetL1[0].Children = append(targetL1[0].Children, &SummaryNode{Vector: extreme.Vector, Leaf: extreme})
	level2 = append(level2, &SummaryNode{Children: targetL1})

	// Two 3-level roots over the SAME topology, differing only in the summary
	// key: child-primary pooling (the OLD behaviour) vs the full-pool spread.
	rootOld := &SummaryNode{Children: cloneClusters(level2)}
	rootNew := &SummaryNode{Children: cloneClusters(level2)}
	applySummaryKey(rootNew, ExemplarSummarizer)
	applySummaryKey(rootOld, primaryOnlyExemplarSummarizer)

	if treeDepth(rootNew)-1 < 2 {
		t.Fatalf("test setup invalid: internal depth %d, want ≥2", treeDepth(rootNew)-1)
	}

	flat := ProposeChunks(query, allLeaves, ChunkOptions{Limit: DefaultChunkLimit})
	if _, ok := turnSet(flat)[extreme.TurnNumber]; !ok {
		t.Fatalf("test setup invalid: flat scan did not surface the extreme leaf (turn %d); got %v", extreme.TurnNumber, turnSet(flat))
	}

	// OLD (child-primary) pooling must LOSE the extreme leaf: its vector never
	// propagates past its level-1 node, so the target level-2 node cosine-
	// misses the query and is pruned by the BeamWidth decoys. If this does not
	// reproduce the miss, the construction is invalid (not a vacuous pass).
	old := DescendChunks(query, rootOld, DescendOptions{Limit: DefaultChunkLimit})
	if _, ok := turnSet(old)[extreme.TurnNumber]; ok {
		t.Fatalf("test setup invalid: child-primary pooling unexpectedly KEPT the extreme leaf (turn %d) — the compositional miss is not reproduced; got %v",
			extreme.TurnNumber, turnSet(old))
	}

	// NEW (full-pool) propagation must RECOVER it: the extreme leaf stays an
	// exemplar of its level-1 node and a candidate for the target level-2
	// node's set, so the branch survives to the root.
	got := DescendChunks(query, rootNew, DescendOptions{Limit: DefaultChunkLimit})
	if _, ok := turnSet(got)[extreme.TurnNumber]; !ok {
		t.Errorf("full-pool descent lost the extreme leaf (turn %d) — the exemplar spread did not compose up the tree; got=%v flat=%v",
			extreme.TurnNumber, turnSet(got), turnSet(flat))
	}

	// The property the fix depends on: every exemplar at every level is a real
	// leaf vector — no synthesized/averaged vector ever entered the pool.
	leafVecs := make([][]float64, len(allLeaves))
	for i, lv := range allLeaves {
		leafVecs[i] = lv.Vector
	}
	assertExemplarsAreLeafVectors(t, rootNew, leafVecs)
}

// primaryOnlyExemplarSummarizer reproduces the pre-fix behaviour: it spreads
// over each child's PRIMARY .Vector only (not the children's full exemplar
// sets), so a lower-level exemplar never propagates upward. Used only to
// contrast the OLD failure mode against the full-pool fix over one topology.
func primaryOnlyExemplarSummarizer(children []*SummaryNode) [][]float64 {
	// Reduce each child's exemplar set to its primary, then run the real
	// spread math — its pool is then exactly the child primaries, modelling
	// the pre-fix candidate pool over which lower exemplars never propagate.
	reduced := make([]*SummaryNode, len(children))
	for i, c := range children {
		reduced[i] = &SummaryNode{Vector: c.Vector, Vectors: [][]float64{c.Vector}, Children: c.Children}
	}
	return ExemplarSummarizer(reduced)
}

// assertExemplarsAreLeafVectors walks the tree and fails if any node's
// exemplar is not an exact copy of some real leaf vector.
func assertExemplarsAreLeafVectors(t *testing.T, n *SummaryNode, leafVecs [][]float64) {
	t.Helper()
	if n == nil {
		return
	}
	for i, ex := range n.Vectors {
		if !vecIsAmong(ex, leafVecs) {
			t.Errorf("exemplar %d is not a real leaf vector — a synthesized/averaged vector entered the pool", i)
		}
	}
	for _, c := range n.Children {
		assertExemplarsAreLeafVectors(t, c, leafVecs)
	}
}

// vecIsAmong reports whether v exactly equals one of the candidate vectors.
func vecIsAmong(v []float64, candidates [][]float64) bool {
	for _, c := range candidates {
		if reflect.DeepEqual(v, c) {
			return true
		}
	}
	return false
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
