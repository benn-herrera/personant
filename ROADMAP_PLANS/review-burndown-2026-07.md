# Review burn-down — July 2026

**Provenance.** Five-subsystem parallel code review (2026-07-13, session `fable-review`) over
`initial-implementation` at `b8c8445`, synthesized post-merge of the `worktree-pending` chain (M1
/ #127 / B1,X4 / de-mirror — tip `324dc94`). Subsystem scopes: turn, recall, substrate/port, sim
harness, periphery. This document is the designed-before-coded record for the fixes;
harness-touching items inherit the ARCHITECTURE.md "Testing as the lab bench" discipline.

**Decisions taken (2026-07-13, user-ratified):**

- **D1 (zero-overflow semantics post-#127):** the X4 whole-request token-ceiling gate is the
  *normative* §9.1 zero-overflow criterion; a cheap per-layer byte `VerifyNoBudgetOverflow` is added
  for mock rungs as belt-and-suspenders; delta-time budget enforcement (T3-3) stays deferred. SPEC
  §9.1/§9.4 + ARCHITECTURE invariant table updated to say so (BD-6).
- **D2 (phantom-owner fallback):** when the topic tag names a nonexistent or cross-project-declined
  thread as owner, fall back to the most-recent *valid* engaged thread; else create a new thread
  from the turn; never drop the excerpt (BD-7).
- **D3 (#94 startup recovery):** out of scope for this burn-down — too large to ride along. It is
  the next major item in the queue after the post-merge live run.
- **D4 (live rungs):** B1/X4 live profiles stay opt-in (too costly per turn for `make test`). After
  this burn-down merges, run a 1-week live-enabled sim (`LIVE_EMBEDDING=true LIVE_INFERENCE=true
  DURATION=1w`) — observational, not gating; results may seed the next burn-down.
- **D5 (B2 remedy):** verify-first. Reproduce the >cap unflushed window with a test before touching
  the floor; if real, widen the lexical floor bound per the SPEC's own in-flight-continuity logic.

**Process rules.** Per-item EDIT GATE only (`make build` + `make test-run PKG=… RUN=…`); one full
`make test` CHECKPOINT GATE per wave, at the wave's commit. Waves 3 and 4 parallelize across coders
(disjoint files); Wave 1 lands before Wave 2 so bug fixes are measured by a hardened instrument.
MAD-style adversarial review applies to BD-7 and BD-8 (design content); remaining items are
review-provenance mechanical fixes. Docs (SPEC/ARCHITECTURE/AGENTS/README) sync at each wave
checkpoint that changes behavior.

---

## Wave 1 — instrument integrity

| ID | Item | Acceptance |
|---|---|---|
| BD-1 | Move every cross-seam metric key into `metric_keys.go`: `recall_unexplained_absence`, `turns`, `threads_created`, `turn_duration_ms`, `recall_fidelity_*`, `embed_recall_fidelity_*`; sweep for any remaining raw literals crossing the harness↔sim seam | no raw key literal crosses the seam; rename ⇒ compile error |
| BD-2 | Runtime-driven contract test pinning the `thread.created` log marker (pattern: `TestRuntimeEmitsMatchFireMarker`); `VerifyThreadAccounting` two-directional and non-vacuous (fails when created-set is empty while spine grew) | marker rename breaks a test, not a measurement |
| BD-3 | Export W1 classification labels from `recall/measure` (single source); harness references the exported symbols; delete the comment-only mirror in `harness_run.go` | same pattern as `turn.EmbeddingDebtCap` (324dc94) |
| BD-4 | Two-sided Layer-B shadow cross-check: detect shadow-retains/runtime-evicted divergence, not only the subset direction | forward gated ==0; reverse RESIDUAL RECOVERED (2026-07-14, B3): the reverse direction is now checked against a runtime-mirror (`rtActive`) that models the two legitimate eviction sources the original amendment deferred — closure/retirement (§3.5 turn-idle decay) and persistent carrier displacement — plus archival traced non-contributing. Reverse divergence collapsed 363 → 0 and is now a hard ==0 gate on mock rungs (report-only under live inference, shadow id-blind). |
| BD-5 | Implement `VerifyArchiveResolvable` and `VerifyDedupConsistency` (features shipped; stubs still say v0.2) | stubs replaced; invariants run in `DefaultInvariants` |
| BD-6 | Execute D1: SPEC §9.1/§9.4 + ARCHITECTURE updated (X4 = normative overflow gate; T3-3 deferral noted); add mock-rung per-layer byte `VerifyNoBudgetOverflow` | doc + invariant land together |

## Wave 2 — runtime correctness

| ID | Item | Acceptance |
|---|---|---|
| BD-7 | Phantom engaged threads (`turn/engage.go`): engaged-miss / cross-project-declined IDs must not enter `ActiveThreads` or own turns. Fallback per D2: valid-engaged → new-thread → never drop. MAD review | tests for both paths; no phantom ID persisted; excerpt never lost |
| BD-8 | §3.4 dead zone, verify-first per D5: test drives unflushed depth >cap (slow-embedder mock); if reproduced, widen `debtWindowBound` to cap+in-flight and sync SPEC §3.4. MAD review | either a passing proof the window can't exceed cap, or floor widened + SPEC synced |
| BD-9 | `RecoverThread` stamps recovery turn/time (`LastEngagedTurn`, `last_engaged`) so the closure scan can't immediately re-retire a recovered thread | test: recover → closure scan → no retire prompt |
| BD-10 | Unknown `--provider` fails loud, naming the unknown value and available providers (no alphabetical fallback); fix empty-name error message | REPL bootstrap test |
| BD-11 | Aging gate checks blob-at-path (commit *and* `hash:path` present), so gate and `GetFileVersion` genuinely share one predicate | test: commit reachable but path absent ⇒ refuse aging |

## Wave 3 — concurrency / robustness (parallel dispatch)

`EnqueueFlush` after `Close` (guard; no panic, no unbounded block) · `.vec` manifest mutex (indexer
`Write` vs sleep-cycle `Sweep`/`WriteTree` RMW race) · two-CAS-writer invariant documented at the
publish sites (`measure.go` "sole writer" comments corrected; `RebuildTrees` contract stated) ·
`internal/log`: atomic `showFileLine` read, safe path-prefix trim, `LevelNone` uint32 clamp ·
streamed tool-call delta merge (or explicit unsupported-guard) · dedup LCS size guard (fall back to
full-literal version above an n×m cap) · fmcache deep-clone `DerivedFrom`
+ aliasing regression test · small-fry: closure log-error no longer aborts the remaining scan;
  `coldnessKey` parse-time ordering; curator retry scoped to unsupported-stream errors; Score-0
  lexical debt hits rank after scored candidates; `modellist`/`ping` surface provider-load faults.

## Wave 4 — DRY / dead code (parallel dispatch)

Unify recall-accept + reprompt fetch-through-chain into one §3.0.5 chokepoint helper ·
`processJob`/`reconcileThread` shared reuse/re-embed projection; `Kf` ↔ `scoring.DefaultChunkLimit`
single constant · delete `index.RebuildSymbols` (zero callers) · `spineIDSet` ↔ `liveSpineThreadSet`
unify · http client `doRequest` helper (collapses 4× boilerplate) · wire the SPEC-required
`workset.warning` on layer render failure (removes dead `ComposeOptions.Logger`) · move the
daily-record schema + snapshot writer from `sim_test.go` to `telemetry.go` (makes the documented
file-responsibility table true) · delete `promptConfirmation` dead loop; un-shadow builtin `cap` in
`turn.go`.

## Wave 5 — doc sweep (single commit)

§2.8 event vocabulary gains `archive.*`, `fs.*`, `dedup.*`, `session.*`, staging events ·
`ParentCommitHash` added to SPEC archive-index schema · harness state-ownership map: archived-set
row (index-derived via `refreshArchivedSet`, not log-folded), telemetry.go row (post-BD Wave 4 move)
· §11.x → §9.x numbering residue; "six-month" residue · SPEC §5.1.3 hot-reload claim corrected ·
ARCHITECTURE invariant table synced to the real validator set.

---

## Live-run findings (2026-07-13/14, D4 runs — seed the next burn-down)

- **HEADLINE — missing-topic-tag recovery policy.** 1d live-inference run (gemma-4-main via reaper):
  the model ran ~90 coherent turns, then omitted the topic tag on an `fs.write`-bearing turn; the
  runtime treats this as a fatal §3.0/§3.3 protocol violation and aborts the turn (`fs.write without
  topic tag … substrate state not advanced`). Real models WILL intermittently omit tags — the
  runtime needs a defined recovery (single re-prompt for the tag, or owner-thread default + forensic
  log), not an abort. This is a realism-convergence element in its own right; the §9.1 tag-fidelity
  backlog entry now has its first empirical datum.
- **Live per-turn cost ≈ 29 s** (gemma-4-main, whole-turn), ~3× the single-digit-seconds planning
  estimate — sharpens the #98 sweet-spot hunt's budget arithmetic.
- **Shadow oracle is structurally blind under live inference** (expected; why the 1d cap exists):
  forward Layer-B divergence WARNs continuously because the scripted shadow cannot track a live
  model's thread decisions. Any future live-inference rung needs either a runtime-derived oracle or
  gates scoped to live-safe measurements.
- **B1 completeness gate span-conditionality** (mechanism confirmed on the 14d run): the gate arms
  package-wide whenever `-sim.live-embedding` is set, so the two head-to-head *machinery* tests —
  which drive fixed 24h internal workloads regardless of DURATION — fail by construction (24h
  structurally cannot scroll a probe into the dead zone). The gate must distinguish
  "structurally-cannot-probe at this span" (skip with note) from "should-have-probed and didn't"
  (fail), or arm only on the rung whose span qualifies. Until fixed, `make sim LIVE_EMBEDDING=true`
  reports package FAIL even when the actual rung passes.
- **14d live-embedding rung (TestSim): PASS, strong.** 11,790 turns / 600 threads; W1 divergence **0
  over 359 probes** (strict_miss=0 tie=0 tree_mismatch=0 — the exemplar-set keys + k=8 +
  watermark-tie fix hold at 14d); **completeness floor 33/33 dead-zone probes surfaced** — the
  (1+P)×cap floor proves §3.4 non-vacuously under live embedding; embedding recall 0.940 vs symbolic
  0.933 with the expected precision gap (0.093 — the layer-3 judgment motivation); drift hop-curve
  validates the layered design live (symbolic collapses to 0.028/0.000 at hops 2/3, embedding holds
  0.556/0.409); intra-thread hop recall 1.000 across hops 2–11; latency P50 109ms / P95 564ms / P99
  2.76s; 2 sleep cycles, git gc reclaimed ~383 MB. Watch item: hop-0 embed wander_current_recall
  0.882 vs the ~0.95 aspiration (report-only; symbolic 0.345).
- One non-monotonic generator step under live latency (warn-only tripwire fired; step 51, Step.At <
  pinnedClock) — harness follow-up.

## Post-merge follow-ups (not in this burn-down)

1. **1-week live-enabled sim** (D4) — observational; results may seed the next burn-down.
2. **#94 startup recovery/reconcile** (D3) — next major queue item.
3. Deferred watch items carried from review: `store.ThreadTurnWindow` direct import in
   `turn/engage.go` (documented concession vs. port move — revisit if a second adapter arrives);
   unbounded `DormantThreads` count under slow closure (sim will measure); non-ASCII symbols
   invisible to `exact.isWordRune` lexical floor.
4. Added during Wave 2 (adversarial-review residue): sim slow-embedder mode so the (1+P)×cap
   transient band becomes observable and the B1 dead-zone classifier can widen meaningfully (until
   then the 1×cap band is the deterministic lexical-only band, documented in `intra.go`); shadow-LRU
   closure/carrier-displacement modeling to recover a reverse ==0 Layer-B gate (BD-4 amendment —
   DONE 2026-07-14 as B3: runtime-mirror `rtActive`, reverse divergence 363 → 0, now gated); single
   re-enqueue-on-failure for flush jobs if the session-bounded failed-embed gap ever shows up in
   live-run data. Wave 4 pickup: delete `store.CommitReachable` (zero production callers since BD-11
   moved the gate to `BlobReachable`). Wave-5 residue (comment-only, deferred): ~13 "six-month"
   phrasings in Go comments across turn.go/archival.go/workload.go/clock.go/
   thread_io.go/threadfiles.go/memops.go/autogit.go/sim_rung_test.go — the gate is
   four-month/120-day; sweep opportunistically when those files are next touched.
