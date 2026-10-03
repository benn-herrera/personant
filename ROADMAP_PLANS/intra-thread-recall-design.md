# Intra-thread recall — chunk-level hierarchical embedding index (#109 / #102)

**Status:** design (a focused design pass preceding build, per SPEC §3.4). **Scope:** the unified
chunk-level hierarchical embedding index that delivers intra-thread recall (#109) and subsumes
thread-level index freshness/maintenance (#102). The authoritative contract is **SPEC §3.4
"Embedding index — granularity, maintenance, persistence"**; this document resolves the open design
points that block clause names there and produces a buildable skeleton, metrics, acceptance gates,
and a hazards/forks list.

**Decided pillars (constraints — not re-litigated here):**
1. Hierarchical coarse→fine: a coarse one-vector-per-thread tier over a fine per-turn-excerpt
   chunk-vector tier. The coarse tier stays.
2. Organizing distinction = **live FIFO window** (in-context, not embedded) vs. **indexed body**
   (recall-eligible). One uniform mechanism; intra-thread is a *filter* choice, not a separate code
   path.
3. Chunk unit = **turn-excerpt**.
4. Maintenance = async single-indexer goroutine, `atomic.Pointer` index swap, per-thread
   turncount-at-dispatch watermark.
5. Two flush triggers: embedding-debt cap (every N scrolled-out turns) + dormancy transition.
6. Persisted derived vector cache: gitignored, regenerable, staleness-keyed, write-through,
   load-on-startup → O(changed) startup.

---

## 1. Invariants

These are what must hold regardless of implementation. They are the exit contract for review.

- **I1 — Lock-free reads.** `Recall` never blocks on the indexer and never takes a lock the indexer
  holds. The live index is read via a single `atomic.Pointer[indexSnapshot]` load. The indexer
  builds a new immutable snapshot off to the side and swaps the pointer in one store. Readers see
  either the old whole snapshot or the new whole snapshot, never a torn one.
- **I2 — Monotonic per-thread updates.** A vector write for thread `t` carries the `turn_count` at
  which its source body was read (the *dispatch watermark*). The swap accepts a thread's new vectors
  only if their watermark is `>` the watermark already recorded for `t` in the live snapshot. A
  stale in-flight embed (thread re-activated and re-decayed while the embed was outstanding) is
  dropped on swap, never overwrites a fresher vector.
- **I3 — No performance decay with thread length.** Per-query cost and retrieval latency are bounded
  by the coarse-tier size and the top-K fan-out, *not* by the length of any single thread (§4). A
  thread with 50 000 turn-excerpts must cost the same per query as one with 50, modulo the bounded
  second-pass scan over one thread's chunks.
- **I4 — Coarse tier stays bounded.** The coarse tier holds exactly one vector per indexed thread.
  Its size is the live thread count `T`, which is already bounded by archival (§3.8) pulling the
  coldest retired threads off-spine. The coarse tier never grows with per-thread length.
- **I5 — Derived cache is never canonical.** The persisted vector cache (§5) is gitignored,
  rebuildable from canonical (`spine.jsonl` + `threads/*/turns/*.md`) on any miss or staleness, and
  carries no information not derivable from canonical. Deleting it costs CPU at next startup, never
  data.
- **I6 — The live FIFO window is never a recall candidate.** A turn-excerpt that is still inside a
  thread's `ThreadTurnWindow` (in the working set) is never embedded into the fine tier and never
  returned as a chunk hit. Only scrolled-out (or flushed-on-dormancy) excerpts are index material.
  This is the in-context/indexed-body boundary that makes the mechanism uniform.
- **I7 — Determinism preservation.** With a nil embedder the entire subsystem is a no-op and the
  symbolic-only path is byte-identical to today. With an embedder, the *runtime* index path
  introduces no new source of nondeterminism into the canonical step stream — embedding is async and
  advisory; recall output ordering is a deterministic function of the snapshot contents.
- **I8 — Existing recall unchanged.** The merged `Result` stream, the top-3 offer surface, the
  `spine.match-fire` / `spine.embed-match-fire` log vocabulary, and the symbolic Jaccard layer all
  keep their current behavior and observable events. Intra-thread chunk hits ride the same surfaces
  (§7).

### 1.1 Failure modes the design must prevent (pre-output reasoning)

The skeleton below exists to block these specific coder paths:

| # | Likely wrong path | Prevented by |
|---|---|---|
| F1 | Coder makes the indexer write directly into the slice `Recall` scans (mutex around reads). | I1 + the `atomic.Pointer` snapshot in §6.2. A mutex on the read path is a review-reject. |
| F2 | Coder flat-searches all chunks (drops the coarse tier under "simpler"). | I3/I4 + §4 cost model; pillar 1. Flat chunk search is O(total turns) per query — exactly the perf-decay the gate forbids. |
| F3 | Coder embeds the live FIFO window too (treats "active thread" as the unit instead of "indexed body"). | I6; §3 boundary. Embedding in-context turns wastes calls and pollutes recall with content the model already sees. |
| F4 | Coder rebuilds the whole index every startup (ignores the cache). | I5 + §5 staleness key. O(all) startup is untenable at multi-year scale; pillar 6 makes the cache a requirement. |
| F5 | Coder lets a late embed overwrite a fresher vector on thread re-activation. | I2 dispatch watermark; §6.2 swap rule. |
| F6 | Coder builds a *second* oracle that reads runtime index state to score intra-thread recall (couples oracle to runtime). | §8 reuses the shadow-emission pattern — the oracle scores the generator's own per-turn emission log, never a runtime read. |

---

## 2. Chunk unit (pillar 3, justified)

The chunk is **one turn-excerpt file** (`threads/thr_<id>/turns/<n>.md`) — the same unit the FIFO
window already manages. Justification for not deviating to fixed-size windows:

- The turn-excerpt is already the substrate's atomic body unit (§2.3): one file, written once,
  FIFO-evicted as a whole. Chunking on that boundary means the index unit == the substrate unit ==
  the staleness unit. No re-chunking pass, no offset bookkeeping, no straddling a chunk boundary
  across two files.
- A turn-excerpt is terse and operational (§2.3 body guidelines) — already close to the size an
  embedding call wants. The C.6 corpus work embedded section-grained Wikipedia fragments
  successfully at comparable granularity.
- Staleness is trivially per-file: a turn-excerpt is immutable once written (FIFO only deletes whole
  files), so its content hash is stable for life. A fixed-size window would re-chunk and re-hash on
  every append.

Deviation would only be justified if turn-excerpts proved *too large* for the embedder's quality
(one excerpt spanning many topics). That is a §9 calibration observation, not a design assumption;
the fixed-size fallback is a parked known-unknown (§10 hazard H3), not built.

---

## 3. The in-context / indexed-body boundary

Every thread is an **indexed body**: a coarse thread vector plus a set of fine chunk vectors, one
per *scrolled-out* (or dormancy-flushed) turn-excerpt. An **active** thread additionally carries its
live FIFO window (the last `ThreadTurnWindow` excerpts), which is in the working set and therefore
**not** embedded (I6).

The boundary moves with the FIFO. When `ThreadTurnWindow` is exceeded and the runtime FIFO-evicts
the lowest-numbered excerpt file (§2.3), that excerpt's *content* is not lost — it stays recoverable
from the §2.8 event log and the turns directory until archival — and it becomes **debt**: a chunk
owed to the fine tier. The indexer pays debt down (§6).

Consequence: "intra-thread recall of early content" is not a special path. The early turns of a
long-running thread scrolled out of its FIFO long ago; they are in the fine tier exactly like every
other indexed chunk. Recall over the engaged thread's own indexed body is enabled by **including**
the engaged thread in the fine-tier scan (the filter choice), while still **excluding** its live
FIFO window (never a chunk, I6) and its coarse vector from the *thread-selection* pass (it is
already in context — no point re-surfacing the whole thread).

---

## 4. Coarse→fine retrieval algorithm

### 4.1 Procedure

One query embedding `q` per turn (the existing single embed call). Then:

1. **Coarse pass (thread selection).** Cosine `q` against the coarse tier (one vector per thread).
   Take the top-`Kc` threads above the coarse threshold. Exclude threads engaged this turn from the
   *thread-level* result (matches the existing `Exclude` semantics) — but see step 3 for the engaged
   thread's own intra-thread treatment.
2. **Fine pass (content selection).** For each of the `Kc` selected threads, cosine `q` against
   *that thread's* chunk vectors only. Take the top-`Kf` chunks per thread above the fine threshold.
   The fine pass scans only the chunks of the `Kc` coarse-selected threads — never the whole chunk
   space.
3. **Engaged-thread intra-thread pass (the #109 case).** The engaged thread (the long-running thread
   the user is in) is added to the fine pass *by ID*, unconditionally, regardless of whether its
   coarse vector cleared the coarse threshold. Its coarse vector is the average over a multi-year
   body and will be diluted; the early-content chunks we want are exactly the ones the coarse
   average buries. So intra-thread recall bypasses the coarse gate for the one engaged thread and
   runs the fine pass directly on its chunks. Its live FIFO window chunks are excluded (I6).

### 4.2 Choice of Kc and Kf

- **Kc (coarse top-K threads): start at 10.** Rationale: the C.6 ranking finding is the calibration
  anchor — true topic is the #1 cosine match ~73% of the time, top-3 ~92%, and the curve is flat by
  ~top-10 (the "precision problem" there was the *top-10-above-cutoff candidate policy*,
  CONVENTIONS.md C.6 note). `Kc = 10` captures essentially all genuine thread matches while bounding
  the fine-pass fan-out. The final user-facing offer is still capped at 3 (`recallOfferK`), so `Kc =
  10` is generous headroom for the merge/rank/judgment stages, not the surface width. `Kc` is a §9
  calibration window, not a frozen constant.
- **Kf (fine top-K chunks per thread): start at 3.** Enough to surface the relevant passage plus
  immediate neighbours without flooding the merge. Also a §9 calibration window.

### 4.3 Per-query cost

Let `T` = live thread count, `C` = average chunks per thread, `D` = embedding dimension.

- Query embed: one embedding call (unchanged from today; the dominant wall-cost, ~single-digit
  seconds live / amortized in cache otherwise).
- Coarse pass: `T · D` multiply-adds → **O(T·D)**. At `T ≈ 400` threads (the §3.5 spine-cardinality
  working figure) and `D ≈ 768`, that is ~300K flops — microseconds.
- Fine pass: `(Kc + 1) · C · D` multiply-adds → **O(Kc·C·D)**, independent of `T` beyond the `Kc`
  cap and **independent of any single thread's total length except the engaged thread's `C`** (the
  `+1` term). With `Kc = 10`, `C` bounded by archival of cold threads and by the engaged thread's
  own chunk count.

**The perf-decay control (I3):** total per-query cosine work is `O(T·D + Kc·C·D)`. `T` grows with
population but is archival-bounded; the second term is independent of `T`. Neither term grows with
the *length* of a non-engaged thread, because the coarse pass sees only its single averaged vector.
The only length-sensitive term is the engaged thread's own `C` in the intra-thread pass — and that
is the cost we are explicitly buying. If a single multi-year thread's `C` itself becomes a cost
(tens of thousands of chunks), the fine pass over one thread is still a flat cosine scan,
parallelizable and SIMD-friendly; the §10 hazard H1 flags the point at which a per-thread sub-index
(e.g. coarse-within-thread) would be warranted — explicitly *not* built v0.1 unless the metric
demands it.

---

## 5. Persisted derived vector cache (pillar 6)

### 5.1 Layout

Gitignored, under the substrate home, mirroring the thread layout so a thread's vectors live beside
(but never inside the git-tracked part of) its directory:

```
$PERSONANT_HOME/
  .recall-cache/                 # gitignored; operational, never canonical
    threads/
      thr_<id>.vec               # one file per thread: coarse vector + chunk vectors
    manifest.json                # [derived] index of cached threads + staleness keys
```

`.recall-cache/` is added to the home `.gitignore` at init (it is operational state in the §2.1
sense, like `tmp/` and `history`).

### 5.2 File format

Per-thread `.vec` file (a small binary or length-prefixed format — chosen at build time; vectors are
`[]float64`, not human-edited, so JSON is wasteful but acceptable for v0.1 if simpler). Each `.vec`
holds:

- `thread_id`
- `dispatch_watermark` — the `turn_count` the vectors were computed at (feeds I2).
- `body_hash` — content-staleness key for the **coarse** vector: hash of the exact text fed to the
  coarse embed (the assembled indexed body, §6.1).
- `coarse_vector`
- per chunk: `{turn_number, chunk_hash, vector}` where `chunk_hash` is the hash of that turn-excerpt
  file's bytes.

`manifest.json` is a `[derived]` roll-up: `thread_id → {dispatch_watermark, body_hash, chunk_count}`
so startup reconciliation reads one small file instead of opening every `.vec`.

### 5.3 Staleness key and load-on-startup (O(changed))

The staleness key is **content hash**, per the SPEC contract (body hash for the coarse vector, chunk
hash per chunk). On startup, for each live spine thread:

1. Read the thread's current chunk set (turn-excerpt filenames + a cheap hash of each — or trust
   mtime as a fast pre-filter, hash on suspicion).
2. Compare against the cached `manifest.json` entry.
   - **Hit** (every chunk hash matches, body_hash matches): load the `.vec` verbatim into the
     snapshot. Zero embedding calls.
   - **Partial miss** (some chunks new/changed): re-embed only the changed/added chunks and re-embed
     the coarse body (its hash changed); keep the rest.
   - **Full miss** (no entry, or thread absent from cache): embed from scratch.
3. A cached thread no longer on the spine (archived/removed): its `.vec` is stale garbage; drop it
   (lazy delete, or sweep during Consolidate §6.4).

This makes startup **O(changed since last run)**, not O(all) (I5, F4). The cache is write-through:
the indexer writes a thread's `.vec` and updates `manifest.json` at the same swap point it updates
the live snapshot (§6.3).

### 5.4 Coordination with #44 (rebuild-on-open) and #99 (RecoverThread)

- **#44 startup recovery (SPEC §4.5.8).** The recall cache is *derived, operational* state. After an
  unclean shutdown, the §4.5.8 reconcile pass treats `.recall-cache/` exactly as it treats any
  derived artifact: it is not trusted blind. Reconciliation is precisely the §5.3 staleness sweep —
  content-hash the canonical turn-excerpts, rebuild-on-miss. So #109's cache *is* its own
  rebuild-on-open logic; #44 does not need bespoke recall handling beyond "run the staleness sweep
  on open" (which startup already does). Recorded as a shared-logic note, not a coupling.
- **#99 RecoverThread (SPEC §3.8.3).** A recovered thread re-enters the spine with `state = wip`. It
  has no cache entry (it was archived → its `.vec` was swept). On its first engagement (or at next
  startup) it is a full-miss and gets embedded from its recovered turn-excerpts. No special path:
  recovery surfaces a thread, the staleness sweep notices the new spine member with no cache, embeds
  it. The §3.8.4 rule that *archived* threads are off the recall surface is unchanged — only
  live-spine threads are in the coarse tier.

---

## 6. Maintenance: the async indexer (pillars 4, 5)

### 6.1 What gets embedded

- **Coarse vector** of a thread = embed of its assembled *indexed body* (the scrolled-out + retained
  body text the way `LoadThread`/`AddThread` assemble it today, truncated by `maxEmbedChars`). This
  replaces today's whole-body coarse embed in `AddThread`/`Prepare` — same call, now one tier of
  two.
- **Chunk vectors** = one embed per scrolled-out turn-excerpt (I6). Batched: the embedder takes
  `[]string`, so a flush submits all of a thread's pending chunks in one call.

### 6.2 Triggers (pillar 5)

The runtime accrues **embedding debt** per thread: the count of turn-excerpts that have scrolled out
of the FIFO but are not yet in the fine tier. Two flush triggers enqueue an embed job for a thread:

- **Debt cap N.** When a thread's debt reaches `N` scrolled-out turns, enqueue a flush of those `N`
  chunks. **Starting N = 16** (see §6.5).
- **Dormancy transition.** When a thread is demoted out of Layer B (the `touchActiveLRU` overflow
  path in `internal/turn/lru.go` — the structural "decays out of the working set" event), enqueue a
  flush of *all* its remaining debt, so its full body is in the fine tier before it becomes a pure
  recall target. This is the §3.5/§3.1 dormancy hook; it is where the live FIFO window's surviving
  content (the part never scrolled out) also becomes index material.

Both triggers enqueue onto one channel consumed by **one** indexer goroutine.

### 6.3 The indexer goroutine and the swap

```
type indexSnapshot struct {          // immutable once published
    coarse     []ThreadVector         // one per thread (existing scoring.ThreadVector)
    fine       map[string][]ChunkVector  // threadID -> chunk vectors (scrolled-out only)
    watermark  map[string]int         // threadID -> dispatch turncount of these vectors
}

// Service holds an atomic.Pointer[indexSnapshot] instead of []ThreadVector.
```

Indexer loop, per job `{threadID, dispatchTurncount, chunkHashesAtDispatch}`:

1. Load the thread body + its scrolled-out excerpts (port reads).
2. Embed coarse body + pending chunks (one or two batched embed calls).
3. **Swap:** load current snapshot; if `dispatchTurncount <= snapshot.watermark[threadID]`, **drop
   the result** (I2, F5 — a fresher embed already won). Else build a new snapshot = old snapshot
   with this thread's coarse/fine/watermark replaced, and `atomic` compare-and-store the pointer
   (retry the CAS on lost race; the build is cheap relative to the embed).
4. Write-through the cache: write `thr_<id>.vec` + update `manifest.json` (§5.3).

Readers (`Recall`) only ever do `snapshot := s.cur.Load()` and scan it (I1).

### 6.4 Sleep-cycle integration

`MemoryOps.Consolidate` (the §3.11 sleep cycle) gains a recall-cache sweep: drop `.vec` files for
threads no longer on the spine (archived), and optionally compact `manifest.json`. This is
opportunistic, off the working-hours path, consistent with the existing gc-on-day-off behavior
(#108). Not load-bearing for correctness (the sweep is also done lazily at startup) — it just keeps
the cache footprint flat over a multi-year run.

### 6.5 Debt-window N: policy, blind spot, starting value

- **Policy.** N bounds how stale the fine tier may be for an *active* thread: content that scrolled
  out within the last `< N` turns is not yet embedded, so intra-thread recall on *very recent*
  scrolled-out content has a lag of up to N turns. Dormancy flush closes the gap for any thread that
  goes dormant (the common long-tail case); the debt cap bounds the gap for a thread that stays
  active a long time (the multi-year single-thread case — exactly #109's target, so the debt cap,
  not dormancy, is the load-bearing trigger there).
- **Blind spot (the §3.11-shaped gap).** A turn-excerpt that scrolled out `< N` turns ago and has
  not triggered the cap is not yet recallable. This is acceptable because that content is *recent* —
  it is either still in the FIFO window of a related view or close enough that the user has not lost
  it. The blind spot is bounded by `N` turns of one thread and is the direct analogue of §3.11's
  "content-only turns between structural commits are uncommitted" gap: a small, bounded, by-design
  staleness window, measured (§9) not eliminated.
- **Starting N = 16.** Rationale: `ThreadTurnWindow` is 512; `N = 16` means the fine tier lags the
  FIFO by at most ~3% of the window, and a thread flushes ~32 times over one window's worth of
  scroll-out — frequent enough that recent early content is recallable within a bounded lag,
  infrequent enough that the embed-call rate stays well under one batch per turn even on a
  continuously active thread. N is a **§9 calibration window**: too small floods the embedder; too
  large widens the blind spot. The sim measures the blind-spot miss rate (§9) and the embed-call
  rate to settle it.

---

## 7. Composition with §3.4 layer-1/2/3 and the top-3 offer

Intra-thread chunk hits **merge into the same `Result` stream** — they do not surface separately.
Reasoning: the experience layer already consumes one ranked `[]Result` and offers the top 3 (§3.4
recall surface, `recallOfferK`). A separate intra-thread surface would be a second offer UX, a
second resolver path, and a second set of accept/decline events — complexity with no capability
gain. Define it as:

- `Result` gains an optional `IntraThread *IntraThreadHit` field (sibling to `Symbolic` /
  `Embedding`), carrying the matched chunk(s)' turn numbers and scores for the one engaged thread. A
  coarse/fine thread-level embedding hit still populates `Embedding` as today.
- The merge in `Service.Recall` is unchanged in shape: still a `map[threadID] *Result`, still
  ranked, still capped at `recallOfferK` by the caller. An intra-thread hit on the engaged thread
  produces a `Result` whose `ThreadID` is the engaged thread; its presence in the offer means
  "earlier in *this* thread, you settled X — here is the passage." `Result.Layers()` reports
  `intra-thread` when that field is set (parallel to `symbolic` / `embedding`).
- **Logging:** a new `spine.intra-match-fire` event (turn number(s) + score), added to the §2.8
  `spine` vocabulary, parallel to `embed-match-fire`. The harness gets an `intraMatchFireSet`
  extractor mirroring `embedMatchFireSet` (§8). Existing `match-fire` / `embed-match-fire` are
  untouched (I8).
- **Engaged-thread exclusion subtlety:** the engaged thread is normally in `Exclude` (it shadows its
  own engagement signal — `surfaceRecallCandidates`). Intra-thread recall *intentionally* re-admits
  it for the fine pass only (§4.1 step 3). The thread-level `Exclude` for coarse/embedding hits is
  preserved; only the engaged thread's *chunk* hits bypass it. This keeps the existing
  embedding-recall behavior identical (I8) and adds intra-thread as a strictly additive surface.

---

## 8. The simulation intra-thread ground-truth oracle (the hard one)

### 8.1 The existing oracle (prior art)

The thread-level recall oracle (`internal/scenarios/sim/workload.go`) is a **shadow-emission**
oracle: the generator records every symbol it emits per thread into `emittedSyms`
(`recordEmission`), and at a recall opportunity it scores the query against each candidate thread's
`shadowRetainedSet` via `simJaccard` at `simRecallThreshold` (= the production threshold constant).
It is *coherent by construction* (same union definition, same threshold, threads kept under the
eviction cap) yet *independent of the runtime* (it scores the generator's own emission log, never a
runtime read). `recallExpectedFor` returns the expected thread-ID set; the harness scores observed
`spine.match-fire` against it. This is the pattern the intra-thread oracle must follow (F6).

### 8.2 The intra-thread oracle — turn-granularity shadow

The question: for a long-running thread, when the workload re-issues a query that *should* retrieve
an EARLY turn of that same thread, how does the deterministic generator know *which* earlier
turn(s)?

**Design: per-turn shadow chunks.** Extend the shadow from "set of symbols per thread" to "ordered
list of (turn_number, symbol-set) per thread." The generator already emits, per turn, the symbol set
for the engaged thread (`recordEmission(idx, tags)`); the only change is to *also* append a
`(turnNumber, tagSet)` record to a per-thread `shadowChunks[idx]` list at every emission. This is
the generator's shadow of the fine tier — exactly mirroring that each scrolled-out turn-excerpt
becomes one chunk vector whose "content" is that turn's emitted tags.

The intra-thread workload (§9.1) drives a single long thread through a **trajectory of distinct
topical slots** (reusing the existing wander/drift trajectory machinery — a long thread is just a
deep-trajectory thread). An **intra-thread probe** turn re-issues the mad-libs query for one of the
thread's *earlier* (scrolled-out) trajectory slots and declares the expected hit as: the engaged
thread itself, *iff* some shadow chunk of that thread (a) is for a turn that has scrolled out of the
FIFO (turn_number ≤ current − `ThreadTurnWindow`) **and** (b) clears `simRecallThreshold` against
the probe query under `simJaccard`. The hop distance (how far back the queried slot sits in the
trajectory) buckets the observation, exactly like the existing abandoned-topic `wanderProbe`.

Coherence by construction (the same discipline as §8.1):
- Same threshold constant.
- Same set/union definition, now per chunk rather than per whole-thread.
- The "scrolled out" predicate uses the *same* `ThreadTurnWindow` the runtime FIFO uses, so the
  oracle's "is this chunk in the index" matches the runtime's I6 boundary.
- The debt-window blind spot (§6.5) is modeled: a chunk scrolled out `< N` turns ago is predicted
  **miss** (not yet flushed), so the oracle does not penalize the runtime for the by-design lag —
  and a divergence there is a real bug.

The probe records, per hop bucket: predicted-hit vs observed (did the runtime fire
`spine.intra-match-fire` for this thread on this probe?), the same coherence/divergence tally the
wander probe uses (`wanderHopCoherent` / `wanderHopDiverge`). Divergence is the failure signal; the
expected decay at high hops / inside the blind spot is the deliverable.

### 8.3 Fork check

I evaluated a second oracle approach: an **anchor-sentinel** oracle, where the generator plants a
unique sentinel token in exactly one early turn of the long thread and later queries that sentinel;
the expected hit is "the thread, via the chunk containing the sentinel." This is simpler to *state*
but worse: a unique sentinel makes the chunk trivially separable (high cosine, no drift), so it
tests retrieval plumbing but **not** the realistic drift-recovery property #109 exists for (finding
early content whose vocabulary differs from the current query). The shadow-chunk approach (§8.2)
reuses the validated drift trajectory machinery and exercises the real property.

**These two are not a genuine fork** — the sentinel approach is strictly weaker and I am not
escalating it. I am recording it here so the choice is visible and a reviewer can object if they
disagree. The shadow-chunk oracle is the design.

The one *genuine* open risk in the oracle is whether shadow-chunk symbol-set Jaccard is a faithful
enough proxy for the runtime's *embedding* chunk retrieval (the oracle is symbolic; the runtime fine
tier is embedding cosine). This is the **same** symbolic-proxy-for-embedding tension the whole sim
already lives with (SPEC §9.1 "symbolic-only metrics do not validate the recall path users actually
get"), and its resolution is the **same**: the embedding-in-loop run (#98) scores the embedding
layer head-to-head against the identical forgiven ground truth. So the intra-thread oracle
inherits #98's closure path — the symbolic shadow is the cheap coherence gate; the embedding-in-loop
run is the real validation. Flagged as hazard H2, not a build-blocking fork.

---

## 9. Long-running-thread workload + perf-decay metrics

### 9.1 The workload (Candidate A)

Add a workload mode that designates **one persistent "main thread"** (the multi-year project /
main-default thread, "Candidate A") that:

- Is created on day 0 and **never retired** for the whole run.
- Is engaged on a steady fraction of turns throughout (interleaved with the ordinary population), so
  its turn-excerpt count grows monotonically into the thousands across the 120-day rung — driving
  its FIFO to scroll out continuously and its debt cap to fire ~`turns/N` times.
- Follows a **deep trajectory** (reuse wander machinery with `wanderMaxHops` raised for this one
  thread, or an unbounded variant) so its early content is vocabulary-distinct from its current
  content — the realistic intra-thread drift-recovery case.
- Is the target of periodic **intra-thread probes** (§8.2) querying its early scrolled-out slots,
  bucketed by hop distance.

This is additive to the existing generator: the main thread is one extra tracked thread with a
"never retire, always eligible, deep trajectory" flag; the probe reuses the `wanderProbe` plumbing.
Determinism is preserved — the main thread and its probes are pure functions of (Seed, Duration,
Corpus), same discipline as the existing probe (rng-free, part of the canonical stream).

### 9.2 Perf-decay metrics (the I3 obligation, §9 of SPEC)

Emit per-rung (and as steady-state trajectories across the rung walk):

- `recall_index_coarse_size` — coarse-tier vector count (must track `T`, flat per-thread-length; the
  I4 check).
- `recall_index_fine_chunks` — total chunk vectors; and `recall_index_fine_chunks_main` — the main
  thread's chunk count specifically (the length axis).
- `recall_query_cosine_ops` — modeled/measured cosine ops per query (validates the §4.3 `O(T·D +
  Kc·C·D)` model; must be flat in main-thread length).
- `recall_query_latency_{p50,p95,p99}` — wall-clock per-query retrieval latency (profiling clock),
  as a function of both `T` and the main thread's chunk count.
- `recall_index_flush_calls` / `recall_index_flush_chunks` — embed-call rate from the debt cap +
  dormancy triggers (the cost N pays).
- `recall_intra_blindspot_misses` — intra-thread probes that missed *because* the target chunk was
  inside the debt-window blind spot (predicted+observed miss); separates by-design lag from real
  loss.
- `recall_intra_hop_recall[hop]` — symbolic and (on #98 runs) embedding intra-thread recall by hop
  distance, the #109 fidelity curve.

### 9.3 The decay gates (what "no decay" means, measurably)

"No performance decay" (I3) is operationalized as: across the `15d→30d→60d→120d` ladder, with the
main thread's chunk count growing into the thousands —

- `recall_query_latency_p99` is **flat** (within a bounded constant, e.g. ≤ 1.5× the 15d-rung value
  — calibrated, not invented), *not* trending with the main thread's chunk count or with `T`. This
  is the headline I3 gate.
- `recall_query_cosine_ops` grows only with `T` and the main thread's own `C` (per §4.3), and is
  **flat in non-engaged thread length** (cross-check: a run with the same `T` but a shorter main
  thread has the same coarse-pass ops).
- `recall_index_coarse_size == T` exactly, every rung (I4).

---

## 10. Acceptance criteria

Observable outcomes that must hold when #109/#102 is complete (exit condition for the review loop):

- **AC1 — Intra-thread recall works.** On the long-thread workload, the runtime surfaces the engaged
  thread for an intra-thread probe whose target early chunk is (a) scrolled out and (b) outside the
  debt-window blind spot, at a fidelity that **matches the oracle's prediction** (coherence: zero
  divergence between predicted-hit and observed-hit, modulo the #98 symbolic/embedding gap). The
  hop-recall curve (`recall_intra_hop_recall`) is reported, not asserted to a floor (the curve is
  the deliverable, per the §9.1 within-thread-wander re-scoping — coherence + curve, not a recall
  floor).
- **AC2 — No performance decay.** The §9.3 gates hold: `recall_query_latency_p99` flat across the
  ladder and flat in main-thread length; `coarse_size == T`; `cosine_ops` flat in non-engaged-thread
  length.
- **AC3 — Lock-free, monotonic, uniform.** Recall reads never block the indexer (I1; verifiable by
  construction — single `atomic.Pointer` load, no read-path lock). A stale in-flight embed never
  overwrites a fresher vector (I2; unit- testable with a forced re-activation-mid-job). There is
  exactly one index code path for active and dormant threads (I6 boundary; no `if intraThread`
  branch in maintenance).
- **AC4 — O(changed) startup.** A restart after a run re-embeds only chunks whose content hash
  changed since last run; an unchanged thread costs zero embed calls at startup (measurable:
  `recall_index_flush_calls` at startup ≈ changed-chunk count, not total-chunk count). Exercised on
  a `RestartSession` step.
- **AC5 — Determinism + existing recall unchanged.** The symbolic-only sim is byte-identical to
  today (nil embedder → no-op subsystem, I7). The `spine.match-fire` / `embed-match-fire` events and
  the top-3 offer surface are unchanged (I8); intra-thread rides the additive
  `spine.intra-match-fire` event and the additive `Result.IntraThread` field.
- **AC6 — Cache is never canonical.** Deleting `.recall-cache/` and restarting produces an identical
  index (rebuilt from canonical) and identical recall output (I5).

Non-functional criteria (AC2) are the load-bearing ones for #109; they are the SPEC §3.4 "no
performance decay … validated in the §9 acceptance simulation" obligation made measurable.

---

## 11. Module skeleton

Dependency direction unchanged: `scoring` is substrate-free pure math; `measure` is substrate-aware
composition over the port; `turn` drives the chain and owns the debt/dormancy hooks; `scenarios/sim`
owns the oracle. No new package needs to import the substrate directly.

### 11.1 `internal/recall/scoring/` (pure, substrate-free)

- **New type `ChunkVector{ TurnNumber int; Vector []float64 }`** — fine-tier unit.
- **New `ProposeChunks(query []float64, chunks []ChunkVector, opts) []ChunkCandidate`** — the
  fine-pass cosine matcher (mirrors `ProposeEmbedding`; reuses `cosineSimilarity`). Pure; no I/O.
- `ProposeEmbedding` (coarse pass) is unchanged — it already ranks `[]ThreadVector` and is exactly
  the coarse tier.
- **Negative constraint:** `scoring` knows nothing about debt, dormancy, snapshots, atomics, the
  cache, or the FIFO. It is cosine math over slices.

### 11.2 `internal/recall/measure/` (the Service extension)

- **`indexSnapshot`** (immutable; §6.3): `coarse []scoring.ThreadVector`, `fine
  map[string][]scoring.ChunkVector`, `watermark map[string]int`.
- **`Service`** changes: replace `index []scoring.ThreadVector` with `cur
  atomic.Pointer[indexSnapshot]`; add the indexer goroutine (`jobs chan indexJob`), a `cache` handle
  (§11.4), and `Kc/Kf/threshold` config. `Recall` does `snap := s.cur.Load()`, runs §4.1
  coarse→fine, merges into `[]Result` (now possibly with `IntraThread` hits), unchanged offer cap.
- **`Result.IntraThread *IntraThreadHit`** + `Layers()` extension (§7).
- **`EnqueueFlush(threadID, dispatchTurncount)`** — the debt-cap / dormancy entry the turn loop
  calls. `Prepare` becomes "load cache + reconcile vs canonical (§5.3), build the initial snapshot,
  start the indexer goroutine." `AddThread` becomes a thin "enqueue full flush for a new thread."
- **Negative constraint:** `measure` does not parse turn-excerpt filenames or touch git — it asks
  the port for bodies/excerpts and asks the cache to persist. It owns the snapshot/atomic/indexer;
  it does not own the FIFO policy.

### 11.3 `internal/turn/` (the hooks — no new recall logic)

- In `lru.go`'s `touchActiveLRU`, at the **demotion** point (a thread overflows Layer B → dormant),
  call the recaller's `EnqueueFlush` (dormancy trigger, §6.2). Guarded behind the
  `threadIndexer`-style optional interface so the symbolic-only default is unaffected (I7).
- At the FIFO scroll-out point (where a turn-excerpt is evicted past `ThreadTurnWindow`), increment
  per-thread debt; when debt hits `N`, `EnqueueFlush`. (This is the `EngageThread`/excerpt-append
  path; the debt counter is session state on `State`, reset semantics like the other LRU state.)
- **Negative constraint:** `turn` does not embed, does not touch vectors, does not know coarse/fine
  — it only signals "this thread's body changed, flush it," passing the dispatch `turn_count` for
  the watermark (I2).

### 11.4 cache (in `measure`, or a small `measure/veccache` helper)

- `Load() (snapshot, manifest)`, `Reconcile(canonical) (snapshot, jobs)` (§5.3), `Write(threadID,
  coarse, chunks, watermark, hashes)` (write-through, §6.3), `Sweep(liveThreadIDs)` (Consolidate
  hook, §6.4). Reads canonical via the port; reads/writes `.recall-cache/` via plain file I/O (it is
  operational state, not a substrate op — analogous to how `SaveWorkingSet` keeps session membership
  out of git). **Negative constraint:** the cache never writes into the git-tracked tree and never
  claims to be canonical (I5).

### 11.5 `internal/scenarios/sim/` + `internal/scenarios/` (oracle + metrics)

- Generator: add `shadowChunks map[int][]chunkRecord` and append per emission (§8.2); add the
  main-thread workload flag (§9.1); add the intra-thread probe (reuse `wanderProbe` plumbing) and
  its hop-bucketed coherence tally.
- Harness: add `intraMatchFireSet` extractor (mirrors `embedMatchFireSet`) and the §9.2 metric
  series; thread `IntraMatchFireIDs` through `StepFeedback` (parallel to `EmbedMatchFireIDs`).
- **Negative constraint:** the oracle scores the generator's own shadow, never a runtime index read
  (F6).

### 11.6 Port touchpoints (`internal/memops`)

- No new canonical operations. The indexer reads via existing `LoadThread` / `ListThreads`; the
  per-excerpt read needs either the existing body-load (chunking on the assembled body) or a small
  **additive** read op `LoadThreadExcerpts(ctx, threadID) ([]Excerpt, error)` if per-file
  granularity is cleaner than re-splitting the assembled body. Prefer chunking the assembled body
  first (no new port method) and add the op only if splitting proves lossy — decided at build time.
- `Consolidate` already exists; the cache sweep is added inside the adapter's Consolidate or called
  alongside it. The cache itself is operational state, not a port concept (like the working-set
  artifact), so it does **not** get port methods.

---

## 12. Hazards / forks

Honest list of contested or high-risk-unknown items, flagged for targeted MAD or user decision
rather than built on a guess:

- **H1 (perf, medium):** if a *single* thread's chunk count `C` grows so large that the
  engaged-thread fine pass (`O(C·D)`) itself becomes the latency floor (tens of thousands of chunks
  on one multi-year thread), a per-thread sub-index (coarse-within-thread, or recency-bucketed fine
  tiers) is warranted. **Not built v0.1.** The §9.2
  `recall_query_latency_p99`-vs-main-thread-chunk-count metric is the trip-wire; if it bends,
  escalate to a focused perf MAD. Flagged so a coder does not pre-build the sub-index speculatively
  (that would be exactly the complexity-for-its-own-sake the review lens rejects).
- **H2 (oracle fidelity, medium):** the intra-thread oracle is symbolic (shadow-chunk Jaccard) while
  the runtime fine tier is embedding cosine. This is the same symbolic-proxy tension the whole sim
  carries; resolution is the #98 embedding-in-loop head-to-head against the identical forgiven
  ground truth. Not a fork — but the coverage claim must state honestly (SPEC §9.1 honesty clause)
  that symbolic-only intra-thread numbers are a coherence gate, and the embedding validation
  rides #98. If #98 is not yet landed when #109 builds, AC1 is "coherence + curve under the symbolic
  shadow" and the embedding validation is a documented parked dependency, not a silent gap.
- **H3 (chunk unit, low):** turn-excerpt chunking assumes one excerpt is a coherent embedding unit.
  If excerpts prove multi-topic enough to dilute their chunk vector (a §9 observation), a
  sub-excerpt fixed-size chunker is the fallback. Parked known-unknown; not built. The staleness key
  (per-file hash) would need revisiting if this fires.
- **H4 (cache format, low):** §5.2 leaves binary-vs-JSON `.vec` format to build time. JSON is
  simpler and fine for v0.1 correctness; if startup-load time on a multi-thousand-thread cache
  becomes a measured cost, a length-prefixed binary format is the boring optimization. Decided by
  measurement, not upfront.

**No scratch-rewrite fork.** The decided pillars are mutually consistent and the open points all
resolved within the existing port/scoring/measure boundary; I did not find an approach that cannot
be made to work within the contract, so I am not escalating a redesign. The one item I want a
reviewer's eye on is **H2** (the oracle's symbolic/embedding gap and its dependency on #98) — it is
a coverage- honesty question more than a structural one, but it is the place where a wrong call
would let a green check overclaim.

---

## 13. Resolution: durable content source (corrects §3 and §11.6)

**Build-surfaced correction (Inc 2, 2026-06-01).** §3 and §11.6 above inherited SPEC §2.3's claim
that scrolled-out content "stays recoverable from the §2.8 event log and the turns directory until
archival." Building Inc 2 (the D-A decision) **falsified it**: the event log records only event
*metadata* (`source=` + byte count), never content; and `turns/` FIFO-**deletes** excerpt files past
`ThreadTurnWindow=512` (`store.ReadThreadExcerpts` / `thread_io.go` eviction). So a multi-year
thread's early content was being *destroyed*, leaving nothing to embed or return — the headline #109
case had no durable source.

**Resolution (decided with the user; SPEC §2.3 + §3.10.8 updated):**

- **Decouple the recency window from on-disk retention.** `ThreadTurnWindow` governs only *assembly
  into context* (the Layer-B byte budget); turn-excerpts are **retained on disk** past the window.
  They are the durable decision-class content the fine tier embeds. The FIFO no longer deletes them.
  (Inc 4 changes here: window = assembly bound; retention keeps excerpts; debt = excerpts scrolled
  out of the *assembly* window but retained on disk. Inc 2's `LoadThreadExcerpts` already reads
  retained excerpts — no Inc 2 rework.)
- **Turn-excerpts are uniformly decision-class** (terse `user:`/`agent:` renderings); raw task-class
  tool output is a transient §3.0 context delta, never an excerpt. So the first cut is simply
  *retain all excerpts* (over-retain — safe), not a per-excerpt class filter.
- **Keep/toss precision is deferred** (SPEC §3.10.8, sleep-cycle): raw tool output always-transient
  (deterministic) + the agent's tool-data re-presentation marked inline `lifetime:transient |
  lifetime:durable` (pinned closed-set marker, fail-safe durable, stripped, compliance-measured on
  the #98 harness). Trims the retained set; not a v0.1 #109 blocker.
- **Storage note:** retain-all grows the git-tracked `turns/` **linearly** with thread length
  (inherent — intra-thread recall needs the content somewhere; the cheapest honest home is the
  canonical excerpts). Archival (§3.8) pulls cold threads off-spine; the keep/toss layer trims live
  retained sets. The §9.3 perf-decay gates are about *query* cost (flat via coarse→fine), not
  storage (linear, inherent). H1/the gates remain the trip-wire.

This resolves the blocker as a contained Inc-4 change (decouple window/retention) plus the SPEC
correction — no redesign, no new store, Inc 2/3/5/6 proceed.
