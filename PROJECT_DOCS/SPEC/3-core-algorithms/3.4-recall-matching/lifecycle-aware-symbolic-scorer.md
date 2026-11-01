#### Lifecycle-aware symbolic scorer

The match target is `T(thr) = anchors ∪ {h.normalized}`, so a
`superseded` symbol (retained in `history_symbols`, [§2.7.4](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)) stays matchable. The Jaccard *numerator*
is a **weighted sum**: each matched symbol contributes `1.0` if it is `active` in that thread,
`recall.superseded-weight` (default **1.0**) if it is `superseded`. A symbol that is also a
projected anchor counts as active. The superseded **label is canonical** — read from the thread's
frontmatter `lifecycle` ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)), not the derived index. The denominator (union size) is unchanged; at
weight 1.0 the score is byte-identical to the plain-count scorer. `recall.superseded-weight` is a [§9](../../9-measurement-and-validation-regime/index.md)
calibration window, lowered below 1.0 only if the sim's `superseded_precision` /
`abandoned_premise_recall` show abandoned threads crowding out focused ones.
`symbols.jsonl.superseded_in` ([§2.4](../../2-data-model/2.4-symbol-index-schema-symbols-jsonl.md)) is the cheap-scan candidate-filter surface ("which threads
abandoned premise X", O(1) per query symbol); the scorer does not consume it.

