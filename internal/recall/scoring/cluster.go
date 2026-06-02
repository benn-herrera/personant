package scoring

import "sort"

// Within-thread summary-tree BUILDER — the pure clustering math (design
// within-thread-summary-hierarchy.md §3, #111 Inc D). This file holds the
// substrate-free, LLM-free, deterministic clustering + tree assembly that
// turns a thread's flat leaf set (its fine-tier ChunkVectors) into the
// balanced summary tree DescendChunks walks. It knows nothing of threads,
// the FIFO, the cache, the sleep cycle, or how summary vectors are
// produced — the summary-key strategy is injected by the caller as a
// Summarizer (the §2.2 / fork-F-A seam; see ExemplarSummarizer for the v0.1
// exemplar-spread impl). Pure: no I/O.

// Summarizer produces an internal node's descent-key EXEMPLAR SET from its
// children's vectors — the §2.2 / fork-F-A summary-vector strategy seam.
//
// The key is a SET, not a single vector (#111 Finding A): a single centroid
// of a heterogeneous cluster sits in the dead middle of its spread, and a
// query near the cluster's EDGE cosine-misses it — descent prunes the
// branch and loses a relevant leaf even at a wide beam (the W1 4/251 gap on
// real nomic vectors). N exemplars span the spread; descent routes by the
// MAX cosine over the set, so an edge query keeps the branch alive.
//
// Design F-A specifies an LLM-summary-then-embed key for production
// (summarize the children's text → embed the summary, producing N summary
// exemplars). v0.1 uses a deterministic SPREAD over the children vectors
// instead (ExemplarSummarizer, in measure), a coordinator call that
// diverges from F-A's stated primary:
//
//   - The sim is the validation vehicle and has no real LLM, so an
//     LLM-summary descent key would be a meaningless mock embedding that
//     breaks W1 (recall-preservation) in the sim — the very gate this
//     feature must pass.
//   - The spread is deterministic (W4), sim-validatable, and W1-sound: it
//     covers the cluster with real child vectors, so a query matching any
//     in-cluster leaf has a near exemplar by construction.
//   - F-A records the non-LLM key as the "boring fallback"; here it is
//     promoted to the v0.1-primary for the validatable path, exactly as
//     the H2 live-embedding quality curve defers real-embedding quality.
//     LLM-summary is the deferred production enrichment at THIS SAME seam
//     — swap a different Summarizer (one that returns N summary embeddings),
//     the tree shape and descent are unchanged.
//
// The returned set's first element is taken as the node's primary exemplar
// (Vector). An empty return is treated as no descent key (the node routes
// by nothing → scores 0).
type Summarizer func(children []*SummaryNode) [][]float64

// BuildTree assembles a balanced summary tree over leaves by semantic
// agglomerative bottom-up clustering with a capped fan-in B (design §3):
// group the leaf vectors into ~B-sized clusters by cosine proximity,
// summarize each group via the injected Summarizer → level-1 internal
// nodes, then repeat over the level-1 node vectors → level-2, up to a
// single root. Depth ≈ log_B(n); fan-in ≤ B.
//
// Deterministic given the leaf set (W4): clusters are grown greedily from
// a turn-number-ordered seed list, and the nearest-neighbour pick breaks
// cosine ties by turn-number locality (the §3 recency tie-break — adjacent
// turns are often the same sub-discussion). The same leaves therefore
// always yield the same topology, which is what makes the tree
// rebuildable-from-leaves (W5) and the tests reproducible.
//
// b ≤ 0 falls back to TreeBranchingFactor. A nil summarizer falls back to
// the exemplar spread (so the pure layer is usable standalone in tests).
// Returns nil for an empty leaf set; a single leaf returns that leaf node
// (a degenerate one-node tree); below TreeBuildThreshold leaves the caller
// is expected to flat-scan (W8) — BuildTree still builds a correct tree if
// asked, it just is not worth it.
func BuildTree(leaves []ChunkVector, b int, summarize Summarizer) *SummaryNode {
	if len(leaves) == 0 {
		return nil
	}
	if b <= 1 {
		b = TreeBranchingFactor
	}
	if summarize == nil {
		summarize = ExemplarSummarizer
	}

	// Level 0: one leaf node per chunk vector. A leaf's exemplar set is its
	// own chunk vector (one element, no spread) — Vector and Vectors[0] both
	// mirror Leaf.Vector, the descent key and the rank key being the same
	// embedding at the leaf (the W1-cheap identity, design §2.1).
	level := make([]*SummaryNode, len(leaves))
	for i, lv := range leaves {
		level[i] = &SummaryNode{Vector: lv.Vector, Vectors: [][]float64{lv.Vector}, Leaf: lv}
	}

	// Agglomerate upward until a single root remains. Each pass clusters
	// the current level into ~B-sized semantic groups and summarizes each
	// group into its EXEMPLAR SET (the spread-covering descent key) — depth
	// grows by one per pass, ≈ log_B(n).
	for len(level) > 1 {
		groups := clusterByCosine(level, b)
		next := make([]*SummaryNode, len(groups))
		for i, g := range groups {
			next[i] = newInternalNode(g, summarize)
		}
		level = next
	}
	return level[0]
}

