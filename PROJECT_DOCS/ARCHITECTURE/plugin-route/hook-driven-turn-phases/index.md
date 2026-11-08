### Hook-driven turn phases

`turn.RunWithInfo` owns a whole turn in-process. Under opencode, opencode owns the model call and
the tool loop, so the memory service is driven by hook events through entry points, one per turn
phase, instead. Composition is already separable: `workset.Compose` is a pure renderer
reached through the `MemoryOps.ComposeWorkingSet` port.

| Phase | Driven by | Covers |
|---|---|---|
| `BeginTurn` | `session.prompt` | budget, live-turn reject (refuse a single user prompt that exceeds its live-turn share; `internal/turn/livebudget.go`), day rollover, pre-turn snapshot, `TurnNumber++`, prompt journal, expiry of staged symbols, pre-event deltas (file deltas a step carries ahead of its prompt, `Step.PreEvents`), `user.prompt` delta |
| `ComposeRequest` | `session.context` | working-set composition and request assembly, from this turn's messages in the hook input |
| `IngestToolResult` | `tool.execute.after` | tool-result deltas, held and applied in call order |
| `IngestModelResponse` | `session.execution.succeeded` | tag strip (from the body the service keeps for in-run history; whether the text opencode stores and shows is stripped is U3, see "Topic-tag mechanics"), response journal, ceiling check, `model.response` delta |
| `CloseTurn` | `session.execution.succeeded`, after `IngestModelResponse` | engagement, closing scan (the decay check for threads due to close), `CommitTurn`, working-set persist, transcript tail; idempotent |
| `AbortTurn` | `session.execution.failed`, `.interrupted`, or a phase error | pre-authoritative rollback (before any authoritative write has landed) to the pre-turn snapshot |

In the prototype, the phases exist in `internal/turn/probe_phases.go`; `RunTurnInProcess`
recomposes them into the in-process turn. `RunWithInfo` is not yet re-expressed over them. The
phases call 34 unexported identifiers of `internal/turn` (probe results §8), so they stay in that
package; `just probe-deps` regenerates the list.

Contents:

- [Close ordering](close-ordering.md)
- [Invariant for the production phase split](invariant-for-the-production-phase-split.md)
