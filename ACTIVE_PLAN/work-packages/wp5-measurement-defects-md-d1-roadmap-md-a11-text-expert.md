### WP5 — Measurement, DEFECTS.md D1, ROADMAP.md A11 text (expert)

This plan does not tick ROADMAP A11: A11 includes re-measuring with opencode in the loop, and this
plan's runs are mock-inference only. WP5 rewrites A11's text to state what remains (step 6).
Opencode-in-loop runs are made only when they can show something a mock run cannot, not as a
routine step.

D1 closes only on SPEC §9.1's steady-state criterion
(`PROJECT_DOCS/SPEC/9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/index.md`
lines 21-23: "After 120 simulated days the trajectories of working-set size, spine size, and
per-operation latency are **flat**"), applied to per-operation latency including
`turn_commit_ms` and to the daily index entry count, over a 120-day mock run (step 4). The 9-day
before/after comparison is a progress check with no pass bar.

Definitions used by steps 2 and 5:
- A **working record** is a `daily.jsonl` record with `day_delta.turns_this_day > 0`, excluding
  the run's last record: that one is written by `finalize` and covers the trailing partial day
  (WP0 "Facts and references"). Days without turns (the workload's days off, e.g. day 7 of the
  9-day run) have zero-valued deltas and are not samples.
- The **daily series** are `day_delta.turn_commit_ms_mean_this_day`,
  `day_delta.per_turn_ms_mean_this_day`, `day_delta.latency_p95_this_day` and
  `day_delta.daily_index_entries_at_seal`.
- The **barrier series** is `.histograms.barrier_duration_ms` in
  `<run>/<run-basename>.metrics.json`: one sample per sealed barrier, in seal order.

Steps:
1. After run: `just DURATION=9d sim` on WP4's commit. Report the run directory; the coordinator
   records it in the Evidence column.
2. Progress check (no pass bar). For the "before" (WP0) and "after" runs, the mean of each daily
   series over working records of days 2–8:

   ```
   jq -s 'def win(f; lo; hi): [.[] | select(.day >= lo and .day <= hi) | f] | add / length;
     [.[:-1][] | select(.day_delta.turns_this_day > 0)]
     | {turn_commit: win(.day_delta.turn_commit_ms_mean_this_day; 2; 8),
        per_turn: win(.day_delta.per_turn_ms_mean_this_day; 2; 8),
        p95: win(.day_delta.latency_p95_this_day; 2; 8),
        index_entries: win(.day_delta.daily_index_entries_at_seal; 2; 8)}' <run>/daily.jsonl
   ```

   Report each mean for both runs and the after/before ratio, plus the after/before ratio of the
   barrier series' mean and of `day_commit_bytes`. Put the table, the run directories and both
   commits in the commit message.
3. Residual attribution: `just DURATION=8d sim-profile` and `just DURATION=9d sim-profile`, then
   `just pprof -top -cum -focus='turn\.RunWithInfo$' -diff_base=bin/profiles/sim-8d.cpu.pprof
   bin/profiles/sim-9d.cpu.pprof`. Report whether the replacement for `store.turnFileNumbers` and
   `CommitTurn` still grow, and whether folio decoding appears under `RunWithInfo`.
4. The 120-day run: `just DURATION=120d sim` on WP4's commit (overrides go before the recipe
   name). It is the default mock run (both live toggles false). Report the run directory; the
   coordinator records it in the Evidence column.
   - `120d` parses as 120 calendar days (`parseSimDuration`, `internal/scenarios/sim/sim_test.go`
     :203-216, "Whole-day `<N>d` form"); the run directory is
     `test/rundata/sim-workload-seed24095-dur2880h0m0s.<stamp>/`.
   - No recipe or timeout change is needed: the `sim` recipe already passes `-timeout 0`
     (`justfile` line 521), so `GOTESTTIMEOUT` does not apply.
   - The sim's heap watchdog caps `HeapInuse` at 8 GiB and turns an overrun into a panic plus a
     heap profile under the run directory (`internal/scenarios/sim/sim_rung_test.go:211-215`).
     A watchdog panic or any failed gate stops the package: report it with the run directory.
   - No 120-day run is on record (`command ls test/rundata | grep -o 'dur[0-9]*h0m0s' | sort |
     uniq -c` lists spans up to `dur720h0m0s`). The 9-day run held about 7,800 turns, so expect
     about 100,000 turns and several hours of wall-clock; run it in the background.
