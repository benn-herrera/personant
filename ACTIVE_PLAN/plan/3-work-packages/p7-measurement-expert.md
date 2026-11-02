### P7 — Measurement (expert)

**Precondition.** C2 committed.

**Scope.** No source change. Output: the measured table in the C3 commit message, and `ROADMAP.md`
A11a checked off in place if the criteria hold (ROADMAP "Checklist lifecycle"). Any residual
growth found becomes its own open ROADMAP item that states the frame and the measured rise. Scratch
logs go under `.claude-temp/a11a/`.

**Design to read:** §12 criterion 4, §13.

**Facts.**
- Recipes. `sim-profile` writes `bin/profiles/sim-<DURATION>.cpu.pprof`. `pprof` passes its
  arguments to `go tool pprof` (justfile, recipes `sim-profile` and `pprof`). `DURATION` is a
  justfile variable (`DURATION := env_var_or_default("DURATION", "1w")`).
- Turn count. TestSim prints a `turns:` line (`internal/scenarios/sim/sim_rung_test.go:412`). The
  pre-change 31d run printed `turns: 26704` and took 4064.30 s (`.claude-temp/growth/prof-31d.log`).
- Baseline, pre-change, 9d → 31d cumulative seconds: `ComposeWorkingSet` 12.49 → 96.15;
  `loadTrackedFiles` 0.97 → 79.06; `dedup.(*Chain).LiveWindow` 1.22 → 77.66; `CommitTurn`
  16.28 → 27.22; `store.ReadThreadBody` 10.49 → 18.08; `closeTurnAndUpdateEngagement` 6.27 → 12.53
  (`.claude-temp/growth/per-turn-cpu-day9-vs-day31.txt`).
- The sim's tracked file crosses the 19,479 B limit between day 20 (14,390 B) and day 30
  (22,705 B), so only the 9d → 20d ratio shows the expansion path under load (design §13).

**Work.** Capture output as it runs, for example
`just DURATION=9d sim-profile 2>&1 | tee .claude-temp/a11a/sim-9d.log`. Then:

```
just DURATION=9d sim-profile
just DURATION=20d sim-profile
just DURATION=31d sim-profile
just pprof -top -cum -diff_base bin/profiles/sim-9d.cpu.pprof bin/profiles/sim-20d.cpu.pprof
just pprof -top -cum -diff_base bin/profiles/sim-9d.cpu.pprof bin/profiles/sim-31d.cpu.pprof
```

Normalize the cumulative seconds of `ComposeWorkingSet`, `RecordFileEvents` and
`loadActiveThreadData` by each run's `turns:`. Report the 20d/9d and 31d/9d per-turn ratios for
those frames, and confirm `dedup.(*Chain)` and `loadTrackedFiles` are absent. List the files under
the most-edited thread's `files/` at the end of the 31d run; the rundata path is printed as
`rundata:`. Then rule out, or report, the residuals:
- `CommitTurn`;
- `ReadThreadBody` → `readExcerptText`;
- `closeTurnAndUpdateEngagement`;
- `VerifyNoBudgetOverflow`'s share of sim time.

**Tolerance and the check-off rule.** "Flat" means a per-turn ratio of at most **1.25**.
- Why 1.25: before the change, `ComposeWorkingSet`'s per-turn cost roughly doubled from 9d to 31d.
  That is 12.49 s over ≈ 7,750 turns (9/31 of 26,704; P7 records the measured count) against
  96.15 s over 26,704 turns, ≈ 1.6 ms → 3.6 ms, a ratio of ≈ 2.2. A 25% band sits well below that
  growth and above CPU-sampling variation on frames measured in seconds.
- A11a is checked off only when all of the following hold:
  - the 20d/9d and 31d/9d per-turn ratios of `ComposeWorkingSet`, `RecordFileEvents` and
    `loadActiveThreadData` are each ≤ 1.25;
  - `dedup.(*Chain)` and `loadTrackedFiles` are absent from both diffs;
  - the end-of-run listing shows, per kept path, one blob and at most 3 diffs.
- If any of these fails, A11a stays open, and its text is reduced to the frame or condition that
  failed, with its measured ratio.
- A residual frame from the list above whose ratio exceeds 1.25 becomes its own open ROADMAP item
  with the measured ratio. It does not block the A11a check-off.

Coverage is stated as "simulated, mock inference" (CONVENTIONS "Versioning & acceptance").

**Done-line.** The three `just … sim-profile` runs pass. The table is in the C3 commit message. The
ROADMAP change is in the same commit. **Checkpoint C3:** `just test` (cached except for the
ROADMAP-only change), then commit.

---

