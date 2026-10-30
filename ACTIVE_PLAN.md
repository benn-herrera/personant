# ACTIVE PLAN – opencode end-to-end probe

Executes [ACTIVE_DESIGN.md](ACTIVE_DESIGN.md) "First work: the end-to-end probe" and its
prerequisite "Runtime inversion", in the probe-local form stated under Decisions confirmed.
Section names below are ACTIVE_DESIGN.md's unless another file is named. Measured opencode
behaviour is from [OPENCODE_PREPROBE_RESULTS.md](OPENCODE_PREPROBE_RESULTS.md) (cited as PP §n).

**Dispatch status:** dispatchable after the architect revision of the prompt-engineer findings
lands (CONVENTIONS.md "Plan dispatchability").

## Decisions confirmed

- **The probe is throwaway.** Its only output is trustworthy data and lessons for a later real
  implementation. Write the least code that produces the measurements. Write no unit or
  integration tests beyond the fidelity run (P1) and the smoke run (P4). The missed-event
  detector is a metric, not a test.
- **The runtime inversion is probe-local.** One new file, `internal/turn/probe_phases.go`, holds
  the phases and `RunTurnInProcess`. It copies `turn.RunWithInfo`'s turn sequencing only and
  calls the real production pieces directly; it never copies memory logic. No existing
  production file changes. The file has no exported wrappers or accessors over other
  `internal/turn` identifiers. `internal/opencodehook` holds only the memory service.
- **Fidelity gate.** `RunTurnInProcess`, driven through the existing sim, must reproduce the
  committed baselines for the same seed and duration. Any divergence is a probe defect, fixed
  before any opencode run.
- **Comparison exclusions are fixed, not discovered.** The comparator skips only:
  - wall-clock timing keys: names ending `_ms` or containing `latency`
  - the run's own `started_at`, `ended_at` and `labels.scenario`
  - `day_commit_bytes`

  Any other differing key is a defect: stop and report it. These are exactly the keys that
  differed between two same-commit, same-seed runs. A Python stdlib diff of the two blobs,
  `sim-workload-seed24095-dur48h0m0s.260830035927` vs `….260830040825` under `test/rundata/`
  (manifest `git_head` 23922f2 for both), printed `started_at`, `ended_at`, `labels.scenario`, the
  `_ms` histograms, `day_commit_bytes` and `recall_query_latency_p50/p95/p99`, and nothing else.
- **Mock inference only.** Every probe and baseline recipe passes `-sim.live-embedding=false
  -sim.live-inference=false` literally.
- **Acks** use the scripted resolvers `stepSetup` installs on `h.State`
  (internal/scenarios/harness_run.go:253-254). They run unchanged inside `CloseTurn`. The probe
  does not measure opencode's ask path; "Out of probe scope" excludes the ASK pick-list.
- **Checkpoint cadence** is a test flag read by the in-process memory service. No plugin option
  carries it.
- **Contract documents are not updated.** CONVENTIONS.md and ARCHITECTURE.md change only if the
  plugin route is adopted after the probe.
- **Topic tag:** the in-band tag decision stays open. The tag is parsed after the step ends; no
  mid-stream abort and no §5.5 re-issue under opencode.
- **opencode target: v2.2.3.** `opencode --version` → `opencode v2.2.3`; the clone at
  `../opencode` is at tag v2.2.3 (`git rev-parse --short HEAD` → `082ac6281f`). opencode
  citations below are at that tag.
  - PP was measured on v2.2.1. In the clone,
    `git diff --stat v2.2.1 v2.2.3 -- packages/plugin/src packages/core/src/session packages/core/src/tool.ts packages/schema/src/session-event.ts packages/ai/src/protocols/openai-chat.ts packages/ai/src/protocols/shared.ts`
    lists no change to `session/runner`, `session-event.ts`, `openai-chat.ts` or `shared.ts`, and
    `effect/session.ts` gains only a doc comment — but `session/history.ts`, `session/prompt.ts`
    and `session/projector.ts` changed (v2.2.1→v2.2.2).
  - So F5 and F8 and the rows-read reference are taken as the expected behaviour, not as settled:
    the P4 smoke re-confirms F5 and F8 at v2.2.3, and P6 run 2 re-measures rows read.
