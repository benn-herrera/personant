## How decisions evolve

Personant evolves by:

1. **Calibration scenarios** — parameter sweeps over canonical workloads; metrics matrices identify
   operating points.
2. **Directive accrual** — see "Personalization through directive accrual".
3. **Empirical pressure on watch items** — known weak spots (categorized below) surface via
   instrumentation; targeted work follows the data.

When you propose a change, ask:

- Does it have a measurement story? (How will we know it worked?)
- Does it commit to a specific value, or open a calibration window?
- Does it preserve or invert a recurring pattern (above)?
- Does it create migration cost we can't recover from?

**Categories of known weakness** the simulation regime is designed to surface and quantify:

- **Symbol-extraction noise** — synonym fragmentation, low-information leakage, model emission
  drift.
- **Recall precision** — cross-thread symbol collision, anchor selection quality at retirement.
- **Deterministic-pass coverage** — per-project regex curation needs.

---

