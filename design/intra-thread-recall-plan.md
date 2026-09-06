# Implementation plan — intra-thread recall / unified embedding index (#109 / #102)

**Companion to** `design/intra-thread-recall-design.md` (the design; authoritative
for invariants I1–I8, the F1–F6 wrong-path table, the coarse→fine algorithm, the
oracle, and acceptance criteria AC1–AC6). This plan sequences the build into
validated, separately-committed increments — the same discipline used for #47/#92.

## Execution rules (every increment)

- Go work goes to **go-coder** agents, given CONVENTIONS.md + the design doc. Never
  `go build`/`go test` directly — `make build` / `make test` / `make sim`.
  `internal/log` only; DRY; named constants.
- Each increment: implement → `make test` green → **commit a checkpoint** →
  next. A failing gate or a broken design assumption is a **STOP-and-surface**,
  not a barrel-on (esp. the AC2 perf-decay gates and the AC5 determinism gate).
- **H2 honesty (carry through):** the sim's symbolic shadow-chunk oracle is a
  **coherence gate** (oracle/runtime agree at the symbolic level); it does NOT
  validate embedding recall *quality*. Quality is the **live-embedding**
  intra-thread head-to-head (the #98 machinery, now extended to emit intra-thread
  embed metrics). Any green symbolic-only intra-thread number must be reported as
  coherence+curve, never as "recall works for users" (SPEC §9.1 honesty clause).
- **AC2 is the load-bearing acceptance:** `recall_query_latency_p99` flat across
  the 15d→30d→60d→120d ladder AND flat in main-thread chunk count;
  `coarse_size == T`; `cosine_ops` flat in non-engaged-thread length.

## Build-time decisions to resolve early (don't discover mid-build)

- **D-A (Inc 2/4): chunk content source for scrolled-out excerpts.** FIFO
  *deletes* `turns/<n>.md` past `ThreadTurnWindow`, so scrolled-out chunk content
  is NOT in the assembled body. Durable source = the **event log §2.8**.
  Decision: the indexer loads chunk text via a port read over the event log
  (preferred — durable, decouples indexing from eviction timing), adding an
  additive port op (e.g. `LoadThreadExcerpts(ctx, threadID) ([]Excerpt,...)` that
  reads the log) if no existing read suffices. Capture-at-eviction is the
  fallback only if the log read proves lossy. Resolve in Inc 2; it gates Inc 4's
  debt mechanism.
- **D-B (Inc 3): `.vec` format** — JSON for v0.1 (simpler, correctness-first);
  length-prefixed binary only if startup-load cost is measured to warrant it
  (hazard H4). Not upfront.
- **D-C (Inc 6): SPEC §3.4 "build target" framing** — once built, drop the
  "build target / in design" language and state it as-built.

## Increments

### Inc 1 — `scoring`: fine-tier chunk matcher (pure)
- `ChunkVector{ TurnNumber int; Vector []float64 }`; `ProposeChunks(query,
  []ChunkVector, opts) []ChunkCandidate` (cosine fine-pass, reuses
  `cosineSimilarity`, mirrors `ProposeEmbedding`). Pure, substrate-free.
- Negative constraint: `scoring` knows nothing of debt/dormancy/snapshots/cache/FIFO.
- Gate: unit tests (ranking, threshold, empty) green; `make test`. Commit.
- Deps: none.

### Inc 2 — `measure`: snapshot + atomic indexer + coarse→fine Recall
- Replace `Service.index []ThreadVector` with `cur atomic.Pointer[indexSnapshot]`
  (`coarse`, `fine map[string][]ChunkVector`, `watermark map[string]int`).
- Indexer goroutine (`jobs chan indexJob`) + the swap rule (I1 lock-free reads;
  I2 watermark drop-stale-on-swap, CAS-retry). `EnqueueFlush(threadID,
  dispatchTurncount)` entry. Resolve **D-A** here (event-log chunk read).
- `Recall`: §4.1 coarse→fine (Kc=10, Kf=3 consts, §9 calibration) + engaged-thread
  bypass fine pass (step 3); merge into `[]Result` with additive
  `Result.IntraThread *IntraThreadHit` + `Layers()` extension; offer cap unchanged.
- `Prepare` = load cache + reconcile (stub until Inc 3) + start indexer;
  `AddThread` = enqueue full flush.
- Gate: unit tests incl. **AC3** (forced re-activation-mid-job → stale embed
  dropped; lock-free-by-construction = single atomic load, no read lock) and
  **AC5** (nil embedder → no-op, byte-identical symbolic path; existing
  `match-fire`/`embed-match-fire` unchanged). `make test`. Commit.
- Deps: Inc 1.