5. Flatness, from the 120-day run. The early window is working records of days 31–60, the late
   window working records of days 91–119; days 1–30 are warm-up and are not compared. For each
   daily series, ratio = mean over the late window ÷ mean over the early window:

   ```
   jq -s 'def win(f; lo; hi): [.[] | select(.day >= lo and .day <= hi) | f] | add / length;
     def ratio(f): win(f; 91; 119) / win(f; 31; 60);
     [.[:-1][] | select(.day_delta.turns_this_day > 0)]
     | {turn_commit: ratio(.day_delta.turn_commit_ms_mean_this_day),
        per_turn: ratio(.day_delta.per_turn_ms_mean_this_day),
        p95: ratio(.day_delta.latency_p95_this_day),
        index_entries: ratio(.day_delta.daily_index_entries_at_seal)}' <run>/daily.jsonl
   ```

   For the barrier series, ratio = mean of the last quarter of samples ÷ mean of the second
   quarter:

   ```
   jq '.histograms.barrier_duration_ms | (length / 4 | floor) as $q
     | (.[3 * $q:] | add / length) / (.[$q:2 * $q] | add / length)' <run>/<run-basename>.metrics.json
   ```

   This plan's flatness bar: **flat** means `index_entries` ≤ 1.10 (the count is deterministic
   per seed) and every latency ratio (`turn_commit`, `per_turn`, `p95`, barrier) ≤ 1.15. Put the
   five ratios, both window means of each series, the run directory and the commit in the commit
   message.
6. DEFECTS.md and ROADMAP.md (repo root):
   - **Flat:** remove D1 (CONVENTIONS "Docs stay current",
     `PROJECT_DOCS/CONVENTIONS/build-test/docs-stay-current.md` line 4: "A defect fix removes its
     DEFECTS.md entry in the same change"). Rewrite ROADMAP A11's text (line 167, "**A11. Fix
     D1.**"), leaving it `- [ ]`: the D1 fix has landed (commit) and is flat over a 120-day mock
     run (run directory); what remains is the re-measure with opencode in the loop, worth running
     when it can show something a mock run cannot — memory-side cost and any non-commit growth
     under real inference. Replace its `DEFECTS.md D1` reference, which no longer resolves, with
     the commit; keep the other references.
   - **Not flat:** keep D1 and update its Fact/Where to the post-folio mechanism, adding the
     120-day measurements (the five ratios and window means, run directory, commit, date) and the
     9-day progress table as dated evidence. Leave A11 unchanged.
   - Either way, non-commit growth that step 3 attributes (DEFECTS D1's `ComposeWorkingSet`,
     `ReadSpine` rows) and that the flatness ratios still show goes into a new DEFECTS entry in
     the header's form: fact, where, dated evidence.
7. The SPEC §9.1 honesty clause
   (`PROJECT_DOCS/SPEC/9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/honesty-clause.md`):
   the commit states the coverage: mock-inference simulations of 9 and 120 days, opencode not in
   the loop.

Facts and references: D1 evidence for comparison, 9-day run: `turn_commit_ms` 13.3 → 58.6 ms from
the first eighth to the last; N = 1,122 at day 1 and 7,201 in the final daily index (DEFECTS.md D1
"Evidence" and "Diagnosis").

Done-line: both `just DURATION=9d sim` runs and the `just DURATION=120d sim` run complete with
their directories in the Evidence column; the two `sim-profile` runs, the `pprof` diff, the
progress table and the flatness ratios are recorded in the commit message; on a flat run D1 is
removed from DEFECTS.md and A11's text states the remaining opencode-in-loop re-measure, A11 still
unticked; on a run that is not flat D1 carries the measurements and A11 is unchanged; checkpoint
`just test` for the doc changes (it hits Go's test cache), then commit.

---

