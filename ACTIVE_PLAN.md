# ACTIVE PLAN – opencode end-to-end probe

Executes [ACTIVE_DESIGN.md](ACTIVE_DESIGN.md) "First work: the end-to-end probe" and its
prerequisite "Runtime inversion", in the probe-local form stated under Decisions confirmed.
Section names below are ACTIVE_DESIGN.md's unless another file is named.

**Dispatch status: not dispatchable yet.** CONVENTIONS.md "Plan dispatchability" requires a
prompt-engineer usability review of this plan and an architect revision first. The two items
under "Needs user decision" must also be settled before P1 and P5 are dispatched.

## Decisions confirmed

- **The probe is throwaway.** It exists only to produce trustworthy data and lessons for a later
  real implementation. Write the least code that produces the measurements, with no production
  polish. Write no unit or integration tests beyond the fidelity run (P1) and the smoke run (P4).
  The missed-event detector is a metric the probe emits, not a test.
- **The runtime inversion is probe-local.** No production package changes behaviour. The phase
  orchestration (`BeginTurn`, `ComposeRequest`, `IngestToolResult`, `IngestModelResponse`,
  `CloseTurn`, plus `AbortTurn`) copies `turn.RunWithInfo`'s turn sequencing only. It calls the
  real production pieces: `MemoryOps` (including `ComposeWorkingSet`), the `onContextDelta`
  chain, closure, persistence. It never copies memory logic.
- **Fidelity gate.** The copied orchestration, driven in-process through the existing sim, must
  reproduce the committed baselines for the same seed and duration. This replaces "`just sim`
  unchanged". Any divergence is a probe defect, fixed before any opencode run.
- **Committed baselines do not exist yet.** P0 creates them from unmodified code at the path
  ARCHITECTURE.md "Acceptance-run mechanics" names (`testdata/baselines/`).
  `command ls internal/scenarios/testdata/baselines` → `No such file or directory`;
  `git ls-files | grep -i baseline` → no output.
- **Contract documents are not updated by this plan.** The TypeScript plugin conflicts with
  CONVENTIONS.md "Runtime: Go … no host runtime dependencies". CONVENTIONS.md and
  ARCHITECTURE.md are updated only if the plugin route is adopted after the probe.
