### 9.1 The four-month simulation (v0.1 acceptance gate)

The v0.1 acceptance criterion:

- **Simulate four continuous months (120 calendar days)** of realistic usage via a synthetic
  workload ([§9.4](../9.4-measurement-regime/index.md)) running against the real runtime with a mock LLM ([§9.2](../9.2-mock-llm-client.md)) supplying canned
  responses.
- **Memory quality maintained** throughout, measured via the metrics emitted at every event ([§9.4](../9.4-measurement-regime/index.md)):
  recall hit rate, engagement accuracy, retirement timing, round-trip information-preservation
  through retire→archive→recover.
- **Zero out-of-context-space events.** The normative criterion is the whole-request token ceiling
  (`context.token-budget`): the fully-assembled request (every [§3.1](../../3-core-algorithms/3.1-working-set-composition/index.md) slot, including tool definitions
  and this turn's tool results, plus injected fetches) never exceeds it, checked post-flight against
  `usage.prompt_tokens`. Any overflow is a fail. On mock rungs a secondary check asserts each
  rendered layer stayed within its derived byte allocation ([§3.1](../../3-core-algorithms/3.1-working-set-composition/index.md)). Turn-time budget enforcement is
  deferred past v0.1 ([§3.0.2](../../3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/3.0.2-the-hook-chain.md) step 4).
- **Operation runtime costs measured and within bounds.** Wall-clock P50/P95/P99 latency for:
  engagement update, spine match, thread fetch, retirement, archival, recovery, index rebuild, index
  check. "Within bounds" means stable across the simulation, not a threshold that grows with
  accumulated state.
- **Steady-state demonstrated.** After 120 simulated days the trajectories of working-set size,
  spine size, and per-operation latency are **flat**: the system is in a state from which it could
  run another four months without degradation.

Contents:

- [§9.1 The acceptance ladder](the-acceptance-ladder.md)
- [§9.1 Recall-layer scope](recall-layer-scope.md)
- [§9.1 Honesty clause](honesty-clause.md)
- [§9.1 Accounted for](accounted-for.md)
- [§9.1 Open gate requirements](open-gate-requirements.md)
- [§9.1 Version lines](version-lines.md)
