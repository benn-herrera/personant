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

- [ ] **U3 Model-supplied anchors.** Under **U12**, sessions will set thread boundaries, so
  the topic tag's thread list will have no job. Open: whether the model still contributes per-turn
  anchors (`source = model`), in band at response start (which needs the stream
  abort-and-re-issue and transcript stripping) or through a tool, or not at all; and whether a
  recall tool the model calls replaces the §5.5 fetch trigger. Tagged at SPEC §3.0.1, §3.1,
  §5.1, §5.5, §9.1. **Decide in A10.**
- [x] **U4 Memory-native commands and the ack channel: none.** Personant registers
  no command in opencode and asks the user nothing; memory work runs automatically (SPEC §4).
- [x] **U5 Inference for Personant's internal calls** (summarizer, clusterer, embedder,
  memory-vetting reviewer). Personant has no inference path or provider configuration of its own:
  every call goes through opencode, text generation through the plugin's `experimental.generate`,
  embeddings per A12a (forking opencode if it has no route).
- [x] **U6 Front-end version line: retired.** The memory store and home-format lines stay.
- [ ] **U7 Plugin failure policy.** Fail open (leave the hook input unmodified; opencode's own
  composition reaches the model and memory silently misses the step) or fail closed (refuse the
  request; the session stops while the service is down). What opencode does when the `context` hook
  throws or the service refuses is unestablished (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6). Blocks
  ARCHITECTURE "Plugin route". A5 ships fail-open as the interim; **decide before A9 finalises.**
- [x] **U8 Interrupted prompts and retraction: Personant's rule.** A retracted prompt
  never enters memory, whatever opencode's transcript shows: Personant composes its own context,
  and the transcript does not decide what enters it.
- [ ] **U9 Restart persistence.** After a service restart the model sees no earlier conversation
  while the TUI shows all of it (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §3); today only the working set
  reloads. (a) Persist in-run history and the turn counter. (b) Rebuild them from opencode's stored
  messages. (c) Accept: threads carry the memory. Sets a SPEC continuity outcome (SPEC §1.6,
  §4.5.8), and settles SPEC §4.3.3's rollback, which restores run-volatile state whose restart
  persistence is this question. The same counter drives turn-based decay and the unknown-thread-id
  owner fallback through the stored `last_engaged_turn` (`internal/turn/closure.go`
  `decayEligible`). **Decide before A8.**
