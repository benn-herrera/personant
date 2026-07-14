package measure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/scoring"
)

// The §5 persisted derived-vector cache. It lives in package measure (not a
// sub-package) so it can produce the unexported indexSnapshot directly
// without exporting the snapshot type or its scoring-vector plumbing across
// a package boundary — keeping the index representation private to measure.
//
// Layout (design §5.1), under $PERSONANT_HOME/.recall-cache/ (gitignored,
// operational, never canonical — I5):
//
//	.recall-cache/
//	  threads/thr_<id>.vec   one file per thread (vecFile: coarse + chunks + hashes + watermark)
//	  manifest.json          [derived] roll-up: thread_id → {watermark, body_hash, chunk_count}
//
// Format is JSON v0.1 (D-B / hazard H4): vectors are []float64, not human-
// edited; JSON is the simpler correctness-first choice. A length-prefixed
// binary format is a measured-later optimization, deliberately NOT built.
//
// The cache reads canonical only through the MemoryOps port (ListThreads /
// LoadThread / LoadThreadExcerpts); it reads/writes its own files via plain
// os file I/O (operational state, like SaveWorkingSet) and never parses or
// writes git. Deleting .recall-cache/ costs only CPU at the next startup.

const (
	// vecCacheThreadsSubdir holds the per-thread .vec files.
	vecCacheThreadsSubdir = "threads"
	// vecCacheManifestName is the staleness-key roll-up read once at startup.
	vecCacheManifestName = "manifest.json"
	// vecFileExt is the per-thread vector-file extension.
	vecFileExt = ".vec"
)

// vecCache is the persisted-cache implementation of the vectorCache seam.
type vecCache struct {
	ops      memops.MemoryOps // canonical reads only (no git, no paths beyond dir)
	embedder model.Embedder   // partial-miss re-embeds during Reconcile
	dir      string           // $PERSONANT_HOME/.recall-cache

	// manifestMu serializes the manifest.json read-modify-write cycle. Write
	// runs on the indexer goroutine (processJob write-through) while Sweep runs
	// on the sleep-cycle owner goroutine (SweepCache); both do
	// loadManifest → mutate → saveManifest, so without this lock a Sweep
	// interleaving a Write silently loses whichever side's entry landed first —
	// a lost .vec cached vector masquerading as a surprise re-embed next
	// startup. The lock spans the whole RMW (not just saveManifest), so the
	// load and the store are one atomic section. Reconcile takes it too (it
	// calls Write) but runs before the indexer goroutine starts, so it is
	// uncontended there.
	manifestMu sync.Mutex
}

// newVecCache constructs the cache rooted at dir (the adapter's cache
// directory). It does no I/O — directories are created lazily on first
// Write, so a read-only run that never indexes leaves no cache dir.
func newVecCache(ops memops.MemoryOps, embedder model.Embedder, dir string) *vecCache {
	return &vecCache{ops: ops, embedder: embedder, dir: dir}
}

// vecFile is one thread's persisted vectors plus the content-hash staleness
// keys (§5.2). Field tags are explicit so the on-disk JSON stays stable if
// the Go field names ever change.
type vecFile struct {
	ThreadID  string         `json:"thread_id"`
	Watermark int            `json:"dispatch_watermark"`
	BodyHash  string         `json:"body_hash"`
	Coarse    []float64      `json:"coarse_vector"`
	Chunks    []vecFileChunk `json:"chunks"`
}

// vecFileChunk is one fine-tier chunk: its turn number, its content-hash
// staleness key, and its vector. chunk_hash is the hash of the turn-excerpt
// bytes; a turn-excerpt is immutable once written (Inc 4 retains, never
// rewrites), so chunk_hash is stable for the chunk's life (§5.3).
type vecFileChunk struct {
	TurnNumber int       `json:"turn_number"`
	ChunkHash  string    `json:"chunk_hash"`
	Vector     []float64 `json:"vector"`
}

