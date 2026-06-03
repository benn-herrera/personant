package measure

import (
	"context"
	"fmt"
	"testing"

	"personant/internal/model"
	"personant/internal/recall/scoring"
)

// This file is the #111 Inc B (within-thread summary hierarchy) internal
// test surface: it reaches the private indexSnapshot.tree field and the
// private intraChunks / intraThreadDivergence / treeUsable seams directly,
// which the external _test package cannot. It proves (a) the no-tree
// runtime default is byte-identical to the pre-#111 flat scan, (b) the
// descent path is taken when a usable tree is installed and returns the
// same top-Kf leaves the flat scan would (W1), (c) the divergence hook
// detects a real beam miss, and (d) the W8 below-threshold fallback.

// chunksFromVectors builds a fine-tier slice of chunk vectors with
// sequential turn numbers — the leaf set both the flat scan and the tree
// share.
func chunksFromVectors(vecs [][]float64) []scoring.ChunkVector {
	out := make([]scoring.ChunkVector, len(vecs))
	for i, v := range vecs {
		out[i] = scoring.ChunkVector{TurnNumber: i, Vector: v}
	}
	return out
}

// buildBalancedTree groups leaves into contiguous b-sized clusters
// bottom-up, giving each internal node the centroid of its children as its
// summary key — a faithful stand-in for an LLM summary embedding,
// sufficient to drive the descent math. (Mirrors scoring's test helper;
// duplicated here because that one is unexported to scoring's test
// package.) Deterministic given leaf order.
func buildBalancedTree(leaves []scoring.ChunkVector, b int) *scoring.SummaryNode {
	if len(leaves) == 0 {
		return nil
	}
	level := make([]*scoring.SummaryNode, len(leaves))
	for i, lv := range leaves {
		level[i] = &scoring.SummaryNode{Vector: lv.Vector, Leaf: lv}
	}
	for len(level) > 1 {
		var next []*scoring.SummaryNode
		for i := 0; i < len(level); i += b {
			end := i + b
			if end > len(level) {
				end = len(level)
			}
			group := level[i:end]
			next = append(next, &scoring.SummaryNode{
				Vector:   centroidVec(group),
				Children: group,
			})
		}
		level = next
	}
	return level[0]
}

func centroidVec(nodes []*scoring.SummaryNode) []float64 {
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

// axisVec is a roughly-unit vector along axis with a small perturbation on
// the next axis, so leaves cluster by axis but are distinct.
func axisVec(dim, axis int, perturb float64) []float64 {
	v := make([]float64, dim)
	v[axis] = 1.0
	v[(axis+1)%dim] = perturb
	return v
}

// snapWithFine builds a snapshot whose engaged thread has the given fine
// chunks and the given tree (nil for the no-tree default). It also stamps the
// parallel fineHash (one synthetic per-leaf hash, the chunk_hash stand-in)
// and, when a tree is supplied, the treeHash composed from those leaf hashes
// — so treeUsable's #111 Inc C childrenHash key matches a freshly installed
// full tree. A test that mutates the fine tier after building the tree leaves
// treeHash stale on purpose (the staleness path).
func snapWithFine(engaged string, fine []scoring.ChunkVector, tree *scoring.SummaryNode) *indexSnapshot {
	snap := emptySnapshot()
	snap.fine[engaged] = fine
	snap.fineHash[engaged] = synthLeafHashes(fine)
	if tree != nil {
		snap.tree[engaged] = tree
		snap.treeHash[engaged] = composeLeafHash(snap.fineHash[engaged])
	}
	return snap
}

// synthLeafHashes derives a stable per-leaf hash from each chunk's turn
// number — a deterministic stand-in for the .vec chunk_hash, sufficient for
// the childrenHash composition tests (they need the hash to change when a
// leaf is added/removed, which a turn-keyed hash gives).
func synthLeafHashes(fine []scoring.ChunkVector) []string {
	out := make([]string, len(fine))
	for i, cv := range fine {
		out[i] = contentHash(fmt.Sprintf("leaf-%d", cv.TurnNumber))
	}
	return out
}

// TestIntraChunks_NoTree_ByteIdenticalToFlat is the non-breaking gate: with
// no tree installed (the runtime default — nothing builds trees until Inc
// D), intraChunks returns exactly what the flat ProposeChunks over
// snap.fine[engaged] returns, and treeUsable reports false (the W8 fallback
// path is taken).
func TestIntraChunks_NoTree_ByteIdenticalToFlat(t *testing.T) {
	const dim = 8
	var vecs [][]float64
	for axis := 0; axis < dim; axis++ {
		for j := 0; j < 12; j++ { // 96 leaves, well above TreeBuildThreshold
			vecs = append(vecs, axisVec(dim, axis, 0.05*float64(j)))
		}
	}
	fine := chunksFromVectors(vecs)
	snap := snapWithFine("thr_eng", fine, nil)

	if snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=true with no tree installed; expected flat fallback")
	}

	q := axisVec(dim, 3, 0.1)
	got := (&Service{}).intraChunks(q, snap, "thr_eng", nil, nil)
	want := scoring.ProposeChunks(q, fine, scoring.ChunkOptions{Limit: Kf})
	if !sameTurns(got, want) {
		t.Errorf("no-tree intraChunks %v != flat ProposeChunks %v", turns(got), turns(want))
	}
}

