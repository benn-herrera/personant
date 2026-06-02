// Package measure is the application-side §3.4 recall stack — the
// substrate-coupled glue that the experience layer uses to drive
// recall during a turn.
//
// It depends on the memops.MemoryOps port for substrate access (thread
// listing, body loading, log writes) and on the substrate-free
// scoring primitives in recall/scoring for the actual Jaccard and
// cosine math. The split is the architectural boundary called out in
// the MAD architecture-and-standards review: scoring is pure / testable
// without any substrate; measure is the substrate-aware composition.
//
// The embedding index is a hierarchical coarse→fine structure (design
// intra-thread-recall-design.md): a coarse one-vector-per-thread tier
// for thread selection, over a fine per-turn-excerpt chunk-vector tier
// for intra-thread content retrieval. The live index is an immutable
// indexSnapshot published via an atomic.Pointer; one indexer goroutine
// rebuilds snapshots off to the side and swaps the pointer (I1 lock-free
// reads, I2 monotonic per-thread updates via a dispatch watermark).
package measure

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/scoring"
)

// maxEmbedChars caps the text sent to the embedder for one thread, query,
// or chunk. Thread bodies rarely approach this; it keeps an /embeddings
// request within a typical model context window.
const maxEmbedChars = 28000

// Kc is the coarse top-K: the number of threads the coarse pass selects
// for the fine pass (design §4.2). Ten captures essentially all genuine
// thread matches (C.6: true topic in top-10 by cosine) while bounding
// the fine-pass fan-out. A §9 calibration window, not a frozen constant.
const Kc = 10

// Kf is the fine top-K: the number of chunks the fine pass keeps per
// selected thread (design §4.2). Three surfaces the relevant passage
// plus immediate neighbours without flooding the merge. A §9 calibration
// window. (scoring.DefaultChunkLimit mirrors this for the pure layer.)
const Kf = 3

// indexSnapshot is the immutable hierarchical embedding index. Once
// published into Service.cur it is never mutated; the indexer builds a
// fresh snapshot and atomically swaps the pointer (design §6.3). Readers
// load the pointer once and scan a consistent whole — never a torn one
// (I1).
type indexSnapshot struct {
	coarse    []scoring.ThreadVector           // one vector per thread (coarse tier)
	fine      map[string][]scoring.ChunkVector // threadID → scrolled-out chunk vectors (fine tier)
	watermark map[string]int                   // threadID → dispatch turncount these vectors were read at
}

// emptySnapshot is the published value before any thread is indexed and
// the value a nil-embedder Service leaves in place. Holding a non-nil
// snapshot lets Recall load-and-scan unconditionally without a nil check.
func emptySnapshot() *indexSnapshot {
	return &indexSnapshot{
		coarse:    nil,
		fine:      map[string][]scoring.ChunkVector{},
		watermark: map[string]int{},
	}
}

// indexJob is one unit of indexer work: (re)embed a thread's coarse body
// and its scrolled-out chunks, carrying the turncount at dispatch as the
// monotonicity watermark (I2). The indexer always reloads current
// substrate state, so the job needs only the thread and its watermark.
type indexJob struct {
	threadID          string
	dispatchTurncount int
}

// vectorCache is the §5 persisted derived-vector cache. A nil handle is a
// no-op (no embedder → no cache, I7); when present it is the veccache in
// veccache.go. The interface is the seam Prepare and the swap point hook
// into; the concrete cache reads canonical via the port and reads/writes
// .recall-cache/ via plain file I/O (operational state, never canonical —
// I5; it never writes into the git-tracked tree and never parses git).
type vectorCache interface {
	// Reconcile is the §5.3 O(changed) startup. It loads the manifest +
	// .vec files, content-hashes each live thread's current canonical
	// body/chunks against the cache, and returns the fully built initial
	// snapshot: unchanged threads loaded verbatim (zero embed calls — AC4),
	// changed threads re-embedded for only the changed chunks (+coarse if
	// the body moved), and brand-new threads embedded from scratch. The
	// snapshot is warm on return, so Prepare publishes a usable index with
	// no async cold window.
	Reconcile(ctx context.Context) (*indexSnapshot, error)
	// Write persists one thread's freshly embedded vectors (write-through
	// at the swap point, §6.3). bodyHash keys the coarse vector's
	// staleness; chunkHashes[i] keys chunks[i] (§5.3).
	Write(ctx context.Context, e cacheEntry) error
	// Sweep drops .vec files for threads no longer on the spine (archived /
	// removed) and compacts the manifest to the live set (the §6.4
	// Consolidate sleep-cycle hook). Lazy-safe: startup Reconcile also
	// ignores stale entries, so a missed Sweep only wastes disk, never
	// correctness.
	Sweep(ctx context.Context, liveThreadIDs []string) error
}