- [ ] **U10 Model family.** ARCHITECTURE "Model-family as platform" targets one family (Gemma);
  opencode lets the user pick any model. (a) Keep one tuned family for the memory prompts and warn
  on others. (b) Support whatever opencode runs and measure tag and summarizer quality per family,
  which grows the eval surface (U3's recall-tool option shrinks it). Gates no item below.
- [ ] **U11 CONVENTIONS rule, not adopted.** External state (an agent's data home, telemetry) never
  lives inside `~/.personant/` or a test store directory, because crash recovery quarantines untracked files.
  The requirement for opencode's data home holds either way (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §5,
  ARCHITECTURE "Start order and resume"); the question is whether it becomes a general CONVENTIONS
  rule. Yes or no; adopt into CONVENTIONS if yes.
- [x] **U12 Session as thread.** A project is opencode's project directory and
  a bundle of threads; an opencode session is one thread. Recall works across a project's
  threads, and across projects for career-level memory. Sessions, not in-band topic tags, set
  thread boundaries: a returned-to session keeps its thread, and a new session on an old topic
  relies on recall. Follow-up pass: SPEC §1.1, §3.0.4, §3.2, §3.3, §5.1, §5.5, §9.1, and an
  optional opencode session ID on the thread (frontmatter and thread registry) with a store
  format-revision bump. THESIS "What is new" item 2 and SPEC §1.1 ("by the problem it concerns,
  never by session") change with A10.
  Plan: A10.

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
  (ARCHITECTURE "Hook-driven turn phases".)
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
  stable-prefix claim for the now-dynamic expectations slot. (SPEC §3.1 "Slot policies".)
- [ ] **A8. Resume and recovery.** First: `newHarness` (`internal/scenarios/harness_setup.go`)
  does not run the startup reconcile that production runs, so a harness store directory's files stay
  uncommitted and crash recovery quarantines them; crash scenarios through the shared harness are
  not evidence until it does. Then: a recovery turn on a context call with no preceding prompt;
  reconcile against opencode's stored messages at service start; start order (service listening
  before opencode, managed background server included); opencode's data home outside
  `~/.personant/`; restart persistence per **U9**. Add a record that
  distinguishes a retraction rollback from a crash rollback, so reconcile re-ingests
  crash rollbacks and never a retracted turn (**U8**). (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §§4–5; SPEC §1.6,
  §4.5.8.)
- [ ] **A9. Plugin failure policy.** Spike what opencode does when the `context` hook throws or the
  service refuses, then decide **U7** and adjust A5's fail-open interim. (ARCHITECTURE "Plugin
  route"; `OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6.)
- [ ] **A10. Session-as-thread routing** per **U12**, settling **U3**. Turns route
  to the session's thread; the topic tag's thread list, unknown-thread-id resolution, the
  missing-tag fallback and the tag-triggered §5.5 fetch go; the session ID is stored on the thread; the sim
  generator's unit becomes the session, and the tag-fidelity measures go. Needs session IDs from
  the plugin (A5). Re-check the missing `derived_from` lineage under opencode
  (`OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §11) now that recall fetches automatically. The
  parser and tag-fidelity residuals of the old topic-tag item apply only if U3 keeps an in-band
  tag. (SPEC §1.1, §3.0.4, §3.2, §3.3, §5.1, §5.5, §9.1.)
- [ ] **A11. Commit-cost growth: fixed; opencode-in-loop re-measure remains.** Single-load staging
  (`793890f`) and excerpt bundles (`1cecf0c`–`631cd48`) hold per-turn commit cost flat (13–21 ms/turn
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
  dedup. Secondary, smaller: `LoadThreadFiles` decodes `files.json` twice per
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
- [ ] **A12. Install and config.** `personant init` writes the opencode-side
  settings (`compaction.auto: false`, the plugin entry) in opencode's own format, and the
  `~/.config/personant/config.toml` template once A12a fixes what `[embedding] model` names; the
  store directory holds no settings file. Implement the CLI verbs SPEC §4.1 keeps but `cmd/`
  lacks (`search`, `deps`, `log`), or remove them from SPEC. (SPEC §2.1, §4.1, §8; ARCHITECTURE
  "One inspection location".)
- [ ] **A12a. Embeddings through opencode.** Recall's embedding index (SPEC §3.4) needs an
  embedding endpoint, and Personant has no inference path of its own (**U5**). Establish opencode's
  route for embeddings; if it has none, fork opencode to add one. No Personant-side endpoint
  configuration.

### Part B: after the first usable build

- [x] **B1. Retire the self-built harness.** Remove `internal/chat`, `internal/term`,
  `internal/shell`, `internal/tools` and `internal/tools/web`, `internal/ping`,
  `internal/modellist` and the provider pool, the `chat`, `ping` and `models` verbs (**U5**), and
  `version.FrontEnd`, `test-fe` and `fe-scope-check` (**U6**); then delete the readability,
  html-to-markdown, terminal/signal and `langchaingo/llms` dependencies. The old systems are not a fallback: nothing waits on the plugin route to remove
  them. Done.
- [ ] **B2. `recall_query_latency_*` does not measure recall.** The sim's p50/p95/p99 gauges
  (`metricRecallQueryLatencyP50/P95/P99`, `internal/scenarios/sim/workload.go`, populated in
  `sim/telemetry.go`) copy turn-duration percentiles instead of timing recall queries. (SPEC §9.4.)
- [ ] **B3. opencode database cleanup.** Checkpoint compactions must not grow opencode's database without
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
  mutate shared authoritative state — thread registry, thread files, the §3.0 chain — and need real
  synchronization at the memory store boundary); **per-session working set** ("the active thread"
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
  supported; candidate mapping: agent definitions as directives-slot directives scoped per agent, skill
  content as transient task-class.
- [ ] **B8. `/compact` and `/clear` mapping.** Both are no-ops today because memory is not keyed on
  session (SPEC §1.1). Decide what they eventually map to and whether a TUI plugin can replace or
  disable them. (`OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §§4, 6.)
- [ ] **B9. Resume cases not exercised.** A graceful stop with a turn in flight; a standalone-kill
  claim later swept by a managed server; the resume budget, background-job, subagent and shell
  recovery; how a real model answers a resumed request. (`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6.)
- [ ] **B10. Memory vetting build.** Review of memory text before commit by a model applying a fixed
  rubric; a single write path for behavior and process memory with provenance on every outcome; a
  user-configurable acceptance policy that never blocks a turn; periodic re-review of standing
  behavior and process memory; mechanical write checks and batch content review for career memory. Open within
  it: the default acceptance policy, the rubric's content, which model reviews (through opencode, **U5**), the
  re-review cadence. (SPEC §1.8; ARCHITECTURE "Memory vetting".)
- [ ] **B11. Feedback directive tuning aggregation.** The cross-project digest, fallback dissection and
  feedback directive tuning (the old "Phase 5"): tuning may propose a behavior-memory change only from
  patterns aggregated across projects, through the B10 review path. Define the boundary between
  behavior memory and process memory: every behavior adjustment learned in a project resolves to
  exactly one of (a) promotion to universal behavior memory, or (b) a process-memory proposal
  scoped to a project category or role, never a per-project behavior variant. Build the
  process-memory store it proposes into: directive text whose changes take the B10 review path.
  Open within it: the store's file layout and scope values, its place in directive precedence, how
  the categories that apply to a project and the active role are determined (B7's per-agent
  directives are a candidate for role). (SPEC §1.8; THESIS "Three kinds of memory"; ARCHITECTURE "Personalization through feedback directive
  tuning".)
- [ ] **B11a. Fallback dissection.** Intended design: it fires when budget pressure
  exceeds `dissect.pressure-threshold` and normal closing is not keeping up; the clusterer groups
  the oldest poorly threaded content into related clusters; each cluster becomes a thread
  directory and registry record shaped like a normally closed thread and closes through the
  closing flow; one mechanism covers the wandering user, one thread overrunning its share, and
  content never cleanly threaded.
- [ ] **Career memory has no store or write path.** SPEC §1.8 routes a pattern seen in one project
  to career memory (a technique); nothing writes one and no layout exists, so B10's career-memory
  write checks and batch review have no subject yet (ARCHITECTURE "Career memory").
- [ ] **B12. Enforcement of "the service is the only writer of the store directory" (ARCHITECTURE "Automatic vs model-controlled").** opencode's shell reaches
  `~/.personant/`, so the statement cannot hold as written. Decide whether to enforce it (for
  example a plugin hook on shell tool calls) or restate it as intention in SPEC and ARCHITECTURE.
- [ ] **B13. `personant init` refuses an existing store directory.** `init` creates a store directory and nothing else: it
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

- [ ] **Tracked-file content in the history repository.** The day commit puts each thread's
  tracked-file store (`files.jsonl`, `files/`) in the history repository, and archival's capture
  commit pins it there too, so the content of every file the assistant read or wrote is kept
  permanently. SPEC §3.9.1 requires the store to live only
  in the store working tree and the turn repository. Day-rollover completion's restore of the store
  from history repository HEAD depends on the current behavior (ARCHITECTURE "Tracked-file store").
- [ ] **Anchor projection can emit an anchor `personant verify` rejects.** `turn.ProjectAnchors`
  ranks high-specificity identifiers first with no length bound, and extraction admits URLs, file
  paths and hex ids up to 64 characters (`internal/turn/chain.go`), while `verify` errors on an
  anchor outside 3–50 characters (`internal/verify/verify.go`), so a thread whose top symbol is a
  long path fails the day-commit's thread registry integrity check.
- [ ] **`digest.json` is tracked in both repositories.** The managed gitignore block
  (`internal/store/init.go` `seedGitignoreBody`) ignores `symbols.jsonl` as derived but not
  `projects/prj_<n>/digest.json`, so the day-commit step captures each digest before the
  derived-rebuild step refreshes it, and a history-repository commit can pair a `spine.jsonl` with
  a stale digest. Quarantine-and-proceed treats derived files as absent from history-repository
  HEAD (`internal/memops/fileadapter/fileadapter_barrier.go`).
- [ ] **`meta.json` `thread_count` is never written.** `memops.ProjectMeta.ThreadCount` is
  decoded and `personant verify` checks it is non-negative (`internal/verify/verify.go`), but no
  code assigns it, so every project reads 0; the live count is `digest.json`'s
  (`internal/index/digests.go`).
- [ ] **Closing summaries are not length-bounded before they are written.**
  `curator.DraftClosure` (`internal/curator/curator.go`) only trims the summarizer's draft, and
  `applyClosureResolution` (`internal/turn/closure.go`) writes it to `spine.jsonl` as is. The only
  check is `personant verify`'s `spine.entry-max-chars` bound (`internal/verify/verify.go`), which
  `CheckSpineIntegrity` runs at the day-commit, so an over-long draft fails the day rollover's
  thread registry integrity check (SPEC §2.2).
- [ ] **Tag-less conversational turns are not kept.** `internal/turn/engage.go` returns before
  writing any excerpt for a turn with no topic tag and no file edits, so its prompt and response
  are unfindable, against SPEC §3.4's completeness invariant. Session-as-thread routing (A10)
  gives every turn its session's thread as owner.
- [ ] **Wire the mid-day re-baseline arming trigger.** The default-off knob that nukes and recreates
  the turn repository when loose objects cross a threshold mid-session has recovery handling and marker
  plumbing in place; the arming trigger is not wired. Adjacent: whether intra-day derived-watermark
  staleness needs finer handling. (SPEC §3.11, §4.5.8 "Intra-day derived staleness".)
- [ ] **Directive-file plumbing for the §2.6.1 parameters.** No §2.6.1 parameter is read from directive files; every
  key (`context.token-budget`, `history.cap-per-thread`,
  `anchor.projection-max`, `spine.entry-max-chars`, and so on) is a compiled-in constant. The
  reader is built with its first consumer. (SPEC §2.6.1, §3.10.3, §3.1.)
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
  arrives. (ARCHITECTURE "Port-and-adapter at the memory store boundary".)
- [ ] **Flush-job re-enqueue on failure.** A failed embedding flush job is not re-enqueued. Parked
  until live-run data shows failed embeds. (`internal/recall/measure`.)
- [ ] **"Six-month" phrasings in Go comments.** Comments in `internal/memops`, `internal/autogit`,
  `internal/clock`, `internal/store` and `internal/scenarios/sim` still say six-month; the
  acceptance gate is 120 days. Sweep each file when next touched. (SPEC §9.1.)
- [ ] **Code comments cite issue numbers.** About 460 comment lines in about 100 Go files cite
  `#NNN` issue or burndown numbers (`git grep -n -E '#[0-9]+' -- '*.go'`); comments cite no
  decision or review id, so a comment-only pass drops them and keeps the invariant each states.
- [ ] **`last_active` is never refreshed.** `meta.json` `last_active` (ARCHITECTURE "Project
  metadata": updated on any engagement) has no writer, so the other-projects slot's
  most-recently-active order (`internal/workset/workset.go`, SPEC §3.1) is the creation order.
- [ ] **Intra-thread recall finds but does not deliver.** An intra-thread match is logged as a
  match-fire and not fetched: nothing puts a thread's earlier excerpts into the composed request
  (SPEC §3.4 in-context exclusion; `internal/turn/recall.go`).
- [ ] **The sim never resolves a project.** The harness seeds projects with
  `store.SaveProjectMeta` (`internal/scenarios/harness_setup.go`), so `ResolveActiveProject`
  (SPEC §4.5.7) runs only in unit tests.
- [ ] **Named §9.4 scenarios and churn tests are missing.** SPEC §9.4's heavy-closing and
  same-anchor-collision scenarios and the churn level have no test in `internal/scenarios`.

---

## Mid-term

- [ ] **No metric measures recall fetches.** Recall fidelity scores match-fires, and the sim
  oracle copies the runtime's own fetches, so the auto-fetch bar, the top-3 bound and the
  resident skip have unit tests but no acceptance series (SPEC §9.4).
- [ ] **Recall's model-judgment signal is not built.** SPEC §3.4 signal 3; candidates are fetched
  on the symbolic and embedding signals alone (ARCHITECTURE "The model drafts, deterministic code
  writes").
- [ ] **Event-log rotation.** `personant init` creates `logs/archive/` (`internal/store/init.go`)
  and startup recovery leaves it alone (`internal/recovery/logs.go`), but nothing rotates logs
  into it. Intended layout: monthly gzipped tarballs of logs older than 90 days
  (`logs/archive/YYYY-MM.tar.gz`), which would be the store's only non-text file and needs a
  storage class.
- [ ] **Cross-project recall.** Not built: the other-projects slot carries each project's digest,
  but no recall path scans it, and a cross-project fetch or engagement is declined
  (`internal/turn/lru.go` `crossProjectDecline`; SPEC §3.1, §3.2), though SPEC §2.6.1 names
  `recall.cross-project-threshold`. Intended design: the digests scanned each turn
  by the automatic-fetch path, at a higher threshold.
- [ ] **No production trigger for consolidation.** `MemoryOps.Consolidate` runs only from the
  turn-repository loose-object check (`internal/memops/fileadapter/fileadapter_recovery.go`) and
  the scenario harness's day-off step, and `measure.RebuildTrees` only from that step
  (`internal/scenarios/harness_run.go`), so outside the simulation no summary tree is built and
  intra-thread recall always takes the flat scan (SPEC §3.4 *Long threads*).
- [ ] **Deterministic-only memory.** Replace the memory-side model judgments — the summarizer,
  the clusterer, recall's model-judgment signal and the vetting reviewer (SPEC §8.2.1) — with
  deterministic code, so the memory service needs no model's services at all.
- [ ] **Archive recall.** Match `archive/index.jsonl` anchors and fetch on restore so a
  cold-storage thread resurfaces through the same automatic mechanism as a live one. The index
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

- [ ] **Derived index if scale forces it.** bbolt first (a read-heavy, pure-Go B+ tree); SQLite
  only if queries become SQL-shaped; Pebble (write-optimized) and LMDB (CGo) rejected. Trigger:
  simulation data showing storage I/O dominates the sim's wall-clock per simulated month, most
  plausibly in long inference- or embedding-in-loop runs; not a planned upgrade. The index sits
  beside the authoritative text files, which stay authoritative, behind the `MemoryOps` port, and
  is rebuilt by `MemoryOps.RegenerateDerivedState` and drift-checked like any derived file.
- [ ] **Transient-data event-log compaction and class-aware tool-output budget.** Compaction of the
  event log's transient data, and a budget on tool output that differs by data class. Builds on A7,
  which sets per-slot budgets and the whole-request bound but does not classify tool output by
  class. (SPEC §3.0, §3.1.)
- [ ] **Encrypted at-rest storage** (back of mind).
- [ ] **Configuration migration on system upgrade**, covering opencode-held settings that
  `personant init` writes as well as `~/.config/personant/`.
- [ ] **Multi-user** (v2.0; locked, not under active consideration).
- [ ] **Weight-baked instinct from outcome history.** Once local fine-tuning is mature and stable,
  the memory store's *outcome record*, not its content, becomes a training signal for a personal
  alignment adapter. The memory store stays recall; weights become instinct. Baking memories by tuning on
  raw memory store content is a bad trade (it displaces pretrained factual capacity for narrow recall
  the memory store already holds verifiably); instead train on the shape of what worked and failed
  (decision durability, estimate calibration, code-survival durability, user-correction patterns)
  by preference-pair learning (DPO/IPO-style) over `(situation, approach_taken, observed_outcome)`
  triples, producing a small low-rank LoRA on later layers. Constraints: the memory store remains
  source of truth; each training pass is gated, reviewed and reversible (pre-tuned model
  preserved, LoRA tagged with its memory store snapshot hash, eval-gated promotion, rollback on
  failed evals); memory store items used in a pass get a `consolidated_at:` annotation so nothing
  bakes twice. Depends on mature local fine-tuning, outcome-labeled memory store content at scale and a
  tested eval methodology.
- [ ] **Memory consolidation.** Full design capture is kept here;
  `ARCHITECTURE.md` keeps a short now-stub covering what is built (`MemoryOps.Consolidate` git
  gc/repack, SPEC §3.11; `measure.RebuildTrees`) plus the interface fact SPEC §3.10.8 needs.

  Personant's memory layering is deliberately analogous to organic memory: working short-term (the
  live working set), consolidated long-term (thread registry and thread bodies, mostly read), archival cold
  storage (off the thread registry, rarely touched), and a metadata layer for operating on active context fast.
  Under sustained use this organization fragments — threads close out of order, the thread registry accretes,
  derived structures and archival boundaries drift from ideal packing. In-turn maintenance keeps the
  memory store *correct* but not *orderly*.

  The remedy is an offline **consolidation cycle**. During idle time (the day off, or any unused
  window) the runtime runs reorganization it cannot afford mid-turn: re-packing fragmented
  structures, compacting the thread registry, advancing archival (beyond the git gc/repack and summary-tree
  rebuild already wired), and making the final keep/toss calls on data the faster in-turn
  transient-data lifecycle (§3.0) left questionable — the SPEC §3.10.8 content-retention precision
  layer. It is also where batch content review of career memory (B10) runs. Concurrent sessions
  (B5) create exactly the tangle this pass would clear. The day-off is already a real idle window
  in the acceptance workload model, and closing (§3.5) and archival (§3.8) are the mechanisms a
  consolidation pass would tidy.

---

## Parked plans

`ROADMAP_DESIGNS/` holds designs not under way. Those that still apply:

- `OPENCODE_SERVER_REPLACEMENT.md` — the fallback if the plugin route cannot give the memory system
  the control it needs (ARCHITECTURE "Plugin route").
- `code-mode-support.md` — applies only to that fallback.
- `within-thread-interleaving.md` — design for within-thread topic interleaving in the sim
  workload; not implemented (SPEC §9.1 "Workload interleaving").
- `folio-deferred-shrink.md` — unbind writes loose copies without fsync and drops the duplicate
  excerpt-bundle entries only once the copies are sealed in a day-commit; follows the excerpt-bundle plan.
  Open: whether a power-loss-torn loose copy is accepted as a residual or readers fall back to the
  excerpt bundle.
