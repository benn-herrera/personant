# ACTIVE DESIGN – Personant

## BLUF
Personant solves one problem: context management and long-lived agent memory ([THESIS.md](THESIS.md)). It runs
under **unmodified opencode** (release v2.2.2) as two pieces:

- **A thin opencode server plugin** (TypeScript, no memory logic) that forwards opencode's hook events to the
  memory service and applies its answers.
- **An out-of-process Go memory service** that owns all memory state and composes every model request.

The capability analysis behind this shape is [OPENCODE_INTEGRATION_OPTIONS.md](OPENCODE_INTEGRATION_OPTIONS.md)
(cited below as **IO §n**). If this route cannot give the memory system the control it needs, the fallback is a full
Go port of opencode's server: [ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md](ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md).

opencode source citations below are repo-relative paths in opencode at tag v2.2.2
(commit `098aa7fd6e02b03d6fc410f0b052038c7d4c5e81`).

## Theme: Solve only one hard problem
The memory system is our real work. Everything else uses the solutions other people built. Borrow designs, make
reasonable use of high-quality imports. Wheel re-inventing is off the table.

**Conform to opencode; do not evaluate it.** Outside context management, where opencode has made a choice we use it
as it is, whether or not we would have chosen differently. The question is always "what does opencode do", never
"should we do it differently".

**Boundary.** Ours: what enters the model's context — composition of every request, compaction and summarization,
recall, and the sub-mind split and re-merge. opencode's: everything else — the TUI, session storage, the message
data model, the event stream, agents, permissions, tools, providers and config. Where opencode's machinery would
decide what the model sees (compaction, history replay), the plugin takes that decision over through a hook; it does
not reimplement the machinery.

**Language.** The memory service is Go. The plugin is TypeScript because opencode loads server plugins in its own
Bun process (IO §2.1); it stays a stateless adapter so no memory logic lives outside Go.

---

## Integration shape

Each element names the opencode mechanism it rides on. Requirement IDs (R1–R13) are IO §1's.

### 1. Request composition — `session.context` hook (R1, R11, R12)
- The hook fires on every primary step and every retry; `system`, `messages`, `tools` and `options` are all mutable
  (`packages/plugin/src/effect/session.ts:25-36`; IO §2.3). After the hook, opencode only strips media the model
  cannot accept and adds headers (`packages/core/src/session/model-request.ts:276-310`).
- The plugin sends the hook input to the memory service and replaces `system` and `messages` with the service's
  answer. The service builds the answer the way `turn.RunWithInfo` builds a request today: composed working set as
  the system prompt, bounded history tail, current user input, this turn's tool messages
  (`internal/turn/turn.go:905-1000`; see "Runtime inversion" below).
- Tool-call/tool-result pairs in the returned `messages` stay intact (IO §4, item 4).
- Every other model call is kept off the provider: the `session.title` hook supplies `result`, which skips the model
  call (`session.ts:53-56`; IO §2.3).

### 2. Compaction — `compaction.auto: false` plus plugin-supplied checkpoints (R2)
- Config sets `compaction.auto: false`; only manual compaction runs, overflow included
  (`packages/core/src/session/compaction.ts:294-295`).
- opencode reads stored history from the latest compaction onward on every step
  (`packages/core/src/session/history.ts:192-199`). To keep that read bounded, the memory service schedules
  checkpoint compactions: the plugin enqueues one with `session.compact` (`effect/session.ts:197-217`), and the
  `session.compaction` hook answers with `result`, which skips the model call (`compaction.ts:605, 852-866`).
- The checkpoint's summary text is a fixed placeholder: the `context` hook discards opencode's replay of it. The
  checkpoint is a read bound, not a memory mechanism.
- Checkpoint cadence (every N turns) is a memory-service parameter; the probe measures it.

### 3. Tools — through the plugin tool API (R6, R7, R9)
- Recall and any other memory tooling are registered with `tool.transform` (IO §2.3, "Plugin context"); the memory
  service executes them. Tools invented inside the `context` hook are dropped
  (`model-request.ts:273-281`), so registration is the only route.

### 4. Event observation — the §3.0 chain with no gaps (R3, R4, R13)
ARCHITECTURE.md §3.0: "**No path mutates the working window without going through it.**" Every context change
reaches `onContextDelta` (`internal/turn/chain.go:70`) through one of:

