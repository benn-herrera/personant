#### Embedding index — granularity, maintenance, persistence

One embedding index serves all
not-in-context recall. Intra-thread recall (recall of a thread's own earlier content) searches the
engaged thread's (the current turn's owner, [§3.2](../3.2-topic-tagging-and-engagement-update/index.md)) own content in it directly.

*Maintenance.* Indexing is off the turn's critical path: recall reads never wait on it. A thread's
whole content is indexed by the time it leaves the working set.

*Persistence (required).* The vectors are derived and regenerable, and are kept across service runs
([§2.1](../../2-data-model/2.1-storage-layout/index.md) `.recall-cache/`), so a service run re-embeds only content that changed.

*Long threads.* Intra-thread recall on very long single threads must work with **no performance
decay**, validated in the [§9](../../9-measurement-and-validation-regime/index.md) acceptance simulation by a long-running-thread workload with explicit
decay measurement (index size, per-query cost, retrieval latency) as thread count *and* per-thread
length grow into the thousands.

*Completeness floor.* Content not yet embedded is still found: intra-thread recall also matches the
engaged thread's not-yet-indexed content against the query's symbols, at a bounded cost per query.
Accepted residual: a single transient embed failure leaves a gap for that batch until the next
successful indexing; the guarantee is against indexing *lag*, not failed indexing. Summary-tree descent over a long thread is an approximate recall heuristic; exact recall is
guaranteed by the separate exact lexical / flat-scan level.