// cacheEntry is one thread's freshly embedded vectors plus their content-
// hash staleness keys, handed to vectorCache.Write at the swap point. The
// hashes are computed at the same point the embed texts exist (processJob),
// so the cache never re-reads canonical to key its writes.
type cacheEntry struct {
	threadID    string
	coarse      scoring.ThreadVector
	chunks      []scoring.ChunkVector
	watermark   int
	bodyHash    string   // hash of the assembled body fed to the coarse embed
	chunkHashes []string // chunkHashes[i] hashes chunks[i]'s excerpt text
}

// IntraThreadHit is the intra-thread (fine-tier) detail for a recalled
// thread: the matched scrolled-out turn-excerpt number(s) of the engaged
// thread and the best chunk score. Sibling of SymbolicHit / EmbeddingHit
// (design §7).
type IntraThreadHit struct {
	Turns []int
	Score float64
}

// Recaller is the §3.4 recall stack behind one contract. Experience-
// layer code (the recall UI surface) depends only on this interface
// and the Result type; the substrate port, the embedding model, and
// the embedding index are held inside the implementation and never
// exposed. That is the insulation boundary between experience logic
// and data/substrate logic — recall internals (layer composition,
// models, indexes, eventually layer-3 model judgment) can change
// without touching any caller.
type Recaller interface {
	// Prepare warms the recaller — e.g. builds the embedding index and
	// starts the indexer goroutine. Called once after construction. A
	// symbolic-only recaller no-ops.
	Prepare(ctx context.Context) error

	// Recall returns merged, ranked recall candidates for one turn.
	Recall(ctx context.Context, req Request) ([]Result, error)

	// Close releases the recaller's resources — stops the indexer
	// goroutine. Idempotent; a symbolic-only recaller no-ops. Safe to call
	// even if Prepare was never called.
	Close() error
}

// Request is the per-turn input to recall.
type Request struct {
	// QuerySymbols is the turn's coalesced symbol set — layer 1 input.
	QuerySymbols []string
	// QueryText is the turn's user input — layer 2 (embedding) input.
	QueryText string
	// Project scopes the layer-1 symbolic match. Empty → no filter.
	Project string
	// Exclude is the set of thread IDs to omit at the thread level —
	// typically threads engaged this turn. The engaged thread named in
	// Engaged is still admitted to the fine pass by ID (design §4.1 step
	// 3) even when it is in Exclude.
	Exclude map[string]struct{}
	// Engaged is the thread the user is currently in. When set and an
	// embedder is configured, its fine-tier (scrolled-out) chunks are
	// scanned unconditionally — bypassing the coarse gate — to surface
	// early content of the long-running thread (the #109 intra-thread
	// case). Empty → no intra-thread pass.
	Engaged string
}

// SymbolicHit is the layer-1 detail for a recalled thread.
type SymbolicHit struct {
	Score          float64
	MatchedSymbols []string
}

// EmbeddingHit is the layer-2 detail for a recalled thread.
type EmbeddingHit struct {
	Score float64
}

// Result is one merged recall candidate. Symbolic / Embedding /
// IntraThread are nil when that layer did not fire for the thread.
type Result struct {
	ThreadID    string
	Score       float64 // unified ranking score (see Service.Recall)
	Symbolic    *SymbolicHit
	Embedding   *EmbeddingHit
	IntraThread *IntraThreadHit
}

