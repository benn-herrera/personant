## 13. Measurement (the A11a method)

1. **Capture three mock profiles and diff them:**

   ```
   just DURATION=9d sim-profile
   just DURATION=20d sim-profile
   just DURATION=31d sim-profile
   just pprof -top -cum -diff_base bin/profiles/sim-9d.cpu.pprof bin/profiles/sim-20d.cpu.pprof
   just pprof -top -cum -diff_base bin/profiles/sim-9d.cpu.pprof bin/profiles/sim-31d.cpu.pprof
   ```

   The mock run is deterministic per seed, so the 9d run is a prefix of the longer ones.

   The sim's one tracked file grows with every edit. Its current content, from pre-change
   `files.json` snapshots of the same seed, was 3,403 bytes at day 5, 6,962 at day 10, 14,512 at
   day 20, 22,873 at day 30 and 39,097 at day 54. At the default share the threshold is 19,479
   bytes. The file therefore crosses it between day 20 and day 30, and from then on its literal no
   longer renders. A flat 9d → 31d ratio could come from the threshold alone. The 9d → 20d ratio,
   with the file under the threshold throughout, is the one that shows the expansion path is flat.
2. **Normalize** each profile's cumulative seconds for `ComposeWorkingSet`, `RecordFileEvents` and
   `loadActiveThreadData` by the turn count each run prints (`turns:`).
3. **Baseline** (pre-change, the same seed). The 31d run printed `turns: 26704` and took 4,064 s.
   Cumulative seconds from 9d to 31d:

   | Frame | 9d | 31d |
   |---|---|---|
   | `ComposeWorkingSet` | 12.49 | 96.15 |
   | `loadTrackedFiles` | 0.97 | 79.06 |
   | `dedup.(*Chain).LiveWindow` | 1.22 | 77.66 |
   | `CommitTurn` | 16.28 | 27.22 |
   | `store.ReadThreadBody` | 10.49 | 18.08 |
   | `closeTurnAndUpdateEngagement` | 6.27 | 12.53 |

   `ComposeWorkingSet` per turn went from ≈ 1.6 ms (12.49 s over ≈ 7,750 turns, 9/31 of 26,704) to
   ≈ 3.6 ms, a ratio of ≈ 2.2.
4. **"Flat" means a per-turn ratio of at most 1.25.** That band sits well below the baseline's 2.2
   and above CPU-sampling variation on frames measured in seconds. Growth is gone (criterion 4 of
   [§12](12-acceptance-criteria.md)) when:
   - the 20d/9d and 31d/9d per-turn ratios of the three frames in step 2 are each ≤ 1.25;
   - `dedup.(*Chain)` and `loadTrackedFiles` are absent;
   - at the end of the 31d run, the most-edited thread's store holds one blob and at most 3 diffs
     per kept path, plus the index.
5. **Residuals.** While in the profile, rule out the residuals A11a names: `CommitTurn` CPU,
   `ReadThreadBody` → `readExcerptText`, and `closeTurnAndUpdateEngagement`.
   `VerifyNoBudgetOverflow` should also drop back to a small share of sim time, because it
   re-composes.
6. **ROADMAP.** A11a is checked off in place only when step 4's three conditions hold. Otherwise
   it stays open, reduced to the condition that failed, with its measured ratio. A residual frame
   whose ratio exceeds 1.25 becomes its own open ROADMAP item with the measured ratio; it does not
   block the A11a check-off. Its "rule out after the fix" items stay as written until measured.

---
