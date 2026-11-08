#### Open acceptance requirements

Each must be accounted for before the acceptance criterion is met. The list grows as
runs surface new gaps.

- **Workload interleaving.** The workload must interleave excerpts and queries from several
  loosely-related or unrelated topics within a work block ([§9.4.1](../9.4-measurement-regime/9.4.1-workload-cadence-model.md)), with abandon-and-resume and non-sequiturs.
  Until it does, recall/precision numbers describe a too-coherent stream.
- **Topic clustering / deep dives, metronomic edit cadence (edits to the same files at a fixed
  rhythm), cold start.**
- **Transient-data lifecycle ([§3.10](../../3-core-algorithms/3.10-transient-data-lifecycle/index.md)) fidelity** — a prerequisite to an honest recall-fidelity
  measurement.
- **Topic-tag fidelity under real inference.** The mock scripts topic tags, so tag quality is
  otherwise unmeasured. The inference-in-loop run configuration reports, as first-class metrics: re-engagement
  miss rate (turn engages a known thread, tag omits it), spurious `*new-topic*` rate (tag cuts a new
  thread where the oracle expects re-engagement), and tag-symbol overlap vs. the deterministic
  extraction pass. **Open:** U3 — these measures change if the in-band tag is replaced by a recall
  tool.
- **Embedder-enabled completeness run configuration.** A live embedder with mock inference over several
  sim-days, asserting that every flush-lag findability-gap probe (a recall query aimed at content that has left
  the recent-excerpt window but whose embedding flush has not yet landed) is surfaced and that at least one was
  observed. The [§3.4](../../3-core-algorithms/3.4-recall-matching/index.md) flush-lag completeness claim is accepted only when this run configuration runs green.
- **Registry-slot saturation stress.** At display-line lengths and the current partition, the
  registry slot saturates in the low hundreds of threads; a multi-year career accrues thousands,
  beyond what the 120-day span reaches. A stress run configuration with a synthetic pre-seeded
  multi-thousand-thread thread registry measures (a) recognition quality across the registry slot's
  truncation boundary, (b) whether opportunistic recall picks up recognition for threads that fell
  out of the rendered registry slot, and (c) [§3.8](../../3-core-algorithms/3.8-cold-storage/index.md) archival
  onset under genuine cardinality pressure.

