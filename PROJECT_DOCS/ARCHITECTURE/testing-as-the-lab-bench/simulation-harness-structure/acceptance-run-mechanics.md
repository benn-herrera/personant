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

