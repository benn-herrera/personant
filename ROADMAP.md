# ROADMAP – Personant

This file owns **future intent**. It sits **outside** the contract-document precedence chain
(`THESIS.md` > `SPEC.md` > `ARCHITECTURE.md` > `CONVENTIONS.md` > code): those state only the *now*
— current behavior, constraints and scope — and cross-reference here for anything not yet built.
Read this file for planning. Do not hand it to a coding agent as a work order; an item graduates
into a contract document (with acceptance criteria) before it is built, per the project's planning
discipline.

Personant is a memory system under opencode: a stateless TypeScript plugin plus a Go memory service
(`ARCHITECTURE.md` "Plugin route"). Where an item is described in more detail, it cites the place:
a dated report (`OPENCODE_*.md`), `SPEC.md`, `ARCHITECTURE.md` or `DEFECTS.md` (D1–D4). **RD** is
`OPENCODE_ROUTE_DECISIONS.md`, cited only where no other document holds the fact.

**Checklist lifecycle.** Every item is a checkbox. An open item is `- [ ]`; it checks off to `- [x]`
**in place** the moment it ships and stays visible while its list has open siblings. When every item
in a list is checked, the **list is disposed of**: removed from this file, its history already in
`git log`. Without this, ROADMAP becomes the next memory store.

---

## Pending decisions

SPEC and ARCHITECTURE tag clauses that wait on a decision with `Open: Un`. Once a decision is made,
the tagged clauses get a follow-up pass. Each entry states when it must be decided.

- [ ] **U3 Topic tag.** Keep the in-band topic tag, which needs a stream abort-and-re-issue spike
  and per-protocol wire code in the plugin, and which stays in opencode's transcript unless the
  plugin strips it; or replace it with a recall tool the model calls, which rewrites SPEC
  §5.1/§5.5 and the tag-fidelity measures. Tagged at SPEC §3.0.1, §3.1, §5.1, §5.5, §9.1.
  **Decide before A10.**
- [ ] **U4 Memory-native commands and the ack channel.** Commands that alias opencode behavior
  (`/model`, `/models`, `/thinking`, `/key`, `/cd-project`, `/terminal-setup`, `/help`, `/version`,
  `/quit`) are retired; where a memory-native command survives it is a plugin-registered command
  (`CommandDomain.transform` → `CommandEditor.add`), not a `commands/*.md` file. Open: for each of
  `/topic*`, `/topics`, `/done`, `/closures`, `/pause`, `/resume`, `/back-to`, `/no-revisit`,
  `/keep`, `/threads`, `/spine`, `/index-rebuild`, `/project*`, `/stats`, keep as a plugin command,
  make autonomic, or drop; and the ack channel for the recall offer, closure review queue and
  startup project prompts. Plugins have only a yes/no `permission.assert`
  (`OPENCODE_INTEGRATION_OPTIONS.md` §2.3). Readings: (a) plugin tools the model calls plus yes/no
  `permission.assert`; (b) opencode commands carrying a service-recognized marker (spoofable;
  `OPENCODE_CONTEXT_INJECTIONS.md` §2); (c) an out-of-band `personant` CLI; (d) AUTO-only, no
  interactive acks; (e) hand the question to the model through the opencode-expectations slot and
  read the answer next turn. Unverified: how the TUI shows a command whose `execute` sends no
  prompt, and how `execute` reports a result. Tagged at SPEC §3.4, §3.5, §3.10.1, §4.2, §4.3,
  §4.5.3–§4.5.8. **Decide before A12.**
- [ ] **U5 How the service reaches its own models** (curator, dissector, embedder, memory-vetting
  reviewer) and where their settings live. (a) The plugin's `experimental.generate` over opencode's
  providers: no overlap, though the embedder still needs Personant config because opencode has no
  embedding provider. (b) A Personant provider pool under `~/.config/personant/`: overlaps
  opencode's providers when both name one endpoint, which Personant config must not do. Decides
  SPEC §1.8, §2.1, §5.2–§5.3, §8.1, §8.2, the `ping` and `models` verbs and the
  `langchaingo/llms` dependency rule. **Decide before A12.**