- **Topic tag:** the in-band tag decision stays open. The probe parses the tag after the step ends.
  It never aborts mid-stream and never exercises the §5.5 re-issue ("Open decision: the in-band
  topic tag").
- **opencode version.** `sim-opencode` accepts 2.2.x ("Components" item 7).
  `opencode --version` → `opencode v2.2.1`. Design citations are at v2.2.2. In the opencode
  clone, `git diff --stat v2.2.1 v2.2.2 -- packages/plugin packages/core/src/session/model-request.ts packages/core/src/session/compaction.ts packages/core/src/session/runner packages/cli/src/server-process.ts packages/core/src/config/discovery.ts`
  lists only `packages/plugin/src/gui/host-apis.ts` and `packages/plugin/package.json`. The hook
  surface the probe uses is the same in both versions. Storage differs: v2.2.2 adds
  `session_part` (`history.ts` diff), so `oc_db_bytes` is a v2.2.1 measurement.
- **Gates.** Edit gate: `just build` (the plan adds no tests to `test-run`). Checkpoint gate:
  `just test`, once before each commit (AGENTS.md); every flag added here defaults to current
  behaviour.

## Facts the packages act on

- **F1. Where the model call and the re-prompts sit inside `RunWithInfo`:**
  - Command: `grep -n "^func RunWithInfo" internal/turn/turn.go` → line 747.
  - The pre-turn snapshot (`preTurn := snapshotSession(state)`, turn.go:825) is taken before
    `TurnNumber++`.
  - The prompt is journaled at turn.go:849 and the response at turn.go:1234, before the first
    canonical write.
  - `canonicalStarted = true` is set at turn.go:1315. Every error return before that line rolls
    back through `abortPreCanonical` (deferred, turn.go:865-869). After it, the journal scope is
    left open.
  - The three re-prompt causes (empty response turn.go:1036, §5.5 missing thread turn.go:1052,
    missing tag turn.go:1096) are decisions taken on the live stream inside the consult loop
    (turn.go:979-1195).
- **F2. The in-process sim runs no tool rounds.**
  `grep -rn "\.Tools *=\|ToolCalls:" internal/scenarios --include='*.go' | grep -v _test` → no
  output. The harness installs no tools and scripts no tool calls.
- **F3. The sim does exercise the §5.5 re-prompt**, on a run dated 2026-08-30 (it may predate
  current code). In `test/rundata/sim-workload-seed24095-dur24h0m0s.260830042515/logs/`, re-prompt
  lines are 241, all `cause=missing-thread`. The 216h run has 1845, all `cause=missing-thread`.
  Consequence: the fidelity copy must keep the consult loop's re-prompt branches. Under opencode
  the §5.5 fetch never fires, so recall metrics will diverge from in-process. That divergence is
  reported, not gated.
- **F4. Ack resolution is synchronous inside turn close.**
  - The resolver types are `type RecallResolver func(ctx, RecallOffer) (RecallResolution, error)`
    (internal/turn/recall.go:203) and `type ClosureResolver func(...)` (closure.go:173).
  - `stepSetup` installs scripted resolvers on `h.State` for every step
    (harness_run.go:253-254).
  - The sim workload scripts only `ClosureAck{Outcome: turn.ClosureResolved}` and
    `RecallAck{Reason: turn.DeclineNotRelevant}`. Command:
    `grep -rn "ClosureAck\|RecallAck" internal/scenarios/sim/*.go | grep -v _test` → workload.go
    1263-1264 and 2823-2824, campaign.go, intra.go, wander.go, all with those two values.
- **F5. Interrupt and turn-end events exist.**
  - `session.execution.failed` and `session.execution.interrupted` are at
    opencode/packages/schema/src/session-event.ts:252 and :259.
  - `session.text.ended` carries the full `text` of one part (:421-430).
  - `session.step.ended` carries `tokens` but no text (:361-376).
- **F6. opencode reads configuration from outside its XDG roots unless told not to.**
  - Project config discovery walks from cwd up to `/` (`fs.up` with no `stop`,
    packages/core/src/config/discovery.ts:80; packages/util/src/fs-util.ts:170-190). From
    `test/rundata/...` that walk reaches this repo's `.opencode/` and `.claude/`.
  - Env switches, in packages/cli/src/server-process.ts:117-133: `OPENCODE_CONFIG`,
    `OPENCODE_DISABLE_PROJECT_CONFIG`, `OPENCODE_DISABLE_CLAUDE_CODE`,
    `OPENCODE_DISABLE_MODELS_FETCH`.
  - Global roots use `os.homedir()` and `os.tmpdir()` (packages/util/src/global-roots.ts:4-17).
- **F7.** `command ls -la ~/.local/share/opencode/` → `opencode.db` (73023488 bytes) and `-wal`.
- **F8. opencode endpoints** (opencode/packages/protocol/src/groups):
  - `POST /api/session` (session.ts:165)
  - `POST /api/session/:sessionID/prompt` (:337)
  - `POST /api/session/:sessionID/compact` (:441)
  - `GET /api/event` (event.ts:61)
  - `POST /api/session/:sessionID/permission/:requestID/reply` (permission.ts:142)
- **F9. Adding a field to `scenarios.Scenario` breaks no literal.**
  `git grep -l '\bScenario\b' -- '*.go'` lists 17 files. A search for positional literals,
  `git grep -n 'Scenario{[^}]' -- '*.go' | grep -v 'Name:\|:\s'`, gives no output, so every
  literal is keyed.

## Packages

### P0 — Baselines and comparator · mechanical · Status: not started

**Scope:**
- `internal/scenarios/sim/sim_test.go`: add flags next to `simDuration` (line 187):
  - `-sim.baseline-write=<path>`
  - `-sim.baseline-check=<path>`
  - `-sim.turn-executor=runwithinfo`. P0 accepts only this value.
- `justfile`: add targets `sim-baseline` and `sim-fidelity` next to `sim` (`justfile:476`).
- New tracked files: `internal/scenarios/testdata/baselines/sim-1d.json` and `sim-1w.json`.

**Work:**
- After `runSimRung` returns in `TestSim`, read the blob with `readMetrics(h.MetricsPath)`
  (sim_test.go:1206).
- **Write mode:** copy the blob file byte-for-byte to the path.
- **Check mode:** compare `Counters`, `Gauges` and `Histograms` against the baseline:
  - Histograms are compared element-wise as ordered sample lists.
  - Skip any key ending `_ms`, any key containing `latency`, and the keys in a Go slice
    `baselineNondeterministic` declared beside the flags.
  - Log every differing key with both values.
  - `t.Errorf` once if any key differs.
- `sim-baseline DURATION=…` runs TestSim exactly as `sim` does, plus
  `-sim.baseline-write=internal/scenarios/testdata/baselines/sim-{{DURATION}}.json`.
- `sim-fidelity DURATION=… EXECUTOR=phases` runs the same TestSim with
  `-sim.turn-executor={{EXECUTOR}} -sim.baseline-check=…/sim-{{DURATION}}.json`.
- Run `just sim-baseline DURATION=1d` and `just sim-baseline DURATION=1w` on unmodified code.
- Then run `just sim-fidelity EXECUTOR=runwithinfo` at both durations. A key that differs there
  differs between two identical runs. Add it to `baselineNondeterministic`, with a comment
  naming the self-check that showed it, and re-run until the self-check is clean.

**Done-line:** `just sim-fidelity EXECUTOR=runwithinfo DURATION=1d` and `DURATION=1w` both pass;
`just test`; commit the two baselines.

### P2 — Mock model endpoint · expert · Status: not started

**Scope:** new file `internal/model/mockserver.go`, additive. No existing file changes.
- Wire types: `grep -n "^type wire" internal/model/http_client.go internal/model/http_stream.go`
  → `wireRequest`, `wireResponse`, `wireUsage`, `wireModelList`, `wireStreamResponse`,
  `wireStreamDelta`, `wireStreamToolCall` and others. They are unexported, which is why the file
  must sit in `internal/model`.

**Work:**
- Implement the "Components" item 1 handler, backed by a `*model.MockClient`, with a per-turn
  script queue:
  - `Enqueue([]Response)` responses are served first.
  - When the queue is empty, it falls back to the backing mock's current response. That is the
    one `stepSetup` installs with `h.Mock.SetResponse` (harness_run.go:250).
- Streamed tool calls must parse in opencode's `@ai-sdk/openai-compatible` route. Usage goes on
  the final chunk.
- Record per request: arrival time, duration, message count, and the system-prompt text hash.
  P5 uses these for `oc_step_overhead_ms` and for the uncomposed-request detector.
- No test file. P4's smoke run is the first exercise.

**Done-line:** `just build`.

### P1 — Probe-local turn phases + fidelity run · expert · Status: not started

Depends on P0 and on Needs user decision 2. The scope below is reading A. Reading B changes only
where the phases file lives.

**Scope:**
- New package `internal/opencodehook/`:
  - `phases.go` holds a `Turn` record and the six phases.
  - `inprocess.go` holds `RunTurnInProcess(ctx, *turn.State, []turn.Delta, string) (string,
    turn.TurnInfo, error)`. It copies RunWithInfo's consult loop (turn.go:979-1195), keeping all
    three re-prompt branches (F1, F3) and omitting the tool-round branch (F2). It also omits
    `emitPhase`, `notify` and `crashpoint.At`; they only present output or arm crash tests, and
    the harness passes `io.Discard`.
- New file `internal/turn/probe_export.go` (reading A). It holds exported one-line wrappers over
  the unexported pieces the copy calls:
  - `onContextDelta`, `checkUserInput`, `boundTaskResultDeltas`, `boundHistoryTail`,
    `pruneStaging`
  - `snapshotSession` with `sessionState.restore`, behind an exported opaque snapshot type
  - `abortPreCanonical`, `newTurnID`, `closeTurnAndUpdateEngagement`,
    `surfaceClosureCandidates`
  - `readPreamble`, behind an opaque type with `IsEmpty`/`LacksTag`/`IsToolRound`/`TagThreads`/
    `Head`/`Ended`
  - `missingFromActiveB`, `fetchThreadForReprompt`, `streamThroughFilter`
  - the constant `sessionHistoryCapTurns`
- **Not export-only (flagged):**
  - `ResetTurnScoped(*State)`: a copy of the per-turn reset block, turn.go:790-812.
  - Get and set accessors for `tagReprompted` and `emptyReprompted`.
  - `StructuralCounts(*State)`.
  - These touch unexported `State` fields. No existing file in `internal/turn` changes.
- Harness seam, in `internal/scenarios/harness.go` and `harness_setup.go`:
  - Add `type TurnExecutor func(ctx context.Context, h *Harness, step Step) (string,
    turn.TurnInfo, error)` and a `Scenario.TurnExecutor` field (F9: no literal breaks).
  - `stepExecTurn` (harness_run.go:314) calls it when it is non-nil and calls `RunWithInfo` when
    it is nil.
  - Executors read `h.State` on every call, because `restartSession` replaces it
    (harness_run.go:741).
- `internal/scenarios/sim/sim_rung_test.go` (`runSimRung`): set `sc.TurnExecutor` from
  `*simTurnExecutor`, the same way it already reads `*liveEmbedding` (line 304). Add the value
  `phases` to P0's flag.

**Phase split (settles design open item 1):**

| Phase | Covers (turn.go lines) | opencode trigger |
|---|---|---|
| `BeginTurn` | 760-899: budget, reject, barrier, reset, snapshot, `TurnNumber++`, prompt journal, staging GC, pre-event and `user.prompt` deltas | `session.prompt` |
| `ComposeRequest` | 911-935 and 986: system prompt and bounded history tail; the caller adds the user input and this turn's messages | `session.context` |
| `IngestToolResult` | `boundTaskResultDeltas` then `onContextDelta` per delta | `tool.execute.after` |
| `IngestModelResponse` | 1202-1309: tag strip, response journal, ceiling, telemetry and empty logs, `model.response` delta | turn close, from buffered `session.text.ended` |
| `CloseTurn` | 1315-1432: `canonicalStarted`, engagement, closure scan, `CommitTurn`, `MaybeGC`, `SaveWorkingSet`, history append and cap | `experimental.idle` |
| `AbortTurn` | the deferred rollback (865-869); a no-op after canonical start | `session.execution.failed` / `.interrupted` (F5); also a `BeginTurn` that finds an open `Turn` |

- **Journaling:** the `Turn` record carries `turnID`, the snapshot, `canonicalStarted`, the
  journal duration and the barrier result across phase calls.
- **Rollback:** the rollback trigger under opencode is `AbortTurn`.
- **Re-prompts:** the re-prompts live only in `RunTurnInProcess`. Under opencode, nothing sets
  the re-prompt flags.
- **Concurrency:** callers serialize every phase call on one mutex, because `State` is not safe
  for concurrent use.

**Done-line:**
- `just build`.
- `just sim-fidelity EXECUTOR=phases DURATION=1d` and `DURATION=1w` both pass. This is the only
  gated test in the plan.
- `just test`.

### P3 — Driver core: spawn and isolate opencode · expert · Status: not started

Depends on P2.

**Scope:** new package `internal/scenarios/opencode/` (`driver.go`).

**Work:**
- `Start(t, runHome, mockURL, pluginPath, serviceURL)` spawns
  `opencode serve --stdio --port 0` with a random `OPENCODE_PASSWORD` and reads the JSON `{url}`
  from the first stdout line ("Components" item 2).
- **Environment:**
  - Point `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`, `HOME` and
    `TMPDIR` under `runHome`.
  - Set `OPENCODE_CONFIG=<runHome>/opencode.json`.
  - Set `OPENCODE_DISABLE_PROJECT_CONFIG=1`, `OPENCODE_DISABLE_CLAUDE_CODE=1` and
    `OPENCODE_DISABLE_MODELS_FETCH=1` (F6).
- **Workspace:** a fixture workspace `<runHome>/workspace` that is its own git repository
  (`git init`). opencode's cwd is the workspace.
- **Config file:** written in opencode's JSON format, with:
  - provider `personant-mock` (`@ai-sdk/openai-compatible`, `settings.baseURL` set to the mock
    URL)
  - `compaction.auto: false`
  - the plugin entry `{package: pluginPath, options: {url: serviceURL}}`
  - permissions allowing `read`, `write` and `shell`
- **Process lifetime:** follow CONVENTIONS.md "Spawned processes":
  - `exec.CommandContext` with a deadline, in its own process group.
  - Kill by recorded PID in `t.Cleanup`.
  - Verify the process is dead, and fail if it is not.
- **API client:** create session, prompt, compact, event stream, permission reply (F8).
- **User-DB guard:** before start and after stop, `os.Stat` (never open) `~/.local/share/
  opencode/opencode.db` and its `-wal` under the real home. Emit gauge `oc_user_db_touched`
  (0/1) if size or mtime moved (F7).

**Done-line:** `just build`; exercised by P4's `probe-smoke`.

### P4 — Memory service, plugin, one-turn smoke · expert · Status: not started

Depends on P1 and P3.

**Scope:**
- `internal/opencodehook/service.go`: HTTP handlers on loopback; each handler calls one phase.
- `opencode-plugin/personant.ts`: Promise API (`{id, setup}`).
- `internal/scenarios/opencode/smoke_test.go`: one handwritten step through `RunScenario` with an
  opencode `TurnExecutor`. It skips unless `PERSONANT_OPENCODE_PROBE=1`, so `just test` compiles
  it but does not run it.
- `justfile`: add target `probe-smoke`. It sets `PERSONANT_OPENCODE_PROBE=1` and takes an
  optional `DROP_SOURCE`.

**Work:**
- **Plugin:** implement "Components" item 5 and "Integration shape" §§1-5.
  - Every RPC carries `AbortSignal.timeout`. On any failure the plugin leaves the event
    unmodified and increments a fallback count.
  - Each RPC reports the plugin-measured RTT of its previous RPC and the cumulative fallback
    count.
- **Composition:**
  - The `session.context` answer is system text, the history tail as text messages, and the
    index of this turn's user message in the hook input.
  - The plugin keeps opencode's own message objects from that index on. This keeps tool-call and
    tool-result pairs intact, so the service decodes only role and text (design open item 2).
  - Write the first `session.context` hook input as JSON into the run home, as the captured
    sample.
- **Tool results to deltas:** `read` → `fs.read`; `write` → `fs.write`; `shell` running
  `git commit` → `fs.commit`. Content is opencode's tool output as returned (open item 3).
- **Model response (open item 4):**
  - Buffer `session.text.ended` texts per turn.
  - Call `IngestModelResponse` once, inside the `experimental.idle` handler, before `CloseTurn`.
    This matches RunWithInfo's single joined-body delta.
  - Take usage from the last `session.step.ended`.
  - If the idle hook can overtake event delivery, the smoke run shows it as a missed or
    mismatched `model.response`. Fix it here.
- **Checkpoints:**
  - The `CloseTurn` answer requests a checkpoint every `CADENCE` turns, read from the plugin
    options. The plugin then calls `session.compact`.
  - The `session.compaction` hook returns a fixed placeholder `result`.
  - The `session.title` hook returns a fixed `result`.
- **Missed-event ledger:**
  - The service records each applied delta per turn as (source, path or content hash).
  - `DROP_SOURCE` makes the service skip applying one delta of that source once. This proves the
    detector can fire.

**Done-line:**
- `just probe-smoke` runs one turn through real opencode and writes a metrics blob with
  `oc_missed_deltas == 0`, `oc_hook_fallbacks == 0` and `oc_user_db_touched == 0`.
- `just probe-smoke DROP_SOURCE=fs.write` emits `oc_missed_deltas >= 1`.
- `just test`.

### P5 — Workload executor, metrics, `sim-opencode` · expert · Status: not started

Depends on P4 and on Needs user decision 1.

**Scope:**
- `internal/scenarios/opencode/executor.go`.
- `internal/scenarios/metric_keys.go`: one const block for every `oc_*` key.
- `internal/scenarios/sim/sim_rung_test.go`: executor value `opencode`.
- `internal/scenarios/sim/sim_test.go`: the post-run criteria log.
- `justfile`: add target `sim-opencode` next to `sim`. It takes `DURATION` and `CADENCE`
  (default 8), checks that `opencode --version` matches `v2.2.`, and fails with that message
  otherwise. It is not part of `just test`.

**Work:**
- **Per step** ("Components" items 2-3):
  - Map each `PreEvent` to a scripted tool call: `fs.read` → `read` (the driver first writes the
    file's content into the workspace), `fs.write` → `write`, `fs.commit` → `shell`
    `git add/commit`.
  - Enqueue those calls on the P2 endpoint as the turn's first consult.
  - Post `UserInput`, follow `/api/event` until the session goes idle, and return `TurnInfo` from
    `CloseTurn`.
- **Expected-delta set** per turn: `user.prompt`, one per PreEvent, and `model.response` whose
  content equals the mock's served body.
  - `oc_missed_deltas` counts expected entries the ledger lacks, plus response-content
    mismatches.
- **Metrics:** emit the "Components" item 6 table. Add `oc_uncomposed_requests`: primary requests
  reaching the mock whose system-prompt hash matches no `ComposeRequest` output.
  - This catches a silent `session.context` fallback that the plugin's own counter could not
    report.
  - Record `oc_db_bytes` whenever `scenarios.SimDayIndex(h.PinnedClock())` advances.
- **Ordering:** PreEvent deltas now arrive after `user.prompt`, not before it as in-process. This
  ordering difference and open item 3 are reported, not fixed.
- **Post-run:** log each "Pass criteria" line as PASS or FAIL with its values. Warm-up is the
  first 20 turns. If a committed baseline exists for the duration, log recall-key deltas against
  it using P0's comparator in report-only mode.
- **Acks:** under reading B of decision 1, the scripted resolvers installed by `stepSetup` run
  unchanged inside `CloseTurn` and no ack code is written. Under reading A, P5a applies.

**Done-line:**
- `just sim-opencode DURATION=1d` runs to completion.
- It writes a metrics blob containing every "Components" item 6 key plus
  `oc_uncomposed_requests` and `oc_user_db_touched`, and logs the criteria lines.
- `just test`.

### P5a — Acks through `permission.assert` · expert · Status: conditional

Runs only if decision 1 is settled as reading A. Depends on P5.

**Scope:**
- `internal/opencodehook/service.go`
- `opencode-plugin/personant.ts`
- `internal/scenarios/opencode/executor.go`

**Work:**
- The service installs resolvers on `h.State` that suspend `CloseTurn` and return an ask to the
  plugin's idle RPC. The plugin calls `permission.assert` and posts the answer back.
- The driver answers each prompt from the step's ack:
  - `ClosureResolved` → allow.
  - `DeclineNotRelevant` → reject with a message. That gives a `CorrectedError`, not a defect
    (opencode packages/core/src/plugin/host.ts:455-463).
  - Any other ack value fails the step.

**Done-line:** `just sim-opencode DURATION=1d` runs to completion with acks routed through
permission prompts; `just test`.

### P6 — Rungs · mechanical · Status: not started

Depends on P5, or on P5a if it runs.

**Runs:**
1. `just sim-opencode DURATION=1d CADENCE=1`.
2. `just sim-opencode DURATION=1d CADENCE=8`.
3. From run 2, take `oc_turns_per_sec` and the `turns` counter. Run the longest of `7d`, `15d`
   and `30d` whose projected wall time is at most 4 hours. Projected wall time in seconds is
   days × turns ÷ `oc_turns_per_sec`. Run it at CADENCE=8. This settles open item 6.

**On failure:** if any run fails to complete, stop and report the failing run's log tail. Do not
diagnose.

**Done-line:** each run's rundata directory exists and holds a metrics blob.

### P7 — Results document · expert · Status: not started

Depends on P6.

**Scope:**
- New file `OPENCODE_PROBE_RESULTS.md`.
- One line added to ACTIVE_DESIGN.md "Sequence" step 5, pointing to it.

**Content:**
- **Measured data:** every metric per run, with rundata paths.
- **Pass/fail** against "Pass criteria".
- **Lessons for the real implementation:**
  - what the inversion required: the export list, the flagged accessors, the phase table as
    built
  - what opencode's hooks gave and did not give
  - surprises
- **Open items:** the disposition of open items 2, 3, 4 and 6. Record 5, 7 and 8 as out of probe
  scope with the design's reasons.
- **Exclusions:** the `baselineNondeterministic` key list.

Cite run artefacts by path. Write no line counts.

**Done-line:** the file exists and every "Pass criteria" bullet has a measured value or a stated
reason it has none.

## Sequencing

| Wave | Packages | Notes |
|---|---|---|
| 1 | P0 ∥ P2 | disjoint files |
| 2 | P1 ∥ P3 | P1 needs P0; P3 needs P2; disjoint files |
| 3 | P4 | needs P1, P3 |
| 4 | P5, then P5a if reading A | |
| 5 | P6 | |
| 6 | P7 | |

Parallel packages commit only their own files, one commit at a time (shared tree, `just test`).

## Design open items

| # | Disposition |
|---|---|
| 1 | Settled in P1's phase split |
| 2 | P4: minimal decode plus captured sample; measured by `oc_hook_fallbacks` and `oc_uncomposed_requests` |
| 3 | P4/P5: tool output passed as returned; P5 logs recall deltas against the baseline; P7 records them |
| 4 | P4: idle-time ingestion of buffered `text.ended`; the smoke and `oc_missed_deltas` verify it |
| 5 | Out of scope: the probe does not edit the plugin mid-run |
| 6 | P6 sizes the longer rung from `oc_turns_per_sec` |
| 7 | Out of scope: version pinned (2.2.x check in `sim-opencode`) |
| 8 | Out of scope: not exercised by the probe |

## Resolved decisions (pending architect revision into the packages)

**1. Acks:** the scripted resolvers on `h.State`, as `just sim` uses. P5a is dropped. The probe does
not measure opencode's ask path; ACTIVE_DESIGN.md "Out of probe scope" excludes the ASK pick-list.

**2. Copied orchestration:** the phases and `RunTurnInProcess` live in one new file,
`internal/turn/probe_phases.go`. No `probe_export.go`, no wrappers, no accessors;
`internal/opencodehook` keeps only the service. P7 adds a dependency report: a scratch
`go/packages` + `go/types` program lists every identifier used in `probe_phases.go` that is defined
elsewhere in `internal/turn` (functions, methods, variables, fields), marked exported or unexported
with its definition site, and the report goes into `OPENCODE_PROBE_RESULTS.md` as the interface the
real runtime inversion must provide.

**Also folded into the revision:** cadence becomes a test flag read by the in-process memory
service, plus a `checkpoints_performed` counter; `baselineNondeterministic` is removed — the fidelity
comparison skips only wall-clock timing keys, the run's own `started_at`/`ended_at`/`labels.scenario`,
and `day_commit_bytes`, and any other differing key is a defect to report; all probe runs are
mock-inference only; pass criteria for opencode-in-loop runs are restated against the pre-probe's
measured run-to-run noise (OPENCODE_PREPROBE_RESULTS.md).


## Post-sprint review

A single spot check replaces the standard architect and coder review, because the probe will not
be built on.

- **Reviewer and inputs:** one architect, with pre-reading THESIS.md, SPEC.md, ARCHITECTURE.md,
  CONVENTIONS.md, and ACTIVE_DESIGN.md "First work: the end-to-end probe" (for the metric
  definitions and pass criteria). The reviewer does not receive this plan.
- **Questions the brief asks only:**
  1. Does each `oc_*` metric and each reported harness metric measure what its definition says?
     Look for a vacuous series, a silently-zero counter, or a seam that passes while measuring
     nothing. Check `oc_hook_fallbacks` against `oc_uncomposed_requests`.
  2. Can the missed-event gate fire? Check the `DROP_SOURCE` smoke result, and check that
     response-content mismatch is counted.
  3. Does isolation keep the user's opencode database and config untouched? Check
     `oc_user_db_touched`, the env set in P3, and the workspace git root.
  4. Does the fidelity run compare what it claims? Check the comparator's exclusions against
     `baselineNondeterministic`.
- **Out of scope for the reviewer:** production quality, style and test coverage.
- **Burndown:** only if the spot check finds a measurement defect. Write it to
  `ACTIVE_PLAN_REVIEW_BURNDOWN.md`. It gets the prompt-engineer review and revision pass before
  dispatch. The defect is fixed before P7's numbers are final.
