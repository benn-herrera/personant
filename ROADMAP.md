# ROADMAP – Personant

This file owns **future intent**. It sits **outside** the contract-document precedence chain
(`THESIS.md` > `SPEC.md` > `ARCHITECTURE.md` > `CONVENTIONS.md` > code): those state only the *now*
— current behavior, constraints and scope — and cross-reference here for anything not yet built.
Read this file for planning. Do not hand it to a coding agent as a work order; an item graduates
into a contract document (with acceptance criteria) before it is built, per the project's planning
discipline.

Personant is a memory system under opencode: a stateless TypeScript plugin plus a Go memory service
(`ARCHITECTURE.md` "Plugin route"). Where an item is described in more detail, it cites the place:
a dated report (`OPENCODE_RESEARCH/`), `SPEC.md` or `ARCHITECTURE.md`. A known defect is an item
here that states the fact and where it lives.

**Checklist lifecycle.** Every item is a checkbox. An open item is `- [ ]`; it checks off to `- [x]`
**in place** the moment it ships and stays visible while its list has open siblings. When every item
in a list is checked, the **list is disposed of**: removed from this file, its history already in
`git log`. Without this, ROADMAP becomes the next memory store.

---

## Maintenance

- Review of `PROJECT_DOCS/CONVENTIONS/`, particularly house rules. some or all of the conventions
  may have undergone some rot or may have become superseded/duplicates to changes in global
  CLAUDE.md/AGENTS.md or agent definitions

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
  (`OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md` §2.3). Readings: (a) plugin tools the model calls plus yes/no
  `permission.assert`; (b) opencode commands carrying a service-recognized marker (spoofable;
  `OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §2); (c) an out-of-band `personant` CLI; (d) AUTO-only, no
  interactive acks; (e) hand the question to the model through the opencode-expectations slot and
  read the answer next turn. Unverified: how the TUI shows a command whose `execute` sends no
  prompt, and how `execute` reports a result. Tagged at SPEC §3.4, §3.5, §3.10.1, §4.2, §4.3,
  §4.5.3–§4.5.8. **Decide before A12.**
- [x] **U5 Inference for Personant's internal calls** (curator, dissector, embedder,
  memory-vetting reviewer). Personant has no inference path or provider configuration of its own:
  every call goes through opencode, text generation through the plugin's `experimental.generate`,
  embeddings per A12a (forking opencode if it has no route). The provider pool, `providers.toml`,
  the `ping` and `models` verbs and the `langchaingo/llms` dependency go. Follow-up pass: SPEC
  §1.8, §2.1, §5.2–§5.3, §8.1, §8.2.
- [x] **U6 Front-end version line: retired.** `version.FrontEnd`, its bump contract, `test-fe` and
  `fe-scope-check` go with the REPL; the substrate and home-format lines stay. Follow-up pass: SPEC
  §9.1 "Two version lines", CONVENTIONS "Versioning", README Status and the justfile.
- [ ] **U7 Plugin failure policy.** Fail open (leave the hook input unmodified; opencode's own
  composition reaches the model and memory silently misses the step) or fail closed (refuse the
  request; the session stops while the service is down). What opencode does when the `context` hook
  throws or the service refuses is unestablished (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6). Blocks
  ARCHITECTURE "Plugin route". A5 ships fail-open as the interim; **decide before A9 finalises.**
- [ ] **U8 Interrupted prompts and retraction.** SPEC §4.3.3 says a retracted prompt never enters
  memory; opencode's interrupt keeps the prompt in the transcript the user sees. (a) Keep the rule:
  memory forgets what the TUI shows. (b) Conform to opencode: ingest the prompt and the partial
  turn. Also fixes reconcile's treatment of retracted turns (SPEC §4.5.8 (10)). **Decide before
  A8.**
- [ ] **U9 Restart persistence.** After a service restart the model sees no earlier conversation
  while the TUI shows all of it (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §3); today only the working set
  reloads. (a) Persist in-run history and the turn counter. (b) Rebuild them from opencode's stored
  messages. (c) Accept: threads carry the memory. Sets a SPEC continuity outcome (SPEC §1.6,
  §4.5.8), and settles SPEC §4.3.3's rollback, which restores run-volatile state whose restart
  persistence is this question. **Decide before A8.**
- [ ] **U10 Model family.** ARCHITECTURE "Model-family as platform" targets one family (Gemma);
  opencode lets the user pick any model. (a) Keep one tuned family for the memory prompts and warn
  on others. (b) Support whatever opencode runs and measure tag and curator quality per family,
  which grows the eval surface (U3's recall-tool option shrinks it). Gates no item below.
- [ ] **U11 CONVENTIONS rule, not adopted.** External state (an agent's data home, telemetry) never
  lives inside `~/.personant/` or a test home, because crash recovery quarantines untracked files.
  The requirement for opencode's data home holds either way (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §5,
  ARCHITECTURE "Start order and resume"); the question is whether it becomes a general CONVENTIONS
  rule. Yes or no; adopt into CONVENTIONS if yes.

---

## Sequence

### Part A: blocks a first usable build of the plugin route

Runner-target done-lines are written when each item is planned. A8, A9, A10 and A12 wait on the
decisions named in them.

- [x] **A0. Start the implementation branch.** `plugin-route`, cut from `pivot-setup` with the
  recalibrated documents and reports carried across and the probe code left behind. `probe-path`
  stays alive, untagged and undeleted, as the reference until it is no longer needed. Every code
  item below lands on `plugin-route`.
- [x] **A1. Fix the stale model name in `TestLoadConfigFixture`** so `just test` is green; every
  checkpoint commit needs that gate (CONVENTIONS "Two gates").
- [ ] **A2. Carry over the probe code worth keeping.** Bring the baseline comparator
  (`sim-baseline`, `sim-fidelity`, the committed baselines) from `probe-path`, and decide whether
  to keep `model.MockServer` (the mock served as an OpenAI-compatible endpoint; if kept, add it to
  SPEC §9.2). Needed before A3, whose done-line uses the comparator. (SPEC §9.1–§9.2.)
- [ ] **A3. Real runtime inversion.** Turn the runtime into the phase entry points opencode's hooks
  trigger (prompt, context, tool deltas, turn close), exporting what `OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §8
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
  `options.maxTokens`; provider-side request counting; Go decoding of every part type of opencode's
  provider-neutral messages, which have no machine-readable schema (opencode
  `packages/ai/src/schema/messages.ts` is mirrored by hand), pinned by a contract test against
  captured hook input (`OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §6 lessons
  1–6, 8; `OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §3). Complete the delta mapping: `edit` (verify opencode
  exposes post-edit file bytes for the SPEC §3.0.1 `fs.write` delta), generic `tool.result`, and
  `read` stored as file bytes, not formatted output (SPEC §3.9.1). Failure policy interim:
  fail-open (ARCHITECTURE "Failure handling"); A9 decides. The probe counterpart
  (`internal/opencodehook/service.go`, `opencode-plugin/index.ts`, `internal/scenarios/opencode/*`
  on `probe-path`) is reference only, not ported. (SPEC §1, §3.0.1; ARCHITECTURE "Plugin route".)
- [ ] **A6. Injection handling.** Instructions slot rebuilt each request from per-source
  `{key, text}` with content hashes; nested `AGENTS.md` captured as its own keyed source;
  opencode-expectations slot; history rows typed by joining on message `id`; content already
  accounted for dropped on re-injection; the evicted-content thunk. Unblocks A7's accounting.
  (`OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §§1–5; SPEC §1.3–§1.4.)
- [ ] **A7. Per-slot budgets, including the tool-definition slot.** For every slot (instructions,
  opencode expectations, tool definitions, existing layers) fix budget, internal priority, overflow
  form and underusage; underusage flows from fixed slots to dynamic ones. Tool definitions count
  against the token ceiling but are pass-through today, with no budget or overflow form
  (`OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §5); candidate overflow forms are omitting rarely used tools
  or listing them by name with definitions on demand (MCP tools carry a `<server>_` prefix).
  Without this slot the whole-request ceiling cannot be enforced. Absorbs static-prompt-scaffolding
  byte accounting (orientation preamble, topic-tag directive and headers ride on top of the byte
  proxy unaccounted; a fixed deduction would zero the tiny ceilings the acceptance tests use).
  Settles whether `conventions_paths` (SPEC §2.5.1) overlaps the instructions slot, and the
  stable-prefix claim for the now-dynamic expectations slot. (SPEC §3.1 "Slot answers".)
- [ ] **A8. Resume and recovery.** First: `newHarness` (`internal/scenarios/harness_setup.go`)
  does not run the startup reconcile that production runs, so a harness home's files stay
  uncommitted and crash recovery quarantines them; crash scenarios through the shared harness are
  not evidence until it does. Then: a recovery turn on a context call with no preceding prompt;
  reconcile against opencode's stored messages at service start; start order (service listening
  before opencode, managed background server included); opencode's data home outside
  `~/.personant/`; restart persistence per **U9**. If **U8** keeps retraction, add a record that
  distinguishes a retraction rollback from a crash rollback (reconcile skips retracted turns but no
  record tells the two apart). (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §§4–5; SPEC §1.6,
  §4.5.8.)
