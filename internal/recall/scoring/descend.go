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
	// diverges; never tuned by latency. Raised 4→8 (2026-06-02) after a
	// 14d live-embedding rung showed W1 descent-vs-flat divergence 6/251
	// at k=4 over real nomic vectors (centroid keys are less separable
	// than the mock's feature-hash vectors); the §7 W1 gate drives this.
	BeamWidth = 8

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

	// ClearlyRelated is the "clearly related, not kidding" cosine: the bar
	// above which a candidate must NOT be dropped to satisfy a fixed-count
	// cut (#111 Finding A). It is the relevance-net knob distinct from
	// DefaultCosineThreshold (0.55, the relevance FLOOR — the bar a candidate
	// must clear to be considered AT ALL). Set DISTINCTLY above the floor at
	// 0.75: the live diagnosis showed a dense intra-thread cluster where ALL
	// leaves scored 0.80–0.83 and the fixed BeamWidth cut pruned the branch
	// holding the two HIGHEST-cosine leaves — they were clearly related yet
	// lost to an arbitrary slot count. 0.75 sits below that observed cluster
	// (so it captures it) and well above the 0.55 floor (so it does not
	// degrade to "keep everything above the floor", which would re-introduce
	// the O(n) scan this whole feature removes). A §9 calibration window:
	// raise toward the floor only if the net proves too narrow; the
	// recall-preservation gate (W1) and the cosine-ops bend are the signals.
	ClearlyRelated = 0.75

	// NetCap bounds the relevance-sized net at BOTH the beam frontier and
	// the terminal leaf rank (#111 Finding A) — a PERFORMANCE BACKSTOP, not
	// the normal path. The net is normally bounded by genuine relevance
	// density (the count of candidates clearing ClearlyRelated) plus the
	// min-k/Limit floor; NetCap only bites a degenerate pathological cluster
	// where nearly everything is clearly-related, which would otherwise let
	// the net degrade toward the O(n) scan. At 4·LeafFrontierCap it is well
	// above any healthy net (a wide spread of genuinely-related leaves still
	// fits) yet bounds the worst case. When the cap drops a clearly-related
	// candidate, the caller logs recall.net-cap-hit (via NetCapHits) so the
	// pathological tail is VISIBLE — a silent truncation would hide the very
	// density the feature exists to surface.
	NetCap = 4 * LeafFrontierCap

	// NodeExemplars N is the size of an internal node's EXEMPLAR SET — the
	// descent key (design §9 calibration window). The #111 Finding A fix:
	// a single centroid is a meaningless point in the middle of a
	// heterogeneous cluster's spread, and a query near the cluster's EDGE
	// cosine-misses it — the centroid prunes the branch and the relevant
	// leaf is lost even at a wide beam (raising k 6→4 at a full doubling
	// was diminishing — the loss is KEY lossiness, not beam). Covering the
	// cluster's spread with N exemplars and routing by the BEST (max)
	// exemplar keeps the branch alive for a query near ANY region of the
	// cluster. N=1 is the degenerate medoid-only key (≡ a single-vector
	// route); N=4 spans the medoid + the cluster's extremes (the
	// ExemplarSummarizer farthest-point spread). N is the recall/cost knob
	// ALONGSIDE k (BeamWidth): descent now does ×N cosines per scored
	// child, so the per-query cost is O(N·k·B·log_B(n)·D) — still bounded
	// and logarithmic in n. Keep N small.
	NodeExemplars = 4
)

