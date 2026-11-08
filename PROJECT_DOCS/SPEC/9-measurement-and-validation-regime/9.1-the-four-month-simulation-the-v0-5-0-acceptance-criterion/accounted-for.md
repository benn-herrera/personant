#### Accounted for

- **Evolving anchors** — *simulated.* Threads drift from their creation topic without a jump sharp
  enough to cut a new thread, premises invert, and an abandoned premise stays a findable handle. The
  evolving-anchor model ([§2.2](../../2-data-model/2.2-thread-registry-record-schema-spine-jsonl/index.md)/[§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)/[§2.4](../../2-data-model/2.4-symbol-index-schema-symbols-jsonl.md)/[§2.7.4](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)/[§3.2](../../3-core-algorithms/3.2-topic-tagging-and-engagement-update/index.md)/[§3.4](../../3-core-algorithms/3.4-recall-matching/index.md)/[§5.1](../../5-model-interaction/5.1-topic-tag-emission-format/index.md)) is exercised end-to-end by a coupled
  vague-start, drift, premise-inversion and interleave workload.
- **New thread as synthesis of prior threads** — *accounted for at the symbol level.* Multi-parent
  provenance is carried as per-symbol `derived_from` ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)/[§2.7.3](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.3-source.md)), populated deterministically on
  recall; the non-recall residual is an explicit deferred decision ([§2.7.3](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.3-source.md)). The sim models
  synthesis threads that borrow symbols from ≥2 prior threads; recall stays provenance-agnostic.