### Inc 3 — persisted vector cache (`measure/veccache`)
- `.recall-cache/` (gitignored at init) per §5.1; per-thread `.vec` (D-B: JSON v0.1)
  + `manifest.json`; content-hash staleness key (body_hash + per-chunk chunk_hash).
- `Load`/`Reconcile(canonical)→(snapshot, jobs)` (§5.3 O(changed) startup),
  `Write` (write-through at the swap point), `Sweep(liveThreadIDs)` (Consolidate
  hook §6.4). Plain file I/O (operational state, not a port concept — like
  `SaveWorkingSet`). Wire into `Prepare`.
- Gate: **AC4** (restart re-embeds only changed chunks; unchanged thread = 0 embed
  calls — measurable) + **AC6** (delete `.recall-cache/` → rebuild identical index
  + identical recall). `make test`. Commit.
- Deps: Inc 2.

### Inc 4 — `turn`: dormancy + debt hooks (no new recall logic)
- `touchActiveLRU` demotion point → `EnqueueFlush` (dormancy trigger, §6.2),
  behind an optional `threadIndexer`-style interface so symbolic-only default is
  untouched (I7).
- FIFO scroll-out point → per-thread debt counter on `State`; debt hits N (=16
  const) → `EnqueueFlush`. Passes dispatch `turn_count` for the watermark (I2).
- Negative constraint: `turn` does not embed/touch vectors/know coarse-fine.
- Gate: unit tests (dormancy enqueues; debt-cap enqueues at N; symbolic-only
  unaffected). `make test`. Commit.
- Deps: Inc 2 (+ D-A resolved).

### Inc 5 — `sim`: shadow-chunk oracle + Candidate-A workload + metrics
- Generator: `shadowChunks map[int][]chunkRecord` appended per emission (§8.2);
  Candidate-A main thread (never-retire, deep-trajectory, steady engagement, §9.1);
  intra-thread probe reusing `wanderProbe` plumbing + hop-bucketed coherence tally
  (predicted-hit vs observed `spine.intra-match-fire`); model the debt-window
  blind spot (chunk scrolled-out `< N` ago → predicted miss).
- Harness: `intraMatchFireSet` extractor (mirrors `embedMatchFireSet`);
  `IntraMatchFireIDs` through `StepFeedback`; the §9.2 metric series
  (`recall_index_coarse_size`, `..._fine_chunks[_main]`, `recall_query_cosine_ops`,
  `recall_query_latency_{p50,p95,p99}`, `recall_index_flush_calls/_chunks`,
  `recall_intra_blindspot_misses`, `recall_intra_hop_recall[hop]`).
- Negative constraint: oracle scores the generator's own shadow, never a runtime
  index read (F6).
- Gate: `make test` (incl. determinism: `TestGenerateWorkload_Deterministic` green
  — main thread + probes are rng-free/deterministic) + a short `make sim` showing
  the new series populate and `recall_unexplained_absence==0` + intra-thread
  coherence divergence == 0. `make test`. Commit.
- Deps: Inc 2–4.

### Inc 6 — validation runs + SPEC as-built + commit
- `make sim DURATION=15d` then the ladder (30/60/120 as feasible in the window):
  confirm **AC2** decay gates (`latency_p99` flat, `coarse_size==T`, `cosine_ops`
  flat in non-engaged length) and **AC1** intra-thread coherence (zero divergence)
  + report `recall_intra_hop_recall` curve.
- `make sim LIVE_EMBEDDING=true DURATION=14d` (the #98 path): the **embedding**
  intra-thread head-to-head — the H2 quality validation (symbolic vs embedding
  intra-thread hop-recall). Report; this is the honest "recall works" evidence.
- Update SPEC §3.4 to as-built (D-C); update ARCHITECTURE if needed; update
  tasks #102/#109 → done; memory.
- Commit. (Tech-writer README pass #110 still separate/end.)
- Deps: Inc 1–5.

## Escalation triggers during build (surface, don't barrel)
- AC2 latency p99 bends with main-thread chunk count → hazard **H1** (per-thread
  sub-index); stop, surface for a focused perf decision.
- `recall_unexplained_absence` nonzero or intra-thread coherence divergence ≠ 0 →
  zero-tolerance stop-and-root-cause (fix oracle/runtime coherence, never widen
  forgiveness).
- A design assumption breaks (e.g. D-A event-log read is lossy) → surface, don't
  improvise a coupling.

## Sequencing
Inc 1 → Inc 2 → (Inc 3 ∥ Inc 4, disjoint files: `measure/veccache` vs `turn`) →
Inc 5 → Inc 6. Parallelize Inc 3/4 only if confident; otherwise sequential with a
commit between. The design pass is done; this is execution.
