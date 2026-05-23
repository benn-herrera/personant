# Design: within-thread topic interleaving (#96) + corpus density scaling (#95)

Status: DESIGN (not implemented). Scope: sim harness only (`internal/scenarios/sim/`,
`internal/scenarios/recall_fidelity.go`). Branch: `initial-implementation`.

## 0. Problem & coupling (recap, grounded)

- **#96** Every normal thread is welded to one corpus slot for life:
  `corpusModel.slotFor(order) = (order/familySize) % len(slots)` (workload.go:302). A
  thread only ever issues one topic's symbols — unrealistically monotonic. Only the
  scripted `campaign` threads (vague/drift/invert) wander today. The gap is *per-thread
  monotonicity*, NOT between-thread interleaving (already heavy: ~22 threads / ~15 topics
  per ~205-turn session, dwell ~1.67).
- **#95** Recall corpus is a FIXED vocabulary (~3010 slots, ~6384 tokens). As thread
  population grows into the thousands against fixed vocabulary, dormant non-sibling threads
  accumulate enough Jaccard overlap to clear the 0.4 threshold → precision declines (F1
  0.937→0.850 over 7d→120d) while recall stays flat (~0.95). This is a false-positive
  *collision artifact* the symbolic-only oracle scores as a precision miss — NOT a
  production recall bug (production §3.4 layers 2-3 disambiguate; real vocabulary is
  open-ended).
- **Coupling:** #96 raises topics-per-thread → more cross-thread symbol overlap →
  amplifies the saturation precision floor. Adding #96 on the fixed corpus would muddy
  the very measurement #96 is meant to improve. **#95 is a prerequisite; design together.**

## 1. Invariants

1. **Determinism.** The canonical (zero-feedback) step stream remains a pure function of
   `(Seed, Duration, Corpus)`. Every new wander decision and every salt is derived from the
   seed via existing rng draws or pure hashing of stable identifiers (creation order, slot
   index, turn). No new wall-clock or map-iteration-order dependence. (workload.go package
   doc, lines 50-72.)
2. **Oracle independence.** Ground truth is NOT computed by calling the runtime's
   `ProposeFromIndex` / `ProjectAnchors` / store. The oracle is an independent model that
   *replicates the runtime's symbol-set and projection semantics by construction* (§4).
3. **Supersession compatibility.** The wander model rides the EXISTING runtime lifecycle
   (active / superseded-retained / EverCentral latch, projection = top-`AnchorProjectionMax=8`
   of active history_symbols, projection.go:40-104). No production projection/recall change.
4. **Vocabulary-density constant under #95.** Symbol-space density (distinct active symbols
   per thread, contended slot count) is held ~constant as thread population scales, so
   non-sibling collision rate does not grow with Duration. Precision degradation across the
   ladder must flatten relative to the current 0.937→0.850 slope.
5. **Recall-measurement honesty.** The harness must exercise genuine within-thread
   incoherence (multi-topic trajectories, early-topic supersession) AND keep recall
   measurable: every recall opportunity has a *computable, finite* expected-match set, and
   the artifact (FP collisions) is distinguishable from genuine recall behavior in metrics.
6. **Production untouched.** If any production change proves genuinely required, it is a
   SURPRISE and must be flagged (§8). Current finding: none required — the runtime already
   supports evolving history_symbols and superseded-retained recall (confirmed: anchor
   lifecycle shipped Inc 1-5, validated 1w→6m).

## 2. Corpus / vocabulary model (#95)

### 2.1 The density problem, precisely

Today `T(thr) = anchors ∪ {normalized history_symbols}` is drawn from a **shared** 3010-slot
vocabulary. Two threads on *different* slots can still share tags (slots within a topic share
columns; mad-libs synonyms recur across topics). At N=20 threads the chance a query symbol
set Q collides ≥0.4 with an unrelated dormant thread is small; at N=2000 the union of all
dormant `T(thr)` saturates the vocabulary and spurious ≥0.4 hits accumulate. Recall is
unaffected (the true sibling still matches); precision falls.

### 2.2 Fix: per-thread unique-symbol salting