| Delta source | opencode mechanism |
|---|---|
| `user.prompt` | `session.prompt` hook, before the prompt is stored (`packages/core/src/session/prompt.ts:42-90`) |
| `tool.result`, `fs.read`, `fs.write` | `tool.execute.after` (`packages/core/src/tool.ts:115-157`) |
| `user.shell-capture` | `session.shell.ended` event (IO §2.3 event table) |
| `model.response` | `session.step.ended` / `session.text.ended` events |
| turn close | `experimental.idle` hook (`packages/core/src/session/runner/llm.ts:172-182`), plus `session.execution.succeeded` |
| fork, revert, delete | `session.forked`, `session.revert.committed`, `session.deleted` events (IO §2.7) |

The memory service records which deltas it has applied per turn, so a missing one is detectable (the probe's
missed-event gate).

### 5. Transport and failure handling (R5)
- Plugin → service: HTTP + JSON on loopback, using `fetch`. Bun's `node:net` and child-process APIs are unverified
  from plugin code (IO §5, item 6); `fetch` avoids depending on them.
- Hooks have no timeout and run sequentially (`packages/core/src/plugin/hooks.ts:88-95`). Every plugin RPC carries
  its own timeout (`AbortSignal.timeout`) and, on timeout or error, leaves the event unmodified — opencode's default
  behaviour — and reports a fallback count. A Promise-API hook's rejection becomes an Effect defect
  (`packages/plugin/src/promise/adapter.ts:600-602`), so the plugin catches every failure itself.
- The memory service is the single writer of memory state. Several opencode server processes sharing one service is
  open (IO §5, item 4).

### 6. Config
opencode's config files are read by opencode, as they are. Personant-only settings live in a separate file under
`~/.config/personant/` and contain no setting that overlaps or conflicts with opencode's. Settings opencode must hold
for this route (`compaction.auto: false`, the plugin entry) are written into opencode's config in opencode's own
format.

### 7. opencode database GC
opencode keeps one global SQLite database (`$XDG_DATA_HOME/opencode/opencode.db`; OPENCODE_SERVER_SURVEY.md §4.4).
Rows before the latest checkpoint are never read again by the runner but stay on disk. The memory service removes
them under these rules:

1. **Only when nothing uses the database:** every opencode process, the background service included, has exited.
   Otherwise the run is skipped; skipping costs nothing.
2. **Under an exclusive SQLite lock** taken before any change.
3. **Schema-version gated:** it refuses to run on a migration set it was not built for (tables change between patch
   releases; v2.2.2 added `session_part`, `packages/core/src/database/migration.gen.ts:57`).
4. **Reference-safe:** it deletes only rows nothing still points to — fork origins, revert targets, blobs,
   `session_part`.
5. **Verified:** `PRAGMA integrity_check` and `PRAGMA foreign_key_check` pass, then `VACUUM`.
6. **Proven first on a copy:** opencode started on the cleaned copy loads sessions, renders scrollback, and forks
   and reverts from kept rows.

Whole sessions the memory system has finished with (merged sub-mind children) are removed through the supported
`session.remove` instead (`effect/session.ts:197-217`).

### 8. Code Mode and plugins
opencode's own server runs, so its JavaScript plugins and Code Mode work unchanged.
[ROADMAP_PLANS/code-mode-support.md](ROADMAP_PLANS/code-mode-support.md) applies only to the fallback route.

---

## Open decision: the in-band topic tag (R7)
SPEC.md §5.1 and §5.5 have the model emit a topic tag at the head of each response; a tag naming a thread not in
Layer B aborts the stream and re-issues the request with that thread loaded. Two ways to carry this under opencode:

| Option | What it takes | Consequence |
|---|---|---|
| Keep the in-band tag | A spike: read the head of the streaming response in the `session.http.response` hook, cancel upstream, re-issue, return the second stream (`model-request.ts:329-360`; IO §5, item 2) | SPEC unchanged; wire-format code per provider protocol inside the plugin; stored transcript carries the tag unless stripped there |
| Replace it with a recall tool | Register a fetch tool (§3 above); its result enters context at the next step, which the `context` hook controls | No opencode-side stream code; one extra model step per fetch; SPEC §5.1/§5.5 and the tag-fidelity measurements change |