- [ ] **U6 Front-end version line.** `version.FrontEnd`, its bump contract and `test-fe` tracked the
  REPL. (a) Retire it, keeping the substrate and home-format lines; (b) repurpose it for the
  plugin. Decides SPEC §9.1 "Two version lines", CONVENTIONS "Versioning", README Status and the
  justfile. **Decide before B1.**
- [ ] **U7 Plugin failure policy.** Fail open (leave the hook input unmodified; opencode's own
  composition reaches the model and memory silently misses the step) or fail closed (refuse the
  request; the session stops while the service is down). What opencode does when the `context` hook
  throws or the service refuses is unestablished (`OPENCODE_RESUME_PROBE.md` §6). Blocks
  ARCHITECTURE "Plugin route". A5 ships fail-open as the interim; **decide before A9 finalises.**
- [ ] **U8 Interrupted prompts and retraction.** SPEC §4.3.3 says a retracted prompt never enters
  memory; opencode's interrupt keeps the prompt in the transcript the user sees. (a) Keep the rule:
  memory forgets what the TUI shows. (b) Conform to opencode: ingest the prompt and the partial
  turn. Also fixes reconcile's treatment of retracted turns (SPEC §4.5.8 (10)). **Decide before
  A8.**
- [ ] **U9 Restart persistence.** After a service restart the model sees no earlier conversation
  while the TUI shows all of it (`OPENCODE_RESUME_PROBE.md` §3); today only the working set
  reloads. (a) Persist in-run history and the turn counter. (b) Rebuild them from opencode's stored
  messages. (c) Accept: threads carry the memory. Sets a SPEC continuity outcome (SPEC §1.6,
  §4.5.8). **Decide before A8.**