// Layers reports which recall layers fired for this result.
func (r Result) Layers() []string {
	var out []string
	if r.Symbolic != nil {
		out = append(out, "symbolic")
	}
	if r.Embedding != nil {
		out = append(out, "embedding")
	}
	if r.IntraThread != nil {
		out = append(out, "intra-thread")
	}
	return out
}

// Service is the default Recaller. It composes §3.4 layer 1 (symbolic
// Jaccard, via the MemoryOps port) and layer 2 (embedding cosine, over
// a hierarchical coarse→fine index it owns and builds). Layer 3 (model
// judgment) will slot in here later with no interface change.
type Service struct {
	ops      memops.MemoryOps
	embedder model.Embedder // nil → symbolic-only recall, no indexer goroutine
	cache    vectorCache    // §5 persisted cache; nil → no cache (Inc 2 stub)

	// cur is the live index snapshot, read lock-free by Recall (I1). It is
	// always non-nil after construction. Only the indexer goroutine stores
	// to it (CAS), so a single mutator never tears a swap.
	cur atomic.Pointer[indexSnapshot]

	// jobs feeds the single indexer goroutine. nil when no embedder (the
	// goroutine is never started). EnqueueFlush is a no-op while nil.
	jobs chan indexJob

	// indexerDone is closed by the indexer goroutine when it exits, so
	// Close can wait for a clean shutdown (no leaked goroutine).
	indexerDone chan struct{}

	closeOnce sync.Once
}

// recallCacheLocator is the optional interface a MemoryOps adapter
// satisfies to expose the operational directory the persisted vector cache
// lives in (§5.1, $PERSONANT_HOME/.recall-cache/). The adapter owns paths —
// they are its secret — so the cache directory is surfaced through this
// narrow type-assertion rather than a port method or a PersonantPaths in a
// signature (the §5/§11.6 negative constraint: the cache is operational
// state, not a port concept). The file adapter implements it; a port mock
// that does not is simply run without a persisted cache (Prepare falls back
// to the from-scratch coarse build), which keeps tests and alternate
// adapters working unchanged.
type recallCacheLocator interface {
	RecallCacheDir() string
}

// NewService constructs a Service. A nil embedder yields a symbolic-only
// recaller — embedding recall is opt-in per provider, and with no embedder
// the indexer goroutine is never started and no cache is created (I7).
//
// When the embedder is non-nil and ops exposes a recall-cache directory
// (recallCacheLocator), the §5 persisted vector cache is wired in so
// Prepare runs the O(changed) reconcile and the indexer write-through
// persists vectors. A port that does not expose the directory runs without
// a cache — correct, just no startup-cost savings.
func NewService(ops memops.MemoryOps, embedder model.Embedder) *Service {
	s := &Service{ops: ops, embedder: embedder}
	if embedder != nil {
		if loc, ok := ops.(recallCacheLocator); ok {
			s.cache = newVecCache(ops, embedder, loc.RecallCacheDir())
		}
	}
	s.cur.Store(emptySnapshot())
	return s
}

// Prepare warms the index and starts the single indexer goroutine. A
// no-op when no embedder is configured (I7).
//
// With a persisted cache (s.cache != nil) it runs the §5.3 O(changed)
// startup: Reconcile loads cached vectors for unchanged threads (zero
// embed calls — AC4), re-embeds only the changed chunks of partial-miss
// threads, and returns full-miss threads as indexJobs. Without a cache
// (a port that does not expose a cache dir, or a test) it falls back to
// the from-scratch coarse build (embed every thread's body). Either way
// the fine tier for un-cached threads is then filled by EnqueueFlush jobs
// as threads scroll out (Inc 4 drives those).
func (s *Service) Prepare(ctx context.Context) error {
	if s.embedder == nil {
		return nil
	}

	// Build the initial snapshot synchronously so the index is warm on
	// return (a Recall right after Prepare sees the full coarse+fine tier,
	// not an empty one). With a cache this is the §5.3 O(changed) reconcile;
	// without one it is the from-scratch coarse build.
	var snap *indexSnapshot
	if s.cache != nil {
		reconciled, err := s.cache.Reconcile(ctx)
		if err != nil {
			return fmt.Errorf("recall: cache reconcile: %w", err)
		}
		snap = reconciled
	} else {
		built, err := s.buildCoarse(ctx)
		if err != nil {
			return err
		}
		snap = built
	}
	s.cur.Store(snap)

	// Start the single indexer goroutine for runtime upkeep (EnqueueFlush /
	// AddThread driven by Inc 4's debt/dormancy hooks). Startup misses were
	// already filled synchronously by the reconcile above.
	s.jobs = make(chan indexJob, jobQueueDepth)
	s.indexerDone = make(chan struct{})
	go s.runIndexer()
	return nil
}

