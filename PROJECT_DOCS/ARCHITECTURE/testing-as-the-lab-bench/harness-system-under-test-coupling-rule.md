### Harness ↔ system-under-test coupling rule

The harness imports the system's *public contract
surface*, never its *implementation detail*. The discriminator: **would a production consumer
reasonably expect access to this value?** Yes → it is contract; export it if needed and reference
the public symbol (never mirror it). No → it is internal; the harness observes its *effect* through
a public API/predicate, or tolerates the imprecision — it never imports or copies the internal
value. (A shadow oracle still re-implements the system's *logic* independently — importing a public
*scalar parameter* like the excerpt assembly window is not an independence breach; importing or
mirroring an *internal* one like the embedding flush-batching cap is.)

**Layers** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock LLM via canned responses; metrics emission
   per scenario.
3. **Invariant validators** — callable from any test (`invariants.go`).
4. **Churn tests** — randomized seeded sequences; invariants after every operation.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating
   points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Acceptance simulation harness** — synthetic workload, logical-clock acceleration, steady-state
   assertions; the top rung is 120 days / four months (§9.1).

v0.1 acceptance is the SPEC §9.1 gate, run as a converging loop, not a one-shot pass.

When making technique changes (parameter tweaks, algorithm adjustments), the question is always
"what does the simulation say?" — not "does it compile and pass invariants?" Bake metrics emission
into new code from day one; bolting it on later is much more expensive.

