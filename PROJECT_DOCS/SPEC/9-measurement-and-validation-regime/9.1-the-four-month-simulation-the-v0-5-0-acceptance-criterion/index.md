### 9.1 The four-month simulation (the v0.5.0 acceptance criterion)

The acceptance criterion (memory service v0.5.0, Version lines):

- **Simulate four continuous months (120 calendar days)** of realistic usage via a synthetic
  workload ([§9.4](../9.4-measurement-regime/index.md)) running against the real memory service with a mock LLM ([§9.2](../9.2-mock-llm-client.md)) supplying canned
  responses.
- **Memory quality maintained** throughout, measured via the metrics emitted at every event ([§9.4](../9.4-measurement-regime/index.md)):
  recall hit rate, engagement accuracy, closing timing, round-trip information-preservation
  through close→archive→restore.
- **Zero out-of-context-space events.** The normative criterion is the whole-request token ceiling
  (`context.token-budget`): the fully-assembled request (every [§3.1](../../3-core-algorithms/3.1-working-set-composition/index.md) slot, including tool definitions
  and this turn's tool results, plus injected fetches) never exceeds it, checked against the token count
  reported for the request. Any overflow is a fail. On mock run configurations a secondary check asserts each
  rendered slot stayed within its derived byte allocation ([§3.1](../../3-core-algorithms/3.1-working-set-composition/index.md)). Turn-time budget enforcement is
  deferred past v0.1 ([§3.0.2](../../3-core-algorithms/3.0-all-content-enters-through-one-pipeline-deltas/3.0.2-the-per-delta-pipeline.md) step 4).
- **Operation runtime costs measured and within bounds.** Wall-clock P50/P95/P99 latency for:
  engagement update, recall match, thread fetch, thread close, archival, recovery, index rebuild, index
  check. "Within bounds" means stable across the simulation, not a threshold that grows with
  accumulated state.
- **Steady-state demonstrated.** After 120 simulated days the trajectories of working-set size,
  thread registry size, and per-operation latency are **flat**: the system is in a state from which it could
  run another four months without degradation.

Contents:

- [§9.1 The acceptance ladder](the-acceptance-ladder.md)
- [§9.1 Recall-signal scope](recall-signal-scope.md)
- [§9.1 Honesty clause](honesty-clause.md)
- [§9.1 Accounted for](accounted-for.md)
- [§9.1 Open acceptance requirements](open-acceptance-requirements.md)
- [§9.1 Version lines](version-lines.md)
