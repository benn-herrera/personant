# Personant — Roadmap

This file owns **future intent**. It sits **outside** the contract-document
precedence chain (`SPEC.md` > `ARCHITECTURE.md` > `AGENTS.md` > code):
those three state only the *now* — current behavior, current constraints,
current scope boundaries — and cross-reference here for anything not yet
built. Read this file for planning. Do not hand it to a coding agent as a
work order; an item graduates into a contract document (with acceptance
criteria) before it is built, per the project's planning discipline.

Entries carry a source citation (a `file §section` pointer, or a bare
`§ Completed` self-reference) back to wherever the item is described in
more detail, so provenance survives restructuring.

**Checklist lifecycle.** Every item in this file is a checkbox. An open
item is `- [ ]`; it checks off to `- [x]` **in place** the moment it
ships — it stays visible as long as its list still has open siblings,
since a done item gives planning context to what's still open around it.
When every item in a list is checked, the **list is disposed of**:
removed from this file, its history already in `git log`. This is the
file's garbage collection — without it, ROADMAP becomes the next memory
store.

---

## 1. Port from GNU make to just

- [ ] Top priority. No source citation — this is new intent, not a
  migrated item.
- [ ] At port time, delete AGENTS.md's Build/test target list and gate
  reference in favor of one line advising the default `just` invocation
  (which lists recipes and their docstrings) as the target reference —
  the target inventory then lives in the justfile itself, not duplicated
  in prose.

## 2. Restore agents-dependency pull/maintenance machinery

- [ ] The old Makefile `agents`/`update-agents-dependency` targets
  (submodule pinning for the shared agent-definition set) were retired in
  favor of a plain symlink from `.claude/agents` to a cross-project agents
  repository. The pull/maintenance machinery — a mechanism to update that
  shared source on demand rather than relying on the symlink target being
  current by hand — is planned but not yet rebuilt. `AGENTS.md`'s
  repo-tree comment describing the Makefile reflects the current state
  (no agents-submodule pinning, no serve-local-api).

---

## Near-term

Concrete, mostly single-package items; several unblock other queued work.