// TestIntraChunks_Descent_MatchesFlat is the descent-path W1 check: install
// a usable tree → intraChunks descends it → the top-Kf leaf set equals the
// flat scan's over the same leaves (divergence == 0), the coherent case.
func TestIntraChunks_Descent_MatchesFlat(t *testing.T) {
	const dim = 8
	var vecs [][]float64
	for axis := 0; axis < dim; axis++ {
		for j := 0; j < 12; j++ {
			vecs = append(vecs, axisVec(dim, axis, 0.05*float64(j)))
		}
	}
	fine := chunksFromVectors(vecs)
	tree := buildBalancedTree(fine, scoring.TreeBranchingFactor)
	snap := snapWithFine("thr_eng", fine, tree)

	if !snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=false for a freshly built full tree above threshold")
	}

	svc := &Service{}
	for axis := 0; axis < dim; axis++ {
		q := axisVec(dim, axis, 0.1)
		got := svc.intraChunks(q, snap, "thr_eng", nil, nil)
		flat := scoring.ProposeChunks(q, fine, scoring.ChunkOptions{Limit: Kf})
		if !sameTurnSet(got, flat) {
			t.Errorf("axis %d: descent top-Kf %v != flat %v", axis, turns(got), turns(flat))
		}
		if d := svc.intraThreadDivergence(context.Background(), q, snap, "thr_eng"); d != 0 {
			t.Errorf("axis %d: divergence = %d, want 0 (coherent case)", axis, d)
		}
	}
}

// TestIntraThreadDivergence_DetectsBeamMiss proves the gate is not vacuous:
// a tree whose best leaf sits under a heterogeneous summary that cosine-
// misses the query is recovered by the default beam (divergence 0) but
// MISSED by a forced beam-1 descent. The hook returns >0 when the chosen
// descent diverges from flat — the real-divergence detection W1 needs.
//
// We exercise the beam-1 miss against the same flat scan by building the
// tree topology by hand (the §scoring W3 construction) and driving
// DescendChunks with Beam:1 vs the flat result, mirroring intraThreadDiver-
// gence's internals. The production hook uses the default beam (k=4) which
// recovers the leaf — so the gate at default beam is 0 here too, confirming
// the hook only fires on a genuine miss.
func TestIntraThreadDivergence_DetectsBeamMiss(t *testing.T) {
	query := []float64{1, 0, 0, 0}

	decoyLeaves := []scoring.ChunkVector{
		{TurnNumber: 1, Vector: []float64{0.9, 0.2, 0, 0}},
		{TurnNumber: 2, Vector: []float64{0.92, 0.15, 0, 0}},
		{TurnNumber: 3, Vector: []float64{0.88, 0.25, 0, 0}},
	}
	decoy := &scoring.SummaryNode{Children: leafNodesFor(decoyLeaves)}
	decoy.Vector = centroidVec(decoy.Children)

	targetLeaves := []scoring.ChunkVector{
		{TurnNumber: 99, Vector: []float64{1, 0, 0, 0}}, // the best leaf
		{TurnNumber: 10, Vector: []float64{0, 1, 0, 0}},
		{TurnNumber: 11, Vector: []float64{0, 0, 1, 0}},
		{TurnNumber: 12, Vector: []float64{0, 0, 0, 1}},
	}
	target := &scoring.SummaryNode{Children: leafNodesFor(targetLeaves)}
	target.Vector = centroidVec(target.Children) // heterogeneous → cosine-misses

	root := &scoring.SummaryNode{
		Children: []*scoring.SummaryNode{decoy, target},
		Vector:   []float64{0.5, 0.3, 0.1, 0.1},
	}
	allLeaves := append(append([]scoring.ChunkVector{}, decoyLeaves...), targetLeaves...)
	flat := scoring.ProposeChunks(query, allLeaves, scoring.ChunkOptions{Limit: Kf})

	// Forced beam-1: misses leaf 99 → diverges from the flat scan (>0).
	greedy := scoring.DescendChunks(query, root, scoring.DescendOptions{Beam: 1, Limit: Kf})
	if d := turnSetDifference(greedy, flat); d == 0 {
		t.Fatalf("forced beam-1 should diverge from flat (it prunes the cosine-missing target subtree); got divergence 0")
	}

	// Default beam recovers leaf 99 → divergence 0. Drive the production
	// hook with this tree installed (leaf count 7 < TreeBuildThreshold, so
	// treeUsable would fall back; bypass the threshold by asserting the raw
	// descent matches flat at the default beam).
	def := scoring.DescendChunks(query, root, scoring.DescendOptions{Limit: Kf})
	if d := turnSetDifference(def, flat); d != 0 {
		t.Errorf("default beam should match flat (divergence 0), got %d", d)
	}
}