Until this is decided, the probe parses the tag after the step ends (no mid-stream abort) and does not exercise the
§5.5 re-issue.

---

## Runtime inversion (prerequisite for both probe and product)

Today `turn.RunWithInfo` owns the whole turn, including the model call and the tool loop. Under opencode, opencode
owns the model call and the tool loop, and the runtime must be driven by hook events instead.

Facts the inversion acts on:

- `grep -n "^func RunWithInfo" internal/turn/turn.go` → `747:func RunWithInfo(ctx context.Context, state *State, preEvents []Delta, userInput string, out io.Writer) (_ string, info TurnInfo, err error)`
- Inside it, in order: day barrier (`state.Ops.MaybeDayBarrier`), session snapshot and `TurnNumber++`, prompt
  journal, pre-event deltas (`turn.go:892`), the `user.prompt` delta (`turn.go:900`), working-set composition via
  `state.Ops.ComposeWorkingSet` (`turn.go:906`), request assembly (`turn.go:986-1000`: system prompt +
  `boundHistoryTail(state.History, state.Budget)` + user input + tool messages), the model call
  (`turn.go:1007: sr, err := state.Client.ConsultStream(ctx, req)`), the tool loop (`turn.go:1173: runToolRound`) and
  its deltas (`turn.go:1185`), and the `model.response` delta (`turn.go:1307`).
- Composition is already separable: `grep -rn "workset.Compose(" internal --include='*.go' | grep -v _test` →
  `internal/memops/fileadapter/workset_compose.go:65`, reached through the `MemoryOps.ComposeWorkingSet` port
  (`internal/memops/memops.go:350`). `workset.Compose` (`internal/workset/workset.go:141`) is a pure renderer.

The inversion splits `RunWithInfo` into phase entry points the memory service calls from hook events:

| Phase | Driven by | Covers |
|---|---|---|
| `BeginTurn` | `session.prompt` | day barrier, snapshot, `TurnNumber++`, journal, pre-event deltas, `user.prompt` delta |
| `ComposeRequest` | `session.context` | working-set composition and request assembly, given this turn's messages from the hook input |
| `IngestToolResult` | `tool.execute.after` | tool-result deltas |
| `IngestModelResponse` | step/text ended events | `model.response` delta, tag parse |
| `CloseTurn` | `experimental.idle` | closure, commit, LRU/dormancy, working-set persist |

`RunWithInfo` is re-expressed on top of these phases so the in-process simulation keeps running unchanged. Done-line:
`just test` passes and `just sim` reproduces its committed baselines (`internal/scenarios/testdata/baselines/`)
with no metric change. A baseline change means the split altered behaviour.

---

## First work: the end-to-end probe

**Question it answers:** with opencode in the loop, does the memory system keep the properties the simulation
proved — per-turn cost that does not grow as the store fills, and no gaps in the §3.0 chain — and what does
opencode add per step?

ARCHITECTURE.md §Testing: the harness is "*designed in these documents before it is coded*". This section is that
design. The probe extends the existing harness; it does not build a second one.

### Process layout
One Go test process hosts the workload generator, the driver, the mock model endpoint and the memory service. It
spawns one opencode server. The plugin, loaded by opencode, calls back into the test process.

```
test process (Go)                                     opencode serve (Bun, unmodified)
  workload generator ──Steps──> driver ──HTTP API──>  session runner
  memory service  <──────── plugin HTTP/JSON ───────  plugin (hooks)
  mock model endpoint <───── OpenAI-compatible ─────  provider "personant-mock"
```

Because the memory service runs inside the test process, the harness keeps direct access to `h.State`, the event
log and the substrate; invariants, recall-fidelity scraping and metrics run as they do now.

### Components

**1. Mock model endpoint** — `internal/model` (new file beside `mockllm.go`).
- An `http.Handler` serving `POST /chat/completions` (streaming SSE and non-streaming) and `GET /models`, backed by
  a `*model.MockClient`.
- Wire encoding reuses the OpenAI wire types `HTTPClient` already decodes (`internal/model/http_client.go`; streaming
  path at `:154`, `chat/completions` at `:69` and `:164`), so the encoder and decoder share one definition.
