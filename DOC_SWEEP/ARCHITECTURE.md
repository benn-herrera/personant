# ARCHITECTURE – Personant

How this project's implementation meets requirements designated by SPEC

Must-see:
- No user-facing behaviors or outcomes are designed here, that belongs to SPEC
- No project-specific process is dictated, that belongs to CONVENTIONS

## What this is

Personant is a memory system for a coding agent that runs under opencode. It owns
what enters every model request — composition, compaction and summarization, and recall — to meet SPEC §1 (the effect: THESIS "What is new"). opencode owns everything else: the terminal
UI, session storage, agents, tools, permissions, providers and configuration.

---

## Plugin route

Standard pattern: THESIS "The building blocks", rows "The opencode plugin" and "The memory service".

This section cites six research reports, all under `OPENCODE_RESEARCH/` at the repository root:

- the integration-options report, `OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md`
- the pre-probe results, `OPENCODE_RESEARCH/OPENCODE_PREPROBE_RESULTS.md`
- the probe results, `OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md`
- the resume probe, `OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md`
- the context-injections report, `OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md`
- the server survey, `OPENCODE_RESEARCH/OPENCODE_SERVER_SURVEY.md`

opencode `file:line` citations stay in those reports.

### Tense

In this section and the ones that follow it, present tense states the production
design. Facts about the prototype, the throwaway implementation built to test this route, are marked
"the prototype"; the production service and plugin are not built. The prototype lives on branch
`probe-path` and is not on the build branch: every path, recipe and executor this document names as
the prototype's (`opencode-plugin/index.ts`, `internal/opencodehook/`,
`internal/turn/probe_phases.go`, `internal/scenarios/opencode/`, the `phases` and `opencode` sim
executors, `sim-opencode`, `probe-smoke`, `probe-resume`, `probe-deps`) resolves there (probe
results §§6–7). Carrying the baseline comparator (the `sim-baseline` and `sim-fidelity` recipes and the committed baselines) to the build branch is ROADMAP A2; rebuilding the rest is A3 and A5.

An **Open:** line marks a decision not yet made, and a tag such as U7 names it (CONVENTIONS "Document conventions"); ROADMAP "Pending decisions" lists the U-tags, and ids such as A2 or B9 are ROADMAP items.

Personant runs under unmodified opencode as two pieces:

- **Plugin** (`opencode-plugin/`). TypeScript, loaded by opencode in its own Bun process. An
  adapter that holds no memory state: it forwards hook inputs and events to the service and applies
  the answers, and imports only what the host supplies, so all memory logic is in Go
  (integration-options report §2.1). It keeps only transport counters, none of them memory: the sequence number it stamps on each forwarded event, because RPCs can reach the service out of order (see "Ordering and correlation"), and a fail-open count the opencode-in-loop harness reads (`oc_*` hook fail-open counts). The prototype also carries round-trip timings for the harness. All of it lives in the plugin process and starts at zero on a reload.
- **Memory service** (Go). The only writer Personant provides for memory state (see "Automatic vs
  model-controlled"); it composes every model request. The plugin reaches it over loopback HTTP +
  JSON with `fetch`, because Bun's `node:net` and child-process APIs are unverified from plugin
  code (integration-options report §5, item 6). Several opencode servers sharing one service: see
  "Concurrent sessions".

Data flow: opencode's session runner calls the plugin's hooks and emits events; the plugin forwards
each to the memory service over loopback HTTP + JSON; the handler drives an `internal/turn`
phase, which reaches storage through the `memops` port; the answer returns the same way.

### Boundary

Outside context management, Personant uses what opencode has chosen, as it is; the one exception is cleaning up the rows its checkpoint compactions leave in opencode's database (SPEC §1.7; see "opencode database cleanup"). Where
opencode's machinery would decide what the model sees (compaction, history replay), the plugin
takes that decision over through a hook; it does not reimplement the machinery. The host is
opencode, not Charm's Crush: Crush reads provider API keys from environment variables and runs
its TUI in-process with its backend, so there is no protocol to sit under.
### Pinned release

The supported opencode release is not yet stated: the production plugin and service are not built. The prototype states it in the version check of its `sim-opencode` recipe, on `probe-path`. The source clone at `../opencode` is kept on the installed
release's tag, so the code read is the code run.

### Failure handling

Hooks have no timeout and run sequentially (integration-options report §2.2), so every plugin RPC
carries its own timeout. A Promise-API hook's rejection becomes an Effect defect (a rejected Promise-API hook surfaces as an unhandled failure in opencode's Effect runtime, integration-options report §2.2), so the plugin
catches every failure itself. The prototype plugin fails open: on
timeout or error it leaves the hook input as opencode built it and reports a fail-open count.

**Open:** U7 — the production failure policy, fail open or fail closed (SPEC §1; ROADMAP U7). What opencode does when the `context` hook throws or the service refuses is unestablished (resume probe §6).

### Integration shape

Each element names the opencode mechanism it rides on (integration-options report §2.3 unless
noted).

- **Composition: the `session.context` hook.** It fires on every primary step (a step of the main agent's model loop, not opencode's title or compaction call) and every retry;
  `system`, `messages`, `tools` and `options` are all mutable. The plugin sends the hook input to
  the service and replaces `system` and `messages` with the answer. The service builds the answer
  as `turn.ComposeRequest` does: the composed working set (SPEC §3.1) as the system prompt, the transcript
  tail (the newest messages of the service run's in-run history, bounded to 35% of the live-turn reserve; `boundHistoryTail`, `internal/turn/livebudget.go`), the current user input and this turn's tool messages. Tool-call/tool-result pairs in the
  returned `messages` stay intact (integration-options report §4, item 4). Any other model call is
  kept off the provider: the `session.title` and `session.compaction` hooks supply `result`, which
  skips the model call, so no model request reaches the provider uncomposed while the plugin answers its hooks (SPEC §1); on a hook failure the prototype fails open (see "Failure handling", U7).
- **Wire shape.** A composed request carries one non-empty system part, no empty parts and no
  mid-conversation system messages. Under that shape prompt text reaches the model byte-exact
  (pre-probe results §5; probe results §6, lesson 8). The hook also resets `options.maxTokens`:
  opencode fits it to its own estimate of its own message history, which is too low for a smaller
  composed request (context-injections report §3).
- **Compaction: `compaction.auto: false` plus plugin-answered checkpoint compactions.** With auto
  compaction off only manual compaction runs, overflow included. opencode reads stored message rows
  from the latest compaction onward on every step (integration-options report §2.4), so the service
  schedules checkpoint compactions at a turn cadence to keep that read bounded (**Open:** the
  cadence parameter's name and default; the prototype takes it as a constructor argument,
  `-sim.checkpoint-cadence`). The plugin enqueues one with `session.compact` and the
  `session.compaction` hook answers with `result`. The checkpoint compaction's summary is a fixed
  placeholder that the `context` hook discards on replay. A checkpoint compaction is a read bound,
  not a memory mechanism. `experimental.idle` (an opencode plugin hook, integration-options report §2.3) only decides whether a checkpoint compaction is due:
  it fires before the turn's final step event and again after one, so it never closes a turn
  (probe results §6, lessons 1–2).
- **`/compact` and `/clear`** (context-injections report §4). A user `/compact` cannot be refused;
  it is answered like a checkpoint compaction, and `session.compaction.started` carries a `reason`
  that separates the two. `/clear` is client-side only; it discards nothing because memory is not
  keyed on the session. This is how SPEC §1.1 holds. **Open:** what the two commands eventually map
  to, and whether a TUI plugin can replace or disable them (context-injections report §6).
- **Tools.** Any memory tool registers through `tool.transform`; a tool invented inside the
  `context` hook is dropped.
- **Config.** `personant init` writes the settings opencode must hold (`compaction.auto: false`,
  the plugin entry) into opencode's own config in opencode's own format. Personant-only settings
  live in `~/.config/personant/` and never overlap opencode's (see "One inspection location").
- **Code Mode and plugins.** opencode's own server runs, so its JavaScript plugins and Code Mode (SPEC §1)
  work unchanged.
- **Sessions and projects.** The project resolves from opencode's session directory through
  project resolution (see "Project identity"). A session reads and writes the one memory; turns
  are assigned to threads, never to sessions; deleting a session does not touch memory
  (SPEC §1.1).

### Event observation

Every context change reaches the per-delta pipeline (see "The per-delta pipeline (§3.0)") through one of
these opencode mechanisms:

| Context change | opencode mechanism |
|---|---|
| `user.prompt` | `session.prompt` hook, before the prompt is stored |
| `tool.result`, `fs.read`, `fs.write` | `tool.execute.after` |
| `fs.commit` | `tool.execute.after` of the shell tool, for a commit of a tracked path |
| `user.shell-capture` | `session.shell.ended` event (context-injections report) |
| `model.response` | `session.text.ended` and `session.step.ended` events, ingested at turn close |
| turn close | `session.execution.succeeded` |
| turn abort | `session.execution.failed`, `session.execution.interrupted` |
| fork, revert, delete | `session.forked`, `session.revert.committed`, `session.deleted` events |

The service records which deltas it has applied per turn, so a missing one is detectable (the
`oc_missed_deltas` check, see "opencode-in-loop harness").

The prototype service maps only `read`, `write` and a shell `git commit` to deltas
(`internal/opencodehook/service.go`, `tool`); other tools' `tool.result` deltas are not mapped.

**Open:** `edit` — SPEC §3.0.1 requires the post-edit file bytes as an `fs.write` delta; no
measurement probe established that opencode's `edit` exposes them, and the prototype does not map
`edit`.

**Open:** fork, revert and sub-minds — how a branch's work is incorporated or discarded (SPEC
§1.1), decided together. The events above are observed; what memory does on them is undecided.

**Open:** agents and skills — whether and how opencode agent definitions and skills are supported (ROADMAP B7).

**Open:** provenance — no thread carries `derived_from` through opencode, while in-process runs do
(probe results §5). The cause is unverified; candidates are tool deltas now applying after
`user.prompt` (probe results §11) and the mid-turn fetch not firing.

### Ordering and correlation

- **Tool results apply in tool-call order.** opencode runs one response's tool calls concurrently
  (probe results §6, lesson 3), so arrival order is not repeatable. The service holds tool results
  and applies them in call order at the next `session.context` call or at turn close. This makes
  memory state independent of completion order (SPEC §1.5). The prototype reads each call's
  position from the id its mock executor assigns (`call_<step>_<index>`); **Open:** the production source of tool-call
  order — a real provider's call ids carry no such index, so the order must come from the response
  itself, and no source is established.
- **Sequence numbers are taken in the plugin as each event arrives,** with forwarding chained,
  because RPCs can reach the service out of order (probe results §6, lesson 6).
- **Events correlate by opencode message ID,** not by "the current turn", so a resumed execution's
  late events cannot close another turn (resume probe §5.3, item 3).
- **Model requests are counted at the provider side,** never from `session.model.request`, which
  reports `title` and `compaction` kinds even when hooks answered them (probe results §6, lesson
  4).
- **File content.** `fs.write` content comes from the `write` tool's input; its output is only a
  confirmation line (probe results §6, lesson 5). The tracked-file store holds file bytes
  (SPEC §3.9.1), while opencode's `read` returns formatted output (a header and `N: ` line
  prefixes); the prototype stores that output as returned (probe results §9, item 3). **Open:** how
  the production service recovers the file bytes from `read`'s formatted output.

### Injection handling

opencode injects content into a request besides the user's prompt: instruction files, nested
`AGENTS.md`, reminders, MCP content, tool definitions (context-injections report §1). The service
turns each into state rather than history, so that nothing is stale and nothing is counted twice
(SPEC §1.3).

- **Instructions slot.** Rebuilt each request from the full per-source texts in the instructions
  system part's `metadata.experimental.instructions`, a list of `{key, text}`. A source's content
  hash decides whether the slot changes. opencode's instruction-update diff messages are not used,
  so no stale baseline exists to patch.
- **Nested `AGENTS.md`.** opencode injects one once, on a `read` into its directory, and never
  re-sends it (context-injections report §3). The service captures it into the instructions slot
  as its own keyed source, where it stays for as long as it applies.
- **Message rows are typed by joining** the hook input's message `id` against the plugin's session
  API, because the hook input drops metadata on synthetic messages (context-injections report §2).
- **Drop on re-injection.** Content already accounted for is dropped: by (source key, content hash)
  for instructions, by stored message `id` for message rows. Checkpoint-compaction messages are
  dropped (context-injections report §5).
- **Expectations slot.** The todo list, plan/agent mode, an open question or permission request,
  reminders still in force, the subagent session ID needed to continue a child, and background-job
  results render as current state, not history. The slot is dynamic: its content changes mid-turn
  (SPEC §3.1).
- **Tool definitions.** Sent with every request and counted against the token ceiling. opencode
  curates them per agent and passes them through, with no budget or overflow form (context-injections
  report §5). MCP tools are recognizable by the `<server>_` name prefix.
- **Stand-in line for evicted content.** File content dropped from a request for budget is replaced
  by a stand-in line that pins the version (a content hash), points at stored bytes when replay
  cannot reproduce the content, and reads as a display line the model can expand (see "Display
  lines are how the model recognizes threads"; SPEC §1.4).

**Open:** per-slot policies — all four for the instructions and tool-definition slots, and budget,
priority and overflow form for the expectations slot (SPEC §3.1). Without a tool-definition slot
the whole-request ceiling cannot be enforced (ROADMAP A7).

**Open:** a way to tell an MCP prompt expansion apart from a typed prompt; the hook input does
not distinguish them (context-injections report §2). Until found, an expansion is handled as user
input.

**Open:** injection unknowns (context-injections report §6) — whether plugin activation order
equals hook registration order after a reload, and how system-role messages are lowered on provider
routes other than openai-chat.

### Hook-driven turn phases

`turn.RunWithInfo` owns a whole turn in-process. Under opencode, opencode owns the model call and
the tool loop, so the memory service is driven by hook events through entry points, one per turn
phase, instead. Composition is already separable: `workset.Compose` is a pure renderer
reached through the `MemoryOps.ComposeWorkingSet` port.

| Phase | Driven by | Covers |
|---|---|---|
| `BeginTurn` | `session.prompt` | budget, live-turn reject (refuse a single user prompt that exceeds its live-turn share; `internal/turn/livebudget.go`), day rollover, pre-turn snapshot, `TurnNumber++`, prompt journal, expiry of staged symbols, pre-event deltas (file deltas a step carries ahead of its prompt, `Step.PreEvents`), `user.prompt` delta |
| `ComposeRequest` | `session.context` | working-set composition and request assembly, from this turn's messages in the hook input |
| `IngestToolResult` | `tool.execute.after` | tool-result deltas, held and applied in call order |
| `IngestModelResponse` | `session.execution.succeeded` | tag strip (from the body the service keeps for in-run history; whether the text opencode stores and shows is stripped is U3, see "Topic-tag mechanics"), response journal, ceiling check, `model.response` delta |
| `CloseTurn` | `session.execution.succeeded`, after `IngestModelResponse` | engagement, closing scan (the decay check for threads due to close), `CommitTurn`, working-set persist, transcript tail; idempotent |
| `AbortTurn` | `session.execution.failed`, `.interrupted`, or a phase error | pre-authoritative rollback (before any authoritative write has landed) to the pre-turn snapshot |

In the prototype, the phases exist in `internal/turn/probe_phases.go`; `RunTurnInProcess`
recomposes them into the in-process turn. `RunWithInfo` is not yet re-expressed over them. The
phases call 34 unexported identifiers of `internal/turn` (probe results §8), so they stay in that
package; `just probe-deps` regenerates the list.

#### Close ordering

The response is the turn's `session.text.ended` texts concatenated in event
order, with the last `session.step.ended` token counts as usage (the prototype's buffering, probe
results §9, item 4; `internal/opencodehook/service.go`, `close`).

**Open:** the resumed turn (SPEC §3.0.4; see "Start order and resume"). No phase covers an entry
on a context call with no preceding prompt: the prototype service answers it with an error
(`compose`). Undecided: which `BeginTurn` steps a resumed turn runs, and what its `user.prompt`
content is — opencode's synthetic continue text or the original prompt (resume probe §5.3, item 1).

**Open:** mid-turn prompt — a `steer` prompt (a prompt the user sends while a turn is running) can arrive after tool results; whether it opens a new
turn or joins the turn in progress (SPEC §3.0.4). `BeginTurn` is written for one prompt per turn.

#### Invariant for the production phase split

`RunWithInfo` is re-expressed on the phases, `just
test` passes, and the in-process simulation reproduces its committed baselines
(`internal/scenarios/testdata/baselines/`) with no metric change. A baseline change means the split
altered behaviour.

**Open:** U8 — what `AbortTurn` does with an interrupted turn. Until decided, SPEC §4.3.3 binds: a
retracted prompt does not enter memory, and the rollback restores run-volatile state
(`internal/turn/rollback.go`): the turn counter, the cross-turn staging buffer, active-set and
dormant-set membership, history, recall-surfaced marks and embedding debt (excerpts scrolled out of the recent-excerpt window and not yet embedded; the cap is the unembedded backlog limit, 16), all to the pre-turn
snapshot. The `user.prompt` delta has already run (pipeline step 1) when a retraction arrives,
which is why staged symbols need the restore. The rollback also releases the turn's journal scope,
the span in which the turn's journal is non-empty, so the next start opens with no turn to recover.
It happens in memory only and must not perturb the write ordering crash recovery depends on.
Failures after an authoritative write do not roll back; recovery owns them.

### Start order and resume

- **The memory service is listening before opencode starts,** including opencode's managed
  background server, which sweeps and resumes interrupted executions at boot (resume probe §§2,
  5.3).
- **Resumed turn.** The managed server resumes an interrupted turn with a synthetic continue
  message and a model step that has no prompt hook; the `context` hook still fires (resume probe
  §2). The service opens a resumed turn on a context call with no open turn, derives the turn's
  suffix from the hook input or stored message IDs rather than from process memory, composes the
  request and ingests the resumed reply (SPEC §1.6, §4.5.8 (10)).
- **Reconcile at service start.** After crash recovery (SPEC §4.5.8), the service diffs
  opencode's stored messages — the durable record of what the user saw — against memory's last
  committed turn, and re-ingests each turn opencode completed that memory rolled back, or records
  it as lost (resume probe §5.3, item 2). **Open:** U8 — whether reconcile skips turns rolled back
  by retraction; no record distinguishes a retraction rollback from a crash rollback.
- **opencode's data home never lives inside the store directory** (default `~/.personant/`; see
  "Store directory layout"). Crash recovery quarantines untracked files there, which would include
  opencode's database (see "One inspection location").