- [ ] **A9. Plugin failure policy.** Spike what opencode does when the `context` hook throws or the
  service refuses, then decide **U7** and adjust A5's fail-open interim. (ARCHITECTURE "Plugin
  route"; `OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6.)
- [ ] **A10. Topic tag** per **U3** (stream-abort spike, or recall tool), together with the missing
  `derived_from` lineage: no thread carries lineage through opencode while in-process runs do;
  candidate causes are tool deltas now ordered after `user.prompt` and the §5.5 mid-turn fetch not
  firing (`OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §11). Behind this item, whichever U3 branch is taken:
  residual never-attempted tags and a doubled anchor-list shape the parser
  (`internal/prompt/parser.go`) does not absorb; the tag-fidelity instrument limits (no `topic.tag-parsed` event; the
  re-engagement miss rate is inflated by spurious `*new-topic*` duplicates; SPEC §9.1 "Topic-tag
  fidelity"); runtime hot-reload of the topic-tag template (`internal/prompt/template.go`).
- [ ] **A11. Commit-cost growth: fixed; opencode-in-loop re-measure remains.** Single-load staging
  (`793890f`) and excerpt folios (`1cecf0c`–`631cd48`) hold per-turn commit cost flat (13–21 ms/turn
  over 54 mock sim-days, `test/rundata/sim-workload-seed24095-dur2880h0m0s.261101104107`, stopped at
  day 54) and the daily index near 2,000 entries. What remains is the re-measure with opencode in the
  loop, worth running when it can show memory-side cost under real inference that a mock run
  cannot. (`OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §§4, 11; SPEC §3.11, §9.1.)
