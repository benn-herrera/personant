### Runtime inversion

`turn.RunWithInfo` owns a whole turn in-process. Under opencode, opencode owns the model call and
the tool loop, so the runtime is driven by hook events through phase entry points instead.
Composition is already separable: `workset.Compose` is a pure renderer reached through the
`MemoryOps.ComposeWorkingSet` port.

| Phase | Driven by | Covers |
|---|---|---|
| `BeginTurn` | `session.prompt` | budget, live-turn reject, day barrier, snapshot, `TurnNumber++`, prompt journal, staging GC, pre-event deltas, `user.prompt` delta |
| `ComposeRequest` | `session.context` | working-set composition and request assembly, from this turn's messages in the hook input |
| `IngestToolResult` | `tool.execute.after` | tool-result deltas, held and applied in call order |
| `IngestModelResponse` | `session.execution.succeeded` | tag strip, response journal, ceiling check, `model.response` delta |
| `CloseTurn` | `session.execution.succeeded`, after `IngestModelResponse` | engagement, closure scan, `CommitTurn`, GC, working-set persist, history; idempotent |
| `AbortTurn` | `session.execution.failed`, `.interrupted`, or a phase error | pre-canonical rollback to the pre-turn snapshot |

In the probe, the phases exist in `internal/turn/probe_phases.go`; `RunTurnInProcess` recomposes
them into the in-process turn. `RunWithInfo` is not yet re-expressed over them. The phases call 34 unexported
identifiers of `internal/turn` (PR §8), so they stay in that package; `just probe-deps` regenerates
the list.

Contents:

- [Close ordering](close-ordering.md)
- [Invariant for the production inversion](invariant-for-the-production-inversion.md)