- **Restart persistence.** Only the working set reloads (`working-set.json`). The transcript tail
  and the turn counter, which drives checkpoint-compaction cadence and turn-based decay, reset on every relaunch
  (resume probe §5.3, item 6). **Open:** U9 — whether the transcript tail and the turn counter are persisted, rebuilt from
  opencode's store, or left to the threads.
- **Resume cases not exercised** (resume probe §6) are listed in ROADMAP B9.

### opencode database cleanup

opencode keeps one global SQLite database. Rows before the latest checkpoint compaction are never
read again by the runner but stay on disk (server survey §4.4; SPEC §1.7). The cleanup is not
built. Its rules:

1. **Only when nothing uses the database:** every opencode process, the background service
   included, has exited. Otherwise the run is skipped; skipping costs nothing.
2. **Under an exclusive SQLite lock** taken before any change.
3. **Schema-version checked:** it refuses to run on a migration set it was not built for; tables
   change between patch releases.
4. **Reference-safe:** it deletes only rows nothing still points to — fork origins, revert targets,
   blobs, `session_part`.
5. **Verified:** `PRAGMA integrity_check` and `PRAGMA foreign_key_check` pass, then `VACUUM`.
6. **Proven first on a copy:** opencode started on the cleaned copy loads sessions, renders
   scrollback, and forks and reverts from kept rows.

Whole sessions the memory system has finished with (merged sub-mind children) are removed through
the supported `session.remove` instead.

---

## Module map

| Path | Responsibility |
|---|---|
| `cmd/` | cobra subcommands: `init`, `index`, `verify`, `archive`, `version`; persistent flags `--home`, `--allow-newer-home`. Every verb that opens the memory store goes through the one `openHome` helper (format check, then Reconcile); `version` is exempt from the format check. |
| `internal/memops/` | the `MemoryOps` port: interface plus the domain types it trades in (`SpineRecord`, `Thread`, `ProjectMeta`, `Config`, the symbol enums, `Normalize`, `DominantSource`). The application layer depends only on this; it imports no internal package. |
| `internal/memops/fileadapter/` | the memory-store adapter: JSONL + markdown + YAML + TOML + go-git on the store directory |
| `internal/store/` | file storage for the memory store: paths, JSONL helpers, init, thread registry and project ops, thread I/O, excerpt bundles, project resolution. Depends on `memops` for the domain types. |
| `internal/autogit/` | go-git-backed automatic git wrapper with bitflag systemic-validation policy |
| `internal/recovery/` | startup reconcile orchestrator over the on-disk observables |
| `internal/crashpoint/` | named kill-point hooks, test-armed and a no-op in production |
| `internal/eventlog/` | plain-text event log, one file per day |
| `internal/dedup/` | content identifiers, write-time diffs, excerpt identifier lines and the composition plan; pure; depends only on go-git `plumbing` |
| `internal/turn/` | the per-delta pipeline, the turn loop, per-turn coalescing (the turn buffer), the staging buffer, recall candidates and the decay-triggered flow for closing a thread |
| `internal/recall/scoring/` | pure Jaccard and cosine recall primitives, store-independent; fine level (per-excerpt chunk vectors), beam descent, and the summary-tree build (a per-thread index for long threads, searched by beam descent) |
| `internal/recall/measure/` | application-side recall stack: `Service`, `Recaller`, the single async indexer and its index snapshot, `.recall-cache` sidecars, the consolidation tree builder |
| `internal/recall/exact/` | the exact lexical (flat-scan) recall level |
| `internal/curator/` | the summarizer's closing-summary drafting (SPEC §3.5, §5.2) |
| `internal/workset/` | context composition into slots (directives, registry, other-projects, active-threads, dormant-threads); a pure renderer |
| `internal/prompt/` | system-prompt template, topic-tag parser, stream filter |
| `internal/model/` | model client surface: OpenAI-compatible `HTTPClient`, scripted and generated `MockClient`, `MockServer` (the mock served as an OpenAI-compatible endpoint), SSE streaming |
| `internal/opencodehook/` | Not in this tree (on `probe-path`): prototype memory service: the loopback handlers the plugin calls, each driving one `internal/turn` phase |
| `opencode-plugin/` | Not in this tree (on `probe-path`): prototype plugin: forwards hooks and events, applies answers, per-RPC timeout and fail-open count |
| `internal/scenarios/` | scenario harness, invariants, metrics; `sim/` the acceptance simulation (the prototype's `opencode/` driver and executor is on `probe-path`) |
| `internal/{metrics,clock,index,verify,version,log,testsupport}/` | supporting subsystems: metrics emission, the single clock, derived-file rebuild, structural validation, version identity, logging, live-endpoint test helpers |
| `justfile` | build, test and dependency-vetting recipes; the target inventory lives there |

**Open:** SPEC §4.1 keeps the `search`, `deps` and `log` verbs; `cmd/` has none of them.

---

## Applying the thesis

THESIS states why Personant exists. This implementation applies two design rules at every layer:

> **Deterministic state as authoritative. The model in narrow judgment roles.**

| Component | Role |
|---|---|
| Deterministic (Go service) | authoritative state holder; integrity enforcer; build/query/index operations; automatic git management of the store directory |
| Model | narrow generative/judgment roles: topic tagging, summary drafting (the summarizer, SPEC §2.2), recognition, clustering for fallback dissection (the clusterer, SPEC §3.6), memory-vetting review |
| Host (opencode) | the terminal UI, tools, permissions, providers and every interaction mode. Personant defines no interaction mode or workflow (SPEC §1). |

---

## Recurring patterns

### The model drafts, deterministic code writes

Standard pattern: THESIS "The building blocks", row "Model judgments".

The model never mutates authoritative state directly. It proposes content; deterministic code decides what of it is kept, and writes it.

| Judgment | Deterministic check before it is kept |
|---|---|
| Topic tag: which threads a turn engages, plus an anchor list (SPEC §5.1) | `prompt.Parse` accepts only a line matching the tag pattern within the preamble scan; each thread entry must match `thr_\d+` or `*new-topic*` (the unclosed `*new-topic` is normalized to it); extra tags are ignored with a `topic.warning`. Anchors are normalized (SPEC §2.7.2) and enter the symbol history as `model`-sourced symbols, never directly as anchors: the projection picks the anchors and caps their count. An unknown or other-project thread id is resolved or declined (SPEC §3.2). Anchor length is not checked (ROADMAP Near-term, "Anchor projection can emit an anchor `personant verify` rejects"). |
| Closing summary (the summarizer, SPEC §5.2) | Trimmed; an empty draft is not applied. The anchors written at close come from the projection, not the draft. Length is not checked before the write: `spine.entry-max-chars` is enforced only by `personant verify` at the history-repository write (ROADMAP Near-term, "Closing summaries are not length-bounded before they are written"). |
| The clusterer's grouping (SPEC §5.3) | Not built. |
| Model judgment on recall candidates (SPEC §3.4) | Not built: recall composes the symbolic and embedding signals only. |
| Memory-vetting review (SPEC §1.8) | Not built. |
| Lifetime tag (SPEC §3.10.8) | Not built (the keep/toss stage is deferred); SPEC fixes the fail-safe: a missing or malformed tag keeps the content. |

Recall scoring and loading a thread into the active set are deterministic (SPEC §3.4). The behavior- and process-memory (SPEC §1.8) write path is "Memory vetting"'s, which is not built.

### Automatic vs model-controlled

The service performs many operations the model cannot. Most notably **git**:

- The service owns the store directory's git repositories through `internal/autogit` (in-process
  go-git) at the cadence of the turn and history repositories (see "Crash recovery and the turn
  and history repositories"). No git hook: systemic checks `CheckDerivedFresh` (wraps
  `personant index check`) and `CheckSpineIntegrity` (wraps `personant verify`), parameterized
  only by paths, are passed per call as `GitCheckFlags` (`preFlags`/`postFlags`, 64 slots: a `uint64` bitmask of check functions; `internal/autogit/autogit.go:60`) and are
  applied to history-repository writes; turn-repository commits run none. Operation-tied
  verifications live in purpose-specific wrappers composing `autogit.Checkout`.
- Commits take their author and committer identity from the memory store's git config, falling back to `personant <personant@localhost>`; they are not cryptographically signed
  (`store.CommitSignature`).
- The service reads the user's *workspace* repository only to identify the project: its root
  (`store.FindGitRoot`) and origin URL (`store.GetGitOriginURL`).
- The memory service exposes no git tool for the store directory. The agent has opencode's shell,
  which can reach it, and nothing blocks it; crash recovery treats a broken hand-edit as legitimate
  (quarantine, never delete). That the service is the only writer of the store directory is the
  intent.

### One inspection location

Personant's own state lives in two places, and it reads a third:

- The store directory — memory: the authoritative files and both git repositories.
- `~/.config/personant/` — Personant-only settings (`config.toml`, SPEC §8.2.2). Nothing here
  overlaps opencode's configuration. `internal/store/paths.go` still places `config.toml` and
  `providers.toml` under the store directory; ROADMAP.md work moves `config.toml` and removes `providers.toml` (U5).
- opencode's data home — never inside the store directory (see "Start order and resume"); no
  Personant state, read by the database cleanup and reconcile.

### No speculative prefetch

Every loaded thread is loaded because it was recognized as needed, never guessed.

**Open:** U3 — how a fetch is triggered under opencode: the in-band topic tag or a recall tool (ROADMAP U3). The mid-turn fetch does not fire under opencode today (probe results §5). Until decided, the mechanism below binds:

The registry slot is always in the composed request → the model recognizes a prior thread from its
display line → emits a topic tag → the corresponding threads are loaded into the active set → the
response is generated. The thread is recognized — by the model naming it in a topic tag, or by recall scoring it in the auto-fetch band (SPEC §3.4) — never prefetched on speculation.

### Asymmetric cost: prove ahead of time

When the cost of being wrong is far higher than the cost of being right, the bar shifts toward
"prove it."

- Whether this design meets SPEC over months of accumulated memory cannot be judged by inspection. The §9 measurement regime exists
  because "use it and find out across extended accumulated usage and then revise if it's wrong" has
  no graceful recovery — you'd have to either complex-refactor accumulated memory or lose it all.
- Migration cost counts at decision time (e.g. YAML over TOML for thread frontmatter: the
  off-standard gain was modest, the migration cost high). If it's high, default to the standard
  path.

### Approved-when-earned dependencies

Stdlib-first, but a dep that earns its keep is fine. Two failure modes to avoid:

1. Reaching for a dep before there's a real consumer (speculative scaffolding).
2. Implementing complex stdlib substitutes for a dep that would just work (NIH).

Approved deps land *with their consumer*, not before. `go.mod` is the catalog; CONVENTIONS holds
the project-specific dependency rules, including which SQLite driver a Go SQLite need takes.

### Port-and-adapter at the memory store boundary

Application code (`internal/turn`, `internal/recall/measure`, `cmd/*`) depends on `memops.MemoryOps` — the conceptual operations port — not on the
memory store directly. One adapter ships (`internal/memops/fileadapter`: JSONL + markdown +
YAML frontmatter + TOML + go-git on the store directory); callers and tests depend only on the interface.

Pure renderers (`internal/workset.Compose`, `internal/recall/scoring`) are **store-independent**:
they import `memops`'s plain domain types (`SpineRecord`, `Budget`, `ThreadMeta`, ...) but never
the `MemoryOps` port itself, operating only on data the caller already fetched. They import nothing from `internal/store`; the pure helpers they use (`Normalize`, `DominantSource`) are in `memops`. The scenario harness and its invariants read the memory store directly (`h.Paths`; `store.LoadArchiveIndex` for the archived thread set) — the test-side validator pattern, not an application-code exception.

---

## Storage non-negotiables

Standard pattern: THESIS "The building blocks", row "The memory store".

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`config.toml` under `~/.config/personant/`) + markdown directives | hand-editable; no secret-bearing file (SPEC §2.1) |
| Derived files | regenerable from authoritative files | drift cannot accumulate; `autogit.CheckDerivedFresh` fails a day rollover or archival batch that leaves a derived file stale |
| Git management | automatic on the store directory, never on user's workspace | the service owns the store's git; the user owns the workspace's |

### Port abstraction policy

Design rules for the `memops` port surface:

1. **Default: backend-agnostic naming and types.** Port types describe abstract concepts
   (per-thread metadata, project record, thread registry record), not storage-format details.
   Names that would not make sense for an alternate storage backend (SQL, bbolt, in-memory, etc.)
   are leaks.

2. **No ceremony for hypothetical backends.** Don't pre-engineer abstractions, defensive
   interfaces, or migration scaffolds for storage backends that don't exist.

3. **API surface is not frozen.** The port evolves when concrete evidence — a real second storage
   backend, a discovered constraint — shows the current shape needs to change.

4. **Concessions are explicit, case-by-case, user-approved.** When a backend-specific concept
   legitimately needs to live at the port (performance, debuggability, no abstract framing exists
   yet), it's an explicit exception: name the concession, state the rationale, state what would
   cause it to be revisited. Implicit concessions — a backend term creeping in without
   deliberation — get cleaned up to the default.

---

## Storage and record formats

SPEC §2 states the outcomes: memory lives under one store directory, is plain text and is
versioned in git. This section holds the layout and record formats this implementation uses.
`personant verify` enforces the constraints marked below; `personant index rebuild` regenerates
derived files.

### Store directory layout

```
$PERSONANT_HOME/                    # default ~/.personant; configurable
  spine.jsonl                       # thread registry; authoritative, sorted lexically by id
  symbols.jsonl                     # [derived] inverse symbol → threads index
  threads/thr_<id>/                 # one directory per thread
    thread.md                       # frontmatter (authoritative) + title; no accumulating body
    turns/                          # loose tier: turn excerpts, one file each (0000024.md)
    folio.jsonl                     # excerpt bundle: bundled-tier turn excerpts, one line each
    files.jsonl                     # tracked-file index (SPEC §3.9)
    files/                          # current blobs and kept diffs
  projects/prj_<n>/
    meta.json                       # project metadata (authoritative)
    digest.json                     # [derived] the project's other-projects slot entry
  directives/
    defaults.md                     # baked-in defaults; rewritten from the binary by every `personant init`
    user.md                         # user-wide overrides, including feedback directive tuning
    prj_<n>/project.md              # project-scoped directives
  logs/YYYY-MM-DD.log               # plain-text, append-only, one file per day
  logs/archive/                     # created empty by init; nothing writes it (moving old day files there is not built)
  version.toml                      # authoritative, committed; format revision `format = N` (SPEC §9.1)
  README.md                         # layout documentation for human inspection
  last-active                       # operational; prj_<n> of the most recently active project
  turn-journal.jsonl                # operational, gitignored; in-flight-turn journal (SPEC §4.5.8)
  op-in-progress.json               # operational, gitignored; operation marker, survives reset
  derived-watermark                 # operational, gitignored; turn-repository HEAD the derived files match
  working-set.json                  # operational, gitignored; active-set order + dormant set
  .recall-cache/                    # operational, gitignored; derived recall indexes
  archive/index.jsonl               # authoritative; cold storage index (SPEC §3.8)
  recovery/quarantine/<stamp>/      # operational, gitignored; byte-exact bytes preserved by recovery
  .git/                             # history repository: permanent; one day-commit per day plus archival commits (SPEC §3.11)
  .git-daily/                       # operational, gitignored: turn repository, per-turn, deleted and recreated each day
```

