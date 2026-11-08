### Harness ↔ system-under-test coupling rule

The harness imports the system's *public contract
surface*, never its *implementation detail*. The discriminator: **would a production consumer
reasonably expect access to this value?** Yes → it is contract; export it if needed and reference
the public symbol (never mirror it). No → it is internal; the harness observes its *effect* through
a public API/predicate, or tolerates the imprecision — it never imports or copies the internal
value. (An active-set oracle — the generator's own model of which threads are in the active set —
still re-implements the system's *logic* independently; importing a public *scalar parameter* like
the recent-excerpt window is not an independence breach, while importing or mirroring an *internal*
one like the embedding flush-batching cap is.)

**Test levels** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock model via canned responses; metrics emission
   per scenario.
3. **Invariant validators** — callable from any test (`invariants.go`).
4. **Churn tests** — randomized seeded sequences (80% engage / 10% create / 5% close / 5% switch);
   invariants after every operation; a failure dumps the operation log and seed for replay.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating
   points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Acceptance simulation harness** — synthetic workload, logical-clock acceleration, steady-state
   assertions; the longest span is 120 days / four months (§9.1).

Acceptance is the SPEC §9.1 criterion, run as a converging loop, not a one-shot
pass.

The mock model's generated responses carry a counter suffix or a seed-derived UUID in the body
text, so no two responses dedup accidentally.

