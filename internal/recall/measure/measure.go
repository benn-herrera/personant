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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/exact"
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

	// fineHash carries the per-chunk content-hash staleness keys parallel to
	// fine: fineHash[threadID][i] is the chunk_hash of fine[threadID][i] (the
	// same SHA-256 of the excerpt text the .vec stores, §5.3). It is the leaf
	// bottom-out of the summary-tree childrenHash (#111 Inc C): a tree is
	// usable iff the composed hash of the CURRENT leaves' chunk hashes matches
	// the hash the tree was built against (treeHash). Reusing the existing
	// chunk_hash here is the DRY childrenHash leaf key — no new leaf hash is
	// introduced. Parallel to fine and carried through every swap/reconcile so
	// the read path can compose the staleness key with no canonical re-read.
	fineHash map[string][]string

	// tree is the per-thread summary-hierarchy index (#111 / design
	// within-thread-summary-hierarchy.md §9.2): one root SummaryNode per
	// thread whose leaves are exactly that thread's fine[threadID] chunk
	// vectors, used by the engaged-thread intra-pass to descend in O(log n)
	// instead of flat-scanning all of fine[engaged] (the §0 decay fix).
	// It is part of the immutable published index (same I1 atomic-swap
	// discipline as coarse/fine). nil/absent for a thread with no built tree.
	// The sleep-cycle builder (RebuildTrees) populates it; Prepare/Reconcile
	// loads persisted trees from the .tree sidecars (Inc C). A nil/absent tree
	// is the W8 signal: the intra-pass falls back to the flat scan.
	tree map[string]*scoring.SummaryNode

	// treeHash[threadID] is the composed childrenHash of the leaf set tree[
	// threadID] was built against — the staleness key (#111 Inc C §6). A tree
	// is usable iff treeHash[threadID] equals the hash composed from the
	// thread's CURRENT fineHash leaves (composeLeafHash): trimming/adding a
	// leaf moves the composed hash, marking the tree stale → flat fallback
	// (W8) until the next sleep cycle rebuilds and re-stamps it.
	treeHash map[string]string
}