| Type | Examples | Drift policy |
|---|---|---|
| Authoritative | `spine.jsonl`, `threads/*`, `directives/*.md`, `projects/*/meta.json`, `logs/*.log`, `version.toml`, `archive/index.jsonl` | Source of truth; hand-editable (`directives/defaults.md` is rewritten by every `personant init`). `logs/*.log` is the append-only turn-grain forensic record that crash recovery preserves and re-appends. |
| Derived | `symbols.jsonl`, `projects/*/digest.json` | Regenerable from the authoritative files; a day rollover or archival batch fails if it leaves a derived file stale; turn-repository commits are not checked; never hand-edited. |
| Operational | `.git-daily/`, `last-active`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `working-set.json`, `.recall-cache/`, `recovery/` | System-managed, not drift-checked, and gitignored: the managed gitignore block (the part of the store's `.gitignore` that Personant writes and keeps current) lists each, `.git/` and `.git-daily/` included, so neither repository sees the other as untracked (the ignore-block rule; see "Crash recovery and the turn and history repositories"). |

`.git/`, the history repository, is in no row: system-managed and gitignored like the operational files, it is the only home of archived threads (SPEC §3.8.1) and so irreplaceable; a missing one is refuse-to-open.

`.recall-cache/` and `working-set.json` are filed operational for different reasons. `.recall-cache/` holds derived vectors and summary trees (SPEC §3.4 *Persistence*), but regenerating it costs embedding calls, so it cannot be kept fresh at every day rollover and archival batch as derived files are. Instead each sidecar carries a content-staleness key, a stale or missing sidecar is rebuilt on load, and deleting the directory costs only re-embedding. `working-set.json` holds the active-set order and the dormant set, which no authoritative file records and nothing rebuilds. Losing it costs only recency: the next service run starts with both sets empty (`turn.LoadSession`); a malformed file fails the load.

### Thread registry record (`spine.jsonl`)

The file `spine.jsonl` holds the thread registry: one JSON object per line, sorted by `id`, so
diffs stay line-grain.

```typescript
interface SpineRecord {
  id: string;                        // /^thr_\d+$/; monotonic serial
  project: string;                   // /^prj_\d+$/; "prj_default" for unscoped threads
  anchors: string[];                 // projected from history_symbols and stored; 0..anchor.projection-max (8)
  anchors_projected_at_turn: number; // owner turn of last projection change (SPEC §3.2)
  summary: string;                   // 100-150 char closing summary, ≤ spine.entry-max-chars; set from the first response at creation; replaced by the closing summary
  description: string;               // triggering utterance; set once, never rewritten
  state: ThreadState;                // the SPEC §2.2.1 states, as their lowercase names
  created: string;                   // RFC3339
  last_engaged: string;              // RFC3339; read by engagement decay, dormant order, digests
  last_engaged_turn: number;          // service-run turn counter at last engagement; restarts each relaunch (U9); read by turn-based decay and the unknown-thread-id owner fallback
  state_changed: string;             // RFC3339
  turn_count: number;                // owner turns of this thread (SPEC §3.0.4)
  recall_fires: number;              // recall matches that led to a fetch; orders each symbol's thread lists in symbols.jsonl
}
```

`anchors_projected_at_turn` and `description` decode as 0 and empty on old records. `anchors` and `anchors_projected_at_turn` are derived (the anchor projection computes them from `history_symbols`) but stored in the authoritative record. The display line and the registry slot are generated from the record alone (SPEC §2.2, §2.2.2); without the stored copy, composing a request would read every `thread.md`. They are written at owner-turn close and at closing (`internal/turn/engage.go`, `internal/turn/closure.go`). `personant index rebuild` does not regenerate them, and no drift check covers them.

At owner-turn close, after merge → project → trim (see "Projection"), the projected anchors are written
to the record; `anchors_projected_at_turn` is set to the current owner-turn index only when the
projected set differs from the prior one. Every engagement rewrites `last_engaged`,
`last_engaged_turn` and `turn_count` (SPEC §3.2).

#### Constraints enforced by `personant verify`

| Field | Constraint |
|---|---|
| `id` | `/^thr_\d+$/`, globally unique |
| `project` | `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | 0 to `anchor.projection-max` inclusive. 0 is legal (a vague-start thread); verify flags only an over-ceiling count, a memory service bug, since the model never controls this count. |
| `anchors[i]` | normalized form; 3–50 chars |
| `summary.length` | ≤ `spine.entry-max-chars` |
| `state` | a defined state name |
| timestamps | RFC3339; `created` ≤ `last_engaged` |

**Display form** — the display line, generated from the record and never stored; what the model sees of a thread:

```
thr_<id> [<anchors joined with ", ">] — <summary> [<STATE>]
```

`<STATE>` is the state name uppercased.

Example: `thr_88 [trefoil, unknot] — electron body-topology conflict;
entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]`.

### Thread directory (`threads/thr_<id>/`)

- **`thread.md`** — YAML frontmatter (authoritative for metadata except `anchors`, a stored projection) and a single `# <title>` line; no
  accumulating body, rewritten in full on every engagement.
- **`turns/`** — the loose tier of excerpt storage: one terse excerpt file per owner turn, named by
  the thread's turn number zero-padded to 7 digits (`turns/0000024.md`). Appending a turn writes
  one small loose file; the turn path never writes the excerpt bundle.
- **`folio.jsonl`** — the bundled tier of excerpt storage, the excerpt bundle: the thread's bundled
  excerpts, one JSONL line each (see "Excerpt storage tiers"). Absent when nothing is bundled.
- **`files.jsonl`, `files/`** — the tracked-file store (SPEC §3.9); see "Tracked-file store".

```markdown
---
id: thr_88
project: prj_3                       # internal handle; the display name is in meta.json
state: wip
created: 2026-05-06T14:23:00-07:00
last_engaged: 2026-05-08T03:12:00-07:00
state_changed: 2026-05-07T19:42:00-07:00
turn_count: 24
recall_fires: 3
description: how does the trefoil's body topology constrain electron shape?
anchors:
  - trefoil
  - unknot
history_symbols:
  - {raw: "trefoil", normalized: "trefoil", first_seen_turn: 142, count: 17, source: deterministic}
  - {raw: "(3,2)-torus knot", normalized: "32-torus-knot", first_seen_turn: 145, count: 4,
     source: model}
---

# Body topology — trefoil vs unknot
```

A turn-excerpt file (`turns/0000024.md`):

```markdown
## Turn 24 · 2026-05-08T03:12:00-07:00 · [trefoil, unknot, body-topology]

[file read #1a2b3c4d5e6f] src/a.go
[file write #9f8e7d6c5b4a] src/a.go
[file commit @abc1234] src/a.go

**user:** [terse prompt excerpt]

**agent:** [terse response excerpt, topic tag stripped]
```

- **Recent-excerpt window vs retention.** The working-set assembler reads at most 512 (default;
  calibratable) of a thread's most recent excerpts, the recent-excerpt window, sized so that at
  `b-top-k = 3` (SPEC §2.6.1 `layer.b-top-k`) each thread's window stays within `LayerB`/3 bytes (`LayerB` is the
  active-threads slot's budget); the body is read newest-first up to a byte budget, then joined
  oldest to newest. Excerpts are retained past the window, remain recall-indexed, and are the
  durable source the fine-level index embeds for intra-thread recall. An excerpt is
  decision-class content; raw task-class output (SPEC §3.0.1, §3.10.1) is never written as one (SPEC §3.10.6). Durable
  turn content lives only in excerpts, never the event log. Retention trimming is the deferred
  keep/toss stage (SPEC §3.10.8); v0.1 keeps all excerpts.
- **Excerpt storage tiers.** A thread's excerpts are split between loose files (`turns/`, the loose
  tier) and its excerpt bundle (`folio.jsonl`, the bundled tier) so the number of files git tracks in
  the store does not grow with thread length (SPEC §2.3). Excerpts change tier only in the day
  rollover's excerpt-storage step, which settles each thread's excerpts
  (`store.SettleExcerptTiers`, see "Crash recovery and the turn and history repositories").
  - *Bundle format.* JSONL, one line per bundled excerpt, keys in the order
    `{"name":"0000024.md","content":"<the file's exact bytes>"}`, sorted ascending by (turn number,
    name) with no repeated name, written and read through the generic `store.WriteJSONL` /
    `store.ReadJSONL` (atomic write). Like every JSONL file in the store directory, bundle lines keep `encoding/json`'s default escaping: `<`, `>` and `&` are written as `\u003c`, `\u003e` and `\u0026`, and a newline as `\n`, so an excerpt cannot be read in place; any JSON reader (`jq -r .content`) returns its exact bytes. That legibility cost against SPEC §2.1's plain-text inspection is accepted so that one JSONL writer serves the whole store. A line is malformed if it fails to decode, its `name` is not an excerpt name, or
    `content` is absent.
  - *Placement rule.* A pure function of authoritative state: the thread registry record's state
    and the thread's excerpt names. A closed thread (`memops.IsRetiredState`) bundles every
    excerpt; any other thread keeps its newest `store.HotExcerptTail` (= `ThreadTurnWindow` +
    `memops.EmbeddingDebtCap` = 528, the recent-excerpt window plus the steady-state unembedded
    backlog limit) loose and bundles the rest. An excerpt whose bundle line would exceed
    `ReadJSONL`'s 1 MiB line cap stays loose, and a name in both tiers with different bytes (a hand
    edit) moves in neither direction. Loose files can therefore sit permanently below bundle
    entries, as can a hand-made one: nothing assumes the excerpt bundle is a strict prefix of the
    loose files. Idleness and membership of the active or dormant set are not placement signals.
  - *Tier-transparent readers.* `ReadThreadBody`, `ReadDebtWindowExcerpts` and
    `ReadThreadExcerpts` return the union of both tiers, each name once, the loose copy winning
    for a name in both, byte-identical whichever tier holds it. A reader decodes the excerpt bundle
    only when it needs more than min(loose count, `HotExcerptTail`) of the newest excerpts, so a
    non-closed thread after a day rollover never opens it; `ReadThreadExcerpts` reads it whenever
    one exists. A read never moves an excerpt: a re-engaged closed thread reads its bundled
    excerpts from the excerpt bundle until the next day rollover unbundles its newest
    `HotExcerptTail` excerpts. `store.ListExcerptEntries` and `store.ReadFolio` serve
    `personant verify`'s bundle checks.
  - *UTF-8.* Excerpt content is valid UTF-8 (it arrives through JSON decoding). Bundling asserts it
    before writing anything for the thread, since Go's JSON encoder would silently replace invalid
    bytes; a violation fails the day rollover with `store.ErrExcerptNotUTF8` naming the file, never
    a skip or a repair.
- **Tracked-file store.** One index and its content files per thread, written at turn close
  for the owner's file events (`store.ApplyFileEvents`) and pruned in the day rollover's
  file-retention step (`store.PruneTrackedFiles`).
  - **Out of date — SPEC §3.9.1 governs.** Everything here that relies on the tracked-file store
    being in the history repository (the day-commit step, the archival capture commit, and restoring `files.jsonl` and `files/`
    from history-repository HEAD in day-rollover completion) describes the current code, not the
    intended design: the history repository never holds the tracked-file store (ROADMAP
    "Tracked-file content in the history repository").
  - *Index format.* `files.jsonl`, one line per path, sorted by `path`, keys in the order
    `{"path","id","size","turn","at","changes":[{"turn","at","from","to","diff","size"}]}`,
    written through `store.WriteJSONL`. `path` is the event's path as given. `id` is the git
    blob id of the current bytes, `size` their length in bytes. `turn` and `at` are the owner
    excerpt number and timestamp of the latest read or write. `changes` holds the last
    `dedup.live-diff-window` id transitions, oldest first; `diff` says whether the diff was
    kept, and the change's `size` is the diff's length (0 when not kept).
  - *Content files.* `files/<id>` holds the current bytes; `files/<from>-<to>.diff` a kept
    forward unified diff.
  - *Write order.* Blob and diff files are written (`WriteFileAtomic`) before the index that
    names them, so the index never names an absent file; the turn path never deletes.
  - *Retention.* The file-retention step keeps entries with `turn > TurnCount − ThreadTurnWindow`
    and the blob and `diff:true` files they name. It deletes every other file under `files/` and a
    legacy `files.json`, rewriting the index before deleting. The store per thread is bounded by
    the paths touched within the recent-excerpt window × (current size + the diff window).
  - *Identifier lines.* Each file event of a turn is one line in the owner's excerpt,
    between the header and `**user:**`. Composition never parses them; which positions
    expand comes from the index.
- **`description` vs `summary`.** `description` is a whitespace-collapsed copy of the spawning
  prompt, truncated to 120 bytes (`descriptionFromNewThread`), set deterministically (no model
  paraphrase). `summary` is the summarizer's closing summary, in frontmatter and the thread registry record, never in a turn
  file; before closing it is the provisional summary set at creation.
- **No parent thread.** No parent-thread field exists or is synthesized (the flat model, SPEC §3.2).
  Origin provenance is per symbol (`derived_from`); no thread-level provenance field exists.

```typescript
interface HistorySymbol {
  raw: string;                      // surface form as encountered
  normalized: string;               // normalized form (see "Symbol normalization")
  first_seen_turn: number;
  count: number;                    // total emissions across all turns
  source: SymbolSource;
  lifecycle?: SymbolLifecycle;      // authoritative supersession state; omitted ≡ "active"
  ever_central?: boolean;           // latched true on first entry to the projection; never cleared
  last_active_turn?: number;        // last turn in the active projection; omitted/0 ≡ never-active
  derived_from?: string[];          // origin thread ids ("Symbol source"); omitted/empty ≡ organic
}
type SymbolSource = "deterministic" | "model" | "user" | "curator";
type SymbolLifecycle = "active" | "superseded";  // "trimmed" is the absence of the entry
```

A record decoding without the lifecycle fields is well-formed (the zero values above);
`personant index rebuild` regenerates the symbol index from them; the anchor projection is recomputed at the thread's next owner turn.

`history_symbols` is capped at `history.cap-per-thread` (default 40) over active and superseded
entries together. Over the cap, trimming operates on the **trimmable** partition only: a symbol is
trimmable iff it is not `ever_central` and not a high-specificity identifier (URL, file path, git
SHA). Within it, lowest `count` goes first, ties broken by lowest `first_seen_turn`. If the
protected set alone exceeds the cap, the lowest-`count` `superseded` entries go first.

### Symbol index (`symbols.jsonl`)

[Derived] Inverse index, sorted by `symbol`, rebuilt from `spine.jsonl` and `threads/*/thread.md`
frontmatter on every index refresh.

```typescript
interface SymbolRecord {
  symbol: string;               // normalized form
  threads: string[];            // thread ids where it appears anywhere (anchor or history)
  anchor_in: string[];          // subset where it is an anchor
  superseded_in: string[];      // subset where its history entry is superseded (SPEC §3.4)
  source_dominant: SymbolSource; // highest-ranked source across emissions (see "Symbol source")
}
```

`threads` and `superseded_in` are sorted by descending `recall_fires` (most-recalled first).
`superseded_in` is rebuilt from the frontmatter `lifecycle`, the only source of supersession state.

### Project metadata

**`projects/prj_<n>/meta.json`** (authoritative). Three identity levels: the internal handle `id`
(`prj_<n>`, never changes), the remote identity `remote_urls[0]` (normalized git remote, gained
when the tree first acquires one) and the display name `name`.

```typescript
interface ProjectMeta {
  id: string;                        // /^prj_\d+$/, or the reserved prj_default
  name: string;                      // /^[a-z][a-z0-9-]*$/; mutable
  current_root_path: string;         // absolute
  historical_root_paths?: string[];  // appended on a root move; never rewritten
  remote_urls?: string[];            // normalized; primary = remote_urls[0]
  historical_remote_urls?: string[]; // de-duplication memory
  created: string;                   // RFC3339
  last_active: string;               // RFC3339; updated on any engagement
  thread_count: number;              // not maintained: nothing writes it, so it reads 0; a project's thread count is digest.json's
  conventions_paths: string[];       // absolute paths to convention files loaded into the directives slot
  symbol_patterns: ProjectPattern[]; // project-specific deterministic-pass patterns
  ignore_symbols: string[];          // project-scoped stopword additions
}
interface ProjectPattern { name: string; regex: string /* RE2 */; category: SymbolCategory; }
```

Remote URL normalization: lowercase host; strip trailing `.git`; `git@host:owner/name` and
`https://host/owner/name` are the same URL. `prj_default` is reserved for threads created before any
project was active: empty path fields, `name: default`.

**`projects/prj_<n>/digest.json`** ([derived], regenerated with the other derived files: at the day rollover, archival, restore and `personant index rebuild`; other-projects slot content):

```typescript
interface ProjectDigest {
  project: string;            // prj_<n>
  display_name: string;       // resolved at render time
  thread_count: number;
  recent_anchors: string[];   // top N anchors across the N most recently engaged threads
  one_line_summary: string;   // ≤ 80 chars: recent_anchors comma-joined and truncated
  byte_size: number;          // should be ≤ cross-project.digest-per-project-bytes
}
```

### Symbol normalization

Categories: `identifier` (pattern-extractable: file paths, URLs, code symbols, commit
SHAs), `entity` (model-emitted named concepts), `tag` (user-emitted `#hash`).

| Category | Case | Whitespace | Punctuation | Notes |
|---|---|---|---|---|
| identifier | preserved | preserved | preserved | exact match required |
| entity | lowercase | hyphenated | stripped except `-`, `.`, `_` | stop words dropped from multi-word forms |
| tag | lowercase | hyphenated | stripped except `-` | leading `#` stripped |

Multi-word entity stop words (per-project additions in `meta.json`): the, a, an, of, in, on, at,
by, for, to, with, from, as, is, are, was, were, be, been, being. They are dropped only from
multi-word entities; a single-token entity that is a stop word is preserved. Storage is
`{raw, normalized}`: matching uses `normalized`, display uses `raw`.

### Symbol source

When several sources emit a symbol, the highest-ranked wins, `curator > user > model > deterministic` (`memops.DominantSource`): within a turn, across turns and in `source_dominant`. In the symbol index a
thread's anchor counts as `curator`-sourced (`internal/index/symbols.go`): a stored label for
anchors, not summarizer output, since the summarizer contributes no symbols (see "Symbol extraction").
`source` records how a symbol was emitted; `derived_from` records which thread it came from; the
two are orthogonal.

**`derived_from` population** is deterministic (no model, no human). At each turn's symbol-history
merge, for every symbol emitted into the engaged thread: if that normalized symbol also belongs to
a thread currently resident among those recall has loaded into the active set (by turn-close
acceptance or the mid-turn fetch) and not yet demoted, that thread's id is unioned into the
symbol's `derived_from`. A turn-close acceptance attributes from the next turn, a mid-turn fetch
within the same turn. Eligibility ends when the parent is demoted. The set is monotonic. Recall does
not write symbols into a thread, and `derived_from` does not take part in matching. When a thread
is started by recalling and combining two or more threads, each carried symbol acquires that
parent's id.

### Symbol lifecycle and anchor projection

A thread's evolving identity lives in `history_symbols`; `anchors` is a deterministic re-derived
projection of it, with no model involved.

- `active` — not superseded: in the projection now, or never yet central (the zero value).
- `superseded` — once central, now ranked out. A label, not a candidacy gate: it competes on equal
  footing every turn. Retained, and protected from trimming while the protected set fits the cap, so an abandoned premise remains a findable handle.
