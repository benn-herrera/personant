package measure

import (
	"context"
	"os"
	"testing"

	"personant/internal/recall/scoring"
)

// This file is the #111 Inc C internal test surface for the persisted .tree
// sidecar: it drives the real vecCache.WriteTree / LoadTrees file round-trip
// directly (reaching the private cache + scoring tree), proving (a) a built
// tree persists and reloads with identical topology + vectors + DescendChunks
// results (W1 preserved across persist/reload), (b) the childrenHash root key
// round-trips so treeUsable accepts the reloaded tree, and (c) a tree whose
// leaf is missing from the reloaded .vec tier is dropped (unreconstructable →
// flat fallback, never a wrong descent).

// fineFixture builds n axis-clustered leaves with sequential turn numbers and
// their synthetic chunk hashes — the leaf set + .vec staleness keys a tree is
// built and persisted against.
func fineFixture(dim, n int) (fine []scoring.ChunkVector, hashes []string) {
	for i := 0; i < n; i++ {
		fine = append(fine, scoring.ChunkVector{TurnNumber: i, Vector: axisVec(dim, i%dim, 0.01*float64(i))})
	}
	return fine, synthLeafHashes(fine)
}

// leafHashMap mirrors leafHashByTurn over a standalone fine/hashes pair (the
// snapshot-free fixture form the internal cache tests use).
func leafHashMap(fine []scoring.ChunkVector, hashes []string) map[int]string {
	out := make(map[int]string, len(fine))
	for i, cv := range fine {
		out[cv.TurnNumber] = hashes[i]
	}
	return out
}

// TestTreeCache_RoundTrip is the W1-across-persist gate: build a tree, persist
// it, reload it from disk, and assert the reloaded tree (a) reconstructs with
// the SAME root childrenHash (treeUsable accepts it), and (b) returns the SAME
// DescendChunks top-Kf as the original in-memory tree on every axis query —
// the recall-preservation property survives the persist/reload round trip.
func TestTreeCache_RoundTrip(t *testing.T) {
	const dim = 8
	fine, hashes := fineFixture(dim, 96)
	tree := scoring.BuildTree(fine, scoring.TreeBranchingFactor, scoring.ExemplarSummarizer)
	rootHash := composeLeafHash(hashes)

	cache := newVecCache(nil, nil, t.TempDir())
	if err := cache.WriteTree(context.Background(), "thr_1", tree, rootHash, leafHashMap(fine, hashes)); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}

	fineMap := map[string][]scoring.ChunkVector{"thr_1": fine}
	trees, loadedHashes := cache.LoadTrees(context.Background(), fineMap)
	reloaded := trees["thr_1"]
	if reloaded == nil {
		t.Fatal("LoadTrees did not return the persisted tree")
	}
	if loadedHashes["thr_1"] != rootHash {
		t.Fatalf("reloaded root hash %q != original %q", loadedHashes["thr_1"], rootHash)
	}

	// The reloaded tree must be usable against the same leaves (childrenHash
	// key round-tripped) and descend identically to the original.
	snap := emptySnapshot()
	snap.fine["thr_1"] = fine
	snap.fineHash["thr_1"] = hashes
	snap.tree["thr_1"] = reloaded
	snap.treeHash["thr_1"] = loadedHashes["thr_1"]
	if !snap.treeUsable("thr_1") {
		t.Fatal("reloaded tree not usable (childrenHash key did not round-trip)")
	}

	for axis := 0; axis < dim; axis++ {
		q := axisVec(dim, axis, 0.1)
		orig := scoring.DescendChunks(q, tree, scoring.DescendOptions{Limit: Kf})
		got := scoring.DescendChunks(q, reloaded, scoring.DescendOptions{Limit: Kf})
		if !sameTurns(orig, got) {
			t.Errorf("axis %d: reloaded descent %v != original %v", axis, turns(got), turns(orig))
		}
	}
}

// TestTreeCache_NilTree_RemovesSidecar: WriteTree with a nil tree removes a
// stale .tree (a thread that dropped below the build threshold or lost its
// tree leaves no orphan sidecar).
func TestTreeCache_NilTree_RemovesSidecar(t *testing.T) {
	const dim = 4
	fine, hashes := fineFixture(dim, 40)
	tree := scoring.BuildTree(fine, scoring.TreeBranchingFactor, scoring.ExemplarSummarizer)
	cache := newVecCache(nil, nil, t.TempDir())
	ctx := context.Background()

	if err := cache.WriteTree(ctx, "thr_1", tree, composeLeafHash(hashes), leafHashMap(fine, hashes)); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	if _, err := os.Stat(cache.treePath("thr_1")); err != nil {
		t.Fatalf("expected .tree on disk: %v", err)
	}
	if err := cache.WriteTree(ctx, "thr_1", nil, "", nil); err != nil {
		t.Fatalf("WriteTree nil: %v", err)
	}
	if _, err := os.Stat(cache.treePath("thr_1")); !os.IsNotExist(err) {
		t.Errorf("nil-tree WriteTree should remove the .tree; stat err = %v", err)
	}
}

// TestTreeCache_MissingLeaf_DropsTree: a persisted tree whose leaf turn number
// is absent from the reloaded fine tier (the .vec lost that chunk) is dropped
// — descent could not faithfully return that leaf, so the thread flat-falls-
// back rather than descend a tree with a hole.
func TestTreeCache_MissingLeaf_DropsTree(t *testing.T) {
	const dim = 4
	fine, hashes := fineFixture(dim, 40)
	tree := scoring.BuildTree(fine, scoring.TreeBranchingFactor, scoring.ExemplarSummarizer)
	cache := newVecCache(nil, nil, t.TempDir())
	if err := cache.WriteTree(context.Background(), "thr_1", tree, composeLeafHash(hashes), leafHashMap(fine, hashes)); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}

	// Reload against a fine tier missing the last leaf's vector.
	short := fine[:len(fine)-1]
	trees, _ := cache.LoadTrees(context.Background(), map[string][]scoring.ChunkVector{"thr_1": short})
	if trees["thr_1"] != nil {
		t.Error("tree with a leaf missing from the .vec tier should be dropped (unreconstructable)")
	}
}

// TestComposeNodeHash_Sensitivity guards the staleness key's core property:
// the composed hash changes when the leaf set changes (add / remove / reorder
// content) and is stable when it does not — the basis for trim/add
// invalidating exactly the affected subtrees.
func TestComposeNodeHash_Sensitivity(t *testing.T) {
	base := []string{"a", "b", "c"}
	same := composeNodeHash(base)
	if composeNodeHash([]string{"a", "b", "c"}) != same {
		t.Error("composeNodeHash not stable for identical input")
	}
	if composeNodeHash([]string{"a", "b"}) == same {
		t.Error("composeNodeHash unchanged after removing a child (staleness would miss a trim)")
	}
	if composeNodeHash([]string{"a", "b", "c", "d"}) == same {
		t.Error("composeNodeHash unchanged after adding a child (staleness would miss an add)")
	}
	// A length-ambiguous split must not collide ("ab"+"c" vs "a"+"bc").
	if composeNodeHash([]string{"ab", "c"}) == composeNodeHash([]string{"a", "bc"}) {
		t.Error("composeNodeHash collides on a length-ambiguous child split")
	}
}
