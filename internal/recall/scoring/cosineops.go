package scoring

// CosineCounter is a caller-owned tally of the cosine comparisons a recall
// scan actually performs (design within-thread-summary-hierarchy.md §7.2:
// recall_query_cosine_ops MEASURED, not modeled). The pure scoring matchers
// (ProposeEmbedding, ProposeChunks, DescendChunks) bump it once per
// cosineSimilarity call they make when a non-nil counter is supplied via
// their options. It is deliberately the ONE place the otherwise-pure scoring
// functions touch caller state: no package-global counter (which would race
// and tie measurement to a single process), no return-signature change on the
// hot path (the count is irrelevant to production recall, which passes nil).
//
// The descent path's count is NOT derivable from its inputs — a level whose
// child count is <= the beam width is lossless and computes ZERO cosines
// (topKByCosine short-circuits), and the terminal leaf rank scans only the
// surviving frontier. So the honest count must be incremented at the actual
// call sites, which is exactly what threading this counter through does. A
// flat scan increments len(leaves); a descent increments only the pruned
// levels' children plus the terminal frontier — the empirical O(log n)-vs-O(n)
// contrast the §0 perf bend validates.
//
// Not safe for concurrent use; a counter is owned by one query's scan. The
// Service aggregates per-query totals into its own atomic run counter.
type CosineCounter struct {
	ops int
}

// add records n cosine comparisons. Nil-safe so the matchers can call it
// unconditionally without a per-call nil check at every site.
func (c *CosineCounter) add(n int) {
	if c == nil {
		return
	}
	c.ops += n
}

// Ops returns the cosine comparisons tallied so far.
func (c *CosineCounter) Ops() int {
	if c == nil {
		return 0
	}
	return c.ops
}
