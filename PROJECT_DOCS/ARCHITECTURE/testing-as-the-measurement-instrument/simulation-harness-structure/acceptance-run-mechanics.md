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
  findability is surfaced.
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

