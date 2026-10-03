# Burn-down 2026-07b — live-run findings + startup recovery

**Provenance.** Successor to `review-burndown-2026-07.md` (Waves 1–5, complete at `4d4f5d9`;
live-run findings recorded at `eac048a`). Seeded by the two D4 live runs (2026-07-13/14): the 1d
live-inference failure (missing-tag abort), the 14d live-embedding PASS (with its watch items), and
the carried follow-ups from the July doc. Same process rules: EDIT GATE per item, one CHECKPOINT
GATE (`make test`) per wave commit, adversarial review on design-content items.

**Decisions (D6–D7 RATIFIED 2026-07-14; D8 POSTPONED — #94 and its MAD-design will be run separately
by the user; Wave C is out of this burn-down's execution scope):**

- **D6 (missing-tag recovery policy — the headline).** When a model response arrives with NO topic
  tag on a turn whose deltas require owner binding (e.g. `fs.write`), the runtime currently
  hard-aborts (§3.0/§3.3 protocol violation — 1d live run, turn 91). *Recommended:* single re-prompt
  for the tag (reusing the §5.5 re-issue machinery and its cap of 1 re-prompt/turn — consistent with
  existing design); if the re-prompt also lacks a tag, fall back to binding the turn to the current
  owner thread with a `thread.tag-defaulted` forensic log line; never abort a turn for a missing
  tag. Alternatives: owner-default immediately (zero cost, higher misbind risk), or keep abort
  (hostile; refuted by live data).
- **D7 (B1 gate span-conditionality).** *Recommended:* the completeness gate distinguishes
  "structurally cannot probe at this span" (main thread cannot scroll past ThreadTurnWindow + debt
  cap) → SKIP with a logged note, from "could have probed and didn't" → FAIL. Alternative: arm the
  gate only on explicitly-designated rung profiles. Either way `make sim LIVE_EMBEDDING=true` must
  go green when the actual rung passes.
- **D8 (#94 design process).** Startup recovery is fresh crash-correctness design (not accreted
  territory). *Recommended:* run it through the MAD-design process before implementation — the
  failure modes (mid-drain archival crash, stale derived state, torn spine writes) are exactly the
  class where adversarial design pays.

---

## Wave A — runtime (tag protocol + instrumentation)

| ID | Item | Acceptance |
|---|---|---|
| A1 | **Missing-tag recovery** per D6: re-prompt-once → owner-default → never abort. Includes a mock-LLM tag-omission mode so the recovery path runs under `make test`, not just live. SPEC §3.3/§5.1 sync. Adversarial review (design content) | tag-omission scenario test: turn completes, edits bind, forensic line emitted; live 1d rung reruns past turn 91 |
| A2 | **Tag-fidelity instrumentation** (SPEC §9.1 entry, first datum in hand): re-engagement miss rate, spurious `*new-topic*` rate, anchor-emission overlap vs deterministic pass — emitted by the live-inference rung profile, keys in `metric_keys.go`. **Grader (ratified 2026-07-14):** ground truth is plan *intent*, not shadow thread-ids (shadow is id-blind under live inference). Hybrid: symbolic anchor-overlap fast path → embedding adjudication of the non-overlapping residue only, judged RANK-based (tagged thread top/near-top cosine match for the prompted content; C.6: ranking is drift-invariant, thresholds are not), cosines computed measurement-side against the plan's topic content (never through the runtime recall stack — oracle independence). Borderline adjudications log their cosine ranks rather than silently binning; residue clustering at the rank boundary escalates to human review, not instrument trust. | rerun of the 1d rung produces the three metric series |
| A3 | Non-monotonic generator step under live latency (warn tripwire, step 51): root-cause and fix the Step.At ordering | tripwire silent on a live rerun; regression test |

## Wave B — harness (gate scoping + oracle reach)

| ID | Item | Acceptance |
|---|---|---|
| B1 | Completeness-gate span-conditionality per D7 | `make sim LIVE_EMBEDDING=true` green end-to-end when the rung passes; skip-note visible for unscrollable spans |
| B2 | **Sim slow-embedder mode** (carried): deterministic embed-latency injection so the (1+P)×cap transient band is observable; widen the dead-zone classifier alongside it (the intra.go comment's stated precondition) | a mock rung exercises P≥1 dead-zone probes; classifier band matches runtime bound |
| B3 | **Shadow-LRU closure/displacement modeling** (carried, BD-4 residual): model closure/retirement and persistent carrier displacement in the generator shadow to recover a reverse ==0 Layer-B gate. **DONE 2026-07-14** — residual recovered via the execution-time runtime-mirror (`rtActive`): §3.5 turn-idle decay closure + persistent carrier displacement modeled, archival traced non-contributing to the reverse divergence. | reverse divergence gated ==0 on mock rungs (363 → 0; closure + carrier displacement modeled, archival non-contributing; hard ==0 gate on 1d and 14d mock rungs via `TestShadowLayerB_ReverseDivergence`, report-only under live inference) |

## Wave C — #94 startup recovery (the major build; D3 from July)

Design first per D8 (MAD-design), then implement SPEC §4.5.8: unclean-shutdown detection,
derived-state reconcile/rebuild on open, archival mid-drain repair (the un-stamped
`archive/index.jsonl` entries the July review flagged as load-bearing), and the normal
shutdown→resume cycle exercised in the sim (restart-session machinery exists in the harness).
Acceptance: crash-injection tests across the archival three-commit window; sim rung with mid-run
restart passes all invariants; SPEC §4.5.8 updated from design output.

## Live rerun results (2026-07-14, 1d live-inference, post-Wave-A — A1/A2 acceptance)

Full span PASS (~8.6 h, 1,015 turns): zero aborts. **A1 recovery live-proven** — 32 missing-tag
re-prompts on binding turns, all recovered (the class that killed the 2026-07-13 run at turn 91).
First A2 series (gemma-4-main):

- stream-level tag omissions: 349/1015 (~34% — 317 conversational tag-missing + 32 binding
  re-prompts)
- spurious `*new-topic*` rate: 0.194 (183)
- re-engagement miss rate: 0.847 (797/941; borderline=0, unadjudicated=0)
- anchor-emission overlap: 0.000 over 151 threads

**RESOLVED (2026-07-15, three-round live elicitation probe — 600 calls,
`internal/prompt/*_probe_test.go`, artifacts in `test/rundata/elicitation_probe/`):** the omission
driver is the **work-switch genre**, not model capability and not prompt placement/format. Round 1:
100% single-turn compliance across placement/format variants at production pins (static prompt
exonerated; prefill unsupported on reaper). Round 2: history-precedent mimicry real but minor (4% at
saturated k=12 tag-less history; production History replays tag-INTACT — only excerpts strip). Round
3: work-switch turns drop to 88% (Fisher p≈0.013 vs V0's 100%); excerpt-format Layer B adds NOTHING
(E: 0 real omissions; EG ≈ G). Miss signatures: (a) clarify-question-without-tag on ambiguous
routing; (b) bare `*new-topic* [anchors]` — the model unwraps the confusable nested- asterisk
syntax; (c) full-budget hidden-reasoning burns returning empty content (switch-genre exclusive).
12%/switch-turn × sim switch density × own-omission feedback ≈ composes toward the observed 34%.
**Fixes indicated:** (1) directive clause — the tag is required even on clarifying/ambiguous
responses (best-guess or new-topic); (2) parser alias accepting bare `*new-topic* [anchors]` (absorb
the confusable syntax at the deterministic tier, per minimize-infrastructural-prompts); (3) extend
the D6 re-prompt to cover empty responses; track reasoning-burn empties as a serving-level hazard.
Layer-B excerpt tag retention: exonerated, do not build.

**CLOSED (2026-07-16).** Post-fix 1d live acceptance run + forensic decomposition: of 380 apparent
omissions, **364 (96%) were attempted tags** mangled by a third nested-asterisk variant (`*topic:
*new-topic [...]​*` — inner literal unclosed); true never-attempted omissions ≈ 16/1038 (~1.5%). The
d0e4a52 fixes worked (attempts way up, spurious 0.194→0.173, miss 0.847→0.771); the final parser
tolerance (accept the unclosed inner literal in a valid wrapper) recovers **363/380 on offline
replay of the captured real bodies** — the replay harness (`PERSONANT_TAGMISS_REPLAY_DIR`) is the
zero-cost acceptance instrument, per the testing-cost regime. Instrument fixes landed alongside: run
manifest.json per rundata dir (mock/live confusion caused a false integrity alarm on 2026-07-16 —
investigated, no actual integrity loss), per-turn grader join (instant-grouping smeared 380→256
groups), anchor-overlap honestly UNMEASURED on this workload (deterministic identifier vocabulary is
disjoint from anchor vocabulary; staging GC drops uncited identifiers — structural, not a bug).
Residuals for the watch list: 16 true omissions correlate with serving-side loop-detection artifacts
("CRITICAL LOOP DETECTED" bodies — serving-level hazard); one-off double-anchor-list shape not
absorbed; ~15 pre-existing files with gofmt drift flagged for a separate sweep; grader join falls
back degraded on pre-manifest logs.

**Interpretation discipline (before drawing the big conclusion):** the top-line signal —
gemma-4-main's tag discipline is far below mock assumptions — is almost certainly real (34% omission
is model behavior, full stop). But two numbers need instrument-vs-subject decomposition before they
drive design: (a) the exact-0.000 anchor overlap across 151 observations smells like a
source-attribution/join artifact (verify model-emitted anchors actually persist as `source=model`
under live tags), and (b) the 0.847 miss rate is inflated to an unknown degree by spurious-new-topic
duplicates splitting cosine mass across clones (the "right" thread's rank degrades as its
near-duplicates accumulate). Verification of both is the first task of the next pass; the family-
specific topic-tag prompt tuning (model-family layer-2 work) is the likely remedy either way, with
recall layer-3 as backstop.

- hop-0 embedding `wander_current_recall` 0.882 vs ~0.95 aspiration (14d rung; report-only —
  investigate threshold/query composition if it persists on the next long rung).
- #98 sampled-inference sweet-spot: live per-turn cost measured at ~29 s (3× estimate) — the
  coverage/cost arithmetic for inference-in-loop needs the sampling design before any long
  live-inference rung.
- Flush re-enqueue-on-failure: parked; 14d run showed zero flush failures. Revisit on live-run
  evidence.
- A2 anchor-emission overlap is a RUN-LEVEL realization of the per-turn intent: the event log
  carries no per-turn model-vs-deterministic source split, so the grader reads the per-thread
  frontmatter accumulation (mean Jaccard over threads with ≥1 model anchor). A true per-turn series
  needs a one-line `topic.tag-parsed` runtime hook logging each turn's model-emitted anchor set
  (recommended future).
- Dogfood feedback channel: REPL is dogfood-ready; ack-edit-rate canary and parked §9.1 category-3
  items collect from real use (front-end phase owns evaluation).
- **Sequencing decision (user, 2026-07-14): after this burn-down and the #94 crash-stability
  design+implementation, the next phase is a "living with" period** — daily real use of personant.
  Purpose: (a) first data on human tolerance of the interaction demands (ack timing/
  frequency/quality — the load-bearing element no sim can validate); (b) center design choices for
  further platform capabilities on lived experience rather than speculation. The §2.8 event log is
  the passive instrument (retire.ack edited=, recall accept/decline reasons, tag-defaulted lines);
  evaluation is a periodic log/metrics review, not new machinery.