// manifestEntry is the per-thread roll-up the startup reconcile reads
// instead of opening every .vec (§5.2).
type manifestEntry struct {
	Watermark  int    `json:"dispatch_watermark"`
	BodyHash   string `json:"body_hash"`
	ChunkCount int    `json:"chunk_count"`
}

// manifest is the [derived] thread_id → entry roll-up.
type manifest struct {
	Threads map[string]manifestEntry `json:"threads"`
}

// contentHash is the staleness key: a hex SHA-256 of the exact text fed to
// the embedder. Shared by the cache (keying its writes / comparing on
// reconcile) and processJob (computing keys at the embed point) so both
// sides agree by construction (DRY: one hash definition).
func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (c *vecCache) threadsDir() string   { return filepath.Join(c.dir, vecCacheThreadsSubdir) }
func (c *vecCache) manifestPath() string { return filepath.Join(c.dir, vecCacheManifestName) }
func (c *vecCache) vecPath(threadID string) string {
	return filepath.Join(c.threadsDir(), threadID+vecFileExt)
}

// loadManifest reads manifest.json. A missing file is the fresh-cache state:
// an empty manifest, no error (mirrors LoadWorkingSet's missing-artifact
// semantics). A present-but-malformed file is an error — corruption should
// be visible, not silently treated as a cold start.
func (c *vecCache) loadManifest() (manifest, error) {
	b, err := os.ReadFile(c.manifestPath())
	if err != nil {
		if os.IsNotExist(err) {
			return manifest{Threads: map[string]manifestEntry{}}, nil
		}
		return manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Threads == nil {
		m.Threads = map[string]manifestEntry{}
	}
	return m, nil
}

// saveManifest writes the manifest, creating the cache dir if needed.
func (c *vecCache) saveManifest(m manifest) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir cache: %w", err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeFileAtomic(c.manifestPath(), b); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// loadVecFile reads one thread's .vec file. A missing file returns
// (nil, nil): the manifest claimed an entry but the .vec is gone — treat it
// as a full miss, not an error (the data is rebuildable, I5).
func (c *vecCache) loadVecFile(threadID string) (*vecFile, error) {
	b, err := os.ReadFile(c.vecPath(threadID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read vec %s: %w", threadID, err)
	}
	var vf vecFile
	if err := json.Unmarshal(b, &vf); err != nil {
		return nil, fmt.Errorf("parse vec %s: %w", threadID, err)
	}
	return &vf, nil
}

// Reconcile is the §5.3 O(changed) startup. It builds the whole initial
// snapshot synchronously so Prepare returns with a warm index (no cold
// window where a Recall right after Prepare sees an empty coarse tier). For
// each live spine thread it content-hashes the current canonical body +
// excerpts against the cached .vec and classifies:
//
//   - HIT  (body_hash + every chunk_hash match, chunk count unchanged): load
//     the .vec verbatim into the snapshot. Zero embed calls.
//   - PARTIAL (some chunks new/changed/removed, or body moved): re-embed only
//     the changed/added chunk texts and the coarse body if its hash moved,
//     reuse the cached vectors for unchanged chunks, persist the refreshed
//     .vec + manifest entry. Embed cost == changed-chunk count (+coarse),
//     not total-chunk count (AC4).
//   - FULL MISS (no manifest entry / no .vec): embed from scratch and persist.
//
// On a warm cache (the steady state) the changed set is small, so the pass
// is O(changed) embed calls regardless of how many threads or chunks exist.
func (c *vecCache) Reconcile(ctx context.Context) (*indexSnapshot, error) {
	recs, err := c.ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	m, err := c.loadManifest()
	if err != nil {
		return nil, err
	}

	snap := emptySnapshot()
	for _, rec := range recs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vf, err := c.loadVecFile(rec.ID)
		if err != nil {
			return nil, err
		}
		_, cached := m.Threads[rec.ID]
		if !cached || vf == nil {
			vf = nil // full miss: reconcileThread re-embeds everything
		}

		entry, refreshed, err := c.reconcileThread(ctx, rec, vf)
		if err != nil {
			return nil, err
		}
		snap = snap.with(rec.ID, entry.coarse, entry.chunks, entry.chunkHashes, entry.watermark)
		if refreshed {
			// PARTIAL / FULL miss: persist the .vec + manifest so the next
			// startup is a HIT. A pure HIT (refreshed == false) wrote nothing.
			if err := c.Write(ctx, entry); err != nil {
				return nil, err
			}
		}
	}
	return snap, nil
}

// reconcileThread classifies one cached thread against current canonical
// content and returns its cacheEntry (vectors + freshly computed hashes)
// plus whether a re-embed happened (any miss → caller persists). A pure HIT
// reuses every cached vector and re-embeds nothing. A nil vf is a full miss:
// nothing is reused and the whole thread (coarse + every chunk) is embedded.
func (c *vecCache) reconcileThread(ctx context.Context, rec memops.SpineRecord, vf *vecFile) (cacheEntry, bool, error) {
	excerpts, err := c.ops.LoadThreadExcerpts(ctx, rec.ID)
	if err != nil {
		return cacheEntry{}, false, fmt.Errorf("excerpts %s: %w", rec.ID, err)
	}
	thr, err := c.ops.LoadThread(ctx, rec.ID)
	if err != nil {
		return cacheEntry{}, false, fmt.Errorf("load %s: %w", rec.ID, err)
	}

	// A full miss (nil vf) reuses nothing: an empty reuse lookup forces every
	// chunk and the coarse body through the embedder below. The reuse lookup is
	// content hash → cached vector, the same key policy the incremental flush
	// uses (see chunkReusePartition).
	reuse := map[string][]float64{}
	cachedBodyHash := ""
	var cachedCoarse []float64
	cachedCount := -1 // sentinel: differs from any real count → full re-embed
	if vf != nil {
		reuse = make(map[string][]float64, len(vf.Chunks))
		for _, ch := range vf.Chunks {
			reuse[ch.ChunkHash] = ch.Vector
		}
		cachedBodyHash = vf.BodyHash
		cachedCoarse = vf.Coarse
		cachedCount = len(vf.Chunks)
	}

	// Chunk tier: reuse cached vectors for unchanged excerpts (hash match),
	// re-embed only the changed/added ones — the AC4 "only that chunk" path
	// (chunkReusePartition, the shared shape with the incremental flush).
	chunks, chunkHashes, toEmbedTexts, toEmbedSlot := chunkReusePartition(excerpts, reuse)

	// A removed/added excerpt (or full miss) moves the thread, as does any
	// re-embedded chunk (toEmbedTexts non-empty) — either forces a persist.
	refreshed := cachedCount != len(excerpts) || len(toEmbedTexts) > 0

	// Coarse tier: re-embed the body only if its hash moved. Prepend it so the
	// coarse slot is index 0 of the embed batch when present.
	bodyText := truncateForEmbed(thr.Body)
	bodyHash := contentHash(bodyText)
	var coarseVec []float64
	if vf != nil && bodyHash == cachedBodyHash {
		coarseVec = cachedCoarse
	} else {
		refreshed = true
		toEmbedTexts = append([]string{bodyText}, toEmbedTexts...)
		toEmbedSlot = append([]int{-1}, toEmbedSlot...)
	}

	if len(toEmbedTexts) > 0 {
		vecs, err := c.embedder.Embed(ctx, toEmbedTexts)
		if err != nil {
			return cacheEntry{}, false, fmt.Errorf("reembed %s: %w", rec.ID, err)
		}
		if len(vecs) != len(toEmbedTexts) {
			return cacheEntry{}, false, fmt.Errorf("reembed %s: %d vectors for %d texts", rec.ID, len(vecs), len(toEmbedTexts))
		}
		for j, slot := range toEmbedSlot {
			if slot == -1 {
				coarseVec = vecs[j]
				continue
			}
			chunks[slot].Vector = vecs[j]
		}
	}

	return cacheEntry{
		threadID:    rec.ID,
		coarse:      scoring.ThreadVector{ThreadID: rec.ID, Vector: coarseVec},
		chunks:      chunks,
		watermark:   rec.TurnCount,
		bodyHash:    bodyHash,
		chunkHashes: chunkHashes,
	}, refreshed, nil
}

// Write persists one thread's freshly embedded vectors and updates the
// manifest — the write-through called at the indexer swap point (§6.3) and
// on the Reconcile partial-miss refresh path.
func (c *vecCache) Write(ctx context.Context, e cacheEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(e.chunkHashes) != len(e.chunks) {
		return fmt.Errorf("cache write %s: %d chunk hashes for %d chunks", e.threadID, len(e.chunkHashes), len(e.chunks))
	}
	if err := os.MkdirAll(c.threadsDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir cache threads: %w", err)
	}

	vf := vecFile{
		ThreadID:  e.threadID,
		Watermark: e.watermark,
		BodyHash:  e.bodyHash,
		Coarse:    e.coarse.Vector,
		Chunks:    make([]vecFileChunk, len(e.chunks)),
	}
	for i, ch := range e.chunks {
		vf.Chunks[i] = vecFileChunk{TurnNumber: ch.TurnNumber, ChunkHash: e.chunkHashes[i], Vector: ch.Vector}
	}
	b, err := json.Marshal(vf)
	if err != nil {
		return fmt.Errorf("marshal vec %s: %w", e.threadID, err)
	}
	if err := writeFileAtomic(c.vecPath(e.threadID), b); err != nil {
		return fmt.Errorf("write vec %s: %w", e.threadID, err)
	}

	// Manifest RMW under the lock so a concurrent Sweep cannot lose this entry
	// (the .vec write above is per-thread atomic and needs no lock).
	c.manifestMu.Lock()
	defer c.manifestMu.Unlock()
	m, err := c.loadManifest()
	if err != nil {
		return err
	}
	m.Threads[e.threadID] = manifestEntry{
		Watermark:  e.watermark,
		BodyHash:   e.bodyHash,
		ChunkCount: len(e.chunks),
	}
	return c.saveManifest(m)
}

// Sweep drops .vec files for threads no longer in liveIDs and compacts the
// manifest to the live set (the §6.4 Consolidate sleep-cycle hook). A
// missing cache dir is a no-op (nothing was ever written). Lazy-safe:
// startup Reconcile ignores entries with no live spine thread anyway, so a
// missed Sweep only wastes disk.
func (c *vecCache) Sweep(ctx context.Context, liveIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Manifest RMW under the lock so a concurrent indexer Write cannot lose an
	// entry across this load-modify-save (see manifestMu). The .vec/.tree
	// removals are held under the lock too so the on-disk files and the manifest
	// stay consistent w.r.t. a racing Write.
	c.manifestMu.Lock()
	defer c.manifestMu.Unlock()
	m, err := c.loadManifest()
	if err != nil {
		return err
	}
	live := make(map[string]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		live[id] = struct{}{}
	}

	changed := false
	for id := range m.Threads {
		if _, ok := live[id]; ok {
			continue
		}
		// Drop the .vec; a missing file is fine (already gone).
		if err := os.Remove(c.vecPath(id)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("sweep remove vec %s: %w", id, err)
		}
		// Drop the derived .tree sidecar alongside the .vec (#111 Inc C §6):
		// a tree is meaningless without its leaves. Missing is fine.
		if err := os.Remove(c.treePath(id)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("sweep remove tree %s: %w", id, err)
		}
		delete(m.Threads, id)
		changed = true
	}
	if changed {
		return c.saveManifest(m)
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file + rename, so a crash
// mid-write never leaves a half-written .vec the next startup would fail to
// parse. The temp file sits in the same directory so rename stays on one
// filesystem.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
