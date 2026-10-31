# opencode end-to-end probe: results

**What this is.** The results of the throwaway probe in ACTIVE_PLAN.md (packages P0–P7): the mock sim workload
(seed 24095, mock inference and embedding) driven turn by turn through unmodified opencode v2.2.3, with the
probe plugin (`opencode-plugin/index.ts`) and the in-process memory service (`internal/opencodehook/service.go`)
calling the probe-local phases in `internal/turn/probe_phases.go`. Results are judged against ACTIVE_PLAN.md "Pass
criteria for opencode-in-loop runs". Pre-probe measurements are OPENCODE_PREPROBE_RESULTS.md, cited as PP §n.

**Conventions.** Unlabelled lines are measured facts with their source path. Lines starting **Judgment:** are
interpretation. Rundata paths are under `test/rundata/`; logs are under `.claude-temp/` (untracked). Derived
figures (per-turn DB bytes, hashes, event-order counts, per-day curves) come from `.claude-temp/p7_metrics.py`,
output captured in `.claude-temp/p7-metrics.txt`.

## 1. Runs

| Run | Command | Rundata | Log |
|---|---|---|---|
| P4 smoke | `just probe-smoke` | `probe-smoke/` | `.claude-temp/p4-smoke-nodrop.log`, `p4-smoke-1.log` |
| P4 drop check | `just DROP_SOURCE=fs.write probe-smoke` | `probe-smoke/` | `.claude-temp/p4-smoke-drop-fswrite.log` |
| P5 repeat a | `just DURATION=1d sim-opencode` (CADENCE=8) | `sim-workload-seed24095-dur24h0m0s.261030172200` | — |
| P5 repeat b | same | `sim-workload-seed24095-dur24h0m0s.261030172402` | — |
| P6 run 1 | `just DURATION=1d CADENCE=1 sim-opencode` | `sim-workload-seed24095-dur24h0m0s.261030172716` | `.claude-temp/p6-run1.log` |
| P6 run 2 | `just DURATION=1d CADENCE=8 sim-opencode` | `sim-workload-seed24095-dur24h0m0s.261030172903` | `.claude-temp/p6-run2.log` |
| P6 run 3 | run 2 + `TRACE_CHECK=<run 2>/oc_trace.jsonl` | `sim-workload-seed24095-dur24h0m0s.261030173044` | `.claude-temp/p6-run3.log` |
| Run 4 (partial) | `just DURATION=30d CADENCE=8 sim-opencode`, stopped at sim-day 24 | `sim-workload-seed24095-dur720h0m0s.261030173227` | `.claude-temp/p6-run4.log` |

- Run 4 was stopped by direction at sim-day 24 (20,813 turns), because long-session opencode cost was already
  measured in PP §4. It wrote `daily.jsonl` through day 24 and per-day `oc_db_bytes` / RSS log lines; it has no
  metrics blob and no `oc_trace.jsonl`.
- Fidelity gate (P1): `just EXECUTOR=phases DURATION=1d sim-fidelity` and `DURATION=1w` both `ok`
  (`.claude-temp/p1-fidelity-1d.log`, `p1-fidelity-1w.log`); `just test-run ./internal/turn .` `ok`
  (`.claude-temp/p1-test-run-turn.log`).

## 2. Pass/fail summary

