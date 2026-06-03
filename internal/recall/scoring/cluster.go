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
// it returns up to NodeExemplars vectors that COVER THE CLUSTER'S SPREAD, so
// descent's max-cosine route keeps the branch alive for a query near ANY
// region of the cluster — not just its center. It is exported as the named
// v0.1 seam implementation so measure can wire it explicitly.
//
// The candidate POOL is the UNION of every child's full exemplar set
// (children[i].Vectors), NOT each child's primary .Vector (#111 Finding A
// residual W1 fix). A child's .Vector is its own medoid, so spreading over
// child primaries computes the spread over level-1 MEDOIDS — an extreme leaf
// under a central-medoid child gets averaged away at every level above it,
// and the one-level spread-coverage property does NOT compose up the tree.
// Pooling the full exemplar sets keeps every extreme leaf that became a
// lower-level exemplar a candidate to propagate to the root, so its branch
// stays alive at the beam. Every pooled candidate is itself a real leaf
// vector (leaves seed Vectors={leafVec} and this summarizer only ever
// returns members of its input pool), so the pool — and the output — is
// always real leaf vectors at every level; no synthesized/averaged vector
// ever enters (the centroid is used only to PICK the medoid, never stored).
//
// Method — deterministic medoid + farthest-point spread (a k-medoids-lite)
// over the pool:
//
//  1. The MEDOID — the pool member nearest the pool centroid (max cosine to
//     the mean) — anchors the set at the cluster's center. A REAL leaf
//     vector, not a meaningless average.
//  2. Then, greedily, the pool member FARTHEST from the already-chosen
//     exemplars (lowest max-cosine to the chosen set — the least-covered
//     member) is added, NodeExemplars-1 times. Farthest-point sampling
//     spreads the exemplars to the cluster's extremes, so a query matching
//     any in-cluster leaf routes through this node (the W1 property).
//
// If the pool has ≤ NodeExemplars members, the set is simply ALL of them —
// full coverage, no loss. Determinism (W4): every pooled candidate carries a
// stable key (subtree-min turn of its child, index within that child's
// Vectors, child index); ALL tie-breaks (medoid and farthest pick) use that
// key, so the same children always yield the same exemplar set → the same
// tree. Mismatched dims are handled by cosineSimilarity's defensive ragged
// behaviour. An empty group yields nil (no signal), which descent scores 0.
func ExemplarSummarizer(children []*SummaryNode) [][]float64 {
	if len(children) == 0 {
		return nil
	}

	pool := poolExemplars(children)
	if len(pool) == 0 {
		return nil
	}
	if len(pool) <= NodeExemplars {
		// Small pool: every candidate IS an exemplar — full coverage, no loss.
		out := make([][]float64, len(pool))
		for i, c := range pool {
			out[i] = c.vec
		}
		return out
	}

	center := centroidOfVecs(pool)
	chosen := make([]int, 0, NodeExemplars)
	taken := make([]bool, len(pool))

	// 1. Medoid: the pool member nearest the centroid (max cosine), stable-key
	//    tie-break.
	medoid := bestPooled(pool, func(i int) float64 {
		return cosineSimilarity(center, pool[i].vec)
	})
	chosen = append(chosen, medoid)
	taken[medoid] = true

	// 2. Farthest-point spread: repeatedly add the least-covered member — the
	//    one whose MAX cosine to the already-chosen exemplars is smallest
	//    (lower cosine = farther). We pick the member MINIMISING that coverage
	//    score, stable-key tie-break, so each addition reaches a new extreme.
	for len(chosen) < NodeExemplars {
		next := worstCoveredPooled(pool, chosen, taken)
		if next < 0 {
			break
		}
		chosen = append(chosen, next)
		taken[next] = true
	}

	out := make([][]float64, len(chosen))
	for i, idx := range chosen {
		out[i] = pool[idx].vec
	}
	return out
}

