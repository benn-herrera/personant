package scoring

import "sort"

// Within-thread summary-tree BUILDER — the pure clustering math (design
// within-thread-summary-hierarchy.md §3, #111 Inc D). This file holds the
// substrate-free, LLM-free, deterministic clustering + tree assembly that
// turns a thread's flat leaf set (its fine-tier ChunkVectors) into the
// balanced summary tree DescendChunks walks. It knows nothing of threads,
// the FIFO, the cache, the sleep cycle, or how summary vectors are
// produced — the summary-vector strategy is injected by the caller as a
// Summarizer (the §2.2 / fork-F-A seam; see measure for the v0.1 centroid
// impl). Pure: no I/O.

// Summarizer produces an internal node's descent-key vector from its
// children's vectors — the §2.2 / fork-F-A summary-vector strategy seam.
//
// Design F-A specifies an LLM-summary-then-embed key for production
// (summarize the children's text → embed the summary). v0.1 uses the
// CENTROID of the children vectors instead (CentroidSummarizer, in
// measure), a coordinator call that diverges from F-A's stated primary:
//
//   - The sim is the validation vehicle and has no real LLM, so an
//     LLM-summary descent key would be a meaningless mock embedding that
//     breaks W1 (recall-preservation) in the sim — the very gate this
//     feature must pass.
//   - The centroid is deterministic (W4), sim-validatable, and W1-sound
//     under semantic clustering: over a COHERENT cluster the centroid is a
//     faithful direction, and Inc A's W1 descent test already passes with
//     centroid keys over coherent clusters.
//   - F-A itself records the centroid as the "boring fallback"; here it is
//     promoted to the v0.1-primary for the validatable path, exactly as
//     the H2 live-embedding quality curve defers real-embedding quality.
//     LLM-summary is the deferred production enrichment at THIS SAME seam
//     — swap a different Summarizer, the tree shape and descent are
//     unchanged.
type Summarizer func(children []*SummaryNode) []float64

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
// the centroid (so the pure layer is usable standalone in tests). Returns
// nil for an empty leaf set; a single leaf returns that leaf node (a
// degenerate one-node tree); below TreeBuildThreshold leaves the caller is
// expected to flat-scan (W8) — BuildTree still builds a correct tree if
// asked, it just is not worth it.
func BuildTree(leaves []ChunkVector, b int, summarize Summarizer) *SummaryNode {
	if len(leaves) == 0 {
		return nil
	}
	if b <= 1 {
		b = TreeBranchingFactor
	}
	if summarize == nil {
		summarize = CentroidSummarizer
	}

	// Level 0: one leaf node per chunk vector. Leaf.Vector mirrors the
	// chunk vector — the descent key and the rank key are the same
	// embedding at the leaf (the W1-cheap identity, design §2.1).
	level := make([]*SummaryNode, len(leaves))
	for i, lv := range leaves {
		level[i] = &SummaryNode{Vector: lv.Vector, Leaf: lv}
	}

	// Agglomerate upward until a single root remains. Each pass clusters
	// the current level into ~B-sized semantic groups and summarizes each
	// group into one parent — depth grows by one per pass, ≈ log_B(n).
	for len(level) > 1 {
		groups := clusterByCosine(level, b)
		next := make([]*SummaryNode, len(groups))
		for i, g := range groups {
			next[i] = &SummaryNode{Vector: summarize(g), Children: g}
		}
		level = next
	}
	return level[0]
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

// CentroidSummarizer is the default summary-vector strategy: the mean of
// the children's descent-key vectors (design §2.2 / F-A fallback promoted
// to v0.1-primary — see Summarizer). It is exported as the named v0.1 seam
// implementation so measure can wire it explicitly. Mismatched child
// dimensions are summed component-wise up to the shortest, the same
// defensive behaviour cosineSimilarity has for ragged input. An empty group
// yields nil (no signal), which cosineSimilarity scores as 0.
func CentroidSummarizer(children []*SummaryNode) []float64 {
	if len(children) == 0 {
		return nil
	}
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
