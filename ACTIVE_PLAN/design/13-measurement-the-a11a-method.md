## 13. Measurement (the A11a method)

1. Capture two profiles:

   ```
   just DURATION=9d sim-profile
   just DURATION=31d sim-profile
   just pprof -top -cum -diff_base bin/profiles/sim-9d.cpu.pprof bin/profiles/sim-31d.cpu.pprof
   ```

   The mock run is deterministic per seed, so the 9d run is a prefix of the 31d run.

   Also capture `just DURATION=20d sim-profile`. The sim's one tracked file grows with every edit:
   its current content was 6,908 B at day 10, 14,390 B at day 20 and 22,705 B at day 30 (the
   pre-change `files.json` snapshots). At the default share the threshold is 19,479 B, so the file
   crosses it between day 20 and day 30, and from then on its literal no longer renders. A flat
   9d → 31d ratio could therefore come from the threshold alone. The 9d → 20d ratio, where the file
   stays under the threshold, is the one that shows the expansion path is flat.
2. Normalize each profile's cumulative seconds for `ComposeWorkingSet`, `RecordFileEvents` and
   `loadActiveThreadData` by the turn count each run reports.
3. Baseline: `.claude-temp/growth/per-turn-cpu-day9-vs-day31.txt`. There `ComposeWorkingSet` goes
   from 12.49 s to 96.15 s cumulative, and `loadTrackedFiles` from 0.97 s to 79.06 s.
4. "Growth gone" means:
   - the per-turn ratio of 20d to 9d, and of 31d to 9d, is ≈ 1 for these frames;
   - `dedup.(*Chain)` and `loadTrackedFiles` are absent;
   - the store of the run's most-edited thread at the end of the run is about one current literal
     plus ≤ 3 diffs.
5. While in the profile, rule out the residuals A11a names: `CommitTurn` CPU, `ReadThreadBody` →
   `readExcerptText`, and `closeTurnAndUpdateEngagement`. `VerifyNoBudgetOverflow` should also drop
   back to a small share of sim time, because it re-composes.

---