// SummaryNode is one node of a per-thread summary tree (design §2.1).
// It is a pure topology of {exemplar-set, children}: an internal node
// carries a small EXEMPLAR SET in Vectors (the descent key) and its child
// summary nodes in Children; a leaf node carries a single ChunkVector in
// Leaf and has no Children. The summary text itself, the LLM that produced
// it, and the childrenHash staleness key are NOT modeled here — scoring
// sees only the descent key (Vectors) and the topology.
//
// The descent key is a SET, not a single centroid (#111 Finding A): an
// internal node routes by the MAX cosine over its exemplars, so a query
// near any region of a heterogeneous cluster's spread keeps the branch
// alive (a single centroid sits in the dead middle and cosine-misses an
// edge query, pruning the branch and losing a relevant leaf). Vector is
// the primary exemplar (Vectors[0], the medoid) — kept as a field for the
// degenerate single-vector route and for callers/tests that read one key.
//
// A node is a leaf iff len(Children) == 0; its Leaf field then holds the
// fine-tier chunk vector that descent returns and ProposeChunks ranks. A
// leaf's exemplar set is just its own chunk vector (Vectors == {Vector} ==
// {Leaf.Vector}) — one element, no spread, the W1-cheap identity (the
// descent key and the rank key are the same chunk embedding at the leaf,
// so the leaves descent reaches are the identical ChunkVectors the flat
// scan ranks). An internal node's Leaf is the zero value and ignored.
type SummaryNode struct {
	// Vector is the primary exemplar (Vectors[0]) — the medoid for an
	// internal node, the chunk vector for a leaf. Retained as a single-key
	// field for the degenerate N=1 route and back-compat with single-vector
	// callers; descent routes over the full Vectors set.
	Vector []float64
	// Vectors is the node's exemplar set, the max-cosine descent key
	// (#111 Finding A). Vectors[0] == Vector. A leaf has exactly one
	// exemplar (its chunk vector); an internal node has up to NodeExemplars
	// spanning its cluster's spread.
	Vectors  [][]float64
	Children []*SummaryNode
	Leaf     ChunkVector
}

// isLeaf reports whether n is a leaf (carries a chunk, no children).
func (n *SummaryNode) isLeaf() bool { return len(n.Children) == 0 }

// exemplars returns the node's descent-key exemplar set. It tolerates a
// node built with only Vector set (a single-vector caller, or a leaf
// constructed before the set was populated) by falling back to {Vector} —
// so a node is always routable by at least its primary key.
func (n *SummaryNode) exemplars() [][]float64 {
	if len(n.Vectors) > 0 {
		return n.Vectors
	}
	if n.Vector != nil {
		return [][]float64{n.Vector}
	}
	return nil
}

// maxCosine scores a node against the query by the BEST cosine over its
// exemplar set (#111 Finding A: route by max, not by a single centroid).
// It bumps the counter by the number of exemplars compared (the ×N cost
// the measured recall_query_cosine_ops must reflect). A node with no
// exemplars scores 0 and costs nothing.
func maxCosine(query []float64, n *SummaryNode, counter *CosineCounter) float64 {
	exs := n.exemplars()
	if len(exs) == 0 {
		return 0
	}
	counter.add(len(exs))
	best := cosineSimilarity(query, exs[0])
	for _, ex := range exs[1:] {
		if c := cosineSimilarity(query, ex); c > best {
			best = c
		}
	}
	return best
}

// DescendOptions governs DescendChunks. Zero-valued fields fall back to
// the documented design §4.1 defaults.
type DescendOptions struct {
	// Beam is the per-level branch MINIMUM-floor count k (#111 Finding A) —
	// no longer a hard cap. Each level keeps the top-Beam branches PLUS every
	// additional branch whose max-exemplar-cosine is at or above
	// ClearlyRelated, so a clearly-related branch is never pruned for losing
	// a fixed slot to an equally-related sibling (the live W1 gap: the branch
	// holding the two highest-cosine leaves was cut at the BeamWidth slot
	// boundary). 0 → BeamWidth (8). Wider k recovers more leaves under
	// cosine-missing summaries (W3); the §7 recall-preservation gate raises
	// it on divergence. The clearly-related widening is bounded by NetCap.
	Beam int

	// Threshold is the minimum cosine for the terminal leaf rank, passed
	// through to ProposeChunks. 0 → DefaultCosineThreshold.
	Threshold float64

	// Limit caps the returned leaf candidates — the MINIMUM floor of the
	// terminal net (#111 Finding A), passed through to ProposeChunks, which
	// returns top-Limit PLUS all leaves at or above ClearlyRelated. 0 →
	// DefaultChunkLimit; negative → unbounded.
	Limit int

	// Counter, when non-nil, tallies the cosine comparisons this descent
	// performs — the MEASURED recall_query_cosine_ops instrument (design
	// §7.2). Only the PRUNED levels (where child count > beam) and the
	// terminal leaf rank compute cosines; a lossless level (child count <=
	// beam) computes none. This is precisely why the count is measured at
	// the call site, not modeled. nil on the production hot path.
	Counter *CosineCounter

	// NetCapHits, when non-nil, is incremented once per NetCap truncation
	// that drops a clearly-related branch (beam) or leaf (terminal) the net
	// would otherwise have kept — the pathological-density signal the caller
	// logs (recall.net-cap-hit, #111 Finding A). Threaded through to both the
	// beam (topKByCosine) and the terminal ProposeChunks. nil on the
	// production hot path. See NetCap.
	NetCapHits *int
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
		frontier = topKByCosine(query, children, beam, opts.Counter, opts.NetCapHits)
	}

	// Frontier is now all leaves (bounded by the beam net + NetCap). Rank
	// them with the terminal ProposeChunks — the SAME cosine AND the SAME
	// relevance-net policy the flat scan uses (W1, #111 Finding A): both
	// paths return top-Limit PLUS all clearly-related leaves, so the
	// descent-vs-flat comparison is like-for-like.
	leaves := make([]ChunkVector, 0, len(frontier))
	for _, n := range frontier {
		leaves = append(leaves, n.Leaf)
	}
	return ProposeChunks(query, leaves, ChunkOptions{Threshold: opts.Threshold, Limit: opts.Limit, Counter: opts.Counter, NetCapHits: opts.NetCapHits})
}

