#### Recall-layer scope

The standard acceptance run uses the mock LLM ([§9.2](../9.2-mock-llm-client.md)) with no embedder, so it
exercises only the [§3.4](../../3-core-algorithms/3.4-recall-matching/index.md) symbolic Jaccard layer — not the embedding layer that is primary in
production, nor model judgment. Consequences:

- Symbolic-only metrics are not reported as system recall. A within-thread-drift miss on the
  symbolic layer is expected, not a substrate defect.
- Inference-in-loop and embedding-in-loop rungs, each individually configurable, are required for
  convergence. They run as periodic deep validation; the mock regime stays the default iteration
  loop.

**The gate is realism convergence, not a single run.** The criterion is the full top-rung span (120
days) passed with every identified element of realism accounted for, including elements surfaced by
prior runs. Each run that exposes a missing realism behavior (or a defect previous incoherence
masked) is followed by addressing it; the gate is met at the run that surfaces **no new realism
gap**.

A realism element is accounted for by one of, in descending preference:

1. **Simulated** — exercised end-to-end in the workload.
2. **Modeled and attempted** — a written model of its expected effect, a solution implemented
   against that model, and unit tests over its decomposable parts.
3. **Known unknown, deferred to empirical human-use data** — parked explicitly; real use supplies
   the data and a later substrate iteration closes the item.

