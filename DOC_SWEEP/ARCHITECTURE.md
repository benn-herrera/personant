# ARCHITECTURE – Personant

How this project's implementation meets requirements designated by SPEC

Must-see:
- No user-facing behaviors or outcomes are designed here, that belongs to SPEC
- No project-specific process is dictated, that belongs to CONVENTIONS

## What this is

Personant is a context-management and long-lived memory system that runs under opencode. It owns
what enters every model request — composition, compaction and summarization, recall, and the
sub-mind split and re-merge — so that an agent has **persistent working memory across arbitrary
projects with continuity**: one continuous career, no session boundaries, no compaction-driven
information loss, cross-project recognition (SPEC §1). opencode owns everything else: the terminal
UI, session storage, agents, tools, permissions, providers and configuration.

---

## Plugin route

Report shorthand used below, all in `OPENCODE_RESEARCH/` at the repository root: **IO** `OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md`,
**PP** `OPENCODE_RESEARCH/OPENCODE_PREPROBE_RESULTS.md`, **PR** `OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md`, **RP**
`OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md`, **CI** `OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md`, **SS**
`OPENCODE_RESEARCH/OPENCODE_SERVER_SURVEY.md`. opencode `file:line` citations stay in those reports.

### Tense

In this section and the ones that follow it, present tense states the production
design. Facts about the probe code are marked "the probe"; the production service and plugin are
not built. The probe code lives on branch `probe-path` and is not on the build branch: every path,
recipe and executor this document names as the probe's (`opencode-plugin/index.ts`,
`internal/opencodehook/`, `internal/turn/probe_phases.go`, `internal/scenarios/opencode/`, the
`phases` and `opencode` sim executors, `sim-opencode`, `probe-smoke`, `probe-resume`,
`probe-deps`) resolves there (PR §§6–7). ROADMAP A2 brings the baseline comparator across; A3 and
A5 rebuild the rest.

Personant runs under unmodified opencode as two pieces:

- **Plugin** (`opencode-plugin/`). TypeScript, loaded by opencode in its own Bun process. An
  adapter that holds no memory state: it forwards hook inputs and events to the service and applies
  the answers, and imports only what the host supplies, so all memory logic is in Go (IO §2.1).
- **Memory service** (Go). The only writer Personant provides for memory state (see "Autonomic vs
  LLM-controlled"); it composes every model request. The plugin reaches it over loopback HTTP +
  JSON with `fetch`, because Bun's `node:net` and child-process APIs are unverified from plugin
  code (IO §5, item 6). Several opencode servers sharing one service: see "Concurrent sessions".

Data flow: opencode's session runner calls the plugin's hooks and emits events; the plugin forwards
each to `internal/opencodehook` over loopback HTTP + JSON; the handler drives an `internal/turn`
phase, which reaches storage through the `memops` port; the answer returns the same way.

### Boundary

Outside context management, Personant uses what opencode has chosen, as it is. Where
opencode's machinery would decide what the model sees (compaction, history replay), the plugin
takes that decision over through a hook; it does not reimplement the machinery. The host is
opencode, not Charm's Crush: Crush reads provider API keys from environment variables and runs
its TUI in-process with its backend, so there is no protocol to sit under. The fallback, if
this route cannot give the memory system the control it needs, is a full Go port of opencode's
server (`ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md`).

### Pinned release

The supported opencode release is stated in one place: the version check in
the `sim-opencode` recipe (`justfile`). The source clone at `../opencode` is kept on the installed
release's tag, so the code read is the code run.

### Failure handling

Hooks have no timeout and run sequentially (IO §2.2), so every plugin RPC
carries its own timeout. A Promise-API hook's rejection becomes an Effect defect, so the plugin
catches every failure itself (IO §2.2). The probe plugin fails open: on timeout or error it leaves
the hook input as opencode built it and reports a fallback count.

**Open:** U7 — the production failure policy: fail open (opencode's own composition reaches the
model and memory misses the step) or fail closed (the request is refused). What opencode does when
the `context` hook throws or the service refuses is unestablished (RP §6).

### Integration shape

Each element names the opencode mechanism it rides on (IO §2.3 unless noted).

- **Composition: the `session.context` hook.** It fires on every primary step and every retry;
  `system`, `messages`, `tools` and `options` are all mutable. The plugin sends the hook input to
  the service and replaces `system` and `messages` with the answer. The service builds the answer
  as `turn.ComposeRequest` does: the composed working set as the system prompt, the bounded history
  tail, the current user input and this turn's tool messages. Tool-call/tool-result pairs in the
  returned `messages` stay intact (IO §4, item 4). Any other model call is kept off the provider:
  the `session.title` and `session.compaction` hooks supply `result`, which skips the model call.
- **Wire shape.** A composed request carries one non-empty system part, no empty parts and no
  mid-conversation system messages. Under that shape prompt text reaches the model byte-exact
  (PP §5; PR §6, lesson 8). The hook also resets `options.maxTokens`: opencode fits it to its own
  estimate of its own history, which is too low for a smaller composed request (CI §3, A17).
- **Compaction: `compaction.auto: false` plus plugin-answered checkpoints.** With auto compaction
  off only manual compaction runs, overflow included. opencode reads stored history from the latest
  compaction onward on every step (IO §2.4), so the service schedules checkpoint compactions at a
  turn cadence to keep that read bounded (**Open:** the cadence parameter's name and default; the
  probe takes it as a constructor argument, `-sim.checkpoint-cadence`). The plugin enqueues one
  with `session.compact` and the `session.compaction` hook answers with `result`. The checkpoint's
  summary is a fixed placeholder that the `context` hook discards on replay. A checkpoint is a read
  bound, not a memory mechanism. `experimental.idle` only decides whether a checkpoint is due: it
  fires before the turn's final step event and again after a checkpoint, so it never closes a turn
  (PR §6, lessons 1–2).
- **`/compact` and `/clear`** (CI §4). A user `/compact` cannot be refused; it is answered like a
  checkpoint, and `session.compaction.started` carries a `reason` that separates the two. `/clear`
  is client-side only; it discards nothing because memory is not keyed on the session. This is how
  SPEC §1.1 holds. **Open:** what the two commands eventually map to, and whether a TUI plugin can
  replace or disable them (CI §6).
- **Tools.** Any memory tool registers through `tool.transform`; a tool invented inside the
  `context` hook is dropped.
- **Config.** `personant init` writes the settings opencode must hold (`compaction.auto: false`,
  the plugin entry) into opencode's own config in opencode's own format. Personant-only settings
  live in `~/.config/personant/` and never overlap opencode's (see "Single spelunking location").
- **Code Mode and plugins.** opencode's own server runs, so its JavaScript plugins and Code Mode
  work unchanged. `ROADMAP_PLANS/code-mode-support.md` applies only to the fallback route.
- **Sessions and projects.** The project resolves from opencode's session directory through the
  bootstrap waterfall (see "Project identity"). A session is a window into the one memory; turns
  route to threads by topic, never by session; deleting a session does not touch memory
  (SPEC §1.1).
- **Memory-native commands.** A command that survives is a plugin-registered command
  (`CommandDomain.transform` → `CommandEditor.add`, whose `execute` runs server-side code), not a
  `commands/*.md` file, which only expands into a prompt to the model that cannot be told apart
  from a typed one (CI §2).

**Open:** U4 — which memory-native commands survive (`/topic*`, `/topics`, `/done`, `/closures`,
`/pause`, `/resume`, `/back-to`, `/no-revisit`, `/keep`, `/threads`, `/spine`, `/index-rebuild`,
`/project*`, `/stats`): each is a plugin-registered command, made autonomic, or dropped. Unverified:
how the TUI shows a command whose `execute` sends no prompt, and how `execute` reports a result.

**Open:** U4 — the ack channel. The recall offer, the closure review queue and the startup project
prompts ask the user something. A plugin has only yes/no `permission.assert`; opencode's form
service is not exposed to plugins (IO §2.3). The alternative is to hand the question to the model
through the opencode-expectations slot and read the answer on the next turn.

### Event observation

Every context change reaches the §3.0 chain (see "The §3.0 hook chain") through one of these
opencode mechanisms:

| Context change | opencode mechanism |
|---|---|
| `user.prompt` | `session.prompt` hook, before the prompt is stored |
| `tool.result`, `fs.read`, `fs.write` | `tool.execute.after` |
| `fs.commit` | `tool.execute.after` of the shell tool, for a commit of a tracked path |
| `user.shell-capture` | `session.shell.ended` event (CI M9) |
| `model.response` | `session.text.ended` and `session.step.ended` events, ingested at turn close |
| turn close | `session.execution.succeeded` |
| turn abort | `session.execution.failed`, `session.execution.interrupted` |
| fork, revert, delete | `session.forked`, `session.revert.committed`, `session.deleted` events |

The service records which deltas it has applied per turn, so a missing one is detectable (the
`oc_missed_deltas` gate, see "opencode-in-loop harness").

The probe service maps only `read`, `write` and a shell `git commit` to deltas
(`internal/opencodehook/service.go`, `tool`); other tools' `tool.result` deltas are not mapped.

**Open:** `edit` — SPEC §3.0.1 requires the post-edit file bytes as an `fs.write` delta; no probe
established that opencode's `edit` exposes them, and the probe does not map `edit`.

**Open:** fork, revert and sub-minds — how a branch's work is incorporated or discarded (SPEC
§1.1), decided together. The events above are observed; what memory does on them is undecided.

**Open:** agents and skills — whether and how opencode agent definitions and skills are supported.
A candidate mapping: agent definitions as Layer E directives scoped per agent, skill content as
transient task-class.

**Open:** lineage — no thread carries `derived_from` through opencode, while in-process runs do
(PR §5). The cause is unverified; candidates are tool deltas now applying after `user.prompt` (PR
§11) and the mid-turn fetch not firing.

### Ordering and correlation

- **Tool results apply in tool-call order.** opencode runs one response's tool calls concurrently
  (PR §6, lesson 3), so arrival order is not repeatable. The service holds tool results and applies
  them in call order at the next `session.context` call or at turn close. This makes memory state
  independent of completion order (SPEC §1.5). The probe reads each call's position from the id its
  mock executor assigns (`call_<step>_<index>`); **Open:** the production source of tool-call
  order — a real provider's call ids carry no such index, so the order must come from the response
  itself, and no source is established.
- **Sequence numbers are taken in the plugin as each event arrives,** with forwarding chained,
  because RPCs can reach the service out of order (PR §6, lesson 6).
- **Events correlate by opencode message ID,** not by "the current turn", so a resumed execution's
  late events cannot close another turn (RP §5.3, item 3).
- **Model requests are counted at the provider side,** never from `session.model.request`, which
  reports `title` and `compaction` kinds even when hooks answered them (PR §6, lesson 4).
- **File content.** `fs.write` content comes from the `write` tool's input; its output is only a
  confirmation line (PR §6, lesson 5). The tracked-file store holds file bytes (SPEC §3.9.1), while
  opencode's `read` returns formatted output (a header and `N: ` line prefixes); the probe stores
  that output as returned (PR §9, item 3). **Open:** how the production service recovers the file
  bytes from `read`'s formatted output.

### Injection handling

opencode injects content into a request besides the user's prompt: instruction files, nested
`AGENTS.md`, reminders, MCP content, tool definitions (CI §1). The service turns each into state
rather than history, so that nothing is stale and nothing is counted twice (SPEC §1.3).

- **Instructions slot.** Rebuilt each request from the full per-source texts in the instructions
  system part's `metadata.experimental.instructions`, a list of `{key, text}`. A source's content
  hash decides whether the slot changes. opencode's instruction-update diff messages are not used,
  so no stale baseline exists to patch.
- **Nested `AGENTS.md`.** opencode injects one once, on a `read` into its directory, and never
  re-sends it (CI §3, A9). The service captures it into the instructions slot as its own keyed
  source, where it stays for as long as it applies.
- **History rows are typed by joining** the hook input's message `id` against the plugin's session
  API, because the hook input drops metadata on synthetic messages (CI §2).
- **Drop on re-injection.** Content already accounted for is dropped: by (source key, content hash)
  for instructions, by stored message `id` for history rows. Compaction-checkpoint messages are
  dropped (CI §5, M19).
- **Expectations slot.** The todo list, plan/agent mode, an open question or permission request,
  reminders still in force, the subagent session ID needed to continue a child, and background-job
  results render as current state, not history. The slot is dynamic: its content changes mid-turn
  (SPEC §3.1).
- **Tool definitions.** Sent with every request and counted against the token ceiling. opencode
  curates them per agent and passes them through, with no budget or overflow form (CI §5, T1–T4).
  MCP tools are recognizable by the `<server>_` name prefix.
