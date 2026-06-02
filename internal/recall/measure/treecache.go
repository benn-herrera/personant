package measure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"personant/internal/recall/scoring"
)

// The #111 Inc C persisted summary-tree sidecar. It lives beside the .vec in
// .recall-cache/threads/ and is the SAME kind of derived, gitignored,
// staleness-keyed, per-thread artifact (design within-thread-summary-
// hierarchy.md §6): the within-thread summary tree, persisted so it survives
// across sessions like the Inc-3 vector cache and need not be re-clustered /
// re-summarized at every startup.
//
// Layout (under the gitignored, never-canonical .recall-cache/ — I5/W5):
//
//	.recall-cache/
//	  threads/thr_<id>.vec    Inc 3: coarse + chunks + chunk_hashes + watermark
//	  threads/thr_<id>.tree   Inc C: this file — topology + node vectors + leaf
//	                          turn-numbers + per-node childrenHash + root hash
//
// Format is JSON v0.1, the same D-B reasoning as .vec: derived, not human-
// edited, correctness-first. Leaf VECTORS are NOT stored here — they live in
// the .vec, the single source of truth (DRY); a leaf node records only its
// turn number and chunk_hash, and its vector is rehydrated from the loaded
// fine tier by turn number on LoadTrees. Deleting .recall-cache/ (incl every
// .tree) costs only CPU at the next sleep cycle, never data (W5).

// treeFileExt is the per-thread summary-tree sidecar extension.
const treeFileExt = ".tree"

func (c *vecCache) treePath(threadID string) string {
	return filepath.Join(c.threadsDir(), threadID+treeFileExt)
}

// treeFile is one thread's persisted summary tree (#111 Inc C §6). RootHash
// is the composed childrenHash the tree was built against — the staleness key
// the read path compares against the current leaves (treeUsable). Root is the
// recursive topology.
type treeFile struct {
	ThreadID string    `json:"thread_id"`
	RootHash string    `json:"root_hash"`
	Root     *treeNode `json:"root"`
}

// treeNode is one persisted summary node. An internal node carries its
// summary Vector (the descent key, derived — not in the .vec) and its
// Children; a leaf carries TurnNumber + ChunkHash and no Children/Vector (its
// vector is rehydrated from the .vec-loaded fine tier by turn number, DRY).
// ChildrenHash is the node's composed childrenHash (§6): a leaf's is its
// chunk_hash; an internal node's is the SHA-256 of its ordered children's
// childrenHashes — composed up the topology so a leaf change re-hashes
// exactly its ancestor path (the dirtied set), enabling subtree-level
// reconcile later. The read-path staleness check uses only RootHash.
type treeNode struct {
	ChildrenHash string      `json:"children_hash"`
	Vector       []float64   `json:"vector,omitempty"`
	TurnNumber   int         `json:"turn_number,omitempty"`
	ChunkHash    string      `json:"chunk_hash,omitempty"`
	Children     []*treeNode `json:"children,omitempty"`
}