- [x] **A11a. Per-turn growth outside the commit: `Chain.LiveWindow` is quadratic.** Mean per-turn
  time rises with sim-day and accelerates, breaking SPEC §9.1's flat per-operation latency. The main
  cause is `dedup.(*Chain).LiveWindow` (`internal/dedup/livewindow.go`), called per turn from
  `loadTrackedFiles` (`internal/memops/fileadapter/workset_compose.go`): it `Reconstruct`s every
  version of a tracked file's chain, only to hash the old ones into identifiers. Cost is versions ×
  file size, and both grow with every edit. The chain never shrinks in a mock run because §3.9
  aging requires the version's blob to be reachable in the workspace repo (`store.BlobReachable`).
  The sim never commits the workspace, so every aging attempt is refused with
  `reason=blob-unreachable`. A real user who never commits a tracked file sees the same thing.
  This misses SPEC §3.9's intent. Older copies were meant to be replaced in place where the context
  holds them. The build instead appends a per-version identifier list in a separate tracked-files
  section, and ties retention to the user's git. The fix is to implement the designed in-place
  dedup (`ACTIVE_PLAN/`). Secondary, smaller: `LoadThreadFiles` decodes `files.json` twice per
  engaged turn (compose and `AgeFileChains`). The same `LiveWindow` cost also runs in the sim
  harness's `VerifyNoBudgetOverflow`, which roughly doubles the sim's run time. Rule out after the
  fix: residual growth in `CommitTurn` CPU, `ReadThreadBody` → `readExcerptText`, and
  `closeTurnAndUpdateEngagement`.