- *trimmed* — the absence of the entry (capacity trimming above).

#### Projection

A pure function of current salience over all retained history symbols, active and
superseded alike. Rank by (1) class: a high-specificity identifier (URL, file path, git SHA) above any ordinary symbol,
whatever their counts; (2) `count` descending; (3) recency: `last_active_turn` then `first_seen_turn`, newest
first; (4) `normalized` ascending. The top `anchor.projection-max` become `anchors`, in rank order;
none gives an empty list. `ever_central` is not a ranking criterion; it only protects from trimming.

**Transitions** are labels read off the re-ranked top slice each turn, with no TTL. *Entry*: a
symbol in a slot this turn latches `ever_central`, sets `last_active_turn = turn` and is `active`
(also for a superseded symbol that ranks back in). *Exit*: a once-central symbol outside the slice
flips to `superseded`. A symbol is never trimmed while `ever_central` as long as the protected set
fits the cap.

**Ordering invariant** (per turn, before the thread registry/frontmatter write): merge → project →
trim, so a symbol entering the projection has `ever_central` latched before the trimming predicate
reads it.
`history_len` and `ever_central_count` staying flat over a run is a SPEC §9 sim obligation.

### Archive index (`archive/index.jsonl`)

[Authoritative] One JSON line per archived thread, sorted by `thr_id`; SPEC §3.8.1 states what it
must hold. The completed entry:

```jsonl
{"thr_id":"thr_42","commit_hash":"<H>","parent_commit_hash":"<capture commit; H's parent>","tree_hash":"<tree sha of threads/thr_42/ at the capture commit>","archived_at":"<RFC3339>","original_path":"threads/thr_42/","spine_summary":"<summary at archival time>","anchors":["..."],"project":"prj_3","recovered_at":""}
```

`spine_summary` and `anchors` are copied verbatim from the thread registry record at archival time.
`parent_commit_hash` is the capture commit whose tree still holds the thread bytes; a restore reads
from it directly rather than walking `commit_hash`'s first parent, which picks the wrong lineage on
a merge commit. An entry whose `commit_hash` is still empty is an **archive entry awaiting
back-fill**. `recovered_at` is empty until the thread is restored. Losing the index loses no data:
the thread bytes stay reachable from the capture commit's tree.

---

## The per-delta pipeline (§3.0)

The memory service takes content in as **deltas** (SPEC §3.0: each is one content addition to the
window); a turn groups deltas. Every delta runs the per-delta pipeline, a fixed sequence of steps,
so no content entering the window skips a step (**no gaps**).

**Delta taxonomy** (SPEC §3.0.1; the opencode mechanism behind each is in "Event observation"):

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `fs.read`, `fs.write`, `fs.commit`
- `thread.fetched` (**Open:** U3 — how a fetch is triggered under opencode)

Other-projects slot digest content and directive reloads are not deltas:
they change what the composer (`workset.Compose`) assembles.

**Every delta runs the pipeline in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions; routing by
   retention class.
2. **Engagement signal** (§3.2) — coalesced per turn in the turn buffer (multi-tool-call turns
   don't multiply `turn_count`).
3. **Dedup decision** (§3.9) — buffered; identifier lines and the tracked-file store update at
   turn close; replacement at composition.
4. **Budget check** (§3.1) — budget demotion so the budget is honored before the next delta lands.
   Demotion adds no delta: it emits a log line and ends.
5. **Logging** (SPEC §2.8; see "Event log format").

Delta order and turn attribution: see "Ordering and correlation".

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)`
entry point. **No delta reaches the working window without going through it.**

Step 4 (budget check) is a documented no-op in v0.1 (`internal/turn/chain.go`,
`TODO(phase-2-budget)`); `TestBudgetCheckIsExplicitNoOpV01` trips if it is half-enabled without a
deliberate decision. Step 5 summarizes every task-class source to a metadata line (source, path,
byte count) instead of logging the body, so a newly added task-class source inherits the
minimization.

### Transient-data lifecycle (load-bearing for recall fidelity)

Symbols from one-moment-of-value content (e.g., `# git status` output, search-result text) would
silently degrade Jaccard recall: they inflate the union, dilute meaningful symbols and drive
spurious matches. The lifecycle prevents this at the source:

1. **Provisional class at delta time** (mechanical, by source: SPEC §3.10.1). `task` → symbols
   extracted into a cross-turn **staging buffer** keyed by normalized form. `decision` → symbols
   extracted directly into the turn buffer (the existing engagement path).
2. **Confirmation via decision-citation.** When a decision-class delta extracts a symbol whose
   normalized form matches a staged entry, **promote**: the staged entry migrates from the staging
   buffer into the turn buffer (where source-dominance merge folds it with the citing delta's
   contribution) and is removed from the staging buffer.
3. **Expiry.** At the top of each turn (after `TurnNumber++`, before any pipeline step), staged
   entries staged before turn `currentTurn − K + 1` expire (`pruneStaging`: `cutoff :=
   TurnNumber − stagingWindowTurns + 1`, with `stagingWindowTurns` = K, default 3). Their symbols
   never reach the persistent symbol index. The window is a compiled-in constant; a staging entry
   holds the normalized form, raw form, source, and the turn it was staged. A later task-class
   delta carrying the same normalized form overwrites the entry and refreshes its staged turn,
   which restarts the window.

The principle: **raw task-class bytes are always discardable; only the symbols that crossed over
into a decision delta survive.** The persistent symbol index never sees transient symbols
because they are filtered at insert time, not removed retroactively. File content is kept
separately, in the per-thread tracked-file store, only as long as composition needs it (SPEC
§3.9.1); this lifecycle does not govern it.

---

## The composed request and its slots

Standard pattern: THESIS "The building blocks", row "Context composition".

SPEC §3.1 states the outcomes: the token ceiling, the slot table and percentages, the live-turn
reserve, the four slot policies (budget, internal priority, overflow form, unused-share rule), the
unused-share reallocation, the opencode-content slots and the tool-output cap. This section keeps
the mechanism.

- **Derivation.** `context.token-budget` is a const (`memops.DefaultTokenCeiling`) plus a
  constructor override. The post-flight prompt token count is the real check (opencode reports it in
  `session.step.ended`; see "Model access and inference telemetry"); the byte
  shares drive pre-flight truncation. A slot's byte share is the ceiling times a conservative
  ~2.5 bytes per token, counting text bytes only; the pre-flight check covers this composition plus
  the live-turn sub-policy. Of the §2.6.1 parameters only `closure.ack-mode` and
  `recall.ack-mode` are served by the directive files; the rest are compiled-in defaults.
- **Slot sources and rendering** (`internal/workset`):
  - *directives:* `directives/defaults.md`, `directives/user.md` and
    `directives/prj_<n>/project.md` of the active project, plus the project's `conventions_paths`;
    a section-divided concatenation with each directive file's YAML frontmatter stripped.
  - *registry:* `spine.jsonl` filtered to `project == active`, one display line per record (see
    "Display lines are how the model recognizes threads").
  - *other-projects:* `projects/<id>/digest.json` of every other project except `prj_default`, one
    line each, `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active`
    descending, each line capped at `cross-project.digest-per-project-bytes`.
  - *active-threads:* `threads/thr_<id>/` for each id in the active set, up to `layer.b-top-k`,
    most recently engaged first, each rendered as a heading, a frontmatter line and the body; a
    thread's share is the slot's share ÷ `layer.b-top-k` and an oversized thread is truncated on its
    own.
  - *dormant-threads:* the `spine.jsonl` records of the dormant set, one display line per record.
- **Truncation.** Each rendered slot truncates to its byte budget at a UTF-8 rune boundary and
  appends the honest truncation notice (SPEC §3.1 truncation policy). The dormant set is not count-capped: the dormant-threads
  slot's byte budget truncates it at composition time.
- **Demotion order under budget pressure:** the dormant-threads slot's oldest thread, then the
  active-threads slot's oldest (compressed to summary). The directives, registry and
  other-projects slots are never evicted from the request.
- **Tool output.** `boundTaskResultDeltas` (`internal/turn/livebudget.go`) applies the cap to the
  delta, so the capped content is what the request carries and what the working window holds.
- **File content.** `ComposeWorkingSet` loads each active-set thread's `files.jsonl` and runs one
  pure `dedup.Plan` across them. The plan gives each path that passes the size check a
  full-content candidate at its most recent position. It gives diffs at the path's N most recent
  changes, minus those not kept or at or above `dedup.diff-literal-threshold` of the current size.
  A path whose current size is at or above `dedup.literal-share-threshold` × the per-thread share
  gets neither. Threads are walked in active-set order and their excerpts newest-first. An excerpt
  is admitted only if its text and a size note per planned expansion fit; the newest is always
  admitted. Notes then become content while the share allows. A content id renders as full content
  once: the first taken wins. Only content that renders is read. `workset` renders it inside
  its excerpt, and `ComposeWorkingSet` returns a structured report of what rendered.
- **Scaffolding.** Static prompt scaffolding (about 1.7 KB: preamble, topic-tag directive, fallback
  reminders, headers) is charged to no slot share and rides on top of the byte proxy.
  `TestA4PreflightAssembledBytesWithinBudget` caps it (`Total + measured scaffolding`); a fixed
  deduction would zero out the tiny ceilings the acceptance tests exercise.
- **Prefix stability.** Fixed slots are composed first, so the request keeps a stable prefix across
  turns, which preserves the provider's prompt cache.

**Open:** per-slot policies and the `conventions_paths` overlap with the instructions slot (see
"Injection handling"; SPEC §2.5.1, §3.1).

The directives, registry and other-projects slots are what the model recognizes threads from; the
active-threads slot is active engagement (fetch flow: "No speculative prefetch").

### Active-set and dormant-set order

The active-set order and the dormant set are the memory service's `ActiveThreads` and
`DormantThreads` lists (most-recently-engaged first), persisted each turn to `working-set.json` in
the store directory (gitignored) so recency survives a relaunch. At turn close, after the turn's
engagements commit, each engaged thread id is removed from both lists if present and placed at the
front of `ActiveThreads`; if `ActiveThreads` then exceeds `layer.b-top-k`, its tail is demoted to
the front of `DormantThreads`. A second excerpt write for
one turn is refused with `ErrTurnAlreadyOwned`. The unknown-thread-id owner fallback ranks tagged
ids by `last_engaged_turn`, which is stored in the thread registry record but counted in the service run's turn counter;
it is deliberately not used for the no-tag owner-default, which ranks by active-set order because
the counter inverts recency across a relaunch. Active-set order cannot rank the fallback's
candidates: a tagged id may sit outside the active set, and a same-turn §5.5 fetch reorders the
list by fetch order.

---

## Key abstractions

| Concept | Definition | Spec |
|---|---|---|
| **Thread registry record** | one line of `spine.jsonl`; authoritative thread bookkeeping (id, project, anchors, summary, state, timestamps, turn_count, recall_fires) | "Storage and record formats" |
| **Thread** | the turns, not necessarily consecutive, that work on one specific problem (SPEC §1.1, §3.2); identified by `thr_<n>` (stable forever); a directory holding `thread.md` (YAML frontmatter), serial turn excerpts and a tracked-file store | "Storage and record formats" |
| **Project** | three identity levels: stable handle `prj_<n>`, mutable display name, optional remote identity (normalized git remote URL) | "Project metadata", SPEC §4.5 |
| **Anchor symbol** | 0..8 normalized symbols, projected from retained `history_symbols` (see "Symbol extraction") and stored in the record; drive recall and appear in the display line | "Symbol lifecycle and anchor projection" |
| **History symbol** | per-thread accumulated symbol (`raw`, `normalized`, `first_seen_turn`, `count`, `source`) plus a `lifecycle` (`active`/`superseded`) and an `ever_central` latch; the thread's evolving identity and the recall match set; capped per `history.cap-per-thread`, trimmed with ever-central + high-specificity protection | "Storage and record formats" |
| **Symbol category** | `identifier` (preserve case), `entity` (lowercase + hyphenate), `tag` (lowercase, leading `#` stripped) | "Symbol normalization" |
| **Symbol source** | `deterministic` / `model` / `user` / `curator`; dominance: `curator > user > model > deterministic` | "Symbol source" |
| **Delta** | one content-modifying event entering the per-delta pipeline (§3.0), with source + content + metadata + retention class | SPEC §3.0 |
| **Retention class** | `task` (default-transient: `tool.result`, `user.shell-capture`, `fs.*`) vs `decision` (default-persistent: `user.prompt`, `model.response`, ...). Drives the two-stage transient-data lifecycle. | SPEC §3.0.1, §3.10.1 |
| **Slot** | one named, budgeted region of a composed request (a memory slot or an opencode-content slot) with a budget, internal priority, overflow form and unused-share rule | SPEC §3.1 |
| **`MemoryOps` port** | the conceptual operations interface (`internal/memops`) the application layer depends on. One adapter (`fileadapter`, JSONL + markdown + go-git); callers depend only on the interface. | port-and-adapter |

---

## Mechanisms

### Thread lifecycle

Standard pattern: THESIS "The building blocks", row "Thread lifecycle".

A thread's life moves through five stages (SPEC §2.2.1 holds the states):

1. **Created** when the model emits `*new-topic*` in a topic tag, or by an explicit user act
   (**Open:** U4 — the command).
2. **Active**, in the active set, while engaged (the model tags it in the per-turn topic list).
3. **Dormant**, in the dormant set, once more recently engaged threads push it out of the active set (SPEC §3.1 LRU update rule).
4. **Closed** at a resting point: it leaves the active and dormant sets and remains in the registry
   slot as a display line, with full content preserved in `threads/thr_<n>/`.
5. **Archived**, in cold storage, only if thread registry size pressure builds — restorable by
   explicit fetch through git.

A thread is closed when the work reaches a resting point, not under "context window pressure."
Resting points are recognized by engagement decay (Spec §3.5). Fallback dissection is the one path that closes threads under budget pressure; it is not built (see "Fallback dissection").

### Display lines are how the model recognizes threads

> The display line must carry the decision state SPEC §2.2.2 requires.

The anchor set is what makes recall possible; the summary is what makes recognition cheap. The
display form is in "Storage and record formats".

### Symbol extraction (two passes ordered cheapest-first)

Standard pattern: THESIS "The building blocks", row "Symbol extraction".

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file
   paths, URLs, code symbols, project-configured patterns, user `#tags`). Free, reliable, narrow.
2. **Model-emitted** (every turn) — the topic tag's anchor list provides named-entity coverage the
   deterministic pass can't reach.

At close the summarizer drafts the closing summary (§3.5); it contributes no symbols, and anchors are not summarizer-picked (below).

#### Anchors and symbol history

`history_symbols` is the authoritative, accreting, weighted per-thread symbol history (capped at 40
by default) and the recall match set. `anchors` is the
deterministic top-`AnchorProjectionMax` (computed by `ProjectAnchors`) **projection** of the
thread's *retained* history symbols, re-derived each owner turn and stored in the thread registry record and frontmatter. `memops.AnchorProjectionMax` is a
compiled constant (8), not a config key: the SPEC §2.6.1 `anchor.projection-max` directive is not
read. A symbol that falls out of the projection flips to **superseded** but is **retained** — an abandoned premise stays a findable recall handle, protected from capacity trimming
by its `ever_central` latch while the protected set fits the cap. 0 anchors is legal (a vague-start thread that hasn't accreted a
headline yet). (Spec §3.4; "Symbol lifecycle and anchor projection".)

### Topic-tag mechanics

**Open:** U3 — the in-band topic tag under opencode, including stripping it from the stored
transcript (see "No speculative prefetch"). Until decided, the mechanics below bind.

The tag prompt is a compiled constant in `internal/prompt/template.go` (`TopicTagDirective`); the
two fallback reminders (`TopicTagReminder`, `EmptyResponseReminder`) live in the same file, and
changing any of them is a rebuild. The owner of a multi-existing tag is the lowest `thr_N` because
the turn buffer is a map and does not preserve tag order. The bare `*new-topic* [...]` alias is
accepted by the length-limited preamble scan (so the §5.5 fetch timing, the missing-tag trigger and the
stream-filter suppression agree) and by the stream filter's Close-time trailing-line fallback; an
all-whitespace response longer than that scan resolves as tag-less rather than empty, so it fires
the missing-tag reminder. `fs.read` deltas buffer a write entry like `fs.write`, so a tag-less
read-only turn pays the full missing-tag fallback.

The parser (`internal/prompt/parser.go`) recognizes a tag with this RE2 pattern, compiled with
`(?m)`:

```
^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$
```

Group 1 is the thread list: comma-split, whitespace-trimmed, each entry validated against
`thr_\d+|\*new-topic\*`, the unclosed `*new-topic` accepted and normalized to `*new-topic*` (SPEC §5.1.2). Group 2 is the anchor list: comma-split, whitespace-trimmed, then run
through the SPEC §2.7.2 normalization. The bare alias is the line-anchored pattern
`^\s*\*new-topic\*\s*\[([^\]]*)\]\s*$`; a bare `*new-topic*` with no bracket pair stays invalid and
is classified `bare-new-topic` (see "Event log format").

**Mid-turn fetch (SPEC §5.5).** When the tag names a `thr_<n>` outside the active set, the service
parses the tag before it consumes any response body and aborts the in-flight stream. For each such
thread it reads `threads/thr_<n>/` (frontmatter from `thread.md`; body assembled from the
recent-excerpt window and, when needed, from `folio.jsonl`), truncates it to its active-threads
allowance, fires a `thread.fetched` delta (SPEC §3.0.1) and loads the thread at the front of the
active set, where overflow past `layer.b-top-k` demotes the tail to the dormant set. It then
re-issues the request with the recomposed system prompt, and the second stream's body is the
user-visible response. The re-prompt is capped at one per turn; a thread of another project is
declined at the fetch seam (`thread.fetch-cross-project`).

