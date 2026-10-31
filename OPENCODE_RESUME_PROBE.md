# opencode stop/kill-and-resume probe: results

**Question.** When an opencode session is stopped or killed and later resumed, does the memory service keep full
control of what the model sees from the first post-resume request on, and does memory state stay consistent?

**Verdict: full control after resume — CONDITIONAL.** Measured as built: **no.** Under the managed server
(`opencode serve --service`, which the default TUI connects to), a kill while an execution holds opencode's claim
makes opencode resume the turn by itself at boot, and that model request was not composed by the service (1
uncomposed request in each of `service-idle` and `service-tool`). The route stays open: that resumed request still
passes through the `session.context` hook (measured), so the service can compose it. The condition is that the
service composes a `session.context` call that has no `session.prompt` before it, and takes in the turn opencode
resumes. The probe service does neither (§5). Under the standalone server (`serve --stdio`, TUI `--standalone`),
and after a clean stop between turns in either mode, every model request after the restart was composed.

**Conventions.** Unlabelled lines are measured facts, with their source. Lines starting **Judgment:** are
interpretation. opencode paths are in `/Users/agent-user/projects/opencode` at tag v2.2.3, the installed binary's
version. Logs are under `.claude-temp/` (untracked); run artifacts are under `test/rundata/` (gitignored).

## 1. Runs and artifacts

- `just probe-resume` runs `TestProbeResume` (`internal/scenarios/opencode/resume_test.go`), with subtests
  `{stdio,service}-{stop,idle,succeeded,tool}`. `just RESUME_CASE=<regexp> probe-resume` selects subtests.
- The cited run's log is `.claude-temp/resume-final.log`. Earlier runs gave the same opencode-side outcomes:
  `.claude-temp/r4-all.log` and `.claude-temp/r5-all.log`. `r4` predates the setup Reconcile in §5.2.
- Transcripts are `test/rundata/probe-resume-<case>.opencode/transcript.json`, from
  `GET /api/experimental/session/:id/export`. The one-line-per-message summary is
  `.claude-temp/resume-final-transcripts.txt`, produced by `python3 -I .claude-temp/resume_transcripts.py test/rundata`.
- `just probe-smoke` still passes after the driver and executor changes (`.claude-temp/resume-smoke-regress.log`).

**Shape of every case.** Five turns run against mock inference at checkpoint cadence 2.
- Turn 1 does read+write+commit.
- Turn 3 is the break turn.
- Turn 4 has `RestartSession: true` and a read tool.
- At the break, opencode is SIGKILLed through its recorded process group. For `stop`, opencode is stopped cleanly
  after turn 3 instead.
- The memory service "dies" at the same point: from that RPC on, it answers nothing (`breakGate`).
- The memory service relaunches as production does (`internal/chat/chat.go:143,321`): a fresh
  `fileadapter.NewFileAdapter` over the same home, `Reconcile`, `scenarios.AssertPostRecovery` (with the ≤1-turn
  oracle), then `turn.LoadSession`. The harness's `RestartSession` then reloads once more with its ack policy.
- Next, a fresh `opencodehook.Service` starts on a new port. opencode then starts on the same isolated data home
  (§5.2), and the same session ID is prompted.
- Before turn 4's prompt, the probe waits for anything opencode does on its own. It polls `GET /api/session/active`:
  3 s for the session to start draining, then until the drain ends. Model requests and RPCs seen in that window are
  booked separately.

Break points (the RPC the dying service never answers):

| Point | RPC held | opencode state at the kill |
|---|---|---|
| `idle` | first `hook.session.experimental.idle` of turn 3 | response stored; execution claimed |
| `succeeded` | `session.execution.succeeded` event | turn terminal; claim already released |
| `tool` | first `hook.tool.execute.after` (read); a `sleep 20` shell is still running | execution claimed |
| `stop` | none; `Driver.Stop` (stdin close / SIGTERM) after turn 3 closed | idle, no claim |

## 2. What opencode does on server start with an in-flight execution

