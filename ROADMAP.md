#  ROADMAP – Personant

This file owns **future intent**. It sits **outside** the contract-document precedence chain
(`SPEC.md` > `ARCHITECTURE.md` > `CONVENTIONS.md` > code): those three state only the *now* —
current behavior, current constraints, current scope boundaries — and cross-reference here for
anything not yet built. Read this file for planning. Do not hand it to a coding agent as a work
order; an item graduates into a contract document (with acceptance criteria) before it is built, per
the project's planning discipline.

Entries carry a source citation (a `file §section` pointer) back to wherever the item is described
in more detail, so provenance survives restructuring.

**Checklist lifecycle.** Every item in this file is a checkbox. An open item is `- [ ]`; it checks
off to `- [x]` **in place** the moment it ships — it stays visible as long as its list still has
open siblings, since a done item gives planning context to what's still open around it. When every
item in a list is checked, the **list is disposed of**: removed from this file, its history already
in `git log`. This is the file's garbage collection — without it, ROADMAP becomes the next memory
store.

---

## Near-term

Concrete, mostly single-package items; several unblock other queued work.

- [ ] **Build the `fs.*` tool surface.** `fs.read`, `fs.list`, `fs.grep` (read/think) and
  `fs.tmp_write`/`fs.tmp_read`/`fs.tmp_list`/
  `fs.propose_promote`/`fs.propose_rename`/`fs.propose_delete` (draft/mutate) are fully specified
  (`SPEC.md` §6.1.1–§6.1.2) but not implemented — only the seven `web.*` query tools are built and
  registered (`SPEC.md` §6.1.1 "Status:" line; `ARCHITECTURE.md` "Tool surface" section). This is
  the rest of the bounded 12-tool surface the architecture already commits to.
- [ ] **Directive-file plumbing for the remaining §2.6.1 parameters.** `closure.ack-mode` and
  `recall.ack-mode` are the only two parameters currently read live via
  `store.ReadParameter`/`MemoryOps.DirectiveParam`; everything else in `SPEC.md` §2.6.1
  (`context.token-budget`, `staging.window-turns`, `history.cap-per-thread`,
  `anchor.projection-max`, `spine.entry-max-chars`, etc.) is still a compiled-in constant.
  (`SPEC.md` §2.6.1, §3.10.3, §3.1.)
- [ ] **Bind Alt-B/Alt-F (word-back/word-forward) in the REPL line editor.** The terminal decoder
  already reports the Meta form; the editor does not yet bind it, so an Alt-modified rune is
  inserted as its base character. (`SPEC.md` §4.3.1.)
- [ ] **The `git-auto` sentinel for `[user] name`/`email`.** `name` or `email` set to the literal
  string `git-auto` would resolve at invocation via repo-scoped `git config user.name`/`user.email`
  (no `--global`), so a session in a work tree with an `includeIf` identity picks up the per-project
  address instead of the global one. Contract sketched, deliberately not built pending demonstrated
  need. (`SPEC.md` §8.2.2.)