Give each thread a small **deterministic salt symbol** appended to the symbols it emits, so
that two threads bound to the same logical slot still share enough to recall each other, but
the *background* (non-sibling) collision probability does not grow with N.

Salt rule (deterministic, no rng):

```
saltSymbol(order int) string  =  fmt.Sprintf("t%dz", order)   // greppable, collision-free by order
```

- The salt is **NOT** a query symbol on a recall opportunity (the query Q reissues only the
  topical slot tags — see §3.3), so the salt never helps OR hurts the Jaccard *match* between
  query and sibling. Its sole job: inflate every thread's `T(thr)` denominator with a unique
  token so that two *non-sibling* threads' `T` sets gain a private element each, lowering
  their pairwise Jaccard below the unrelated-collision regime.
- **Density invariant:** distinct symbols per thread = (topical tags ≈ 4-5) + (salt = 1) +
  (wander-accreted topics × ~4-5, capped by `AnchorProjectionMax=8` active + retained
  superseded). Salt count scales 1:1 with thread count → vocabulary grows with population →
  density (contended-symbol-per-slot) stays ~constant. This is the "scale vocabulary with
  thread population" requirement, realized as 1 unique token per thread rather than a new
  corpus file.

### 2.3 Interaction with FamilySize sibling mechanism

The `familySize=2` mechanism (workload.go:278-294) is what guarantees every recall
opportunity has exactly one dormant same-slot sibling. **Salting must not break this.**

- Siblings share the topical slot tags (the recall handle). The salt is *per-thread distinct*,
  so a sibling pair {A,B} has `T(A) = topical ∪ {saltA}`, `T(B) = topical ∪ {saltB}`.
- On a recall opportunity Q = topical tags only (no salt). `|Q ∩ T(B)| = |topical|`,
  `|Q ∪ T(B)| = |topical| + 1` (the salt). So Jaccard = `|topical| / (|topical|+1)` ≈
  4/5 = 0.8 ≫ 0.4 — the sibling still fires comfortably.
- A non-sibling C on a *different* slot: `Q ∩ T(C)` is now only the incidental
  cross-topic synonym overlap, and `T(C)` carries its own salt + its own wander symbols
  inflating the union → its Jaccard drops further below 0.4. **Salt suppresses exactly the
  false positives #95 targets, without touching the true-sibling fire.**

### 2.4 Why not a second corpus file / generated synonyms

Rejected: regenerating a population-sized vocabulary file is non-deterministic to maintain,
duplicates the corpus loader, and couples corpus size to Duration. A per-thread salt is
O(1), pure from creation order, and is the minimum that holds density constant. Boring beats
clever here.

## 3. Within-thread wander model (#96)

### 3.1 Generalize the campaign drift mechanic to normal threads

The campaign `drift` program already does a controlled origin→dest wander on a dedicated
thread with supersession (workload.go:1740-1758): genesis on origin tags, then emit a
different-topic dest set every turn so dest Counts climb past origin and outrank origin out
of the top-8 projection → origin becomes superseded-retained (still recall-findable).

**#96 = make a fraction of normal threads carry their own multi-topic trajectory using the
same supersession mechanic, driven incrementally across their natural engagements** (not as a
dedicated scripted campaign). A wandering normal thread is no longer pinned to one slot; it
holds an ordered **visited-slot trajectory** `traj = [s0, s1, s2, ...]`, accreting a new slot
when it wanders.

### 3.2 Wander schedule (deterministic)

Per thread, on each engagement (continue/switch/resume; NOT new, NOT recall-opportunity), a
wander decision:

```
wanderEligible := !isNew && !recallOpp && engagementCount(thr) >= wanderMinDwell
if wanderEligible && g.rng.Intn(wanderSelector) < wanderPct {
    nextSlot := nextWanderSlot(thr)   // pure: deterministic neighbor of current slot
    thr.traj = append(thr.traj, nextSlot)
    thr.cur  = nextSlot               // emissions now use nextSlot's tags
}
```

- `wanderPct` / `wanderSelector`: seed-tunable rate (start ~15-20% per eligible engagement,
  matching campaign drift cadence; tune on the rung walk). Draw made **unconditionally inside
  the eligibility gate** so rng ordering stays input-determined (same discipline as
  `maybeUserDictated`, workload.go:1926-1932).