- **Evicted content (the thunk).** File content dropped from a request for budget is replaced by an
  entry that pins the version (a content hash), points at stored bytes when replay cannot
  reproduce the content, and reads as a recognition entry the model can expand (see "Spine entries
  are the recognition surface"; SPEC §1.4).

**Open:** per-slot answers — all four for the instructions and tool-definition slots, and budget,
priority and overflow form for the expectations slot (SPEC §3.1). Without a tool-definition slot
the whole-request ceiling cannot be enforced. Candidate overflow forms for tool definitions: omit
rarely used tools from a request, or list them by name with definitions on demand.

**Open:** a route that tells an MCP prompt expansion apart from a typed prompt; the hook input does
not distinguish them (CI §2). Until found, an expansion is handled as user input.

**Open:** injection unknowns (CI §6) — whether plugin activation order equals hook registration
order after a reload, and how system-role messages are lowered on provider routes other than
openai-chat.

### Runtime inversion

`turn.RunWithInfo` owns a whole turn in-process. Under opencode, opencode owns the model call and
the tool loop, so the runtime is driven by hook events through phase entry points instead.
Composition is already separable: `workset.Compose` is a pure renderer reached through the
`MemoryOps.ComposeWorkingSet` port.

| Phase | Driven by | Covers |
|---|---|---|
| `BeginTurn` | `session.prompt` | budget, live-turn reject, day barrier, snapshot, `TurnNumber++`, prompt journal, staging GC, pre-event deltas, `user.prompt` delta |
| `ComposeRequest` | `session.context` | working-set composition and request assembly, from this turn's messages in the hook input |
| `IngestToolResult` | `tool.execute.after` | tool-result deltas, held and applied in call order |
| `IngestModelResponse` | `session.execution.succeeded` | tag strip, response journal, ceiling check, `model.response` delta |
| `CloseTurn` | `session.execution.succeeded`, after `IngestModelResponse` | engagement, closure scan, `CommitTurn`, GC, working-set persist, history; idempotent |
| `AbortTurn` | `session.execution.failed`, `.interrupted`, or a phase error | pre-canonical rollback to the pre-turn snapshot |

In the probe, the phases exist in `internal/turn/probe_phases.go`; `RunTurnInProcess` recomposes
them into the in-process turn. `RunWithInfo` is not yet re-expressed over them. The phases call 34 unexported
identifiers of `internal/turn` (PR §8), so they stay in that package; `just probe-deps` regenerates
the list.

#### Close ordering

The response is the turn's `session.text.ended` texts concatenated in event
order, with the last `session.step.ended` token counts as usage (the probe's buffering, PR §9,
item 4; `internal/opencodehook/service.go`, `close`).

**Open:** the recovery turn (SPEC §3.0.4; see "Start order and resume"). No phase covers an entry
on a context call with no preceding prompt: the probe service answers it with an error
(`compose`). Undecided: which `BeginTurn` steps a recovery turn runs, and what its `user.prompt`
content is — opencode's synthetic continue text or the original prompt (RP §5.3, item 1).

**Open:** mid-turn prompt — a `steer` prompt can arrive after tool results; whether it opens a new
turn or joins the turn in progress (SPEC §3.0.4). `BeginTurn` is written for one prompt per turn.

#### Invariant for the production inversion

`RunWithInfo` is re-expressed on the phases, `just
test` passes, and the in-process simulation reproduces its committed baselines
(`internal/scenarios/testdata/baselines/`) with no metric change. A baseline change means the split
altered behaviour.

**Open:** U8 — what `AbortTurn` does with an interrupted turn. Until decided, SPEC §4.3.3 binds: a
retracted prompt does not enter memory, and the rollback restores run-volatile state
(`internal/turn/rollback.go`).

### Start order and resume

- **The memory service is listening before opencode starts,** including opencode's managed
  background server, which sweeps and resumes interrupted executions at boot (RP §§2, 5.3).
- **Recovery turn.** The managed server resumes an interrupted turn with a synthetic continue
  message and a model step that has no prompt hook; the `context` hook still fires (RP §2). The
  service opens a recovery turn on a context call with no open turn, derives the turn's suffix from
  the hook input or stored message IDs rather than from process memory, composes the request and
  ingests the resumed reply (SPEC §1.6, §4.5.8 (10)).
- **Reconcile at service start.** After startup recovery (SPEC §4.5.8), the service diffs
  opencode's stored messages — the durable record of what the user saw — against memory's last
  committed turn, and re-ingests each turn opencode completed that memory rolled back, or records
  it as lost (RP §5.3, item 2). **Open:** U8 — whether reconcile skips turns rolled back by
  retraction; no record distinguishes a retraction rollback from a crash rollback.
- **opencode's data home never lives inside `~/.personant/`.** Crash recovery quarantines untracked
  files there, which would include opencode's database (see "Single spelunking location").
- **Restart persistence.** Only the working set reloads (`working-set.json`). History and the turn
  counter, which drives checkpoint cadence, reset on every relaunch, so after a restart the TUI
  shows the conversation while the model does not (RP §5.3, item 6). **Open:** U9 — whether
  in-run history and the turn counter are persisted, rebuilt from opencode's store, or left to the
  threads.
- **Resume cases not exercised** (RP §6): a graceful stop with a turn in flight, a standalone-kill
  claim later swept by a managed server, resume budget, background-job, subagent and shell
  recovery, and how a real model answers a resumed request.

### opencode database cleanup

opencode keeps one global SQLite database. Rows before the latest checkpoint are never read again
by the runner but stay on disk (SS §4.4; SPEC §1.7). The cleanup is not built. Its rules:

1. **Only when nothing uses the database:** every opencode process, the background service
   included, has exited. Otherwise the run is skipped; skipping costs nothing.
2. **Under an exclusive SQLite lock** taken before any change.
3. **Schema-version gated:** it refuses to run on a migration set it was not built for; tables
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
| `cmd/` | cobra subcommands: `init`, `index`, `verify`, `archive`, `version`; persistent flags `--home`, `--allow-newer-home`. Every verb that opens the substrate goes through the one `openHome` helper (format gate, then Reconcile); `version` is ungated and never fails on a broken home. |
| `internal/memops/` | the `MemoryOps` port: interface plus the domain types it trades in (`SpineRecord`, `Thread`, `ProjectMeta`, `Config`, the symbol enums, `Normalize`, `DominantSource`). The application layer depends only on this; it imports no internal package. |
| `internal/memops/fileadapter/` | the substrate-backed adapter: JSONL + markdown + YAML + TOML + go-git on `~/.personant/` |
| `internal/store/` | file substrate: paths, JSONL helpers, init, spine and project ops, thread I/O, excerpt folios, bootstrap. Depends on `memops` for the domain types. |
| `internal/autogit/` | go-git-backed autonomic git wrapper with bitflag systemic-validation policy |
| `internal/recovery/` | startup reconcile orchestrator over the on-disk observables |
| `internal/crashpoint/` | named kill-point hooks, test-armed and a no-op in production |
| `internal/eventlog/` | daily-rotated plain-text event log |
| `internal/dedup/` | content identifiers, write-time diffs, excerpt identifier lines and the composition plan; pure; depends only on go-git `plumbing` |
| `internal/turn/` | the §3.0 chain, the turn loop, per-turn coalescing, the staging buffer, the recall surface, the decay-triggered closure flow, and the phase entry points (`probe_phases.go`) |
| `internal/recall/scoring/` | pure Jaccard and cosine recall primitives, substrate-free; chunk tier, beam descent, summary-tree build |
| `internal/recall/measure/` | application-side recall stack: `Service`, `Recaller`, the single async indexer and its snapshot, `.recall-cache` sidecars, sleep-cycle tree builder |
| `internal/recall/exact/` | the exact lexical recall tier |
| `internal/curator/` | closure-summary drafting (SPEC §3.5, §5.2) |
| `internal/workset/` | layered context composition (E/A1/A2/B/C); a pure renderer |
| `internal/prompt/` | system-prompt template, topic-tag parser, stream filter |
| `internal/model/` | LLM client surface: OpenAI-compatible `HTTPClient`, scripted and generated `MockClient`, `MockServer` (the mock served as an OpenAI-compatible endpoint), SSE streaming |
| `internal/opencodehook/` | probe memory service: the loopback handlers the plugin calls, each driving one `internal/turn` phase |
| `opencode-plugin/` | probe plugin: forwards hooks and events, applies answers, per-RPC timeout and fallback count |
| `internal/scenarios/` | scenario harness, invariants, metrics; `sim/` the acceptance simulation; `opencode/` the opencode driver and executor |
| `internal/{metrics,clock,index,verify,version,log,testsupport}/` | supporting subsystems: metrics emission, the single clock, derived-file rebuild, structural validation, version identity, logging, live-endpoint test helpers |
| `justfile` | build, test and dependency-vetting recipes; the target inventory lives there |

**Open:** SPEC §4.1 keeps the `search`, `deps` and `log` verbs; `cmd/` has none of them.

---

## Applying the thesis (load-bearing)

THESIS states why Personant exists. This implementation applies two design rules at every layer:

> **Deterministic state as canonical. LLM in narrow judgment roles.**

When making a design decision, ask:

1. Could deterministic code do this without involving the LLM?
2. If the LLM is involved, is its role narrow and well-bounded?

| Tier | Role |
|---|---|
| Deterministic (Go service) | canonical state holder; integrity enforcer; build/query/index operations; autonomic git management of `~/.personant/` |
| LLM | narrow generative/judgment roles: topic tagging, summary drafting, recognition, dissection clustering, memory-vetting review |
| Harness (opencode) | the terminal UI, tools, permissions, providers and every interaction mode. Personant defines no interaction mode or workflow (SPEC §1). |

---

## Recurring patterns

### The model drafts, deterministic code writes

The LLM never directly mutates canonical state. It proposes content; deterministic code validates
it and writes it.

- **Closure:** the curator drafts the retirement summary; deterministic code writes the thread file
  and spine entry (SPEC §3.5).
- **Recall:** a deterministic match scores candidates; the fetch into Layer B is an explicit
  deterministic operation (SPEC §3.4).
- **Behavior and process memory:** every proposed change goes through one reviewed write path (see "Memory
  vetting").

A confirmation the user clears reflexively verifies nothing, so a routine closure and a recall
candidate above its auto threshold apply without asking. Each is recorded `ack=auto`, which keeps
the accept-rate and edit-rate measures counting only outcomes a user actually answered.

### Autonomic vs LLM-controlled

The service performs many operations the LLM cannot. Most notably **git**:

- The service owns `~/.personant/`'s git through `internal/autogit` (in-process go-git) at the
  dual-repo cadence (see "Crash stability and the dual-repo substrate"). No git hook: systemic
  checks `CheckDerivedFresh` (wraps `personant index check`) and `CheckSpineIntegrity` (wraps
  `personant verify`), parameterized only by paths, are passed per call as `GitCheckFlags`
  (`preFlags`/`postFlags`, 64 slots) and gate primary writes; daily commits are ungated.
  Operation-tied verifications live in purpose-specific wrappers composing `autogit.Checkout`.
- Commits are signed from the home repository's git config, falling back to a fixed identity
  (`store.CommitSignature`).
- The service reads the user's *workspace* repository only to identify the project: its root
  (`store.FindGitRoot`) and origin URL (`store.GetGitOriginURL`).
- Memory exposes no git tool for the home tree. The agent has opencode's shell, which can reach
  `~/.personant/`, and nothing blocks it; recovery treats a broken hand-edit as legitimate
  (quarantine, never delete). That the service is the only writer of the home tree is the intent.

### Single spelunking location

Personant's own state lives in three places:

- `~/.personant/` — memory: the canonical files and both git DBs. Forensic inspection is
  `cd ~/.personant && grep -r ...`.
- `~/.config/personant/` — Personant-only settings (`config.toml`, SPEC §8.2.2). Nothing here
  overlaps opencode's configuration. `internal/store/paths.go` still places `config.toml` and
  `providers.toml` under the home; moving them is ROADMAP.md work (install and config).
- opencode's data home — never inside `~/.personant/` (see "Start order and resume"); no Personant
  state, read by the database cleanup and reconcile.

**Open:** U5 — whether `providers.toml` and `api_keys/` exist and where they are placed (SPEC
§2.1, §8.2.1).

### No speculative prefetch

Every loaded thread is loaded because it was recognized as needed, never guessed.

**Open:** U3 — how a fetch is triggered under opencode: keep the in-band topic tag with a stream
abort-and-re-issue, or replace it with a recall tool. The in-band tag needs a spike (read the head
of the streaming response in the `session.http.response` hook, cancel upstream, re-issue, return
the second stream), per-protocol wire code in the plugin, and a way to strip the tag from the
stored transcript and the user-visible text, for which the service has no route. A recall tool is
a registered fetch tool whose result enters context at the next step, at the cost of one extra
model step per fetch, and rewrites SPEC §5.1/§5.5 and the tag-fidelity measures. The mid-turn
fetch does not fire under opencode today (PR §5). Until decided, the mechanism below binds:

Spine is always in window → the model recognizes a prior topic from spine entries → emits a topic
tag → the corresponding threads are fetched into Layer B → the response is generated. The model is
the recognition oracle, not a heuristic guess.

### Asymmetric cost: prove ahead of time

When the cost of being wrong is far higher than the cost of being right, the bar shifts toward
"prove it."

- The architectural thesis cannot be validated by inspection. The §9 measurement regime exists
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
the project-specific dependency rules, including the SQLite choice.

### Port-and-adapter at the substrate boundary

Application code (`internal/turn`, `internal/opencodehook`, `internal/recall/measure`, `cmd/*`,
scenarios harness) depends on `memops.MemoryOps` — the conceptual operations port — not on the
substrate directly. One adapter ships today (`internal/memops/fileadapter`: JSONL + markdown + YAML
frontmatter + TOML + go-git on `~/.personant/`); future adapters implement the same interface
without rewriting callers or tests.

Pure renderers (`internal/workset.Compose`, `internal/recall/scoring`) are **substrate-free**: they
import `memops`'s plain domain types (`SpineRecord`, `Budget`, `ThreadMeta`, ...) but never the
`MemoryOps` port itself, operating only on data the caller already fetched. What stays direct from
`internal/store` for the same reason: pure helpers (`Normalize`, `DominantSource`) and data-type
aliases (`ThreadFrontmatter`, ...). Tests inspect the substrate directly via `h.Paths` in invariants
— that's the test-side substrate validator pattern, not an application-code exception.

---

## Substrate non-negotiables

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`config.toml` under `~/.config/personant/`; `providers.toml`: Open, U5) + markdown directives | hand-editable; secret-bearing files separated |
| Derived files | regenerable from canonical | drift cannot accumulate; `autogit.CheckDerivedFresh` fails any state-changing git op on stale |
| Git management | autonomic on `~/.personant/`, never on user's workspace | the service owns its house; the user owns theirs |

### Substrate evolution: if/when scale forces it

The substrate non-negotiables above are constraints on the *canonical* layer — the source of truth
that humans inspect, git tracks, and `cat`/`grep` operate on. They do **not** preclude a fast
read-optimized **derived index** alongside the canonical, accessed through the same `MemoryOps` port
(an additional adapter or an internal optimization within the file adapter).

If the markdown/JSONL canonical substrate ever hits a critical speed ceiling — most plausibly under
long-rung simulation acceleration (especially inference-/embedding-in-loop runs), not interactive
use — the option to reach for is **[bbolt](https://github.com/etcd-io/bbolt)** as a derived KV
index, with the markdown-graph remaining canonical. Choice: bbolt (read-heavy B+ tree, pure Go,
etcd-proven), rebuildable via `MemoryOps.RegenerateDerivedState` and drift-checked like any derived
file. Rejected: Pebble (write-optimized), LMDB (CGo); SQLite only if queries become SQL-shaped.

This is **not** a planned upgrade. The asymmetric-cost discipline says: wait for simulation data
that shows substrate I/O dominates the sim's wall-clock per simulated month. The `MemoryOps` port
makes the eventual decision local to one adapter.

### Port abstraction policy

Design rules for the `memops` port surface:

1. **Default: substrate-agnostic naming and types.** Port types describe abstract concepts
   (per-thread metadata, project record, spine entry), not storage-format details. Names that would
   not make sense for an alternate substrate (SQL, bbolt, in-memory, etc.) are leaks.

2. **No ceremony for hypothetical substrates.** Don't pre-engineer abstractions, defensive
   interfaces, or migration scaffolds for substrates that don't exist.

3. **API surface is not frozen.** The port evolves when concrete evidence — a real second substrate,
   a discovered constraint — shows the current shape needs to change.

4. **Concessions are explicit, case-by-case, user-approved.** When a substrate-specific concept
   legitimately needs to live at the port (performance, debuggability, no abstract framing exists
   yet), it's an explicit exception: name the concession, state the rationale, state what would
   cause it to be revisited. Implicit concessions — a substrate term creeping in without
   deliberation — get cleaned up to the default.

---

## Storage and record formats

SPEC §2 states the outcomes: memory lives under one home, is plain text and is versioned in git.
This section holds the layout and record formats this implementation uses. `personant verify`
enforces the constraints marked below; `personant index rebuild` regenerates derived files.

### Home layout

```
$PERSONANT_HOME/                    # default ~/.personant; configurable
  spine.jsonl                       # canonical, sorted lexically by id
  symbols.jsonl                     # [derived] inverse symbol → threads index
  threads/thr_<id>/                 # one directory per thread
    thread.md                       # frontmatter (canonical) + title; no accumulating body
    turns/                          # warm turn excerpts, one file each (0000024.md)
    folio.jsonl                     # bound cold turn excerpts, one line each
    files.jsonl                     # tracked-file index (SPEC §3.9)
    files/                          # current blobs and kept diffs
  projects/prj_<n>/
    meta.json                       # project metadata (canonical)
    digest.json                     # [derived] Layer A2 digest
  directives/
    defaults.md                     # baked-in defaults (read-only after install)
    user.md                         # user-wide accrued overrides
    prj_<n>/project.md              # project-scoped directives
  logs/YYYY-MM-DD.log               # plain-text, append-only, daily rotation
  logs/archive/YYYY-MM.tar.gz       # rotated logs older than 90 days, gzipped
  version.toml                      # canonical, committed; `format = N` (SPEC §9.1)
  README.md                         # layout documentation for human inspection
  last-active                       # operational; prj_<n> of the most recently active project
  turn-journal.jsonl                # operational, gitignored; in-flight-turn journal (SPEC §4.5.8)
  op-in-progress.json               # operational, gitignored; batch-op marker, survives reset
  derived-watermark                 # operational, gitignored; daily HEAD the derived files match
  working-set.json                  # operational, gitignored; Layer B order + dormant set
  .recall-cache/                    # operational, gitignored; derived recall indexes
  archive/index.jsonl               # canonical; deep cold archive index (SPEC §3.8)
  recovery/quarantine/<stamp>/      # operational; byte-exact bytes preserved by recovery
  .git/                             # primary DB: permanent history, one day-grain commit per day
  .git-daily/                       # operational, gitignored: disposable per-turn DB, reborn daily
```

| Type | Examples | Drift policy |
|---|---|---|
| Canonical | `spine.jsonl`, `threads/*`, `directives/*.md`, `projects/*/meta.json`, `logs/*.log`, `version.toml`, `archive/index.jsonl` | Source of truth; hand-editable. `logs/*.log` is the append-only turn-grain forensic record that recovery preserves and re-appends. |
| Derived | `symbols.jsonl`, `projects/*/digest.json` | Regenerable from canonical; a state-changing git operation on the home tree fails while any derived file is stale; never hand-edited. |
| Operational | `.git/`, `.git-daily/`, `last-active`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `working-set.json`, `.recall-cache/`, `recovery/` | System-managed, not drift-checked. All but `.git/`, `recovery/` and the shipped files are gitignored; both git DBs are in the managed gitignore block so neither repo sees the other as untracked (SPEC §4.5.8 INV-6). |

### Spine record (`spine.jsonl`)

One JSON object per line, sorted by `id`, so diffs stay line-grain.

```typescript
interface SpineRecord {
  id: string;                        // /^thr_\d+$/; monotonic serial
  project: string;                   // /^prj_\d+$/; "prj_default" for unscoped threads
  anchors: string[];                 // [derived] 0..anchor.projection-max (8)
  anchors_projected_at_turn: number; // [derived] owner turn of last projection change (SPEC §3.2)
  summary: string;                   // 100-150 char gist, ≤ spine.entry-max-chars; at retirement
  description: string;               // triggering utterance; set once, never rewritten
  state: ThreadState;                // the SPEC §2.2.1 states, as their lowercase names
  created: string;                   // RFC3339
  last_engaged: string;              // RFC3339; read by closure decay, dormant order, digests
  state_changed: string;             // RFC3339
  turn_count: number;                // total turns this thread has been engaged in
  recall_fires: number;              // recall matches that led to a fetch; orders symbols.jsonl
}
```

`anchors_projected_at_turn` and `description` decode as 0 and empty on old records.

#### Constraints enforced by `personant verify`

| Field | Constraint |
|---|---|
| `id` | `/^thr_\d+$/`, globally unique |
| `project` | `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | 0 to `anchor.projection-max` inclusive. 0 is legal (a vague-start thread); verify flags only an over-ceiling count, a substrate bug, since the model never controls this count. |
| `anchors[i]` | normalized form; 3–50 chars |
| `summary.length` | ≤ `spine.entry-max-chars` |
| `state` | a defined state name |
| timestamps | RFC3339; `created` ≤ `last_engaged` |

**Display form** — generated from the record, never stored; what the model sees in working context:

```
thr_<id> [<anchors joined with ", ">] — <summary> [<state>]
```

Example: `thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict;
entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]`.

The human half of a thread's display — the working name a user reads and types back — is its
`description`, falling back to `summary`, falling back to the id. A rename rewrites that name and
nothing else: the id, the anchors, `history_symbols` and everything recall matches on are
untouched. The user-facing rendering is `thr_N: display — gist` (SPEC §3.4).

### Thread directory (`threads/thr_<id>/`)

- **`thread.md`** — YAML frontmatter (canonical for metadata) and a single `# <title>` line; no
  accumulating body, rewritten in full on every engagement.
- **`turns/`** — the warm tier: one terse excerpt file per primary-engagement turn, named by the
  thread's turn number zero-padded to 7 digits (`turns/0000024.md`). Appending a turn writes one
  small loose file; the turn path never writes the folio.
- **`folio.jsonl`** — the cold tier: the thread's bound excerpts, one JSONL line each (see
  "Excerpt tiers"). Absent when nothing is bound.
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
  - {raw: "(3,2)-torus knot", normalized: "3-2-torus-knot", first_seen_turn: 145, count: 4,
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

- **Assembly window vs retention.** The working-set assembler reads at most 512 (default;
  calibratable) of a thread's most recent excerpts, sized so that at `b-top-k = 3` each thread's
  window stays within LayerB/3 bytes; the body is read newest-first up to a byte budget, then
  joined oldest to newest. Excerpts are retained past the window, remain recall-indexed, and are
  the durable source the chunk-level index embeds for intra-thread recall. An excerpt is
  decision-class content; raw task-class output is never written as one (SPEC §3.10.6). Durable
  turn content lives only in excerpts, never the event log. Retention trimming is the deferred
  keep/toss layer (SPEC §3.10.8); v0.1 keeps all excerpts.
- **Excerpt tiers.** A thread's excerpts are split between loose files (`turns/`) and its folio so
  the tracked-file count does not grow with thread length (SPEC §2.3). Tiers change only in the
  day barrier's step B1b (`store.SettleExcerptTiers`, see "Crash stability and the dual-repo
  substrate").
  - *Folio format.* JSONL, one line per bound excerpt, keys in the order
    `{"name":"0000024.md","content":"<the file's exact bytes>"}`, sorted ascending by (turn number,
    name) with no repeated name, written and read through the generic `store.WriteJSONL` /
    `store.ReadJSONL` (atomic write, default HTML escaping as in every home JSONL file). A line is
    malformed if it fails to decode, its `name` is not an excerpt name, or `content` is absent.
  - *Placement rule.* A pure function of canonical state: the spine record's state and the
    thread's excerpt names. A retired thread (`memops.IsRetiredState`) binds every excerpt; any
    other thread keeps its newest `store.HotExcerptTail` (= `ThreadTurnWindow` +
    `memops.EmbeddingDebtCap` = 528, the assembly window plus the steady-state embedding-debt
    window) loose and binds the rest. An excerpt whose folio line would exceed `ReadJSONL`'s
    1 MiB line cap stays loose, and a name in both tiers with different bytes (a hand edit) moves
    in neither direction. Loose files can therefore sit permanently below folio entries, as can a
    hand-made one: nothing assumes the folio is a strict prefix of the loose files. Idleness and
    Layer B/C membership are not placement signals.
  - *Tier-transparent readers.* `ReadThreadBody`, `ReadDebtWindowExcerpts` and
    `ReadThreadExcerpts` return the union of both tiers, each name once, the loose copy winning
    for a name in both, byte-identical whichever tier holds it. A reader decodes the folio only
    when it needs more than min(loose count, `HotExcerptTail`) of the newest excerpts, so a warm
    thread after a barrier never opens it; `ReadThreadExcerpts` reads it whenever one exists. A
    read never moves an excerpt: a re-engaged retired thread reads its bound excerpts from the
    folio until the next barrier unbinds its hot tail. `store.ListExcerptEntries` and
    `store.ReadFolio` serve `personant verify`'s folio checks.
  - *UTF-8.* Excerpt content is valid UTF-8 (it arrives through JSON decoding). Bind asserts it
    before writing anything for the thread, since Go's JSON encoder would silently replace invalid
    bytes; a violation fails the barrier with `store.ErrExcerptNotUTF8` naming the file, never a
    skip or a repair.
- **Tracked-file store.** One index and its content files per thread, written at turn close
  for the owner's file events (`store.ApplyFileEvents`) and pruned at barrier step B1c
  (`store.PruneTrackedFiles`).
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
  - *Retention.* B1c keeps entries with `turn > TurnCount − ThreadTurnWindow` and the blob and
    `diff:true` files they name. It deletes every other file under `files/` and a legacy
    `files.json`, rewriting the index before deleting. The store per thread is bounded by the
    paths touched within the assembly window × (current size + the diff window).
  - *Identifier lines.* Each file event of a turn is one line in the owner's excerpt,
    between the header and `**user:**`. Composition never parses them; which positions
    expand comes from the index.
- **`description` vs `summary`.** `description` is a whitespace-collapsed copy of the spawning
  prompt, truncated to the new-thread summary length bound, set deterministically (no LLM
  paraphrase). `summary` is the curator's closure gist, in frontmatter only (never in a turn file);
  a live thread has no `summary`.
- **No parent thread.** No parent-thread field exists or is synthesized (the flat model, SPEC §3.2).
  Origin provenance is per symbol (`derived_from`); thread-level lineage is reserved.

```typescript
interface HistorySymbol {
  raw: string;                      // surface form as encountered
  normalized: string;               // normalized form (see "Symbol normalization")
  first_seen_turn: number;
  count: number;                    // total emissions across all turns
  source: SymbolSource;
  lifecycle?: SymbolLifecycle;      // canonical supersession state; omitted ≡ "active"
  ever_central?: boolean;           // latched true on first entry to the projection; never cleared
  last_active_turn?: number;        // last turn in the active projection; omitted/0 ≡ never-active
  derived_from?: string[];          // origin thread ids ("Symbol source"); omitted/empty ≡ organic
}
type SymbolSource = "deterministic" | "model" | "user" | "curator";
type SymbolLifecycle = "active" | "superseded";  // "evicted" is the absence of the entry
```

A record decoding without the lifecycle fields is well-formed (the zero values above);
`verify`/index rebuild regenerate the derived projection from canonical on first run.

`history_symbols` is capped at `history.cap-per-thread` (default 40) over active and superseded
entries together. Over the cap, eviction operates on the **evictable** partition only: a symbol is
evictable iff it is not `ever_central` and not a high-specificity identifier (URL, file path, git
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
  source_dominant: SymbolSource; // most common source across emissions
}
```

`threads` and `superseded_in` are sorted by descending `recall_fires` (most-recalled first).
`superseded_in` is rebuilt from the frontmatter `lifecycle`, the only source of supersession state.

### Project metadata

**`projects/prj_<n>/meta.json`** (canonical). Three identity layers: the internal handle `id`
(`prj_<n>`, never changes), the external identity `remote_urls[0]` (normalized git remote, gained
when the tree first acquires one) and the display name `name`.

```typescript
interface ProjectMeta {
  id: string;                        // /^prj_\d+$/
  name: string;                      // /^[a-z][a-z0-9-]*$/; mutable
  current_root_path: string;         // absolute
  historical_root_paths?: string[];  // appended on a root move; never rewritten
  remote_urls?: string[];            // normalized; primary = remote_urls[0]
  historical_remote_urls?: string[]; // de-duplication memory
  created: string;                   // RFC3339
  last_active: string;               // RFC3339; updated on any engagement
  thread_count: number;              // [derived] threads with project == id
  conventions_paths: string[];       // absolute paths to convention files loaded into Layer E
  symbol_patterns: ProjectPattern[]; // project-specific deterministic-pass patterns
  ignore_symbols: string[];          // project-scoped stopword additions
}
interface ProjectPattern { name: string; regex: string /* RE2 */; category: SymbolCategory; }
```

Remote URL normalization: lowercase host; strip trailing `.git`; `git@host:owner/name` and
`https://host/owner/name` are the same URL. `prj_default` is reserved for threads created before any
project was active: empty path fields, `name: default`.

**`projects/prj_<n>/digest.json`** ([derived], regenerated whenever any thread in the project
updates; Layer A2 content):

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

When several sources emit a symbol, `source_dominant` is the most frequent across its history
entries; ties break `curator > user > model > deterministic`. `source` records how a symbol was
emitted; `derived_from` records which thread it came from; the two are orthogonal.

**`derived_from` population** is deterministic (no LLM, no human). At each turn's history merge, for
every symbol emitted into the engaged thread: if that normalized symbol also belongs to a thread
currently resident in the recall-surfaced working set — promoted into Layer B by recall (turn-close
acceptance or the mid-turn fetch) and not yet evicted — that thread's id is unioned into the
symbol's `derived_from`. A turn-close acceptance attributes from the next turn, a mid-turn fetch
within the same turn. Eligibility ends when the parent is evicted. The set is monotonic. Recall does
not write symbols into a thread, and `derived_from` does not take part in matching. When a thread
is started by recalling and combining two or more threads, each carried symbol acquires that
parent's id.

### Symbol lifecycle and anchor projection

A thread's evolving identity lives in `history_symbols`; `anchors` is a deterministic re-derived
projection of it, with no LLM involved.

- `active` — currently in a top-`anchor.projection-max` slot (the zero value).
- `superseded` — once central, now ranked out. A label, not a candidacy gate: it competes on equal
  footing every turn. Retained, not evicted, so an abandoned premise remains a findable handle.
- *evicted* — the absence of the entry (capacity eviction above).

#### Projection

A pure function of current salience over all retained history symbols, active and
superseded alike. Rank by (1) class: a high-specificity identifier above an ordinary symbol of
equal count; (2) `count` descending; (3) recency: `last_active_turn` then `first_seen_turn`, newest
first; (4) `normalized` ascending. The top `anchor.projection-max` become `anchors`, in rank order;
none gives an empty list. `ever_central` is not a ranking tier; it only protects from eviction.

**Transitions** are labels read off the re-ranked top slice each turn, with no TTL. *Entry*: a
symbol in a slot this turn latches `ever_central`, sets `last_active_turn = turn` and is `active`
(also for a superseded symbol that ranks back in). *Exit*: a once-central symbol outside the slice
flips to `superseded`. A symbol is never evicted while `ever_central` as long as the protected set
fits the cap.

**Ordering invariant** (per turn, before the spine/frontmatter write): merge → project → evict, so
a symbol entering the projection has `ever_central` latched before the eviction predicate reads it.
`history_len` and `ever_central_count` staying flat over a run is a SPEC §9 sim obligation.

---

## The §3.0 hook chain (load-bearing primitive)

Personant treats the working window as a sequence of **context-modification events**, not
user-agent turns. The "turn" is a logging convenience; the load-bearing primitive is the per-event
hook chain. The principle: **no gaps**.

**Event taxonomy** (SPEC §3.0.1; the opencode mechanism behind each is in "Event observation"):

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `fs.read`, `fs.write`, `fs.commit`
- `thread.fetched` (**Open:** U3 — how a fetch is triggered under opencode)

Layer A2 digest content, memory-command effects and directive reloads are not chain deltas: they
change what the composer (`workset.Compose`) assembles.

**Every event runs the chain in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions; routing by
   retention class.
2. **Engagement signal** (§3.2) — coalesced per-turn (multi-tool-call turns don't multiply
   `turn_count`).
3. **Dedup decision** (§3.9) — buffered; identifier lines and the tracked-file store update at
   turn close; replacement at composition.
4. **Budget check** (§3.1) — bump-eviction so the budget is honored before the next event lands.
5. **Logging** (SPEC §2.8; see "Event log format").

Delta order and turn attribution: see "Ordering and correlation".

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)`
entry point. **No path mutates the working window without going through it.** Code review treats any
direct mutation as a bug.

Step 4 (budget check) is a documented no-op in v0.1 (`internal/turn/chain.go`,
`TODO(phase-2-budget)`); `TestBudgetCheckIsExplicitNoOpV01` trips if it is half-enabled without a
deliberate decision. Step 5 summarizes every task-class source to a metadata line (source, path,
byte count) instead of logging the body, so a newly added task-class source inherits the
minimization.

### Transient-data lifecycle (load-bearing for recall fidelity)

Symbol pollution by one-moment-of-value content (e.g., `# git status` output, search-result noise)
would silently degrade Jaccard recall: noise inflates the union, dilutes meaningful symbols, drives
spurious matches. The lifecycle prevents this at the source:

