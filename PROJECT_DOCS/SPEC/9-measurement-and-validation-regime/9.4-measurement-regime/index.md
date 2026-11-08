### 9.4 Measurement regime

The scenario, churn, calibration and acceptance-simulation levels run against the
mock LLM client ([§9.2](../9.2-mock-llm-client.md)) with logical-clock acceleration, so the full simulation completes in minutes.
Every level emits a machine-readable metrics blob with a stable JSON schema for cross-version
comparison.

**Acceptance metrics** (normative; must hold through the four-month simulation):

- **Recall fidelity:** precision/recall/F1 per scenario step against ground-truth expected matches.
  Strict mode fails the test on mismatch; measure-only mode records adversarial probes (injected recall queries built to be hard to match)
  without failing. Regressions are tracked against stored per-scenario baselines.
- **Engagement accuracy:** tagged-engaged threads match authoritative-by-construction ground truth.
- **Size bounded / zero overflow:** the token-ceiling criterion of [§9.1](../9.1-the-four-month-simulation-the-v0-5-0-acceptance-criterion/index.md), backed on mock run
  configurations by a per-turn check that each rendered slot stayed within its byte allocation; the
  byte check is skipped on live-inference run configurations.
- **Steady-state trajectory:** working-set size, thread registry cardinality, and per-operation
  latency (P50/P95/P99) flat after 120 simulated days.
- **Round-trip fidelity:** archive → restore → diff against original; information-preservation rate
  measured.
- **Operation latency within bounds:** engagement update, recall match, thread fetch, thread close,
  archival, recovery, index rebuild, index check — stable as accumulated state grows.
- **Oracle agreement with the memory service's active set:** the workload generator's own model of
  active-set membership, from which it derives expected recall matches, agrees exactly with the
  memory service's active set after every turn, in both directions. Zero divergence is an acceptance criterion on mock run
  configurations; under live inference, where the real model engages off-plan, it is reported only.
- **Intra-thread descent divergence**: how often the approximate
  summary-tree descent ([§3.4](../../3-core-algorithms/3.4-recall-matching/index.md)) substitutes a different leaf than the exhaustive scan. A reported
  quality measure, **not** an acceptance criterion.

**Invariant validators** run after every operation in churn sequences and at key checkpoints in
scenario tests. They cover thread registry integrity, index freshness, project references,
last-active validity, thread-metadata/thread-registry agreement, engagement consistency, thread
accounting, closed-thread consistency, archive resolvability, dedup consistency, and per-slot
budget.

**Named scenario set** (initial): single-thread lifecycle, multi-thread interleaving, project
switching, heavy closing (50 threads), same-anchor collision, transient shell-capture content,
cross-boundary recovery (crash recovery across a day rollover). Churn sequences are randomized but seeded; a failure is replayable from its seed. Calibration sweeps a
directive parameter across a range and emits a metrics matrix — the instrument by which [§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)
defaults earn their values.

**Acceptance simulation harness**: a deterministic seeded workload generator with logical-clock
acceleration, steady-state assertions, and per-operation cost profiling, run at a chosen span in
calendar days (ladder: [§9.1](../9.1-the-four-month-simulation-the-v0-5-0-acceptance-criterion/index.md)). Output: metrics JSON plus a human-readable summary; pass/fail per
[§9.1](../9.1-the-four-month-simulation-the-v0-5-0-acceptance-criterion/index.md).

Recall-fidelity scoring reads the event log ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)), not memory-service state, so a renamed event
fails the measurement rather than silently zeroing a series. Two reads are declared
exceptions: the active-set oracle-agreement check above, which compares the generator's model with
the memory service's active set, and the set of archived threads, read from the archive index ([§3.8.1](../../3-core-algorithms/3.8-cold-storage/3.8.1-mechanism.md)) so that an archived expected match is excluded rather than counted a miss ([§3.8.4](../../3-core-algorithms/3.8-cold-storage/3.8.4-recall-treatment-of-archived-threads-v0-1-off-recall.md)).

Contents:

- [§9.4.1 Workload cadence model](9.4.1-workload-cadence-model.md)