// TestTreeUsable_BelowThreshold_FlatFallback is the W8 short-thread check:
// a present tree whose leaf count is below TreeBuildThreshold is NOT used —
// intraChunks falls back to the flat scan (a tree over so few chunks is
// pure overhead).
func TestTreeUsable_BelowThreshold_FlatFallback(t *testing.T) {
	const dim = 4
	// Fewer than TreeBuildThreshold leaves.
	n := scoring.TreeBuildThreshold - 1
	var vecs [][]float64
	for i := 0; i < n; i++ {
		vecs = append(vecs, axisVec(dim, i%dim, 0.01*float64(i)))
	}
	fine := chunksFromVectors(vecs)
	tree := buildBalancedTree(fine, scoring.TreeBranchingFactor)
	snap := snapWithFine("thr_eng", fine, tree)

	if snap.treeUsable("thr_eng") {
		t.Fatalf("treeUsable=true for %d leaves (< TreeBuildThreshold=%d); expected flat fallback",
			len(fine), scoring.TreeBuildThreshold)
	}

	q := axisVec(dim, 0, 0.1)
	got := (&Service{}).intraChunks(q, snap, "thr_eng", nil, nil)
	want := scoring.ProposeChunks(q, fine, scoring.ChunkOptions{Limit: Kf})
	if !sameTurns(got, want) {
		t.Errorf("below-threshold intraChunks %v != flat %v", turns(got), turns(want))
	}
	// And the divergence hook is 0 below threshold (no fast path).
	if d := (&Service{}).intraThreadDivergence(context.Background(), q, snap, "thr_eng"); d != 0 {
		t.Errorf("below-threshold divergence = %d, want 0", d)
	}
}

// TestTreeUsable_StaleChildrenHash_FlatFallback proves the #111 Inc C
// childrenHash staleness guard: a tree whose stored treeHash no longer matches
// the composed hash of the CURRENT leaf chunk hashes (a leaf added/trimmed/
// changed since the build) is not used — it falls back to the flat scan
// (W8 / G8). The tree is usable while the leaves are unchanged, and becomes
// stale the moment the leaf set moves.
func TestTreeUsable_StaleChildrenHash_FlatFallback(t *testing.T) {
	const dim = 8
	var vecs [][]float64
	for i := 0; i < 64; i++ {
		vecs = append(vecs, axisVec(dim, i%dim, 0.01*float64(i)))
	}
	fine := chunksFromVectors(vecs)
	tree := buildBalancedTree(fine, scoring.TreeBranchingFactor)

	// Install the tree against the original leaves: usable while unchanged.
	snap := snapWithFine("thr_eng", fine, tree)
	if !snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=false for a tree whose leaves are unchanged")
	}

	// Grow the fine tier by one leaf WITHOUT rebuilding the tree: the composed
	// leaf hash moves, so the stored treeHash no longer matches → stale.
	grownFine := append(append([]scoring.ChunkVector{}, fine...),
		scoring.ChunkVector{TurnNumber: 64, Vector: axisVec(dim, 0, 0.5)})
	snap.fine["thr_eng"] = grownFine
	snap.fineHash["thr_eng"] = synthLeafHashes(grownFine)

	if snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=true for a stale tree (leaf added since build); expected fallback")
	}

	// Trimming a leaf is symmetric: restore the tree's stamp, then trim.
	snap.fine["thr_eng"] = fine
	snap.fineHash["thr_eng"] = synthLeafHashes(fine)
	if !snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=false after restoring the original leaf set")
	}
	trimmed := fine[:len(fine)-1]
	snap.fine["thr_eng"] = trimmed
	snap.fineHash["thr_eng"] = synthLeafHashes(trimmed)
	if snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=true for a stale tree (leaf trimmed since build); expected fallback")
	}
}

