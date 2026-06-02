package scoring

import "sort"

// Within-thread summary-hierarchy descent (design within-thread-summary-
// hierarchy.md §4). The engaged-thread intra-pass replaces the flat
// O(C_main) ProposeChunks scan over every retained chunk with an
// O(log n) beam descent over a per-thread summary tree. This file holds
// the PURE topology + descent math only: cosine over a tree of
// {vector, children} + leaf ChunkVectors. It knows nothing of how the
// summaries are generated (LLM), how the embeddings are produced, the
// derived cache, the sleep cycle, threads, or the FIFO — those live in
// measure / turn / the sleep-cycle builder (§9.1 negative constraint).

// Tree-shape and beam knobs (design §4.1; §9 calibration windows — start
// values, tuned by the §7 recall-preservation gate, not invented here).
const (
	// TreeBranchingFactor B is the summary-tree fan-in: each internal
	// node summarizes ~B children, so depth ≈ log_B(n). Wide enough that
	// depth stays tiny (10⁴ leaves → depth 4), narrow enough that a
	// level's beam rank over k·B children is cheap.
	TreeBranchingFactor = 16

	// BeamWidth k is the number of branches kept per descent level. This
	// is the W3 recall-preservation mechanism: descent is BEAM, never
	// greedy top-1. A relevant leaf can sit under an intermediate summary
	// whose vector cosine-misses the query (a summary averages a
	// heterogeneous cluster); greedy top-1 would prune that subtree and
	// lose the leaf. k>1 recovers it. Raised by the §7 gate if recall
	// diverges; never tuned by latency.
	BeamWidth = 4

	// LeafFrontierCap bounds the leaf set the terminal ProposeChunks
	// ranks: k·B, independent of total n (design §4.1). The frontier
	// after the last internal level is the children of the k surviving
	// branches — at most k·B of them.
	LeafFrontierCap = BeamWidth * TreeBranchingFactor

	// TreeBuildThreshold is the leaf count below which a summary tree is
	// pure overhead — a flat scan over so few chunks is already cheap.
	// The W8 fallback (the caller's, Inc B) flat-scans below this; here
	// it only documents the calibrated window (2·B ≈ 32).
	TreeBuildThreshold = 2 * TreeBranchingFactor
)

// SummaryNode is one node of a per-thread summary tree (design §2.1).
// It is a pure topology of {vector, children}: an internal node carries
// the embedding of its summary text in Vector and its child summary
// nodes in Children; a leaf node carries a single ChunkVector in Leaf
// and has no Children. The summary text itself, the LLM that produced
// it, and the childrenHash staleness key are NOT modeled here — scoring
// sees only the descent key (Vector) and the topology.
//
// A node is a leaf iff len(Children) == 0; its Leaf field then holds the
// fine-tier chunk vector that descent returns and ProposeChunks ranks.
// An internal node's Leaf is the zero value and ignored. The leaf's
// Vector mirrors Leaf.Vector (the descent key and the rank key are the
// same chunk embedding at the leaf — that identity is what makes W1
// cheap: the leaves descent reaches are the identical ChunkVectors the
// flat scan ranks).
type SummaryNode struct {
	Vector   []float64
	Children []*SummaryNode
	Leaf     ChunkVector
}

// isLeaf reports whether n is a leaf (carries a chunk, no children).
func (n *SummaryNode) isLeaf() bool { return len(n.Children) == 0 }

// DescendOptions governs DescendChunks. Zero-valued fields fall back to
// the documented design §4.1 defaults.
type DescendOptions struct {
	// Beam is the per-level branch count k. 0 → BeamWidth (4). Wider k
	// recovers more leaves under cosine-missing summaries (W3), at linear
	// descent cost. The §7 recall-preservation gate raises it on
	// divergence.
	Beam int

	// Threshold is the minimum cosine for the terminal leaf rank, passed
	// through to ProposeChunks. 0 → DefaultCosineThreshold.
	Threshold float64

	// Limit caps the returned leaf candidates (the §4.2 Kf cap), passed
	// through to ProposeChunks. 0 → DefaultChunkLimit; negative →
	// unbounded.
	Limit int

	// Counter, when non-nil, tallies the cosine comparisons this descent
	// performs — the MEASURED recall_query_cosine_ops instrument (design
	// §7.2). Only the PRUNED levels (where child count > beam) and the
	// terminal leaf rank compute cosines; a lossless level (child count <=
	// beam) computes none. This is precisely why the count is measured at
	// the call site, not modeled. nil on the production hot path.
	Counter *CosineCounter
}

