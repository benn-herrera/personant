package measure

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"personant/internal/recall/scoring"
)

// TestVecCache_WriteSweepManifestRace drives vecCache.Write (the indexer
// write-through) concurrently with vecCache.Sweep (the sleep-cycle hook) — the
// two unsynchronized manifest.json read-modify-write cycles the burn-down
// flagged. Both do loadManifest → mutate → saveManifest; without manifestMu a
// Sweep interleaving a Write silently drops whichever entry it did not observe,
// masquerading as a surprise re-embed next startup.
//
// The assertion is logical (the race is a lost FILE update, which the -race
// detector does not track — file I/O is outside its memory model): every
// entry a Write persisted must survive concurrent Sweeps that never list it
// for deletion. The Sweeps still perform real RMWs (they delete a pre-seeded
// dead set), so their saves race the writers' saves. Run under `GOFLAGS=-race`
// for the added memory-safety check on manifestMu.
func TestVecCache_WriteSweepManifestRace(t *testing.T) {
	c := newVecCache(nil, nil, t.TempDir())
	ctx := context.Background()

	// Pre-seed a dead set the Sweeps will delete, so every Sweep does a real
	// load-modify-SAVE (not a no-op) and thus races the writers' saves.
	const dead = 40
	deadIDs := make([]string, dead)
	m, err := c.loadManifest()
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	for i := 0; i < dead; i++ {
		id := fmt.Sprintf("thr_dead_%d", i)
		deadIDs[i] = id
		m.Threads[id] = manifestEntry{Watermark: 1, BodyHash: "d", ChunkCount: 0}
	}
	if err := c.saveManifest(m); err != nil {
		t.Fatalf("saveManifest seed: %v", err)
	}

	const writers = 32
	writerIDs := make([]string, writers)
	for i := range writerIDs {
		writerIDs[i] = fmt.Sprintf("thr_w_%d", i)
	}
	// liveIDs keeps every writer thread alive (never swept) but omits the dead
	// set (always swept).
	live := append([]string(nil), writerIDs...)

	var wg sync.WaitGroup
	for _, id := range writerIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := c.Write(ctx, cacheEntry{
				threadID: id,
				coarse:   scoring.ThreadVector{ThreadID: id, Vector: []float64{1, 0}},
				bodyHash: "h",
			})
			if err != nil {
				t.Errorf("Write %s: %v", id, err)
			}
		}(id)
	}
	for s := 0; s < 8; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Sweep(ctx, live); err != nil {
				t.Errorf("Sweep: %v", err)
			}
		}()
	}
	wg.Wait()

	final, err := c.loadManifest()
	if err != nil {
		t.Fatalf("final loadManifest: %v", err)
	}
	for _, id := range writerIDs {
		if _, ok := final.Threads[id]; !ok {
			t.Errorf("writer entry %s lost to a concurrent Sweep RMW (manifest lock not holding)", id)
		}
	}
	// The dead set must be gone (the Sweeps ran) — and no writer collateral.
	for _, id := range deadIDs {
		if _, ok := final.Threads[id]; ok {
			t.Errorf("dead entry %s survived every Sweep", id)
		}
	}
}
