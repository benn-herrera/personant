### 3.3 Symbol extraction (three passes)

Three passes per [§3.0.2](../3.0-context-modification-events-the-architectural-primitive/3.0.2-the-hook-chain.md) step 1, cheapest first:

1. **Deterministic** — regex over the delta (file paths, URLs, claim IDs, `#`-tags).
2. **Model-emitted** — anchors from the topic tag ([§5.1](../../5-model-interaction/5.1-topic-tag-emission-format/index.md)) are folded in directly.
3. **Curator** — at retirement the curator drafts the closure summary ([§3.5](../3.5-closure-flow/index.md)). It does **not** select
   anchors: the anchor set is the deterministic projection of active `history_symbols` ([§2.7.4](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)).

Symbols from task-class deltas ([§3.10.1](../3.10-transient-data-lifecycle/3.10.1-provisional-classification-at-delta-time.md)) enter the staging buffer ([§3.10.2](../3.10-transient-data-lifecycle/3.10.2-staging-buffer.md)) instead of the coalesce
buffer; they reach `symbols.jsonl` only on citation-window promotion ([§3.10.4](../3.10-transient-data-lifecycle/3.10.4-promotion.md)).

Contents:

- [§3.3 Missing-tag protocol recovery (violation → recovery, never abort)](missing-tag-protocol-recovery-violation-recovery-never-abort.md)
- [§3.3 Empty-response recovery (any turn)](empty-response-recovery-any-turn.md)
