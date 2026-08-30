# Personant — Roadmap

This file owns **future intent**. It sits **outside** the contract-document
precedence chain (`SPEC.md` > `ARCHITECTURE.md` > `AGENTS.md` > code):
those three state only the *now* — current behavior, current constraints,
current scope boundaries — and cross-reference here for anything not yet
built. Read this file for planning. Do not hand it to a coding agent as a
work order; an item graduates into a contract document (with acceptance
criteria) before it is built, per the project's planning discipline.

Entries carry a source citation (`file §section` or line-era pointer) back
to wherever the item was previously described in-line, so provenance
survives the migration that created this file (2026-08-29).

---

## 1. Port from GNU make to just

Top priority, user-specified. No source citation — this is new intent, not
a migrated item.

## 2. Restore agents-dependency pull/maintenance machinery

The old Makefile `agents` / `update-agents-dependency` targets (submodule
pinning for the shared agent-definition set) were retired in favor of a
plain symlink from `.claude/agents` to a cross-project agents repository.
The pull/maintenance machinery — a mechanism to update that shared source
on demand rather than relying on the symlink target being current by hand —
is planned but not yet rebuilt. (User statement, 2026-08-29.
`AGENTS.md`'s repo-tree comment describing the Makefile has been corrected
to drop the stale "agents-submodule pinning + serve-local-api" claim,
neither of which the current Makefile does.)

---

## Near-term

Concrete, mostly single-package items; several unblock other queued work.

- **Build the `fs.*` tool surface.** `fs.read`, `fs.list`, `fs.grep`
  (read/think) and `fs.tmp_write`/`fs.tmp_read`/`fs.tmp_list`/
  `fs.propose_promote`/`fs.propose_rename`/`fs.propose_delete`
  (draft/mutate) are fully specified (`SPEC.md` §6.1.1–§6.1.2) but not
  implemented — as of front end 0.0.9 only the seven `web.*` query tools
  are built and registered (`SPEC.md` §6.1.1 "Status:" line;
  `ARCHITECTURE.md` "Tool surface" section). This is the rest of the
  bounded 12-tool surface the architecture already commits to.