// WriteTree persists threadID's summary tree to its .tree sidecar (§6 write-
// through). A nil tree removes any stale sidecar (the thread dropped below the
// build threshold, or lost its tree). leafHashByTurn supplies each leaf's
// chunk_hash so node childrenHashes compose from the existing .vec key (DRY).
func (c *vecCache) WriteTree(ctx context.Context, threadID string, tree *scoring.SummaryNode, rootHash string, leafHashByTurn map[int]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tree == nil {
		if err := os.Remove(c.treePath(threadID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove tree %s: %w", threadID, err)
		}
		return nil
	}
	if err := os.MkdirAll(c.threadsDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir cache threads: %w", err)
	}
	tf := treeFile{
		ThreadID: threadID,
		RootHash: rootHash,
		Root:     serializeNode(tree, leafHashByTurn),
	}
	b, err := json.Marshal(tf)
	if err != nil {
		return fmt.Errorf("marshal tree %s: %w", threadID, err)
	}
	if err := writeFileAtomic(c.treePath(threadID), b); err != nil {
		return fmt.Errorf("write tree %s: %w", threadID, err)
	}
	return nil
}

// serializeNode recursively converts a scoring tree into the persisted form,
// composing each node's childrenHash from the existing leaf chunk hashes
// (DRY, §6). Leaf nodes drop their vector (rehydrated from the .vec on load);
// internal nodes keep their summary vector (the derived descent key, not in
// the .vec).
func serializeNode(n *scoring.SummaryNode, leafHashByTurn map[int]string) *treeNode {
	if len(n.Children) == 0 {
		h := leafHashByTurn[n.Leaf.TurnNumber]
		return &treeNode{ChildrenHash: h, TurnNumber: n.Leaf.TurnNumber, ChunkHash: h}
	}
	children := make([]*treeNode, len(n.Children))
	childHashes := make([]string, len(n.Children))
	for i, c := range n.Children {
		children[i] = serializeNode(c, leafHashByTurn)
		childHashes[i] = children[i].ChildrenHash
	}
	return &treeNode{
		ChildrenHash: composeNodeHash(childHashes),
		Vector:       n.Vector,
		Children:     children,
	}
}

// LoadTrees loads every present .tree sidecar for the threads in the fine
// tier, rehydrating each into a *scoring.SummaryNode (leaf vectors looked up
// from fine by turn number) plus its stored root childrenHash (§6). It loads
// what is on disk verbatim; the staleness decision is the read path's
// (treeUsable composes the CURRENT leaf hash and compares it to the returned
// root hash), so a tree whose leaves moved since persistence simply fails the
// match later and flat-falls-back (W8) — LoadTrees never has to re-hash.
//
// A tree is OMITTED (not loaded) only when it is structurally
// unreconstructable against the current leaves — a leaf turn number with no
// vector in the loaded fine tier — since descent could not return that leaf;
// the thread then flat-falls-back until the next sleep cycle rebuilds it.
// Missing or malformed sidecars are skipped, never fatal (the tree is
// derived, rebuildable — W5).
func (c *vecCache) LoadTrees(ctx context.Context, fine map[string][]scoring.ChunkVector) (map[string]*scoring.SummaryNode, map[string]string) {
	trees := make(map[string]*scoring.SummaryNode, len(fine))
	hashes := make(map[string]string, len(fine))
	for threadID := range fine {
		if ctx.Err() != nil {
			break
		}
		tf, err := c.loadTreeFile(threadID)
		if err != nil || tf == nil || tf.Root == nil {
			continue // absent / malformed → no tree, flat fallback (W8)
		}
		byTurn := make(map[int][]float64, len(fine[threadID]))
		for _, cv := range fine[threadID] {
			byTurn[cv.TurnNumber] = cv.Vector
		}
		root, ok := rehydrateNode(tf.Root, byTurn)
		if !ok {
			continue // a leaf vector is missing from the .vec → unusable tree
		}
		trees[threadID] = root
		hashes[threadID] = tf.RootHash
	}
	return trees, hashes
}

// loadTreeFile reads one .tree. A missing file is (nil, nil) — the no-tree
// state. A malformed file is an error the caller treats as "no tree" (skip,
// never fatal — the tree is rebuildable).
func (c *vecCache) loadTreeFile(threadID string) (*treeFile, error) {
	b, err := os.ReadFile(c.treePath(threadID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tree %s: %w", threadID, err)
	}
	var tf treeFile
	if err := json.Unmarshal(b, &tf); err != nil {
		return nil, fmt.Errorf("parse tree %s: %w", threadID, err)
	}
	return &tf, nil
}

// rehydrateNode rebuilds a scoring node from its persisted form, looking up
// each leaf's vector from the .vec-loaded fine tier by turn number (DRY: leaf
// vectors are never duplicated into the .tree). Returns (node, false) if any
// leaf's vector is absent from byTurn — an unreconstructable tree the caller
// drops, since descent could not faithfully return that leaf.
func rehydrateNode(tn *treeNode, byTurn map[int][]float64) (*scoring.SummaryNode, bool) {
	if len(tn.Children) == 0 {
		vec, ok := byTurn[tn.TurnNumber]
		if !ok {
			return nil, false
		}
		// Leaf.Vector mirrors the chunk vector — the descent key and the rank
		// key are the same embedding at the leaf (the W1-cheap identity).
		return &scoring.SummaryNode{
			Vector: vec,
			Leaf:   scoring.ChunkVector{TurnNumber: tn.TurnNumber, Vector: vec},
		}, true
	}
	children := make([]*scoring.SummaryNode, len(tn.Children))
	for i, c := range tn.Children {
		child, ok := rehydrateNode(c, byTurn)
		if !ok {
			return nil, false
		}
		children[i] = child
	}
	return &scoring.SummaryNode{Vector: tn.Vector, Children: children}, true
}