// buildCoarse is the no-cache fallback for Prepare: it embeds every
// thread's body into a fresh coarse tier (the fine tier starts empty,
// filled by EnqueueFlush). Used only when the port exposes no cache dir.
func (s *Service) buildCoarse(ctx context.Context) (*indexSnapshot, error) {
	recs, err := s.ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return nil, fmt.Errorf("recall: list threads: %w", err)
	}
	snap := emptySnapshot()
	if len(recs) == 0 {
		return snap, nil
	}
	texts := make([]string, len(recs))
	for i, r := range recs {
		thr, err := s.ops.LoadThread(ctx, r.ID)
		if err != nil {
			return nil, fmt.Errorf("recall: load %s: %w", r.ID, err)
		}
		texts[i] = truncateForEmbed(thr.Body)
	}
	vecs, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("recall: embed index: %w", err)
	}
	if len(vecs) != len(recs) {
		return nil, fmt.Errorf("recall: %d vectors for %d threads", len(vecs), len(recs))
	}
	snap.coarse = make([]scoring.ThreadVector, len(recs))
	for i := range recs {
		snap.coarse[i] = scoring.ThreadVector{ThreadID: recs[i].ID, Vector: vecs[i]}
	}
	return snap, nil
}

// jobQueueDepth bounds the indexer's pending-job buffer. Flush triggers
// (debt cap + dormancy, Inc 4) enqueue well under one job per turn even
// on a continuously active thread; this depth absorbs bursts without the
// turn loop blocking on a busy indexer.
const jobQueueDepth = 256

// AddThread enqueues a full flush for one thread — the incremental
// counterpart to Prepare's batch build. A long-lived session creates
// threads continuously; without per-thread upkeep the embedding layer
// could never recall a thread created after Prepare ran.
//
// A no-op when no embedder is configured (I7). Asynchronous: the indexer
// embeds the thread and swaps it into the snapshot; the call returns
// immediately. The dispatch watermark is the thread's current turncount.
func (s *Service) AddThread(ctx context.Context, threadID string) error {
	if s.embedder == nil {
		return nil
	}
	rec, found, err := s.ops.FindThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("recall: add-to-index find %s: %w", threadID, err)
	}
	if !found {
		return fmt.Errorf("recall: add-to-index %s: %w", threadID, memops.ErrThreadNotFound)
	}
	s.EnqueueFlush(threadID, rec.TurnCount)
	return nil
}

// EnqueueFlush is the public entry the turn loop calls (Inc 4) at a debt-
// cap or dormancy trigger to (re)index a thread. It is non-blocking up to
// the queue depth; a no-op when no embedder is configured or before
// Prepare started the indexer. dispatchTurncount is the thread's
// turncount at dispatch — the monotonicity watermark (I2).
func (s *Service) EnqueueFlush(threadID string, dispatchTurncount int) {
	if s.jobs == nil {
		return
	}
	s.jobs <- indexJob{threadID: threadID, dispatchTurncount: dispatchTurncount}
}