// emptySnapshot is the published value before any thread is indexed and
// the value a nil-embedder Service leaves in place. Holding a non-nil
// snapshot lets Recall load-and-scan unconditionally without a nil check.
func emptySnapshot() *indexSnapshot {
	return &indexSnapshot{
		coarse:    nil,
		fine:      map[string][]scoring.ChunkVector{},
		fineHash:  map[string][]string{},
		watermark: map[string]int{},
		tree:      map[string]*scoring.SummaryNode{},
		treeHash:  map[string]string{},
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
	// Sweep drops .vec AND .tree files for threads no longer on the spine
	// (archived / removed) and compacts the manifest to the live set (the
	// §6.4 Consolidate sleep-cycle hook). Lazy-safe: startup Reconcile also
	// ignores stale entries, so a missed Sweep only wastes disk, never
	// correctness.
	Sweep(ctx context.Context, liveThreadIDs []string) error

	// WriteTree persists one thread's summary tree to its .tree sidecar
	// (#111 Inc C §6) — the write-through the sleep-cycle RebuildTrees calls
	// after publishing a rebuilt tree. rootHash is the composed childrenHash
	// the tree was built against (the staleness key on reload); leafHashByTurn
	// maps each leaf turn number to its chunk_hash (the existing .vec key,
	// DRY) so the writer can compose every node's childrenHash. Leaf vectors
	// are NOT written (they live in the .vec — DRY); the sidecar holds the
	// topology, internal-node summary vectors, leaf turn numbers, and the
	// per-node childrenHash. A nil tree removes any stale sidecar.
	WriteTree(ctx context.Context, threadID string, tree *scoring.SummaryNode, rootHash string, leafHashByTurn map[int]string) error

	// LoadTrees loads the persisted .tree sidecars for the given threads,
	// rehydrating each into a *scoring.SummaryNode (leaf vectors looked up
	// from the supplied fine tier by turn number — the .vec is the leaf
	// source of truth, DRY) plus its stored root childrenHash. A tree whose
	// root childrenHash still matches its leaves loads usable immediately
	// (descent active from session start, no rebuild); a stale/absent one is
	// simply omitted, so the thread flat-falls-back (W8) until the next sleep
	// cycle rebuilds it. Best-effort: a malformed/missing sidecar is skipped,
	// never fatal (the tree is derived — rebuildable, W5).
	LoadTrees(ctx context.Context, fine map[string][]scoring.ChunkVector) (trees map[string]*scoring.SummaryNode, hashes map[string]string)
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
	// EngagedDebtWindow bounds the §3.4 recall-completeness lexical pass
	// (#123): the maximum number of the engaged thread's most-recent
	// scrolled-out excerpts that may be awaiting their fine-tier embedding
	// flush — the runtime's §6.5 debt CAP, not the instantaneous debt depth.
	// When > 0, the intra-thread pass adds a BOUNDED lexical scan over up to
	// that many recent scrolled-out excerpts (loaded on demand) so durable
	// content not yet embedded stays findable — the completeness floor that
	// closes the async-flush lag dead zone.
	//
	// It is the CAP, not the live count, on purpose: a debt-cap flush resets
	// the live counter to 0 and embeds asynchronously, so a live-count bound
	// would re-open the dead zone for exactly the cap-sized batch during the
	// flush's in-flight window. Bounding by the cap guarantees the lexical
	// floor always covers the whole possibly-unflushed tail, continuously. The
	// redundant overlap with already-flushed excerpts is harmless (≤ cap short
	// excerpts, de-duped in the union). 0 → no debt pass (byte-identical prior
	// behaviour). Sourced from the runtime's own debt-cap constant.
	EngagedDebtWindow int
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

	// cosineOps / recallQueries are the MEASURED recall_query_cosine_ops
	// instrument (design within-thread-summary-hierarchy.md §7.2): the
	// run-total count of cosine comparisons the embedding recall path
	// actually performed, and the number of embedding-recall queries that
	// performed them. The per-query average (cosineOps/recallQueries) is the
	// headline perf-bend metric — empirical (counted at the scoring call
	// sites via scoring.CosineCounter), NOT a formula. Bumped once per Recall
	// query that runs the embedding pass; read by the harness via CosineOps /
	// RecallQueries. Atomic because Recall may be called concurrently in
	// principle (the read path takes no lock, I1) even though the sim drives
	// it one turn at a time.
	cosineOps     atomic.Int64
	recallQueries atomic.Int64

	// w1DiagStrictMiss / w1DiagTie / w1DiagTreeMismatch are the
	// PER-INSTANCE classification tally of W1 descent-vs-flat divergences
	// (the #111 diagnostic — design within-thread-summary-hierarchy.md §7.1).
	// Each divergent probe (descent top-Kf != flat top-Kf) is classified once
	// and bumps exactly one of these (see classifyW1Divergence). They are
	// pure observation: never gate, never alter recall. On the mock run no
	// divergence occurs (W1==0 by construction), so all three stay 0 and no
	// diagnostic line is emitted. Their ONLY consumer is the per-instance
	// recall.W1-diag-tally line emitted on Close — useful forensics beside the
	// per-probe recall.W1-diag lines for the threads this one Service handled.
	//
	// They are NOT the run-total: the harness rebuilds the Service per session
	// (every RestartSession installs a fresh one with these zeroed), so the
	// authoritative run-total W1 classification is accumulated by the harness
	// at the per-probe call site (recall_intra_w1_* counters), in lockstep with
	// recall_intra_descent_divergence, from the class IntraThreadDivergence
	// returns. Do NOT re-add a post-run accessor that reads these as a run
	// total — that two-instance mismatch was the #111 bug.
	w1DiagStrictMiss   atomic.Int64
	w1DiagTie          atomic.Int64
	w1DiagTreeMismatch atomic.Int64

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
		// Load the persisted summary trees beside the reconciled leaves
		// (#111 Inc C): a .tree whose root childrenHash matches its (cache-
		// loaded) leaves is usable immediately — descent is active from
		// session start with no rebuild. A stale/absent tree is omitted, so
		// the thread flat-falls-back (W8) until the next sleep-cycle
		// RebuildTrees rebuilds and re-persists it. The reconciled snapshot
		// is not yet published, so attaching the trees here is race-free.
		reconciled.tree, reconciled.treeHash = s.cache.LoadTrees(ctx, reconciled.fine)
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
		// Per-run W1 diagnostic tally (#111 §7.1) — the headline. Emitted
		// once, only when at least one divergence was classified, so the mock
		// run (no embedder, no divergence) writes nothing. Greppable in logs/
		// as recall.W1-diag-tally beside the per-probe recall.W1-diag lines.
		sm, tie, tm := s.w1DiagStrictMiss.Load(), s.w1DiagTie.Load(), s.w1DiagTreeMismatch.Load()
		if sm+tie+tm > 0 {
			_ = s.ops.Log(context.Background(), memops.LogCategoryRecall, "W1-diag-tally",
				fmt.Sprintf("strict_miss=%d tie=%d tree_mismatch=%d", sm, tie, tm))
		}
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

	// INCREMENTAL FLUSH (#102). A turn-excerpt is immutable once written, so
	// its content hash is stable across flushes: once a chunk is indexed, a
	// later flush must REUSE its existing vector rather than re-embed it.
	// Re-embedding the whole thread on every flush is O(flushes × thread_size)
	// — the cost that grew ~2.1× from 14d→30d on a continuously-active thread.
	//
	// The prior indexed state is the live snapshot's fine/fineHash for this
	// thread: it is authoritative (those vectors were embedded by a past flush
	// or loaded from the .vec cache, which the cache built from an embed) and
	// lock-free to read (the single indexer goroutine is the only writer, so a
	// load here cannot race a swap). Key reuse on (turnNumber, contentHash):
	// only an unchanged chunk at an existing turn matches, so a changed-text
	// chunk or a new turn falls through to EMBED.
	reuse := s.priorChunkVectors(job.threadID)

	bodyText := truncateForEmbed(thr.Body)
	// The coarse body legitimately changes as the thread grows (it is the
	// truncated whole-body), so it is always re-embedded — one call per flush,
	// O(1). It rides slot -1 of the embed batch (index 0 below).
	texts := []string{bodyText}
	chunks := make([]scoring.ChunkVector, len(excerpts))
	chunkHashes := make([]string, len(excerpts))
	var embedSlot []int // index into chunks for each text beyond the coarse slot
	for i, ex := range excerpts {
		h := contentHash(ex.Text)
		chunkHashes[i] = h
		chunks[i].TurnNumber = ex.TurnNumber
		if v, ok := reuse[chunkKey{turn: ex.TurnNumber, hash: h}]; ok {
			chunks[i].Vector = v // REUSE: prior snapshot already embedded this exact chunk
			continue
		}
		// EMBED: new turn, changed text, or a chunk the prior snapshot is
		// missing (fall back to embedding — never serve a stale/missing vector).
		texts = append(texts, truncateForEmbed(ex.Text))
		embedSlot = append(embedSlot, i)
	}

	// One batched embed call for the coarse body + the EMBED-partition chunks
	// (design §6.1 batch efficiency, now over the delta only).
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
	for j, slot := range embedSlot {
		chunks[slot].Vector = vecs[j+1] // +1 skips the coarse slot at index 0
	}

	s.swap(job.threadID, job.dispatchTurncount, coarse, chunks, chunkHashes)

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

// chunkKey identifies a fine-tier chunk for reuse lookup: a turn-excerpt is
// immutable once written, so (turn number, content hash) uniquely pins one
// embedded chunk. The hash guards against the rare case of a turn's text
// changing — a hash mismatch at an existing turn falls through to re-embed.
type chunkKey struct {
	turn int
	hash string
}

// priorChunkVectors builds the reuse lookup for an incremental flush (#102):
// (turn, content hash) → the vector the live snapshot already holds for that
// chunk. The snapshot's fine[threadID] and fineHash[threadID] are parallel
// (built together at every swap), so they zip into the keyed map. Reading the
// live snapshot here is race-free: the single indexer goroutine is the sole
// writer of s.cur, so this load (on that goroutine) cannot observe a torn or
// concurrently-mutated snapshot. A missing/empty prior entry (first flush of a
// thread) yields an empty map → every chunk embeds, the from-scratch path.
//
// A length mismatch between fine and fineHash (which should never happen — the
// swap always writes them together) degrades safely to no reuse: any chunk it
// cannot confidently key is simply re-embedded.
func (s *Service) priorChunkVectors(threadID string) map[chunkKey][]float64 {
	snap := s.cur.Load()
	priorChunks := snap.fine[threadID]
	priorHashes := snap.fineHash[threadID]
	if len(priorChunks) != len(priorHashes) {
		return nil // defensive: cannot key reliably → re-embed everything
	}
	out := make(map[chunkKey][]float64, len(priorChunks))
	for i, cv := range priorChunks {
		out[chunkKey{turn: cv.TurnNumber, hash: priorHashes[i]}] = cv.Vector
	}
	return out
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
func (s *Service) swap(threadID string, dispatchTurncount int, coarse scoring.ThreadVector, chunks []scoring.ChunkVector, chunkHashes []string) {
	for {
		old := s.cur.Load()
		if dispatchTurncount <= old.watermark[threadID] {
			return // stale embed — a fresher vector already published (I2/F5)
		}
		next := old.with(threadID, coarse, chunks, chunkHashes, dispatchTurncount)
		if s.cur.CompareAndSwap(old, next) {
			return
		}
	}
}

// with returns a new snapshot equal to s but with threadID's coarse
// vector, chunk vectors, and watermark replaced. The receiver is not
// mutated (snapshots are immutable once published). The maps are copied;
// the coarse slice is rebuilt with the one entry replaced or appended.
func (s *indexSnapshot) with(threadID string, coarse scoring.ThreadVector, chunks []scoring.ChunkVector, chunkHashes []string, watermark int) *indexSnapshot {
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
	newFineHash := make(map[string][]string, len(s.fineHash)+1)
	for id, h := range s.fineHash {
		newFineHash[id] = h
	}
	if len(chunks) == 0 {
		delete(newFine, threadID)
		delete(newFineHash, threadID)
	} else {
		newFine[threadID] = chunks
		newFineHash[threadID] = chunkHashes
	}

	newWM := make(map[string]int, len(s.watermark)+1)
	for id, w := range s.watermark {
		newWM[id] = w
	}
	newWM[threadID] = watermark

	// Carry the tree maps forward unchanged. The indexer does not build or
	// invalidate trees (the sleep-cycle builder owns them); a fine-tier swap
	// here may leave a stale tree for threadID, which treeUsable handles — a
	// tree whose stored treeHash no longer matches the composed hash of the
	// CURRENT fineHash[threadID] leaves falls back to the flat scan (W8) until
	// the next sleep cycle rebuilds and re-stamps it. nil chunkHashes (an
	// empty fine entry was just deleted) leaves no leaf hashes, so any prior
	// tree for it is unconditionally stale on the next read.
	newTree := make(map[string]*scoring.SummaryNode, len(s.tree))
	for id, t := range s.tree {
		newTree[id] = t
	}
	newTreeHash := make(map[string]string, len(s.treeHash))
	for id, h := range s.treeHash {
		newTreeHash[id] = h
	}

	return &indexSnapshot{
		coarse:    newCoarse,
		fine:      newFine,
		fineHash:  newFineHash,
		watermark: newWM,
		tree:      newTree,
		treeHash:  newTreeHash,
	}
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

		// MEASURED recall_query_cosine_ops (§7.2): one counter per query,
		// threaded into both the coarse→fine population pass and the engaged-
		// thread intra pass, so it tallies the ACTUAL cosine comparisons —
		// coarse over T vectors + the population fine pass + the engaged path
		// (flat O(C_main) scan OR k·B·log_B(n) descent). Accumulated into the
		// run total after the query so the perf bend is empirical.
		counter := &scoring.CosineCounter{}

		for _, c := range s.coarseFine(q, snap, req.Exclude, counter) {
			r := merged[c.ThreadID]
			if r == nil {
				r = &Result{ThreadID: c.ThreadID}
				merged[c.ThreadID] = r
			}
			r.Embedding = &EmbeddingHit{Score: c.Score}
			r.Score = c.Score // embedding score dominates the unified rank
		}

		// netCapHits is the #111 Finding A backstop signal: the relevance-
		// sized net (top-Kf OR all clearly-related leaves) is normally bounded
		// by genuine relevance density, but a pathological dense cluster could
		// blow past scoring.NetCap; when that truncation drops a clearly-
		// related candidate, the scorer bumps this and we log it once so the
		// degenerate tail is VISIBLE (it is never the normal path).
		netCapHits := 0
		if hit := s.intraThread(q, snap, req.Engaged, counter, &netCapHits); hit != nil {
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
		if netCapHits > 0 {
			_ = s.ops.Log(ctx, memops.LogCategoryRecall, "net-cap-hit",
				fmt.Sprintf("thread=%s hits=%d cap=%d", req.Engaged, netCapHits, scoring.NetCap))
		}

		// Accumulate the MEASURED cosine-op tally for this query into the run
		// total (§7.2). One query that ran the embedding pass → one increment
		// of recallQueries; the per-query average is cosineOps/recallQueries.
		s.cosineOps.Add(int64(counter.Ops()))
		s.recallQueries.Add(1)
	}

	// §3.4 recall-completeness floor (#123). The fine embedding tier lags
	// scroll-out by up to the §6.5 debt cap: durable content scrolled out of
	// the assembly window but not yet flushed to the fine tier is invisible to
	// the embedding intra-pass above (it has no vector yet) — a dead-zone the
	// invariant forbids. Close it with a BOUNDED, embedder-independent LEXICAL
	// pass over exactly the engaged thread's debt-window excerpts (≤ debt cap,
	// loaded on demand) and union the matched turns into the intra hit. Runs
	// outside the q != nil block on purpose: completeness must not depend on a
	// query embedding being produced. No-op unless there is an engaged thread,
	// known debt, and query symbols to match against.
	if turns := s.debtWindowTurns(ctx, req.Engaged, req.EngagedDebtWindow, req.QuerySymbols); len(turns) > 0 {
		r := merged[req.Engaged]
		if r == nil {
			r = &Result{ThreadID: req.Engaged}
			merged[req.Engaged] = r
		}
		r.IntraThread = unionIntraTurns(r.IntraThread, turns)
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
func (s *Service) coarseFine(q []float64, snap *indexSnapshot, exclude map[string]struct{}, counter *scoring.CosineCounter) []scoring.EmbeddingCandidate {
	coarse := scoring.ProposeEmbedding(q, snap.coarse, scoring.EmbeddingOptions{
		Exclude: exclude,
		Limit:   Kc,
		Counter: counter,
	})
	if len(snap.fine) == 0 {
		return coarse // no fine tier yet — coarse hits stand as thread-level embedding hits
	}
	out := make([]scoring.EmbeddingCandidate, len(coarse))
	for i, c := range coarse {
		out[i] = c
		chunks := scoring.ProposeChunks(q, snap.fine[c.ThreadID], scoring.ChunkOptions{Limit: Kf, Counter: counter})
		if len(chunks) > 0 && chunks[0].Score > c.Score {
			out[i].Score = chunks[0].Score // refine the thread score by its best chunk
		}
	}
	return out
}

// ExhaustiveIntraScan is the §3.4 SEMANTIC-EXACT tier (#117): a full,
// UNBOUNDED flat cosine scan over EVERY indexed chunk of one thread. It is
// the exhaustive counterpart to the approximate within-thread summary-tree
// descent (#111) — it answers "find EVERYTHING we discussed about X" where X
// is paraphrased and COMPLETENESS matters, the case the approximate O(log n)
// tree deliberately trades away for speed. Expensive and rare by design; the
// future routing layer invokes it only when a query is classified
// exhaustive-semantic (that LLM query-class routing is DEFERRED — not this
// method's job).
//
// It embeds the query (the same embedQuery the production read path uses),
// loads the live index snapshot (one atomic load, I1), and runs the flat
// scan over ALL of snap.fine[threadID] with NO top-Kf truncation — distinct
// from coarseFine/intraThread, which cap at Kf, and distinct from the
// summary-tree descent, which it must NOT use (the tree is the approximate
// path; this is the exact O(n) scan by definition). It does NOT bypass via
// the engaged-thread tree even when one exists.
//
// threshold is the minimum cosine; 0 → scoring.DefaultCosineThreshold. The
// returned candidates are ordered score-desc then turn-number-asc, reusing
// scoring.ScanChunks for the cosine math, sort, and threshold filter — every
// candidate at or above threshold, nothing dropped.
//
// The exact tier deliberately bypasses the approximate-recall net policy
// (scoring.capNet / scoring.NetCap, which ProposeChunks applies) by calling
// ScanChunks directly: NetCap is the approximate path's pathological-density
// backstop, and inheriting it would silently truncate a dense thread and
// break the exhaustive contract this tier exists to honor.
func (s *Service) ExhaustiveIntraScan(ctx context.Context, queryText, threadID string, threshold float64) []scoring.ChunkCandidate {
	q := s.embedQuery(ctx, queryText)
	if q == nil {
		return nil
	}
	return exhaustiveIntraScan(q, s.cur.Load(), threadID, threshold)
}

// exhaustiveIntraScan is the pure scan half of ExhaustiveIntraScan, split out
// so it can be driven directly from an injected snapshot with a controlled
// query vector (the deterministic unit test) without a live embedder. It runs
// the unbounded flat scoring.ScanChunks over the thread's fine tier — NEVER
// the summary-tree descent (the approximate path) and NEVER ProposeChunks
// (which would apply capNet/NetCap). An absent/empty fine tier yields nil
// (not an error): a thread with nothing indexed has nothing exhaustive to
// return.
func exhaustiveIntraScan(q []float64, snap *indexSnapshot, threadID string, threshold float64) []scoring.ChunkCandidate {
	leaves := snap.fine[threadID]
	if len(leaves) == 0 {
		return nil
	}
	return scoring.ScanChunks(q, leaves, threshold, nil)
}

// intraThread runs the §4.1 step-3 engaged-thread pass: directly over the
// engaged thread's scrolled-out chunks, bypassing the coarse gate. Returns
// nil when no engaged thread, no fine chunks for it, or no chunk clears the
// threshold. The engaged thread's live FIFO-window excerpts are not in
// snap.fine (only scrolled-out chunks are indexed, I6), so the bypass is
// automatically window-excluded.
//
// It chooses between two paths (design §4 / W8): the O(log n) summary-tree
// descent when a usable tree is present, else the flat O(C_main)
// ProposeChunks scan over all of snap.fine[engaged]. See intraChunks for
// the path-selection rule.
func (s *Service) intraThread(q []float64, snap *indexSnapshot, engaged string, counter *scoring.CosineCounter, netCapHits *int) *IntraThreadHit {
	if engaged == "" {
		return nil
	}
	chunks := s.intraChunks(q, snap, engaged, counter, netCapHits)
	if len(chunks) == 0 {
		return nil
	}
	turns := make([]int, len(chunks))
	for i, c := range chunks {
		turns[i] = c.TurnNumber
	}
	return &IntraThreadHit{Turns: turns, Score: chunks[0].Score}
}

// debtWindowTurns is the §3.4 recall-completeness floor (#123): a BOUNDED
// LEXICAL pass over the engaged thread's embedding-debt window — the recent
// scrolled-out excerpts not yet flushed to the fine embedding tier — returning
// the turn numbers whose text lexically matches any query symbol. The union of
// these with the fine-tier intra hit guarantees durable content is findable
// even during the async-flush lag, with no dead zone (completeness is
// continuous, not eventual).
//
// Bounded by construction: it loads at most window (the §6.5 debt cap)
// excerpts via LoadDebtWindowExcerpts — a small, fixed-bound disk read that
// does NOT grow with thread age — then lexically matches them. No embedding is
// performed (the debt tail has no vectors yet; that is the gap), so this adds
// no per-query embedding cost and no unbounded scan. A load error is logged
// and swallowed: the completeness floor is opportunistic and must never abort
// recall. Returns nil unless there is an engaged thread, a positive window
// bound, and query symbols.
func (s *Service) debtWindowTurns(ctx context.Context, engaged string, window int, symbols []string) []int {
	if engaged == "" || window <= 0 || len(symbols) == 0 {
		return nil
	}
	excerpts, err := s.ops.LoadDebtWindowExcerpts(ctx, engaged, window)
	if err != nil {
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "debt-window-error",
			fmt.Sprintf("thread=%s window=%d err=%v", engaged, window, err))
		return nil
	}
	return exact.MatchExcerptsBySymbols(excerpts, symbols)
}

// unionIntraTurns merges the lexical debt-window turns into the (possibly nil)
// embedding intra hit, producing the combined IntraThreadHit the caller stores.
// When there is no prior embedding hit, the lexical turns stand alone with a
// zero Score — they are a completeness signal, not a ranked cosine match, so
// they surface the thread without claiming an embedding similarity. When an
// embedding hit exists, its Score is preserved (the embedding match is the
// stronger relevance signal) and the turn sets are merged, sorted, and
// de-duplicated.
func unionIntraTurns(prior *IntraThreadHit, lexicalTurns []int) *IntraThreadHit {
	seen := make(map[int]struct{}, len(lexicalTurns))
	var turns []int
	score := 0.0
	if prior != nil {
		score = prior.Score
		for _, t := range prior.Turns {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				turns = append(turns, t)
			}
		}
	}
	for _, t := range lexicalTurns {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			turns = append(turns, t)
		}
	}
	sort.Ints(turns)
	return &IntraThreadHit{Turns: turns, Score: score}
}

// intraChunks returns the engaged thread's top-Kf chunk candidates, taking
// the summary-tree descent when usable and the flat scan otherwise. It is
// the single point where the descent-vs-fallback decision lives, so the
// divergence hook (intraThreadDivergence) can ask "which path would
// production take" without duplicating the rule.
//
// Descent is taken iff the tree is present AND the thread has at least
// scoring.TreeBuildThreshold leaves AND the tree is not stale (W8: a short
// thread, an absent tree, or a stale tree all fall back to the flat scan —
// a tree is an optimization layered over a still-correct flat path). Tree
// staleness is the leaf-count mismatch check (treeUsable): a fuller
// childrenHash reconcile is Inc C's, but a tree whose leaf count no longer
// matches snap.fine[engaged] cannot be apples-to-apples with the flat scan,
// so it falls back.
//
// Because NOTHING builds trees in this increment (Inc D's sleep cycle
// does), snap.tree is always empty at runtime and this always takes the
// flat-scan branch — byte-identical to the pre-#111 behaviour. The descent
// branch is reached only by tests that install a tree.
func (s *Service) intraChunks(q []float64, snap *indexSnapshot, engaged string, counter *scoring.CosineCounter, netCapHits *int) []scoring.ChunkCandidate {
	leaves := snap.fine[engaged]
	if snap.treeUsable(engaged) {
		return scoring.DescendChunks(q, snap.tree[engaged], scoring.DescendOptions{Limit: Kf, Counter: counter, NetCapHits: netCapHits})
	}
	return scoring.ProposeChunks(q, leaves, scoring.ChunkOptions{Limit: Kf, Counter: counter, NetCapHits: netCapHits})
}

// treeUsable reports whether the engaged thread's summary tree should drive
// the intra-pass (design §4 fallback predicate / W8). It returns false — the
// caller flat-scans — for any of:
//
//   - no tree built for the thread (nil entry);
//   - a thread below scoring.TreeBuildThreshold leaves (a short thread the
//     flat scan handles cheaply, a tree is pure overhead);
//   - a STALE tree (#111 Inc C §6 childrenHash key): the tree's stored
//     treeHash — the composed childrenHash of the leaf set it was built
//     against — no longer equals the hash composed from the thread's CURRENT
//     leaf chunk hashes. Adding, trimming, or changing a leaf moves the
//     composed hash, so the tree is stale until the next sleep cycle rebuilds
//     and re-stamps it (G8).
//
// The staleness key reuses the existing per-chunk chunk_hash as the leaf
// bottom-out (composeLeafHash), composing it into one root hash — the DRY
// childrenHash: no new leaf hash is introduced (§6).
func (s *indexSnapshot) treeUsable(threadID string) bool {
	tree := s.tree[threadID]
	if tree == nil || len(s.fine[threadID]) < scoring.TreeBuildThreshold {
		return false
	}
	return s.treeHash[threadID] == composeLeafHash(s.fineHash[threadID])
}

// composeLeafHash composes a thread's leaf chunk hashes into the root
// childrenHash staleness key (#111 Inc C §6). The leaf bottom-out is the
// existing chunk_hash (DRY — the .vec/snapshot already carry it, no new leaf
// hash); the root key is the SHA-256 of the chunk hashes joined in their
// canonical turn order (fineHash is parallel to fine, which is turn-ordered).
// Internal-node childrenHashes (composed up the topology) are persisted in
// the .tree for subtree-level reconcile; the read-path staleness check needs
// only the root composition, which is recomputable from the current leaves
// with no topology — exactly so trim/add invalidates it without the tree.
//
// composeNodeHash is the building block both this and the .tree writer use: a
// node's childrenHash = SHA-256 over its ordered children's hashes.
func composeLeafHash(leafHashes []string) string {
	return composeNodeHash(leafHashes)
}

// composeNodeHash hashes an ordered list of child hashes into one parent
// childrenHash (#111 Inc C §6). A length prefix per child makes the
// composition unambiguous (no two child splits collide). An empty list
// hashes the empty input — a stable, distinct key for a childless node.
func composeNodeHash(childHashes []string) string {
	h := sha256.New()
	for _, ch := range childHashes {
		fmt.Fprintf(h, "%d:%s", len(ch), ch)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// intraThreadDivergence is the W1 recall-preservation differential (design
// §7.1, the §7 gate mechanism). It runs BOTH the tree descent and the flat
// scan over the SAME engaged-thread leaves and returns the size of the
// symmetric set difference between their top-Kf turn sets — the metric
// recall_intra_descent_divergence the sim/harness REPORTS as an
// approximation-drift quality measure (#119; the within-thread tree is an
// approximate O(log n) heuristic, not the exact-recall tier #117).
//
// This is a measurement-only seam: production recall (intraChunks, called
// from Recall) runs only the single chosen path and never pays the double
// cost. The harness calls this explicitly when it wants the differential.
//
// The comparison is apples-to-apples by construction: the tree's leaves are
// exactly snap.fine[engaged] (treeUsable's childrenHash key enforces that the
// tree was built against the current leaf set), so both paths consume the
// identical leaf vectors and the only difference is
// which leaves the beam reached. When no usable tree exists, descent is not
// run and divergence is 0 by definition (there is no fast path to diverge
// from — the flat scan IS the result). A nonzero return is the
// approximation-drift signal (#119): the heuristic substituted a within-Kf
// leaf. A strict_miss is a real ranking defect worth raising the beam for; a
// tie/tree_mismatch is a sub-perceptible boundary effect. Exact/exhaustive
// recall is the separate #117 tier, not this approximate path.
// It returns the divergence size and, when div>0, the per-probe
// classification (one of w1StrictMiss / w1Tie / w1TreeMismatch); div==0
// returns the empty class. The class is surfaced — not just bumped on the
// Service atomic — so the harness can accumulate the run-total classification
// at the SAME call site and moment it accumulates the divergence counter,
// keeping the two structurally consistent across the per-session Service
// instance churn a RestartSession step causes (a fresh Service has zeroed
// atomics; the harness-held counters survive the swap).
func (s *Service) intraThreadDivergence(ctx context.Context, q []float64, snap *indexSnapshot, engaged string) (int, w1Class) {
	if engaged == "" {
		return 0, ""
	}
	leaves := snap.fine[engaged]
	if !snap.treeUsable(engaged) {
		return 0, "" // no fast path → nothing to diverge from (W8)
	}
	descent := scoring.DescendChunks(q, snap.tree[engaged], scoring.DescendOptions{Limit: Kf})
	flat := scoring.ProposeChunks(q, leaves, scoring.ChunkOptions{Limit: Kf})
	div := turnSetDifference(descent, flat)
	if div == 0 {
		return 0, ""
	}
	// DIAGNOSTIC ONLY (#111 §7.1): classify and log the per-probe detail
	// so a live run's divergences can be read out of logs/. Pure
	// observation — it does not touch div, the gate, or recall. emitW1Diag
	// returns the class so the caller can accumulate it without reclassifying.
	class := s.emitW1Diag(ctx, snap.tree[engaged], descent, flat)
	return div, class
}

// w1Epsilon is the cosine-equality tolerance for the W1 divergence
// classification (#111 §7.1 diagnostic). Two leaf cosines within this of
// each other are treated as EQUAL — a tie-boundary substitution, not a
// recall loss. nomic embeddings are float64 and cosine arithmetic carries
// rounding noise well below this; 1e-9 is loose enough to absorb that noise
// yet tight enough that a genuine ranking gap (a strict-miss) never reads as
// a tie. It classifies a measurement, never gates.
const w1Epsilon = 1e-9

// w1Class is the diagnostic classification of one W1 divergence (#111
// §7.1). It says whether the divergence is a genuine recall loss or a
// benign equal-cosine substitution — the empirical question the
// instrumentation settles.
type w1Class string

const (
	// w1StrictMiss: the best leaf the flat scan ranked but descent missed
	// has STRICTLY higher cosine (beyond w1Epsilon) than the best leaf
	// descent substituted for it — a real recall loss (a better leaf was
	// pruned). Fixable by better keys / beam (F-A), not a gate refinement.
	w1StrictMiss w1Class = "strict-miss"
	// w1Tie: the best missed leaf and the best substituted leaf are within
	// w1Epsilon — descent picked a DIFFERENT leaf of EQUAL cosine at the
	// top-Kf cut. Not lost recall; fixable by a gate-correctness refinement
	// (a deterministic tie-break), not by better keys.
	w1Tie w1Class = "tie"
	// w1TreeMismatch: a missed leaf is not present among the tree's leaves
	// at all — the flat scan ranked a leaf the descent tree does not
	// contain. A staleness/build edge (the tree's leaf set drifted from
	// snap.fine), not a key-quality or tie issue.
	w1TreeMismatch w1Class = "tree-mismatch"
)

// classifyW1Divergence classifies one W1 divergence from the missed set
// (in flat top-Kf, not in descent) and the substituted set (in descent,
// not in flat), given the set of turn numbers the tree actually contains.
// It is pure and unit-tested: tree-mismatch first (a missed leaf absent
// from the tree is a build/staleness edge, distinct from any cosine
// comparison), then the best-missed-vs-best-substituted cosine comparison
// at tolerance eps (strict-miss when missed is strictly higher, else tie).
//
// Each candidate carries its own cosine in Score (the terminal
// ProposeChunks fills it for both paths over the same operating point), so
// no cosine is recomputed here — the classification reads the scores the
// gate already produced. If descent returned fewer leaves (empty substituted
// set) and the missed leaf has a real positive cosine, bestSub is 0 and the
// row is a strict-miss — descent dropped a leaf the flat scan surfaced and
// put nothing in its place, a genuine loss.
func classifyW1Divergence(missed, substituted []scoring.ChunkCandidate, treeTurns map[int]struct{}, eps float64) w1Class {
	for _, m := range missed {
		if _, ok := treeTurns[m.TurnNumber]; !ok {
			return w1TreeMismatch
		}
	}
	bestMissed := bestScore(missed)
	bestSub := bestScore(substituted)
	if bestMissed > bestSub+eps {
		return w1StrictMiss
	}
	return w1Tie
}

// bestScore returns the highest cosine Score in a candidate set, or 0 for
// an empty set (the candidates are already threshold-filtered and >= 0).
func bestScore(cands []scoring.ChunkCandidate) float64 {
	best := 0.0
	for _, c := range cands {
		if c.Score > best {
			best = c.Score
		}
	}
	return best
}

// emitW1Diag computes the per-divergence detail, writes ONE greppable
// recall.W1-diag line to the substrate log (#111 §7.1 diagnostic), bumps the
// run tally on the Service atomic (classifyW1Divergence's verdict), and
// RETURNS the class so the caller can accumulate the run-total at the harness
// call site (the instance-churn-proof reader; see intraThreadDivergence).
// Called only on a divergent probe; pure observation, never gates. The log
// channel is the same memops.LogCategoryRecall the embed-error line uses, so
// logs/ can be grepped for `recall.W1-diag` after a live run.
//
// CONSISTENCY CONTRACT (#119 regression fix). The forensic LINE and the
// COUNT must never diverge: on a 1-month live rung the run tally reported
// strict_miss=1 but NO per-probe recall.W1-diag line reached logs/, so the
// missed-leaf cosines were unreadable — a real ranking defect could not be
// told from a sub-perceptible boundary tie. Root cause: the tally was bumped
// (and the class returned to the harness counter) BEFORE the log emission,
// which sat on a separate code path — skipped when s.ops == nil and silently
// `_`-swallowing any Log error. That is the mirror image of the d8e32b3
// tally-instance bug: that fix made the tally survive instance churn but left
// the line droppable. The fix here makes the LINE the gating action: it is
// built and emitted FIRST, and only on a successful emission (or the unit-test
// s.ops == nil path, which has no substrate by construction) is the tally
// bumped. A Log failure on a live Service is surfaced on the index-error
// channel, never swallowed, and leaves the count un-incremented so the tally
// and the readable forensics stay one-for-one. Tally and line now emit at one
// site, the structural-consistency approach the d8e32b3 fix used for the
// counter pair.
func (s *Service) emitW1Diag(ctx context.Context, tree *scoring.SummaryNode, descent, flat []scoring.ChunkCandidate) w1Class {
	descSet := turnSet(descent)
	flatSet := turnSet(flat)
	missed := diffCandidates(flat, descSet)         // in flat, not descent — lost
	substituted := diffCandidates(descent, flatSet) // in descent, not flat — gained

	treeTurns := make(map[int]struct{})
	for _, t := range scoring.TreeLeafTurns(tree) {
		treeTurns[t] = struct{}{}
	}
	class := classifyW1Divergence(missed, substituted, treeTurns, w1Epsilon)

	// Emit the forensic line FIRST, before bumping the tally, so every counted
	// divergence is guaranteed to have a readable per-probe line. s.ops == nil
	// is the unit-test construction (no substrate exists) — the only legitimate
	// no-log path; the tally still bumps there because the harness consumes the
	// returned class directly, not the log.
	if s.ops != nil {
		detail := fmt.Sprintf(
			"class=%s flat=%s descent=%s missed=%s substituted=%s",
			class,
			fmtCandidates(flat),
			fmtCandidates(descent),
			fmtCandidates(missed),
			fmtCandidates(substituted),
		)
		if err := s.ops.Log(ctx, memops.LogCategoryRecall, "W1-diag", detail); err != nil {
			// A live Service that cannot write its forensic line must not bump
			// the tally as if it had — that re-creates the count-without-line
			// divergence. Surface the failure on the index-error channel (best
			// effort) and return the empty class so the harness counter is not
			// incremented for an unreadable divergence.
			_ = s.ops.Log(ctx, memops.LogCategoryRecall, "index-error",
				fmt.Sprintf("W1-diag emit failed: %v", err))
			return ""
		}
	}

	switch class {
	case w1StrictMiss:
		s.w1DiagStrictMiss.Add(1)
	case w1Tie:
		s.w1DiagTie.Add(1)
	case w1TreeMismatch:
		s.w1DiagTreeMismatch.Add(1)
	}
	return class
}

// turnSet collects a candidate set's turn numbers into a lookup set.
func turnSet(cands []scoring.ChunkCandidate) map[int]struct{} {
	out := make(map[int]struct{}, len(cands))
	for _, c := range cands {
		out[c.TurnNumber] = struct{}{}
	}
	return out
}

// diffCandidates returns the candidates whose turn number is NOT in other —
// the set difference at candidate granularity (so each kept candidate
// retains its Score for the classification + log).
func diffCandidates(cands []scoring.ChunkCandidate, other map[int]struct{}) []scoring.ChunkCandidate {
	var out []scoring.ChunkCandidate
	for _, c := range cands {
		if _, ok := other[c.TurnNumber]; !ok {
			out = append(out, c)
		}
	}
	return out
}

// fmtCandidates renders a candidate set as a compact greppable
// "turn:cosine" list, e.g. "[412:0.7321 88:0.6904]". Empty → "[]".
func fmtCandidates(cands []scoring.ChunkCandidate) string {
	if len(cands) == 0 {
		return "[]"
	}
	b := make([]byte, 0, len(cands)*16)
	b = append(b, '[')
	for i, c := range cands {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, fmt.Sprintf("%d:%.4f", c.TurnNumber, c.Score)...)
	}
	b = append(b, ']')
	return string(b)
}

// turnSetDifference returns the number of turn numbers present in exactly
// one of the two candidate sets (the symmetric difference size). Ordering
// within top-Kf is irrelevant to W1 — only the leaf set matters (§7.1).
func turnSetDifference(a, b []scoring.ChunkCandidate) int {
	setA := make(map[int]struct{}, len(a))
	for _, c := range a {
		setA[c.TurnNumber] = struct{}{}
	}
	setB := make(map[int]struct{}, len(b))
	for _, c := range b {
		setB[c.TurnNumber] = struct{}{}
	}
	diff := 0
	for t := range setA {
		if _, ok := setB[t]; !ok {
			diff++
		}
	}
	for t := range setB {
		if _, ok := setA[t]; !ok {
			diff++
		}
	}
	return diff
}

// IntraThreadDivergence is the exported W1 recall-preservation measure seam
// (design §7.1) the sim/harness calls per intra-thread probe: embed the
// query, load the live index snapshot, and return the descent-vs-flat
// top-Kf set difference for the engaged thread. The sim REPORTS
// recall_intra_descent_divergence as an approximation-drift quality measure
// (#119; it is not gated to 0 — exact recall is the #117 tier).
//
// It mirrors Recall's read path — one atomic snapshot load (I1), the same
// query embedding — so the differential is measured against the exact index
// production recall would have used. Returns (0, "") when no embedder is
// configured, the query is empty, or no usable tree exists for the engaged
// thread (the W8 flat-only case, where there is no fast path to diverge
// from). Keeping the snapshot and tree private to measure, this is the only
// surface the harness needs.
//
// The second return is the per-probe classification ("strict-miss" / "tie" /
// "tree-mismatch", empty when div==0) as a plain string so the harness — in a
// different package, which cannot see the unexported w1Class — can accumulate
// the run-total class tally at the SAME call site it accumulates the
// divergence counter. That co-location is what makes the two metrics survive
// the per-session Service instance churn a RestartSession causes: the
// Service-side atomics (W1DiagStrictMiss et al.) belong to whichever instance
// ran the probe, but the harness-held counters are read from one place
// post-run regardless of how many Services the run created and closed.
func (s *Service) IntraThreadDivergence(ctx context.Context, queryText, engaged string) (int, string) {
	q := s.embedQuery(ctx, queryText)
	if q == nil {
		return 0, ""
	}
	div, class := s.intraThreadDivergence(ctx, q, s.cur.Load(), engaged)
	return div, string(class)
}

// CosineOps returns the run-total count of cosine comparisons the embedding
// recall path has performed — the MEASURED numerator of recall_query_cosine_ops
// (design §7.2). RecallQueries returns the number of embedding-recall queries
// that performed them (the denominator). The per-query average
// CosineOps()/RecallQueries() is the empirical perf-bend metric: with a flat
// scan the engaged term tracks C_main; with a usable descent tree it collapses
// to ~k·B·log_B(n). The harness reads these post-run via the cosineOpsReporter
// optional interface and emits the average as recall_query_cosine_ops, so the
// §0 O(C_main)→O(log) bend is counted, not modeled.
func (s *Service) CosineOps() int64 { return s.cosineOps.Load() }

// RecallQueries returns the number of embedding-recall queries counted into
// CosineOps. See CosineOps.
func (s *Service) RecallQueries() int64 { return s.recallQueries.Load() }

// truncateForEmbed bounds text sent to the embedder. Byte truncation
// may clip a trailing multi-byte rune; embedding endpoints tolerate
// that, and thread bodies rarely reach the cap.
func truncateForEmbed(s string) string {
	if len(s) > maxEmbedChars {
		return s[:maxEmbedChars]
	}
	return s
}
