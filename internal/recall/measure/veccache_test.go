package measure_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
)

// countingEmbedder wraps the deterministic MockEmbedder and counts the
// number of individual texts embedded (the AC4 metric: an embed call over a
// batch of N texts costs N, so "only that chunk" is N == changed-chunk
// count). Concurrency-safe because the indexer goroutine and the
// Prepare/Reconcile path may both call Embed.
type countingEmbedder struct {
	inner *model.MockEmbedder
	mu    sync.Mutex
	texts int
}

func newCountingEmbedder() *countingEmbedder {
	return &countingEmbedder{inner: model.NewMockEmbedder()}
}

func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	c.mu.Lock()
	c.texts += len(texts)
	c.mu.Unlock()
	return c.inner.Embed(ctx, texts)
}

func (c *countingEmbedder) embedded() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.texts
}

// waitForIndexerIdle gives the async indexer time to consume any enqueued
// jobs (full misses from Prepare) and write through to the cache. It polls
// the cache manifest's presence as a cheap "indexer has run" signal; a more
// precise sync is the per-thread Recall poll used elsewhere, but for the
// cache round-trip we just need the write-through to have landed.
func waitForIndexerIdle(t *testing.T, cacheDir string, wantThreads int) {
	t.Helper()
	manifest := filepath.Join(cacheDir, "manifest.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n := manifestThreadCount(t, manifest); n >= wantThreads {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("cache manifest never reached %d threads within deadline", wantThreads)
}

// manifestThreadCount reads the cache manifest and returns how many threads
// it records. A missing manifest is zero (the cache has not been written).
// Counts the per-thread "dispatch_watermark" key occurrences so the test
// stays decoupled from the cache's private JSON types.
func manifestThreadCount(t *testing.T, manifestPath string) int {
	t.Helper()
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read manifest: %v", err)
	}
	return strings.Count(string(b), `"dispatch_watermark"`)
}

// TestVecCache_RoundTrip writes vectors through the indexer, then reloads
// them on a fresh Service over the same home: the reloaded snapshot recalls
// the same intra-thread chunk with zero new embed calls for the reloaded
// thread body+chunks (the .vec was loaded verbatim).
func TestVecCache_RoundTrip(t *testing.T) {
	paths, ops := newRecallHome(t)
	body := "## Turn 1\nquasar redshift spectroscopy luminosity\n\n" +
		"## Turn 2\nglacier moraine sediment erosion\n\n" +
		"## Turn 3\nenzyme catalysis substrate kinetics\n"
	seedThread(t, paths, "thr_1", []string{"thr_1"}, body)

	// First session: index and write through.
	emb1 := newCountingEmbedder()
	svc1 := measure.NewService(ops, emb1)
	if err := svc1.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 1: %v", err)
	}
	r := flushAndWait(t, svc1, "thr_1", 3, "quasar redshift spectroscopy luminosity")
	if r.IntraThread == nil || r.IntraThread.Turns[0] != 1 {
		t.Fatalf("session 1 intra-thread hit wrong: %+v", r.IntraThread)
	}
	waitForIndexerIdle(t, paths.RecallCache, 1)
	if err := svc1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// The .vec file exists on disk.
	if _, err := os.Stat(filepath.Join(paths.RecallCache, "threads", "thr_1.vec")); err != nil {
		t.Fatalf("expected thr_1.vec on disk: %v", err)
	}

	// Second session: Reconcile loads the cache. An unchanged thread costs
	// zero embed calls at startup (AC4). The query embed still costs one.
	emb2 := newCountingEmbedder()
	svc2 := measure.NewService(ops, emb2)
	if err := svc2.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 2: %v", err)
	}
	t.Cleanup(func() { _ = svc2.Close() })
	if got := emb2.embedded(); got != 0 {
		t.Errorf("AC4: unchanged thread cost %d startup embeds, want 0", got)
	}

	results, err := svc2.Recall(context.Background(), measure.Request{
		QueryText: "quasar redshift spectroscopy luminosity",
		Engaged:   "thr_1",
		Exclude:   map[string]struct{}{"thr_1": {}},
	})
	if err != nil {
		t.Fatalf("Recall 2: %v", err)
	}
	r2, ok := findResult(results, "thr_1")
	if !ok || r2.IntraThread == nil {
		t.Fatalf("session 2 (from cache) did not recall intra-thread; results=%+v", results)
	}
	if r2.IntraThread.Turns[0] != 1 {
		t.Errorf("session 2 best turn = %v, want 1", r2.IntraThread.Turns)
	}
}