- **Gates.** Edit gate: `just build`. Checkpoint gate: `just test`, once before each commit
  (AGENTS.md). Every flag added here defaults to current behaviour.

## Facts the packages act on

- **F1. The control flow `RunWithInfo` has to preserve:**
  - `grep -n "^func RunWithInfo" internal/turn/turn.go` → 747.
  - Snapshot at turn.go:825. Prompt journal at :849. Response journal at :1234.
    `canonicalStarted = true` at :1315. The deferred pre-canonical rollback is at :865-869.
  - Re-prompt branches are inside the consult loop (:979-1195): empty response :1036, §5.5
    missing thread :1052, missing tag :1096.
- **F2. No tool rounds in the in-process sim:** `grep -rn "\.Tools *=\|ToolCalls:"
  internal/scenarios --include='*.go' | grep -v _test` → no output.
- **F3. The sim exercises the §5.5 re-prompt.** A same-seed run dated 2026-08-30
  (`test/rundata/sim-workload-seed24095-dur24h0m0s.260830042515/logs/`) has 241 `re-prompt`
  lines, all `cause=missing-thread`. The copy keeps all three re-prompt branches. Under opencode
  the fetch never fires, so the recall delta against in-process is reported, not gated.
- **F4. opencode events and hooks used** (opencode/packages/schema/src/session-event.ts):
  - `session.execution.failed` :252, `.interrupted` :259.
  - `session.text.ended` :421 carries the full `text` of one part.
  - `session.step.ended` :361 carries `tokens`.
  - `session.usage.updated` :173 is subscribable. `session.usage.recorded` is not (PP §2).
  - `session.status` and `session.idle` are never published. Do not depend on them (PP §2).
  - `experimental.idle` is triggered at packages/core/src/session/runner/llm.ts:178.
- **F5. Turn-close ordering (PP §2, §6.1-2):**
  - `experimental.idle` ran before the plugin received the turn's final `session.step.ended` in
    48,158/48,158 turns.
  - `session.execution.succeeded` follows both the final step and any checkpoint compaction.
  - `experimental.idle` fires a second time when the idle hook itself enqueues a checkpoint.
  - `session.model.request` reports `kind: "compaction"` even when the hook's `result` means no
    request is sent.
- **F6. Isolation:**
  - Project config walks from cwd up to `/` (packages/core/src/config/discovery.ts:80).
  - Env switches, in packages/cli/src/server-process.ts:120-132: `OPENCODE_DISABLE_MODELS_FETCH`,
    `OPENCODE_DISABLE_PROJECT_CONFIG`, `OPENCODE_DISABLE_CLAUDE_CODE`, `OPENCODE_CONFIG`.
  - Global roots use `os.homedir()` and `os.tmpdir()` (packages/util/src/global-roots.ts:4, :17).
  - PP §2: XDG_*/HOME/TMPDIR isolation holds; the user DB size and mtime are unchanged.
  - PP §1: a user-owned `opencode serve --service` holds the user DB open.
- **F7. Config shape (PP §1):**
  - The provider key is `providers` (packages/schema/src/config.ts:115) and the plugin key is
    `plugins` (:106).
  - Plugins load from a directory path with an `index.ts`; no `@opencode/plugin` import is
    needed.
  - opencode always streams (PP §2).
- **F8. Text fidelity (PP §5):** text set in `session.context` arrives byte-exact.
  - System parts are joined with `"\n"` (packages/ai/src/protocols/shared.ts:186).
  - Text parts within one message are concatenated, and empty parts vanish.
  - A mid-conversation `system` message becomes a `<system-update>` user message with `&<>`
    escaped (:198).
- **F9. Adding a field to `scenarios.Scenario` breaks no literal.**
  - `git grep -nE 'Scenario\{[^}:]+\}' -- '*.go'` → no output (no positional literal).
  - References, for scope: `git grep -l '\bScenario\b' -- '*.go'` → 17 files:
    `internal/scenarios/{crash_matrix_test,harness,harness_resolvers,harness_run,harness_setup,harness_validate_test,invariants,invariants_test,marker_contract_test,recall_fidelity_test,recall_madlibs_test,runmanifest_test,scenarios_test}.go`,
    `internal/scenarios/sim/{sim_embed_test,sim_test,telemetry,workload}.go`.
