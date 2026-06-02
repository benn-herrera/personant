package measure

import (
	"context"

	"personant/internal/recall/scoring"
)

// Within-thread summary-tree sleep-cycle builder (design within-thread-
// summary-hierarchy.md §5 / §9.3, #111 Inc D). This is the OFFLINE half of
// the feature: the builder runs in the consolidation pass (the #108 sleep
// cycle), never on the turn loop (W6/G2). The turn loop only DESCENDS the
// already-built tree (intraChunks); it never summarizes. Trees are derived,
// in-memory, part of the immutable published snapshot (W5/G4) — persistence
// is Inc C, not here.

// RebuildTrees is the §5/§9.3 sleep-cycle entry: (re)build the per-thread
// summary trees from the current snapshot's settled fine tier and publish
// them via an atomic snapshot swap (the same I1 discipline the indexer uses).
// It is called from the sleep-cycle owner (the harness runSleepCycle, beside
// SweepCache / MemoryOps.Consolidate), AFTER the leaf-tier reconcile and the
// cache sweep so the trees summarize a settled leaf set (§5.3 ordering).
//
// It returns the number of trees (re)built this pass — the
// recall_intra_tree_rebuild_calls trip-wire metric (design §7.2, fork F-B):
// the sleep-cycle owner records it so unbounded semantic rebalancing churn
// on a multi-year thread is measurable and escalates to the F-B hybrid MAD.
//
// A no-op (0, nil) when no embedder is configured — nil embedder → no fine
// tier → no trees (I7/W8); the intra-pass flat-scans, which is still correct.
//
// Incremental/dirty-subtree reconcile (§5 full design) vs. what this builds:
// the full design assigns only NEW leaves to the nearest existing level-1
// cluster, splits overflowing clusters, and re-summarizes only the dirtied
// path — O(Δleaves/B) work per pass. v0.1 builds the "correct-but-simple"
// version the increment brief permits: it REBUILDS a thread's whole tree
// when its leaf count crossed the dirty threshold since the last build (or
// it has no tree yet), and reuses the prior tree verbatim otherwise. This is
// bounded (a rebuild only fires on a thread that actually grew past the
// threshold, not every pass for every thread) and the rebuild count is
// emitted so the churn is measured. The full incremental reconcile is the
// deferred §5 work; rebuilding-on-dirty is sound because the tree is derived
// (W5) — a rebuilt tree from the same leaves is equivalent (WA4), and the
// staleness guard (treeUsable) flat-falls-back for any thread whose tree this
// pass chose not to rebuild yet.
func (s *Service) RebuildTrees(ctx context.Context) (int, error) {
	if s.embedder == nil {
		return 0, nil
	}
	for {
		old := s.cur.Load()
		next, rebuilt := buildTreeSnapshot(old, s.treeSummarizer())
		if rebuilt == 0 {
			return 0, nil // nothing crossed the dirty threshold this pass
		}
		if s.cur.CompareAndSwap(old, next) {
			return rebuilt, nil
		}
		// Lost a race with the indexer's swap — rebuild against the fresh
		// snapshot. Bounded retry: the indexer publishes at most once per
		// in-flight job, and RebuildTrees runs on the idle sleep window.
	}
}

// treeSummarizer returns the summary-vector strategy this Service builds
// trees with — the §2.2 / fork-F-A seam. v0.1 always uses the centroid
// (scoring.CentroidSummarizer): the sim has no real LLM, so an LLM-summary
// descent key would be a meaningless mock embedding that breaks the W1
// recall-preservation gate the feature must pass; the centroid is
// deterministic, sim-validatable, and W1-sound under semantic clustering.
// This is the coordinator call that promotes F-A's "boring fallback" to the
// v0.1-primary for the validatable path. LLM-summary is the deferred
// production enrichment at THIS SAME seam: a Service built with an injected
// summarizer model would return an embed-the-LLM-summary strategy here, and
// the tree shape and descent would be unchanged.
func (s *Service) treeSummarizer() scoring.Summarizer {
	return scoring.CentroidSummarizer
}

// treeRebuildThreshold is the leaf-count delta that dirties a thread's tree
// for the next sleep pass (the v0.1 "correct-but-simple" incremental rule;
// the full §5 design assigns individual new leaves instead). A thread whose
// retained leaf count moved by at least this much since its tree was built
// (its tree's leaf count) is rebuilt; one that drifted less keeps its prior
// tree (the staleness guard treeUsable handles the small mismatch by flat-
// falling-back until the next qualifying pass). B is the natural unit — a
// drift of one branching factor's worth of leaves is when a level-1 cluster
// would have wanted to split/merge anyway (§3 rebalancing).
const treeRebuildThreshold = scoring.TreeBranchingFactor

// buildTreeSnapshot returns a new snapshot with rebuilt trees for the
// above-threshold threads whose leaf set drifted past treeRebuildThreshold
// (or that have no tree yet), reusing every other thread's prior tree
// verbatim, and the count of trees rebuilt. The input snapshot is not
// mutated (W5/I1 immutability); the coarse/fine/watermark tiers carry over
// by reference (immutable, shared safely).
//
// A thread below scoring.TreeBuildThreshold leaves gets NO tree (W8: the
// flat scan handles a short thread; a tree is pure overhead) — any prior
// tree for it is dropped so the staleness/threshold guard cannot pick it up.
func buildTreeSnapshot(old *indexSnapshot, summarize scoring.Summarizer) (*indexSnapshot, int) {
	newTree := make(map[string]*scoring.SummaryNode, len(old.fine))
	rebuilt := 0
	for threadID, leaves := range old.fine {
		if len(leaves) < scoring.TreeBuildThreshold {
			continue // short thread → no tree (W8)
		}
		prior := old.tree[threadID]
		if !treeDirty(prior, len(leaves)) {
			newTree[threadID] = prior // unchanged enough — reuse verbatim
			continue
		}
		newTree[threadID] = scoring.BuildTree(leaves, scoring.TreeBranchingFactor, summarize)
		rebuilt++
	}
	if rebuilt == 0 {
		return old, 0
	}
	return &indexSnapshot{
		coarse:    old.coarse,
		fine:      old.fine,
		watermark: old.watermark,
		tree:      newTree,
	}, rebuilt
}

// treeDirty reports whether a thread's tree must be rebuilt this pass: it has
// no tree yet, or its leaf count drifted by at least treeRebuildThreshold
// from the current fine-tier leaf count (the v0.1 dirty rule; the full §5
// design tracks per-leaf dirty marks instead). A tree whose leaf count is
// within the threshold of the current fine tier is reused — the small
// mismatch (if any) is handled by treeUsable's flat-fallback on the read
// path until a later pass crosses the threshold.
func treeDirty(prior *scoring.SummaryNode, leafCount int) bool {
	if prior == nil {
		return true
	}
	drift := treeLeafCount(prior) - leafCount
	if drift < 0 {
		drift = -drift
	}
	return drift >= treeRebuildThreshold
}