- **The claim.** Turn start writes a durable claim, `session_v2.time_suspended`, in the same transaction as
  `session.execution.started`. Success, failure and user interrupt release it. A shutdown interrupt keeps it
  (`packages/core/src/session/execution.ts:73-83,110-146`; `store.ts:218-237`). So SIGKILL, and a graceful stop with
  a turn in flight, leave the same signature.
- **Who sweeps.** `SessionRestart.resumeSuspendedSessions` (`execution/restart.ts:231-269`) runs only where a host
  calls it:
  - the managed server at boot (`packages/server/src/process.ts:109-112,245-247`);
  - `fetch.ts:47` and `sdk/src/internal/host.ts:56`.
  - The CLI passes a lifecycle to `ServerProcess.start` only in `--service` mode
    (`packages/cli/src/server-process.ts:146-160`). `serve --stdio` and the TUI's `--standalone` therefore never
    sweep. The doc comment says unregistered servers never sweep (`restart.ts:56-62`).
- **What the sweep does** for each suspended top-level session (`restart.ts:259-266`):
  1. `prepareResume` counts the attempt durably (`store.countResume`). Past `maxAttempts` (default 10) it publishes
     `Execution.Failed` instead (`restart.ts:78-92`).
  2. It publishes `SessionEvent.Synthetic` with "The server restarted while you were working. Continue from where you
     left off without repeating completed work." (`restart.ts:16-17,93-98`).
  3. It calls `execution.resume`, which is `coordinator.run`, which is `start(key, force=true)`
     (`run-coordinator.ts:119-129`). `force` skips the pending-input check (`runner/llm.ts:69-74`), so a model step
     runs with no new user prompt.
  - No `session.prompt` hook fires for this. That hook fires only when a prompt is admitted
    (`session/prompt.ts:42`), and the sweep publishes its synthetic message directly.
  - The resumed step's request does go through the `context` hook
    (`runner/llm.ts:257-271` → `model-request.ts:426`).
- **Interrupted tools.** Every drain first marks `streaming`/`running` tool parts failed with "Tool execution
  interrupted" (`runner/llm.ts:77,409-432`). Tools are not re-run.
- **Measured hooks and events of a resumed execution** (`resume-final.log:66,89`): `hook.session.context` (which the
  service refused: "/context 409: context with no open turn" in
  `test/rundata/probe-resume-service-idle.opencode/opencode.stderr`), `hook.session.model.request:primary`,
  `session.text.ended`, `hook.session.experimental.idle`, `session.usage.updated`, `session.step.ended`,
  `session.execution.succeeded`. There was no `hook.session.prompt`.

## 3. Per-scenario results

All values are from `.claude-temp/resume-final.log`.

Column key:
- **Resumed by itself:** whether opencode drained the session before the next prompt.
- **Pre-prompt requests:** model requests opencode made in that window; the service composed none of them.
- **Uncomposed / missed:** counts for turns 4–5 (`oc_uncomposed_requests`, `oc_missed_deltas`).
- **First post-restart request composed:** the startup-ordering check (scenario 3).