- **F10. HTTP API** (opencode/packages/protocol/src/groups):
  - `POST /api/session` (session.ts:165)
  - `…/:sessionID/prompt` (:337)
  - `GET /api/event` (event.ts:61)
- **F11.** `grep -n golang.org/x/tools go.mod` → no output: `go/packages` is not a dependency.

## justfile variables

Declare these beside `DURATION` / `LIVE_EMBEDDING` / `LIVE_INFERENCE` (justfile:450-452), each as
`env_var_or_default`, with these defaults: `EXECUTOR` = `"phases"`, `CADENCE` = `"8"`,
`DROP_SOURCE` = `""`, `TRACE_CHECK` = `""`.

## Packages

### P0 — Baselines and comparator · mechanical · Status: not started

**Scope:**
- `internal/scenarios/sim/sim_test.go`: flags next to `simDuration` (:187):
  - `-sim.baseline-write=<path>`
  - `-sim.baseline-check=<path>`
  - `-sim.turn-executor` (default `runwithinfo`; P0 accepts only that value), declared as
    `var simTurnExecutor = flag.String("sim.turn-executor", "runwithinfo", …)`
- `justfile`: the `EXECUTOR` variable; targets `sim-baseline` and `sim-fidelity` next to `sim`
  (:476).
- New tracked files: `internal/scenarios/testdata/baselines/sim-1d.json` and `sim-1w.json`.

**Work:**
- After `runSimRung` returns in `TestSim`, read the blob with `readMetrics(h.MetricsPath)`
  (sim_test.go:1206).
- **Write mode:** copy the blob file byte-for-byte to the path.
- **Check mode:** compare `Counters`, `Gauges` and `Histograms` (histograms element-wise, as
  ordered lists) with the exclusions in Decisions confirmed. Log each differing key with both
  values. Call `t.Errorf` once if any key differs.
- **Recipes.** Both run TestSim as `sim` does, with the live flags pinned to `false`:
  - `sim-baseline` adds
    `-sim.baseline-write=internal/scenarios/testdata/baselines/sim-{{DURATION}}.json`.
  - `sim-fidelity` adds `-sim.turn-executor={{EXECUTOR}} -sim.baseline-check=` with the same
    path.

**Done-line:**
- `just sim-baseline DURATION=1d` and `DURATION=1w` on unmodified code.
- Then `just sim-fidelity EXECUTOR=runwithinfo` at both durations passes.
- If a key outside the exclusions differs, stop and report it.
- `just test`. Commit the two baselines.

### P2 — Mock model endpoint · expert · Status: not started

**Scope:** new file `internal/model/mockserver.go`. The OpenAI wire types it reuses are
unexported (`grep -n "^type wire" internal/model/http_client.go internal/model/http_stream.go`),
so the file sits in `internal/model`.

**API that P4 and P5 consume:**
- `func NewMockServer(m *MockClient) *MockServer`. `*MockServer` is an `http.Handler`. The
  executor builds it over `h.Mock`.
- `func (s *MockServer) Enqueue(rs ...Response)`: served before the fallback to `m`'s current
  response (set by `stepSetup`, harness_run.go:250).
- `type ServedRequest struct { Arrived time.Time; Duration time.Duration; Messages int;
  SystemMessages int; SystemHash string; Usage Usage }`
- `func (s *MockServer) TakeRequests() []ServedRequest` drains the records since the last call.
- `func SystemPromptHash(text string) string`: SHA-256 hex of the exact bytes.
  - The mock applies it to the content of the request's `system` message as received.
  - The memory service applies it to the system text it composed.
  - `SystemHash` is `""` when the request has no system message.

**Work:**
- Serve `POST …/chat/completions` as SSE only (F7) and `GET …/models`.
- Tool-call arguments must parse in opencode's openai-compatible route.
- Usage goes on a final `choices: []` chunk (PP §1).
- No test file.

**Done-line:** `just build`.

### P1 — Probe-local turn phases + fidelity run · expert · Status: not started

Depends on P0.