// pooledVec is one candidate in the exemplar pool: a real leaf vector plus
// the stable sort key (childTurn, vecIdx, childIdx) that makes every pick and
// tie-break deterministic (W4). The key uniquely orders pooled vectors even
// when two share a value, which subtreeMinTurn alone no longer can (a child
// contributes multiple pooled vectors).
type pooledVec struct {
	vec       []float64
	childTurn int // child's subtree-min turn number
	vecIdx    int // index within that child's Vectors
	childIdx  int // child's position in the children slice
}

// less reports whether p sorts before q under the stable key — the single
// tie-break ordering used everywhere in the spread (W4 determinism).
func (p pooledVec) less(q pooledVec) bool {
	if p.childTurn != q.childTurn {
		return p.childTurn < q.childTurn
	}
	if p.vecIdx != q.vecIdx {
		return p.vecIdx < q.vecIdx
	}
	return p.childIdx < q.childIdx
}

// poolExemplars flattens the children's full exemplar sets into one candidate
// pool (#111 Finding A residual fix). Each child contributes every vector in
// its Vectors (falling back to {Vector} for a node built single-key), each
// tagged with the stable sort key. Nil/empty vectors are skipped.
func poolExemplars(children []*SummaryNode) []pooledVec {
	pool := make([]pooledVec, 0, len(children)*NodeExemplars)
	for ci, c := range children {
		ct := subtreeMinTurn(c)
		for vi, v := range c.exemplars() {
			if len(v) == 0 {
				continue
			}
			pool = append(pool, pooledVec{vec: v, childTurn: ct, vecIdx: vi, childIdx: ci})
		}
	}
	return pool
}

// centroidOfVecs returns the element-wise mean of the pooled vectors — used
// only to seed the medoid pick (the centroid itself is never a descent key;
// #111 Finding A). Ragged dims are summed up to the shortest.
func centroidOfVecs(pool []pooledVec) []float64 {
	d := len(pool[0].vec)
	out := make([]float64, d)
	for _, p := range pool {
		for i := 0; i < d && i < len(p.vec); i++ {
			out[i] += p.vec[i]
		}
	}
	inv := 1.0 / float64(len(pool))
	for i := range out {
		out[i] *= inv
	}
	return out
}

// centroidOf returns the element-wise mean of the children's primary vectors.
// It is the node-level convenience over centroidOfVecs (DRY) — used by tests
// (centroidOnlySummarizer) to model the old single-centroid key. Ragged dims
// are summed up to the shortest.
func centroidOf(children []*SummaryNode) []float64 {
	pool := make([]pooledVec, len(children))
	for i, c := range children {
		pool[i] = pooledVec{vec: c.Vector}
	}
	return centroidOfVecs(pool)
}

// bestPooled returns the index of the pool member maximising score(i),
// breaking ties toward the lower stable key (W4 determinism).
func bestPooled(pool []pooledVec, score func(i int) float64) int {
	best := 0
	bestScore := score(0)
	for i := 1; i < len(pool); i++ {
		s := score(i)
		if s > bestScore || (s == bestScore && pool[i].less(pool[best])) {
			best, bestScore = i, s
		}
	}
	return best
}

// worstCoveredPooled returns the index of the unchosen pool member least
// covered by the chosen exemplar set — the one whose MAX cosine to any chosen
// exemplar is lowest (farthest from the spread so far), stable-key tie-break.
// -1 if none remain.
func worstCoveredPooled(pool []pooledVec, chosen []int, taken []bool) int {
	worst := -1
	var worstCoverage float64
	for i := range pool {
		if taken[i] {
			continue
		}
		coverage := maxCosineToChosenPooled(pool[i].vec, pool, chosen)
		if worst == -1 || coverage < worstCoverage ||
			(coverage == worstCoverage && pool[i].less(pool[worst])) {
			worst, worstCoverage = i, coverage
		}
	}
	return worst
}

// maxCosineToChosenPooled returns the best (max) cosine of v to any chosen
// exemplar's vector — the member's coverage by the current spread.
func maxCosineToChosenPooled(v []float64, pool []pooledVec, chosen []int) float64 {
	best := cosineSimilarity(v, pool[chosen[0]].vec)
	for _, idx := range chosen[1:] {
		if c := cosineSimilarity(v, pool[idx].vec); c > best {
			best = c
		}
	}
	return best
}