- [ ] **Wire the mid-day re-baseline arming trigger.** The default-off knob that nukes/recreates the
  daily DB when loose objects cross a threshold mid-session has its recovery-side handling and
  marker plumbing in place; the arming trigger itself is not yet wired. Adjacent: revisit whether
  intra-day derived-watermark staleness (currently parked-by-design — the barrier owns freshness
  between barriers) should get finer-grained handling. (`SPEC.md` §3.11, §4.5.8 "Intra-day derived
  staleness".)
- [ ] **Banded/auto-mode sim rung.** Both the §3.5 closure ack-mode amendment and the §3.4 recall
  banding amendment are unit-covered but not sim-covered; a named sim rung exercising
  `banded`/`auto` end-to-end is owed. (`SPEC.md` §3.4, §3.5.)
- [ ] **W1 recall-preservation follow-ups.** Re-verify `recall_intra_descent_divergence == 0` at a
  narrower beam (currently beam width k=8); build the brute-force O(n) backstop for the
  user-asserted-confidence fallback path. (`SPEC.md` §3.4.)
- [ ] **Runtime hot-reload of the topic-tag prompt template**, for empirical tuning without a
  rebuild. Currently `internal/prompt/template.go`'s `TopicTagDirective` is a compiled constant.
  (`SPEC.md` §5.1.3.)
- [ ] **Turn-time (delta-time) budget enforcement (T3-3).** The §3.0.2 step-4 budget check is a
  documented no-op in v0.1 (guarded by `TestBudgetCheckIsExplicitNoOpV01`); the only live
  protections are the FIFO turn-pair cap and render-time byte truncation. Enabling delta-time
  eviction is a separate, deliberate decision, not an incremental fill-in. (`SPEC.md` §3.0.2 step 4,
  §9.1.)
- [ ] **Static-prompt-scaffolding byte accounting (#127 follow-up).** The layer-budget partition
  allocates 100% of the byte total to layers + `live_turn`; the ~1.7 KB static prompt scaffolding
  (orientation preamble, topic-tag directive, D6 reminders, headers) rides on top of the byte proxy
  unaccounted. Harmless against the authoritative token gate today; a partition that charges it a
  real share is deferred because a fixed deduction would zero out the tiny ceilings the acceptance
  tests exercise. (`SPEC.md` §3.1.)
- [ ] **F-A enrichment: LLM-summary key for the within-thread summary tree.** v0.1 node keys are
  deterministic exemplar sets (`recall/measure.ExemplarSummarizer`); an LLM-authored summary key at
  the same seam is deferred. (`SPEC.md` §3.4 / recall-measure internals.)
- [ ] **Recall precision/recall tuning, deferred until data demands it:** enrich the stored-thread
  symbol set beyond the current 5 anchors, and evaluate moving recall off symmetric Jaccard to an
  asymmetric overlap coefficient. (`SPEC.md` §3.4.)
- [ ] **Non-ASCII symbols in the lexical completeness floor.** `exact.isWordRune`
  (`internal/recall/exact/exact.go`) admits ASCII letters and digits only, so a symbol containing
  other letters is invisible to the lexical floor. (`SPEC.md` §3.4.)
- [ ] **"Six-month" phrasings in Go comments.** Comments in `internal/memops`, `internal/autogit`,
  `internal/clock`, `internal/store` and `internal/scenarios/sim` still say six-month; the
  acceptance gate is 120 days. Sweep each file when next touched. (`SPEC.md` §9.1.)
- [ ] **`store.ThreadTurnWindow` imported directly by `internal/turn/engage.go`**, bypassing the
  `MemoryOps` port. Accepted while the file adapter is the only one; revisit when a second adapter
  arrives. (`ARCHITECTURE.md` "Port-and-adapter at the substrate boundary.")
- [ ] **Flush-job re-enqueue on failure.** A failed embedding flush job is not re-enqueued. Parked
  until live-run data shows failed embeds. (`internal/recall/measure`.)
- [ ] **Topic-tag residuals under live inference.** Never-attempted tags remain on roughly 1.5% of
  live turns and coincide with serving-side loop-detection output bodies; a doubled anchor-list tag
  shape is not absorbed by the parser. (`internal/prompt/parser.go`.)
- [ ] **Tag-fidelity instrument limits.** No `topic.tag-parsed` event logs each turn's
  model-emitted anchor set, so anchor-emission overlap is a run-level figure read from thread
  frontmatter, and it is unmeasurable on the current workload (identifier vocabulary is disjoint
  from anchor vocabulary). The re-engagement miss rate is inflated by an unknown amount by spurious
  `*new-topic*` duplicates splitting cosine mass; decompose it before it drives design.
  (`internal/scenarios/sim/tagfidelity.go`; `SPEC.md` §9.1 "Topic-tag fidelity.")
- [ ] **Hop-0 embedding recall below aspiration.** Under live embedding, `wander_current_recall` at
  hop 0 measured 0.882 against the ~0.95 target on the 14-day rung (report-only). Investigate
  threshold and query composition if it persists on the next long rung.
  (`internal/scenarios/sim/wander.go`.)

---

## Mid-term

- [ ] **Phase 5.** Cross-project digest, fallback dissection, directive accrual.
- [ ] **Boundary between behavior memory and project-convention career memory.** Define it so that
  every behavior adjustment learned in a project resolves to exactly one of (a) promotion to
  universal behavior memory, with human acknowledgement, or (b) reformulation as a project
  convention rule — never a per-project behavior variant. Marking the two sides: how experimental
  versus conservative to be on a project is project-related; agent policy such as the no-self-blame
  rule is not. (`THESIS.md` "What follows"; `ARCHITECTURE.md` "Personalization through directive
  accrual.")
- [ ] **Math rendering delivery ladder.** Personant must eventually render mathematics as inline
  images in image-capable terminals, not raw LaTeX or lossy unicode. Three independently-shippable
  rungs: (1) **sidecar** — push committed turns to `laterm`'s local socket, math renders in a second
  window, no personant architecture change; (2) **inline, Rust-renders/Go-splices** — a
  laterm-derived tool emits finished protocol bytes, `internal/term` splices them as an opaque
  committed block; (3) **Rust front end** — the far-horizon two-executable split (Go substrate, Rust
  terminal front end) unifying laterm's renderer with `internal/term`. The binding-now protocol
  principle (semantic content only across any future front/back boundary — never pre-rendered bytes)
  and the ecosystem finding (LaTeX→terminal-image tooling lives in Rust, not Go) stay in `SPEC.md`
  §4.6 since they constrain current decisions. Also open, decided when the first rung ships: the
  model-output posture (LaTeX vs. unicode math in prompt/template). (`SPEC.md` §4.6.)
- [ ] **Archive recall (v0.2 addendum).** Matching `archive/index.jsonl` anchors and offering a
  recovery-fetch, so a deep-cold thread can resurface via the same opportunistic mechanism as a live
  one. The index schema already carries `anchors` + `spine_summary`, so this is additive, not a
  schema change. Deliberately out of scope for v0.1 (re-surfacing the coldest threads ambiently
  would reintroduce the cardinality pressure archival exists to relieve). (`SPEC.md` §3.8.4.)
- [ ] **Synthesis-attribution residual.** When a thread synthesizes a prior thread's idea by
  paraphrase with no recall hit (so no normalized symbol matches), `derived_from` is honestly left
  empty today. Attributing that residual is a fuzzy judgment reserved for an LLM-inference or
  human-declaration path — an explicit open decision, not built in v0.1. (`SPEC.md` §2.7.3 "Open
  (deferred)".)
- [ ] **Git tags for project open/close lifecycle navigation.** The thread/project
  *created*/*retired*/*archived* tags already ship (derived once/day at the day barrier); tags for a
  project's *opened*/*closed*/*switched* points are still deferred because the semantics are
  ambiguous in the current wave (a project is never explicitly "closed"; open/switch/cd overlap) —
  resolve the vocabulary before minting tags for it. (`SPEC.md` §4.5.8.)
- [ ] **Prompt/response timestamp index.** A dedicated metadata index recording when every user
  prompt was sent and every model response was received, kept strictly out-of-band (never injected
  into LLM context — timestamps in context can distort model behavior). The §2.8 event log carries
  clock stamps already; this is for efficient temporal queries ("when did we discuss X?").
- [ ] **Inter-agent data-sharing security.** Deterministic containment boundaries for sharing
  Personant's memory with a client's agent system under NDA-class restrictions. Core principle:
  exclusion, not redaction — secret bytes never enter the model's context. Sketch: (1) prepare a
  redacted base dataset offline (allow-list, default-deny hides existence); (2) rebuild all derived
  structures (embedding index, `derived_from` edges, A2 digests) from the clean set only; (3)
  `chown` the prepared tree to an unprivileged guest OS user; (4) run the liaison inside OS-level
  containment (mount-namespace/jail, fail-closed on any reach-across attempt). Reuses the
  rebuild-on-open primitive. Irreducible limit: prose
  cross-references inside allowed content require human verification at prep time; the deterministic
  layer only guarantees structural containment.
- [ ] **Synonym cluster resolution** (v0.2+, if empirical pressure).
- [ ] **Sub-agent runtime extension.** Use case: kbase-class domain work
  wanting model routing and focused contexts. The shape:
  - *Definition files*: markdown with a frontmatter subset — `name`, `description`, `tools`,
    `model`. Body becomes the agent's system charter over a thin runtime base. Load-time validation:
    unknown tool names fail loudly; `model` validates against the provider pool
    (refuse-or-warn-unverified, the startup convention); omission semantics defined explicitly.
    Descriptions written under the selection-anchor discipline — an orchestrating model chooses
    agents by description text.
  - *Context*: fresh per run — stable base (charter + task brief) plus a budgeted rolling volatile
    window. Overflow demotes deterministically (budget math, oldest block first, no model judgment)
    to serially numbered plain-markdown scratch files, replaced in-context by filename + a ≤8-word
    topic tag written for the future grep that will search for it. Recovery is grep + read; a
    re-read is a tool call, never permanent re-inflation. Serial numbering doubles as chronology.
  - *Memory*: session memory readable at need via recall-as-a-tool — no working-set dump, no writes.
    The single-writer invariant is untouched: agents hold scratch space (disposal = deletion, no
    lifecycle management) and a report channel; they may *propose* that a finding be cached, and
    promotion into career memory is the main session's write, human-acked — the propose-then-ack
    pattern applied to memory itself.
  - *Output*: cached to a file; the main session pulls sections via seek/read/tail tools rather than
    swallowing a dump. Dispatch and completion surface as receipts and event-log entries; runs carry
    lifecycle guards (turn caps, timeouts — self-limiting or verified dead).
  - *Routing*: frontmatter `model` is the per-task-class routing mechanism (navigate cheap, derive
    big) over the existing provider pool.
- [ ] **Phrasal-concept symbol extraction** (v0.2).

---

## Far-horizon

- [ ] **Transient-data event-log compaction + class-aware tool-output budget.** Not superseded by
  any shipped code — genuinely open.
- [ ] **v1.0: Python computational workflow** (math/physics simulation). Tools: `python.run`,
  `python.format`, `python.lint`. Sandboxed subprocess; stdout/stderr into thread body subject to
  the §6.5 budget; resource caps (wall-clock, memory, output bytes). The shell-execution exclusion
  lifts *only* for these targeted Python tools — not a general "agent can run anything." Open:
  Python environment policy (`$PATH` python vs. per-project venv vs. `uv`). Held until v0.1
  acceptance passes — orthogonal to the memory architecture. (`ARCHITECTURE.md` "Tool surface";
  `SPEC.md` §6.1.3.)
- [ ] **Rust front end / back-end split** — delivery-ladder rung 3 of math rendering (see Mid-term);
  the far-horizon two-executable architecture, language-portable by design. (`SPEC.md` §4.6.)
- [ ] **Encrypted at-rest storage** (back-of-mind).
- [ ] **Configuration migration on system upgrade** (v1.0+).
- [ ] **Multi-user** (v2.0; locked — not under active consideration).
- [ ] **Weight-baked instinct from outcome history.** Once local fine-tuning is mature and stable,
  the substrate's *outcome record* — not its content — becomes a training signal for a personal
  alignment adapter on the underlying model. Substrate stays recall; weights become instinct.
  Premise: baking memories by tuning on raw substrate content is a bad trade (displaces pretrained
  factual capacity for narrow recall the substrate already holds verifiably); instead train on the
  *shape of what worked and what failed* — decision durability, estimate calibration, external
  code-survival durability, user-correction patterns — via preference-pair learning (DPO/IPO-style)
  over `(situation, approach_taken, observed_outcome)` triples, producing a small low-rank LoRA on
  later layers / alignment-relevant attention heads. Hard constraints carried over from the
  substrate-as-canonical thesis: substrate remains source of truth (weights are an optimization on
  instinct, never a replacement for recall); each training pass is gated, reviewed, and reversible
  (pre-tuned model preserved, new LoRA tagged with its substrate snapshot hash, eval-gated
  promotion, failed evals roll back); substrate items used in a training pass get a
  `consolidated_at:` annotation so nothing bakes twice. Not v0.1, not v1.0, not v2.0 — depends on
  mature local fine-tuning infrastructure, accumulated outcome-labeled substrate at scale, and a
  tested eval methodology.
- [ ] **Memory consolidation — the "sleep" cycle.** Full design capture is kept here;
  `ARCHITECTURE.md` keeps a short now-stub covering the parts already built —
  `MemoryOps.Consolidate` git gc/repack (SPEC §3.11) and `measure.RebuildTrees` (#111) — plus the
  interface fact SPEC §3.10.8 needs, since that section cites this entry by name.

  Personant's memory layering is deliberately analogous to organic memory: working short-term (the
  live working set), consolidated long-term (the spine + thread bodies, mostly read), archival deep
  memory (off-spine, rarely touched), and a metadata layer for operating on active context fast.
  Under sustained use this organization fragments unavoidably — threads close out of order, the
  spine accretes, derived structures and archival boundaries drift from their ideal packing. In-turn
  maintenance keeps the substrate *correct* but not *orderly*.

  The intended remedy is an offline **consolidation cycle** — the system's equivalent of organic
  sleep. During idle time (the day off, or any unused window) the runtime would run larger-scale
  reorganization it cannot afford mid-turn: re-packing fragmented structures into orderly
  arrangements, compacting the spine, advancing archival (beyond the git gc/repack and summary-tree
  rebuild already wired), and making the final keep/toss calls on data the faster in-turn
  transient-data lifecycle (§3.0) left questionable — this is the SPEC §3.10.8 content-retention
  precision layer specifically. Working hours stay responsive; the heavy reorganization happens when
  nothing is waiting on it. Couples with concurrent-session fragmentation below: interleaved
  multitasking touches threads out of order and lets per-session working sets drift apart, which is
  exactly the tangle an offline consolidation pass would clear.

  Not v0.1 — but the v0.1 acceptance simulation already supplies the hook: the day-off is a real
  idle window in the workload model, and closure (§3.5) / archival (§3.8) are exactly the mechanisms
  a consolidation pass would tidy.
- [ ] **Concurrent sessions.** Full design capture is kept here; `ARCHITECTURE.md` keeps a short
  now-stub.

  A user routinely interleaves work — two tasks open at once, attention alternating. CWD-scoped
  agents (Claude Code, opencode, …) get this for free: each working directory is its own isolated
  context. Personant cannot take that shortcut — its premise is a single unified awareness and
  career, so a separate context per directory would fragment the very thing the system exists to
  keep whole. Personant must instead genuinely **multitask**: multiple live conversations open
  against one shared memory.

  Consequences, in rough order of when they bite: **thread safety** (concurrent sessions read and
  mutate shared canonical state — spine, thread files, the §3.0 chain — and the runtime is currently
  single-session; concurrent sessions need real synchronization at the substrate boundary);
  **per-session working set** (each session carries its own active threads, its own Layer B, and
  must track which is which — "the active thread" becomes session-scoped, not global);
  **cross-reference vs. isolation** (two concurrently-active threads may legitimately want to
  cross-reference, but the user must also be able to declare two lines of work unrelated — an
  explicit "do not conflate these" that recall and working-set composition would have to honor).

  Concurrency is also a fragmentation *source*: interleaved multitasking touches threads out of
  order and lets per-session working sets drift apart, which makes the results of concurrent
  sessions prime material for the "sleep" cycle above — concurrency creates the tangle, offline
  consolidation clears it.

  Not v0.1 — and distinct from multi-*user* (v2.0, locked): this is one user, one career, many
  concurrent conversations.