- [ ] **A11b. Whole-thread reads grow with thread length.** `store.ReadThreadBody` reads every
  excerpt of a thread (`store.LoadThread` passes no byte budget), so its per-turn CPU rises with
  sim-day. Its callers in a mock run are `turn.fetchThroughChain` and `turn.draftClosure`, via
  `FileAdapter.LoadThread`. Measured per-turn ratio over the seed-24095 mock sim: 1.27 from day 9 to
  day 20 and 1.47 from day 9 to day 31 (`ReadThreadBody` → `readExcerptText` alone: 1.21 and 1.44),
  against a flat band of 1.25. Small in absolute terms (≈ 1.3% of sim CPU at day 31). (SPEC §9.1.)
- [ ] **A12. Install, config and the user command and ack channel.** `personant init` writes the
  opencode-side settings (`compaction.auto: false`, the plugin entry) in opencode's own format;
  Personant-only settings move to `~/.config/personant/` (`internal/store/paths.go` still places
  `config.toml` under the home, and `providers.toml`, which goes per **U5**); memory-native commands and the ack
  channel per **U4**, including the recovery banner, project prompts and the closure-drain boundary
  (an opencode session never "exits"). Implement the CLI verbs SPEC §4.1 keeps but `cmd/` lacks
  (`search`, `deps`, `log`), or remove them from SPEC. (SPEC §2.1, §4.1, §8; ARCHITECTURE "Single
  spelunking location".)
- [ ] **A12a. Embeddings through opencode.** Recall's embedding index (SPEC §3.4) needs an
  embedding endpoint, and Personant has no inference path of its own (**U5**). Establish opencode's
  route for embeddings; if it has none, fork opencode to add one. No Personant-side endpoint
  configuration.

### Part B: after the first usable build

- [ ] **B1. Retire the self-built harness.** Remove `internal/chat`, `internal/term`,
  `internal/shell`, `internal/tools` and `internal/tools/web`, `internal/ping`,
  `internal/modellist` and the provider pool, the `chat`, `ping` and `models` verbs (**U5**), and
  `version.FrontEnd`, `test-fe` and `fe-scope-check` (**U6**); then delete the readability,
  html-to-markdown, terminal/signal and `langchaingo/llms` dependencies and CONVENTIONS "Free-API
  citizenship". The old systems are not a fallback: nothing waits on the plugin route to remove
  them. Plan: `ROADMAP_PLANS/strip-self-built-harness.md`.
- [ ] **B2. `recall_query_latency_*` does not measure recall.** The sim's p50/p95/p99 gauges
  (`metricRecallQueryLatencyP50/P95/P99`, `internal/scenarios/sim/workload.go`, populated in
  `sim/telemetry.go`) copy turn-duration percentiles instead of timing recall queries. (SPEC §9.4.)
- [ ] **B3. opencode database cleanup.** Checkpoints must not grow opencode's database without
  bound and cleanup must never break a kept session's scrollback, fork or revert; use
  `modernc.org/sqlite` for any Go SQLite need. (SPEC §1.7; ARCHITECTURE "opencode database cleanup";
  `OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §3.4 for the measured growth.)
- [ ] **B4. MCP prompt expansions and injection unknowns.** Detect MCP prompt expansions, which are
  indistinguishable from typed prompts in the hook input and are treated as user input until a
  route is found; establish hook order after a plugin reload and how system-role messages lower on
  provider routes other than openai-chat. (`OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §§2, 6; SPEC §1.2.)
- [ ] **B4a. Events missed across a plugin reload.** opencode reloads a local plugin when its files
  change. Whether `event.subscribe` delivers events published during the reload is unverified; the
  source suggests a live bus with no replay, in which case recovery reads stored messages instead.
  (`OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md` §5, item 5.)
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
  memory is the main session's write. (SPEC §1.1.)
- [ ] **B6a. Delegated large-file work.** A task that needs a file too large for its thread's Layer
  B share is handed whole to a sub-mind: it reads, edits and checks the file, and returns a summary
  of what it did and learned, with the identifiers of files it read or changed and the diffs it
  made, so the main mind can check the summary against them. The returned summary is the main
  thread's excerpt; no file literal crosses back. The size threshold forces delegation, and the main
  mind may also delegate smaller work. On the plugin route, whether the plugin can route such a read
  to an opencode child session, or only steer the model toward one, is an open research question.
  Shares the sub-mind mechanics of B6. (SPEC §3.9.)
- [ ] **B7. Agents and skills.** Whether and how opencode agent definitions and skills are
  supported; candidate mapping: agent definitions as Layer E directives scoped per agent, skill
  content as transient task-class.
- [ ] **B8. `/compact` and `/clear` mapping.** Both are no-ops today because memory is not keyed on
  session (SPEC §1.1). Decide what they eventually map to and whether a TUI plugin can replace or
  disable them. (`OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §§4, 6.)
- [ ] **B9. Resume cases not exercised.** A graceful stop with a turn in flight; a standalone-kill
  claim later swept by a managed server; the resume budget, background-job, subagent and shell
  recovery; how a real model answers a resumed request. (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6.)
- [ ] **B10. Memory vetting build.** Review of memory text before commit by a model applying a fixed
  rubric; a single write path for behavior memory with provenance on every outcome; a
  user-configurable acceptance policy that never blocks a turn; periodic re-review of standing
  behavior memory; mechanical write checks and batch content review for career memory. Open within
  it: the default acceptance policy, the rubric's content, which model reviews (through opencode, **U5**), the
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
- [ ] **B13. `personant init` refuses an existing home.** `init` creates a home and nothing else: it
  succeeds on an absent or empty directory and otherwise exits non-zero, naming the path and
  pointing to `personant verify`. Drops "idempotent" from SPEC §4.1 and §8.1; touches
  `cmd/init.go`, `store.Init` and the init tests.
- [ ] **B14. Rune-safe truncation of thread descriptions and summaries.** `descriptionFromNewThread`
  and `summarizeForNewThread` (`internal/turn/excerpt.go`) cut with `s[:maxLen-3]`, a byte index, so
  multi-byte text can be split mid-rune; the invalid bytes reach `spine.jsonl`, where the JSON
  encoder silently substitutes U+FFFD. Cut at a rune boundary, as SPEC §3.1 requires for layer
  truncation, with a test on multi-byte input.

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
- `folio-deferred-shrink.md` — unbind writes loose copies without fsync and drops the duplicate
  folio entries only once the copies are sealed in a day-commit; follows the excerpt-folio plan.
  Open: whether a power-loss-torn loose copy is accepted as a residual or readers fall back to the
  folio.
- `strip-self-built-harness.md` — removes the self-built REPL, tools, terminal and provider
  handling with their tests, and reshapes config for a memory service under opencode (extends B1).
  U5 and U6 are decided; waits on the config-migration stance and whether to strip before A5–A12.