**Scope:**
- New file `internal/turn/probe_phases.go`: a `Turn` record, the six phases and
  `RunTurnInProcess`. Its signature matches `RunWithInfo` without `out`:
  `RunTurnInProcess(ctx, state, preEvents, userInput)`.
- Harness seam in `internal/scenarios/harness.go`, `harness_setup.go` and `harness_run.go`:
  - `type TurnExecutor func(ctx context.Context, h *Harness, step Step) (string, turn.TurnInfo,
    error)` and a field `Scenario.TurnExecutor` (F9).
  - `stepExecTurn` (harness_run.go:314) calls the executor when it is set, and `RunWithInfo`
    when it is nil.
  - Executors read `h.State` on every call, because `restartSession` replaces it (:741).
- `internal/scenarios/sim/sim_rung_test.go` `runSimRung` sets `sc.TurnExecutor` from
  `*simTurnExecutor`, as it reads `*liveEmbedding` (:304). Add the flag value `phases`.

**Work:**
- `RunTurnInProcess` copies the consult loop with all three re-prompt branches (F1, F3).
- It omits the tool-round branch (F2), and `emitPhase`, `notify` and `crashpoint.At`, because
  the harness passes `io.Discard` and arms no crashpoints.

**Phase split (settles design open item 1):**

| Phase | Covers (turn.go lines) | opencode trigger (P4) |
|---|---|---|
| `BeginTurn` | 760-899: budget, reject, barrier, reset, snapshot, `TurnNumber++`, prompt journal, staging GC, pre-event and `user.prompt` deltas | `session.prompt` |
| `ComposeRequest` | 911-935 and 986: system prompt and bounded history tail | `session.context` |
| `IngestToolResult` | `boundTaskResultDeltas`, then `onContextDelta` per delta | `tool.execute.after` |
| `IngestModelResponse` | 1202-1309: tag strip, response journal, ceiling, telemetry and empty logs, `model.response` delta | turn close |
| `CloseTurn` | 1315-1432: engagement, closure scan, `CommitTurn`, `MaybeGC`, `SaveWorkingSet`, history append and cap. Idempotent per `Turn`: a second call returns the first result | turn close |
| `AbortTurn` | the deferred rollback (865-869); a no-op after canonical start | `session.execution.failed` / `.interrupted` |

- The `Turn` record carries `turnID`, the snapshot, `canonicalStarted`, the journal duration
  and the barrier result.
- Callers serialize every phase call on one mutex.

**Done-line:**
- `just build`.
- `just sim-fidelity EXECUTOR=phases DURATION=1d` and `DURATION=1w` both pass. On a difference
  outside the exclusions, fix the copy, not the comparator.
- `just test`.

### P3 — Driver core: spawn and isolate opencode · expert · Status: not started

Depends on P2.

**Scope:**
- New package `internal/scenarios/opencode/`, file `driver.go`.
- `internal/scenarios/metric_keys.go`: add `oc_user_db_touched`.

**Work:**
- **Spawn:** `opencode serve --stdio --port 0` with a random `OPENCODE_PASSWORD`. Read the JSON
  `{url}` from stdout line 1. Use Basic auth `opencode:<password>` (PP §2).
- **Environment** (F6):
  - `HOME`, `TMPDIR` and `XDG_{CONFIG,DATA,STATE,CACHE}_HOME` under the run home.
  - `OPENCODE_CONFIG=<runHome>/opencode.json`.
  - `OPENCODE_DISABLE_PROJECT_CONFIG=1`, `OPENCODE_DISABLE_CLAUDE_CODE=1`,
    `OPENCODE_DISABLE_MODELS_FETCH=1`.
- **Workspace:** `<runHome>/workspace`, its own `git init`, with `user.name` and `user.email`
  set locally. opencode's cwd is the workspace.
- **Config** (F7; PP §1):
  - `providers.mock` with `package: "@ai-sdk/openai-compatible"` and `settings.baseURL` set to
    the mock URL.
  - `model: "mock/mock-model"`.
  - `compaction.auto: false`.
  - `permissions: [{action:"*", resource:"*", effect:"allow"}]`.
  - `plugins` entry: the `opencode-plugin/` directory, with options `{url: serviceURL}` only.
  - `update: "disable"` and `share: "disabled"`.