// newInternalNode builds an internal node over children g, computing its
// exemplar-set descent key via summarize and taking the first exemplar as
// the node's primary Vector (the single-key back-compat field). An empty
// exemplar set leaves Vector nil (the node routes by nothing → scores 0,
// which the beam prunes — never a panic).
func newInternalNode(g []*SummaryNode, summarize Summarizer) *SummaryNode {
	exemplars := summarize(g)
	var primary []float64
	if len(exemplars) > 0 {
		primary = exemplars[0]
	}
	return &SummaryNode{Vector: primary, Vectors: exemplars, Children: g}
}

// clusterByCosine partitions nodes into semantically-coherent groups of at
// most b by greedy agglomeration (design §3, the deterministic single-pass
// B-capped clusterer). It is the heart of W4: same input → same grouping.
//
// Nodes are seeded in turn-number order (each node's seed turn is its
// subtree's smallest leaf turn-number — a stable, content-independent key).
// Starting from the lowest unassigned seed, a group grows by repeatedly
// pulling in the nearest remaining node by cosine to the SEED node, ties
// broken toward the nearest turn-number (the §3 recency tie-break), until
// the group reaches b members or no node remains. Then the next-lowest
// unassigned seed starts the next group.
//
// Anchoring proximity to the fixed seed (not a moving centroid) keeps the
// pass deterministic and O(groups · b · remaining) without a re-clustering
// loop — a "correct-but-simple" balanced semantic grouping, the v0.1 call
// over a full k-means iteration (which buys tighter clusters at the cost
// of iteration-count nondeterminism and far more code; F-B is the flagged
// MAD for the at-depth refinement, not this pass).
func clusterByCosine(nodes []*SummaryNode, b int) [][]*SummaryNode {
	n := len(nodes)
	if n <= b {
		return [][]*SummaryNode{nodes}
	}

	// Stable seed order: ascending subtree-min turn number, then by the
	// node's position so equal turns never reorder nondeterministically.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	turnOf := make([]int, n)
	for i, nd := range nodes {
		turnOf[i] = subtreeMinTurn(nd)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return turnOf[order[i]] < turnOf[order[j]]
	})

	assigned := make([]bool, n)
	groups := make([][]*SummaryNode, 0, (n+b-1)/b)

	for _, seedIdx := range order {
		if assigned[seedIdx] {
			continue
		}
		assigned[seedIdx] = true
		group := make([]*SummaryNode, 0, b)
		group = append(group, nodes[seedIdx])
		seedVec := nodes[seedIdx].Vector
		seedTurn := turnOf[seedIdx]

		// Pull the nearest remaining nodes to the seed until the group is
		// full. Each pick scans the remaining set once (deterministic
		// argmax by cosine, then turn-number locality tie-break).
		for len(group) < b {
			best := -1
			var bestCos float64
			var bestTurnGap int
			for _, cand := range order {
				if assigned[cand] {
					continue
				}
				cos := cosineSimilarity(seedVec, nodes[cand].Vector)
				gap := turnOf[cand] - seedTurn
				if gap < 0 {
					gap = -gap
				}
				if best == -1 || cos > bestCos || (cos == bestCos && gap < bestTurnGap) {
					best, bestCos, bestTurnGap = cand, cos, gap
				}
			}
			if best == -1 {
				break // nothing left to pull
			}
			assigned[best] = true
			group = append(group, nodes[best])
		}
		groups = append(groups, group)
	}
	return groups
}

// subtreeMinTurn returns the smallest leaf turn-number under n — the stable
// temporal seed key for clustering (a leaf's own turn; an internal node's
// minimum descendant turn). Used only for the deterministic seed order and
// the recency tie-break, never for ranking.
func subtreeMinTurn(n *SummaryNode) int {
	if n.isLeaf() {
		return n.Leaf.TurnNumber
	}
	min := subtreeMinTurn(n.Children[0])
	for _, c := range n.Children[1:] {
		if t := subtreeMinTurn(c); t < min {
			min = t
		}
	}
	return min
}