| Case | Claim after break | Resumed by itself | Pre-prompt requests | Uncomposed / missed | First post-restart request composed | Test |
|---|---|---|---|---|---|---|
| stdio-stop | null | no | 0 | 0 / 0 | yes | PASS |
| stdio-idle | set | no | 0 | 0 / 0 | yes | PASS |
| stdio-succeeded | null | no | 0 | 0 / 0 | yes | PASS |
| stdio-tool | set | no | 0 | 0 / 0 | yes | PASS |
| service-stop | null | no | 0 | 0 / 0 | yes | PASS |
| service-idle | set | **yes** | **1** (5 messages, opencode's own system) | 0 / 0 | **no** | FAIL |
| service-succeeded | null | no | 0 | 0 / 0 | yes | PASS |
| service-tool | set | **yes** | **1** (7 messages, opencode's own system) | 0 / 0 | **no** | FAIL |

- The resumed execution consumed the plugin's only fallback: `oc_hook_fallbacks` was 1 after the restart in
  `service-idle` and `service-tool`, and 0 elsewhere (`resume-final.log:70,93`).
- **Stale claim (stdio).** Under stdio, the claim set by the kill stays set after the restart
  (`resume-final.log:22,44`). It is released by turn 4's `execution.succeeded`, and the final claim is null
  (`:25,47`).
- **Judgment:** a standalone-mode kill followed by no further prompt leaves a claim. A managed server would resume it
  at its next boot if it shares that DB (`packages/cli/src/database-path.ts` picks the file by channel, not by mode).
  Not run.
- **Turn numbering.** `state.TurnNumber` restarts at 1 after every restart (turn IDs `t1-…`, `t2-…` on steps 4–5).
  Turn IDs stay unique through their nanosecond suffix (`internal/turn/turn.go:555-569`).
  - The service's cadence count restarts with it: `service.go:215` keys on `turnNum % cadence`. At cadence 2, the
    post-restart checkpoint landed on turn 5: the last `compaction` message in every transcript.
- **History.** `len(state.History)` was 4 before the break and 2 after turn 4 (`resume-final.log:19,23`).
  `LoadSession` reloads only the working set (`turn.go:507-519`), so turn 4's composed history tail was empty.
  - The plugin replaces everything before the prompt with the service's history (`opencode-plugin/index.ts:85-89`).
  - So after any restart the model sees no earlier conversation turns, only what memory threads carry, while the TUI
    shows all of them.

## 4. Memory-state consistency and the TUI transcript

- **Recovery outcome.**
  - Every kill case: `Reconcile` → `cell-4-torn-turn`, reset true (`resume-final.log:21,32,43,65,77,88`).
    `AssertPostRecovery` passed: substrate invariants, convergence, and spine change ≤1 against the pre-turn oracle
    (spine 2 before turn 3, 2 after).
  - The harness's per-step and final invariants passed in all eight cases. The only failures are the two
    full-control lines (`:71,94`).
  - Clean stops hit `cell-3-hand-edit` with no reset (`:10,54`). **Judgment:** that is harness telemetry
    (`mem.jsonl`) written into the memory home, not turn state.
- **Lost turns.** Turn 3 is lost from memory in every kill case. It is rolled back whole; the journal holds the
  prompt, and `CloseTurn` never ran. opencode's transcript keeps it:
  - `idle`, `succeeded`: a completed assistant reply (transcript message 9).
  - `tool`: an assistant message with `read` and `shell` both "error: Tool execution interrupted"
    (`resume-final-transcripts.txt`).
  - In `succeeded`, opencode had already recorded the turn as terminal and released the claim. Memory lost a turn
    the user saw complete.
- **Not ingested.** Under the managed server, opencode adds a `synthetic` "server restarted…" message (message 10)
  and a second assistant reply (message 11). Neither was ingested: `service.go:383-385` drops events when no turn is
  open.
- **No duplicates.** No delta was ingested twice. The only re-run, the resumed step, was not ingested at all.
- **TUI-visible transcript.** Every case shows all five user prompts. Checkpoint placeholders appear as
  `compaction` messages with summary `PERSONANT PROBE CHECKPOINT` after turns 2 and 5. The killed turn's `idle`
  marker is absent in the `idle`/`tool` kill cases.

## 5. What broke in the probe and what a real implementation needs

### 5.1 Probe service and plugin (held only in memory)

- **What is held in memory and lost on a kill:**
  - `opencodehook.Service`: the open `*turn.Turn`, buffered `text.ended` texts, held tool results, the report,
    `turnNum`, `ord` and stats (`service.go:109-118`).
  - `turn.State`: `TurnNumber`, `History`, the coalesce/staging buffers, `fileEdits` and the re-prompt flags
    (`probe_phases.go:70-87`).
  - The adapter's `turnScope` (`fileadapter.go:63-76`).
  - On disk there is only the prompt journal (`probe_phases.go:91`) plus working-tree writes, which `Reconcile`
    rolls back.
- **Failures in the probe:**
  - `compose` refuses a context call with no open turn (`service.go:255-257`). That refusal is the measured
    uncomposed request.
  - The plugin finds the turn's suffix by `promptID`, a per-process variable set only by the prompt hook
    (`index.ts:35,65,71,80-84`). After a restart, even an answering service would hit the plugin's own
    "user message is not in the hook input" fallback.
  - The plugin fails open: any failed RPC leaves opencode's own composition in place (`index.ts:53-57,79`). A
    service that is down when opencode's managed server boots (the sweep is forked at boot, `process.ts:109-112`)
    yields uncomposed requests.
  - Events are tied to "the current open turn", not to an execution or message ID (`service.go:382-417`).
    - In run r5, the resumed execution's `session.execution.succeeded` reached the service after opencode already
      reported the session inactive: it is missing from the RPCs booked at `.claude-temp/r5-all.log:66`.
    - **Judgment:** a late event from a resumed execution can land on the next prompt's turn and close it early.
      Not observed to do so.

### 5.2 Harness traps (fixed locally to observe the next step)

- **Data home inside the memory home.** opencode's data home was inside the memory home
  (`<RunHome>/opencode`). The first `cell-4` reset's debris sweep quarantined it whole, opencode DB included, and
  every kill case then got `404 Session not found` (`.claude-temp/r2-all.log:26`). The quarantine directory was
  `test/rundata/probe-resume-stdio-idle/recovery/quarantine/` from that run, since overwritten.
  - Fix: the resume probe uses `<RunHome>.opencode`. `Executor.Setup` keeps `<RunHome>/opencode` unless `runHome`
    is preset.
- **Uncommitted harness files.** The harness never reconciles before its first turn, so `projects/prj_1` and
  `providers.toml` stayed uncommitted. The first crash recovery quarantined or reverted them, and
  `VerifyProjectReferences` / `VerifyLastActiveValid` failed from then on (`.claude-temp/r4-all.log`, stdio-idle).
  - Fix: the resume test's Setup runs one `Reconcile`, as `internal/chat/chat.go:143` does at every launch.

### 5.3 What the real implementation needs (Judgment, from the above)

1. **Own opencode's resumption.** On a `session.context` call with no open turn, open a recovery turn and compose
   it. The turn's suffix must come from the hook input itself or from stored message IDs, not from process memory.
   Its user input is opencode's synthetic continue text, or the original prompt.
2. **Reconcile against opencode's store at service start.** opencode's DB is the durable record of what the user
   saw. Diff it (session export or messages API) against memory's last committed turn. Then re-ingest each turn
   opencode completed that memory rolled back (the `succeeded` case), or record it as lost explicitly.
3. **Correlate by ID.** Key turn open and close, tool results and events by session and message ID, so a resumed
   execution's late events cannot close another turn.
4. **Start order and failure policy.**
   - The memory service must be listening before the managed server boots, and must be reachable for its boot
     sweep.
   - Decide whether the plugin fails closed. Not established: whether a throwing `context` hook aborts the request
     (§6).
5. **Keep opencode's data out of the memory home.** Personant recovery quarantines untracked files there.
6. **Decide what survives a restart.** `History` and the turn counter (which drives checkpoint cadence) reset on
   every relaunch. Through opencode, the TUI keeps the conversation while the model loses it. Persist History, or
   rebuild it from opencode's store.

## 6. Not settled by this probe

- A graceful stop with a turn in flight was not run. From code, a shutdown interrupt keeps the claim
  (`execution.ts:126-133`), so the managed server would resume it as it does after a kill.
- A standalone-kill claim later swept by a managed server on the same DB (§3) was not run.
- Whether the plugin can fail closed: what opencode does when the `context` hook throws or the service refuses.
- The resume budget (`maxAttempts`, `restart.ts:78-92`), background jobs, subagent and shell recovery
  (`restart.ts:102-228`), and an automatic compaction interrupted mid-run (`llm.ts:379-407`) were not exercised.
- How a real model answers the resumed request: the mock served turn 4's scripted response to it.
- Each case ran once per run. Three runs (r4, r5, final) agree on the opencode-side outcome. The late-event ordering
  in §5.1 varied between runs.
