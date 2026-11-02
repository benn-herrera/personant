## 4. Acceptance criteria

The design's criteria 1–10 ([design §12](../design/12-acceptance-criteria.md)) are the exit
condition. Each is shown at:

| Criterion | Shown at |
|---|---|
| 1, 10 | P4 compose tests, the harness check tests and the per-step sim check (`dedup-expansions:` line) |
| 2 | P5 `TestBarrier_Prune…` |
| 3 | P6 done-line (the two criterion-3 greps) and P7's end-of-run store listing |
| 4 | P7 |
| 5 | P4 `TestComposeWorkingSet_LeavesExcerptsByteIdentical`, P5 `TestBarrier_Prune…`, and the B1b tests unchanged at C2 |
| 6 | P5 W-TURN crash case and `TestBarrier_CrashWalk` B1c points |
| 7 | P5 `TestHomeFormat_Migrate2To3RemovesLegacySidecar` |
| 8 | P2 verify tests |
| 9 | P6 done-line |