// ExemplarSummarizer is the default summary-key strategy (#111 Finding A):
// it returns up to NodeExemplars CHILD vectors that COVER THE CLUSTER'S
// SPREAD, so descent's max-cosine route keeps the branch alive for a query
// near ANY region of the cluster — not just its center. It is exported as
// the named v0.1 seam implementation so measure can wire it explicitly.
//
// Method — deterministic medoid + farthest-point spread (a k-medoids-lite):
//
//  1. The MEDOID — the child whose vector is nearest the cluster centroid
//     (max cosine to the mean) — anchors the set at the cluster's center.
//     This is the single-centroid stand-in the old key was, but a REAL
//     child vector (so it is a faithful point, not a meaningless average).
//  2. Then, greedily, the child FARTHEST from the already-chosen exemplars
//     (the one whose best/max cosine to the chosen set is LOWEST — the
//     least-covered child) is added, NodeExemplars-1 times. Farthest-point
//     sampling spreads the exemplars to the cluster's extremes, so every
//     child has a near exemplar by construction → a query matching any
//     in-cluster leaf routes through this node (the W1 recall-preservation
//     property the spread buys).
//
// If the cluster has ≤ NodeExemplars children, the set is simply ALL the
// children's vectors — full coverage, no loss (exactly the centroid-free
// guarantee for a small cluster). Determinism (W4): ties in both the medoid
// pick and the farthest pick break by the child's subtree-min turn number
// (a stable, content-independent key), so the same children always yield
// the same exemplar set → the same tree. Mismatched child dimensions are
// handled by cosineSimilarity's defensive ragged behaviour. An empty group
// yields nil (no signal), which descent scores as 0.
func ExemplarSummarizer(children []*SummaryNode) [][]float64 {
	if len(children) == 0 {
		return nil
	}
	if len(children) <= NodeExemplars {
		// Small cluster: every child IS an exemplar — full coverage, no loss.
		out := make([][]float64, len(children))
		for i, c := range children {
			out[i] = c.Vector
		}
		return out
	}

	center := centroidOf(children)
	chosen := make([]int, 0, NodeExemplars)
	taken := make([]bool, len(children))

	// 1. Medoid: the child nearest the centroid (max cosine), turn-number
	//    tie-break.
	medoid := bestChild(children, func(i int) float64 {
		return cosineSimilarity(center, children[i].Vector)
	})
	chosen = append(chosen, medoid)
	taken[medoid] = true

	// 2. Farthest-point spread: repeatedly add the least-covered child — the
	//    one whose MAX cosine to the already-chosen exemplars is smallest
	//    (lower cosine = farther). We pick the child MINIMISING that coverage
	//    score, turn-number tie-break, so each addition reaches a new extreme.
	for len(chosen) < NodeExemplars {
		next := worstCoveredChild(children, chosen, taken)
		if next < 0 {
			break
		}
		chosen = append(chosen, next)
		taken[next] = true
	}

	out := make([][]float64, len(chosen))
	for i, idx := range chosen {
		out[i] = children[idx].Vector
	}
	return out
}

// centroidOf returns the element-wise mean of the children's primary
// vectors — used only to seed the medoid pick (the centroid itself is never
// a descent key; #111 Finding A). Ragged dims are summed up to the shortest.
func centroidOf(children []*SummaryNode) []float64 {
	d := len(children[0].Vector)
	out := make([]float64, d)
	for _, c := range children {
		for i := 0; i < d && i < len(c.Vector); i++ {
			out[i] += c.Vector[i]
		}
	}
	inv := 1.0 / float64(len(children))
	for i := range out {
		out[i] *= inv
	}
	return out
}

// bestChild returns the index of the child maximising score(i), breaking
// ties toward the lower subtree-min turn number (W4 determinism).
func bestChild(children []*SummaryNode, score func(i int) float64) int {
	best := 0
	bestScore := score(0)
	for i := 1; i < len(children); i++ {
		s := score(i)
		if s > bestScore || (s == bestScore && subtreeMinTurn(children[i]) < subtreeMinTurn(children[best])) {
			best, bestScore = i, s
		}
	}
	return best
}

// worstCoveredChild returns the index of the unchosen child least covered by
// the chosen exemplar set — the one whose MAX cosine to any chosen exemplar
// is lowest (farthest from the spread so far), turn-number tie-break. -1 if
// none remain.
func worstCoveredChild(children []*SummaryNode, chosen []int, taken []bool) int {
	worst := -1
	var worstCoverage float64
	for i := range children {
		if taken[i] {
			continue
		}
		coverage := maxCosineToChosen(children[i].Vector, children, chosen)
		if worst == -1 || coverage < worstCoverage ||
			(coverage == worstCoverage && subtreeMinTurn(children[i]) < subtreeMinTurn(children[worst])) {
			worst, worstCoverage = i, coverage
		}
	}
	return worst
}

// maxCosineToChosen returns the best (max) cosine of v to any chosen
// exemplar's vector — the child's coverage by the current spread.
func maxCosineToChosen(v []float64, children []*SummaryNode, chosen []int) float64 {
	best := cosineSimilarity(v, children[chosen[0]].Vector)
	for _, idx := range chosen[1:] {
		if c := cosineSimilarity(v, children[idx].Vector); c > best {
			best = c
		}
	}
	return best
}