- `model.Response` is `{Content, ToolCalls, Usage, FinishReason}` (`internal/model/client.go:152-157`); the handler
  emits exactly that, including usage, so opencode's token accounting sees real numbers.
- Every request is logged with its arrival time and message count for the metrics below.

**2. Driver** — `internal/scenarios/opencode/` (new package).
- Starts opencode headless: `opencode serve --stdio --port 0` with a random `OPENCODE_PASSWORD`; the URL is the first
  stdout line, JSON `{url}` (OPENCODE_TUI_LAUNCH.md, Q2 item 1: "the CLI spawns a private child: `<self> serve
  --stdio --port 0` with a random `OPENCODE_PASSWORD`. It reads the URL from the child's first stdout line, a JSON
  `{url}`"). `opencode serve --help` on the installed v2.2.1 lists `--hostname`, `--port`, `--cors`, `--service`,
  `--stdio`.
- Isolation: `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_CACHE_HOME` all point under the run's data
  home (`runDataHome`, `internal/scenarios/harness_paths.go:39`, as `newHarness` uses). opencode derives every global
  path from these (`packages/util/src/global-roots.ts:5-8`). The user's opencode database is never touched.
- Writes the isolated opencode config: provider `personant-mock` (`@ai-sdk/openai-compatible`, `settings.baseURL` =
  mock endpoint; IO §2.6), `compaction.auto: false`, the plugin entry, and permissions that allow the tools the
  workload uses.
- Per `Step` (fields: `UserInput`, `MockResponse`, `PreEvents`, `ExpectedRecallMatches`, `RecallMode`, `RecallAck`,
  `ClosureAck`, … — `internal/scenarios/harness.go`): installs the step's model responses into the mock, posts
  `UserInput` to `POST /api/session/{id}/prompt`, follows `GET /api/event` until the turn closes, and answers
  permission prompts from `RecallAck` / `ClosureAck` through `POST /api/session/{id}/permission/{requestID}/reply`.
- Replaces only the turn-execution phase of `runStep`: `stepExecTurn` (`internal/scenarios/harness_run.go:314`, which
  calls `turn.RunWithInfo` at `:317`) becomes "drive the turn through opencode". Clock, recall measurement, metrics,
  day-close and sleep-cycle phases are reused unchanged.

**3. Workload split** — the generator (`internal/scenarios/sim/workload.go`) is reused as-is; its steps are split
across the two halves:
- **Driver half:** `UserInput`, acks, clock.
- **Model half:** `MockResponse`, installed per step (as `harness_run.go:250` does with `h.Mock.SetResponse`).
- **`PreEvents`** (the sim emits `fs.read`, `fs.write` and `fs.commit` deltas) must reach the memory service through
  opencode, not be injected. The mock answers the turn's first consult with tool calls to opencode's `read`, `write`
  and `shell` tools against a fixture workspace the driver prepares, then serves `MockResponse`
  (`model.NewScriptedMockPerConsult`, `internal/model/mockllm.go:173`). See open item 3.

**4. Memory service** — `internal/opencodehook/` (new package).
- HTTP handlers for the plugin's calls, each mapped to a phase from "Runtime inversion".
- Decodes opencode's provider-neutral messages (`packages/ai/src/schema/messages.ts`) into `model.Message`.
- Runs the real runtime and composer. No stub.
- Records per-turn delta coverage for the missed-event gate.

**5. Plugin** — `opencode-plugin/personant.ts` (new top-level directory), Promise API (`{id, setup}`;
IO §2.1).
- Hooks: `session.prompt`, `session.context`, `session.compaction`, `session.title`, `tool.execute.after`,
  `experimental.idle`, `session.model.request` (records `kind`); event subscriptions per Integration shape §4.
- Registers memory tools through `tool.transform`.
- Calls `session.compact` when the service's turn-close answer asks for a checkpoint.
- Per-RPC timeout and fallback (Integration shape §5). No state beyond the service URL.

**6. Metrics** — through `internal/metrics` (`Run.Counter`, `Run.Record`, `Run.Set`, `Run.WriteJSON`) and the sim's
per-day telemetry (`internal/scenarios/sim/telemetry.go`); process memory through `internal/scenarios/memtel.go`.
Every new cross-seam key goes in `internal/scenarios/metric_keys.go`.

| Metric | Measured as |
|---|---|
| `oc_hook_rtt_ms` (histogram, per hook) | plugin-side timing reported with each call |
| `oc_step_overhead_ms` (histogram) | turn wall time at the driver − mock model time − hook time |
| `oc_history_messages` (histogram) | message count in each `session.context` hook input — the rows opencode read for the step |
| `oc_db_bytes` (gauge, per sim-day) | size of the isolated `opencode.db` plus `-wal` |
| `oc_missed_deltas` (counter) | deltas the driver/mock produced that the memory service never applied |
| `oc_hook_fallbacks` (counter) | plugin RPCs that timed out or failed |
| `oc_unexpected_model_kinds` (counter) | `session.model.request` calls whose `kind` is not `primary` |
| `oc_turns_per_sec` (gauge) | throughput, to size longer rungs |

Existing harness metrics (recall fidelity, budget, substrate growth) are emitted as in `just sim` and reported, not
gated, while the topic-tag decision is open.

**7. Runner target** — `sim-opencode` in the justfile beside `sim` (`justfile:476`), taking `DURATION` like `sim`.
It checks `opencode --version` is 2.2.x and fails with that message otherwise. Not part of `just test`.

### Pass criteria
- `oc_missed_deltas == 0`, `oc_hook_fallbacks == 0`, `oc_unexpected_model_kinds == 0`.
- No growth trend in per-turn cost: the median of `oc_step_overhead_ms` and of `oc_hook_rtt_ms` over the last 10% of
  turns is at most 1.25× the median over the first 10% after warm-up.
- `oc_history_messages` never exceeds the checkpoint cadence × the workload's maximum messages per turn, plus the
  checkpoint message.
- The run's existing invariants pass as in `just sim`.
- Reported, not gated: `oc_db_bytes` growth (GC is out of scope for the probe), recall-fidelity deltas against the
  in-process baseline, checkpoint cadence sweep (N = 1 and N = 8).

### Sequence
1. Runtime inversion; `just sim` baselines unchanged.
2. Mock model endpoint, with a unit test decoding its output through `HTTPClient`.
3. Memory service handlers and plugin; a one-turn smoke test through real opencode.
4. Driver and workload split; `just sim-opencode DURATION=1d`.
5. Longer rungs as throughput allows; results recorded in this document.

Work packages for these steps go into `ACTIVE_PLAN.md` under the dispatchability rules in CONVENTIONS.md
("Plan dispatchability").

### Out of probe scope
The in-band topic tag's stream abort; the ASK pick-list (yes/no `permission.assert` only; IO §4); sub-mind split and
re-merge; database GC; the live TUI.

---

## Open items

1. **Inversion coupling.** `RunWithInfo` interleaves journaling, rollback (`internal/turn/rollback.go`) and
   re-prompt state with the model call. Whether the abort/rollback paths still have a trigger once opencode owns the
   model call has to be settled while splitting; a turn opencode interrupts arrives without `experimental.idle`
   (IO §2.3).
2. **Message decoding.** No machine-readable schema for `packages/ai/src/schema/messages.ts` was found; the Go types
   mirror it by hand, pinned by a contract test against captured hook input.
3. **PreEvent fidelity.** opencode's `read` tool formats its output (e.g. line numbers), so symbols extracted from a
   tool-delivered `fs.read` can differ from the injected delta's. Recall metrics may shift for that reason alone.
4. **Turn boundary in events.** Which event marks the end of a turn's model output for `IngestModelResponse`
   (`session.step.ended` per step vs `session.text.ended`) is unverified; multi-step tool turns depend on it.
5. **Missed events across plugin reload** (IO §5, item 5). The probe does not edit the plugin mid-run.
6. **Throughput.** Real opencode per turn is unmeasured; a 120-day rung may not be practical. `oc_turns_per_sec`
   from the 1-day rung sizes it.
7. **Experimental hooks.** `experimental.idle` is marked experimental (`effect/session.ts:164-179`); the opencode
   version is pinned.
8. **Several opencode servers on one memory service** (IO §5, item 4) — not exercised by the probe.
