### WP0 — Instrumentation and the "before" run (mechanical)

Purpose: make before and after readable from `daily.jsonl`, then record "before" on the unchanged
substrate. The two new daily fields are what WP5's D1 criterion reads.

Scope:
- `internal/autogit/autogit.go`: add `IndexEntryCount(paths store.PersonantPaths, which Repo) (int,
  error)`, which opens the repo and returns the number of index entries. One new test in the
  package's tests: a repo with k staged files reports k (name it `TestIndexEntryCount`).
- `internal/memops/memops_barrier.go`: add `DailyIndexEntries int` to `DayBarrierResult`, beside
  `DailyLooseObjects` (line 35).
- `internal/memops/fileadapter/fileadapter_barrier.go` `runBarrier`: sample
  `autogit.IndexEntryCount(a.paths, autogit.Daily)` next to the existing loose-object sample
  (lines 154-158), best-effort in the same way.
- `internal/scenarios/metric_keys.go`: add `MetricDailyIndexEntries = "daily_index_entries"` to the
  day-barrier block (lines 30-43). Move `turn_commit_ms` into the registry as `MetricTurnCommitMs`,
  since it now crosses the seam.
- `internal/scenarios/harness_run.go`: record `MetricDailyIndexEntries` where line 355 records
  `MetricDailyLooseObjects`. Replace `metricTurnCommitMs` (lines 331, 369) with
  `MetricTurnCommitMs`.
- `internal/scenarios/sim/sim_rung_test.go`: the `OnSimDayClose` wrapper records the barrier gauges
  a second time when it is the one that seals (line 269 records `MetricDailyLooseObjects`); record
  `MetricDailyIndexEntries` beside it.
- `internal/scenarios/sim/telemetry.go` `dailyDelta` (lines 226-232): add
  `TurnCommitMsMeanThisDay float64 \`json:"turn_commit_ms_mean_this_day"\``, the mean over the
  `turn_commit_ms` histogram slice since the previous record (its own previous-length counter, as
  `prevHistLen` does for durations). Add
  `DailyIndexEntriesAtSeal float64 \`json:"daily_index_entries_at_seal"\``: the last
  `daily_index_entries` sample recorded since the previous daily record, 0 when no barrier sealed
  in that span.
- `internal/scenarios/sim/sim_barrier_test.go` `TestSimBarrier2Day`: add
  `scenarios.MetricDailyIndexEntries` to the gauge list at lines 47-53 (it fails when a gauge
  recorded no samples).
- `internal/scenarios/sim/sim_test.go` `TestDailyStatsSeries` (line 1703, a 2-day run that already
  reads every daily record): add a clause asserting day 1's record has
  `day_delta.turn_commit_ms_mean_this_day > 0` and `day_delta.daily_index_entries_at_seal > 0`.
  This is the telemetry unit test; it reuses the existing 2-day run instead of adding another.

Facts and references:
- `git grep -l 'DayBarrierResult' -- '*.go'`: `internal/memops/fileadapter/fileadapter_barrier.go`,
  `internal/memops/memops.go`, `internal/memops/memops_barrier.go`,
  `internal/turn/pipeline_test.go`, `internal/turn/turn.go`. The new field is additive, and none
  of these builds the struct positionally, so only the producer and the harness change.
- `git grep -n 'MetricDailyLooseObjects\|DailyLooseObjects' -- '*.go'`:
  `fileadapter_barrier.go:157`, `memops_barrier.go:32,35`, `scenarios/harness_run.go:355`,
  `scenarios/metric_keys.go:35,43`, `scenarios/sim/sim_barrier_test.go:52`,
  `scenarios/sim/sim_rung_test.go:269`. The new gauge goes everywhere the loose-object gauge goes.
- `git grep -n 'metricTurnCommitMs' -- '*.go'`: `internal/scenarios/harness_run.go:331` and `:369`
  only.
- `git grep -l 'dailyDelta' -- '*.go'`: `internal/scenarios/sim/telemetry.go` only.
- `internal/scenarios/metric_keys.go:35-37`: "MetricDailyLooseObjects samples the daily DB's
  loose-object count at each day close … taken BEFORE the barrier nukes the day's accrual." The
  new gauge is sampled at the same point.
- `TestMetricKeyTagsMatchRegistry` (`sim_test.go:1421`) pins `dailyStats` tags only; the new
  `dailyDelta` keys are derived names, not registry keys, so it needs no edit and stays in the
  done-line as a regression check.
- Interior daily records (days 1 … N−1) are written by `onDayClose` after the wrapper's
  `MaybeDayBarrier` (`sim_rung_test.go:257-271`), so each follows a seal. The final record (day N,
  `finalize`) covers the trailing partial day; a barrier seals in it only if a turn ran after the
  N×24h tick, so its `daily_index_entries_at_seal` may be 0.

Done-line:
1. `just build`; `just test-run ./internal/autogit 'TestIndexEntryCount'`;
   `just test-run ./internal/scenarios/sim 'TestSimBarrier2Day|TestDailyStatsSeries|TestMetricKeyTagsMatchRegistry'`.
2. Checkpoint: `just test`, then commit.
3. Before run: `just DURATION=9d sim`. Report the run directory
   (`test/rundata/sim-workload-seed24095-dur216h0m0s.<stamp>/`) and the commit hash; the
   coordinator records them in the Evidence column. Its `daily.jsonl` is the "before" data.
4. Before-run check:
   `jq -c '{day, tc: .day_delta.turn_commit_ms_mean_this_day, ix: .day_delta.daily_index_entries_at_seal}' <run>/daily.jsonl`.
   Expected: 9 records; `tc > 0` in every record from day 1; `ix > 0` in every record for days
   1–8; day 9's `ix` may be 0 (see the last fact above). Anything else stops the package: report
   the output.