### Recall paths (four, ordered by cost)

Standard pattern: THESIS "The building blocks", row "Recall".

1. **Model-native recognition** (default; cheapest). The registry slot is always in the composed
   request; the model recognizes prior threads directly when a turn engages them. It emits the
   topic tag at turn start. Handles synonyms and paraphrases for free, since the model does the
   recognizing.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols match, above threshold, the anchors and history symbols of a thread of the active project that this turn did not engage — or, with an embedder configured, the turn's input is close in embedding similarity to a thread's indexed body — the thread is brought forward by band (SPEC §3.4). The embedding scan (`measure.Service`, `coarseFine`) selects the top threads by body vector and scores each by its best chunk; it is not limited to the active project, and another project's candidate is declined at the fetch (SPEC §3.2).
   The symbolic scorer matches on `T(thr) = anchors ∪ {h.normalized}` over the thread's
   `history_symbols`, so a `superseded` symbol stays matchable. The Jaccard numerator is a weighted
   sum: a matched symbol counts 1.0 if `active` in that thread and `recall.superseded-weight` if
   `superseded`, and a symbol that is also a projected anchor counts as active. The denominator
   (union size) is unchanged, and at weight 1.0 the score is byte-identical to the plain-count
   scorer. The label is read from the thread's frontmatter `lifecycle`, not from the derived index:
   `symbols.jsonl` `superseded_in` is the cheap-scan filter ("which threads abandoned premise X",
   O(1) per query symbol) and the scorer does not consume it.
