# Burn-down 2026-07b — live-run findings + startup recovery

**Provenance.** Successor to `review-burndown-2026-07.md` (Waves 1–5,
complete at `4d4f5d9`; live-run findings recorded at `eac048a`). Seeded
by the two D4 live runs (2026-07-13/14): the 1d live-inference failure
(missing-tag abort), the 14d live-embedding PASS (with its watch items),
and the carried follow-ups from the July doc. Same process rules: EDIT
GATE per item, one CHECKPOINT GATE (`make test`) per wave commit,
adversarial review on design-content items.

**Decisions (D6–D7 RATIFIED 2026-07-14; D8 POSTPONED — #94 and its
MAD-design will be run separately by the user; Wave C is out of this
burn-down's execution scope):**

- **D6 (missing-tag recovery policy — the headline).** When a model
  response arrives with NO topic tag on a turn whose deltas require
  owner binding (e.g. `fs.write`), the runtime currently hard-aborts
  (§3.0/§3.3 protocol violation — 1d live run, turn 91).
  *Recommended:* single re-prompt for the tag (reusing the §5.5
  re-issue machinery and its cap of 1 re-prompt/turn — consistent with
  existing design); if the re-prompt also lacks a tag, fall back to
  binding the turn to the current owner thread with a
  `thread.tag-defaulted` forensic log line; never abort a turn for a
  missing tag. Alternatives: owner-default immediately (zero cost,
  higher misbind risk), or keep abort (hostile; refuted by live data).
- **D7 (B1 gate span-conditionality).** *Recommended:* the completeness
  gate distinguishes "structurally cannot probe at this span" (main
  thread cannot scroll past ThreadTurnWindow + debt cap) → SKIP with a
  logged note, from "could have probed and didn't" → FAIL. Alternative:
  arm the gate only on explicitly-designated rung profiles. Either way
  `make sim LIVE_EMBEDDING=true` must go green when the actual rung
  passes.
- **D8 (#94 design process).** Startup recovery is fresh
  crash-correctness design (not accreted territory). *Recommended:*
  run it through the MAD-design process before implementation — the
  failure modes (mid-drain archival crash, stale derived state, torn
  spine writes) are exactly the class where adversarial design pays.

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
| B3 | **Shadow-LRU closure/displacement modeling** (carried, BD-4 residual): model closure/retirement and persistent carrier displacement in the generator shadow to recover a reverse ==0 Layer-B gate | reverse divergence gated ==0 on mock rungs (363 → 0 explained) |

## Wave C — #94 startup recovery (the major build; D3 from July)

Design first per D8 (MAD-design), then implement SPEC §4.5.8:
unclean-shutdown detection, derived-state reconcile/rebuild on open,
archival mid-drain repair (the un-stamped `archive/index.jsonl` entries
the July review flagged as load-bearing), and the normal
shutdown→resume cycle exercised in the sim (restart-session machinery
exists in the harness). Acceptance: crash-injection tests across the
archival three-commit window; sim rung with mid-run restart passes all
invariants; SPEC §4.5.8 updated from design output.

## Watch / calibration (tracked, no wave)

- hop-0 embedding `wander_current_recall` 0.882 vs ~0.95 aspiration
  (14d rung; report-only — investigate threshold/query composition if
  it persists on the next long rung).
- #98 sampled-inference sweet-spot: live per-turn cost measured at
  ~29 s (3× estimate) — the coverage/cost arithmetic for
  inference-in-loop needs the sampling design before any long
  live-inference rung.
- Flush re-enqueue-on-failure: parked; 14d run showed zero flush
  failures. Revisit on live-run evidence.
- A2 anchor-emission overlap is a RUN-LEVEL realization of the per-turn
  intent: the event log carries no per-turn model-vs-deterministic
  source split, so the grader reads the per-thread frontmatter
  accumulation (mean Jaccard over threads with ≥1 model anchor). A true
  per-turn series needs a one-line `topic.tag-parsed` runtime hook
  logging each turn's model-emitted anchor set (recommended future).
- Dogfood feedback channel: REPL is dogfood-ready; ack-edit-rate canary
  and parked §9.1 category-3 items collect from real use (front-end
  phase owns evaluation).
- **Sequencing decision (user, 2026-07-14): after this burn-down and
  the #94 crash-stability design+implementation, the next phase is a
  "living with" period** — daily real use of personant. Purpose: (a)
  first data on human tolerance of the interaction demands (ack timing/
  frequency/quality — the load-bearing element no sim can validate);
  (b) center design choices for further platform capabilities on lived
  experience rather than speculation. The §2.8 event log is the passive
  instrument (retire.ack edited=, recall accept/decline reasons,
  tag-defaulted lines); evaluation is a periodic log/metrics review,
  not new machinery.