// DescendChunks is the §4 beam descent over a per-thread summary tree.
// From root it keeps, at each level, the top-Beam child branches by
// cosine(query, child.Vector) — BEAM, never greedy top-1 (W3) — descends
// ~log_B(n) levels to a bounded leaf frontier (≤ LeafFrontierCap), then
// runs the terminal ProposeChunks over that frontier's ChunkVectors to
// rank the top-Limit leaves.
//
// It returns the same []ChunkCandidate shape as ProposeChunks, so the
// caller's intra-pass is uniform whether it descended a tree or flat-
// scanned (the W8 fallback). The terminal rank is the identical pure
// ProposeChunks the flat scan uses, on the same cosine operating point —
// the only difference from a flat scan is *which* leaves were considered.
// W1 (recall-preservation) is then exactly: the beam reached the leaves
// the flat scan would have ranked top-Limit.
//
// Cost is O(Beam · B · log_B(n) · D) descent + O(LeafFrontierCap · D)
// terminal rank (design §4.1, W2) — bounded by tree depth, independent
// of total n beyond the log.
//
// A nil root yields nil (the caller falls back to the flat scan, W8). A
// single-leaf tree returns that leaf if it clears the threshold. Pure
// function: no I/O, no LLM, no substrate.
func DescendChunks(query []float64, root *SummaryNode, opts DescendOptions) []ChunkCandidate {
	if root == nil || len(query) == 0 {
		return nil
	}
	beam := opts.Beam
	if beam == 0 {
		beam = BeamWidth
	}
	if beam < 1 {
		beam = 1
	}

	// Beam frontier: start at the root, descend while it holds internal
	// nodes, keeping the top-Beam children by cosine per level.
	frontier := []*SummaryNode{root}
	for hasInternal(frontier) {
		children := make([]*SummaryNode, 0, len(frontier)*TreeBranchingFactor)
		for _, n := range frontier {
			if n.isLeaf() {
				// A leaf reached before the deepest level survives into
				// the next frontier so it is not pruned by depth — its
				// branches are itself.
				children = append(children, n)
				continue
			}
			children = append(children, n.Children...)
		}
		frontier = topKByCosine(query, children, beam, opts.Counter)
	}

	// Frontier is now all leaves (≤ LeafFrontierCap). Rank them with the
	// terminal ProposeChunks — the SAME cosine the flat scan uses (W1).
	leaves := make([]ChunkVector, 0, len(frontier))
	for _, n := range frontier {
		leaves = append(leaves, n.Leaf)
	}
	return ProposeChunks(query, leaves, ChunkOptions{Threshold: opts.Threshold, Limit: opts.Limit, Counter: opts.Counter})
}

// hasInternal reports whether any node in the frontier is an internal
// node (has children to descend into).
func hasInternal(frontier []*SummaryNode) bool {
	for _, n := range frontier {
		if !n.isLeaf() {
			return true
		}
	}
	return false
}

// topKByCosine returns the top-k nodes by cosine(query, node.Vector),
// stable-sorted (cosine desc, then leaf turn-number asc for a
// deterministic tie-break). It is the per-level beam step. When k >=
// len(nodes) all nodes survive (no pruning), which is what keeps a short
// or wide level lossless.
func topKByCosine(query []float64, nodes []*SummaryNode, k int, counter *CosineCounter) []*SummaryNode {
	if len(nodes) <= k {
		// No pruning needed; preserve all branches (lossless level) — and,
		// crucially, compute ZERO cosines here. This short-circuit is the
		// O(log n) win the measured counter must reflect, so it bumps nothing.
		return nodes
	}
	type scored struct {
		node  *SummaryNode
		score float64
	}
	counter.add(len(nodes)) // one cosine per child node at this pruned level
	ranked := make([]scored, len(nodes))
	for i, n := range nodes {
		ranked[i] = scored{node: n, score: cosineSimilarity(query, n.Vector)}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].node.Leaf.TurnNumber < ranked[j].node.Leaf.TurnNumber
	})
	out := make([]*SummaryNode, k)
	for i := 0; i < k; i++ {
		out[i] = ranked[i].node
	}
	return out
}