| Criterion | Run 1 (C=1) | Run 2 (C=8) | Run 3 (C=8) | Run 4 |
|---|---|---|---|---|
| Zero counts | PASS | PASS | PASS | no blob (stopped) |
| Exact repeat (run 3 vs run 2 trace) | — | — | **PASS** (1015 vs 1015 turns, no differing turn) | — |
| Rows read | PASS (6 ≤ 7) | PASS (33 ≤ 49) | PASS (33 ≤ 49) | no blob |
| Growth, `oc_step_overhead_ms` | not judged: C=1 | PASS (−0.354 ≤ 0.556 ms, re-judged with run 3's σ) | PASS (0.292 ≤ 0.556 ms) | no trace |
| Growth, per-hook `oc_hook_rtt_ms` | not judged: C=1 | 5 PASS / 6 FAIL (re-judged with run 3's σ) | 4 PASS / 7 FAIL | no trace |
| Harness invariants | PASS | PASS | PASS | not reached (stopped) |

Sources: `criterion` lines in `.claude-temp/p6-run{1,2,3}.log`. Run 2's logged growth lines used PP σ (1.6 ms) and
"NO σ" for hooks; the run 2 verdicts above apply run 3's σ to run 2's logged slopes, as the plan directs (§3.3).
Bound for `oc_step_overhead_ms`: s₀ × N + σ = 0.000140 ms/turn × 995 + 0.416 ms = 0.556 ms.

## 3. Metrics per run

### 3.1 Counters and gauges

From each run's `<rundata>/<name>.metrics.json`.

| Key | Run 1 (C=1) | Run 2 (C=8) | Run 3 (C=8) | P5 a | P5 b |
|---|---|---|---|---|---|
| turns | 1015 | 1015 | 1015 | 1015 | 1015 |
| `checkpoints_performed` | 1015 | 126 | 126 | 126 | 126 |
| `oc_missed_deltas` | 0 | 0 | 0 | 0 | 0 |
| `oc_hook_fallbacks` | 0 | 0 | 0 | 0 | 0 |
| `oc_uncomposed_requests` | 0 | 0 | 0 | 0 | 0 |
| `oc_user_db_touched` | 0 | 0 | 0 | 0 | 0 |
| `oc_db_bytes` (run end) | 5,821,368 | 2,606,000 | 2,606,000 | 2,606,000 | 2,606,000 |
| `oc_db_bytes` per turn | 5.74 KB | 2.57 KB | 2.57 KB | 2.57 KB | 2.57 KB |
| `oc_opencode_rss_bytes` (run end) | 747 MB | 649 MB | 656 MB | 654 MB | 601 MB |
| `oc_turns_per_sec` | 10.32 | 10.85 | 10.69 | 9.29 | 8.88 |
| turn latency P50 / P95 / mean (ms) | 83 / 166 / 91.25 | 78 / 164 / 86.57 | 79 / 167 / 87.89 | — | — |
| recall keys differing from `sim-1d.json` | 0 | 0 | 0 | — | — |

Smoke (`.claude-temp/p4-smoke-nodrop.log`): one turn, 2 model requests at the mock, 2 `session.context` calls,
2 idles; `checkpoints_performed` 1, `oc_missed_deltas` 0, `oc_hook_fallbacks` 0, `oc_uncomposed_requests` 0,
`oc_user_db_touched` 0. With `DROP_SOURCE=fs.write` (`p4-smoke-drop-fswrite.log`): `oc_missed_deltas` 1.

Harness summary lines (recall, closures, thread counts, wander, intra-thread recall) in each P6 log are identical
across runs 1–3 once latency, wall-time and path lines are set aside (`.claude-temp/p7-run{1,2,3}-harness.txt`);
full blobs are at the rundata paths above.

### 3.2 Histograms

From the metrics blobs; values in run 1 / run 2 / run 3.

| Histogram | n | p50 | p95 | max |
|---|---|---|---|---|
| `oc_history_messages` | 1093 | 2 / 10 / 10 | 5 / 19 / 19 | 6 / 33 / 33 |
| `oc_turn_messages` | 1015 | 1 / 1 / 1 | 4 / 4 / 4 | 5 / 5 / 5 |
| `oc_step_overhead_ms` | 1015 | 25.55 / 24.50 / 24.92 | 91.12 / 87.70 / 90.96 | 629.0 / 666.1 / 620.4 |
| `session.context` input bytes (trace) | 1093 | 500 / 3197 / 3197 | 1476 / 5979 / 5979 | 3069 / 9629 / 9629 |

### 3.3 Per-hook RTT, run-to-run spread and growth

Run 3 RTT medians from `oc_trace.jsonl`; σ, CV and slope × N (N = 995 turns after warm-up) from the
`trace-check timing` and `criterion hook-growth` lines of `.claude-temp/p6-run3.log`; run 2 slope × N from
`.claude-temp/p6-run2.log`. All in ms. The bound is slope × N ≤ σ (run 3).

| Series | Run 3 p50 RTT | σ (run 3) | per-turn CV | Run 3 slope × N | Run 3 | Run 2 slope × N | Run 2 |
|---|---|---|---|---|---|---|---|
| `hook.session.prompt` | 5.383 | 0.135 | 0.0404 | 0.564 | FAIL | 0.629 | FAIL |
| `hook.session.context` | 5.819 | 0.165 | 0.0343 | 5.531 | FAIL | 5.308 | FAIL |
| `hook.tool.execute.after` | 0.311 | 0.001 | 0.0515 | 0.003 | FAIL | −0.018 | PASS |
| `hook.session.experimental.idle` | 0.218 | 0.009 | 0.0634 | −0.030 | PASS | −0.015 | PASS |
| `hook.session.compaction` | 0.179 | 0.010 | 0.0688 | −0.021 | PASS | −0.153 | PASS |
| `hook.session.model.request:primary` | 0.119 | 0.003 | 0.0891 | 0.049 | FAIL | 0.038 | FAIL |
| `hook.session.model.request:compaction` | 0.124 | 0.002 | 0.0698 | 0.024 | FAIL | −0.185 | PASS |
| `session.text.ended` | 0.230 | 0.010 | 0.0687 | −0.028 | PASS | −0.032 | PASS |
| `session.step.ended` | 0.501 | 0.013 | 0.1347 | 0.171 | FAIL | 0.192 | FAIL |
| `session.usage.updated` | 0.188 | 0.007 | 0.1863 | −0.041 | PASS | 0.012 | FAIL |
| `session.execution.succeeded` | 40.922 | 1.314 | 0.0212 | 15.605 | FAIL | 15.000 | FAIL |
| `oc_step_overhead_ms` (not a hook) | 24.92 | 0.416 | 0.0311 | 0.292 | PASS | −0.354 | PASS |

`hook.session.title` and `hook.session.model.request:title` occur once per run and are not judged.

- Of run 3's seven FAILs, three are against σ < 0.01 ms (`tool.execute.after` 0.001, `model.request:compaction`
  0.002, `model.request:primary` 0.003) and one against σ 0.013 ms (`step.ended`). Their slope × N is at most
  0.171 ms over the run.
- The other three FAILs are the RPCs inside which the memory service runs a phase (`service.go`): `session.prompt`
  runs `BeginTurn` (:239), `session.context` runs held `IngestToolResult` calls and `ComposeRequest` (:258-261),
  and `session.execution.succeeded` runs `IngestModelResponse` and `CloseTurn` (:419-437). Their growth over the
  1d run is 0.564, 5.531 and 15.605 ms.
- opencode's own per-step overhead with hook and mock time removed (`oc_step_overhead_ms`) passes in both C=8 runs.

**Judgment:** the hook-growth criterion as written (slope × N ≤ that hook's σ) fails on series whose total drift is
a fraction of a millisecond, because their run-to-run spread is a few microseconds. The large drifts are in the
memory system's own phases, not in opencode's hook dispatch. This is data on where the per-turn cost sits, not a
verdict on the hook route; the memory system's growth is the open question in §4.

### 3.4 Reported, not gated

- `oc_db_bytes` per sim-day, run 4 (`.claude-temp/p6-run4.log`): 2.61 MB after 1015 turns, 15.1 MB after 4842,
  30.1 MB after 10,831, 45.9 MB after 16,900, 57.8 MB after 20,813 (2.78 KB per turn). PP §4 measured about
  4.2 KB per turn "whatever the cadence"; here cadence changes it: 5.74 KB per turn at C=1, 2.57 KB at C=8 (§3.1).
- `oc_opencode_rss_bytes`, run 4: 643 MB at day 1, 906 MB at day 16, 592 MB at day 17, 739 MB at day 24 (not
  monotonic). PP §4: about 1 GB.
- Recall deltas against the committed in-process baseline `internal/scenarios/testdata/baselines/sim-1d.json`:
  0 recall keys differ in runs 1–3 (`recall deltas` line of each log).
- Cadence sweep: C=1 vs C=8 changes `checkpoints_performed` (1015 vs 126), `oc_history_messages` max (6 vs 33),
  context bytes p50 (500 vs 3197), `oc_db_bytes` (5.82 vs 2.61 MB) and RSS (747 vs 649–656 MB). Memory state is
  byte-identical between C=1 and C=8 (§5).
- `oc_turns_per_sec`: 8.88–10.85 across the five 1d runs. PP §6.5 estimated about 20 turns/s for opencode alone.

## 4. Per-turn cost over a long run (partial data)

`per_turn_ms_mean_this_day` from `daily.jsonl` (mean wall time per turn at the harness, memory system and
opencode together). Days with 0 turns omitted.

| Sim-day | Run 4, opencode, C=8 | In-process 1w (`…dur168h0m0s.261030151228`) | In-process 9d (`…dur216h0m0s.261030144050`) |
|---|---|---|---|
| 1 | 88.38 | 73.72 | 59.86 |
| 2 | 129.70 | 79.76 | 74.67 |
| 3 | 138.35 | 83.47 | 86.32 |
| 4 | 155.80 | 95.68 | 96.20 |
| 5 | 177.54 | 119.43 | 113.90 |
| 6 | 170.73 | 121.89 | 128.54 |
| 8 | 162.90 | — | 122.06 |
| 9 | 188.18 | — | 130.19 |
| 10, 11, 12 | 185.38, 195.61, 222.99 | — | — |
| 13, 15, 16 | 226.58, 237.46, 264.98 | — | — |
| 17, 18, 19 | 286.55, 253.39, 306.87 | — | — |
| 20, 22, 23, 24 | 300.03, 313.88, 309.36, 341.12 | — | — |

- Per-turn cost grows over the run both through opencode and in-process for the overlapping days (1–9).
- No in-process run longer than 9 sim-days exists in `test/rundata/`, so days 10–24 have no in-process comparison.
- A second in-process 9d run (`…dur216h0m0s.261030155402`, 8 days, no metrics blob) reads 157.41 ms on day 6
  against 128.54 in `…144050`: same-code in-process runs differ by up to 22% on one day.
- Other workloads may have shared the machine during any of these runs; nothing recorded excludes it.
- Turn counts and spine sizes per day in run 4 match the in-process 9d run on days 1–8 (`p7-metrics.txt`).

**Open question for the in-process sim:** whether the memory system's own per-turn cost keeps growing past day 9,
and at what rate. The probe does not answer it.

## 5. Exact repeat and determinism

- Run 3's trace check against run 2: no differing turn (`.claude-temp/p6-run3.log:64`).
- The exact-field trace digest (tool and applied-delta lists sorted) is identical for runs 2, 3 and both P5 runs;
  run 1 differs, as its cadence does (`p7-metrics.txt`).
- Memory-state files (`spine.jsonl`, `symbols.jsonl`, `working-set.json`, `threads/`) are byte-identical across
  runs 1, 2, 3 and both P5 runs.
- Against the in-process 1d run `…dur24h0m0s.261030152716`: `spine.jsonl`, `symbols.jsonl` and `working-set.json`
  are identical; 78 files under `threads/` differ (33 `files.json`, 45 `thread.md`;
  `.claude-temp/p7-inproc-vs-opencode-threads.txt`).
  - Every `thread.md` difference is a `derived_from:` block present in-process and absent through opencode
    (197 blocks, 290 entries). The cause is not established.
  - `files.json` `fs.read` literals carry opencode's `read` output (a `Read file <path>, lines a-b` header and
    `N: ` line prefixes) instead of the raw file, and there are more literal entries (open item 3).
- §5.5 re-prompts: 241 `re-prompt` log lines in the in-process run, 0 through opencode (F3: the fetch never fires
  under opencode); recall keys still match.

## 6. What opencode's hooks gave, what they did not, surprises

**Gave:**
- Every delta the sim produces reached the memory service: `oc_missed_deltas` 0 in every run, and the detector
  fires when a delta is dropped (§3.1).
- `session.context` replacement reached the model byte-exact: every request at the mock carried exactly one system
  message whose SHA-256 matched a system text the service composed that turn (`oc_uncomposed_requests` 0; P4
  executor counts at the mock). Composition is one non-empty system part plus plain text messages (PP §5).
- `session.title` and `session.compaction` answered by the plugin. A title or compaction request at the mock would
  carry no composed system hash and count in `oc_uncomposed_requests`, which is 0.
- Checkpoints held `session.context` input bounded at C=8 (max 33 messages, 9629 bytes over 1015 turns).
- opencode under the full memory service is as deterministic as PP §3 found it without one (§5).

**Did not give:**
- A turn-close signal ordered after the turn's data. `experimental.idle` ran before the final `session.step.ended`
  in 1015/1015 turns of every 1d run; `session.text.ended` arrived before idle in 1015/1015 (trace event order).
- A fixed tool-result order: opencode runs one response's tool calls concurrently (`runner/step.ts:123-133`).
- The written content of a `write` call in its output (opencode `write.ts:39` returns a confirmation line).
- A raw file from `read`: its output is formatted (§5).
- The §5.5 mid-turn re-prompt (not built under opencode; topic-tag decision open).

**Lessons for the real implementation, with where each showed up:**
1. `experimental.idle` fires before the turn's final step event, and again after a checkpoint the idle hook
   enqueued: 2 idles in 1015/1015 turns at C=1 and in 126/126 checkpoint turns at C=8 (trace); PP §2.
2. Turn close must key on `session.execution.succeeded` (`service.go:411`); the idle hook only answers the
   checkpoint question (`service.go:213-215`). Usage taken there is the last `step.ended`.
3. opencode runs a response's tool calls concurrently, so the service must impose call order. P5's first full run
   applied them in arrival order and was not repeatable (commit b4e2934); holding them and applying in call order
   at the next `session.context` or close made traces and memory state identical (commit 749cea6,
   `service.go:343-359`).
4. `session.model.request` reports `title` (1 per run) and `compaction` (126 at C=8, equal to
   `checkpoints_performed`) kinds although the hooks answered both and no request was sent (trace event counts;
   PP §2). Count model calls at the provider side.
5. The `write` tool's output is only a confirmation line; `fs.write` content must come from the tool input (P4
   smoke; `service.go:307-309`).