- **Recall layer-3 model judgment.** Design direction captured, build
  gated on inference-in-loop (#98) landing the live-model seam: a
  `[recall]` provider/model config (small ~8K context ceiling, default
  E2B) that judges/reranks top-N embedding candidates before offering at
  most 3. Empirical discipline: sweep E2B → E4B → 26B-A4B against the
  embedding-only precision baseline (already collected, C.6 head-to-head)
  and pick the smallest passing tier. (`ARCHITECTURE.md` "Recall
  mechanisms" + "Deferred" list; `SPEC.md` §8.2.2 `[recall] model`.)
- **Directive-file plumbing for the remaining §2.6.1 parameters.**
  `closure.ack-mode` and `recall.ack-mode` are the only two parameters
  currently read live via `store.ReadParameter`/`MemoryOps.DirectiveParam`;
  everything else in `SPEC.md` §2.6.1 (`context.token-budget`,
  `staging.window-turns`, `history.cap-per-thread`,
  `anchor.projection-max`, `spine.entry-max-chars`, etc.) is still a
  compiled-in constant. (`AGENTS.md` repo-state note on §2.6.1;
  `SPEC.md` §2.6.1, §3.10.3, §3.1.)
- **Bind Alt-B/Alt-F (word-back/word-forward) in the REPL line editor.**
  The terminal decoder already reports the Meta form; the editor does not
  yet bind it, so an Alt-modified rune is inserted as its base character.
  (`SPEC.md` §4.3.1.)
- **The `git-auto` sentinel for `[user] name`/`email`.** `name` or `email`
  set to the literal string `git-auto` would resolve at invocation via
  repo-scoped `git config user.name`/`user.email` (no `--global`), so a
  session in a work tree with an `includeIf` identity picks up the
  per-project address instead of the global one. Contract sketched,
  deliberately not built pending demonstrated need. (`SPEC.md` §8.2.2.)
- **Wire the mid-day re-baseline arming trigger.** The default-off knob
  that nukes/recreates the daily DB when loose objects cross a threshold
  mid-session has its recovery-side handling and marker plumbing in place;
  the arming trigger itself is not yet wired. Adjacent: revisit whether
  intra-day derived-watermark staleness (currently parked-by-design —
  the barrier owns freshness between barriers) should get finer-grained
  handling. (`SPEC.md` §3.11, §4.5.8 "Intra-day derived staleness".)
- **Banded/auto-mode sim rung.** Both the §3.5 closure ack-mode amendment
  and the §3.4 recall banding amendment are unit-covered but not
  sim-covered; a named sim rung exercising `banded`/`auto` end-to-end is
  owed. (`AGENTS.md` repo-state, §3.4 recall-surface bullet.)
- **W1 recall-preservation follow-ups.** Re-verify
  `recall_intra_descent_divergence == 0` at a narrower beam now that the
  exemplar-set propagation fix confirms the keys carry recall (currently
  beam width k=8); build the brute-force O(n) backstop for the
  user-asserted-confidence fallback path. (`AGENTS.md` repo-state,
  intra-thread recall bullet.)
- **Runtime hot-reload of the topic-tag prompt template**, for empirical
  tuning without a rebuild. Currently `internal/prompt/template.go`'s
  `TopicTagDirective` is a compiled constant. (`SPEC.md` §5.1.3.)
- **Turn-time (delta-time) budget enforcement (T3-3).** The §3.0.2 step-4
  budget check is a documented no-op in v0.1 (guarded by
  `TestBudgetCheckIsExplicitNoOpV01`); the only live protections are the
  FIFO turn-pair cap and render-time byte truncation. Enabling delta-time
  eviction is a separate, deliberate decision, not an incremental fill-in.
  (`SPEC.md` §3.0.2 step 4, §9.1.)
- **Static-prompt-scaffolding byte accounting (#127 follow-up).** The
  layer-budget partition allocates 100% of the byte total to layers +
  `live_turn`; the ~1.7 KB static prompt scaffolding (orientation
  preamble, topic-tag directive, D6 reminders, headers) rides on top of
  the byte proxy unaccounted. Harmless against the authoritative token
  gate today; a partition that charges it a real share is deferred
  because a fixed deduction would zero out the tiny ceilings the
  acceptance tests exercise. (`SPEC.md` §3.1.)
- **F-A enrichment: LLM-summary key for the within-thread summary tree.**
  v0.1 node keys are deterministic exemplar sets
  (`recall/measure.ExemplarSummarizer`); an LLM-authored summary key at
  the same seam is deferred. (`SPEC.md` §3.4 / recall-measure internals.)
- **Recall precision/recall tuning, deferred until data demands it:**
  enrich the stored-thread symbol set beyond the current 5 anchors, and
  evaluate moving recall off symmetric Jaccard to an asymmetric overlap
  coefficient. (`AGENTS.md` repo-state, C.6 calibration-sweep bullet.)
- **Inference-/embedding-in-loop simulation.** Wire real `reaper.local`
  inference (gemma-4 family) and embedding
  (`nomicai-modernbert-embed-base-bf16`) into the acceptance sim,
  individually configurable, to close the coverage gap the mock
  LLM/nil-embedder regime leaves (most importantly, measuring
  embedding-primary §3.4 recall under realistic workload rather than
  inferring it from the separate C.6 head-to-head). Needs an empirical
  sweet-spot hunt: per-turn cost rises from ~10s of ms to single-digit
  seconds. This item **gates substrate v0.5.0 convergence** — full
  detail, honesty-clause obligations, and the "Open realism backlog"
  checklist (workload interleaving, topic-tag fidelity under real
  inference, spine-cardinality/A1-saturation stress, transient-data
  lifecycle fidelity, topic-clustering/Lens-B gaps) live in `SPEC.md` §9.1,
  which is process contract and stays there — this entry exists so the
  work is prioritizable from one list.

---

## Mid-term

- **Phase 5.** Cross-project digest, fallback dissection, directive
  accrual. (`AGENTS.md` repo-state, formerly "Queued".)
- **Math rendering delivery ladder.** Personant must eventually render
  mathematics as inline images in image-capable terminals, not raw LaTeX
  or lossy unicode. Three independently-shippable rungs: (1) **sidecar**
  — push committed turns to `laterm`'s local socket, math renders in a
  second window, no personant architecture change; (2) **inline,
  Rust-renders/Go-splices** — a laterm-derived tool emits finished
  protocol bytes, `internal/term` splices them as an opaque committed
  block; (3) **Rust front end** — the far-horizon two-executable split
  (Go substrate, Rust terminal front end) unifying laterm's renderer with
  `internal/term`. The binding-now protocol principle (semantic content
  only across any future front/back boundary — never pre-rendered bytes)
  and the ecosystem finding (LaTeX→terminal-image tooling lives in Rust,
  not Go) stay in `SPEC.md` §4.6 since they constrain current decisions.
  Also open, decided when the first rung ships: the model-output posture
  (LaTeX vs. unicode math in prompt/template). (`SPEC.md` §4.6.)
- **Archive recall (v0.2 addendum).** Matching `archive/index.jsonl`
  anchors and offering a recovery-fetch, so a deep-cold thread can
  resurface via the same opportunistic mechanism as a live one. The index
  schema already carries `anchors` + `spine_summary`, so this is additive,
  not a schema change. Deliberately out of scope for v0.1 (re-surfacing
  the coldest threads ambiently would reintroduce the cardinality
  pressure archival exists to relieve). (`SPEC.md` §3.8.4.)
- **Synthesis-attribution residual.** When a thread synthesizes a prior
  thread's idea by paraphrase with no recall hit (so no normalized symbol
  matches), `derived_from` is honestly left empty today. Attributing that
  residual is a fuzzy judgment reserved for an LLM-inference or
  human-declaration path — an explicit open decision, not built in v0.1.
  (`SPEC.md` §2.7.3 "Open (deferred)".)
- **Git tags for project open/close lifecycle navigation.** The
  thread/project *created*/*retired*/*archived* tags already ship
  (derived once/day at the day barrier); tags for a project's
  *opened*/*closed*/*switched* points are still deferred because the
  semantics are ambiguous in the current wave (a project is never
  explicitly "closed"; open/switch/cd overlap) — resolve the vocabulary
  before minting tags for it. (`ARCHITECTURE.md` former "Deferred" list;
  `SPEC.md` §4.5.8.)
- **Prompt/response timestamp index.** A dedicated metadata index
  recording when every user prompt was sent and every model response was
  received, kept strictly out-of-band (never injected into LLM context —
  timestamps in context can distort model behavior). The §2.8 event log
  carries clock stamps already; this is for efficient temporal queries
  ("when did we discuss X?"). (`ARCHITECTURE.md` former "Deferred" list.)
- **Inter-agent data-sharing security.** Deterministic containment
  boundaries for sharing Personant's memory with a client's agent system
  under NDA-class restrictions. Core principle: exclusion, not redaction —
  secret bytes never enter the model's context. Sketch: (1) prepare a
  redacted base dataset offline (allow-list, default-deny hides
  existence); (2) rebuild all derived structures (embedding index,
  `derived_from` edges, A2 digests) from the clean set only; (3) `chown`
  the prepared tree to an unprivileged guest OS user; (4) run the liaison
  inside OS-level containment (mount-namespace/jail, fail-closed on any
  reach-across attempt). Reuses the rebuild-on-open primitive and the
  submind clone mechanism below. Irreducible limit: prose
  cross-references inside allowed content require human verification at
  prep time; the deterministic layer only guarantees structural
  containment. (`ARCHITECTURE.md` former "Deferred" list.)
- **Gemma-4 domain retraining.** A queued project to produce
  domain-specialized retrained variants of gemma-4-E4B (PoC) and
  gemma-4-31B (real target) on a physics + math knowledge base, served on
  `reaper` alongside the stock tiers. Deepens the single-family commitment
  to the weights level and pre-validates the local fine-tuning workflow
  the weight-baked-instinct far-horizon item (below) will need.
  (`ARCHITECTURE.md` "Model-family as platform" section + former
  "Deferred" list.)
- **Synonym cluster resolution** (v0.2+, if empirical pressure).
  (`ARCHITECTURE.md` former "Deferred" list.)
- **Sub-agent runtime extension** (when use case demands).
  (`ARCHITECTURE.md` former "Deferred" list.)
- **Phrasal-concept symbol extraction** (v0.2).
  (`ARCHITECTURE.md` former "Deferred" list.)
- **Startup recovery after unclean shutdown** — *flagged, likely stale.*
  Originally queued as a v0.1 substrate requirement (SPEC §4.5.8):
  reconcile/rebuild stale derived state on open after a crash. This
  appears superseded by the crash-stability work (#94, waves R1–R5)
  already marked complete elsewhere in `AGENTS.md`'s repo-state section,
  which describes exactly this reconciliation as shipped. Carried forward
  here rather than silently dropped; verify against current code before
  treating it as open work, and delete this entry if it is confirmed
  redundant.

---

## Far-horizon

- **v0.2 bundle** — *deep cold archival via git* and *working-set content
  dedup*: **flagged, likely stale.** `ARCHITECTURE.md`'s own "Implemented
  and wired into the turn loop" section describes both as already shipped
  (`internal/turn/archival.go` + `internal/memops/fileadapter` for §3.8;
  `internal/dedup` for §3.9) — a three-way inconsistency with this v0.2
  label and with two other in-body "(v0.2)" mentions of the same features.
  Not resolved as part of this migration (no code was inspected); verify
  against `internal/turn/archival.go` / `internal/dedup` before
  prioritizing or deleting. The one part of this bundle **not**
  contradicted elsewhere: **transient-data event-log compaction +
  class-aware tool-output budget**, still open. (`AGENTS.md`, formerly
  "Queued".)
- **v1.0: Python computational workflow** (math/physics simulation).
  Tools: `python.run`, `python.format`, `python.lint`. Sandboxed
  subprocess; stdout/stderr into thread body subject to the §6.5 budget;
  resource caps (wall-clock, memory, output bytes). The shell-execution
  exclusion lifts *only* for these targeted Python tools — not a general
  "agent can run anything." Open: Python environment policy (`$PATH`
  python vs. per-project venv vs. `uv`). Held until v0.1 acceptance
  passes — orthogonal to the memory architecture. (`ARCHITECTURE.md`
  "Tool surface" + former "Deferred" list; `SPEC.md` §6.1.3.)
- **Rust front end / back-end split** — delivery-ladder rung 3 of math
  rendering (see Mid-term); the far-horizon two-executable architecture,
  language-portable by design. (`SPEC.md` §4.6.)
- **Encrypted at-rest storage** (back-of-mind).
  (`ARCHITECTURE.md` former "Deferred" list.)
- **Configuration migration on system upgrade** (v1.0+).
  (`ARCHITECTURE.md` former "Deferred" list.)
- **Multi-user** (v2.0; locked — not under active consideration).
  (`ARCHITECTURE.md` former "Deferred" list.)
- **Weight-baked instinct from outcome history.** Once local fine-tuning
  is mature and stable, the substrate's *outcome record* — not its
  content — becomes a training signal for a personal alignment adapter on
  the underlying model. Substrate stays recall; weights become instinct.
  Premise: baking memories by tuning on raw substrate content is a bad
  trade (displaces pretrained factual capacity for narrow recall the
  substrate already holds verifiably); instead train on the *shape of
  what worked and what failed* — decision durability, estimate
  calibration, external code-survival durability, user-correction
  patterns — via preference-pair learning (DPO/IPO-style) over
  `(situation, approach_taken, observed_outcome)` triples, producing a
  small low-rank LoRA on later layers / alignment-relevant attention
  heads. Hard constraints carried over from the substrate-as-canonical
  thesis: substrate remains source of truth (weights are an optimization
  on instinct, never a replacement for recall); each training pass is
  gated, reviewed, and reversible (pre-tuned model preserved, new LoRA
  tagged with its substrate snapshot hash, eval-gated promotion, failed
  evals roll back); substrate items used in a training pass get a
  `consolidated_at:` annotation so nothing bakes twice. Not v0.1, not
  v1.0, not v2.0 — depends on mature local fine-tuning infrastructure,
  accumulated outcome-labeled substrate at scale, and a tested eval
  methodology. (Formerly `ARCHITECTURE.md` Mechanisms, "Weight-baked
  instinct from outcome history (far-future consideration)"; also former
  "Deferred" list.)
- **Memory consolidation — the "sleep" cycle.** An offline consolidation
  cycle during idle time (day-off, or any unused window): re-packing
  fragmented structures, compacting the spine, advancing archival,
  running `git gc`/repack on the substrate, making final keep/toss calls
  the in-turn transient-data lifecycle left questionable, and
  building/reconciling within-thread summary trees. Full design capture
  — including how this couples to concurrent-session fragmentation below
  — stays in `ARCHITECTURE.md`'s Mechanisms section (not migrated: it is
  directly cross-referenced by a *now*-statement, `SPEC.md` §3.10.8's
  content-retention precision layer, so relocating the design text would
  break that reference without a compensating rewrite). Tracked here for
  prioritization only.
- **Submind via clone.** Isolated exploration and frontier-model
  collaboration via a subdirectory git clone operating as a full personant
  on a named branch, integrating back via git push + merge. Enables
  speculative exploration with a commit boundary, specialization without
  context-switch cost, parallel triangulation, and — via a two-model
  liaison/guest configuration inside the submind — architecturally safe
  frontier-model collaboration without cross-family substrate
  contamination. Full design capture (naming discipline, merge semantics,
  nested subminds) stays in `ARCHITECTURE.md`'s Mechanisms section.
  Tracked here for prioritization only.
- **Concurrent sessions.** Multiple live conversations against one shared
  memory (multitasking one career), distinct from subminds (isolated
  clones with a merge point) and from multi-user (v2.0). Raises thread
  safety, per-session working-set scoping, and explicit
  cross-reference-vs-isolation declarations. Full design capture stays in
  `ARCHITECTURE.md`'s Mechanisms section. Tracked here for prioritization
  only.

---

## Judgment calls made during the 2026-08-29 migration

See the migration report for the full list of now/future classification
calls (what stayed as scope-boundary documentation vs. what moved here),
and the doc-accuracy inconsistencies surfaced along the way (flagged
inline above where they affect an entry's reliability).