// SweepCache drops persisted .vec files for threads no longer on the spine
// and compacts the manifest to the live set — the §6.4 sleep-cycle hook,
// meant to be called from the same owner that calls MemoryOps.Consolidate
// (the cache is operational state the adapter's Consolidate cannot reach,
// so the seam is on the Service the owner already holds). A no-op when no
// cache is configured (nil embedder / no cache dir). Best-effort and
// non-fatal: the sweep is opportunistic disk hygiene, never correctness —
// startup Reconcile ignores stale entries regardless (I5).
func (s *Service) SweepCache(ctx context.Context) error {
	if s.cache == nil {
		return nil
	}
	recs, err := s.ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return fmt.Errorf("recall: sweep-cache list threads: %w", err)
	}
	live := make([]string, len(recs))
	for i, r := range recs {
		live[i] = r.ID
	}
	if err := s.cache.Sweep(ctx, live); err != nil {
		return fmt.Errorf("recall: sweep-cache: %w", err)
	}
	return nil
}

// Close stops the indexer goroutine and waits for it to drain. Idempotent
// and safe to call when Prepare never ran (no goroutine, no channel).
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		if s.jobs == nil {
			return
		}
		close(s.jobs)
		<-s.indexerDone
	})
	return nil
}

// runIndexer is the single index-maintenance goroutine (I1/I2, design
// §6.3). It owns every write to s.cur. It exits when s.jobs is closed,
// signalling via s.indexerDone so Close can join it.
func (s *Service) runIndexer() {
	defer close(s.indexerDone)
	ctx := context.Background()
	for job := range s.jobs {
		s.processJob(ctx, job)
	}
}

// processJob (re)embeds one thread and swaps the result into the live
// snapshot under the I2 watermark rule. Embedding failures are logged and
// swallowed — indexing is advisory; a failed job leaves the prior vectors
// in place rather than blanking the thread.
func (s *Service) processJob(ctx context.Context, job indexJob) {
	thr, err := s.ops.LoadThread(ctx, job.threadID)
	if err != nil {
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "index-error", fmt.Sprintf("load %s: %v", job.threadID, err))
		return
	}
	excerpts, err := s.ops.LoadThreadExcerpts(ctx, job.threadID)
	if err != nil {
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "index-error", fmt.Sprintf("excerpts %s: %v", job.threadID, err))
		return
	}

	// Batch: coarse body first, then one chunk text per excerpt. One embed
	// call for the whole thread (design §6.1). The body text and each
	// excerpt text are hashed for the cache's staleness keys (§5.3) at the
	// same point they exist, so the cache never re-reads canonical to key
	// its write.
	bodyText := truncateForEmbed(thr.Body)
	texts := make([]string, 0, len(excerpts)+1)
	texts = append(texts, bodyText)
	for _, ex := range excerpts {
		texts = append(texts, truncateForEmbed(ex.Text))
	}
	vecs, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "index-error", fmt.Sprintf("embed %s: %v", job.threadID, err))
		return
	}
	if len(vecs) != len(texts) {
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "index-error",
			fmt.Sprintf("embed %s: %d vectors for %d texts", job.threadID, len(vecs), len(texts)))
		return
	}

	coarse := scoring.ThreadVector{ThreadID: job.threadID, Vector: vecs[0]}
	chunks := make([]scoring.ChunkVector, len(excerpts))
	chunkHashes := make([]string, len(excerpts))
	for i, ex := range excerpts {
		chunks[i] = scoring.ChunkVector{TurnNumber: ex.TurnNumber, Vector: vecs[i+1]}
		chunkHashes[i] = contentHash(ex.Text)
	}

	s.swap(job.threadID, job.dispatchTurncount, coarse, chunks)

	if s.cache != nil {
		if err := s.cache.Write(ctx, cacheEntry{
			threadID:    job.threadID,
			coarse:      coarse,
			chunks:      chunks,
			watermark:   job.dispatchTurncount,
			bodyHash:    contentHash(bodyText),
			chunkHashes: chunkHashes,
		}); err != nil {
			_ = s.ops.Log(ctx, memops.LogCategoryRecall, "index-error", fmt.Sprintf("cache %s: %v", job.threadID, err))
		}
	}
}

