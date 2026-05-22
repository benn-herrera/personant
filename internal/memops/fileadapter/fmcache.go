package fileadapter

import (
	"sync"

	"personant/internal/memops"
	"personant/internal/store"
)

// frontmatterCache is an in-memory, write-through cache of parsed thread
// frontmatter, keyed by thread ID. It exists to eliminate the per-recall
// reparse cost measured in recall_bench_test.go (commit 377c0372): every
// FileAdapter.ProposeRecall call was re-reading and YAML-unmarshalling
// EVERY live thread's thread.md, which dominated 90–96% of recall time.
//
// # Correctness model: invalidate / write-through on every write
//
// The cache is kept coherent by write-through and invalidation on every
// thread.md frontmatter mutation, NOT by mtime/stat validation. The sim
// compresses wall-clock time, so many turns write within the same
// filesystem mtime tick — any mtime-token scheme could return a stale
// parse. Invalidate-on-write has no such hazard.
//
// This is sound only because FileAdapter is the SOLE mutator of thread.md
// frontmatter in production and sim. The four FileAdapter write sites
// (CreateThread, EngageThread, RecordRecallFire, ArchiveThread) each call
// Put or Invalidate after a successful store write. Out-of-band writers
// (store.SeedThread in tests, future submind git-merge per #94) MUST call
// FileAdapter.InvalidateThread / InvalidateAll to preserve coherence —
// there is no other path that corrects a stale entry.
//
// A stale cache that returned wrong recall results already burned this
// project once (the reverted "Inc 6" symbols.jsonl candidate filter read a
// stale on-disk index and regressed recall); this design exists to make
// that class of bug impossible by construction.
type frontmatterCache struct {
	mu      sync.Mutex
	entries map[string]memops.ThreadMeta
}

// newFrontmatterCache constructs an empty cache with its map initialized.
func newFrontmatterCache() *frontmatterCache {
	return &frontmatterCache{entries: make(map[string]memops.ThreadMeta)}
}

// LoadAll returns the parsed frontmatter of every live thread, using
// cached parses where available. It replicates store.LoadAllThreadFrontmatter's
// observable semantics exactly so the result is indistinguishable from
// calling that function directly:
//
//   - ListThreadIDs supplies the authoritative live set (cheap readdir;
//     the expensive per-thread parse is what the cache eliminates).
//   - Tolerate-and-continue: a per-thread load error is logged via logf
//     and the thread is skipped, not fatal. A nil logf is silent.
//   - A missing ThreadsDir (no ids) yields (nil, nil).
//
// On every call the cache evicts entries whose id is no longer in the
// live set, so the map tracks the live thread count (bounded by archival)
// rather than growing without limit.
func (c *frontmatterCache) LoadAll(paths store.PersonantPaths, logf func(format string, args ...any)) ([]memops.ThreadMeta, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ids, err := store.ListThreadIDs(paths)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	live := make(map[string]struct{}, len(ids))
	out := make([]memops.ThreadMeta, 0, len(ids))
	for _, id := range ids {
		live[id] = struct{}{}
		if fm, ok := c.entries[id]; ok {
			out = append(out, fm)
			continue
		}
		fm, err := store.LoadThreadFrontmatter(paths, id)
		if err != nil {
			logf("load thread frontmatter: skip %s: %v", store.ThreadMetaPath(paths, id), err)
			continue
		}
		c.entries[id] = fm
		out = append(out, fm)
	}

	// Evict entries for threads that are no longer live.
	for id := range c.entries {
		if _, ok := live[id]; !ok {
			delete(c.entries, id)
		}
	}
	return out, nil
}

// Put write-throughs fm keyed by fm.ID. A zero ID is ignored (there is
// nothing to key on, and store.SaveThreadFrontmatter would have rejected
// it anyway).
func (c *frontmatterCache) Put(fm memops.ThreadMeta) {
	if fm.ID == "" {
		return
	}
	c.mu.Lock()
	c.entries[fm.ID] = fm
	c.mu.Unlock()
}

// Invalidate drops the entry for id (a no-op if absent), forcing the next
// LoadAll to re-parse it from disk.
func (c *frontmatterCache) Invalidate(id string) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

// Reset clears every entry, forcing a full re-parse on the next LoadAll.
func (c *frontmatterCache) Reset() {
	c.mu.Lock()
	c.entries = make(map[string]memops.ThreadMeta)
	c.mu.Unlock()
}