3. **Cross-project recall.** Not built: a cross-project fetch and cross-project engagement are declined (SPEC §3.1, §3.2; ROADMAP "Cross-project recall").
4. **Intra-thread recall of scrolled-out early content.** For the engaged long-running thread, the
   fine-level chunk embedding index recalls early turns that have scrolled out of the
   recent-excerpt window. The engaged thread bypasses the coarse level (one vector per thread body) and runs directly against
   its own indexed chunks. Every thread is an indexed body: one coarse vector for the thread body
   plus per-excerpt chunk vectors. An active thread's recent-excerpt window is in the working set
   and is not embedded; excerpts scrolling out of it become index material. On threads long enough to have a built summary tree, an O(log n) beam
   descent (`scoring.DescendChunks`) replaces the flat O(C_main) scan (`C_main` is the engaged thread's chunk count) — consolidation builds and
   maintains the per-thread summary trees (`measure.RebuildTrees`); absent or stale trees fall back
   to the flat scan with no correctness loss. The summary tree serves SPEC §3.4 *Long threads* (no performance decay as a thread's length grows into the thousands): a flat scan's per-query cost grows with the thread's chunk count (`C_main`). The descent is approximate; how often it picks a different leaf than the flat scan is measured (`recall_intra_descent_divergence`, SPEC §9.4), and exact recall stays with the lexical level (SPEC §3.4). Summary trees are built only by consolidation (see "Memory consolidation"). The fine level is filled asynchronously, so it lags
   scroll-out by up to the unembedded backlog limit; the recall-completeness invariant (SPEC §3.4)
   forbids a gap in findability there, so the automatic intra path also runs the completeness floor,
   a lexical pass over exactly that backlog (`exact.MatchExcerptsBySymbols` over
   `MemoryOps.LoadDebtWindowExcerpts`, (1 + pending) × the limit (below), query-symbol match), and unions the hits.

   The index is maintained by a single background indexer that swaps an `atomic.Pointer` index
   snapshot, so recall reads never block; an embed sequence guard (the per-thread turn count each
   flush carries) stops a stale in-flight embed from overwriting a fresher vector, and a flush
   arriving at an *equal* count (the same-turn limit-flush plus dormancy-flush double enqueue)
   always publishes. Two triggers enqueue a flush: the unembedded backlog limit (16 turns of
   accumulated scrolled-out content, a provisional value calibrated in SPEC §9) and the dormancy
   transition, which flushes a thread's remaining backlog when it decays out of the working set so
   its whole body is indexed before it becomes a pure recall target. The completeness floor scans `(1 + pending) × limit` excerpts: `limit` is the
   unembedded backlog limit (16) and `pending` counts enqueued-but-unpublished flush jobs — 0 at
   steady state and at most 258 at the v0.1 queue depth of 256 (queue plus one in process plus one
   blocked enqueue). Only a genuinely failed flush drops a pending job without publishing.
   Persisted vectors and summary trees are `.vec`/`.tree` sidecars under `.recall-cache/` in the
   store directory (`internal/recall/measure/veccache.go`, `treecache.go`). Each is keyed by a
   content-staleness marker (a body or chunk hash), written through by the indexer and loaded at
   startup; a miss is rebuilt, so a service run re-embeds only what changed, O(changed) rather than
   O(all).

   In-context exclusion: when an intra-thread candidate is accepted, the service records its
   turn-excerpts as in-window for the engaged thread (`recallWindowTurns` in `internal/turn`), and
   the recall stack drops them from both the intra-thread scan and the lexical completeness floor.
   The mark ends when the thread is demoted out of the active set. It is held in memory only:
   `working-set.json` restores the active set after a restart, but the composed request then no
   longer holds the accepted excerpts, and the marks and the window are rebuilt together.

**Decline categorization** is an input to feedback directive tuning (cross-project aggregate only;
see "Personalization through feedback directive tuning") so that a bad match does not suppress good
offers. SPEC §4.3 holds the reason enum and its effects; how a reason is captured waits on U4.

#### Guarantee boundary — findability, not reminiscence

The recall stack guarantees that durable
content is *findable* (the §3.4 completeness invariant) and that overlapping context *surfaces
opportunistically* (the recall paths above). A connection between today's problem and years-old
work in a different vocabulary surfaces only if anchors or embeddings overlap (SPEC §3.4).

### Closing a thread

Triggers:

- Engagement decay: no engagement for `engagement.decay-turns` OR `engagement.decay-time`
  wall-clock (either threshold; OR semantics). The turn count is `TurnNumber − last_engaged_turn`, both in the service run's turn counter, which starts again at 0 on every relaunch (see "Start order and resume", U9). The stored `last_engaged_turn` therefore means nothing to a later run. A stored value above the current counter skips the turn threshold; a lower one is compared as if it came from the same run (`decayEligible`, `internal/turn/closure.go`). Across a relaunch, only the wall-clock threshold is a durable decay timer.
- An explicit close request (`/done`; **Open:** U4 — the command).

Flow (SPEC §3.5): the summarizer drafts the closing summary; a closing writes the
thread file and updates the thread registry, and demotion out of the active and dormant sets follows.

Cold storage archival (`internal/memops/fileadapter/fileadapter_archive.go`;
`cmd/archive.go` `archive list`/`archive recover`): when thread registry size pressure builds, the
memory service archives the oldest closed threads into cold storage, out of the thread registry, by
removing their directories, committing the removal and recording an `archive/index.jsonl` entry. (Spec §3.8.)

A batch meets the three results of SPEC §3.8.1 as follows:

- *Membership recorded before deletion.* One entry per thread (see "Archive index") is durably
  appended to `archive/index.jsonl` with an empty `commit_hash` but full `parent_commit_hash`,
  `tree_hash`, `original_path` and thread registry snapshot, before any thread directory is
  removed. That entry set is the worklist a crash completion consumes. Each `threads/thr_<n>/` is
  confirmed tracked at HEAD before its tree hash is taken.
- *Bytes reachable after.* The capture commit is `Add(".")` of the full store working tree into the
  history repository and precedes the membership append. It lands the day's cumulative content,
  which makes removing the turn repository at rollover safe, and becomes the parent of the deletion
  commit whatever the turn repository's commit cadence. Each directory's git tree hash is captured
  there, and a restore verifies against it. Removals are staged by `os.RemoveAll` plus
  `autogit.Add(".")`, and `RemoveSpineRecords` performs the single thread registry rewrite, with
  derived state regenerated once for the batch. The capture commit runs `CheckSpineIntegrity`
  before; the deletion commit runs `CheckDerivedFresh` and `CheckSpineIntegrity` after.
- *Bounded commits.* A final back-fill commit records the deletion commit's hash as `commit_hash`
  in the entries awaiting back-fill, so a batch adds a bounded number of commits.

Restore (`RecoverThread`, `autogit.CheckoutTree`):

1. Look up the entry by `thr_id`; refuse with `ErrArchiveEntryNotFound` if absent, and refuse an
   entry whose `commit_hash` is empty.
2. Restore the full `threads/thr_<n>/` subtree from the entry's `parent_commit_hash` commit; an
   entry lacking it (written before the field existed) is restored from the deletion commit's first parent (`autogit.ParentCommitHash`), the capture commit, since Personant makes no merge commits.
3. Recompute the restored directory's tree hash and verify it against `tree_hash`. A mismatch
   deletes the partial restore and fails with `ErrArchiveIntegrity`, so a partial thread is never
   written onto the thread registry (SPEC §3.8.3).
4. Add a thread registry record reusing the same `thr_<n>` id (the registry stays sorted by id),
   with `state = wip` and `last_engaged` updated; anchors and summary come from the restored
   frontmatter, not the archive snapshot.
5. Set `recovered_at` on the entry and commit the restored files, the registry and the archive
   index to the turn repository (`autogit.Daily`, message `archive: recover …`), which folds them
   into the day at the next rollover.

Archived threads are off live recall because the §3.4 scan covers the thread registry and
per-thread frontmatter, and an archived thread has neither. `RecoverThread` and `ArchiveThreads`
are public port operations; no product path calls `ArchiveThreads` outside the rollover (tests do), and such a call is crash-recoverable through the same
roll-forward (case 8).

### Fallback dissection (worst-case path)

Not built: no package implements the clusterer. SPEC §3.6 states the outcome; the work is ROADMAP B11.

### Many projects, one career

One thread registry across all projects. Every thread carries a `project` tag. The registry slot
holds the current project's thread registry as display lines, truncated to its budget (SPEC §3.1); the other-projects slot holds
compressed cross-project digests. Cross-project recall is not built (recall path 3); cross-project engagement is declined (SPEC §3.2).
(See "Project metadata"; Spec §4.5.)

### Personalization through feedback directive tuning

Standard pattern: THESIS "The building blocks", row "Directives".

Directives are inspectable files, and feedback signals tune them:

- `directives/defaults.md` — system defaults, baked in.
- `directives/user.md` — user-wide overrides. By default, feedback directive tuning proposes a
  change here only from patterns aggregated across projects (explicit instructions, decline
  categorizations and accept/decline statistics are inputs to the aggregate, not single-project
  writers; SPEC §1.8).
- `directives/prj_<n>/project.md` — project-specific directives.

Process-memory directive files do not exist yet (SPEC §1.8
`Open:`).

Every feedback-tuning proposal goes
through "Memory vetting", the same review and acceptance path as any other. (Spec §1.8, §2.6.)

**Open:** how cross-project aggregation is computed (what counts as a pattern, how many projects,
what threshold); no aggregator exists, and the per-project statistics the recall loop collects
(`directive.accrual-update`, reserved) feed none of these files yet.

### Career memory

Standard pattern: THESIS "The building blocks", row "Career memory".

No career-memory mechanism is built beyond threads and recall. No store holds technique notes. Nothing files a lesson as career memory: SPEC §1.8 routes a pattern seen in one project there, but feedback directive tuning has no aggregator. Cross-project recall, the path by which a past project's threads would surface, is not built (recall path 3).

### Memory vetting

Standard pattern: THESIS "The building blocks", row "Memory vetting".

Not built; outcomes and open items: SPEC §1.8.

Mechanism: behavior- and process-memory writes pass through the `MemoryOps` port, so the review stage sits at
one port operation. The port has no directive-write operation today (`DirectiveParam` only reads).
Threads are protected (SPEC §1.8) by the slots and the turn and history repositories; consolidation's content review is not built (see "Memory consolidation",
"The composed request and its slots", "Crash recovery and the turn and history repositories").

**Open:** which package holds the review stage and its provenance record.

### Crash recovery and the turn and history repositories

Standard pattern: THESIS "The building blocks", row "Durability and crash recovery".

The memory service can be hard-killed (SIGKILL, power loss) at any instant, so recovery is designed
as a **deterministic cold-start reconciliation** — no signal handler doing teardown work, no model
in the loop, no user acknowledgement. On-disk state alone determines the outcome. The load-bearing
property: **the store working tree is the truth; git is a recovery-point index over it.** No
authoritative byte of the store working tree lives only in the turn repository, so the turn repository can be recreated from the store working tree and losing it is never losing content. Archived threads live only in the history repository (cold storage, SPEC §3.8), so a missing history repository is refuse-to-open. (Spec §4.5.8.)

Two git repositories track the one store working tree: the turn repository, per-turn and
disposable, and the history repository, day-grain and permanent (cadence, cost bar and ≤1-turn
durability: SPEC §3.11). The in-flight turn's raw `(prompt, response)` bytes are protected by a
durable append journal written *before* any authoritative write, so even the one at-risk turn's
content survives.

Recovery reduces to a small state machine over on-disk observables (operation marker, in-flight
journal, turn and history repository HEAD trailers, watermark, turn-repository presence) that
classifies into one case and repairs idempotently — a torn turn resets **only the turn repository**
to ≤1-turn-ago; a day-rollover or archival crash **rolls forward** (never resets the store working
tree, which would destroy up to a day); a broken hand-edit is **quarantined byte-exact, never
deleted**. Ruled out permanently: loss-bar extension (commit batching / ≤N-turn windows — ≤1 turn
is the bar), turn-repository durability guarantees (disposable by construction), and
history-repository auto-recreation (a missing history repository is refuse-to-open, not a benign
recreate).

Under opencode the bar is judged against turns the user saw complete (SPEC §4.5.8 (10)): a turn
opencode recorded as terminal that memory rolled back is lost until reconcile re-ingests it (see
"Start order and resume").

#### Observables

Recovery reads the following on-disk signals and classifies them into exactly one case, numbered
1–12 under "Recovery cases". No signal is trusted transitively; each is read against the correct
repository.

- **Journal as turn signal.** A non-empty `turn-journal.jsonl` is the in-flight-turn signal; there
  is no separate marker file. Its first record carries the turn id (`store.JournalOwner`), and the
  first append's fsync makes the signal durable. An `op=turn` marker file left by an older binary
  is still read and reconciled.
- **Batch marker.** `op-in-progress.json` (store directory, gitignored, survives `reset --hard`)
  covers batch operations only: `{op: archival|sleep|recovery|barrier|rebaseline, day?, orig?}` (`rebaseline`: the mid-day turn-repository re-baseline, see "Mechanism names"),
  where `sleep` is consolidation and `barrier` is the day rollover. It is written before a batch
  operation's authoritative mutations and cleared as the last step. Its presence is the
  batch-in-flight signal, and `op=barrier` carries the target `day`.
- **Watermark.** `derived-watermark` (gitignored) holds the turn repository HEAD hash the derived
  files were last built from. Turn repository HEAD equal to the watermark, with the store working
  tree clean against that HEAD, means derived files are fresh: the O(1) clean-open certificate.
- **Trailers.** Every turn-repository commit carries `Personant-Turn: <id>`, read as the head turn
  id; every history-repository commit carries `Personant-Day: <N>`, read as the head day number.
  The head turn id discriminates the torn-turn cases (4, 5, 6); the head day number drives
  new-day detection and the day-commit guard. The head day number is undefined only for a bare,
  never-committed history repository.
- **Turn repository state** — present, missing or half-created — is evaluated defensively and
  first, before any turn-repository read. A failure to open a corrupt or partial `.git-daily`
  classifies as half-created and is never a propagated error, so a corrupt turn repository is
  recreated from the store working tree and never wedges an open.

#### Turn transaction and journal

A turn is written to the turn repository in three ordered steps:

1. Append the prompt record to the journal (`JournalTurn`, kind `prompt`, fsync) before the model
   call. The first append arms the in-flight signal.
2. Append the response record (kind `response`, fsync) before any authoritative write. A
   journal-append failure aborts the turn before any authoritative write.
3. At turn close, `CommitTurn` commits the turn's scoped write set to the turn repository with the
   `Personant-Turn: <id>` trailer, then truncates the journal; the truncate is the scope release.
   A commit failure leaves the journal non-empty and fails loudly into recovery.

Staging is scoped to the turn's recorded write set (`spine.jsonl`, touched thread directories, the
day's event log, recorded at each write site and never guessed) through `autogit.AddPaths` with no
integrity flags, so it costs O(write set) with no tree walk and turn-repository commits are not
validated. Creating or closing a thread rides the per-turn
commit and folds into the day at rollover; creating a project takes a checkpoint (SPEC §3.11). A hand-edit is therefore absorbed at the next full
staging pass: a checkpoint, or the archival and day-commit steps of the day rollover.
The §5.5 re-prompt caps live entirely inside a turn, ahead of step 3.

The journal is `turn-journal.jsonl` in the store directory (flat, operational, gitignored): one
record per line, `{turn, kind: prompt|response, at, bytes}`, fsync per append. The truncate to
empty at turn commit carries no fsync (`store.TruncateJournal`). A power-loss resurrection of a
stale-but-truncated journal is the redundant-journal case, case 5 (`turn == head turn id`)
(`TestCell5_ResurrectedTruncatedJournal`; Cell N is case N), and the next turn's first append fsync makes the
truncate durable before any new authoritative dirt can exist. A torn append is bounded to that one
turn's content, which is the ≤1-turn bar. A torn final line is skipped by the scan, never parsed.
On a torn turn the journal bytes are written to a recovery artifact and surfaced to the user
(**Open:** U4 — the channel). They are never auto-replayed into the authoritative files, because a
turn's derivation (tag parse, symbol extraction, engagement) is not replayable.

#### Recovery cases

Turn cases 4, 5 and 6 evaluate against the turn repository; case 12 against the history
repository. An archive entry awaiting back-fill is an entry of the "Archive index" whose
`commit_hash` is still empty.

| Case | Trigger | Action |
|---|---|---|
| 1 clean | no marker, turn repository state present, store working tree clean against turn repository HEAD, turn repository HEAD == watermark, journal empty | no-op — O(1) open |
| 2 derived-stale | no marker, clean, derived stale | rebuild derived; set watermark = turn repository HEAD |
| 3 hand-edit | markerless dirty against turn repository HEAD, watermark present | never reset — a legitimate hand-edit, absorbed at the next full staging pass |
| 4 torn turn | journal non-empty, dirty, `head turn id ≠ turn` | preserve and surface the journal → quarantine dirty tracked bytes byte-exact → reset the store working tree to the turn repository's HEAD (≤1 turn) → move untracked debris in the authoritative-file namespace (excluding `logs/` and `recovery/`) into the same quarantine → re-append log bytes → truncate the journal |
| 5 turn committed | journal non-empty, clean, `head turn id == turn` | redundant journal (turn already committed) — truncate, zero loss |
| 6 turn no-writes | journal non-empty, clean, `head turn id ≠ turn` | nothing landed structurally — preserve and surface the journal, truncate |
| 7 (composite) | case 4 coinciding with an archive entry awaiting back-fill | case 4, then the case 9 back-fill pass |
| 8 archival | `op=archival` marker | detect only → reported as a pending completion; the roll-forward completes the batch (no reset, no re-archive as drift); marker left in place until completion |
| 9 back-fill repair | archive entry awaiting back-fill (any case, post-normalization) | locate the deletion commit (the child of the entry's `parent_commit_hash`, by tree comparison) and back-fill its hash; unlocatable ⇒ `recovery.unrepairable`, the entry stays refused, never guessed |
| 10 consolidation | `op=sleep` marker | clear the marker; the memory service self-heals (gc and rebuild are idempotent) |
| 11 recovery re-entry | `op=recovery` (crash during recovery) | re-run under the original marker; every phase is idempotent, so it converges |
| 12 legacy adopt | dirty, no watermark ever | greenfield or legacy: verify-gated adopt-commit to the history repository (`Personant-Day` trailer), back-fill repair, full rebuild, then turn-repository init (see "Day rollover") |

Dual-repository operation rows:

| Op | Action |
|---|---|
| `op=barrier` | detect only → reported as a pending completion; the memory service runs the idempotent day-rollover completion. The turn repository state observable is irrelevant to classification. |
| `op=rebaseline` | `rm -rf .git-daily` plus turn-repository init, unconditionally; no history-repository touch |
| turn-repository init rows (no marker, turn repository missing or half-created) | greenfield (no watermark) → adopt if the history repository is dirty, then turn-repository init and rebuild; benign morning (a service-run open that finds the turn repository removed by the last day rollover: the normal first open of a day; turn repository missing, watermark present) → turn-repository init, rebuild iff derived is stale; interrupted init or corrupt directory → `rm -rf`, then turn-repository init. A missing turn repository is normal, never a data-loss signature. |

An orthogonal heal runs on every path, before classification: an additive event-log tail-heal (last
byte ≠ `\n` ⇒ append one `\n`, never truncate).

#### Repository invariants

- **No-reset rule:** archival and the day rollover never reset the store working tree (their
  recovery is re-drive or repair); only turn recovery resets, and only ever to the turn
  repository's HEAD (≤1 turn).
- **Day-commit guard:** the day-commit is guarded by history repository HEAD being a
  day-commit-shape commit (one the day-commit step made, as opposed to a back-fill or adopt commit
  that carries the same trailer) with `Personant-Day == N`, not merely a trailered commit, so a
  re-drive after a crash never mints a second day-commit.
- **Capture-before-removal rule:** `.git-daily` is deleted only after the store working tree is
  captured to the history repository.
- **Watermark rule:** the watermark tracks the turn repository's HEAD, re-set by derived rebuild and
  by turn-repository init (the re-init at day rollover, or the init of a missing repository).
- **History-writer rule:** the history repository is written by the day rollover (its archival and
  day-commit steps, and the lifecycle tags it derives (see "Lifecycle git tags")) and by three recovery paths — case 9
  back-fill repair, case 12 adopt and archival roll-forward completion — and by nothing else; the
  checkpoint and the archive restore target the turn repository. Every such writer, day-commit and
  archival alike, validates thread registry integrity before minting, so history repository HEAD
  always passes it.
- **Ignore-block rule:** both `.git/` and `.git-daily/` are in the managed `.gitignore` block, so neither repository
  sees the other's git directory as untracked debris.

#### Day rollover

The rollover fires on new-day detection — the day index exceeds the head day number — checked at
service-run open, after startup recovery, and before each turn. It runs under an
`op=barrier {day: N}` marker. Its steps, in order:

1. **Rollover marker step:** write the `op=barrier` marker. It owns the whole sequence, including
   the archival step, which opens no nested `op=archival` scope.
2. **Archival step:** archival-if-pressure against the history repository via the batch archive
   (see "Closing a thread"; no nested marker). Batch membership is recorded durably before any
   deletion, and its capture commit stages the full store working tree into the history repository,
   which makes the turn-repository removal step safe (capture-before-removal rule). The step is
   skipped while the registry is not under size pressure, and the day-commit step is then the sole
   store-working-tree capture.
3. **Excerpt-storage step:** each live thread's turn excerpts move between individual files and its
   excerpt bundle to match the placement rule (see "Thread directory"). Store working tree only,
   byte-preserving and idempotent: every excerpt stays readable at every instant, and the moves
   land in the day-commit step's commit. Tiers change at no other time.
4. **File-retention step:** each live thread's tracked-file store drops entries whose latest read
   or write has left the recent-excerpt window, every file no kept entry names, and a legacy
   `files.json`. Store working tree only and idempotent; the deletions land in the day-commit
   step's commit.
5. **Day-commit step:** commit the store working tree to the history repository with
   `Personant-Day: N`, staging everything including overnight hand-edits. The staged tree is
   validated for thread registry integrity before the commit is minted (history-writer rule). The
   step is guarded by the day-commit guard, and an empty day still produces exactly one trailered
   day-commit, so the head day number is always defined.
6. **Derived-rebuild step:** rebuild derived files from the now-committed store working tree,
   re-setting the watermark against the pre-removal turn repository HEAD.
7. **Turn-repository removal step:** `rm -rf .git-daily` (idempotent).
8. **Turn-repository re-init step:** create the turn repository with a baseline commit of the
   current store working tree, giving the baseline hash H_d.
9. **Freshness-assert step:** set the watermark to H_d (watermark rule), assert that derived files
   are fresh, and clear the marker.

Only the archival and day-commit steps mutate the history repository; the turn-repository removal
and re-init steps are pure turn-repository lifecycle; the derived-rebuild and freshness-assert
steps touch derived files and the watermark. The authoritative content of the store working tree is
untouched from the day-commit step through the freshness-assert step. It is only reduced by the
archival step's removals, whose bytes are preserved in the history repository, and by the
file-retention step, which deletes on purpose, including file versions written and superseded since
the last day-commit, whose bytes never reach the history repository. The excerpt-storage step only
rearranges it, without loss.

**Turn-repository init** (`git init .git-daily`, a baseline commit of the store working tree, and
the watermark set to the baseline hash) is exactly the turn-repository re-init and freshness-assert
steps in isolation. It costs O(store working tree bytes), meaning the live, non-archived threads,
independent of career-history length.

**Lifecycle git tags.** After the day-commit step lands, inside the `op=barrier` scope and before
the marker is cleared, the rollover derives git tags on the history repository from the sealed
day's event log: `thread.created` → `thread/<id>/created`, `retire.complete` →
`thread/<id>/retired` and `project.created` → `project/<id>/created`, all targeting that day's
day-commit, and `archive.archived` → `thread/<id>/archived`, targeting the archival commit recorded
in the thread's archive-index entry. Project open, close and switch are not mapped. The ref name is
`<kind>/<id>/<event>_<stamp>`, e.g. `thread/thr_42/created_2026-05-08T03-12-00Z`; `<stamp>` is the
event's UTC-normalized RFC3339 instant with time colons rewritten to hyphens (git refs forbid `:`),
so names are unique and sort lexically as chronologically. Derivation is idempotent (skip if the
exact name exists) and best-effort: a read, parse or tag fault logs `barrier.tag-error` and is
skipped, never failing the seal. Day-scoping: the rollover for day N fires the next day, so
`created`, `retired` and project lines are those with event day index N, while `archived` (emitted
by the rollover itself) is not day-filtered; life events on earlier idle gap-days, completed days
before N on which no rollover ran, go untagged.

**Roll-forward completion** (a crash inside the rollover). Every crash window recovers by
roll-forward, never a reset. The `op=barrier` marker, with its `day: N`, is the idempotent re-entry
token, and every sub-action is guarded and idempotent, so re-entry converges. The archival step's
completion re-derives the durable batch-membership worklist: ensure-removed →
ensure-registry-removed → regen → the deletion commit (only if none has landed, else back-fill the
located one) → back-fill. A deletion commit that already landed is back-filled, not duplicated
(where roll-forward meets case 9), and completion must not infer state from
store-working-tree-clean-versus-HEAD, because an overnight hand-edit would be misread as
uncommitted removals. Completion also re-runs the archival step's pressure drain, so a rollover
killed before archival does not defer the day's archival, and re-runs the excerpt-storage and
file-retention steps while the day-commit has not landed, finishing any moves or deletions the
crash interrupted.

**Day-commit-shape guard.** New-day detection reads the head day number off any trailered
history-repository commit; the guard's idempotence check fires only on a day-commit-shape HEAD for
the target day. A trailered but non-day-shape HEAD (a roll-forward back-fill, an adopt) advances
detection but does not satisfy the guard, so the real day-commit is still minted. Recovery is
fork-free across a completion on the following day (N+1) and across adopt-then-rollover kill
timings. Only completed days, strictly before the current clock day, are sealed, each exactly once
(gap-day at-most-once).

**Quarantine-and-proceed.** If the day-commit step's thread registry integrity check or the
freshness-assert step's derived assert fails and still fails across one idempotent re-drive:
(1) identify the offending paths; (2) quarantine their bytes byte-exact under
`recovery/quarantine/<stamp>/`; (3) scoped-restore exactly those paths from history repository
HEAD — path-scoped, never `reset --hard` and never a whole-store-working-tree restore; derived
paths, absent from the history repository, are regenerated and verified instead, and an offending
path absent from HEAD is quarantined and removed; (4) re-run the failing step once; (5) if it still
fails, refuse to open with the marker left in place as the retry token, and name the quarantine
directory in the report. This is safe because the restore is scoped and quarantined first (no-reset
rule) and history repository HEAD always passes thread registry integrity validation
(history-writer rule).

**Archival crash completion.** Archival fires only at the rollover's archival step, so apart from
the recovery paths the history-writer rule names, the history repository is written only at
rollover. Completion is deterministic because membership is recorded before deletion (archive-index
entries are appended with `parent_commit_hash`, `tree_hash`, `original_path` and the thread
registry snapshot, and an empty `commit_hash`, before any thread directory is removed, leaving a
durable worklist) and because an already-landed deletion commit is resolved as the child of the
entry's `parent_commit_hash` by tree comparison, which is robust to overnight hand-edit dirt, so
the roll-forward back-fills the existing commit instead of minting a duplicate
(`recovery.LocateDeletionCommit`, see "Mechanism names"). Outside a rollover only tests call `ArchiveThreads`; such a
batch runs under its own `op=archival` marker, and case 8 is likewise
detect-then-complete, reporting it pending on marker presence alone while the memory service runs
the same roll-forward tail. Turn and structural commits land in the turn repository and fold into
the day at the rollover, so archival, which writes the history repository, never races a turn.

**Intra-day derived staleness.** The turn path never sets the watermark; the day rollover owns
freshness (derived-rebuild and freshness-assert steps). Between rollovers, derived files track the
turn repository's per-turn state via the watermark, and a markerless hand-edit stays deferred
(case 3) until the next full staging pass.

**Greenfield and legacy.** A greenfield store (no watermark, no turn repository) takes the case 12
verify-gated adopt-commit to the history repository, with `Personant-Day: <current day>` as the
sole bootstrap carve-out, then back-fill repair, a full rebuild and a
fresh turn-repository baseline. An `op=turn` marker file left by an older binary is honored and
cleared. A missing turn repository on an existing store directory is the legacy-upgrade shape and
stays absent through init (`store.Init` creates `.git-daily`, the turn repository, only alongside
a fresh history repository), so case 12's adopt gate and the benign-morning row (turn-repository init with
no adopt commit) discriminate correctly. Legacy archive entries awaiting back-fill take the general
adopt-forward repair path, with no special case.

#### Mechanism names

Core recovery (`internal/recovery`) only detects
day-rollover and archival cases and returns a typed `RecoveryReport.Pending`
(`PendingCompletion{Kind, Day}`, plus `ScopedRestored` for quarantined-and-restored paths);
`fileadapter.Reconcile`, which owns `ArchiveThreads` and both repository handles, completes them,
which keeps `recovery` from importing the adapter. `recovery.LocateDeletionCommit`
(`internal/recovery/stamp.go`) finds an already-landed deletion commit by a
child-of-`ParentCommitHash` tree test. The day rollover mints its day-commit with
`AllowEmptyCommits` after `CheckSpineIntegrity` on the staged tree, asserts `CheckDerivedFresh` in
the freshness-assert step, and derives lifecycle tags through `autogit.TagStamp` / `ParseTagStamp`
(the only formatters), reading the sealed day's log with the tolerant `eventlog` reader and
tolerating `ErrTagExists`. The excerpt-storage step (`settleExcerptTiers`, between the archival
step's batch and the day-commit step) calls `store.SettleExcerptTiers` per thread registry record in
ascending id order with `memops.IsRetiredState` (a closed thread) as the signal to bundle every
excerpt; completion re-runs it after the archival batch while the day-commit has not landed. Per
thread it writes each excerpt that belongs in the loose tier as a loose file and fsyncs `turns/` and
the thread directory, then writes the excerpt bundle (`WriteFileAtomic`, which does not fsync the
parent) and fsyncs the thread directory, reads it back, and only then removes the loose files now
bundled — so every excerpt is readable from at least one tier at every crash point. Convergence: a
name in both tiers with identical bytes is an unfinished move and settle keeps the target-tier copy;
different bytes are a hand edit, and neither copy moves (`folio-skip reason=conflict`). A torn
excerpt-bundle temp file (`tmp-folio.jsonl`) is quarantined by recovery's unconditional `tmp-`
debris cleanup. A hand-broken excerpt bundle is skipped by settle (`reason=unreadable`) and fails
the day-commit step's integrity check on the live run, operation marker kept; the next open's
completion runs `spineQuarantineRepair`, quarantining the bundle byte-exact and scoped-restoring it
from history repository HEAD (or removing it as a path absent from history repository HEAD), which is consistent with the
thread's loose files because tiers change only at a day rollover. The file-retention step
(`pruneTrackedFiles`, between the excerpt-storage step and the day-commit step) calls
`store.PruneTrackedFiles` per thread registry record, in registry order, with the record's
`TurnCount`. It rewrites a thread's `files.jsonl` only if an entry drops, then deletes the files no
kept entry names, so a crash leaves orphans, never a dangling index. Completion re-runs it under the
same condition as the excerpt-storage step. An unreadable index is skipped
(`dedup.prune-skip reason=unreadable`) and fails the day-commit step's integrity check. Any
tracked-file finding makes completion's `spineQuarantineRepair` quarantine the thread's
`files.jsonl` and `files/` together and restore both from history repository HEAD. Crashpoints: the
four `folio.settle.*` and `barrier.prune.postIndex` in `store`, and
`barrier.postSettle.preDayCommit` and `barrier.postPrune.preDayCommit`, all walked by
`TestBarrier_CrashWalk`; and `fs.applyEvents.preIndex` in `store`, between a turn's tracked-file
writes and its index write, walked by `TestCrashMatrix_WTurnFileEvents`. Recovery handles and plumbs
the `op=rebaseline` operation marker, the mid-day re-baseline that would delete and recreate the
turn repository at a commit boundary when its loose objects cross a threshold, without touching the
history repository or advancing the day; recovery routes an `op=rebaseline` crash straight to
turn-repository init. The mid-day arming trigger is not wired (ROADMAP.md).
Packing: `MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`) packs both repositories. After every `gcCheckEveryCommits` (64) turn-repository commits the adapter reads the turn
repository's loose-object count and runs `Consolidate` at `gcLooseObjectThreshold` (5000); the simulation's day off (the harness's `SleepCycle` step) also runs it. Packing is safe at any time because archived blobs stay reachable through the deletion
commit's parent and Personant never rewrites the history repository's commits (the turn repository
is disposable). No aggressive repack (`git gc --aggressive`) is scheduled. Session checkpoint commits target the turn repository with a full `Add(".")` staging
pass. The open sequence runs `memops.GateHomeFormat` before Reconcile;
`version.UnversionedHomeFormat` is pinned at 1 forever and is never an alias of
`version.CurrentHomeFormat`, so an unmigrated legacy store directory keeps resolving to format
revision 1. `GateHomeFormat` migrates an older store directory by walking the table
`homeFormatMigrations` (steps keyed by from-revision; 1→2 and 2→3 change only the format revision:
revision 2 adds optional excerpt bundles, revision 3 the tracked-file store, whose legacy
`files.json` the file-retention step removes), the caller writes the format revision before
Reconcile, and a crash between that write and the log leaves a current-revision store directory that
needs nothing further.

### Memory consolidation

Built: `MemoryOps.Consolidate` packs both git repositories (git gc and repack, SPEC §3.11), and `measure.RebuildTrees` rebuilds the within-thread summary trees. Only the simulation's day off (the harness's `SleepCycle` step) runs both. Outside the simulation, `Consolidate` runs only from the turn-repository loose-object check (see "Mechanism names"), and nothing runs `RebuildTrees` (ROADMAP "No production trigger for consolidation"). SPEC §3.10.8's content-retention keep/toss stage, which cites this section by name, is not built (ROADMAP Far-horizon, "Memory consolidation").

### Model access and inference telemetry

Standard pattern: THESIS "The building blocks", row "Model access".

opencode makes the provider call, so the memory service sees no provider response and receives none
of the extended inference-timing block that `model.inference-telemetry` logs (`ttft_ms`,
`prefill_ms`, `gen_ms`, `total_ms`, tokens/second and token counts; see "Event log format"). Token
counts reach the memory service through opencode's step and usage events (`session.step.ended`); no
source for the timing fields is established.

**Open:** the source of the inference-timing block under opencode. Only the in-process turn emits `model.inference-telemetry` today (`internal/turn/turn.go`), from its own provider response.

The local provider is **oMLX**, an OpenAI-compatible endpoint, configured in opencode's own
provider settings.

The memory service's judgments go through the host (SPEC §8.2.1); text generation for the
summarizer, clusterer and recall judgment through the plugin's `experimental.generate` (an opencode plugin hook, integration-options report §2.3) over
opencode's providers; the embedder names an opencode provider in `[embedding]` (SPEC §8.2.2); how
embeddings are requested through opencode is Open A12a (SPEC §8.2.1). Where `[embedding] vectorLength` is set,
the embeddings request carries it as the `dimensions` parameter.

### Model-family as platform

**Open:** U10 — whether one model family is tuned for the memory prompts (the prompts that ask the model for narrow memory judgments: topic tagging, summary drafting, clustering, recognition) or every model opencode runs is supported and measured per family (ROADMAP U10). Until decided, the Gemma family, served locally via the `reaper` provider, is the tuning target for the memory prompts.

Adding a model family is per-family engineering, not configuration: model families share no
deterministic foundation to abstract over, and cross-family mismatches fail silently and
statistically, accumulating misclassified threads before recall measurably drops. Each supported
family carries (1) base intent prompts, (2) family-specific prompts, (3) a per-family evaluation
suite.

#### Isolating the model-family surface

Keep the model-family surface narrow and isolated: the memory prompts
(`internal/prompt`, `internal/curator`), output-stream interpretation and embedding model
assumptions. Everything else (the memory store, port boundaries, recall scoring, dedup, automatic
git) is model-agnostic and survives a move to another storage backend cleanly.

#### Token ceiling and model context window

The default `context.token-budget` (200000, SPEC §2.6.1) is the token ceiling composition enforces.
256K is the model context window the simulation and working-set sizing assume, so the ceiling sits
below it. The window figure affects active-threads slot byte budgets, the pressure to close threads,
recall query depth, and the mock client's need to simulate token counts for budget-exhaustion
testing.

### Minimize memory prompts

> **Every memory prompt is a tax paid forever. Every deterministic algorithm is a fixed cost paid
> once.**

The model is non-deterministic, costly, slow, opaque, and — because of the
[model-family-as-platform](#model-family-as-platform) principle — every memory prompt multiplies:
each prompt requires re-tuning per supported family.

The corollary: **even within model tasks, keep prompts narrow.** Constrain the input space;
constrain the output space; validate output deterministically before trusting it. A broadened
prompt expands both the family-tuning surface and the silent-drift surface.

### Concurrent sessions

Not built. Two live opencode sessions against one memory is Open (SPEC §1.1; ROADMAP B5). The memory service is the single writer either way. One consequence of today's design: `session.started active=` is logged once per service run, while the active project is per opencode session.

---

## Project identity

| Level | Form | Mutable? | Purpose |
|---|---|---|---|
| Internal handle | `prj_<n>` | no — assigned at creation | storage key (`projects/prj_<n>/`); thread registry reference |
| Remote identity | normalized git remote URL | yes (rare) | "is this the same project I've seen before?" — survives file moves and renames |
| Display name | user-chosen string | yes (`/project rename`) | UI rendering, command targeting |

**Project resolution** when an opencode session starts (SPEC §4.5.7). Its input is opencode's
session directory:

1. Git remote of the session directory (or an ancestor) → known project's `remote_urls` /
   `historical_remote_urls`.
2. Session directory path → known project's `current_root_path` / `historical_root_paths`.
3. `last-active` confirmation.

**Open:** U4 — the channel for the interactive steps (the last-active confirmation, the final
fallback, the remote-adoption and collision questions, the file-move question) and the command for
an explicit selection.

When a project gains a remote URL after creation (local-only → published), the memory service
detects it and adopts it as the project's remote identity (SPEC §4.5.4; the adoption question waits
on U4).

---

## Testing as the measurement instrument

SPEC §9 is the measurement instrument that tests, by simulation, whether the design meets SPEC over a long career, and the means by which the techniques evolve iteratively.

The simulation harness (`internal/scenarios/`, including `internal/scenarios/sim/`) is a
load-bearing deliverable under production discipline: one owner per value, key and contract; named
constants. A seam failure (a renamed metric key,
a drifted log line name or a second clock path) makes a check pass vacuously, and the
acceptance criterion is only as sound as the harness.

### Harness ↔ system-under-test coupling rule

The harness imports the system's *public contract
surface*, never its *implementation detail*. The discriminator: **would a production consumer
reasonably expect access to this value?** Yes → it is contract; export it if needed and reference
the public symbol (never mirror it). No → it is internal; the harness observes its *effect* through
a public API/predicate, or tolerates the imprecision — it never imports or copies the internal
value. (An active-set oracle — the generator's own model of which threads are in the active set —
still re-implements the system's *logic* independently; importing a public *scalar parameter* like
the recent-excerpt window is not an independence breach, while importing or mirroring an *internal*
one like the embedding flush-batching cap is.)

**Test levels** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock model via canned responses; metrics emission
   per scenario.
3. **Invariant validators** — callable from any test (`invariants.go`).
4. **Churn tests** — randomized seeded sequences (80% engage / 10% create / 5% close / 5% switch);
   invariants after every operation; a failure dumps the operation log and seed for replay.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating
   points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Acceptance simulation harness** — synthetic workload, logical-clock acceleration, steady-state
   assertions; the longest span is 120 days / four months (§9.1).

Acceptance is the SPEC §9.1 criterion, run as a converging loop, not a one-shot
pass.

The mock model's generated responses carry a counter suffix or a seed-derived UUID in the body
text, so no two responses dedup accidentally.

### Simulation-harness structure

#### File layout — `internal/scenarios/`

| File | Responsibility |
|---|---|
| `harness.go` | Core types: `Step`, `Scenario`, `Harness`, `StepSource`, `StepFeedback`, `RecallAck`, `ClosureAck`, `RecallFidelityMode`. Narrow optional interfaces (`threadIndexer`, `cacheSweeper`, `treeRebuilder`, `intraDivergenceProbe`, `cosineOpsReporter`). The `sliceSource` shim that adapts a fixed `[]Step` to `StepSource`. `SimClockStart`, `SimDayLength`, `SimWorkdayStart`, `SimDayIndex`. |
| `harness_setup.go` | `newHarness` (per-scenario isolation, clock default, telemetry wiring) and `scriptedCurator`. |
| `harness_run.go` | `RunScenario`, `runStep` (the seven named phase helpers: `stepSetup` / `stepSetClock` / `stepExecTurn` / `stepScrapeEventLog` / `stepMeasureRecall` / `stepRecordMetrics` / `stepCloseDays`), `restartSession`, and the final-gauge helpers (`foldEventLines`, `peakHistorySymbols`). |
| `harness_resolvers.go` | Per-step resolver builders (`recallResolverFor`, `closureResolverFor`) and the invariant-cadence policy (`perStepInvariants`, `runInvariants`). |
| `recall_fidelity.go` | Recall-fidelity measurement (`recordRecallFidelity`), the incremental log-tailer (`logTailer`), and `classifyRecoverability`. |
| `invariants.go` | The invariant validator set (thread registry integrity, index drift, project refs, last-active validity, frontmatter-registry sync, engagement consistency, thread accounting, closed-thread consistency, archive resolvability, dedup consistency, budget overflow: the `Verify*` functions). `DefaultInvariants` = `cheapDefaultInvariants` (per-step: `VerifyLastActiveValid`, `VerifyNoBudgetOverflow`) + `heavyDefaultInvariants` (store-scale: once per sim day and once at end of run). |
| `metric_keys.go` | Cross-seam metric-key table (see "State-ownership map" below). |
| `sim/workload.go` | On-demand workload generator (`generator`, `GenerateWorkload`, `runWorkDay`/`runSession`/`runDayOff`). |
| `sim/campaign.go`, `sim/wander.go`, `sim/intra.go` | Specialized step-sequence builders for the campaign, within-thread wander, and intra-thread recall workload variants. |
| `sim/telemetry.go` | Per-day snapshot emission and the sim daily-record schema. |
| `sim/sim_test.go`, `sim/sim_rung_test.go` | `TestSim` (entry point of the acceptance ladder, SPEC §9.1) and span-by-span regression guards. |
| `opencode/` | the prototype's opencode driver and executor (on `probe-path`; see "opencode-in-loop harness"). |

**State-ownership map — single source for every mutable harness value**

A seam failure (above) comes from two independent sources claiming the same value.
Each of the following is owned in exactly one place:

| Value | Owner | Rule |
|---|---|---|
| Simulated clock | `Harness.pinnedClock` — a **slaved copy** of `Step.At`. `stepSetClock` does `pinnedClock = Step.At`, never integrates a delta. One clock advance path; see SPEC §9.4.1 for the single-clock rationale. | Never set `pinnedClock` from a delta except as described for the execution-time refinement step (a refinement turn is one the generator injects after a recall step misses), which normalises `At = pinnedClock + TimeDelta` before the set. |
| Day index | Derived: `SimDayIndex(pinnedClock)`. Never a parallel counter. | Any code that needs "which sim-day is this?" calls `SimDayIndex(h.pinnedClock)`. |
| Generator exec clock | `generator.execClock` — an exact generator-side **mirror** of `pinnedClock`, maintained by `reconcileAt`: every emitted step's `At` is re-anchored forward to it when execution-time refinement turns advanced the clock past the step's planned instant; an `At`-less refinement advances it by `TimeDelta`, mirroring the harness normalize. | The harness never reads it; it exists so the fix lives at the source (the generator) and `stepSetClock`'s non-monotonic warning stays a pure indicator — mirror drift is exactly what triggers it, so the indicator stays armed. |
| Cross-seam metric keys | `metric_keys.go` — the single const block. Harness writes them; sim reads them. | A key rename is one edit; a compile error if a reference is missed. Keys owned entirely inside `sim/` are NOT mirrored here. |
| Crash-scenario coverage | `crashScenarioCoverage` (`crash_matrix_test.go`) — the single greppable table mapping each registered `crashpoint` name to its scenario. `TestCrashPointCoverageGate` enumerates `crashpoint.RegisteredNames()` against it. | A registered crashpoint with no scenario **fails the suite** — the mechanical check that stops silent kill-point rot. The `TurnOracle` is a local immutable pre-crash baseline, not shared mutable state, so it is deliberately not mapped here. |
| Archived thread set | `Harness.archivedThreadIDs` — **index-derived** via `refreshArchivedSet` from the authoritative archive index (`store.LoadArchiveIndex`, entries with `RecoveredAt == ""`), reassigned wholesale; refreshed when a tailed batch carries an archive or restore line (the change trigger, not the source of truth). | The archival-forgiveness filter in recall fidelity and `VerifyThreadAccounting` both read this set; a restored thread drops out the instant its index entry records the restore, and a log-emission gap can no longer under-count it. |
| Created thread set | `Harness.createdThreadIDs` — folded incrementally from tailed `thread.created` log lines. | Same pattern as above. |
| Recall-preservation divergence tally | Accumulated directly in `runStep` (not read from `measure.Service` atomics post-run), so the tally survives `RestartSession`-driven Service churn. | A restart installs a fresh Service whose atomics start at zero; reading post-run would lose measurement probes (a measurement probe is a scripted step with `ExpectedRecallMatches` ground truth) from prior sessions. |
| Active-set oracle | Generator-owned. Cross-checked each step against `StepFeedback.RuntimeLayerB` in two directions, both ==0 checks on mock and live-embedding run configurations and report-only under live inference: forward (`layerb_shadow_divergence`, service-resident threads absent from the oracle: LRU agreement) and reverse (`layerb_shadow_reverse_divergence`, checked against `rtActive` (the harness's mirror of the service's active set), which also replicates measurement-carrier displacement (a measurement carrier is a synthetic thread created to host a recall-measurement query, `sim/workload.go`) and §3.5 turn-idle decay-triggered closing). One of the two declared exceptions to SPEC §9.4's event-log-only recall scoring. | The oracle's LRU is the generator's basis for expected-match sets; a divergence would corrupt recall measurements. |

#### Generator ↔ harness protocol

The `StepSource` interface is the only coupling between the on-demand workload generator and the
harness drive loop. The protocol is:

1. The generator emits `Step` structs carrying an **absolute `Step.At`** (the step's position on the
   simulated clock, derived from `SimClockStart`). This field is mandatory for sim steps;
   `stepSetClock` sets `pinnedClock = Step.At`.
2. After each turn, the harness passes `StepFeedback` back into `StepSource.Next`. `StepFeedback`
   carries: `RecallMatchFireIDs` / `EmbedMatchFireIDs` / `IntraMatchFireIDs` (what the memory service
   surfaced), `RuntimeLayerB` (the authoritative active-set membership for the oracle cross-check),
   `TargetRecoverable` (archival-forgiveness predicate for measurement probe scoring), and
   `RecallExpectedForgiven` (the forgiven expected count the hit/miss counter must use).
3. The **log-line-name contract**: the harness scrapes specific memory-service log strings
   (`spine.match-fire`, `spine.embed-match-fire`, `spine.intra-match-fire`, `thread.created`,
   `archive.archived`) as the measurement signal; these strings are pinned by contract tests so a
   rename breaks a test, not a measurement.

#### Acceptance-run mechanics

- `internal/scenarios/sim/` holds the acceptance simulation (`TestSim`, the template-filled
  corpus workload); `just sim DURATION=<N>d` runs a chosen span (aliases `1d`/`1w`, Go duration
  forms such as `168h`) and `just test` runs the default `1d` span. Metrics come from
  `internal/metrics`; scenario steps carry `ExpectedRecallMatches` ground truth scored in
  `RecallStrict` or `RecallMeasureOnly` mode. The baseline comparator — committed baselines under
  `testdata/baselines/<scenario>.json`, `just sim-baseline` (records one for a duration) and
  `just sim-fidelity` (re-runs the mock sim under a chosen executor and compares) — is the
  prototype's, on `probe-path` (ROADMAP A2).
- The mock runs (`just sim`, and the sim package under `just test`) run with a nil embedder, so the fine level and the
  completeness floor do not execute there; `just sim-completeness-rung` is the
  embedder-enabled run configuration that asserts every measurement probe in the flush-lag gap in
  findability is surfaced. The scenario
  harness pins `recall.ack-mode: always` (a scripted `Step.RecallAck` stands in for a user's
  answer).
- The opening line of each turn in the log is `context.modified source=user.prompt`: the
  tag-fidelity grader (`internal/scenarios/sim/tagfidelity.go`) segments observed turns on it, so
  suppressing, reordering or duplicating that line must revisit the grader.
  `recall_unexplained_absence` (an expected thread that is absent from the thread registry and from
  the archive index) is always a memory store integrity failure and must stay 0, while an archived
  expected match that can be restored is forgiven (`TargetRecoverable`,
  `RecallExpectedForgiven`) and counted as `recall_archived_recoverable`.
- Long-span precision decline is the SPEC §9.1 corpus-saturation floor.
- Recall-fidelity series are `recall_fidelity_{precision,recall,f1}` and
  `recall_fidelity_adversarial_{precision,recall,f1}`; the intra-thread descent divergence (SPEC
  §9.4) is `recall_intra_descent_divergence`.
- Live run configurations use `reaper.local` (`reaper.local` is the live-run host, oMLX serving the gemma-4 family, reached through `internal/testsupport`, override `PERSONANT_REAPER_URL`; gemma-4 family inference, `nomicai-modernbert-embed-base-bf16`
  embeddings). Service-in-loop raises per-turn cost from tens of milliseconds to single-digit
  seconds against roughly 20 minutes for a mock 120-day run, so the mock regime stays the default
  loop. The anchor-projection workload variants (`vague-new`, `drift`, `invert`, interleave) report
  `drift_recall_origin`/`drift_recall_dest`, `abandoned_premise_recall` (whether a query for an
  abandoned premise still surfaces its thread), `superseded_precision` (the precision of recall that
  matches through superseded symbols), `ever_central_count`/`history_len` and `projection_churn`
  through `metric_keys.go`.
- Cadence: the generator's day functions (`sim/workload.go` `runWorkDay`/`runSession`/`runDayOff`)
  and the harness day-close share SPEC §9.4.1's clock contract. The day-off (the simulated workload's day off, SPEC §9.4.1) emits a `SleepCycle`
  step the harness intercepts to drive consolidation, and a day-close is timestamped
  `simDayCloseDate(d) = SimClockStart + d·SimDayLength` (`internal/scenarios/harness.go`). All fuzz (start jitter, per-turn spans, break, work-block boundary) draws from the single seeded
  RNG in fixed order. Tests:
  `TestCadence_DayStartsNoPrecession` (at least 9 days), `TestCadence_DayOffPreservesPhase` and
  `TestDayOffThroughHarness`. A run of at least 9 days asserts that work boundaries after the
  day-off stay on grid, that the day-off advances exactly one 24h slot on each side, that exactly one
  0-turn day-off record appears with contiguous `sim_date`, and that the day count equals
  `int(Duration/24h)`. A simulated-clock read inside one turn (`clock.Timeline()` within
  `turn.Run`) returns the same instant; decay-triggered closing keys off `TurnNumber − LastEngagedTurn`.