6. Event sequence numbers must be taken in the plugin as each event arrives, with forwarding chained, because RPCs
   can reach the service out of order (`opencode-plugin/index.ts:132-139`; ACTIVE_PLAN.md P5 "Ordering in the
   trace").
7. Git author and committer dates are pinned to the sim clock in the scripted commit
   (`internal/scenarios/opencode/executor.go:184`), so the commit hash in `fs.commit` `Meta["hash"]` repeats
   across runs.
8. Prompt text is byte-exact under the single-system-part composition: `oc_uncomposed_requests` 0 in every run,
   with 1093 `session.context` calls per 1d run (PP §5).

## 7. Phase table as built

`internal/turn/probe_phases.go`; triggers in `internal/opencodehook/service.go`.

| Phase | Covers (turn.go lines, per ACTIVE_PLAN.md P1) | Triggered by in the probe |
|---|---|---|
| `BeginTurn` (:49) | 760-899: budget, reject, barrier, reset, snapshot, `TurnNumber++`, prompt journal, staging GC, pre-event and `user.prompt` deltas | `session.prompt` hook; called with no pre-events (`service.go:239`) |
| `ComposeRequest` (:113) | 911-935 and 986: system prompt and bounded history tail | `session.context` hook, after held tool results are applied (`:258-261`) |
| `IngestToolResult` (:142) | `boundTaskResultDeltas`, then `onContextDelta` per delta | `tool.execute.after`, held and applied in call order at the next `session.context` or close (`:347-359`) |
| `IngestModelResponse` (:154) | 1202-1309: tag strip, response journal, ceiling, telemetry, `model.response` delta | `session.execution.succeeded`, with the turn's `text.ended` texts joined and the last `step.ended` tokens (`:419-430`) |
| `CloseTurn` (:201) | 1315-1432: engagement, closure scan, `CommitTurn`, `MaybeGC`, `SaveWorkingSet`, history; idempotent | `session.execution.succeeded`, after `IngestModelResponse` (`:432`) |
| `AbortTurn` (:263) | the deferred pre-canonical rollback | `session.execution.failed` / `.interrupted`, or a phase error (`:413-414`, `:439-444`) |
| `RunTurnInProcess` (:274) | recomposes the phases, with the consult loop and its three re-prompt branches | `sim-fidelity EXECUTOR=phases` |

`experimental.idle` drives no phase: the service answers "checkpoint" at most once per turn, on the first idle of a
turn whose number is a multiple of the cadence. `session.compaction` only counts checkpoints.

Consequence carried into the trace: tool deltas apply after `user.prompt`, where in-process the PreEvents apply
before it (ACTIVE_PLAN.md P5).

## 8. Dependency report

`just probe-deps` (`internal/turn/probedeps/main.go`, stdlib `go/parser`, `go/types`,
`go/importer.ForCompiler(fset, "source", nil)`). Every identifier `probe_phases.go` uses that is defined elsewhere
in `internal/turn`. This is the interface the real runtime inversion must provide. Types are listed as well as the
five kinds the plan names. Output (`.claude-temp/p7-probe-deps.txt`):

```
const  unexported sessionHistoryCapTurns                        turn.go:447
field  exported   State.ActiveProject                           turn.go:92
field  exported   State.ActiveThreads                           turn.go:235
field  exported   State.Budget                                  turn.go:249
field  exported   State.Client                                  turn.go:94
field  exported   State.DormantThreads                          turn.go:243
field  exported   State.History                                 turn.go:229
field  exported   State.MaxTokens                               turn.go:210
field  exported   State.Model                                   turn.go:206
field  exported   State.Ops                                     turn.go:91
field  exported   State.Temperature                             turn.go:209
field  exported   State.Tools                                   turn.go:102
field  exported   State.TurnNumber                              turn.go:295
field  unexported State.coalesce                                turn.go:252
field  unexported State.emptyReprompted                         turn.go:279
field  unexported State.fileEdits                               turn.go:287
field  unexported State.staging                                 turn.go:302
field  unexported State.structuralCreates                       turn.go:434
field  unexported State.structuralRetires                       turn.go:435
field  unexported State.structuralWIPs                          turn.go:436
field  unexported State.tagReprompted                           turn.go:270
field  unexported State.turnOwner                               turn.go:260
field  exported   TurnInfo.Barrier                              turn.go:673
field  exported   TurnInfo.CommitDuration                       turn.go:665
field  exported   TurnInfo.GCDuration                           turn.go:667
field  exported   TurnInfo.JournalDuration                      turn.go:666
field  exported   TurnInfo.PromptTokens                         turn.go:663
field  exported   TurnInfo.Telemetry                            turn.go:679
field  exported   TurnInfo.TurnID                               turn.go:664
field  unexported preambleResult.ended                          stream.go:53
field  unexported preambleResult.tag                            stream.go:52
field  unexported preambleResult.tools                          stream.go:65
func   unexported abortPreCanonical                             turn.go:605
func   unexported boundHistoryTail                              livebudget.go:154
func   unexported boundTaskResultDeltas                         livebudget.go:129
func   unexported checkUserInput                                livebudget.go:90
func   unexported closeTurnAndUpdateEngagement                  engage.go:98
func   unexported fetchThreadForReprompt                        lru.go:215
func   unexported missingFromActiveB                            stream.go:227
func   unexported newCoalesceBuffer                             coalesce.go:31
func   unexported newStagingBuffer                              staging.go:58
func   unexported newTurnID                                     turn.go:567
func   unexported onContextDelta                                chain.go:70
func   unexported preambleIsEmpty                               stream.go:220
func   unexported preambleLacksTag                              stream.go:194
func   unexported pruneStaging                                  staging.go:107
func   unexported readPreamble                                  stream.go:83
func   unexported removeString                                  lru.go:315
func   unexported snapshotSession                               rollback.go:61
func   unexported streamThroughFilter                           stream.go:21
func   unexported surfaceClosureCandidates                      closure.go:489
method unexported coalesceBuffer.reset                          coalesce.go:73
type   exported   Delta                                         chain.go:22
type   exported   State                                         turn.go:90
type   exported   TurnInfo                                      turn.go:662
type   unexported sessionState                                  rollback.go:45
var    exported   ErrMarkerRetained                             turn.go:625

kind   exported unexported
const         0          1
field        19         12
func          0         19
method        0          1
type          3          1
var           1          0
total        23         34
```

Every function and method the phases call is unexported: the phases cannot move out of `internal/turn` without
exporting them.

## 9. Design open items

| # | Disposition |
|---|---|
| 2 Message decoding | Role and text decode only. `oc_hook_fallbacks` 0 and `oc_uncomposed_requests` 0 in every run. Captured first hook input: `<rundata>/opencode/context-sample.json`. No contract test was written (probe is throwaway). |
| 3 PreEvent fidelity | `fs.read` content is the `read` tool output as returned, so stored literals carry its header and line prefixes (§5); `fs.write` content comes from the tool input. Recall keys against `sim-1d.json`: 0 differ. `threads/` differs from in-process (§5). |
| 4 Turn boundary | Close on `session.execution.succeeded` with the buffered `text.ended` texts and the last `step.ended` tokens. `oc_missed_deltas` (which includes response-content and usage mismatches) is 0 in every run. |
| 6 Throughput | 8.88–10.85 turns/s at 1d (§3.1). Run 4 reached sim-day 24 (20,813 turns) before it was stopped; its per-turn cost rose from 88 to 341 ms/turn (§4). Long-session opencode cost, payload bound and storage growth: PP §4 (40,000 turns at cadence 8). A 120-day rung was not sized. |
| 5 Plugin reload | Out of scope: the probe does not edit the plugin mid-run (the plugin directory is file-watched, PP §6.11). |
| 7 Experimental hooks | Out of scope: the version is pinned (`sim-opencode` checks `v2.2.` and the clone tag). |
| 8 Several servers | Out of scope: not exercised by the probe. |

## 10. Fixed comparison exclusions

From ACTIVE_PLAN.md "Decisions confirmed"; the P0 comparator skips only:
- wall-clock timing keys: names ending `_ms` or containing `latency`;
- the run's own `started_at`, `ended_at` and `labels.scenario`;
- git storage byte sizes: `day_commit_bytes` and keys starting `git_` that contain `_bytes`. Git object counts are
  compared.

The exact-repeat trace check compares only the trace's exact fields (system hashes, context message counts and
bytes, the applied-delta ledger, event and hook names in plugin-sequence order, token counts), with tool entries
and the ledger compared as sorted (source, path) lists within a turn. The trace holds no IDs, paths, ports or
timestamps.

## 11. Not settled by this probe

- Why `derived_from` lineage is absent from every thread through opencode (§5). **Judgment:** a candidate is the
  ordering change (tool deltas after `user.prompt`); unverified.
- Whether the memory system's per-turn cost keeps growing past sim-day 9 (§4).
- Why the cadence changes per-turn DB growth here (2.57 vs 5.74 KB) when PP §4 found none.
- Whether `session.step.ended` and `session.usage.updated` RTT tails (p95 3–5 ms against p50 0.2–0.5 ms) are queueing
  behind the service mutex. **Judgment:** plausible, since one mutex serializes every RPC; unmeasured.
- The criteria for a run longer than 1 sim-day through opencode: run 4 wrote no blob or trace.

## 12. Reproduce

```sh
just DURATION=1d CADENCE=1 sim-opencode
just DURATION=1d CADENCE=8 sim-opencode
just DURATION=1d CADENCE=8 TRACE_CHECK=test/rundata/<run 2>/oc_trace.jsonl sim-opencode
just probe-deps
python3 -I .claude-temp/p7_metrics.py
```