- **Shutdown:** close stdin, then reap the process (PP §2: about 0.04 s, exit code 0). Follow
  CONVENTIONS.md "Spawned processes": a deadline, kill the recorded PID in `t.Cleanup`, verify it
  is dead.
- **User-DB guard:** after stop, copy the real `~/.local/share/opencode/opencode.db` and its
  `-wal` into the run home (never open the originals), open the copy read-only, and set
  `oc_user_db_touched` to 1 if any `session_v2.directory` is the probe workspace or under it.
  Size and mtime are not used: the user's own opencode service writes that file during runs (F6).

**Done-line:** `just build`. The first exercise is P4's `probe-smoke`.

### P4 — Memory service, plugin, one-step executor, smoke · expert · Status: not started

Depends on P1 and P3.

**Scope:**
- `internal/opencodehook/service.go`: loopback HTTP; each handler calls one phase. Constructor
  `NewService(state func() *turn.State, cadence int, dropSource string)`.
- `opencode-plugin/index.ts`: Promise API (`{id, setup}`).
- `internal/scenarios/opencode/executor.go`: the per-step `scenarios.TurnExecutor`.
- `internal/scenarios/opencode/smoke_test.go`: `TestProbeSmoke`.
  - Runs one handwritten step with PreEvents `fs.read`, `fs.write`, `fs.commit` and a tagged
    `MockResponse`, through `RunScenario`, at cadence 1.
  - Takes the flag `-probe.drop-source`.
  - Skips unless `PERSONANT_OPENCODE_PROBE=1`.
- `internal/scenarios/metric_keys.go`: add `oc_missed_deltas`, `oc_hook_fallbacks`,
  `oc_hook_rtt_ms`, `oc_uncomposed_requests`, `oc_history_messages`, `oc_turn_messages` and
  `checkpoints_performed`.
- `justfile`: `DROP_SOURCE`; target `probe-smoke`, which runs
  `PERSONANT_OPENCODE_PROBE=1 go test ./internal/scenarios/opencode -run TestProbeSmoke -count=1 -v -probe.drop-source={{DROP_SOURCE}}`.

**Plugin** ("Components" item 5; "Integration shape" §§1-5):
- Every RPC carries `AbortSignal.timeout`. On failure, leave the event unmodified and increment
  a fallback count.
- Each RPC carries the plugin-measured RTT of its previous RPC tagged with that RPC's turn number
  and hook name, the cumulative fallback count, and the plugin's own sequence number for
  forwarded events. The service books each RTT to the turn it measured; the last RPC of the run
  has no RTT, and that is the only one missing.
- `session.title` and `session.compaction` return fixed `result`s.
- Subscribe to `session.text.ended`, `session.step.ended`, `session.usage.updated`,
  `session.execution.succeeded` / `.failed` / `.interrupted`, and the fork, revert and delete
  events. Never `session.status` or `session.idle` (F4).

**Composition** (`session.context`):
- The answer is exactly one non-empty system part (the composed text) and the history tail as
  plain user/assistant messages, each with exactly one non-empty text part.
- Never put a `system` role in `messages` (F8).
- The plugin keeps opencode's own message objects from this turn's user message onward, so tool
  pairs stay intact; the service decodes only role and text (open item 2).
- The service records `SystemPromptHash` of each composed system text, per turn.
- The plugin reports the hook input's message count (`oc_history_messages`) and this turn's
  suffix length (`oc_turn_messages`). The suffix runs from this turn's user message to the end
  of the input, at the turn's last call.
- Write the first hook input to the run home as the captured sample.

**Tool results:** `read` → `fs.read`, `write` → `fs.write`, `shell` running `git commit` →
`fs.commit` (path parsed from the scripted command). Content is the tool output as returned
(open item 3).

**Turn close** (F5; open item 4):
- At `experimental.idle` the plugin only asks the service whether to checkpoint. The service
  answers yes at most once per turn: on the first idle of a turn whose number is a multiple of
  `cadence`, and no on every later idle of that turn (F5: idle fires again after the hook
  enqueues a checkpoint). On yes the plugin calls `session.compact`.