// swap publishes one thread's freshly embedded vectors under the I2
// monotonicity rule: a result whose dispatch watermark is not strictly
// greater than the thread's recorded watermark is dropped (a fresher
// embed already won — F5). Otherwise it builds a new snapshot with this
// thread's coarse/fine/watermark replaced and CAS-stores it, retrying on
// a lost race (the build is cheap relative to the embed).
//
// This is the sole writer of s.cur; the CAS retry guards only against a
// future second mutator — today the single indexer goroutine makes the
// CAS always succeed first try, but the loop keeps the contract explicit.
func (s *Service) swap(threadID string, dispatchTurncount int, coarse scoring.ThreadVector, chunks []scoring.ChunkVector) {
	for {
		old := s.cur.Load()
		if dispatchTurncount <= old.watermark[threadID] {
			return // stale embed — a fresher vector already published (I2/F5)
		}
		next := old.with(threadID, coarse, chunks, dispatchTurncount)
		if s.cur.CompareAndSwap(old, next) {
			return
		}
	}
}

// with returns a new snapshot equal to s but with threadID's coarse
// vector, chunk vectors, and watermark replaced. The receiver is not
// mutated (snapshots are immutable once published). The maps are copied;
// the coarse slice is rebuilt with the one entry replaced or appended.
func (s *indexSnapshot) with(threadID string, coarse scoring.ThreadVector, chunks []scoring.ChunkVector, watermark int) *indexSnapshot {
	newCoarse := make([]scoring.ThreadVector, 0, len(s.coarse)+1)
	replaced := false
	for _, tv := range s.coarse {
		if tv.ThreadID == threadID {
			newCoarse = append(newCoarse, coarse)
			replaced = true
		} else {
			newCoarse = append(newCoarse, tv)
		}
	}
	if !replaced {
		newCoarse = append(newCoarse, coarse)
	}

	newFine := make(map[string][]scoring.ChunkVector, len(s.fine)+1)
	for id, cv := range s.fine {
		newFine[id] = cv
	}
	if len(chunks) == 0 {
		delete(newFine, threadID)
	} else {
		newFine[threadID] = chunks
	}

	newWM := make(map[string]int, len(s.watermark)+1)
	for id, w := range s.watermark {
		newWM[id] = w
	}
	newWM[threadID] = watermark

	return &indexSnapshot{coarse: newCoarse, fine: newFine, watermark: newWM}
}

// Recall runs the recall layers and returns merged candidates ranked
// for the experience layer.
//
// Layers run as parallel signals, not a cascade — the C.6 calibration
// showed symbolic Jaccard recall collapses under vocabulary drift, so
// it must not gate the embedding scan. Layer 1 failure (a substrate
// error) is returned. Layer 2 failure (an embedding call) is logged
// and swallowed — recall is opportunistic and degrades to symbolic.
//
// The embedding layer reads the live index via a single atomic load
// (s.cur.Load) and never takes a lock the indexer holds (I1). It runs the
// coarse→fine algorithm (design §4.1): a coarse thread-selection pass,
// a fine chunk pass over the selected threads, and an engaged-thread
// intra-thread pass that bypasses the coarse gate (step 3).
//
// Ranking (v0.1, provisional — calibration will tune it): results where
// the embedding layer fired rank above symbolic-only results, each group
// ordered by its layer score. Score carries the embedding cosine when
// layer 2 fired, else the symbolic Jaccard score.
func (s *Service) Recall(ctx context.Context, req Request) ([]Result, error) {
	merged := map[string]*Result{}

	symbolic, err := s.ops.ProposeRecall(ctx, req.QuerySymbols, memops.RecallOptions{
		Project: req.Project,
		Exclude: req.Exclude,
	})
	if err != nil {
		return nil, fmt.Errorf("recall: symbolic: %w", err)
	}
	for _, c := range symbolic {
		merged[c.ThreadID] = &Result{
			ThreadID: c.ThreadID,
			Score:    c.Score,
			Symbolic: &SymbolicHit{Score: c.Score, MatchedSymbols: c.MatchedSymbols},
		}
	}

	q := s.embedQuery(ctx, req.QueryText)
	if q != nil {
		snap := s.cur.Load() // I1: one atomic load, no read-path lock

		for _, c := range s.coarseFine(q, snap, req.Exclude) {
			r := merged[c.ThreadID]
			if r == nil {
				r = &Result{ThreadID: c.ThreadID}
				merged[c.ThreadID] = r
			}
			r.Embedding = &EmbeddingHit{Score: c.Score}
			r.Score = c.Score // embedding score dominates the unified rank
		}

		if hit := s.intraThread(q, snap, req.Engaged); hit != nil {
			r := merged[req.Engaged]
			if r == nil {
				r = &Result{ThreadID: req.Engaged}
				merged[req.Engaged] = r
			}
			r.IntraThread = hit
			if hit.Score > r.Score {
				r.Score = hit.Score
			}
		}
	}

	out := make([]Result, 0, len(merged))
	for _, r := range merged {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ie := out[i].Embedding != nil || out[i].IntraThread != nil
		je := out[j].Embedding != nil || out[j].IntraThread != nil
		if ie != je {
			return ie // embedding/intra-thread results first
		}
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ThreadID < out[j].ThreadID
	})
	return out, nil
}

