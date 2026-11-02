### P7 — Measurement (expert)

**Precondition.** C2 committed.

**Scope.** No source change. Output: the measured table in the C3 commit message, and `ROADMAP.md`
A11a checked off in place if the criteria hold (ROADMAP "Checklist lifecycle"). Any residual
growth found becomes its own open ROADMAP item that states the frame and the measured rise. Scratch
logs go under `.claude-temp/a11a/`.

**Design to read:** [design §12](../../design/12-acceptance-criteria.md) criterion 4;
[design §13](../../design/13-measurement-the-a11a-method.md) (commands, baseline table, tolerance
and the ROADMAP rule).

**Facts.**
- Recipes. `sim-profile` writes `bin/profiles/sim-<DURATION>.cpu.pprof`. `pprof` passes its
  arguments to `go tool pprof` (justfile, recipes `sim-profile` and `pprof`). `DURATION` is a
  justfile variable (`DURATION := env_var_or_default("DURATION", "1w")`).
- Turn count. TestSim prints a `turns:` line (`internal/scenarios/sim/sim_rung_test.go:412`). The
  pre-change 31d run printed `turns: 26704` and took 4,064 s (design §13 step 3).
- Baseline: the pre-change 9d → 31d cumulative seconds are tabulated in design §13 step 3; the
  measurement depends on no scratch file.
- The sim's tracked file crosses the 19,479-byte limit between day 20 (14,512 bytes) and day 30
  (22,873 bytes), so only the 9d → 20d ratio shows the expansion path under load (design §13).

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

**Tolerance and the check-off rule.** "Flat" means a per-turn ratio of at most **1.25**; the reason
for the value is in [design §13](../../design/13-measurement-the-a11a-method.md) step 4. Record the
measured 9d turn count.
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

