### 9.4 Measurement regime

Four test layers — scenario, churn, calibration, and the acceptance simulation — run against the
mock LLM client ([§9.2](../9.2-mock-llm-client.md)) with logical-clock acceleration, so the full simulation completes in minutes.
Every layer emits a machine-readable metrics blob with a stable JSON schema for cross-version
comparison.

**v0.1 acceptance metrics** (normative; must hold through the four-month simulation):

- **Recall fidelity:** precision/recall/F1 per scenario step against ground-truth expected matches.
  Strict mode fails the test on mismatch; measure-only mode records adversarial probes without
  failing. Regressions are tracked against stored per-scenario baselines. Series:
  `recall_fidelity_{precision,recall,f1}` and `recall_fidelity_adversarial_{precision,recall,f1}`.
- **Engagement accuracy:** tagged-engaged threads match canonical-by-construction ground truth.
- **Heap bounded / zero overflow:** the token-ceiling criterion of [§9.1](../9.1-the-four-month-simulation-v0-1-acceptance-gate/index.md), backed on mock rungs by a
  per-turn check that each rendered layer stayed within its byte allocation; the byte check is
  skipped on live-inference rungs.
- **Steady-state trajectory:** working-set size, spine cardinality, and per-operation latency
  (P50/P95/P99) flat after 120 simulated days.
- **Round-trip fidelity:** archive → recover → diff against original; information-preservation rate
  measured.
- **Operation latency within bounds:** engagement update, spine match, thread fetch, retirement,
  archival, recovery, index rebuild, index check — stable as accumulated state grows.
- **Oracle agreement with the runtime's Layer B:** the workload generator's own model of Layer B
  membership, from which it derives expected recall matches, agrees exactly with the runtime's Layer
  B after every turn, in both directions (`layerb_shadow_divergence`,
  `layerb_shadow_reverse_divergence`). Zero divergence is a gate on mock rungs; under live
  inference, where the real model engages off-plan, it is reported only.
- **Intra-thread descent divergence** (`recall_intra_descent_divergence`): how often the approximate
  summary-tree descent ([§3.4](../../3-core-algorithms/3.4-recall-matching/index.md)) substitutes a different leaf than the exhaustive scan. A reported
  quality measure, **not** a gate.

**Invariant validators** run after every operation in churn sequences and at key checkpoints in
scenario tests. They cover spine integrity, index freshness, project references, last-active
validity, thread-metadata/spine agreement, engagement consistency, thread accounting, closed-thread
consistency, archive resolvability, dedup consistency, and per-layer budget. Cheap checks run every
step; substrate-scale checks run once per sim day and always once at end of run.

**Named scenario set** (initial): single-thread lifecycle, multi-thread interleaving, project
switching, heavy retirement (50 threads), same-anchor collision, transient shell-capture content,
cross-boundary recovery. Churn sequences are randomized but seeded (80% engage / 10% create / 5%
retire / 5% switch); a failure dumps the operation log and seed for replay. Calibration sweeps a
directive parameter across a range and emits a metrics matrix — the instrument by which [§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)
defaults earn their values.

**Acceptance simulation harness**: a deterministic seeded workload generator with logical-clock
acceleration, steady-state assertions, and per-operation cost profiling, run at a chosen span in
calendar days (ladder: [§9.1](../9.1-the-four-month-simulation-v0-1-acceptance-gate/index.md)). Output: metrics JSON plus a human-readable summary; pass/fail per
[§9.1](../9.1-the-four-month-simulation-v0-1-acceptance-gate/index.md).

Recall-fidelity scoring reads the event log ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)), not runtime state, and tests pin the scraped
event names so a rename fails a test rather than silently zeroing a series. The one declared
exception is the Layer B oracle-agreement check above, which compares the generator's model with
the runtime's Layer B set.

Contents:

- [§9.4.1 Workload cadence model](9.4.1-workload-cadence-model.md)
