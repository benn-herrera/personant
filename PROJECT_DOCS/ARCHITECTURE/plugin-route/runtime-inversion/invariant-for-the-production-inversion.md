#### Invariant for the production inversion

`RunWithInfo` is re-expressed on the phases, `just
test` passes, and the in-process simulation reproduces its committed baselines
(`internal/scenarios/testdata/baselines/`) with no metric change. A baseline change means the split
altered behaviour.

**Open:** U8 — what `AbortTurn` does with an interrupted turn. Until decided, SPEC §4.3.3 binds: a
retracted prompt does not enter memory, and the rollback restores run-volatile state
(`internal/turn/rollback.go`).

