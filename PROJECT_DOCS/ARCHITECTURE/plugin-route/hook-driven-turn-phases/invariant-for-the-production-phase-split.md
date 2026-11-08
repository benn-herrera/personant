#### Invariant for the production phase split

`RunWithInfo` is re-expressed on the phases, `just
test` passes, and the in-process simulation reproduces its committed baselines
(`internal/scenarios/testdata/baselines/`) with no metric change. A baseline change means the split
altered behaviour.

**Open:** U8 — what `AbortTurn` does with an interrupted turn. Until decided, SPEC §4.3.3 binds: a
retracted prompt does not enter memory, and the rollback restores run-volatile state
(`internal/turn/rollback.go`): the turn counter, the cross-turn staging buffer, active-set and
dormant-set membership, history, recall-surfaced marks and embedding debt (excerpts scrolled out of the recent-excerpt window and not yet embedded; the cap is the unembedded backlog limit, 16), all to the pre-turn
snapshot. The `user.prompt` delta has already run (pipeline step 1) when a retraction arrives,
which is why staged symbols need the restore. The rollback also releases the turn's journal scope,
the span in which the turn's journal is non-empty, so the next start opens with no turn to recover.
It happens in memory only and must not perturb the write ordering crash recovery depends on.
Failures after an authoritative write do not roll back; recovery owns them.

