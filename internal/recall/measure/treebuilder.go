package measure

import (
	"context"
	"fmt"

	"personant/internal/memops"
	"personant/internal/recall/scoring"
)

// Within-thread summary-tree sleep-cycle builder (design within-thread-
// summary-hierarchy.md §5 / §9.3, #111 Inc D). This is the OFFLINE half of
// the feature: the builder runs in the consolidation pass (the #108 sleep
// cycle), never on the turn loop (W6/G2). The turn loop only DESCENDS the
// already-built tree (intraChunks); it never summarizes. Trees are derived,
// part of the immutable published snapshot (W5/G4); a rebuilt tree is
// write-through-persisted to its .tree sidecar here (#111 Inc C, treecache.go)
// so it survives across sessions, and the childrenHash staleness key decides
// what is rebuilt.

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
		next, rebuiltIDs := buildTreeSnapshot(old, s.treeSummarizer())
		if len(rebuiltIDs) == 0 {
			return 0, nil // nothing stale this pass
		}
		if !s.cur.CompareAndSwap(old, next) {
			// Lost a race with the indexer's swap — rebuild against the fresh
			// snapshot. Bounded retry: the indexer publishes at most once per
			// in-flight job, and RebuildTrees runs on the idle sleep window.
			continue
		}
		// Write-through the .tree sidecars for the threads rebuilt this pass
		// (Inc C §6): a tree whose childrenHash matches its leaves loads
		// usable on the next startup with no rebuild. Best-effort and
		// non-fatal (W8/§5.3): a write failure leaves the in-memory tree
		// published — descent still works this session, the next sleep pass
		// re-persists. The cache owns the .tree layout; nil cache (no dir) is
		// a no-op.
		if s.cache != nil {
			for _, id := range rebuiltIDs {
				if err := s.cache.WriteTree(ctx, id, next.tree[id], next.treeHash[id], leafHashByTurn(next, id)); err != nil {
					_ = s.ops.Log(ctx, memops.LogCategoryRecall, "tree-error",
						fmt.Sprintf("persist tree %s: %v", id, err))
				}
			}
		}
		return len(rebuiltIDs), nil
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

// leafHashByTurn maps a thread's leaf turn numbers to their chunk_hash from
// the snapshot's parallel fine / fineHash slices — the per-leaf childrenHash
// bottom-out the .tree writer composes node hashes from (DRY: the chunk_hash
// the .vec already stores). A turn with no recorded hash maps to "" (a fresh
// tier the cache had not yet hashed); composeNodeHash still produces a stable
// key from it.
func leafHashByTurn(snap *indexSnapshot, threadID string) map[int]string {
	fine := snap.fine[threadID]
	hashes := snap.fineHash[threadID]
	out := make(map[int]string, len(fine))
	for i, cv := range fine {
		h := ""
		if i < len(hashes) {
			h = hashes[i]
		}
		out[cv.TurnNumber] = h
	}
	return out
}

// buildTreeSnapshot returns a new snapshot with rebuilt trees for the
// above-threshold threads whose tree is stale or absent, reusing every other
// thread's prior tree verbatim, and the IDs of the threads rebuilt this pass.
// The input snapshot is not mutated (W5/I1 immutability); the coarse / fine /
// fineHash / watermark tiers carry over by reference (immutable, shared
// safely).
//
// Staleness is the #111 Inc C childrenHash key (treeDirty): a tree whose
// stored treeHash no longer matches the composed hash of the thread's current
// leaf chunk hashes is rebuilt; one whose hash still matches is reused
// verbatim (zero work — the bounded-churn property: an unchanged thread is
// never rebuilt). A rebuilt tree is re-stamped with the composed hash of the
// leaf set it was built against, so the next read sees it usable and the next
// pass sees it clean.
//
// A thread below scoring.TreeBuildThreshold leaves gets NO tree (W8: the flat
// scan handles a short thread; a tree is pure overhead) — any prior tree for
// it is dropped so the staleness guard cannot pick it up.
func buildTreeSnapshot(old *indexSnapshot, summarize scoring.Summarizer) (*indexSnapshot, []string) {
	newTree := make(map[string]*scoring.SummaryNode, len(old.fine))
	newTreeHash := make(map[string]string, len(old.fine))
	var rebuiltIDs []string
	for threadID, leaves := range old.fine {
		if len(leaves) < scoring.TreeBuildThreshold {
			continue // short thread → no tree (W8)
		}
		leafHash := composeLeafHash(old.fineHash[threadID])
		if old.tree[threadID] != nil && old.treeHash[threadID] == leafHash {
			newTree[threadID] = old.tree[threadID] // clean — reuse verbatim
			newTreeHash[threadID] = old.treeHash[threadID]
			continue
		}
		newTree[threadID] = scoring.BuildTree(leaves, scoring.TreeBranchingFactor, summarize)
		newTreeHash[threadID] = leafHash
		rebuiltIDs = append(rebuiltIDs, threadID)
	}
	if len(rebuiltIDs) == 0 {
		return old, nil
	}
	return &indexSnapshot{
		coarse:    old.coarse,
		fine:      old.fine,
		fineHash:  old.fineHash,
		watermark: old.watermark,
		tree:      newTree,
		treeHash:  newTreeHash,
	}, rebuiltIDs
}
