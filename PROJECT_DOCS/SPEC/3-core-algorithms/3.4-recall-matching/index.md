### 3.4 Recall matching

> **Recall-completeness invariant (no dead zones).** All **non-transient** turn content MUST be
> locatable by recall at every stage of its lifecycle — creation, assembly-window residency,
> scroll-out and durable storage, archival ([§3.8](../3.8-deep-cold-archival/index.md)), and later summarization/retirement ([§3.5](../3.5-closure-flow/index.md)). **The
> ONLY content that may be unfindable is transient data deliberately discarded** per [§3.10](../3.10-transient-data-lifecycle/index.md) (uncited
> task-class symbols / raw task-bytes evicted at window close, [§3.10.5](../3.10-transient-data-lifecycle/3.10.5-window-close-eviction.md)–[§3.10.6](../3.10-transient-data-lifecycle/3.10.6-what-stays-transient.md)).
>
> "Locatable" is **mechanism-agnostic**: findable by *some* recall path — symbolic, embedding
> coarse/fine tier, summary tree, or the exact lexical / flat-scan tier.
>
> Completeness holds **continuously, not eventually**: durable content momentarily absent from one
> index — e.g. scrolled out of the assembly window but pending its *asynchronous* fine-tier
> embedding flush — must be covered by another mechanism in the interim (the completeness floor
> below), so there is no lag-window dead zone.
>
> **Enforcement.** A measurement harness must assert this completeness directly and may **NOT**
> model any "by-design blind spot" or acceptable lag in which durable content is unfindable — such a
> window is a runtime correctness defect, never a behavior to certify.
>
> **Validation scope.** A run without an embedder exercises neither the fine tier nor its lexical
> completeness floor, so it proves this claim for the symbolic path only. The embedding-flush-lag
> half is **accepted only when the embedder-enabled completeness rung runs green** ([§9.1](../../9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/index.md) open gate
> requirement).

Three layers, run as **parallel signals**, not a cost cascade:

1. **Symbolic Jaccard** over the symbol index — high precision, low recall. Cheap.
2. **Embedding cosine** over an in-memory, **hierarchical** embedding index — the primary recall
   scan, drift-robust. A coarse tier (one vector per thread body) picks threads; a fine tier
   (per-turn-excerpt chunk vectors) picks content within a surfaced thread. The per-turn cost is one
   embedding call to vectorize the query.
3. **Model judgment** on surfaced candidates — expensive confirmation that cuts false positives
   before a recall is offered.

Symbolic Jaccard is never a pre-filter for the embedding scan (under realistic vocabulary drift it
would discard most genuine matches).

Embedding recall (layer 2) is **opt-in**: enabled when `config.toml` pins an `[embedding]`
provider/model ([§8.2.2](../../8-bootstrap-and-lifecycle/8.2-configuration-sources-and-precedence/8.2.2-personant-settings-config-personant-config-toml.md)). With no embedding model configured, recall runs symbolic-only — never a
hard failure.

Contents:

- [§3.4 Embedding index — granularity, maintenance, persistence](embedding-index-granularity-maintenance-persistence.md)
- [§3.4 Recall surface](recall-surface.md)
- [§3.4 Banding](banding.md)
- [§3.4 Accept suppression (the intra-thread re-fire)](accept-suppression-the-intra-thread-re-fire.md)
- [§3.4 Escape hatch](escape-hatch.md)
- [§3.4 Lifecycle-aware symbolic scorer](lifecycle-aware-symbolic-scorer.md)