// TestVecCache_StartupO_Changed is AC4: a restart re-embeds only the changed
// content. Three scenarios share one home:
//   - unchanged thread → 0 startup embeds
//   - thread with one new chunk → exactly that chunk re-embedded (+coarse,
//     whose body hash moved)
//   - brand-new thread (no cache entry) → full miss, embedded from scratch
func TestVecCache_StartupO_Changed(t *testing.T) {
	paths, ops := newRecallHome(t)
	bodyA := "## Turn 1\nquasar redshift spectroscopy\n\n## Turn 2\nglacier moraine sediment\n"
	seedThread(t, paths, "thr_unchanged", []string{"a"}, bodyA)
	bodyB := "## Turn 1\nenzyme catalysis substrate\n\n## Turn 2\nlepton boson neutrino\n"
	seedThread(t, paths, "thr_grow", []string{"b"}, bodyB)

	// Session 1: index both threads, write through, close.
	svc1 := measure.NewService(ops, newCountingEmbedder())
	if err := svc1.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 1: %v", err)
	}
	waitForIndexerIdle(t, paths.RecallCache, 2)
	if err := svc1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// Between sessions: append a new chunk to thr_grow (turn 3), and add a
	// brand-new thread thr_new with no cache entry.
	if err := store.AppendThreadTurn(paths, "thr_grow", 3, "## Turn 3\nplasma tokamak confinement\n"); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	seedThread(t, paths, "thr_new", []string{"c"}, "## Turn 1\nledger accrual depreciation\n")

	// Session 2: Reconcile runs synchronously in Prepare, so the embed count
	// is final on return — no indexer wait needed for the startup pass.
	emb2 := newCountingEmbedder()
	svc2 := measure.NewService(ops, emb2)
	if err := svc2.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 2: %v", err)
	}
	t.Cleanup(func() { _ = svc2.Close() })

	// Expected startup embeds:
	//   thr_unchanged: 0 (HIT)
	//   thr_grow:      1 new chunk (turn 3) + 1 coarse (body hash moved) = 2 (PARTIAL)
	//   thr_new:       1 coarse + 1 chunk = 2 (FULL miss)
	// Total = 4. The unchanged thread's two chunks are NOT re-embedded — that
	// is the O(changed) guarantee.
	if got := emb2.embedded(); got != 4 {
		t.Errorf("AC4: startup embedded %d texts, want 4 (0 unchanged + 2 partial + 2 full-miss)", got)
	}
}

// TestVecCache_NeverCanonical is AC6/I5: deleting .recall-cache/ and
// restarting produces an identical index (rebuilt from canonical) and
// identical recall output — the cache is never the source of truth.
func TestVecCache_NeverCanonical(t *testing.T) {
	paths, ops := newRecallHome(t)
	body := "## Turn 1\ntrefoil knot topology invariant\n\n" +
		"## Turn 2\nmonsoon humidity precipitation\n\n" +
		"## Turn 3\nledger reconciliation accrual\n"
	seedThread(t, paths, "thr_1", []string{"thr_1"}, body)
	seedThread(t, paths, "thr_2", []string{"thr_2"}, "## Turn 1\nneutrino oscillation flavor lepton\n")

	probe := measure.Request{
		QueryText: "trefoil knot topology invariant",
		Engaged:   "thr_1",
		Exclude:   map[string]struct{}{"thr_1": {}},
	}

	// Session 1: build the cache.
	svc1 := measure.NewService(ops, newCountingEmbedder())
	if err := svc1.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 1: %v", err)
	}
	r1 := flushAndWait(t, svc1, "thr_1", 3, probe.QueryText)
	waitForIndexerIdle(t, paths.RecallCache, 2)
	if err := svc1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// Delete the entire cache directory — it must cost only CPU at restart.
	if err := os.RemoveAll(paths.RecallCache); err != nil {
		t.Fatalf("rm cache: %v", err)
	}

	// Session 2: rebuild from canonical (full misses), then probe.
	svc2 := measure.NewService(ops, newCountingEmbedder())
	if err := svc2.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 2: %v", err)
	}
	t.Cleanup(func() { _ = svc2.Close() })
	// The deleted cache forces a full rebuild via the indexer; the engaged
	// thread's chunks are repopulated. Poll until the intra-thread hit is
	// republished (same mechanism flushAndWait uses, but the job was already
	// enqueued by Reconcile as a full miss).
	var r2 measure.Result
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		results, err := svc2.Recall(context.Background(), probe)
		if err != nil {
			t.Fatalf("Recall 2: %v", err)
		}
		if r, ok := findResult(results, "thr_1"); ok && r.IntraThread != nil {
			r2 = r
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r2.IntraThread == nil {
		t.Fatalf("AC6: rebuilt index did not recall intra-thread after cache deletion")
	}

	// Identical recall output: same best turn, same score (the embedder is
	// deterministic, so the rebuilt vectors are bit-identical).
	if r1.IntraThread.Turns[0] != r2.IntraThread.Turns[0] {
		t.Errorf("AC6: best turn differs after rebuild: was %v, now %v",
			r1.IntraThread.Turns[0], r2.IntraThread.Turns[0])
	}
	if r1.IntraThread.Score != r2.IntraThread.Score {
		t.Errorf("AC6: score differs after rebuild: was %v, now %v",
			r1.IntraThread.Score, r2.IntraThread.Score)
	}
}

// TestVecCache_Sweep is the §6.4 hook: a .vec for an absent thread is
// dropped on SweepCache; a live thread's .vec is retained.
func TestVecCache_Sweep(t *testing.T) {
	paths, ops := newRecallHome(t)
	seedThread(t, paths, "thr_live", []string{"l"}, "## Turn 1\nquasar redshift\n")
	seedThread(t, paths, "thr_gone", []string{"g"}, "## Turn 1\nglacier moraine\n")

	svc := measure.NewService(ops, newCountingEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	waitForIndexerIdle(t, paths.RecallCache, 2)

	liveVec := filepath.Join(paths.RecallCache, "threads", "thr_live.vec")
	goneVec := filepath.Join(paths.RecallCache, "threads", "thr_gone.vec")
	for _, p := range []string{liveVec, goneVec} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s before sweep: %v", filepath.Base(p), err)
		}
	}

	// Remove thr_gone from the spine so it is no longer live, then sweep.
	if err := store.RemoveSpineRecords(paths, []string{"thr_gone"}); err != nil {
		t.Fatalf("remove spine record: %v", err)
	}
	if err := svc.SweepCache(context.Background()); err != nil {
		t.Fatalf("SweepCache: %v", err)
	}

	if _, err := os.Stat(goneVec); !os.IsNotExist(err) {
		t.Errorf("thr_gone.vec should be swept; stat err = %v", err)
	}
	if _, err := os.Stat(liveVec); err != nil {
		t.Errorf("thr_live.vec should be retained; stat err = %v", err)
	}
}