// embedQuery vectorizes the turn query for the embedding layer. Returns
// nil (no embedding pass) when no embedder or an empty query, and on an
// embedding-call failure, which it logs and swallows.
func (s *Service) embedQuery(ctx context.Context, queryText string) []float64 {
	if s.embedder == nil || queryText == "" {
		return nil
	}
	vecs, err := s.embedder.Embed(ctx, []string{truncateForEmbed(queryText)})
	if err != nil || len(vecs) != 1 {
		if err == nil {
			err = fmt.Errorf("embedder returned %d vectors for 1 input", len(vecs))
		}
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "embed-error", err.Error())
		return nil
	}
	return vecs[0]
}

// coarseFine runs the §4.1 coarse pass (thread selection, top-Kc) then,
// for each selected thread, the fine pass over its chunks. The returned
// candidates are thread-level embedding hits scored by the best chunk
// when the thread has fine chunks, else by the coarse cosine — so a
// thread with no indexed chunks still surfaces at thread granularity
// exactly as before (I8). Excluded threads are dropped at the thread
// level (the existing Exclude semantics).
func (s *Service) coarseFine(q []float64, snap *indexSnapshot, exclude map[string]struct{}) []scoring.EmbeddingCandidate {
	coarse := scoring.ProposeEmbedding(q, snap.coarse, scoring.EmbeddingOptions{
		Exclude: exclude,
		Limit:   Kc,
	})
	if len(snap.fine) == 0 {
		return coarse // no fine tier yet — coarse hits stand as thread-level embedding hits
	}
	out := make([]scoring.EmbeddingCandidate, len(coarse))
	for i, c := range coarse {
		out[i] = c
		chunks := scoring.ProposeChunks(q, snap.fine[c.ThreadID], scoring.ChunkOptions{Limit: Kf})
		if len(chunks) > 0 && chunks[0].Score > c.Score {
			out[i].Score = chunks[0].Score // refine the thread score by its best chunk
		}
	}
	return out
}

// intraThread runs the §4.1 step-3 engaged-thread pass: the fine pass
// directly over the engaged thread's chunk vectors, bypassing the coarse
// gate. Returns nil when no engaged thread, no fine chunks for it, or no
// chunk clears the threshold. The engaged thread's live FIFO-window
// excerpts are not in snap.fine (only scrolled-out chunks are indexed,
// I6), so the bypass is automatically window-excluded.
func (s *Service) intraThread(q []float64, snap *indexSnapshot, engaged string) *IntraThreadHit {
	if engaged == "" {
		return nil
	}
	chunks := scoring.ProposeChunks(q, snap.fine[engaged], scoring.ChunkOptions{Limit: Kf})
	if len(chunks) == 0 {
		return nil
	}
	turns := make([]int, len(chunks))
	for i, c := range chunks {
		turns[i] = c.TurnNumber
	}
	return &IntraThreadHit{Turns: turns, Score: chunks[0].Score}
}

// truncateForEmbed bounds text sent to the embedder. Byte truncation
// may clip a trailing multi-byte rune; embedding endpoints tolerate
// that, and thread bodies rarely reach the cap.
func truncateForEmbed(s string) string {
	if len(s) > maxEmbedChars {
		return s[:maxEmbedChars]
	}
	return s
}