- [ ] **Build the `fs.*` tool surface.** `fs.read`, `fs.list`, `fs.grep`
  (read/think) and `fs.tmp_write`/`fs.tmp_read`/`fs.tmp_list`/
  `fs.propose_promote`/`fs.propose_rename`/`fs.propose_delete`
  (draft/mutate) are fully specified (`SPEC.md` §6.1.1–§6.1.2) but not
  implemented — only the seven `web.*` query tools are built and
  registered (`SPEC.md` §6.1.1 "Status:" line; `ARCHITECTURE.md` "Tool
  surface" section). This is the rest of the bounded 12-tool surface the
  architecture already commits to.
- [ ] **Recall layer-3 model judgment.** Design direction captured, build
  gated on inference-in-loop (#98) landing the live-model seam: a
  `[recall]` provider/model config (small ~8K context ceiling, default
  E2B) that judges/reranks top-N embedding candidates before offering at
  most 3. Empirical discipline: sweep E2B → E4B → 26B-A4B against the
  embedding-only precision baseline (already collected, C.6 head-to-head)
  and pick the smallest passing tier. (`ARCHITECTURE.md` "Recall
  mechanisms"; `SPEC.md` §8.2.2 `[recall] model`.)
- [ ] **Directive-file plumbing for the remaining §2.6.1 parameters.**
  `closure.ack-mode` and `recall.ack-mode` are the only two parameters
  currently read live via `store.ReadParameter`/`MemoryOps.DirectiveParam`;
  everything else in `SPEC.md` §2.6.1 (`context.token-budget`,
  `staging.window-turns`, `history.cap-per-thread`,
  `anchor.projection-max`, `spine.entry-max-chars`, etc.) is still a
  compiled-in constant. (§ Completed, "Closure & recall ack amendments";
  `SPEC.md` §2.6.1, §3.10.3, §3.1.)
- [ ] **Bind Alt-B/Alt-F (word-back/word-forward) in the REPL line editor.**
  The terminal decoder already reports the Meta form; the editor does not
  yet bind it, so an Alt-modified rune is inserted as its base character.
  (`SPEC.md` §4.3.1.)
- [ ] **The `git-auto` sentinel for `[user] name`/`email`.** `name` or
  `email` set to the literal string `git-auto` would resolve at
  invocation via repo-scoped `git config user.name`/`user.email` (no
  `--global`), so a session in a work tree with an `includeIf` identity
  picks up the per-project address instead of the global one. Contract
  sketched, deliberately not built pending demonstrated need.
  (`SPEC.md` §8.2.2.)
- [ ] **Wire the mid-day re-baseline arming trigger.** The default-off
  knob that nukes/recreates the daily DB when loose objects cross a
  threshold mid-session has its recovery-side handling and marker
  plumbing in place; the arming trigger itself is not yet wired.
  Adjacent: revisit whether intra-day derived-watermark staleness
  (currently parked-by-design — the barrier owns freshness between
  barriers) should get finer-grained handling. (`SPEC.md` §3.11, §4.5.8
  "Intra-day derived staleness".)
- [ ] **Banded/auto-mode sim rung.** Both the §3.5 closure ack-mode
  amendment and the §3.4 recall banding amendment are unit-covered but
  not sim-covered; a named sim rung exercising `banded`/`auto` end-to-end
  is owed. (§ Completed, "Closure & recall ack amendments.")
- [ ] **W1 recall-preservation follow-ups.** Re-verify
  `recall_intra_descent_divergence == 0` at a narrower beam now that the
  exemplar-set propagation fix confirms the keys carry recall (currently
  beam width k=8); build the brute-force O(n) backstop for the
  user-asserted-confidence fallback path. (§ Completed, "Recall stack,"
  intra-thread recall entry.)
- [ ] **Runtime hot-reload of the topic-tag prompt template**, for
  empirical tuning without a rebuild. Currently
  `internal/prompt/template.go`'s `TopicTagDirective` is a compiled
  constant. (`SPEC.md` §5.1.3.)
- [ ] **Turn-time (delta-time) budget enforcement (T3-3).** The §3.0.2
  step-4 budget check is a documented no-op in v0.1 (guarded by
  `TestBudgetCheckIsExplicitNoOpV01`); the only live protections are the
  FIFO turn-pair cap and render-time byte truncation. Enabling delta-time
  eviction is a separate, deliberate decision, not an incremental
  fill-in. (`SPEC.md` §3.0.2 step 4, §9.1.)
- [ ] **Static-prompt-scaffolding byte accounting (#127 follow-up).** The
  layer-budget partition allocates 100% of the byte total to layers +
  `live_turn`; the ~1.7 KB static prompt scaffolding (orientation
  preamble, topic-tag directive, D6 reminders, headers) rides on top of
  the byte proxy unaccounted. Harmless against the authoritative token
  gate today; a partition that charges it a real share is deferred
  because a fixed deduction would zero out the tiny ceilings the
  acceptance tests exercise. (`SPEC.md` §3.1.)
- [ ] **F-A enrichment: LLM-summary key for the within-thread summary tree.**
  v0.1 node keys are deterministic exemplar sets
  (`recall/measure.ExemplarSummarizer`); an LLM-authored summary key at
  the same seam is deferred. (`SPEC.md` §3.4 / recall-measure internals.)
- [ ] **Recall precision/recall tuning, deferred until data demands it:**
  enrich the stored-thread symbol set beyond the current 5 anchors, and
  evaluate moving recall off symmetric Jaccard to an asymmetric overlap
  coefficient. (§ Completed, "Recall stack," Phase C entry.)
- [ ] **Inference-/embedding-in-loop simulation.** Wire real `reaper.local`
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

- [ ] **Phase 5.** Cross-project digest, fallback dissection, directive
  accrual.
- [ ] **Math rendering delivery ladder.** Personant must eventually render
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
- [ ] **Archive recall (v0.2 addendum).** Matching `archive/index.jsonl`
  anchors and offering a recovery-fetch, so a deep-cold thread can
  resurface via the same opportunistic mechanism as a live one. The index
  schema already carries `anchors` + `spine_summary`, so this is additive,
  not a schema change. Deliberately out of scope for v0.1 (re-surfacing
  the coldest threads ambiently would reintroduce the cardinality
  pressure archival exists to relieve). (`SPEC.md` §3.8.4.)
- [ ] **Synthesis-attribution residual.** When a thread synthesizes a prior
  thread's idea by paraphrase with no recall hit (so no normalized symbol
  matches), `derived_from` is honestly left empty today. Attributing that
  residual is a fuzzy judgment reserved for an LLM-inference or
  human-declaration path — an explicit open decision, not built in v0.1.
  (`SPEC.md` §2.7.3 "Open (deferred)".)
- [ ] **Git tags for project open/close lifecycle navigation.** The
  thread/project *created*/*retired*/*archived* tags already ship
  (derived once/day at the day barrier); tags for a project's
  *opened*/*closed*/*switched* points are still deferred because the
  semantics are ambiguous in the current wave (a project is never
  explicitly "closed"; open/switch/cd overlap) — resolve the vocabulary
  before minting tags for it. (`SPEC.md` §4.5.8.)
- [ ] **Prompt/response timestamp index.** A dedicated metadata index
  recording when every user prompt was sent and every model response was
  received, kept strictly out-of-band (never injected into LLM context —
  timestamps in context can distort model behavior). The §2.8 event log
  carries clock stamps already; this is for efficient temporal queries
  ("when did we discuss X?").
- [ ] **Inter-agent data-sharing security.** Deterministic containment
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
  containment.
- [ ] **Gemma-4 domain retraining.** A queued project to produce
  domain-specialized retrained variants of gemma-4-E4B (PoC) and
  gemma-4-31B (real target) on a physics + math knowledge base, served on
  `reaper` alongside the stock tiers. Deepens the single-family commitment
  to the weights level and pre-validates the local fine-tuning workflow
  the weight-baked-instinct far-horizon item (below) will need.
  (`ARCHITECTURE.md` "Model-family as platform" section.)
- [ ] **Synonym cluster resolution** (v0.2+, if empirical pressure).
- [ ] **Sub-agent runtime extension** (when use case demands).
- [ ] **Phrasal-concept symbol extraction** (v0.2).

---

## Far-horizon

- [ ] **Transient-data event-log compaction + class-aware tool-output
  budget.** Not superseded by any shipped code — genuinely open.
- [ ] **v1.0: Python computational workflow** (math/physics simulation).
  Tools: `python.run`, `python.format`, `python.lint`. Sandboxed
  subprocess; stdout/stderr into thread body subject to the §6.5 budget;
  resource caps (wall-clock, memory, output bytes). The shell-execution
  exclusion lifts *only* for these targeted Python tools — not a general
  "agent can run anything." Open: Python environment policy (`$PATH`
  python vs. per-project venv vs. `uv`). Held until v0.1 acceptance
  passes — orthogonal to the memory architecture. (`ARCHITECTURE.md`
  "Tool surface"; `SPEC.md` §6.1.3.)
- [ ] **Rust front end / back-end split** — delivery-ladder rung 3 of math
  rendering (see Mid-term); the far-horizon two-executable architecture,
  language-portable by design. (`SPEC.md` §4.6.)
- [ ] **Encrypted at-rest storage** (back-of-mind).
- [ ] **Configuration migration on system upgrade** (v1.0+).
- [ ] **Multi-user** (v2.0; locked — not under active consideration).
- [ ] **Weight-baked instinct from outcome history.** Once local
  fine-tuning is mature and stable, the substrate's *outcome record* —
  not its content — becomes a training signal for a personal alignment
  adapter on the underlying model. Substrate stays recall; weights become
  instinct. Premise: baking memories by tuning on raw substrate content
  is a bad trade (displaces pretrained factual capacity for narrow recall
  the substrate already holds verifiably); instead train on the *shape of
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
  methodology.
- [ ] **Memory consolidation — the "sleep" cycle.** Full design capture is
  kept here; `ARCHITECTURE.md` keeps a short now-stub covering the parts
  already built — `MemoryOps.Consolidate` git gc/repack (SPEC §3.11) and
  `measure.RebuildTrees` (#111) — plus the interface fact SPEC §3.10.8
  needs, since that section cites this entry by name.

  Personant's memory layering is deliberately analogous to organic
  memory: working short-term (the live working set), consolidated
  long-term (the spine + thread bodies, mostly read), archival deep
  memory (off-spine, rarely touched), and a metadata layer for operating
  on active context fast. Under sustained use this organization fragments
  unavoidably — threads close out of order, the spine accretes, derived
  structures and archival boundaries drift from their ideal packing.
  In-turn maintenance keeps the substrate *correct* but not *orderly*.

  The intended remedy is an offline **consolidation cycle** — the
  system's equivalent of organic sleep. During idle time (the day off, or
  any unused window) the runtime would run larger-scale reorganization it
  cannot afford mid-turn: re-packing fragmented structures into orderly
  arrangements, compacting the spine, advancing archival (beyond the
  git gc/repack and summary-tree rebuild already wired), and making the
  final keep/toss calls on data the faster in-turn transient-data
  lifecycle (§3.0) left questionable — this is the SPEC §3.10.8
  content-retention precision layer specifically. Working hours stay
  responsive; the heavy reorganization happens when nothing is waiting on
  it. Couples with concurrent-session fragmentation below: interleaved
  multitasking touches threads out of order and lets per-session working
  sets drift apart, which is exactly the tangle an offline consolidation
  pass would clear.

  Not v0.1 — but the v0.1 acceptance simulation already supplies the
  hook: the day-off is a real idle window in the workload model, and
  closure (§3.5) / archival (§3.8) are exactly the mechanisms a
  consolidation pass would tidy.
- [ ] **Submind via clone.** Full design capture is kept here;
  `ARCHITECTURE.md` keeps a short now-stub with the interface fact: the
  archive-index schema already stores `parent_commit_hash` explicitly
  (SPEC §3.8), anticipating a future multi-parent submind merge commit.

  Personant's substrate is a git tree already; a **submind** is a
  subdirectory clone of that tree, with the primary's home as the
  submind's `origin`. The submind operates as a full personant on a named
  branch in its local clone — its own spine, its own threads, its own
  working-set discipline — and integrates back via standard git push +
  merge. Branch isolation means the submind never collides with the
  primary until merge; the existing substrate machinery handles the rest.

  Why subminds earn their architectural slot: **speculative exploration
  with a commit boundary** (try a thought experiment in a submind; decide
  whether the result is worth integrating; the primary's belief state
  isn't disturbed); **specialization without context-switch cost** (a
  submind focused on one task continues while the primary continues with
  the broader context); **parallel triangulation** (two submind
  subdirectories pursuing variant hypotheses are just two subdirectories;
  the user or the primary decides at merge time which to integrate); and
  **naming discipline via a universal `mind_id` suffix** (every
  personant has a `mind_id`; every autonomically-generated identifier in
  that personant is suffixed, flat — `<generation>-<timestamp>`, not
  chained, with immediate-parent provenance held in metadata — so
  cross-submind name collisions are impossible by construction and the
  merge is git-trivial on the namespaced substrate).

  **Submind as the natural home for frontier-model collaboration.** A
  submind can run a *two-model* configuration: a local **liaison model**
  (gemma family — E4B for swarm-cheap, 26B-A4B for default judgment
  quality) handling all family-stable infrastructural prompts
  (topic-tagging, symbol extraction, recall scoring, closure summaries),
  and a **guest model** (a frontier model — Claude / GPT / Gemini / etc.)
  providing the actual reasoning content. The liaison model handles every
  surface where family-stable consistency matters; the guest model
  produces the conversational content — what goes into threads, turn
  excerpts, hot state. Substrate stays gemma-shaped (infrastructural);
  content is guest-shaped. The submind's branch identity carries the
  provenance — no per-symbol provenance tags, no per-turn
  guest-engagement aggregator needed; which submind contained the work
  *is* the provenance. This isolation is what makes frontier-model
  collaboration architecturally safe rather than a cross-family
  contamination risk, per `ARCHITECTURE.md`'s "Model-family as platform"
  principle — the submind container bounds the foreign model's
  behavioral influence to the content surface, away from the
  infrastructural surface where family-stability is load-bearing.
  Eval-by-comparison falls out for free: spawn a submind with
  `guest=Claude`, another with `guest=gemma-31B-local`, compare at merge
  time — same task, same substrate format, two perspectives.

  **Distinguished from concurrent sessions (below).** Subminds are
  *isolated clones with a merge integration point*. Concurrent sessions
  are *interleaved multitasking on shared canonical state*. Subminds give
  you a commit boundary ("explore divergent hypotheses without bleeding
  belief state"); concurrent sessions give you simultaneous live
  attention on the same memory ("two coordinated tasks at once"). They
  can coexist; they are not substitutes.

  **Merge semantics** — the viable spectrum, captured for design
  completeness: **archive-only minimal path** (submind archives its own
  active work before merge; primary git-merges only the append-only
  archive; eliminates running-state conflicts by construction, but
  belief-state from divergent thought doesn't silently flow back — most
  conservative); **structural merge** (once names are globally unique via
  the suffix scheme, the submind's live spine entries, thread
  directories, and working-set membership travel back as-is — `git
  merge` handles append-only files via union driver; pre-clone threads
  are read-only inside the submind, continuations create a new
  namespaced thread `thr_42-S1` with `derived_from: thr_42` frontmatter,
  so the same topic surfaces as two threads in the merged primary, both
  recall-reachable); **communication channel during life** (pub/sub IPC
  between subminds and primary while the submind is active — status,
  queries, results — never substrate read/write across the boundary;
  merge is a *termination*, no continuing submind activity after it).

  **Nested subminds.** Naturally recursive — a submind IS a personant; by
  symmetry it can spawn its own submind. Suffix composition
  (`thr_42-S1-S2`) gives unambiguous provenance at any depth; merge
  propagates one level at a time; no structural depth limit — the real
  bound is the user's review-budget at each merge gate.

  Post-v0.1; plausibly v2.0. The frontier-collaboration use case may end
  up being the primary motivator for prioritizing submind work.
- [ ] **Concurrent sessions.** Full design capture is kept here;
  `ARCHITECTURE.md` keeps a short now-stub.

  A user routinely interleaves work — two tasks open at once, attention
  alternating. CWD-scoped agents (Claude Code, opencode, …) get this for
  free: each working directory is its own isolated context. Personant
  cannot take that shortcut — its premise is a single unified awareness
  and career, so a separate context per directory would fragment the
  very thing the system exists to keep whole. Personant must instead
  genuinely **multitask**: multiple live conversations open against one
  shared memory.

  Consequences, in rough order of when they bite: **thread safety**
  (concurrent sessions read and mutate shared canonical state — spine,
  thread files, the §3.0 chain — and the runtime is currently
  single-session; concurrent sessions need real synchronization at the
  substrate boundary); **per-session working set** (each session carries
  its own active threads, its own Layer B, and must track which is which
  — "the active thread" becomes session-scoped, not global);
  **cross-reference vs. isolation** (two concurrently-active threads may
  legitimately want to cross-reference, but the user must also be able to
  declare two lines of work unrelated — an explicit "do not conflate
  these" that recall and working-set composition would have to honor).

  Concurrency is also a fragmentation *source*: interleaved multitasking
  touches threads out of order and lets per-session working sets drift
  apart, which makes the results of concurrent sessions prime material
  for the "sleep" cycle above — concurrency creates the tangle, offline
  consolidation clears it.

  Not v0.1 — and distinct from multi-*user* (v2.0, locked): this is one
  user, one career, many concurrent conversations.

---

## Completed

Kept while still relevant — a shipped subsystem an open item above builds
on, extends, or was deferred pending. Build-wave narrative for each lives
in `git log`, not here. The checklist-lifecycle doctrine above governs
disposal going forward; the groups below are migrated in as fully-checked
groups and are candidates for disposal at the next review, not removed
automatically by this migration.

### Core substrate (Phases 1–B)

- [x] Skeleton + storage scaffold (Phase 1).
- [x] Turn loop, topic-tag parsing, chat REPL, layered working-set
  composition, scenario harness, mid-turn thread re-prompt (Phase 2,
  §5.5).
- [x] Opportunistic recall, symbolic Jaccard (Phase 3) — within-project,
  log-only surface; cross-project + UI deferred until Phase C's
  calibration data (Phase C, below, is complete).
- [x] `MemoryOps` port + `FileAdapter` (Phase A) — application layer
  (turn/chat/cmd/scenarios) depends on the port; `internal/autogit`
  (go-git-backed) handles autonomic git on the home tree.
- [x] Transient-data lifecycle (Phase B) — see `ARCHITECTURE.md`
  "Transient-data lifecycle."

### Recall stack (§3.4)

- [x] Layer-2 embedding recall — `model.Embedder` + provider
  `embeddingModel`; `recall.ProposeEmbedding` cosine matcher; opt-in per
  provider with graceful symbolic-only fallback. Rationale (symbolic
  Jaccard collapses under vocabulary drift) and calibration data: memory
  `project_personant_recall_fidelity_design`, git log around the C.6
  sweep.
- [x] Intra-thread recall (#109/#111) — chunk-level hierarchical
  embedding index, coarse→fine, engaged thread bypasses the coarse gate;
  async single-indexer + `atomic.Pointer` snapshot; persisted
  `.vec`/`.tree` sidecars; sleep-cycle `RebuildTrees` builds/reconciles
  summary trees offline. Beam width k=8 (current). W1 divergence is a
  reported quality measure, not a gate (#119) — see `AGENTS.md`
  "Intra-thread recall metrics." Tuning history (exemplar-set fix,
  beam-width oscillation): git log d8e32b3, 9c9493d, 45bae9c. Further
  cost-minimization and a brute-force O(n) confidence-fallback backstop:
  Near-term above.
- [x] Phase C: recall-fidelity test infrastructure — complete (madlibs
  query generator, adversarial templates, Wikipedia corpus pipeline,
  LLM-authored corpus templates, calibration sweep). Harness lives in
  `internal/scenarios/testdata/`, driven by `make recall-madlibs` /
  `make recall-corpus-test` (opt-in execution; always compiles under
  `make test`). Headline finding informing the recall thresholds below:
  symbolic Jaccard collapses under vocabulary drift (M=4/T=0.4 → 0.098
  recall) while embedding recall stays robust (~0.97) but precision-poor
  (~0.21 at cos 0.45) — validates the layered §3.4 design. Full six-step
  build + sweep data: memory `project_personant_recall_fidelity_design`.
- [x] Evolving-anchor model (SPEC §2.2/§2.7.4/§3.4/§5.1) — see
  `ARCHITECTURE.md` "Anchors vs. history." Shipped across five
  increments; full sequence in git log.

### Crash stability / dual-repo recovery (#94)

- [x] Waves R1–R5 — see `ARCHITECTURE.md` "Crash stability and the
  dual-repo substrate"; normative spec `SPEC.md` §4.5.8 (states
  "Implemented"). Reconciles derived state against canonical on startup
  (Init → Reconcile → day barrier → LoadSession) via the B0–B6 barrier
  sequence and the journal/marker/watermark detection set. History: git
  log f499f94 (design) .. 4efe4ea (R5 docs).

### Dogfood-minimum chat REPL (front end)

- [x] Slash set, term-owned line editing + history (`internal/term`'s own
  editor — emacs keys, ↑/↓ history over `~/.personant/history`, wrapped
  multi-row rendering under a row cap, editable pre-filled defaults,
  question folded into the read), the §4.3.3 interrupt/abort keys
  (`Ctrl-C` clears a non-empty line, hints then exits on a consecutive
  second press at an empty one, and ends the session mid-turn; `Esc`
  retracts the in-flight turn; `Ctrl-Z` suspends cleanly and `fg` comes
  back to a sane terminal), and the §4.3.2 phase-labeled turn progress
  indicator.
- [x] Esc-retraction invariant — rolls session-volatile runtime state
  (turn counter, §3.10 staging buffer, Layer B/C, history, recall marks,
  embedding debt) back to its pre-turn value — `internal/turn/rollback.go`,
  invariant-tested (`TestPreCanonicalAbort_NoSessionResidue`) — gated to
  an allow-list of pre-canonical turn phases, so a retracted prompt
  cannot shape the next turn; the durable trace is one
  `system.turn-aborted` event line, never the text.
- [x] Shipped alongside a broader review burn-down (instrument integrity,
  runtime correctness, concurrency/robustness, DRY/dead-code,
  docs-accuracy sweep): `design/review-burndown-2026-07.md`, git log
  08605bf, 618337f, d5540f3.

### §4.4 shell escape

- [x] `internal/shell` — `$` fire-and-forget and `#` capture-into-next-turn,
  via per-command `$SHELL -c`, not a persistent shell (SPEC §4.4.2).
  Ctrl-C during a command is an invariant, not best-effort; the child
  gets its own process group; stdin is the null device; a `#` capture is
  redacted against resolved provider keys on the context path only
  (§8.2.1) — the terminal itself keeps the unredacted bytes. Full
  mechanics: SPEC §4.4.1–§4.4.4.

### §6.1 tool surface

- [x] Registry (`internal/tools`: name → spec + handler + tier + mutates,
  substrate-free) + execution loop (`internal/turn`, SPEC §6.1.4). An
  empty registry sends no `tools` field. Tool execution is at-least-once
  under #94 crash replay — harmless for read-only tools, NOT for a
  mutating one, so `tools.Registry.Register` mechanically refuses a tool
  declaring `Mutates`; revisit that decision before any mutating tool
  lands.
- [x] Seven `web.*` query tools (`fetch`, `search`, `wikipedia`,
  `wiktionary`, `wikidata`, `arxiv`, `crossref`) — each SPEC section
  (§6.1.1, §6.1.5–§6.1.9) carries its own design rationale (e.g. why
  `web.wikidata` uses the Action API, not the REST path the other two
  Wikimedia tools share). `web.fetch` signals "requires JavaScript
  rendering" explicitly rather than returning a blank body for a
  client-rendered page; `web.search` keeps "search failed" and "search
  found nothing" distinguishable. Tier ruling (SPEC §6.2.7): tier 0
  (silent), scheme allowlist as the boundary instead of an ack. Free-API
  citizenship (`AGENTS.md` house rules) is the shared politeness layer
  all seven ride.

### Terminal layer (`internal/term`)

- [x] Arbiter consolidation, waves W0–W3 — sole owner of fds, mode, the
  single reader, every emitted byte, and ownership state; `internal/chat`
  keeps policy only. `ReadLine(Question)` is the only read path.
- [x] `liner` removed, ONE session mode, waves W3/W5 — git log
  07e5ccd..c1ddea2; debate in `mad-design/terminal-layer/`.
- [x] A planned SIGINT-offer mechanism (wave W4) was dissolved: the ISIG
  behavior makes Ctrl-C a decoded key wherever term owns the fd, so
  `term.Handler` carries no SIGINT hook, by design, not omission. See
  `ARCHITECTURE.md` "Terminal ownership" for the full design rationale.

### Closure & recall ack amendments

- [x] Thread closure / retirement (§3.5) — curator-drafted summary + ack
  flow (`internal/turn/closure.go`, decay-triggered scan at turn close).
  Routine closures auto-accept, one committed line, no prompt; a decayed
  thread is an exception when anchor-rich (≥6) or long-engaged (≥15
  `turn_count`), or its draft failed/came back empty — exceptions batch
  at session boundaries (a derived queue, never stored). `/done` stays
  fully interactive. Escape hatch: `closure.ack-mode: auto|always`
  (SPEC §2.6.1).
- [x] §3.4 recall surface, banded — same medicine as closure: at/above
  the tier's auto threshold (`recall.symbolic-auto-threshold` 0.65,
  `recall.cosine-auto-threshold` 0.75 — SPEC §2.6.1) fetched with one
  committed line and no prompt; the band between surface and auto asks
  once per turn with a legible offer; below surface, unchanged. A
  candidate already resident in Layer B is declined `already-known` by
  the runtime rather than asked. Escape hatch: `recall.ack-mode:
  banded|always`. Threshold rationale (C.6 corpus sweep): memory
  `project_personant_recall_fidelity_design`.
- [x] `closure.ack-mode` and `recall.ack-mode` are the only two
  `SPEC.md` §2.6.1 parameters currently read live via
  `store.ReadParameter`/`MemoryOps.DirectiveParam`; the rest of §2.6.1 is
  still compiled-in constants (tracked in Near-term above).

### Deep-cold archival & dedup (§3.8/§3.9)

- [x] Recoverable deep-cold archival (§3.8) — `cmd/archive.go` (`archive
  list`/`archive recover`), `internal/turn/archival.go`;
  cardinality-pressure scan at turn close.
- [x] Working-set content dedup / git minimization (§3.9) —
  `internal/dedup` + `AgeFileChains`, per engaged thread, wired into
  `internal/turn/chain.go` step 3, `internal/workset`,
  `internal/memops/fileadapter`.
- [x] `ARCHITECTURE.md`'s stale `(v0.2)` labels on both features have
  been corrected to match the shipped state above.

---

## Judgment calls made during the 2026-08-29 migration

See the migration report for the full list of now/future classification
calls (what stayed as scope-boundary documentation vs. what moved here),
and the doc-accuracy inconsistencies surfaced along the way (flagged
inline above where they affect an entry's reliability).
