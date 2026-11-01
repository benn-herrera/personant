#### Embedding index — granularity, maintenance, persistence

One uniform mechanism serves all
not-in-context recall; there is no separate intra-thread path. Every thread is an *indexed body*
(coarse thread vector + per-excerpt chunk vectors); an *active* thread additionally carries a live
FIFO window ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)) that is in the working set and is *not* embedded. Turns scrolling out of the FIFO
become index material. Recall runs over all indexed bodies; including the **engaged thread's own**
indexed body gives intra-thread recall of its scrolled-out early content. The live FIFO window is
never a candidate.

*Maintenance.* Incremental and **asynchronous**, off the turn's critical path: recall reads never
block on indexing, and a stale in-flight embed never overwrites a fresher vector. Two flush triggers
feed the one index: an **embedding-debt cap** (flush after N turns of accumulated scrolled-out
content; N is a [§9](../../9-measurement-and-validation-regime/index.md) calibration window, default 16) and the **dormancy transition** (flush a thread's
remaining debt when it decays out of the working set, so its full body is indexed before it becomes
a pure recall target).

*Persistence (required).* The vectors are a **derived, persisted cache** ([§2.1](../../2-data-model/2.1-storage-layout/index.md) `.recall-cache/`)
keyed by a content-staleness marker (body/chunk hash): gitignored, regenerable (rebuild-on-miss),
written through by the indexer, and loaded at startup so a service run re-embeds only what changed —
**O(changed), not O(all)**.

*Long threads.* Intra-thread recall on very long single threads must work with **no performance
decay**, validated in the [§9](../../9-measurement-and-validation-regime/index.md) acceptance simulation by a long-running-thread workload with explicit
decay measurement (index size, per-query cost, retrieval latency) as thread count *and* per-thread
length grow into the thousands.

*Completeness floor.* After the fine-tier scan, the automatic intra-thread path searches the engaged
thread's not-yet-flushed tail lexically against the query symbols, over a fixed bound that covers
in-flight and queued flushes, and unions the hits into the intra-thread result — no per-query
embedding, no unbounded scan. Accepted residual: a single transient embed failure leaves a gap for
that batch until the next successful flush republishes it (healed by the dormancy-demotion flush or
the next service run's startup recovery); the guarantee is against async-flush *lag*, not failed
flushes. Summary-tree descent over a long thread is an approximate recall heuristic; exact recall is
guaranteed by the separate exact lexical / flat-scan tier.