- On the `session.compaction` hook, the service increments `checkpoints_performed`.
- The service runs `IngestModelResponse` and `CloseTurn` on `session.execution.succeeded`:
  - the response body is the turn's buffered `text.ended` texts
  - usage comes from the last `step.ended` received before it
- A repeated close for the same turn is a no-op. `.failed` or `.interrupted` → `AbortTurn`.
- The service signals turn completion to the executor in-process. The executor waits on that
  signal, not on the event stream, so the next `BeginTurn` cannot overtake the close.

**Executor (per step):**
- Write each `fs.read` path's content into the workspace.
- `Enqueue` one tool-call response carrying one tool call per PreEvent, in PreEvent order:
  `fs.read` → `read`, `fs.write` → `write`, `fs.commit` → `shell`
  `git add <path> && git commit -q -m probe`. A step with no PreEvents enqueues nothing.
- `POST …/prompt` with `UserInput`. Wait for the service's close signal. Return its `TurnInfo`.
- **Expected-delta set:** `user.prompt` keyed by a hash of `UserInput`; one entry per PreEvent
  keyed by (source, path); `model.response` keyed by a hash of the served
  `MockResponse.Content`.
- **`oc_missed_deltas`** counts, per turn: expected entries absent from the service's
  applied-delta ledger, a `model.response` content mismatch, and a usage mismatch against the
  last `ServedRequest.Usage`.
- **`oc_uncomposed_requests`** counts `TakeRequests()` entries whose `SystemMessages != 1` or
  whose `SystemHash` is not among this turn's composed hashes. Model calls are counted at the
  mock, never from `session.model.request` (F5).
- `dropSource` skips applying the first delta of that source, to prove the detector fires.

**Done-line:**
- `just probe-smoke` completes one turn and writes a metrics blob with `oc_missed_deltas`,
  `oc_hook_fallbacks`, `oc_uncomposed_requests` and `oc_user_db_touched` all 0, and
  `checkpoints_performed >= 1`.
- `just probe-smoke DROP_SOURCE=fs.write` writes `oc_missed_deltas >= 1`.
- `just test`.

### P5 — Workload wiring, run metrics, trace, `sim-opencode` · expert · Status: not started

Depends on P4.

**Scope:**
- `internal/scenarios/sim/sim_rung_test.go`: executor value `opencode`.
- `internal/scenarios/sim/sim_test.go`: flags `-sim.checkpoint-cadence` and `-sim.trace-check`;
  the post-run criteria log.
- `internal/scenarios/opencode/executor.go`.
- `internal/scenarios/metric_keys.go`: add `oc_step_overhead_ms`, `oc_db_bytes`,
  `oc_opencode_rss_bytes` and `oc_turns_per_sec`.
- `justfile`: `CADENCE` and `TRACE_CHECK`; target `sim-opencode`.
  - It runs TestSim with `-sim.turn-executor=opencode -sim.checkpoint-cadence={{CADENCE}}
    -sim.trace-check={{TRACE_CHECK}}` and the live flags pinned to `false`.
  - It first checks that `opencode --version` contains `v2.2.` and that the clone matches the
    installed release: `git -C ../opencode describe --tags --exact-match` equals the last
    whitespace-separated field of `opencode --version` (`opencode v2.2.3` → `v2.2.3`). It fails
    naming the mismatch otherwise.
  - It is not part of `just test`.

**Metric kinds** (all `oc_*` keys, P3-P5): counters — `oc_missed_deltas`, `oc_hook_fallbacks`,
`oc_uncomposed_requests`, `checkpoints_performed`; gauges — `oc_user_db_touched` (0/1),
`oc_db_bytes`, `oc_opencode_rss_bytes` (per sim-day), `oc_turns_per_sec` (turns ÷ wall seconds
of the turn loop); histograms — `oc_hook_rtt_ms`, `oc_history_messages`, `oc_turn_messages`,
`oc_step_overhead_ms`. Slopes and maxima in the pass criteria are computed from the per-turn
values in `oc_trace.jsonl`, not from the histograms.

**Work:**
- `oc_step_overhead_ms` = the turn's wall time at the executor − the sum of that turn's
  `ServedRequest.Duration` − the hook RTTs booked to that turn.