- [ ] **U10 Model family.** ARCHITECTURE "Model-family as platform" targets one family (Gemma);
  opencode lets the user pick any model. (a) Keep one tuned family for the memory prompts and warn
  on others. (b) Support whatever opencode runs and measure tag and curator quality per family,
  which grows the eval surface (U3's recall-tool option shrinks it). Gates no item below.
- [ ] **U11 CONVENTIONS rule, not adopted.** External state (an agent's data home, telemetry) never
  lives inside `~/.personant/` or a test home, because crash recovery quarantines untracked files.
  The requirement for opencode's data home holds either way (`OPENCODE_RESUME_PROBE.md` §5,
  ARCHITECTURE "Start order and resume"); the question is whether it becomes a general CONVENTIONS
  rule. Yes or no; adopt into CONVENTIONS if yes.

---

## Sequence

### Part A: blocks a first usable build of the plugin route

Runner-target done-lines are written when each item is planned. A8, A9, A10 and A12 wait on the
decisions named in them.

- [ ] **A0. Start the implementation branch.** Branch fresh from `pivot-setup` and carry across
  exactly: `THESIS.md`, `SPEC.md`, `ARCHITECTURE.md`, `CONVENTIONS.md`, `ROADMAP.md`, `README.md`
  (once rewritten), `DEFECTS.md`, all `OPENCODE_*.md` reports (including
  `OPENCODE_ROUTE_DECISIONS.md`), `ACTIVE_DESIGN.md` (until it retires),
  `DOC_RECALIBRATION_PLAN.md` (until its remaining steps finish), and
  `ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md` and `ROADMAP_PLANS/code-mode-support.md`. Leave the
  probe code behind. `probe-path` stays alive, untagged and undeleted, as the reference until it is
  no longer needed. Every code item below lands on the new branch.
- [x] **A1. Fix the stale model name in `TestLoadConfigFixture`** so `just test` is green; every
  checkpoint commit needs that gate (CONVENTIONS "Two gates").
- [ ] **A2. Carry over the probe code worth keeping.** Bring the baseline comparator
  (`sim-baseline`, `sim-fidelity`, the committed baselines) from `probe-path`, and decide whether
  to keep `model.MockServer` (the mock served as an OpenAI-compatible endpoint; if kept, add it to
  SPEC §9.2). Needed before A3, whose done-line uses the comparator. (SPEC §9.1–§9.2.)
- [ ] **A3. Real runtime inversion.** Turn the runtime into the phase entry points opencode's hooks
  trigger (prompt, context, tool deltas, turn close), exporting what `OPENCODE_PROBE_RESULTS.md` §8
  lists. The in-process sim reproduces its baselines unchanged. The probe counterpart
  (`internal/turn/probe_phases.go` on `probe-path`) is reference only, not ported. Unblocks A5.
  (ARCHITECTURE "Runtime inversion".)
- [ ] **A4. Decide whether acceptance needs an opencode-in-loop rung.** The SPEC §9.1 gate is the
  in-process four-month simulation; nothing written says whether it also needs a rung with opencode
  in the loop. Decide when A3 lands; a yes adds a rung to the ladder.
- [ ] **A5. Production memory service and plugin.** The Go service as single writer and the
  stateless plugin over loopback HTTP+JSON, with: events tied to opencode message ids; tool
  results applied in call order (production must find a source for order, since real provider
  tool-call ids carry no index); plugin-side sequence numbers taken as events arrive; turn close on
  `session.execution.succeeded`; one non-empty system part; the `context` hook resetting
  `options.maxTokens`; provider-side request counting (`OPENCODE_PROBE_RESULTS.md` §6 lessons
  1–6, 8; `OPENCODE_CONTEXT_INJECTIONS.md` §3). Complete the delta mapping: `edit` (verify opencode
  exposes post-edit file bytes for the SPEC §3.0.1 `fs.write` delta), generic `tool.result`, and
  `read` stored as file bytes, not formatted output (SPEC §3.9.1). Failure policy interim:
  fail-open (`ACTIVE_DESIGN.md` §5); A9 decides. The probe counterpart
  (`internal/opencodehook/service.go`, `opencode-plugin/index.ts`, `internal/scenarios/opencode/*`
  on `probe-path`) is reference only, not ported. (SPEC §1, §3.0.1; ARCHITECTURE "Plugin route".)
- [ ] **A6. Injection handling.** Instructions slot rebuilt each request from per-source
  `{key, text}` with content hashes; nested `AGENTS.md` captured as its own keyed source;
  opencode-expectations slot; history rows typed by joining on message `id`; content already
  accounted for dropped on re-injection; the evicted-content thunk. Unblocks A7's accounting.
  (`OPENCODE_CONTEXT_INJECTIONS.md` §§1–5; SPEC §1.3–§1.4.)
- [ ] **A7. Per-slot budgets, including the tool-definition slot.** For every slot (instructions,
  opencode expectations, tool definitions, existing layers) fix budget, internal priority, overflow
  form and underusage; underusage flows from fixed slots to dynamic ones. Tool definitions count
  against the token ceiling but are pass-through today, with no budget or overflow form
  (`OPENCODE_CONTEXT_INJECTIONS.md` §5); candidate overflow forms are omitting rarely used tools
  or listing them by name with definitions on demand (MCP tools carry a `<server>_` prefix).
  Without this slot the whole-request ceiling cannot be enforced. Absorbs static-prompt-scaffolding
  byte accounting (orientation preamble, topic-tag directive and headers ride on top of the byte
  proxy unaccounted; a fixed deduction would zero the tiny ceilings the acceptance tests use).
  Settles whether `conventions_paths` (SPEC §2.5.1) overlaps the instructions slot, and the
  stable-prefix claim for the now-dynamic expectations slot. (SPEC §3.1; `ACTIVE_DESIGN.md` §10.)
- [ ] **A8. Resume and recovery.** Fix D4 first (crash scenarios through the shared harness are not
  evidence until it lands). Then: a recovery turn on a context call with no preceding prompt;
  reconcile against opencode's stored messages at service start; start order (service listening
  before opencode, managed background server included); opencode's data home outside
  `~/.personant/`; restart persistence per **U9**. If **U8** keeps retraction, add a record that
  distinguishes a retraction rollback from a crash rollback (reconcile skips retracted turns but no
  record tells the two apart). (`OPENCODE_RESUME_PROBE.md` §§4–5; DEFECTS.md D4; SPEC §1.6,
  §4.5.8.)
- [ ] **A9. Plugin failure policy.** Spike what opencode does when the `context` hook throws or the
  service refuses, then decide **U7** and adjust A5's fail-open interim. (ARCHITECTURE "Plugin
  route"; `OPENCODE_RESUME_PROBE.md` §6.)