// TreeLeafTurns returns the turn numbers of every leaf reachable in the
// tree rooted at root, in pre-order. It is a pure topology walk used by
// the W1 diagnostic (measure) to detect a tree-mismatch: a leaf the flat
// scan ranked that the tree does not actually contain (a staleness/build
// edge where the tree's leaf set drifted from snap.fine). A nil root
// yields nil. No I/O.
func TreeLeafTurns(root *SummaryNode) []int {
	if root == nil {
		return nil
	}
	var out []int
	var walk func(n *SummaryNode)
	walk = func(n *SummaryNode) {
		if n.isLeaf() {
			out = append(out, n.Leaf.TurnNumber)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
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

// topKByCosine is the per-level beam step with the relevance-sized net
// (#111 Finding A): it keeps the top-k branches by MAX-cosine exemplar
// score (the minimum floor, as before — max over the node's exemplar set,
// not a single centroid) PLUS every additional branch whose score is at or
// above ClearlyRelated (the cast net), bounded by NetCap. So a clearly-
// related branch is never pruned for losing a fixed slot to an equally-
// related sibling — the live W1 gap, where the branch holding the two
// highest-cosine leaves was cut at the BeamWidth boundary.
//
// Branches are stable-sorted (score desc, then leaf turn-number asc for a
// deterministic tie-break). When k >= len(nodes) all nodes survive (no
// pruning) and ZERO cosines are computed — the lossless short-circuit that
// keeps a short or wide level cheap and preserves the O(log n) measured
// bend. netCapHits (nil-safe) is bumped iff NetCap drops a clearly-related
// branch (the caller logs recall.net-cap-hit).
func topKByCosine(query []float64, nodes []*SummaryNode, k int, counter *CosineCounter, netCapHits *int) []*SummaryNode {
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
	ranked := make([]scored, len(nodes))
	for i, n := range nodes {
		// maxCosine bumps the counter by this node's exemplar count — the
		// ×N descent cost the measured metric must show.
		ranked[i] = scored{node: n, score: maxCosine(query, n, counter)}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].node.Leaf.TurnNumber < ranked[j].node.Leaf.TurnNumber
	})

	// The kept count is the larger of the floor k and the number of
	// clearly-related branches (the cast net), bounded by NetCap. Sorted
	// desc → the kept set is a prefix; count clearly-related from the front.
	clearlyRelated := 0
	for _, r := range ranked {
		if r.score >= ClearlyRelated {
			clearlyRelated++
		} else {
			break
		}
	}
	keep := k
	if clearlyRelated > keep {
		keep = clearlyRelated
	}
	if keep > NetCap {
		if clearlyRelated > NetCap {
			bumpNetCap(netCapHits) // dropping a clearly-related branch — pathological
		}
		keep = NetCap
	}
	out := make([]*SummaryNode, keep)
	for i := 0; i < keep; i++ {
		out[i] = ranked[i].node
	}
	return out
}