- Per sim-day (when `scenarios.SimDayIndex(h.PinnedClock())` advances), record:
  - `oc_db_bytes`: isolated `opencode.db` + `-wal`
  - `oc_opencode_rss_bytes`: RSS of the recorded PID
- **Trace.** Write `oc_trace.jsonl` in the run home, one line per turn, holding no IDs, paths,
  ports or timestamps.
  - Exact fields: the composed system hashes; `oc_history_messages`, `oc_turn_messages` and
    context bytes per `session.context` call; the applied-delta ledger as (source, SHA-256 of
    content); session-scoped event and hook names in plugin-sequence order; token counts.
  - Timing fields: overhead and RTTs.
- **`-sim.trace-check=<path>`:**
  - Compare the exact fields line by line with the given trace. Log the first differing turn and
    call `t.Errorf`.
  - From the timing fields of both runs, log the run-median spread of `oc_step_overhead_ms` (and
    of each hook's RTT) and the median per-turn cross-run CV.
- **Post-run.** Log PASS or FAIL with values for each opencode-in-loop pass criterion below,
  judging growth against the PP references (s₀, σ). P7 re-judges every `CADENCE=8` run's growth
  with run 3's σ. Log
  the recall-key deltas against the committed baseline for the duration (P0 comparator, report
  only).
- The PreEvent ordering change (tool deltas after `user.prompt`) and open item 3 are reported,
  not fixed.

**Done-line:** `just sim-opencode DURATION=1d` completes and writes every P3-P5 `oc_*` key,
`checkpoints_performed` and `oc_trace.jsonl`, and logs the criteria lines. Then `just test`.

### P6 — Rungs and repeat · mechanical · Status: not started

Depends on P5.

**Runs:**
1. `just sim-opencode DURATION=1d CADENCE=1`.
2. `just sim-opencode DURATION=1d CADENCE=8`.
3. `just sim-opencode DURATION=1d CADENCE=8 TRACE_CHECK=<run 2's oc_trace.jsonl>`. This is the
   exact-repeat gate. Its logged spread and CV replace the PP references in the growth
   criterion.
4. From run 2's `oc_turns_per_sec` and `turns`, pick the longest of `7d`, `15d` and `30d` with
   days × turns ÷ `oc_turns_per_sec` ≤ 14,400 s. Run it at `CADENCE=8` (open item 6). If none
   fits, run `7d` and record its projected time beside the result.

**On failure:** if a run fails or a gate fails, stop and report the log tail. Do not diagnose.

**Done-line:** four rundata directories, each with a metrics blob and `oc_trace.jsonl`.

### P7 — Results document · expert · Status: not started

Depends on P6.

**Scope:**
- New `OPENCODE_PROBE_RESULTS.md`.
- One line in ACTIVE_DESIGN.md "Sequence" step 5 pointing to it.
- New throwaway `internal/turn/probedeps/main.go` (package main).
- `justfile` target `probe-deps` running `go run ./internal/turn/probedeps`.

**Dependency report.**
- `probedeps` type-checks package `internal/turn` from source. It uses stdlib `go/parser`,
  `go/types` and `go/importer.ForCompiler(fset, "source", nil)`; no `go/packages` (F11), so no
  new dependency.
- It lists every identifier used in `probe_phases.go` and defined elsewhere in `internal/turn`:
  functions, methods, variables, constants and fields.
- Each entry is marked exported or unexported, with its definition site.
- Paste the output into the results as the interface the real runtime inversion must provide.

**Content:**
- Every metric per run, with rundata paths.
- PASS or FAIL against the opencode-in-loop pass criteria below.
- The dependency report and the phase table as built.
- What opencode's hooks gave and did not give, and surprises.
- Open items 2, 3, 4 and 6 dispositions. Items 5, 7 and 8 are out of scope, with the design's
  reasons.
- The fixed comparison exclusions.

Write no line counts.

**Done-line:** `just probe-deps` runs. Every criterion below has a measured value or a stated
reason it has none.

## Pass criteria for opencode-in-loop runs

These replace ACTIVE_DESIGN.md "Pass criteria" for the probe. Warm-up is the first 20 turns.

- **Zero counts:** `oc_missed_deltas`, `oc_hook_fallbacks`, `oc_uncomposed_requests` and
  `oc_user_db_touched` are all 0.
- **Exact repeat:** the P6 run-3 trace check finds no differing turn. PP §3 measured context
  content and size, applied deltas, tool output, event order and token counts identical across
  5×500-turn repeats.
- **Rows read:** max `oc_history_messages` ≤ 1 + CADENCE × (max `oc_turn_messages` + 1).
  Reference: PP §4 held flat at 14 messages, about 9.5 KB, at cadence 8.
- **No growth beyond opencode's own** (`CADENCE=8` runs):
  - Fit an OLS slope s of `oc_step_overhead_ms` against turn index after warm-up, over N turns.
  - Pass if s × N ≤ s₀ × N + σ, where s₀ is opencode's own checkpointed slope and σ is the
    run-median spread.
  - Initial references, from PP: s₀ = 0.14 ms per 1000 turns (§4); σ = 1.6 ms (§3, run-median
    spread at 63 ms/turn, CV 0.009; per-turn cross-run CV 0.067).
  - P6 run 3 re-measures σ under the full memory service, and that value is the one used.
  - For each hook's `oc_hook_rtt_ms`, the slope × N ≤ that hook's σ from run 3.
- **Existing harness invariants** pass as in `just sim`.
- **Reported, not gated:** `oc_db_bytes` (PP §4: about 4.2 KB/turn whatever the cadence);
  `oc_opencode_rss_bytes` (PP §4: about 1 GB); recall deltas against the in-process baseline;
  the CADENCE=1 vs 8 sweep; `oc_turns_per_sec`.

## Sequencing

| Wave | Packages | Notes |
|---|---|---|
| 1 | P0 ∥ P2 | disjoint files |
| 2 | P1 ∥ P3 | P1 needs P0; P3 needs P2 |
| 3 | P4 | needs P1, P3 |
| 4 | P5 | |
| 5 | P6 | |
| 6 | P7 | |

Parallel packages commit only their own files, one commit at a time (shared tree, `just test`).

## Design open items

| # | Disposition |
|---|---|
| 1 | Settled in P1's phase split |
| 2 | P4: role and text decode only, plus captured sample; `oc_hook_fallbacks` and `oc_uncomposed_requests` measure it |
| 3 | P4/P5: tool output as returned; recall deltas reported; P7 records them |
| 4 | P4: close on `session.execution.succeeded` with buffered `text.ended` and the last `step.ended` (F5); `oc_missed_deltas` checks it |
| 5 | Out of scope: the probe does not edit the plugin mid-run (the plugin directory is file-watched, PP §6.11) |
| 6 | P6 sizes the longer rung from `oc_turns_per_sec` |
| 7 | Out of scope: version pinned (`sim-opencode` checks `v2.2.`) |
| 8 | Out of scope: not exercised |

## Post-sprint review

A single spot check replaces the standard architect and coder review, because the probe will not
be built on.

- **Reviewer and inputs:** one architect, with pre-reading THESIS.md, SPEC.md, ARCHITECTURE.md,
  CONVENTIONS.md, ACTIVE_DESIGN.md "First work: the end-to-end probe", and
  OPENCODE_PREPROBE_RESULTS.md. The reviewer does not receive this plan.
- **Questions:**
  1. Does each `oc_*` metric and each reported harness metric measure what its definition says?
     Look for a vacuous series, a silently-zero counter, or a seam that passes while measuring
     nothing. Check `oc_uncomposed_requests` against the mock-side records.
  2. Can the missed-event gate fire? Check the `DROP_SOURCE` smoke result. Check that response
     and usage mismatches are counted.
  3. Does isolation keep the user's opencode database and config untouched? Check
     `oc_user_db_touched`, the P3 environment and the workspace git root.
  4. Does the fidelity run compare what it claims? Check the exclusions against the fixed list.
     Check that the exact-repeat trace holds no IDs, paths or timestamps.
- **Out of scope:** production quality, style and test coverage.
- **Burndown:** only for a measurement defect, in `ACTIVE_PLAN_REVIEW_BURNDOWN.md` (with the
  prompt-engineer pass before dispatch), fixed before P7's numbers are final.
