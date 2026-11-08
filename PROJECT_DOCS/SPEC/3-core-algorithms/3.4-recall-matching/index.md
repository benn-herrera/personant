### 3.4 Recall matching

> **Recall-completeness invariant (no gaps in findability).** All **non-transient** turn content
> MUST be locatable by recall at every stage of its lifecycle — creation, recent-excerpt-window
> residency, scroll-out and durable storage, and later summarization and closing
> ([§3.5](../3.5-closing-a-thread/index.md)). **The ONLY content that may be unfindable is transient data deliberately discarded** per
> [§3.10](../3.10-transient-data-lifecycle/index.md) (uncited task-class symbols / raw task-bytes expired at window close, [§3.10.5](../3.10-transient-data-lifecycle/3.10.5-window-close-expiry.md)–[§3.10.6](../3.10-transient-data-lifecycle/3.10.6-what-stays-transient.md)), and two stated limits: archived threads, which are off live recall and restored with `personant archive recover` ([§3.8.4](../3.8-cold-storage/3.8.4-recall-treatment-of-archived-threads-off-recall.md)), and the accepted embed-failure residual (below).
>
> "Locatable" is **mechanism-agnostic**: findable by *some* recall path — symbolic, embedding
> similarity, summary tree (a hierarchy of summaries over a long thread's excerpts), or the
> exact lexical / flat-scan level (a plain text search of every excerpt).
>
> Completeness holds **continuously, not eventually**: durable content momentarily absent from one
> index — e.g. scrolled out of the recent-excerpt window but pending its *asynchronous* embedding
> flush — must be covered by another mechanism in the interim (the completeness floor
> below), so there is no lag-window gap in findability.
>
> **Enforcement.** A measurement harness must assert this completeness directly and may **NOT**
> model any "by-design blind spot" or acceptable lag in which durable content is unfindable — such a
> window is a correctness defect in the memory service, never a behavior to certify.
>
> **Validation scope.** A run without an embedder exercises neither intra-thread embedding recall
> nor its lexical completeness floor, so it proves this claim for the symbolic path only. The embedding-flush-lag
> half is **accepted only when the embedder-enabled completeness run configuration runs green**
> ([§9.1](../../9-measurement-and-validation-regime/9.1-the-four-month-simulation-the-v0-5-0-acceptance-criterion/index.md) open gate requirement).

Recall guarantees findability; it does not volunteer a connection to past work that shares no symbols or embedding similarity with the current work.

Three signals; signals 1 and 2 run in parallel, not as a cost cascade, and signal 3 runs on the candidates they surface:

1. **Symbolic (Jaccard)** over the symbol index — high precision, low recall. Cheap.
2. **Embedding cosine** — embedding similarity over every thread's content is the primary recall
   scan and is robust to drift: it finds threads, and content within a thread, by meaning, at one
   embedding call per turn. Intra-thread recall searches the engaged thread's own content that has
   left its recent-excerpt window; the recent-excerpt window, already in the request, is never a
   candidate.
3. **Model judgment** on surfaced candidates — expensive confirmation that cuts false positives
   before a candidate is fetched.

Signal 3 is not built (ROADMAP "Recall's model-judgment signal is not built"); candidates are
fetched on signals 1 and 2 alone.

Symbolic (Jaccard) is never a pre-filter for the embedding scan (under realistic vocabulary drift it
would discard most genuine matches).

Embedding recall (signal 2) is **opt-in**: enabled when `config.toml` pins an `[embedding]`
provider/model ([§8.2.2](../../8-bootstrap-and-lifecycle/8.2-configuration-sources-and-precedence/8.2.2-personant-settings-config-personant-config-toml.md)). With no embedding model configured, recall runs symbolic-only — never a
hard failure.

Contents:

- [§3.4 Embedding index — granularity, maintenance, persistence](embedding-index-granularity-maintenance-persistence.md)
- [§3.4 Recall candidates](recall-candidates.md)
- [§3.4 Recall skips content already in the request (in-context exclusion)](recall-skips-content-already-in-the-request-in-context-exclusion.md)
- [§3.4 Superseded symbols still match in recall (lifecycle-aware symbolic scorer)](superseded-symbols-still-match-in-recall-lifecycle-aware-symbolic-scorer.md)