- [ ] **A10. Topic tag** per **U3** (stream-abort spike, or recall tool), together with the missing
  `derived_from` lineage: no thread carries lineage through opencode while in-process runs do;
  candidate causes are tool deltas now ordered after `user.prompt` and the §5.5 mid-turn fetch not
  firing (`OPENCODE_PROBE_RESULTS.md` §11). Behind this item, whichever U3 branch is taken:
  residual never-attempted tags and a doubled anchor-list shape the parser
  (`internal/prompt/parser.go`) does not absorb; the tag-fidelity instrument limits (no `topic.tag-parsed` event; the
  re-engagement miss rate is inflated by spurious `*new-topic*` duplicates; SPEC §9.1 "Topic-tag
  fidelity"); runtime hot-reload of the topic-tag template (`internal/prompt/template.go`).
- [ ] **A11. Fix D1.** Per-turn commit cost grows with tracked files. After the fix, re-measure past
  sim-day 9 with opencode in the loop: whether memory-side cost keeps growing is unmeasured, as is
  the unattributed non-commit growth. (DEFECTS.md D1; `OPENCODE_PROBE_RESULTS.md` §§4, 11; SPEC
  §3.11, §9.1.)
- [ ] **A12. Install, config and the user command and ack channel.** `personant init` writes the
  opencode-side settings (`compaction.auto: false`, the plugin entry) in opencode's own format;
  Personant-only settings move to `~/.config/personant/` (`internal/store/paths.go` still places
  `config.toml` and `providers.toml` under the home) per **U5**; memory-native commands and the ack
  channel per **U4**, including the recovery banner, project prompts and the closure-drain boundary
  (an opencode session never "exits"). Implement the CLI verbs SPEC §4.1 keeps but `cmd/` lacks
  (`search`, `deps`, `log`), or remove them from SPEC. (SPEC §2.1, §4.1, §8; ARCHITECTURE "Single
  spelunking location".)

### Part B: after the first usable build

- [ ] **B1. Retire the self-built harness.** Remove `internal/chat`, `internal/term`,
  `internal/shell`, `internal/tools` and `internal/tools/web`, the `chat` verb, the `ping` and
  `models` verbs per **U5**, and `test-fe` / `fe-scope-check` per **U6**; then delete the
  readability, html-to-markdown and terminal/signal dependencies and CONVENTIONS "Free-API
  citizenship". **Precondition:** A5–A12 done and one end-to-end session through opencode passing
  with `oc_missed_deltas == 0` and `oc_uncomposed_requests == 0`
  (`OPENCODE_PROBE_RESULTS.md` §2). (ARCHITECTURE "Superseded code".)
- [ ] **B2. Fix D2.** `recall_query_latency_*` gauges copy turn-duration percentiles instead of
  timing recall queries. (DEFECTS.md D2; SPEC §9.4.)
- [ ] **B3. opencode database cleanup.** Checkpoints must not grow opencode's database without
  bound and cleanup must never break a kept session's scrollback, fork or revert; use
  `modernc.org/sqlite` for any Go SQLite need. (SPEC §1.7; `ACTIVE_DESIGN.md` §7;
  `OPENCODE_PROBE_RESULTS.md` §3.4 for the measured growth.)
- [ ] **B4. MCP prompt expansions and injection unknowns.** Detect MCP prompt expansions, which are
  indistinguishable from typed prompts in the hook input and are treated as user input until a
  route is found; establish hook order after a plugin reload and how system-role messages lower on
  provider routes other than openai-chat. (`OPENCODE_CONTEXT_INJECTIONS.md` §§2, 6; SPEC §1.2.)
- [ ] **B5. Concurrent sessions.** Full design capture is kept here; `ARCHITECTURE.md` keeps a short
  now-stub. The plugin route makes this reachable (the service is already the single writer).

  A user routinely interleaves work: two tasks open at once, attention alternating. CWD-scoped
  agents get this for free, since each directory is an isolated context. Personant cannot take that
  shortcut: its premise is a single unified awareness and career, so a context per directory would
  fragment what the system exists to keep whole. It must multitask: multiple live conversations
  open against one shared memory.

  Consequences, roughly in the order they bite: **thread safety** (concurrent sessions read and
  mutate shared canonical state — spine, thread files, the §3.0 chain — and need real
  synchronization at the substrate boundary); **per-session working set** ("the active thread"
  becomes session-scoped; so does `session.started active=`, which is logged per service run while
  the active project is per opencode session); **cross-reference vs. isolation** (two active
  threads may legitimately cross-reference, but the user must also be able to declare two lines of
  work unrelated, which recall and working-set composition would have to honor).

  Concurrency is also a fragmentation source: interleaved work touches threads out of order and
  lets per-session working sets drift apart, which makes it prime material for the consolidation
  cycle (Far-horizon, "Memory consolidation"). Distinct from multi-user (v2.0, locked): one user,
  one career, many conversations. (ARCHITECTURE "Concurrent sessions"; SPEC §1.1.)
- [ ] **B6. Fork and revert with sub-minds.** Design fork and revert together with sub-mind split
  and re-merge: each decides whether a branch's work is incorporated or discarded. Memory side of
  agent work: session memory readable by a sub-agent through recall-as-a-tool, no working-set dump
  and no writes; a sub-agent may *propose* that a finding be cached, and promotion into career
  memory is the main session's write. (SPEC §1.1; RD "Sessions, commands, layout".)
- [ ] **B7. Agents and skills.** Whether and how opencode agent definitions and skills are
  supported; candidate mapping: agent definitions as Layer E directives scoped per agent, skill
  content as transient task-class. (RD Open.)
- [ ] **B8. `/compact` and `/clear` mapping.** Both are no-ops today because memory is not keyed on
  session (SPEC §1.1). Decide what they eventually map to and whether a TUI plugin can replace or
  disable them. (`OPENCODE_CONTEXT_INJECTIONS.md` §§4, 6.)
- [ ] **B9. Resume cases not exercised.** A graceful stop with a turn in flight; a standalone-kill
  claim later swept by a managed server; the resume budget, background-job, subagent and shell
  recovery; how a real model answers a resumed request. (`OPENCODE_RESUME_PROBE.md` §6.)
- [ ] **B10. Memory vetting build.** Review of memory text before commit by a model applying a fixed
  rubric; a single write path for behavior memory with provenance on every outcome; a
  user-configurable acceptance policy that never blocks a turn; periodic re-review of standing
  behavior memory; mechanical write checks and batch content review for career memory. Open within
  it: the default acceptance policy, the rubric's content, which model reviews (**U5**), the
  re-review cadence. (SPEC §1.8; ARCHITECTURE "Memory vetting".)
- [ ] **B11. Directive accrual aggregation.** The cross-project digest, fallback dissection and
  directive accrual (the old "Phase 5"): accrual may propose a behavior-memory change only from
  patterns aggregated across projects, through the B10 review path. Define the boundary between
  behavior memory and project-convention career memory: every behavior adjustment learned in a
  project resolves to exactly one of (a) promotion to universal behavior memory, or (b)
  reformulation as a project convention rule, never a per-project behavior variant. (SPEC §1.8;
  THESIS "Behavior vs history"; ARCHITECTURE "Personalization through directive accrual".)
- [ ] **B12. Enforcement of "the LLM never touches the home's git tree".** opencode's shell reaches
  `~/.personant/`, so the statement cannot hold as written. Decide whether to enforce it (for
  example a plugin hook on shell tool calls) or restate it as intention in SPEC and ARCHITECTURE.
  (RD Open "Gaps".)

---

## Near-term

Concrete, mostly single-package items; independent of the sequence above unless noted.

- [ ] **Wire the mid-day re-baseline arming trigger.** The default-off knob that nukes and recreates
  the daily DB when loose objects cross a threshold mid-session has recovery handling and marker
  plumbing in place; the arming trigger is not wired. Adjacent: whether intra-day derived-watermark
  staleness needs finer handling. (SPEC §3.11, §4.5.8 "Intra-day derived staleness".)
- [ ] **Banded/auto-mode sim rung.** The closure ack-mode amendment (§3.5) and recall banding
  amendment (§3.4) are unit-covered, not sim-covered. Waits on **U4**. (SPEC §3.4, §3.5.)
- [ ] **Directive-file plumbing for the remaining §2.6.1 parameters.** Only `closure.ack-mode` and
  `recall.ack-mode` are read live through `store.ReadParameter`; the rest (`context.token-budget`,
  `staging.window-turns`, `history.cap-per-thread`, `anchor.projection-max`,
  `spine.entry-max-chars`, and so on) are compiled-in constants. (SPEC §2.6.1, §3.10.3, §3.1.)
- [ ] **Intra-thread recall-preservation follow-ups.** Re-verify
  `recall_intra_descent_divergence == 0` at a narrower beam (now k=8); build the brute-force O(n)
  backstop for the user-asserted-confidence fallback path. (SPEC §3.4.)
- [ ] **Turn-time (delta-time) budget enforcement.** The §3.0.2 step-4 budget check is a documented
  no-op (guarded by `TestBudgetCheckIsExplicitNoOpV01`); the live protections are the FIFO
  turn-pair cap and render-time byte truncation. Enabling delta-time eviction is a deliberate
  decision, not an incremental fill-in. (SPEC §3.0.2 step 4, §9.1.)
- [ ] **LLM-authored summary key for the within-thread summary tree.** Node keys are deterministic
  exemplar sets (`recall/measure.ExemplarSummarizer`); an LLM-authored key at the same seam is
  deferred. (SPEC §3.4.)
- [ ] **Recall precision/recall tuning, deferred until data demands it.** Enrich the stored-thread
  symbol set beyond the current 5 anchors; evaluate an asymmetric overlap coefficient in place of
  symmetric Jaccard. (SPEC §3.4.)
- [ ] **Non-ASCII symbols in the lexical completeness floor.** `exact.isWordRune`
  (`internal/recall/exact/exact.go`) admits ASCII letters and digits only, so symbols containing
  other letters are invisible to the floor. (SPEC §3.4.)
- [ ] **Hop-0 embedding recall below aspiration.** Under live embedding, `wander_current_recall` at
  hop 0 measured 0.882 against ~0.95 on the 14-day rung (report-only). Investigate threshold and
  query composition if it persists on the next long rung. (`internal/scenarios/sim/wander.go`.)
- [ ] **User-contributed file content sim.** The §3.9 file-editing workload models agentic edits,
  where content arrives via tool deltas and the prompt stays generic. The variant models
  user-dictated content: the prompt carries the literal lines. It exercises user-prompt symbol
  extraction, transient-data classification (`RetentionDecision` vs `RetentionTask`) and
  §3.9.2/§3.9.4 live-window handling of one literal appearing in prompt and file. (SPEC §3.9, §9.1.)
- [ ] **`store.ThreadTurnWindow` imported directly by `internal/turn/engage.go`**, bypassing the
  `MemoryOps` port. Accepted while the file adapter is the only one; revisit when a second adapter
  arrives. (ARCHITECTURE "Port-and-adapter".)
- [ ] **Flush-job re-enqueue on failure.** A failed embedding flush job is not re-enqueued. Parked
  until live-run data shows failed embeds. (`internal/recall/measure`.)
- [ ] **"Six-month" phrasings in Go comments.** Comments in `internal/memops`, `internal/autogit`,
  `internal/clock`, `internal/store` and `internal/scenarios/sim` still say six-month; the
  acceptance gate is 120 days. Sweep each file when next touched. (SPEC §9.1.)

---

## Mid-term

- [ ] **Archive recall.** Match `archive/index.jsonl` anchors and offer a recovery-fetch so a
  deep-cold thread resurfaces through the same opportunistic mechanism as a live one. The index
  already carries `anchors` and `spine_summary`: additive, no schema change. (SPEC §3.8.4.)
- [ ] **Synthesis-attribution residual.** When a thread synthesizes a prior thread's idea by
  paraphrase with no recall hit, `derived_from` is left empty. Attributing it is a fuzzy judgment
  reserved for an LLM-inference or human-declaration path; an open decision. (SPEC §2.7.3.)
- [ ] **Git tags for project open/close lifecycle navigation.** Thread and project
  *created*/*retired*/*archived* tags ship; *opened*/*closed*/*switched* are deferred because their
  semantics are ambiguous (a project is never explicitly closed; open/switch overlap, and under
  opencode the project follows the session directory). Resolve the vocabulary first. (SPEC §4.5.8.)
- [ ] **Prompt/response timestamp index.** A metadata index of when each prompt was sent and each
  response received, kept out-of-band and never injected into model context. The §2.8 event log
  carries clock stamps; this is for efficient temporal queries. (SPEC §2.8.)
- [ ] **Inter-agent data-sharing security.** Deterministic containment for sharing Personant's
  memory with a client's agent system under NDA-class restrictions. Principle: exclusion, not
  redaction; secret bytes never enter the model's context. Sketch: (1) prepare a redacted base
  dataset offline (allow-list, default-deny hides existence); (2) rebuild all derived structures
  (embedding index, `derived_from` edges, digests) from the clean set only; (3) `chown` the tree to
  an unprivileged guest OS user; (4) run the liaison under OS-level containment, failing closed.
  Reuses the rebuild-on-open primitive. Irreducible limit: prose cross-references inside allowed
  content need human verification at prep time.
- [ ] **Synonym cluster resolution**, if empirical pressure appears.
- [ ] **Phrasal-concept symbol extraction.**

---

## Far-horizon

- [ ] **Transient-data event-log compaction and class-aware tool-output budget.** Compaction of the
  event log's transient data, and a budget on tool output that differs by data class. Builds on A7,
  which sets per-slot budgets and the whole-request bound but does not classify tool output by
  class. (SPEC §3.0, §3.1.)
- [ ] **Encrypted at-rest storage** (back of mind).
- [ ] **Configuration migration on system upgrade**, covering opencode-held settings that
  `personant init` writes as well as `~/.config/personant/`.
- [ ] **Multi-user** (v2.0; locked, not under active consideration).
- [ ] **Weight-baked instinct from outcome history.** Once local fine-tuning is mature and stable,
  the substrate's *outcome record*, not its content, becomes a training signal for a personal
  alignment adapter. Substrate stays recall; weights become instinct. Baking memories by tuning on
  raw substrate content is a bad trade (it displaces pretrained factual capacity for narrow recall
  the substrate already holds verifiably); instead train on the shape of what worked and failed
  (decision durability, estimate calibration, code-survival durability, user-correction patterns)
  by preference-pair learning (DPO/IPO-style) over `(situation, approach_taken, observed_outcome)`
  triples, producing a small low-rank LoRA on later layers. Constraints: the substrate remains
  source of truth; each training pass is gated, reviewed and reversible (pre-tuned model
  preserved, LoRA tagged with its substrate snapshot hash, eval-gated promotion, rollback on
  failed evals); substrate items used in a pass get a `consolidated_at:` annotation so nothing
  bakes twice. Depends on mature local fine-tuning, outcome-labeled substrate at scale and a
  tested eval methodology.
- [ ] **Memory consolidation — the "sleep" cycle.** Full design capture is kept here;
  `ARCHITECTURE.md` keeps a short now-stub covering what is built (`MemoryOps.Consolidate` git
  gc/repack, SPEC §3.11; `measure.RebuildTrees`) plus the interface fact SPEC §3.10.8 needs.

  Personant's memory layering is deliberately analogous to organic memory: working short-term (the
  live working set), consolidated long-term (spine and thread bodies, mostly read), archival deep
  memory (off-spine, rarely touched), and a metadata layer for operating on active context fast.
  Under sustained use this organization fragments — threads close out of order, the spine accretes,
  derived structures and archival boundaries drift from ideal packing. In-turn maintenance keeps the
  substrate *correct* but not *orderly*.

  The remedy is an offline **consolidation cycle**. During idle time (the day off, or any unused
  window) the runtime runs reorganization it cannot afford mid-turn: re-packing fragmented
  structures, compacting the spine, advancing archival (beyond the git gc/repack and summary-tree
  rebuild already wired), and making the final keep/toss calls on data the faster in-turn
  transient-data lifecycle (§3.0) left questionable — the SPEC §3.10.8 content-retention precision
  layer. It is also where batch content review of career memory (B10) runs. Concurrent sessions
  (B5) create exactly the tangle this pass would clear. The day-off is already a real idle window
  in the acceptance workload model, and closure (§3.5) and archival (§3.8) are the mechanisms a
  consolidation pass would tidy.

---

## Parked plans

`ROADMAP_PLANS/` holds designs not under way. Those that still apply:

- `OPENCODE_SERVER_REPLACEMENT.md` — the fallback if the plugin route cannot give the memory system
  the control it needs (ARCHITECTURE "Plugin route").
- `code-mode-support.md` — applies only to that fallback.
- `within-thread-interleaving.md` — design for within-thread topic interleaving in the sim
  workload; not implemented (SPEC §9.1 "Workload interleaving").