- Crash injection: `internal/crashpoint` hooks (`crashpoint.At(name)`) are registered at every kill
  point. `RestartWithCrash` (`internal/scenarios/restart.go`) arms a point, drives the turn or
  operation, discards in-memory state and drives `Reconcile` against the on-disk result. The matrix
  covers every registered crashpoint (`crashScenarioCoverage` is the table): the turn orderings,
  every rollover step, every archival sub-window, standalone archival, recovery internals and
  turn-repository init. Each fixture asserts the full invariant suite plus
  `VerifyThreadMetaMatchesSpine`, at most one turn of loss against a pre-crash baseline, exact
  journal bytes, empty `RevertedPaths` on rollover and archival paths, exactly one day-commit, and an
  idempotent second `Reconcile`. The matrix is unit-grade (synthetic store directories; it runs in `just test`); `TestSimBarrier2Day` adds a 2-day run configuration that crosses a
  rollover to the default suite.

### Event log format

Standard pattern: THESIS "The building blocks", row "The event log".

The memory service↔harness measurement contract (`logs/YYYY-MM-DD.log`, `internal/eventlog`). SPEC §2.8
states the requirement that every decision the §9 regime measures is recorded; this is the format
and vocabulary. Plain text, append-only, one event per line; no JSON schema in v0.1:

```
<RFC3339-timestamp> <event-name> <free-form-details>
```

Event names are dot-separated category and action (`thread.engaged`, `spine.match-fire`,
`retire.prompt`). An event absent from the vocabulary cannot be measured, so a mechanism that emits
a new decision event adds its entry here. Actions ending in `-error` (and `warning`) are forensic
diagnostics; §9 measurement keys off the decision events. No event carries multi-line content.

```
2026-05-08T02:55:44-07:00 system.bootstrap version=0.1.0 home-format=1 commit=abc1234 home=/Users/u/.personant
2026-05-08T02:55:45-07:00 session.started active=prj_3
2026-05-08T02:55:50-07:00 thread.engaged thr_42 turn_count=1247
2026-05-08T02:55:51-07:00 spine.match-fire thr_88 score=0.62 matched=trefoil,unknot query_size=5
2026-05-08T02:55:51-07:00 spine.embed-match-fire thr_88 score=0.610 query_chars=140
2026-05-08T03:12:00-07:00 retire.prompt thr=thr_42 inactivity=12
```

| Category | Events |
|---|---|
| `system` | `bootstrap` (identity line, once per process open, after the §9.1 format check resolves: `version=` the memory service semver, `home-format=` the EFFECTIVE on-disk revision, `commit=` (`unknown` on a binary built without a build id), `home=`; distinct from `session.started`), `home-format-override` (a `--allow-newer-home` open waved a newer store directory through the §9.1 refusal — `on-disk=`, `binary=`; always paired with a stderr warning), `home-format-migrated` (an older store directory was migrated forward at open by the §9.1 format check — `from=`, `to=`), `context-ceiling-breach`, `context-ceiling-unenforceable` (the post-flight token-ceiling check could not be evaluated: no `usage.prompt_tokens` for a turn while a ceiling was configured — `reason=usage-unavailable ceiling= turn=`; one line per affected turn; the count is never estimated), `empty-response` (forensic; the final drained response carried zero visible content — `reprompted=yes\|no`, `turn=`; SPEC §3.3), `turn-aborted` (a retracted turn — `turn=` the turn's transaction id, `phase=` the labelled stage, `bytes=` the retracted input's size; SPEC §4.3.3; **never the text**; **Open:** U8 — emitted only if retraction stands), `turn-abort-release-error` (forensic; releasing a retracted turn's recovery scope failed; same Open) |
| `thread` | `engaged`, `engaged-non-owner`, `engaged-cross-project`, `engaged-miss`, `created`, `created-meta-only`, `state-change`, `renamed` (a working name changed — `thr=`, `old=`, `new=`; same shape as `project.renamed`; unmeasured, recorded so every working name is reconstructable from the log), `fetch-miss`, `fetch-cross-project`, `anchor-projection-overflow`, `tag-defaulted` (`thr=` the bound owner, `cause=missing-tag\|empty-response`, `reprompted=yes\|no`; SPEC §3.3) |
| `spine` | `match-fire`, `embed-match-fire`, `intra-match-fire` |
| `recall` | `offer` (`count=N`), `accept` (`thr=`, `layers=`, `ack=human\|auto` — `auto` is an auto-fetch band candidate fetched without asking; any accept-rate measure counts `ack=human` lines only), `decline` (`thr=`, `reason=not-relevant\|wrong-project\|already-known` — `already-known` is also the memory service's own verdict on a candidate already resident in the active set under `recall.ack-mode: banded`), `net-cap-hit` (the intra-thread descent dropped related leaves at `scoring.NetCap`), `flush-backlog` (pending flush jobs passed the warning threshold), `W1-diag` (per-probe line for a descent-versus-flat-scan divergence: class, flat, descent, missed, substituted; `measure.go:1689`), `fire-error`, `error`, `index-error`, `embed-error`, `debt-window-error`, `tree-error` |
| `retire` | `prompt` (`thr=` and `inactivity=`\|`trigger=manual`\|`trigger=queued`, the last for a SPEC §3.5 drain point), `ack` (every applied close-out — close or WIP — `resolution=`, `edited=yes\|no`, `ack=human\|auto`; the ack-edit-rate indicator metric (a metric watched as a ratio) counts `ack=human` lines only), `pending` (a decayed thread queued as an exception for the drain point — `thr=`, `inactivity=`, `reason=anchors=N\|turns=N`; once per idle episode, not per scan), `defer`, `complete` (`resolution=`), `curator-error`, `load-error`, `resolver-error`, `apply-error`, `error` |
| `archive` | `archived`, `recovered`, `recovered-record`, `skip`, `under-drain` (archival at a rollover could not reach the registry low-water mark), `error` |
| `recovery` | (startup recovery, see "Crash recovery and the turn and history repositories"; forensic) `begin`, `complete` (`cells=` lists the recovery cases hit; `reset=`, `stamped=`, `unrepairable=`), `pending` (`op=`, `day=` — a rollover or archival completion left pending), `rollback` (torn-turn reset — `turn=`, `reverted=`, `debris=`), `journal-recovered` (preserved in-flight bytes — `turn=`, `records=`), `morning-init` (`baseline=`, `rebuilt=`), `adopt` (case 12 greenfield/legacy), `stamp-repaired` (case 9), `unrepairable` (archive entry stays refused — `thr=`, `reason=`), `quarantined` (byte-exact preserved path), `log-tail-repaired` |
| `barrier` | (day rollover steps, see "Day rollover"; forensic) `begin` (`day=`), `archived` (archival step — `day=`, `count=`), `folio-settled` (excerpt-storage step, once per rollover — `day=`, `threads=`, `bound=`, `unbound=`: excerpts moved into / out of bundles), `folio-skip` (excerpt-storage step left an excerpt where it was — `thr=`, `name=`, `reason=too-large\|conflict\|unreadable`; for `unreadable` the name is `folio.jsonl` and nothing in the thread moved), `day-committed` (day-commit step — `day=`, optional `repaired=true`), `reborn` (turn-repository re-init step — `day=`, `baseline=`), `complete` (`day=`), `re-mint` (defensive day-commit step re-mint on a non-day-shape HEAD), `quarantined` (quarantine-and-proceed — `paths=`, `dir=`, `restored=`), `tag-error` (forensic; a lifecycle-tag derivation read/parse/tag fault — never fatal to the seal) |
| `consolidate` | `sleep-cycle` |
| `dedup` | `pruned`, `prune-skip`, `error` |
| `fs` | `edit-no-path`, `write-error` |
| `tool` | `result` (a tool result applied to memory as a `tool.result` delta, SPEC §3.0.1 — `name=`, `id=` the call id, `bytes=` the byte count BEFORE the SPEC §3.1 tool-output limit; an error result is a result; never the arguments). |
| `staging` | `promoted` (`normalized=`, `staged_at=`, `cited_at=`, `turn_span=`), `evicted` (expiry at window close, SPEC §3.10) |
| `topic` | `re-prompt` (`cause=missing-thread` + `fetched=` for the §5.5 fetch, `cause=missing-tag` for the §3.3 missing-tag fallback, or `cause=empty-response` for the §3.3 empty-response fallback; **Open:** U3 — `cause=missing-thread` has no producer under opencode), `tag-missing`, `tag-invalid` (forensic; a near-miss tag candidate that failed strict validation — `reason=markdown-mangled\|bad-delimiters\|bad-thread-list\|bare-new-topic` + a short sanitized `snippet=`; additive to `tag-missing`), `warning` |
| `session` | `started` (a service run started, SPEC §1.1 — `active=`; distinct from `system.bootstrap`), `ended` (a service run ended), `working-set-save-error`, `checkpoint-error` |
| `project` | `created`, `switched`, `renamed` |
| `model` | `stream-close-warn`, `inference-telemetry` (the provider's extended inference-timing block, one line per turn: `ttft_ms=`, `prefill_ms=`, `gen_ms=`, `total_ms=` in MILLISECONDS, plus `prefill_tps=`, `gen_tps=` (the provider's own tokens/second), `prompt_tokens=`, `completion_tokens=`, `cached_tokens=` (prompt-cache hits), `turn=`; a de-facto extension, not OpenAI schema — when the provider sends none the line is not emitted, and absence is never an error or warning; **Open:** the source of the block under opencode, which makes the provider call; today only the in-process turn emits it (see "Model access and inference telemetry")) |
| `workset` | `warning` (slot render failure, SPEC §3.1) |
| `context` | `modified` (`source=`, `bytes=`; SPEC §3.0.1). The opening line of each turn is defined in SPEC §2.8. |

Some category and code names predate the settled vocabulary: `spine` is the thread registry, `retire` closing, `dissect` the clusterer, `accrual` feedback directive tuning, `barrier` the day rollover and `sleep` consolidation.

**Reserved names** (a mechanism that emits one follows the shape of its category):
`system.shutdown`, `system.error`, `system.config-reload`, `spine.match-miss`,
`spine.entry-updated`, `recall.cross-project-fire`, `project.cd-changed`, `project.remote-adopted`,
`project.remote-updated`, `project.remote-collision-prompt`, `project.meta-updated`, `dissect.fire`,
`dissect.cluster-proposed`, `dissect.complete`, `directive.accrual-update`,
`directive.parameter-read` (sampled), `index.rebuild-start`, `index.rebuild-complete`,
`index.verify-fail`.

### opencode-in-loop harness

The prototype extends the existing harness; it does not build a second one. One Go test process hosts
the workload generator, the driver, the mock model endpoint and the memory service, and spawns one
opencode server; the plugin, loaded by opencode, calls back into the test process.

- The workload generator emits steps to the driver, which calls opencode's HTTP API.
- opencode's plugin calls the memory service in the test process over HTTP + JSON.
- opencode's provider `personant-mock` (the mock provider in the harness's isolated opencode config) calls the mock model endpoint, also in the test process,
  over the OpenAI-compatible wire.

Because the memory service runs inside the test process, the harness keeps direct access to the
event log and the memory store; invariants, recall-fidelity scraping and metrics run as they do for
the in-process turn.

- **Turn executor.** `-sim.turn-executor` selects `runwithinfo` (default, the in-process turn),
  `phases` (`RunTurnInProcess`, the prototype phases) or `opencode` (`internal/scenarios/opencode`,
  which replaces only the turn-execution step of `runStep`). `just sim-fidelity EXECUTOR=phases`
  proves the phases reproduce the committed baselines; `just sim-opencode` drives every turn
  through the installed opencode and is not part of `just test`.
- **Mock model endpoint.** `model.MockServer` reports usage so opencode's token accounting sees
  real numbers.
- **Isolation.** The driver points `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` and
  `XDG_CACHE_HOME` under the run's data home, so the user's opencode database is never touched, and
  writes the isolated opencode config (the mock provider, `compaction.auto: false`, the plugin
  entry).
- **Workload split.** The generator is reused as is: user input and acks go through the driver,
  the scripted model responses are installed in the mock, and `PreEvents` (`fs.read`, `fs.write`,
  `fs.commit`) reach the service through opencode's own tools against a fixture workspace.
- **Determinism.** Commit author and committer dates are pinned to the memory system's clock
  wherever run-to-run repeatability is measured, so a commit hash repeats across runs (probe results
  §6, lesson 7).
- **Metric requirements.** `oc_missed_deltas` and `oc_uncomposed_requests` must be 0 (probe results
  §2), among the `oc_*` keys in `metric_keys.go` (hook round-trip time, step overhead, opencode
  message row count, database size, hook fail-open counts). `oc_uncomposed_requests` is counted at
  the mock.
- **Prototype targets.** `just probe-smoke` runs one handwritten turn; `just probe-resume` stops or
  kills opencode and the service mid-session and resumes (resume probe).

**Open:** prototype-code retention — whether `model.MockServer` (the mock served as an
OpenAI-compatible endpoint) stays on the main line, as a run with opencode in the loop requires
(SPEC §9.2). The baseline comparator (`sim-baseline`, `sim-fidelity`, the committed baselines) is
named for keeping; the rest of the opencode driver and executor is undecided.

**Open:** whether the §9.1 acceptance criterion needs an opencode-in-loop run configuration.

---

## Deferred work

Deferred work is tracked in **ROADMAP.md** (horizons: near-term / mid-term / far-horizon), which owns
the catalog, including the design capture for consolidation and concurrent sessions.

---

## Anti-patterns to push back on

| Anti-pattern | Why it's wrong | Reach for instead |
|---|---|---|
| "Let the model decide where to put state" | Pushes authoritative state into the model | Deterministic code holds state; the model drafts, deterministic code validates and writes |
| "Re-implement something opencode owns" (tools, permissions, UI, session storage, providers) | Wheel re-invention; the project's one hard problem is context | Conform to opencode; take over only what enters the request, through a hook. Elsewhere, borrow existing designs and use well-maintained imports |
| "Put memory logic in the plugin" | Splits the single writer; the plugin cannot be tested with the sim | The plugin forwards and applies; all state and logic stay in Go |
| "Use SQLite for the thread registry" | Storage violation; loses inspectability + git-diffability | Text + git; a derived index only, if scale forces one (ROADMAP Far-horizon). |
| "Have the model run `git commit` on the store working tree" | Mixes model-controlled with automatic git | The service commits automatically |
| "Add this dep speculatively" | Approved-when-earned violation | Wait until consumer is real; bring dep in *with* consumer |
| "Tests can come later" | Asymmetric-cost violation | Measurement infrastructure built alongside features, not after |
| "Mock model is too narrow; let's use a real model in tests" | Adds non-determinism, network dependency, cost | Mock with scripted/generated responses as the default loop; a real model or embedder only in the opt-in live runs (`just integration-test`, the `sim` live toggles, `just sim-completeness-rung`, `just sim-tokenceiling-rung`) |
| "Speculatively prefetch threads we *might* need" | Violates "every loaded thread is needed" | Model emits topic tag; memory service fetches what was named |
| "Shorten the topic-tag instruction to save tokens" | While the in-band tag stands (Open: U3), emission is the load-bearing recognition signal | The system prompt's topic-tag directive is non-optional |
| "Reset the store working tree when it's dirty on startup" | A markerless-dirty tree is a legitimate hand-edit; resetting on dirtiness destroys user work (and, when it is not yet in the history repository, a whole day) | Make `reset --hard` conditional on the operation marker/journal signal, never on dirtiness; absorb hand-edits forward (see "Crash recovery and the turn and history repositories", case 3) |
| "Commit durability writes async / deferred (flush later)" | A commit lagging its authoritative writes reintroduces the torn-window ambiguity the operation marker/commit protocol exists to eliminate | Commit ordering is synchronous, always; cost comes from cheaper steps (scoped add, disposable turn repository), never from widening the loss window |

---

## Document map

| You want | Look here |
|---|---|
| The project documents themselves | `PROJECT_DOCS/`: THESIS at `entry-point.md`, then `SPEC/`, `ARCHITECTURE/`, `CONVENTIONS/`, one leaf per section; the root files are pointers |
| How the plugin, service and opencode fit together | "Plugin route" (this document) |
| Where a package lives and what it does | "Module map" (this document) |
| Record and file formats | "Storage and record formats" (this document) |
| Storage constraints | ARCHITECTURE §"Storage non-negotiables" |
| The acceptance criterion | SPEC §9.1 |
| opencode facts the route rests on | the reports in `OPENCODE_RESEARCH/`, listed under "Plugin route" |

---

## How decisions evolve

Personant evolves by:

1. **Calibration scenarios** — parameter sweeps over canonical workloads; metrics matrices identify
   operating points.
2. **Feedback directive tuning** — see "Personalization through feedback directive tuning".
3. **Empirical pressure on watch items** — known weak spots (categorized below) surface via
   instrumentation; targeted work follows the data.

**Categories of known weakness** the simulation regime is designed to surface and quantify:

- **Symbol-extraction noise** — synonym fragmentation, low-information leakage, model emission
  drift.
- **Recall precision** — cross-thread symbol collision, anchor projection quality (SPEC §2.7.4).
- **Deterministic-pass coverage** — per-project regex curation needs.

---

## Mental model summary

> The service is the holder of authoritative state and the single writer. The model is a narrow
> judgment instrument. opencode owns everything except what enters the request. Every
> working-window mutation goes through the per-delta pipeline (§3.0). Every dependency earns its
> keep with a real consumer. Testing is the measurement instrument, not a quality check. Constraints
> are load-bearing.
