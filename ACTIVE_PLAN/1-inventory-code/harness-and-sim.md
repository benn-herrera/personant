### Harness and sim

| Site | Kind | What happens | WP |
|---|---|---|---|
| `Step.ClosureAck`, `ClosureAck`, `closureResolverFor`, `installAckPolicy`'s `ClosureAckMode` line, every generator's `ClosureAck:` field | remove | closure runs every step (the curator is always installed) | 6 |
| `Step.RecallAck`, `RecallAck`, `recallResolverFor`, `installAckPolicy`'s `RecallAckMode` line; `installAckPolicy` renamed `installCurator`; every generator's `RecallAck:` field | remove | | 7 |
| `advanceRuntimeMirror` / `rtActive` | reshape | applies the step's fetched thread IDs, from a new `StepFeedback` field scraped from `recall.accept` lines, in the order the runtime promotes them (read `fetchThroughChain`) | 7 |
| `workload.go` notes on decline-all recall (`metricSynthesisBorrowedSymbols` doc, generator field doc) | reshape | `derived_from` can now be stamped end-to-end; restate what the counter measures | 7 |
| `VerifyLastActiveValid`, harness `SwitchProject`'s `WriteLastActive` | remove | | 9 |