1. **Provisional class at delta time** (mechanical, by source: SPEC §3.10.1). `task` → symbols
   extracted into a cross-turn **staging buffer** keyed by normalized form. `decision` → symbols
   extracted directly into the per-turn coalesce buffer (the existing engagement path).
2. **Confirmation via decision-citation.** When a decision-class delta extracts a symbol whose
   normalized form matches a staged entry, **promote**: the staged entry migrates from staging into
   coalesce (where source-dominance merge folds it with the citing delta's contribution), removed
   from staging.
3. **Window-close GC.** At the top of each turn (after `TurnNumber++`, before any chain step),
   staged entries older than `stagingWindowTurns` (default 3) evict (`pruneStaging`). Their symbols
   never reach the persistent symbol index. The window is a compiled-in constant; a staging entry
   holds the normalized form, raw form, source, and the turn it was staged.

The principle: **raw task-class bytes are always discardable; only the symbols that crossed over
into a decision delta survive.** The canonical symbol index never sees transient pollution because
pollution is filtered at insert time, not removed retroactively. File content has a separate
persistent home in the per-thread tracked-file store (SPEC §3.9.1), which this lifecycle does not
govern.

---

## The layered working set

SPEC §3.1 states the outcomes: the token ceiling, the layer table and percentages, the live-turn
reserve, the four slot answers (budget, internal priority, overflow form, underusage), the lending
rule, the opencode-content slots and the tool-output cap. This section keeps the mechanism.

- **Derivation.** `context.token-budget` is a const (`memops.DefaultTokenCeiling`) plus a
  constructor override. The post-flight `usage.prompt_tokens` count is the real check; the byte
  shares drive pre-flight truncation. Of the §2.6.1 parameters only `closure.ack-mode` and
  `recall.ack-mode` are served by the directive layer (resolved at session start); the rest are
  compiled-in defaults.
- **Truncation.** Each rendered layer truncates to its byte budget at a UTF-8 rune boundary and
  appends the honest overflow marker. Layer C's dormant set is not count-capped: its byte budget
  truncates it at composition time (#127).
- **Eviction order under budget pressure:** C-oldest, then B-oldest (compressed to summary). A and
  E are never evicted.
- **Tool output.** `boundTaskResultDeltas` (`internal/turn/livebudget.go`) applies the cap to the
  delta, so the bounded content is what the request carries and what the working window holds.
- **File content.** `ComposeWorkingSet` loads each Layer B thread's `files.jsonl` and runs one
  pure `dedup.Plan` across them. The plan gives each path under the size gate a literal
  candidate at its most recent position. It gives diffs at the path's N most recent changes,
  minus those not kept or at or above `dedup.diff-literal-threshold` of the current size. A
  path whose current size is at or above `dedup.literal-share-threshold` × the per-thread share
  gets neither. Threads are walked in Layer B order and their excerpts newest-first. An excerpt
  is admitted only if its text and a size note per planned expansion fit; the newest is always
  admitted. Notes then become content while the share allows. A content id renders as a literal
  once: the first taken wins. Only content that renders is read. `workset` renders it inside
  its excerpt, and `ComposeWorkingSet` returns a structured report of what rendered.
- **Scaffolding.** Static prompt scaffolding (about 1.7 KB: preamble, topic-tag directive, recovery
  reminders, headers) is charged to no layer share and rides on top of the byte proxy.
  `TestA4PreflightAssembledBytesWithinBudget` bounds it (`Total + measured scaffolding`); a fixed
  deduction would zero out the tiny ceilings the acceptance tests exercise.
- **Prefix stability.** Fixed slots are composed first, so the request keeps a stable prefix across
  turns, which preserves the provider's prompt cache.

**Open:** per-slot answers and the `conventions_paths` overlap with the instructions slot (see
"Injection handling"; SPEC §2.5.1, §3.1).

Layers E and A are the recognition surface; Layer B is active engagement (fetch flow: "No
speculative prefetch").

### Names and recency state

Layer B order and the dormant set are the runtime's `ActiveThreads` and
`DormantThreads` lists (most-recently-engaged first), persisted each turn to
`<home>/working-set.json` (gitignored) so recency survives a relaunch. A second excerpt write for
one turn is refused with `ErrTurnAlreadyOwned`. The phantom-resolution owner fallback ranks tagged
ids by `last_engaged_turn`, a session-scoped counter that is not in the spine record; it is
deliberately not used for the no-tag owner-default, which ranks by Layer B order because the counter
inverts recency across a relaunch. Layer B order cannot rank the fallback's candidates: a tagged id
may sit outside Layer B, and a same-turn §5.5 fetch reorders the list by fetch order.

---

## Key abstractions

| Concept | Definition | Spec |
|---|---|---|
| **Spine record** | one line of `spine.jsonl`; canonical thread bookkeeping (id, project, anchors, summary, state, timestamps, turn_count, recall_fires) | "Storage and record formats" |
| **Thread** | contiguous run of turns united by working on one specific problem; identified by `thr_<n>` (stable forever); a directory holding `thread.md` (YAML frontmatter), serial turn excerpts and a tracked-file store | "Storage and record formats" |
| **Project** | three-layer identity: stable handle `prj_<n>`, mutable display name, optional canonical external identity (normalized git remote URL) | "Project metadata", SPEC §4.5 |
| **Anchor symbol** | `[derived]` 0..8 normalized symbols, projected from active `history_symbols` (see "Symbol extraction"); drive recall and surface in the spine line | "Symbol lifecycle and anchor projection" |
| **History symbol** | per-thread accumulated symbol (`raw`, `normalized`, `first_seen_turn`, `count`, `source`) plus a `lifecycle` (`active`/`superseded`) and an `ever_central` latch; the thread's evolving identity and the recall match set; capped per `history.cap-per-thread`, evicted with ever-central + high-specificity protection | "Storage and record formats" |
| **Symbol category** | `identifier` (preserve case), `entity` (lowercase + hyphenate), `tag` (lowercase, leading `#` stripped) | "Symbol normalization" |
| **Symbol source** | `deterministic` / `model` / `user` / `curator`; dominance: `curator > user > model > deterministic` | "Symbol source" |
| **Context-modification delta** | one event to the chain (§3.0); content-modifying event with source + content + metadata + retention class | SPEC §3.0 |
| **Retention class** | `task` (default-transient: `tool.result`, `user.shell-capture`, `fs.*`) vs `decision` (default-persistent: `user.prompt`, `model.response`, ...). Drives the two-stage transient-data lifecycle. | SPEC §3.0.1, §3.10.1 |
| **Slot** | one region of a composed request (a layer or an opencode-content slot) with a budget, internal priority, overflow form and underusage rule | SPEC §3.1 |
| **`MemoryOps` port** | the conceptual operations interface (`internal/memops`) the application layer depends on. One adapter today (`fileadapter`, JSONL + markdown + go-git); future adapters swap in without rewriting callers. | port-and-adapter |

---

## Mechanisms

### Thread lifecycle

A thread's life moves through five stages:

1. **Created** when the model emits `*new-topic*` in a topic tag, or by an explicit user act
   (**Open:** U4 — the command).
2. **Active** in Layer B while engaged (model tags it in per-turn topic list).
3. **Dormant** in Layer C as engagement decays (no longer in the model's recent topic tags).
4. **Retired** to Layer A (spine only) on closure, with full content preserved in
   `threads/thr_<n>/`.
5. **Archived** off-spine (deep cold) only if spine cardinality pressure builds — recoverable by
   explicit fetch through git.

The natural retirement boundary is "thread reached a resting point," not "context window pressure."
Resting points are recognized by engagement decay (Spec §3.5).

### Spine entries are the recognition surface

> The spine line is the load-bearing artifact. It must encode enough decision-state that the model
> can answer most "what was the outcome of X" questions from the line alone, without needing to
> fetch the thread file.

The anchor set is what makes recall possible; the summary is what makes recognition cheap. The
display form is in "Storage and record formats".

### Symbol extraction (three passes ordered cheapest-first)

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file
   paths, URLs, code symbols, project-configured patterns, user `#tags`). Free, reliable, narrow.
2. **Model-emitted** (every turn) — the topic tag's anchor list provides named-entity coverage the
   deterministic pass can't reach.
3. **Curator** (at retirement) — LLM drafts the closure summary (§3.5). A routine decayed thread
   takes the draft as written; an anchor-rich or long-engaged thread, or one whose draft failed, is
   queued as an exception for review (see "Closure and retirement"). Anchors are not curator-picked
   (below).

#### Anchors vs. history (the evolving-anchor model)

`history_symbols` is the canonical, accreting,
weighted per-thread set (capped at 40 by default) and the recall match surface. `anchors` is **not**
a separate storage tier — it is the deterministic top-`AnchorProjectionMax` (computed by
`ProjectAnchors`) **projection** of the thread's *active* history symbols, re-derived each owner
turn. `memops.AnchorProjectionMax` is a compiled constant (8), not a config key: the SPEC §2.6.1
`anchor.projection-max` directive is not read. A symbol that falls out of the projection flips to
**superseded** but is **retained, not evicted** — an abandoned premise stays a findable recall
handle, protected from capacity eviction by its `ever_central` latch. 0 anchors is legal (a
vague-start thread that hasn't accreted a headline yet). (Spec §3.4; "Symbol lifecycle and anchor
projection".)

### Topic-tag mechanics

**Open:** U3 — the in-band topic tag under opencode, including stripping it from the stored
transcript (see "No speculative prefetch"). Until decided, the mechanics below bind.

The tag prompt is a compiled constant in `internal/prompt/template.go` (`TopicTagDirective`); the
two recovery reminders (`TopicTagReminder`, `EmptyResponseReminder`) live in the same file, and
changing any of them is a rebuild. The owner of a multi-existing tag is the lowest `thr_N` because
the coalesce buffer is a map and does not preserve tag order. The bare `*new-topic* [...]` alias is
accepted by the bounded preamble scan (so the §5.5 fetch timing, the missing-tag trigger and the
stream-filter suppression agree) and by the stream filter's Close-time trailing-line fallback; an
all-whitespace response longer than that scan resolves as tag-less rather than empty, so it fires
the missing-tag reminder. `fs.read` deltas buffer a write entry like `fs.write`, so a tag-less
read-only turn pays the full missing-tag recovery; whether such a turn should count as
conversational is an open calibration question.

### Recall mechanisms (four, layered by cost)

1. **Model-native recognition** (default; cheapest). The spine is in window; the model recognizes
   prior topics directly when a turn engages them. Emits topic tag at turn start. Handles synonyms
   and paraphrases for free since the model is the recognition oracle.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols match a retired
   thread's anchors above threshold, the thread is brought forward by band (SPEC §3.4): a candidate
   at or above its tier's auto threshold is fetched with one visible line and no question; the band
   between the surface and auto thresholds is asked: *"We talked about X 2 days ago — pick up
   there?"*; nothing below surfaces (rationale: "The model drafts, deterministic code writes"). With
   no channel to the user, recall stays log-only.
   **Open:** U4 — the channel for the auto line and the ask offer. Under opencode a plugin has a
   yes/no prompt and no pick-list, so a multi-candidate offer cannot be a numbers/all/none list.
3. **Cross-project recall** (Layer A2 → fetch). Per-project anchor digests in A2 are scanned each
   turn. Same opportunistic mechanism as (2), slightly higher threshold.
4. **Intra-thread recall of scrolled-out early content** (#109/#111). For the engaged long-running
   thread, the fine-tier chunk embedding index recalls early turns that have scrolled out of the
   FIFO assembly window. The engaged thread bypasses the coarse gate and runs directly against its
   own indexed chunks. On threads long enough to have a built summary tree, an O(log n) beam descent
   (`scoring.DescendChunks`) replaces the flat O(C_main) scan — the sleep cycle builds and maintains
   the per-thread summary hierarchy (`measure.RebuildTrees`); absent or stale trees fall back to the
   flat scan with no correctness loss (W8). The fine tier is filled asynchronously, so it lags
   scroll-out by up to the embedding-debt cap; the recall-completeness invariant (SPEC §3.4) forbids
   a dead zone there, so the automatic intra path also runs a **bounded lexical pass** over exactly
   that debt window (`exact.MatchExcerptsBySymbols` over `MemoryOps.LoadDebtWindowExcerpts`, ≤ the
   cap, query-symbol match) and unions the hits (#123).

   The index is maintained by a single background indexer that swaps an `atomic.Pointer` snapshot,
   so recall reads never block; a per-thread turn-count watermark stops a stale in-flight embed from
   overwriting a fresher vector, and a flush arriving at an *equal* watermark (the same-turn
   cap-flush plus dormancy-flush double enqueue) always publishes. The lexical floor scans `(1 +
   pending) × cap` excerpts: `cap` is the embedding-debt cap (16) and `pending` counts
   enqueued-but-unpublished flush jobs — 0 at steady state and at most 258 at the v0.1 queue depth
   of 256 (queue plus one in process plus one blocked enqueue). Only a genuinely failed flush
   retires a pending job without publishing. Persisted vectors and summary trees are `.vec`/`.tree`
   sidecars under `<home>/.recall-cache/` (`internal/recall/measure/veccache.go`, `treecache.go`).

**Decline categorization** is an input to directive accrual (cross-project aggregate only; see
"Personalization through directive accrual") so that a bad match does not suppress good offers. SPEC §4.3 holds the reason enum and its effects; how a reason is captured waits on U4.

#### Guarantee boundary — findability, not reminiscence

The recall stack guarantees that durable
content is *findable* (the §3.4 completeness invariant) and that overlapping context *surfaces
opportunistically* (the layers above). It deliberately does not promise spontaneous cross-domain
reminiscence: a connection between today's problem and years-old work in a different vocabulary
surfaces only if anchors or embeddings overlap. A feature that tries to make the agent volunteer
such a connection ("this reminds me of…") is out of design. State this boundary when setting user
expectations; do not "fix" it.

### Closure and retirement

Triggers:

- Engagement decay: no engagement for `engagement.decay-turns` OR `engagement.decay-time`
  wall-clock (either threshold; OR semantics).
- An explicit close request (`/done`; **Open:** U4 — the command).

Flow (SPEC §3.5): curator drafts retirement summary → the candidate is classified. A **routine**
closure is applied without asking, with one visible line and an `ack=auto` event. An **exception**
(anchor-rich, long-engaged, or a failed or empty draft) is queued silently. The queue is derived,
never stored — a decay-eligible thread that classified as an exception — so it is crash-safe and
correct across restarts. A drained closure writes the thread file, generates the spine entry, and
Layer B/C eviction follows (rationale for auto-applying routine closures: "The model drafts,
deterministic code writes").

**Open:** U4 — the channel and the boundary for the exception drain, the `[e]dit` choice, `always`
mode and the pending-queue announcement. An opencode session never exits, so the clean-exit
boundary the drain used has no counterpart.

Deep cold archival (implemented; `internal/memops/fileadapter/fileadapter_archive.go`,
`cmd/archive.go` `archive list`/`archive recover`): when spine cardinality pressure builds, the
runtime archives oldest retired threads off-spine via `git rm` + `git commit` + an
`archive/index.jsonl` entry. (Spec §3.8.)

A batch meets SPEC §3.8.1's three results as follows: each `threads/thr_<n>/` is confirmed tracked
at HEAD before its tree hash is taken; the capture commit is `Add(".")` of the full worktree into
primary and precedes the membership append; removals are staged by `os.RemoveAll` plus
`autogit.Add(".")`, and `RemoveSpineRecords` performs the single spine rewrite; the deletion commit
runs `CheckSpineIntegrity` before and `CheckDerivedFresh` after. Recovery (`RecoverThread`,
`autogit.CheckoutTree`) refuses with `ErrArchiveEntryNotFound` or `ErrArchiveIntegrity` and never
writes a partial thread. `RecoverThread` and `ArchiveThreads` remain public port operations; a
standalone `ArchiveThreads` is crash-recoverable through the same roll-forward.

### Fallback dissection (worst-case path)

Designed, not built: no package implements the dissector.

When budget pressure exceeds threshold AND normal retirement isn't catching up:

- Dissector LLM clusters the oldest poorly-threaded content into semantically related groups.
- Each cluster produces a thread file + spine entry, identical in shape to a normal retirement.
- Each cluster resolves through the closure path (**Open:** U4 — the channel for its interactive
  step).

Handles three failure modes with one mechanism: wandering user, single-thread overrun,
never-cleanly-threaded content. **Quality is best-effort by design** — graceful degradation under
stress, not catastrophic loss. (Spec §3.6.)

### Multi-project as lifelong accrual

One unified spine across all projects. Every thread carries a `project` tag. Layer A1 = current
project's full spine; Layer A2 = compressed cross-project digests. Cross-project recall is designed
(Recall mechanism 3), not emergent. The architectural commitment: **one entity, many projects,
lifelong accrual**. (See "Project metadata"; Spec §4.5.)

### Personalization through directive accrual

Behavior tuning lives in inspectable directive files that accrue from feedback signals:

- `directives/defaults.md` — system defaults, baked in.
- `directives/user.md` — user-wide overrides. By default, accrual proposes a change here only from
  patterns aggregated across projects (explicit instructions, decline categorizations and accept/decline
  statistics are inputs to the aggregate, not single-project writers).
- `directives/prj_<n>/*.md` — project-specific tuning.

By default a pattern seen in one project does not reach `user.md` or the defaults; it becomes a
process-memory proposal or career memory. The user's policy can change this routing (SPEC §1.8). Process-memory directive files do not exist yet (SPEC §1.8
`Open:`).

**The user can read what the agent learned and correct it.** Every accrual proposal goes through
"Memory vetting", the same review and acceptance path as any other. (Spec §1.8, §2.6.)

**Open:** how cross-project aggregation is computed (what counts as a pattern, how many projects,
what threshold); no aggregator exists, and the per-project statistics the recall loop collects
(`directive.accrual-update`, reserved) feed none of these files yet.

### Memory vetting

Not built; outcomes and open items: SPEC §1.8.

Mechanism: behavior- and process-memory writes pass through the `MemoryOps` port, so the review stage sits at
one port operation. The port has no directive-write operation today (`DirectiveParam` only reads).
Career-memory protection uses the slots, the dual-repo store and the consolidation cycle (see
"The layered working set", "Crash stability and the dual-repo substrate").

**Open:** which package holds the review stage and its provenance record.

### Crash stability and the dual-repo substrate

The service can be hard-killed (SIGKILL, power loss) at any instant, so recovery is designed as a
**deterministic cold-start reconciliation** — no signal handler doing teardown work, no LLM in the
loop, no user acknowledgement. On-disk state alone determines the outcome. The load-bearing
property: **the worktree is the truth; git is a recovery-point index over it.** No canonical byte
ever lives *only* in git, so any git DB can be recreated from the worktree and losing a DB is never
losing content. (Spec §4.5.8, #94.)

Two git DBs track the one worktree, daily per-turn and disposable, primary day-grain and permanent
(cadence, cost bar and ≤1-turn durability: SPEC §3.11). The in-flight turn's raw
`(prompt, response)` bytes are protected by a durable append journal written *before* any canonical
write, so even the one at-risk turn's content survives.

Recovery reduces to a small state machine over on-disk observables (marker file, in-flight journal,
daily/primary HEAD trailers, watermark, daily-DB presence) that classifies into one cell and repairs
idempotently — a torn turn resets **only daily** to ≤1-turn-ago; a barrier or archival crash **rolls
forward** (never resets the worktree, which would destroy up to a day); a broken hand-edit is
**quarantined byte-exact, never deleted**. Ruled out permanently: loss-bar extension (commit
batching / ≤N-turn windows — ≤1 turn is the bar), daily-DB durability guarantees (disposable by
construction), and primary auto-recreation (a missing primary is refuse-to-open, not a benign
recreate).

Under opencode the bar is judged against turns the user saw complete (SPEC §4.5.8 (10)): a turn
opencode recorded as terminal that memory rolled back is lost until reconcile re-ingests it (see
"Start order and resume").

Mechanism names. A turn is two journal appends (`JournalTurn`, kinds `prompt` and `response`)
followed by `CommitTurn`, which stages only the recorded write set through `autogit.AddPaths` with
no integrity flags; `store.JournalOwner` reads the turn id from the journal's first record, and a
legacy `op=turn` marker file is still read. Core recovery (`internal/recovery`) only detects barrier
and archival cells and returns a typed `RecoveryReport.Pending` (`PendingCompletion{Kind, Day}`,
plus `ScopedRestored` for quarantined-and-restored paths); `fileadapter.Reconcile`, which owns
`ArchiveThreads` and both repo handles, completes them, which keeps `recovery` from importing the
adapter. `recovery.LocateDeletionCommit` (`internal/recovery/stamp.go`) finds an already-landed
deletion commit by a child-of-`ParentCommitHash` tree test; `store.TruncateJournal` carries the
truncate-no-fsync proof (`TestCell5_ResurrectedTruncatedJournal`). `store.Init` births `.git-daily`
only alongside a fresh primary, so a missing daily on an existing home keeps its legacy-upgrade
shape. The barrier mints its day-commit with `AllowEmptyCommits` after `CheckSpineIntegrity` on the
staged tree, asserts `CheckDerivedFresh` at B6, and derives lifecycle tags through
`autogit.TagStamp` / `ParseTagStamp` (the only formatters), reading the sealed day's log with the
tolerant `eventlog` reader and tolerating `ErrTagExists`. Barrier step B1b
(`settleExcerptTiers`, between B1's drain and B2) calls `store.SettleExcerptTiers` per spine
record in ascending id order with `memops.IsRetiredState` as the coldness signal; completion
re-runs it after the drain while the day-commit has not landed. Per thread it writes each
unbound excerpt loose and fsyncs `turns/` and the thread directory, then writes the folio
(`WriteFileAtomic`, which does not fsync the parent) and fsyncs the thread directory, reads it
back, and only then removes the bound loose files — so every excerpt is readable from at least
one tier at every crash point. Convergence: a name in both tiers with identical bytes is an
unfinished move and settle keeps the target-tier copy; different bytes are a hand edit, and
neither copy moves (`folio-skip reason=conflict`). A torn folio temp file (`tmp-folio.jsonl`) is
quarantined by recovery's unconditional `tmp-` residue sweep. A hand-broken folio is skipped by settle (`reason=unreadable`) and fails
B2's verify gate on the live run, marker kept; the next open's completion runs
`spineQuarantineRepair`, quarantining the folio byte-exact and scoped-restoring it from primary
HEAD (or removing it as an orphan add), which is consistent with the thread's loose files because
tiers change only at a barrier. Barrier step B1c (`pruneTrackedFiles`, between B1b and B2) calls
`store.PruneTrackedFiles` per
spine record, in spine order, with the record's `TurnCount`. It rewrites a thread's
`files.jsonl` only if an entry drops, then deletes the files no kept entry names, so a crash
leaves orphans, never a dangling index. Completion re-runs it under the same condition as B1b.
An unreadable index is skipped (`dedup.prune-skip reason=unreadable`) and fails B2's verify
gate. Any tracked-file finding makes completion's `spineQuarantineRepair` quarantine the
thread's `files.jsonl` and `files/` together and restore both from primary HEAD. Crashpoints:
the four `folio.settle.*` and `barrier.prune.postIndex` in `store`, and
`barrier.postSettle.preDayCommit` and `barrier.postPrune.preDayCommit`, all walked by
`TestBarrier_CrashWalk`; and `fs.applyEvents.preIndex` in `store`, between a turn's tracked-file
writes and its index write, walked by `TestCrashMatrix_WTurnFileEvents`. Recovery handles and plumbs the
`op=rebaseline` marker, but the mid-day arming trigger is not wired (ROADMAP.md). Packing: after
every `gcCheckEveryCommits` (64) daily commits the adapter reads daily's loose-object count and runs
gc at `gcLooseObjectThreshold` (5000); `MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`)
packs both repos on the day-off idle window. Session checkpoints target daily with a full `Add(".")`
sweep. The open sequence runs `memops.GateHomeFormat` before Reconcile;
`version.UnversionedHomeFormat` is pinned at 1 forever and is never an alias of
`version.CurrentHomeFormat`, so an unmigrated legacy home keeps resolving to format 1.
`GateHomeFormat` migrates an older home by walking the registry `homeFormatMigrations` (steps
keyed by from-format; 1→2 and 2→3 restamp only: format 2 adds optional folios, format 3 the
tracked-file store, whose legacy `files.json` B1c removes), the
caller writes the stamp before Reconcile, and a crash between stamp and log leaves a current-format
home that needs nothing further.

### Memory consolidation — the "sleep" cycle

Designed, partially built. The day-off idle window already fires `MemoryOps.Consolidate` (git
gc/repack on both git DBs, SPEC §3.11) and the within-thread summary-tree rebuild
(`measure.RebuildTrees`, #111). The fuller offline-reorganization vision — re-packing fragmented
structures, compacting the spine, advancing archival, and the SPEC §3.10.8 content-retention
keep/toss precision layer, which cites this section by name — is designed, not built. Full design
capture: **ROADMAP.md → Far-horizon, "Memory consolidation."**

### Serving deployment

opencode makes the provider call, so the service sees no provider response and receives none of the
extended inference-timing block that `model.inference-telemetry` logs (`ttft_ms`, `prefill_ms`,
`gen_ms`, `total_ms`, tokens/second and token counts; see "Event log format"). Token counts reach
the service through opencode's step and usage events (`session.step.ended`); no source for the
timing fields is established.

**Open:** the source of the inference-timing block under opencode; the event is retired from the
vocabulary if there is none.

The local provider is **oMLX**, an OpenAI-compatible endpoint, configured in opencode's own
provider settings.

**Open:** U5 — how the service reaches its own models (the curator, the dissector, the embedder and
recall judgment). (a) Through the plugin's `experimental.generate` over opencode's providers, which
leaves no overlap; the embedder still needs Personant configuration because opencode has no
embedding provider. (b) Through a Personant provider pool under `~/.config/personant/`, which must
not name an endpoint opencode's providers also name.

### Model-family as platform

**Open:** U10 — opencode lets the user run any model. Either one family is tuned for Personant's
memory prompts, with a warning on others, or every model opencode runs is supported and the tag and
curator quality are measured per family (which grows the eval surface; a recall tool in place of
the topic tag, U3, shrinks it). Until decided, the Gemma family, served locally via the `reaper`
provider, is the tuning target for the memory prompts.

Adding a model family is per-family engineering, not configuration: LLM families share no
deterministic substrate to abstract over, and cross-family mismatches fail silently and
statistically, accumulating misclassified threads before recall measurably drops. Each supported
family carries (1) base intent prompts, (2) family-specific prompts, (3) a per-family eval suite.

#### Architectural mechanism

Keep the model-family surface narrow and isolated: the memory prompts
(`internal/prompt`, `internal/curator`), output-stream interpretation and embedding model
assumptions. Everything else (substrate, port boundaries, recall scoring, dedup, autonomic git) is
model-agnostic and survives substrate migration cleanly.

#### Two context figures

The default `context.token-budget` (200000, SPEC §2.6.1) is the ceiling
composition enforces. 256K is the model window the sim and working-set sizing assume, so the
ceiling sits below it. The window figure affects Layer B byte budgets, closure pressure, recall
query depth, and the mock-client's need to simulate token counts for budget-exhaustion testing.

### Minimize infrastructural prompts

> **Every infrastructural prompt is a tax paid forever. Every deterministic algorithm is a fixed
> cost paid once.**

The LLM is non-deterministic, costly, slow, opaque, and — because of the
[model-family-as-platform](#model-family-as-platform) principle — every
infrastructural prompt multiplies: each prompt requires re-tuning per supported family.

The corollary: **even within LLM tasks, keep prompts narrow.** Constrain the input space; constrain
the output space; validate output deterministically before trusting it. A broadened prompt expands
both the family-tuning surface and the silent-drift surface.
### Concurrent sessions

Designed, not built: multiple live opencode sessions open against one shared memory — multitasking
one career. The plugin route makes it reachable (the service is already the single writer).
Distinguished from multi-user (v2.0, locked): concurrent sessions interleave on *shared* canonical
state. Full design capture: **ROADMAP.md → "Concurrent sessions."**

**Open:** two concurrent opencode sessions against one memory (SPEC §1.1). One consequence already
visible: `session.started active=` is logged per service run, but the active project is per
opencode session.

---

## Project identity (dual-layered)

| Layer | Form | Mutable? | Purpose |
|---|---|---|---|
| Internal handle | `prj_<n>` | no — assigned at creation | storage key (`projects/prj_<n>/`); spine reference |
| External canonical identity | normalized git remote URL | yes (rare) | "is this the same project I've seen before?" — survives file moves and renames |
| Display name | user-chosen string | yes (`/project rename`) | UI rendering, command targeting |

**Bootstrap waterfall** when an opencode session starts (SPEC §4.5.7). Its input is opencode's
session directory:

1. Git remote of the session directory (or an ancestor) → known project's `remote_urls` /
   `historical_remote_urls`.
2. Session directory path → known project's `current_root_path` / `historical_root_paths`.
3. `last-active` confirmation.
4. Final fallback: create / switch / no project (`prj_default`).

**Open:** U4 — the channel for the interactive steps (the last-active confirmation, the final
fallback, the remote-adoption and collision prompts, the file-move prompt) and the command for an
explicit selection.

When a project gains a remote URL after creation (local-only → published), the service detects it
and adopts it as canonical identity (SPEC §4.5.4; the adoption prompt waits on U4).

---

## Testing as the lab bench

SPEC §9 is the measurement instrument by which the architectural thesis gets proven empirically,
and the substrate by which the techniques evolve iteratively.

The simulation harness (`internal/scenarios/`, including `internal/scenarios/sim/`) is a
load-bearing deliverable under production discipline: one owner per value, key and contract; named
constants; designed in these
documents before coding; changed through CONVENTIONS review. A seam that silently fails (renamed
metric key, drifted log marker, second clock path) passes its gate vacuously, and the v0.1 gate is
only as sound as the harness.

### Harness ↔ system-under-test coupling rule

The harness imports the system's *public contract
surface*, never its *implementation detail*. The discriminator: **would a production consumer
reasonably expect access to this value?** Yes → it is contract; export it if needed and reference
the public symbol (never mirror it). No → it is internal; the harness observes its *effect* through
a public API/predicate, or tolerates the imprecision — it never imports or copies the internal
value. (A shadow oracle still re-implements the system's *logic* independently — importing a public
*scalar parameter* like the excerpt assembly window is not an independence breach; importing or
mirroring an *internal* one like the embedding flush-batching cap is.)

**Layers** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock LLM via canned responses; metrics emission
   per scenario.
3. **Invariant validators** — callable from any test (`invariants.go`).
4. **Churn tests** — randomized seeded sequences; invariants after every operation.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating
   points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Acceptance simulation harness** — synthetic workload, logical-clock acceleration, steady-state
   assertions; the top rung is 120 days / four months (§9.1).

v0.1 acceptance is the SPEC §9.1 gate, run as a converging loop, not a one-shot pass.

When making technique changes (parameter tweaks, algorithm adjustments), the question is always
"what does the simulation say?" — not "does it compile and pass invariants?" Bake metrics emission
into new code from day one; bolting it on later is much more expensive.

### Simulation-harness structure

#### File layout — `internal/scenarios/`

| File | Responsibility |
|---|---|
| `harness.go` | Core types: `Step`, `Scenario`, `Harness`, `StepSource`, `StepFeedback`, `RecallAck`, `ClosureAck`, `RecallFidelityMode`. Narrow optional interfaces (`threadIndexer`, `cacheSweeper`, `treeRebuilder`, `intraDivergenceProbe`, `cosineOpsReporter`). The `sliceSource` shim that adapts a fixed `[]Step` to `StepSource`. `SimClockStart`, `SimDayLength`, `SimWorkdayStart`, `SimDayIndex`. |
| `harness_setup.go` | `newHarness` (per-scenario isolation, clock default, telemetry wiring) and `scriptedCurator`. |
| `harness_run.go` | `RunScenario`, `runStep` (the seven named phase helpers: `stepSetup` / `stepSetClock` / `stepExecTurn` / `stepScrapeEventLog` / `stepMeasureRecall` / `stepRecordMetrics` / `stepCloseDays`), `restartSession`, and the final-gauge helpers (`foldEventLines`, `peakHistorySymbols`). |
| `harness_resolvers.go` | Per-step resolver builders (`recallResolverFor`, `closureResolverFor`) and the invariant-cadence policy (`perStepInvariants`, `runInvariants`). |
| `recall_fidelity.go` | Recall-fidelity measurement (`recordRecallFidelity`), the incremental log-tailer (`logTailer`), and `classifyRecoverability`. |
| `invariants.go` | The invariant validator set (spine integrity, index drift, project refs, last-active validity, frontmatter-spine sync, engagement consistency, thread accounting, closed-thread consistency, archive resolvability, dedup consistency, budget overflow: the `Verify*` functions). `DefaultInvariants` = `cheapDefaultInvariants` (per-step: `VerifyLastActiveValid`, `VerifyNoBudgetOverflow`) + `heavyDefaultInvariants` (substrate-scale, day-cadenced on the sim). |
| `metric_keys.go` | Cross-seam metric-key registry (see "State-ownership map" below). |
| `sim/workload.go` | On-demand workload generator (`generator`, `GenerateWorkload`, `runWorkDay`/`runSession`/`runDayOff`). |
| `sim/campaign.go`, `sim/wander.go`, `sim/intra.go` | Specialized step-sequence builders for the campaign, within-thread wander, and intra-thread recall workload variants. |
| `sim/telemetry.go` | Per-day snapshot emission and the sim daily-record schema. |
| `sim/sim_test.go`, `sim/sim_rung_test.go` | `TestSim` (acceptance ladder entry point) and rung-by-rung regression guards. |
| `opencode/` | the opencode driver and executor (see "opencode-in-loop harness"). |

**State-ownership map — single source for every mutable harness value**

The "silent lie" failure mode (above) comes from two independent sources claiming the same value.
Each of the following is owned in exactly one place:

| Value | Owner | Rule |
|---|---|---|
| Simulated clock | `Harness.pinnedClock` — a **slaved copy** of `Step.At`. `stepSetClock` does `pinnedClock = Step.At`, never integrates a delta. One clock advance path; see SPEC §9.4.1 for the single-clock rationale. | Never set `pinnedClock` from a delta except as described for the execution-time refinement step (which normalises `At = pinnedClock + TimeDelta` before the set). |
| Day index | Derived: `SimDayIndex(pinnedClock)`. Never a parallel counter. | Any code that needs "which sim-day is this?" calls `SimDayIndex(h.pinnedClock)`. |
| Generator exec clock | `generator.execClock` — an exact generator-side **mirror** of `pinnedClock`, maintained by `reconcileAt`: every emitted step's `At` is re-anchored forward to it when execution-time refinement turns advanced the clock past the step's planned instant; an `At`-less refinement advances it by `TimeDelta`, mirroring the harness normalize. | The harness never reads it; it exists so the fix lives at the source (the generator) and `stepSetClock`'s non-monotonic warn tripwire stays a pure canary — mirror-drift is exactly what trips it, so the canary stays armed. |
| Cross-seam metric keys | `metric_keys.go` — the single const block. Harness writes them; sim reads them. | A key rename is one edit; a compile error if a reference is missed. Keys owned entirely inside `sim/` are NOT mirrored here. |
| Crash-scenario coverage | `crashScenarioCoverage` (`crash_matrix_test.go`) — the single greppable registry mapping each registered `crashpoint` name to its scenario. `TestCrashPointCoverageGate` enumerates `crashpoint.RegisteredNames()` against it. | A registered crashpoint with no scenario **fails the suite** — the mechanical gate that stops silent kill-point rot. The pre-crash `TurnOracle` is a local immutable snapshot, not shared mutable state, so it is deliberately not mapped here. |
| Archived thread set | `Harness.archivedThreadIDs` — **index-derived** via `refreshArchivedSet` from the canonical archive index (`store.LoadArchiveIndex`, entries with `RecoveredAt == ""`), reassigned wholesale; refreshed when a tailed batch carries an archive/recovery line (the change trigger, not the source of truth). | The archival-forgiveness filter in recall fidelity and `VerifyThreadAccounting` both read this set; a recovered thread drops out the instant its index entry is stamped, and a log-emission gap can no longer under-count it. |
| Created thread set | `Harness.createdThreadIDs` — folded incrementally from tailed `thread.created` log lines. | Same pattern as above. |
| W1 divergence tally | Accumulated directly in `runStep` (not read from `measure.Service` atomics post-run), so the tally survives `RestartSession`-driven Service churn. | A restart installs a fresh Service whose atomics start at zero; reading post-run would lose probes from prior sessions. |
| Layer-B shadow | Generator-owned. Cross-checked each step against `StepFeedback.RuntimeLayerB` in two directions, both ==0 gates on mock and live-embedding rungs and report-only under live inference: forward (`layerb_shadow_divergence`, runtime-resident absent from the shadow: LRU agreement) and reverse (`layerb_shadow_reverse_divergence`, checked against the runtime-mirror `rtActive`, which also replicates measurement-carrier displacement and §3.5 turn-idle decay closure). The declared exception to SPEC §9.4's event-log-only recall scoring. | The shadow LRU is the generator's oracle for expected-match sets; a divergence would corrupt recall measurements. |

#### Generator ↔ harness protocol

The `StepSource` interface is the only coupling between the on-demand workload generator and the
harness drive loop. The protocol is:

1. The generator emits `Step` structs carrying an **absolute `Step.At`** (the step's position on the
   simulated clock, derived from `SimClockStart`). This field is mandatory for sim steps;
   `stepSetClock` sets `pinnedClock = Step.At`.
2. After each turn, the harness passes `StepFeedback` back into `StepSource.Next`. `StepFeedback`
   carries: `RecallMatchFireIDs` / `EmbedMatchFireIDs` / `IntraMatchFireIDs` (what the runtime
   surfaced), `RuntimeLayerB` (the authoritative Layer-B membership for shadow cross-check),
   `TargetRecoverable` (archival-forgiveness predicate for probe scoring), and
   `RecallExpectedForgiven` (the forgiven expected count the hit/miss counter must use).
3. The **log-marker contract**: the harness scrapes specific runtime log strings
   (`spine.match-fire`, `spine.embed-match-fire`, `spine.intra-match-fire`, `thread.created`,
   `archive.archived`) as the measurement signal; these strings are pinned by contract tests so a
   rename breaks a test, not a measurement.

#### Acceptance-run mechanics

- `internal/scenarios/sim/` holds the acceptance simulation (`TestSim`, the recall-madlibs
  corpus-slot workload); `just sim DURATION=<N>d` runs a chosen span (aliases `1d`/`1w`, Go duration
  forms such as `168h`) and `just test` runs the default `1d` rung. Metrics come from
  `internal/metrics`; scenario steps carry `ExpectedRecallMatches` ground truth scored in
  `RecallStrict` or `RecallMeasureOnly` mode, with baselines under
  `testdata/baselines/<scenario>.json`. `just sim-baseline` records the committed baseline for a
  duration and `just sim-fidelity` re-runs the mock sim under a chosen turn executor and compares
  against it.
- The mock runs (`just sim`, and the sim package under `just test`) run with a nil embedder, so the fine tier and the
  lexical completeness floor do not execute there; `just sim-completeness-rung` is the
  embedder-enabled rung that asserts every flush-lag dead-zone probe is surfaced. The scenario
  harness pins `recall.ack-mode: always` (a scripted `Step.RecallAck` stands in for a user's
  answer).
- The per-turn boundary of the log is `context.modified source=user.prompt`: the tag-fidelity
  grader (`internal/scenarios/sim/tagfidelity.go`) segments observed turns on it, so suppressing,
  reordering or duplicating that line must revisit the grader. `recall_unexplained_absence` (an
  expected thread that is off-spine and absent from the archive index) is always a substrate
  integrity failure and must stay 0, while an archived-recoverable expected match is forgiven
  (`TargetRecoverable`, `RecallExpectedForgiven`).
- Long-rung precision decline is the SPEC §9.1 corpus-saturation floor.
- Live rungs use `reaper.local` (gemma-4 family inference, `nomicai-modernbert-embed-base-bf16`
  embeddings). Service-in-loop raises per-turn cost from tens of milliseconds to single-digit
  seconds against roughly 20 minutes for a mock 120-day run, so the mock regime stays the default
  loop. The evolving-anchor workload variants (`vague-new`, `drift`, `invert`, interleave) report
  `drift_recall_origin`/`drift_recall_dest`, `abandoned_premise_recall`, `superseded_precision`,
  `ever_central_count`/`history_len` and `projection_churn` through `metric_keys.go`.
- Cadence: the generator's day functions (`sim/workload.go` `runWorkDay`/`runSession`/`runDayOff`)
  and the harness day-close share SPEC §9.4.1's clock contract. The day-off emits a `SleepCycle`
  marker step the harness intercepts to drive consolidation, and a day-close is stamped
  `simDayCloseDate(d) = SimClockStart + d·SimDayLength` (`internal/scenarios/harness.go`). Tests:
  `TestCadence_DayStartsNoPrecession` (at least 9 days), `TestCadence_DayOffPreservesPhase` and
  `TestDayOffThroughHarness`. A simulated-clock read inside one turn (`clock.Timeline()` within
  `turn.Run`) returns the same instant; closure decay keys off `TurnNumber − LastEngagedTurn`.
- Crash injection: `internal/crashpoint` hooks (`crashpoint.At(name)`) are registered at every kill
  point. `RestartWithCrash` (`internal/scenarios/restart.go`) arms a point, drives the turn or
  operation, discards in-memory state and drives `Reconcile` against the on-disk result. The matrix
  covers every registered crashpoint (`crashScenarioCoverage` is the registry): the turn orderings,
  every barrier step, every archival sub-window, standalone archival, recovery internals and
  morning-init. Each fixture asserts the full invariant suite plus `VerifyThreadMetaMatchesSpine`,
  at most one turn of loss against a pre-crash oracle, exact journal bytes, empty `RevertedPaths` on
  barrier and archival paths, exactly one day-commit, and an idempotent second `Reconcile`. The
  matrix is unit-grade (synthetic homes, inside the 30-minute checkpoint budget);
  `TestSimBarrier2Day` adds a 2-day barrier-crossing rung to the default suite.

### Event log format

The runtime↔harness measurement contract (`logs/YYYY-MM-DD.log`, `internal/eventlog`). SPEC §2.8
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
2026-05-08T02:55:44-07:00 system.bootstrap version=0.1.0 home-format=1 commit=abc1234
2026-05-08T02:55:45-07:00 session.started active=prj_3
2026-05-08T02:55:50-07:00 thread.engaged thr_42 turn_count=1247
2026-05-08T02:55:51-07:00 spine.match-fire thr_88 score=0.62 matched=trefoil,unknot query_size=5
2026-05-08T02:55:51-07:00 spine.embed-match-fire thr_88 score=0.610 query_chars=140
2026-05-08T03:12:00-07:00 retire.prompt thr=thr_42 inactivity=12
2026-05-08T03:12:45-07:00 retire.ack thr=thr_42 resolution=resolved edited=no
2026-05-08T03:14:30-07:00 dissect.fire reason=budget-pressure clusters=4
```

| Category | Events |
|---|---|
| `system` | `bootstrap` (identity line, once per process open, after the §9.1 format gate resolves: `version=` substrate, `home-format=` the EFFECTIVE on-disk revision, `commit=` (`unknown` on an unstamped binary), `home=`; distinct from `session.started`), `home-format-override` (a `--allow-newer-home` open waved a newer home through the §9.1 refusal — `on-disk=`, `binary=`; always paired with a stderr warning), `home-format-migrated` (an older home was migrated forward at open by the §9.1 gate — `from=`, `to=`), `context-ceiling-breach`, `context-ceiling-unenforceable` (the post-flight token-ceiling gate could not be evaluated: no `usage.prompt_tokens` for a turn while a ceiling was configured — `reason=usage-unavailable ceiling= turn=`; one line per affected turn; the count is never estimated), `empty-response` (forensic; the final drained response carried zero visible content — `reprompted=yes\|no`, `turn=`; SPEC §3.3), `turn-aborted` (a retracted turn — `turn=` the turn's transaction id, `phase=` the labelled stage, `bytes=` the retracted input's size; SPEC §4.3.3; **never the text**; **Open:** U8 — emitted only if retraction stands), `turn-abort-release-error` (forensic; releasing a retracted turn's recovery scope failed; same Open) |
| `thread` | `engaged`, `engaged-non-owner`, `engaged-cross-project`, `engaged-miss`, `created`, `created-meta-only`, `state-change`, `renamed` (a display name changed — `thr=`, `old=`, `new=`; same shape as `project.renamed`; unmeasured, recorded so every display name is reconstructable from the log), `fetch-miss`, `fetch-cross-project`, `anchor-projection-overflow`, `tag-defaulted` (`thr=` the bound owner, `cause=missing-tag\|empty-response`, `reprompted=yes\|no`; SPEC §3.3) |
| `spine` | `match-fire`, `embed-match-fire`, `intra-match-fire` |
| `recall` | `offer` (`count=N`), `accept` (`thr=`, `layers=`, `ack=human\|auto` — `auto` is an auto-band candidate fetched without asking; any accept-rate measure counts `ack=human` lines only), `decline` (`thr=`, `reason=not-relevant\|wrong-project\|already-known` — `already-known` is also the runtime's own verdict on a candidate already resident in Layer B under `recall.ack-mode: banded`), `net-cap-hit`, `flush-backlog`, `W1-diag`, `fire-error`, `error`, `index-error`, `embed-error`, `debt-window-error`, `tree-error` |
| `retire` | `prompt` (`thr=` and `inactivity=`\|`trigger=manual`\|`trigger=queued`, the last for a SPEC §3.5 boundary drain), `ack` (every applied closure — retire or WIP — `resolution=`, `edited=yes\|no`, `ack=human\|auto`; the ack-edit-rate canary counts `ack=human` lines only), `pending` (a decayed thread queued as an exception for the boundary drain — `thr=`, `inactivity=`, `reason=anchors=N\|turns=N`; once per idle episode, not per scan), `defer`, `complete` (`resolution=`), `curator-error`, `load-error`, `resolver-error`, `apply-error`, `error` |
| `archive` | `archived`, `recovered`, `recovered-record`, `skip`, `under-drain`, `error` |
| `recovery` | (SPEC §4.5.8 startup recovery; forensic) `begin`, `complete` (`cells=`, `reset=`, `stamped=`, `unrepairable=`), `pending` (`op=`, `day=` — a barrier/archival completion left pending), `rollback` (torn-turn reset — `turn=`, `reverted=`, `debris=`), `journal-recovered` (preserved in-flight bytes — `turn=`, `records=`), `morning-init` (`baseline=`, `rebuilt=`), `adopt` (cell-12 greenfield/legacy), `stamp-repaired` (cell-9), `unrepairable` (archive entry stays refused — `thr=`, `reason=`), `quarantined` (byte-exact preserved path), `log-tail-repaired` |
| `barrier` | (SPEC §4.5.8 day barrier B0–B6; forensic) `begin` (`day=`), `archived` (B1 — `day=`, `count=`), `folio-settled` (B1b, once per barrier — `day=`, `threads=`, `bound=`, `unbound=`), `folio-skip` (B1b left an excerpt where it was — `thr=`, `name=`, `reason=too-large\|conflict\|unreadable`; for `unreadable` the name is `folio.jsonl` and nothing in the thread moved), `day-committed` (B2 — `day=`, optional `repaired=true`), `reborn` (B5 daily re-init — `day=`, `baseline=`), `complete` (`day=`), `re-mint` (defensive B2 re-mint on a non-day-shape HEAD), `quarantined` (quarantine-and-proceed — `paths=`, `dir=`, `restored=`), `tag-error` (forensic; a lifecycle-tag derivation read/parse/tag fault — never fatal to the seal) |
| `consolidate` | `sleep-cycle` |
| `dedup` | `pruned`, `prune-skip`, `error` |
| `fs` | `edit-no-path`, `write-error` |
| `tool` | `result` (a tool result applied to memory as a `tool.result` delta, SPEC §3.0.1 — `name=`, `id=` the call id, `bytes=` the byte count BEFORE the SPEC §3.1 tool-output bound; an error result is a result; never the arguments). |
| `staging` | `promoted`, `evicted` (window-close GC, SPEC §3.10) |
| `topic` | `re-prompt` (`cause=missing-thread` + `fetched=` for the §5.5 fetch, `cause=missing-tag` for the §3.3 tag recovery, or `cause=empty-response` for the §3.3 empty-response recovery; **Open:** U3 — `cause=missing-thread` has no producer under opencode), `tag-missing`, `tag-invalid` (forensic; a near-miss tag candidate that failed strict validation — `reason=markdown-mangled\|bad-delimiters\|bad-thread-list\|bare-new-topic` + a short sanitized `snippet=`; additive to `tag-missing`), `warning` |
| `session` | `started` (a service run started, SPEC §1.1 — `active=`; distinct from `system.bootstrap`), `ended` (a service run ended), `working-set-save-error`, `checkpoint-error` |
| `project` | `created`, `switched`, `renamed` |
| `model` | `stream-close-warn`, `inference-telemetry` (the provider's extended inference-timing block, one line per turn: `ttft_ms=`, `prefill_ms=`, `gen_ms=`, `total_ms=` in MILLISECONDS, plus `prefill_tps=`, `gen_tps=` (the provider's own tokens/second), `prompt_tokens=`, `completion_tokens=`, `cached_tokens=` (prompt-cache hits), `turn=`; a de-facto extension, not OpenAI schema — when the provider sends none the line is not emitted, and absence is never an error or warning; **Open:** the source of the block under opencode, which makes the provider call — the event is retired if there is none, see "Serving deployment") |
| `workset` | `warning` (layer render failure, SPEC §3.1) |
| `context` | `modified` (`source=`, `bytes=`; SPEC §3.0.1). The per-turn boundary contract is in SPEC §2.8. |

**Reserved names** (a mechanism that emits one follows the shape of its category):
`system.shutdown`, `system.error`, `system.config-reload`, `spine.match-miss`,
`spine.entry-updated`, `recall.cross-project-fire`, `project.cd-changed`, `project.remote-adopted`,
`project.remote-updated`, `project.remote-collision-prompt`, `project.meta-updated`, `dissect.fire`,
`dissect.cluster-proposed`, `dissect.complete`, `directive.accrual-update`,
`directive.parameter-read` (sampled), `index.rebuild-start`, `index.rebuild-complete`,
`index.verify-fail`.

### opencode-in-loop harness

The probe extends the existing harness; it does not build a second one. One Go test process hosts
the workload generator, the driver, the mock model endpoint and the memory service, and spawns one
opencode server; the plugin, loaded by opencode, calls back into the test process.

- The workload generator emits steps to the driver, which calls opencode's HTTP API.
- opencode's plugin calls the memory service in the test process over HTTP + JSON.
- opencode's provider `personant-mock` calls the mock model endpoint, also in the test process,
  over the OpenAI-compatible wire.

Because the memory service runs inside the test process, the harness keeps direct access to the
event log and the substrate; invariants, recall-fidelity scraping and metrics run as they do for
the in-process turn.

- **Turn executor.** `-sim.turn-executor` selects `runwithinfo` (default, the in-process turn),
  `phases` (`RunTurnInProcess`, the probe phases) or `opencode` (`internal/scenarios/opencode`,
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
  wherever run-to-run repeatability is measured, so a commit hash repeats across runs (PR §6,
  lesson 7).
- **Gates.** `oc_missed_deltas` and `oc_uncomposed_requests` must be 0 (PR §2), among the `oc_*`
  keys in `metric_keys.go` (hook round-trip time, step overhead, history message count, database
  size, hook fallbacks). `oc_uncomposed_requests` is counted at the mock.
- **Probe targets.** `just probe-smoke` runs one handwritten turn; `just probe-resume` stops or
  kills opencode and the service mid-session and resumes (RP).

**Open:** probe-code retention — whether `model.MockServer` (the mock served as an
OpenAI-compatible endpoint) stays on the main line, as a run with opencode in the loop requires
(SPEC §9.2). The baseline comparator (`sim-baseline`, `sim-fidelity`, the committed baselines) is
named for keeping; the rest of the opencode driver and executor is undecided.

**Open:** whether the §9.1 acceptance gate needs an opencode-in-loop rung.

---

## Deferred work

Deferred work is tracked in **ROADMAP.md** (near-term / mid-term / far-horizon tiers), which owns
the catalog, including the design capture for the "sleep" cycle and concurrent sessions.

---

## Anti-patterns to push back on

If you see one of these proposed (or are about to write it), stop and surface the concern:

| Anti-pattern | Why it's wrong | Reach for instead |
|---|---|---|
| "Let the LLM decide where to put state" | Pushes canonical state into the LLM | Deterministic code holds state; the LLM drafts, deterministic code validates and writes |
| "Re-implement something opencode owns" (tools, permissions, UI, session storage, providers) | Wheel re-invention; the project's one hard problem is context | Conform to opencode; take over only what enters the request, through a hook. Elsewhere, borrow existing designs and use well-maintained imports |
| "Put memory logic in the plugin" | Splits the single writer; the plugin cannot be tested with the sim | The plugin forwards and applies; all state and logic stay in Go |
| "Use SQLite for the spine" | Substrate violation; loses inspectability + git-diffability | Text + git; SQLite stays in reserve as a *derived* index if scale forces it |
| "Have the LLM run `git commit` on the home tree" | Mixes LLM-controlled with autonomic git | The service commits autonomically |
| "Add this dep speculatively" | Approved-when-earned violation | Wait until consumer is real; bring dep in *with* consumer |
| "Tests can come later" | Asymmetric-cost violation | Measurement infrastructure built alongside features, not after |
| "Mock LLM is too narrow; let's use a real LLM in tests" | Adds non-determinism, network dependency, cost | Mock with scripted/generated responses; real LLM for `just integration-test` only |
| "Speculatively prefetch threads we *might* need" | Violates "every loaded thread is needed" | Model emits topic tag; runtime fetches what was named |
| "Shorten the topic-tag instruction to save tokens" | While the in-band tag stands (Open: U3), emission is the load-bearing recognition signal | The system prompt's topic-tag directive is non-optional |
| "Reset the worktree when it's dirty on startup" | A markerless-dirty tree is a legitimate hand-edit; resetting on dirtiness destroys user work (and, off primary, a whole day) | Gate `reset --hard` on the turn marker/journal signal, never on dirtiness; absorb hand-edits forward (Spec §4.5.8 cell 3) |
| "Commit durability writes async / deferred (flush later)" | A commit lagging its canonical writes reintroduces the torn-window ambiguity the marker/commit protocol exists to eliminate | Commit ordering is synchronous, always; cost comes from cheaper steps (scoped add, disposable daily), never from widening the loss window |

---

## Document map

| You want | Look here |
|---|---|
| The project documents themselves | `PROJECT_DOCS/`: THESIS at `entry-point.md`, then `SPEC/`, `ARCHITECTURE/`, `CONVENTIONS/`, one leaf per section; the root files are pointers |
| How the plugin, service and opencode fit together | "Plugin route" (this document) |
| Where a package lives and what it does | "Module map" (this document) |
| Record and file formats | "Storage and record formats" (this document) |
| Substrate constraints | ARCHITECTURE §"Substrate non-negotiables" |
| v0.1 acceptance criteria | SPEC §9.1 |
| opencode facts the route rests on | the reports in `OPENCODE_RESEARCH/` (shorthand under "Plugin route") |

---

## How decisions evolve

Personant evolves by:

1. **Calibration scenarios** — parameter sweeps over canonical workloads; metrics matrices identify
   operating points.
2. **Directive accrual** — see "Personalization through directive accrual".
3. **Empirical pressure on watch items** — known weak spots (categorized below) surface via
   instrumentation; targeted work follows the data.

When you propose a change, ask:

- Does it have a measurement story? (How will we know it worked?)
- Does it commit to a specific value, or open a calibration window?
- Does it preserve or invert a recurring pattern (above)?
- Does it create migration cost we can't recover from?

**Categories of known weakness** the simulation regime is designed to surface and quantify:

- **Symbol-extraction noise** — synonym fragmentation, low-information leakage, model emission
  drift.
- **Recall precision** — cross-thread symbol collision, anchor selection quality at retirement.
- **Deterministic-pass coverage** — per-project regex curation needs.

---

## Mental model summary

> The service is the canonical-state holder and the single writer. The LLM is a narrow judgment
> instrument. opencode owns everything except what enters the request. Every working-window
> mutation goes through the §3.0 chain. Every dependency earns its keep with a real consumer.
> Testing is the lab bench, not a quality gate. Constraints are load-bearing.
