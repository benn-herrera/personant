#### Open gate requirements

Each must be accounted for before the gate is met. The list grows as
runs surface new gaps.

- **Workload interleaving.** The workload must interleave excerpts and queries from several
  loosely-related or unrelated topics within a session, with abandon-and-resume and non-sequiturs.
  Until it does, recall/precision numbers describe a too-coherent stream.
- **Topic clustering / deep dives, metronomic edit cadence, cold start.**
- **Transient-data lifecycle ([§3.10](../../3-core-algorithms/3.10-transient-data-lifecycle/index.md)) fidelity** — a prerequisite to an honest recall-fidelity
  measurement.
- **Topic-tag fidelity under real inference.** The mock scripts topic tags, so tag quality is
  otherwise unmeasured. The inference-in-loop rung reports, as first-class metrics: re-engagement
  miss rate (turn engages a known thread, tag omits it), spurious `*new-topic*` rate (tag cuts a new
  thread where the oracle expects re-engagement), and anchor-emission overlap vs. the deterministic
  extraction pass. **Open:** U3 — these measures change if the in-band tag is replaced by a recall
  tool.
- **Embedder-enabled completeness rung.** A live embedder with mock inference over several
  sim-days, asserting that every flush-lag dead-zone probe is surfaced and that at least one was
  observed. The [§3.4](../../3-core-algorithms/3.4-recall-matching/index.md) flush-lag completeness claim is accepted only when this rung runs green.
- **A1-saturation stress.** At spine-display line lengths and the current partition, Layer A1
  saturates in the low hundreds of threads; a multi-year career accrues thousands, beyond what the
  120-day rung reaches. A stress rung with a synthetic pre-seeded multi-thousand-thread spine
  measures (a) recognition quality across the A1 truncation boundary, (b) whether opportunistic
  recall picks up recognition for threads that fell off the rendered surface, and (c) [§3.8](../../3-core-algorithms/3.8-deep-cold-archival/index.md) archival
  onset under genuine cardinality pressure.