- `wanderMinDwell`: a thread must dwell ≥N turns on a topic before wandering (realism: humans
  don't wander every turn). Start ~3.
- `nextWanderSlot(thr)`: deterministic — `(thr.cur + offset) % len(slots)` where `offset` is
  derived from `(thr.order, len(thr.traj))` so the walk is a fixed pure sequence per thread.
  Prefer a slot of a *different topic* than `thr.cur` (skip forward like
  `startCampaign` does at workload.go:1532-1535), so the dest tags are disjoint and origin can
  genuinely supersede. Wander targets need NOT be in the thread's family.
- **Per-thread cap:** trajectory length capped (e.g. ≤4 topics) so a thread cannot accrete
  unbounded vocabulary — keeps density invariant (§2.2) and matches the realistic
  castle→garderobe→servant-health→disease example (4 hops).

### 3.3 How emissions ride supersession

When a wandering thread is engaged on its *current* slot `thr.cur`, it emits
`nonLooseTags(slots[thr.cur])` (verbatim, per existing anchorTags rule, workload.go:1429-1435).
Repeated emission of the current slot's tags raises their Counts; the *earlier* slots' tags,
no longer re-emitted, fall out of the top-8 active projection exactly as campaign drift does →
they become superseded-retained (EverCentral latched). This requires **zero new runtime
behavior** — it is the same mechanic the validated lifecycle already runs.

Recall-opportunity turns still issue the *engaged thread's current-slot* mad-libs query
verbatim (Q = current slot tags), preserving the existing `defaultUserInput` recall-opp path
(workload.go:1878-1885).

### 3.4 Salt on emissions

Every emission also carries `saltSymbol(thr.order)` as a model anchor tag (§2.2). It accretes
into history_symbols and, being a single low-Count token, does not distort the top-8 topical
projection meaningfully.

**CORRECTION (increment 1, commit 90044d9):** the original claim that the salt "never appears
in Q" was WRONG about runtime behavior. The runtime coalesces the *engaged turn's* model anchor
tags into the recall query set (`turn/recall.go` → `coalesce.symbolList()`), so the engaged
thread's OWN salt lands in Q. The oracle MUST mirror this exactly — `Q = slot tags ∪
{saltSymbol(engagedThread)}` — or it manufactures false misses on borderline-loose slots (the
oracle's Q would be one symbol smaller than the runtime's union). The density mechanism is
unaffected: each *candidate* thread carries its own DISTINCT salt in its retained set, so the
engaged thread's salt-in-Q matches only the engaged thread itself (never a recall candidate),
while every candidate's private salt still inflates its union denominator. This is a worked
example of invariant #2 (oracle coherence) requiring the oracle to track the runtime's ACTUAL
query construction, not an idealized one.

## 4. The oracle redesign — THE CRUX

### 4.1 What breaks

Today `recallExpectedFor(idx, slotIdx, isNew)` (workload.go:1852) returns dormant threads on
the SAME slot — an O(1), history-free rule valid only because every thread is monotonic. Once
threads wander, "expected match for a query on slot S" becomes **history-dependent**: it is
every dormant thread whose ACCUMULATED symbol set (active topical tags ∪ superseded-retained
tags ∪ salt) overlaps Q above the Jaccard threshold. The oracle must track each thread's
trajectory and stay coherent with the runtime's projection turn-by-turn, or it manufactures
false misses/positives — the pervasive form of the artifact in §0.

### 4.2 The independent-but-coherent rule

The oracle replicates the runtime's set semantics **from the generator's own bookkeeping**,
never by calling the runtime. The key insight that makes this tractable: **the generator
already fully controls every input to the runtime's projection** — it emits each turn's tags,
it knows the turn index, and the projection is a deterministic pure function
(`ProjectAnchors`, projection.go:40) of (symbol Counts, EverCentral, recency, turn). So the
generator can maintain a *shadow projection* per thread using the SAME ranking discipline,
re-implemented independently, fed only by what the generator emitted.

Per-thread shadow state the generator maintains (mirrors `memops.HistorySymbol` fields it
controls):

```
type shadowSym struct {
    sym         string
    count       int      // ++ each turn the generator emits this tag for this thread
    everCentral bool     // latched when sym enters the shadow top-AnchorProjectionMax
    lifecycle   active|superseded
    firstSeen   int
    lastActive  int
}
type threadShadow struct { syms map[string]*shadowSym }
```

On each emitted turn for thread T with tag set E (current-slot tags + salt):

1. For each `s ∈ E`: increment `count`, set `lastActive = turn`, set `firstSeen` on first
   sight, ensure `lifecycle=active`.
2. Run an **independent re-implementation of the projection ranking** (the §4.3 shared
   contract) over T's active shadow syms → top-`AnchorProjectionMax` = the shadow active set;
   syms that were everCentral and drop out flip to superseded; entrants latch everCentral.

This is the generator's own copy of `symbolRankLess` + the top-max cut. It is **independent
code** (separate function, separate package — `internal/scenarios/sim/`), not a call into
`turn.ProjectAnchors`.

The recall oracle for a query Q on a turn engaging thread `idx`:

```
recallExpectedFor(idx, Q):
    expected = []
    for cand in g.threads where cand != idx and not inLayerB(cand):
        T = activeShadowSyms(cand) ∪ superseded­RetainedShadowSyms(cand) ∪ {saltSymbol(cand)}
        // independent Jaccard, SAME threshold the runtime uses (0.4)
        if jaccard(Q, T) >= recallThreshold:
            expected = append(expected, cand.threadID())
    return sort(expected)
```

`recallThreshold` is a named constant in the sim package set equal to
`scoring.DefaultThreshold` (0.4) — a single source of truth; if the production threshold
moves, the oracle's constant references the production constant (import, not copy) so they
cannot silently diverge. **This is the one permitted coupling: sharing the THRESHOLD CONSTANT
and the symbol-set UNION DEFINITION is sharing the contract, not the implementation.** The
oracle re-derives the *set membership* independently; it merely agrees on the *scoring rule's
parameters*.

### 4.3 Why this stays coherent (the argument)

The runtime's recall match for thread T is: `jaccard(Q, anchors(T) ∪ history_symbols(T)) ≥ 0.4`,
where `anchors(T)` is the projection of active history_symbols and history_symbols includes
superseded-retained (scoring.go:85, buildThreadSet). Note `anchors ⊆ history_symbols` (anchors
are a projection of the same symbols), so the runtime's `T(thr)` = **all non-evicted symbols**
(active + superseded-retained), normalized.

Therefore the oracle does NOT even need to reproduce the projection ranking to compute the
*recall match set* — `anchors ∪ history_symbols` collapses to "every retained symbol." The
projection ranking only decides *which* symbols are labeled active vs superseded, which
affects (a) the `superseded-weight` scorer contribution and (b) eviction. For the **recall
oracle (which threads clear 0.4)**, the generator only needs the set of retained symbols it
emitted, which it knows exactly. **This drastically de-risks the crux.**

The shadow projection (§4.2 step 2) is still needed for two narrower jobs:
- **Eviction coherence:** a symbol is dropped from `T` only on capacity eviction
  (EverCentral || high-specificity protected; projection.go ranking). The generator's threads
  stay well under the cap (≤8 active + bounded superseded from a ≤4-hop trajectory + 1 salt ≈
  ≤13 symbols, below the 40-cap that the 6m ladder showed flat). **So eviction never fires for
  normal wandering threads** → the oracle can treat `T` as "all emitted topical tags ∪ salt"
  with no eviction modeling. (This must be asserted, §6.)
- **superseded-weight metric** (`superseded_precision`, `abandoned_premise_recall`): if a rung
  sets `recall.superseded-weight < 1.0`, a superseded symbol's Jaccard numerator contribution
  is scaled. The oracle then DOES need the active/superseded label. The shadow projection
  (§4.2) supplies it. At the default weight 1.0 (current ladder), superseded == active for
  scoring, so the oracle's weighted Jaccard reduces to plain Jaccard and the label is moot.

**Coherence conclusion:** at default weight, the oracle is "retained-symbol set Jaccard ≥ 0.4"
— exactly the runtime's rule, computed from the generator's own emission log, with the
threshold imported as a shared constant. Independence holds (no runtime call); coherence holds
by construction (same union definition, same threshold, no eviction in range, same emission
history). The ONLY place the shadow projection's independent ranking can diverge from the
runtime is the active/superseded *labeling* under weight<1.0 — that divergence is the residual
risk (§7) and is the sole thing a MAD would need to stress.

### 4.4 Determinism of the oracle

The oracle reads only generator-maintained shadow state, advanced by emitted tags + turn
index — all pure from `(Seed, Duration, Corpus)`. No rng, no runtime read. The expected set is
sorted before emission (matches existing `recallExpectedFor` ascending order).

## 5. Module skeleton

All changes in `internal/scenarios/sim/` unless noted. Production: **none.**

### Types (workload.go)
- `thread` (workload.go:364) gains: `cur int` (current slot), `traj []int` (visited slot
  trajectory). `slotIdx` retained as `traj[0]` for back-compat / annotations.
- NEW `threadShadow` + `shadowSym` (§4.2) — generator's independent symbol-set bookkeeping.
  `generator` gains `shadows []*threadShadow` (indexed by creation order).

### Functions — NEW
- `saltSymbol(order int) string` (§2.2) — pure.
- `nextWanderSlot(thr thread, slots []CorpusSlot) int` (§3.2) — pure, different-topic skip.
- `maybeWander(idx int, isNew, recallOpp bool)` (§3.2) — eligibility gate + single rng draw;
  mutates `thr.cur`/`thr.traj`. Called from `buildStep` after `selectEngagedThread`.
- `(g) recordEmission(idx int, tags []string)` (§4.2) — advance shadow state + run shadow
  projection. Called wherever the generator emits model anchor tags (buildStep,
  campaignEngage).
- `(g) shadowRetainedSet(idx int) map[string]struct{}` — active ∪ superseded ∪ salt for thread.
- `simRecallThreshold` const = `scoring.DefaultThreshold` (single source of truth, §4.2).
- independent `simProjectRank(...)` mirroring `symbolRankLess` (§4.3) — only consumed when
  weight<1.0; gate behind that.

### Functions — CHANGED
- `corpusModel.slotFor` (workload.go:302) — unchanged for *initial* binding (creation-order
  family binding still seeds `traj[0]`); wander mutates `cur` thereafter.
- `recallExpectedFor` (workload.go:1852) — **rewritten** to the §4.2 trajectory-aware rule
  (iterate dormant threads, shadow-set Jaccard ≥ threshold). Signature changes from
  `(idx, slotIdx, isNew)` to `(idx int, Q []string, isNew bool)`.
- `buildStep` (workload.go:1418) — call `maybeWander`; emit `thr.cur` tags + salt; call
  `recordEmission`; compute `Q` from current slot; call new `recallExpectedFor(idx, Q, isNew)`.
- `defaultUserInput` / emission helpers — append salt to the model anchor tags (NOT to Q on
  recall opps).
- campaign helpers (`campaignEngage`, `campaignRecall`) — route through `recordEmission` so
  campaign threads share the same shadow bookkeeping (DRY: one emission path, one oracle).
  Campaigns become a *special-cased, fully-scripted* trajectory; normal wander is the
  stochastic generalization. Consider whether campaigns are now redundant (§7 open question).

### Metrics (workload.go ~1008, sim_test.go ~385)
- NEW `workload_thread_topics_distinct` — distinct slots in each thread's trajectory at
  retirement (proves non-monotonicity; expect mean >1, tail to wander-cap).
- NEW `workload_thread_wander_hops` — trajectory length distribution.
- NEW recall buckets reusing the campaign tally machinery: `wander_current_recall` (query the
  thread's CURRENT topic → must surface, ≥~0.95), and `wander_origin_recall_byhops` —
  abandoned-early-topic recall BUCKETED BY wander distance (hops between the queried topic and
  the thread's current topic). Expected to DECAY with hops on the symbolic layer (1-2 hop ≈
  hit; ≥3-hop → sub-0.4 Jaccard → miss). This decay curve is the quantified motivation for
  §3.4 layer-2/3 (embedding + model judgment, nil in this sim), NOT a failure — see criterion
  (b).
- KEEP existing `abandoned_premise_recall`, `superseded_precision`, core `recall_fidelity_*`,
  `recall_episode_*`, `workload_session_distinct_threads`, `workload_topic_dwell_runlen`.

## 6. Acceptance criteria (metric-based, on the 2w→120d ladder)

Run the existing rung ladder (2w, 1m, 3m=120d per SPEC §9.1) via the make integration target.

(a) **Threads are genuinely non-monotonic (#96 exercised):**
   - `workload_thread_topics_distinct` mean > 1.0 and a tail reaching the wander-cap (≈4),
     across ALL rungs (not just campaign threads). Today this would be ≈1.0 for normal threads.
   - `workload_thread_wander_hops` non-zero for a target fraction (~the wander rate × eligible
     engagements) of the normal-thread population.

(b) **Recall of CURRENT topics holds; abandoned-topic recall decays GRACEFULLY with wander
   distance (policy A — deep wander ~4 hops, measured decay).** The pass condition here is
   ORACLE COHERENCE, not a high recall floor:
   - `wander_current_recall` ≥ ~0.95 (a thread's current topic always surfaces).
   - Core symbolic-only `recall` (recall_fidelity recall component) for current-topic queries
     stays ~0.95 flat across rungs — wander must not depress recall of *current* topics.
   - `wander_origin_recall_byhops` is NOT a flat floor. The PREDICTED curve, from Jaccard math
     (5-tag query vs the thread's full retained set): ~0.85+ at 1-2 hops (retained set ≈10-11
     symbols → 5/11 ≈ 0.45 ≥ 0.4 → hit), decaying toward ~0 by 3-4 hops (retained set ≈16-21 →
     5/16 ≈ 0.31, 5/21 ≈ 0.24 < 0.4 → miss). This decay is EXPECTED and CORRECT: symbolic
     Jaccard cannot retrieve a premise buried under several later topics — that is precisely
     the gap §3.4 layer-2/3 (embedding + model judgment, nil in this sim) fills in production.
     The decay curve is a deliverable (the quantified case for the embedding layer), not a
     regression.
   - **The actual pass condition: the runtime's observed abandoned-recall-by-hops MATCHES the
     oracle's predicted hits/misses at every hop distance.** Oracle and runtime agreeing on the
     misses is coherence (the whole crux). A *divergence* between oracle-predicted and
     runtime-observed recall at any hop distance is the failure — NOT a low recall value the
     oracle correctly predicted.

(c) **Saturation artifact controlled by #95:**
   - F1 across 2w→120d flattens: the precision-driven F1 decline must be materially smaller
     than the current 0.937→0.850 slope (target: precision floor held within a few points
     across the ladder; exact target tuned on the first instrumented run, then pinned as a
     regression gate). Decision rule: precision must NOT decline monotonically with Duration
     once salting is on — that monotone-with-N signature is the artifact's fingerprint.
   - `ever_central` mean and `history_len` mean per thread stay **flat across rung length**
     (the steady-state property the 6m ladder already proved; wander+salt must not break it,
     since trajectory and salt are bounded — §4.3). Cap-overflow / projection violations: zero.
   - Distinguish artifact from genuine miss: a wander-induced precision dip should appear as
     *false positives among non-sibling dormant threads* (oracle expected-set excludes them,
     they fire) — assert FP threads are non-trajectory-overlapping, i.e. genuine collisions,
     not oracle/runtime divergence.

(d) **Determinism preserved:** the existing determinism test (drainSteps, zero feedback)
   still produces a byte-identical canonical stream for fixed (Seed, Duration, Corpus).

(e) **No eviction in range (coherence precondition, §4.3):** max per-thread retained symbol
   count < the eviction cap (40) on every rung. If violated, the oracle's no-eviction
   assumption breaks and the design must add eviction modeling — assert this explicitly.

## 7. Risks & open questions

1. **(Primary) Active/superseded labeling divergence under weight<1.0.** The recall-match
   set is eviction-free union Jaccard (low risk, §4.3). But `superseded_precision` /
   weighted scoring need the active-vs-superseded label, and the generator's shadow projection
   re-implements `symbolRankLess`. Tie-breaks (equal Count → recency → Normalized) must match
   the runtime EXACTLY or the label diverges on contended turns. Mitigation: at the ladder's
   default weight 1.0 the label is moot for recall; only the `superseded_precision` *metric*
   reads it. Recommend: keep the ladder at weight 1.0 for the #96/#95 acceptance, and treat
   the weighted-scoring oracle label as a separate, later concern.
2. **Wander rate calibration.** Too low → not enough non-monotonicity to be realistic; too
   high → density blows past the eviction cap and breaks the no-eviction precondition.
   Best-guess-then-iterate on the rung walk (per `feedback_best_guess_then_iterate`).
3. **Salt and high-specificity.** `saltSymbol` must NOT trip `IsHighSpecificity` (projection.go
   rankClass) — a salt that looked like a URL/SHA would get a projection class boost and
   distort ranking. The `t%dz` form is a plain lowercase identifier; verify against
   `memops.IsHighSpecificity` in a unit test.
4. **Campaign redundancy.** Once normal threads wander with the same supersession mechanic,
   the scripted drift/invert campaigns may be a strict subset. Open: keep campaigns as
   *controlled high-signal* probes (deterministic single-trajectory oracle) vs fold into the
   stochastic model. Recommend keep — they are the cheap canary; the stochastic population is
   the realism. No action needed for this design.
5. **Family binding vs wander.** A wandered thread leaves its birth family's slot. Its
   *initial* sibling recall opportunity (creation-order family) still works on early turns
   before it wanders; after wandering, recall is exercised by the trajectory oracle instead.
   Verify the family-pair recall opportunity still fires *before* the second member wanders
   (dwell gate `wanderMinDwell` ≥3 protects this window).

## 8. Production-change surprise check

**None found.** The runtime already: accretes evolving history_symbols, projects top-8,
supersedes by rank-dropout with EverCentral retention, and recalls over
`anchors ∪ history_symbols`. Every #96/#95 need is satisfied by changing what the generator
*emits* and how the oracle *predicts*. If implementation discovers the shadow projection
cannot be made to match runtime tie-breaks without reading runtime code, that is the trigger
to escalate (§9) — but it would still not require a *production* change.

## 9. MAD-escalation recommendation

**Recommendation: implement in gated increments from this design. Do NOT convene a full MAD.**

Reasoning:
- The crux (oracle coherence) **collapses** under the §4.3 observation that the runtime's
  recall set is the eviction-free retained-symbol union, and the generator's threads provably
  stay under the eviction cap. The hard, history-dependent, projection-mirroring problem
  reduces to "Jaccard of the emitted-tag set against a shared threshold constant" — a
  well-bounded, independently-verifiable computation. That is not the genuine-contention
  signature that warrants a MAD.
- The one residual contention (active/superseded labeling under weight<1.0, risk #1) is
  *sidestepped*, not solved, by holding the acceptance ladder at weight 1.0 — and it is
  orthogonal to #96/#95's actual goal (realistic wander + density). It can be its own scoped
  task if a sub-1.0 weight rung is ever required.
- The remaining unknowns (wander rate, salt form) are calibration knobs resolved by the
  best-guess-then-iterate rung walk, not design-time debate.

Gated increments:
1. **#95 salting + density metric** (salt emission + `recallExpectedFor` denominator,
   shadow-set Jaccard oracle WITHOUT wander). Acceptance: precision-vs-Duration slope flattens
   (criterion c) with monotonic threads — isolates the density fix.
2. **#96 wander + trajectory + shadow bookkeeping** on top of (1). Acceptance: criteria (a),
   (b), determinism (d), no-eviction (e).
3. **New recall buckets + metric wiring + rung gate pinning.** Acceptance: full ladder green;
   pin the measured precision floor as a regression threshold.

Escalate to MAD ONLY if increment 2 reveals the shadow projection diverges from runtime
labeling in a way that corrupts the *recall* set (not just the superseded metric) — i.e. if
the §4.3 eviction-free assumption fails at the 120d rung (criterion e red). That is the single
falsifiable trip-wire.