// TestRebuildTrees_SleepCycleSeam is the Inc-D sleep-cycle wiring check: a
// Service whose published snapshot carries an above-threshold thread and a
// below-threshold thread gets trees populated by RebuildTrees (the sleep
// pass) for ONLY the above-threshold thread; the below-threshold thread
// stays tree-less (W8 flat fallback). After the rebuild the engaged
// above-threshold thread's intra-pass DESCENDS (treeUsable true) and the
// descent matches the flat scan (W1, divergence 0); the short thread still
// flat-scans.
func TestRebuildTrees_SleepCycleSeam(t *testing.T) {
	const dim = 8
	// Above-threshold engaged thread: 96 semantically-clustered leaves.
	var bigVecs [][]float64
	for axis := 0; axis < dim; axis++ {
		for j := 0; j < 12; j++ {
			bigVecs = append(bigVecs, axisVec(dim, axis, 0.05*float64(j)))
		}
	}
	big := chunksFromVectors(bigVecs)
	// Below-threshold thread: a handful of leaves.
	var smallVecs [][]float64
	for i := 0; i < scoring.TreeBuildThreshold-1; i++ {
		smallVecs = append(smallVecs, axisVec(dim, i%dim, 0.01*float64(i)))
	}
	small := chunksFromVectors(smallVecs)

	// A Service with a (non-nil) embedder so RebuildTrees does not no-op;
	// it does not embed here — the snapshot is seeded directly.
	svc := measureServiceWithSeededSnapshot(map[string][]scoring.ChunkVector{
		"thr_big":   big,
		"thr_small": small,
	})

	rebuilt, err := svc.RebuildTrees(context.Background())
	if err != nil {
		t.Fatalf("RebuildTrees: %v", err)
	}
	if rebuilt != 1 {
		t.Fatalf("rebuilt = %d, want 1 (only the above-threshold thread)", rebuilt)
	}

	snap := svc.cur.Load()
	if snap.tree["thr_big"] == nil {
		t.Fatal("above-threshold thread has no tree after RebuildTrees")
	}
	if snap.tree["thr_small"] != nil {
		t.Fatal("below-threshold thread should have no tree (W8)")
	}
	if !snap.treeUsable("thr_big") {
		t.Fatal("above-threshold tree not usable after rebuild")
	}
	if snap.treeUsable("thr_small") {
		t.Fatal("below-threshold thread should fall back to flat scan")
	}

	// The engaged above-threshold thread now descends, matching flat (W1).
	for axis := 0; axis < dim; axis++ {
		q := axisVec(dim, axis, 0.1)
		got := svc.intraChunks(q, snap, "thr_big", nil, nil)
		flat := scoring.ProposeChunks(q, big, scoring.ChunkOptions{Limit: Kf})
		if !sameTurnSet(got, flat) {
			t.Errorf("axis %d: post-rebuild descent %v != flat %v", axis, turns(got), turns(flat))
		}
	}

	// A second rebuild with the leaf set unchanged is a no-op (nothing
	// crossed the dirty threshold) — the bounded-churn property.
	again, err := svc.RebuildTrees(context.Background())
	if err != nil {
		t.Fatalf("second RebuildTrees: %v", err)
	}
	if again != 0 {
		t.Errorf("second rebuild = %d, want 0 (leaf set unchanged)", again)
	}
}

// TestRebuildTrees_NilEmbedder_NoTrees is I7/W8: a nil-embedder Service
// builds no trees (RebuildTrees no-ops), so the intra-pass always flat-scans.
func TestRebuildTrees_NilEmbedder_NoTrees(t *testing.T) {
	svc := &Service{}
	svc.cur.Store(emptySnapshot())
	n, err := svc.RebuildTrees(context.Background())
	if err != nil {
		t.Fatalf("RebuildTrees: %v", err)
	}
	if n != 0 {
		t.Errorf("nil-embedder rebuilt %d trees, want 0", n)
	}
}

// measureServiceWithSeededSnapshot builds a Service with a non-nil embedder
// (so RebuildTrees runs) and a published snapshot whose fine tier is the
// given threads. No indexer goroutine is started (Prepare is not called), so
// the snapshot is stable for the test.
func measureServiceWithSeededSnapshot(fine map[string][]scoring.ChunkVector) *Service {
	svc := &Service{embedder: model.NewMockEmbedder()}
	snap := emptySnapshot()
	for id, chunks := range fine {
		snap.fine[id] = chunks
	}
	svc.cur.Store(snap)
	return svc
}

func leafNodesFor(leaves []scoring.ChunkVector) []*scoring.SummaryNode {
	out := make([]*scoring.SummaryNode, len(leaves))
	for i, lv := range leaves {
		out[i] = &scoring.SummaryNode{Vector: lv.Vector, Leaf: lv}
	}
	return out
}

func turns(cs []scoring.ChunkCandidate) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.TurnNumber
	}
	return out
}

// sameTurns compares ordered turn slices (the flat scan and the no-tree
// intraChunks must be identical, ordering included).
func sameTurns(a, b []scoring.ChunkCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TurnNumber != b[i].TurnNumber {
			return false
		}
	}
	return true
}

// sameTurnSet compares turn sets (descent vs flat W1: set equality, order
// irrelevant).
func sameTurnSet(a, b []scoring.ChunkCandidate) bool {
	return turnSetDifference(a, b) == 0
}
