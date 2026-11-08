### 3.3 Symbol extraction (two passes)

Two passes run per [§3.0.2](../3.0-all-content-enters-through-one-pipeline-deltas/3.0.2-the-per-delta-pipeline.md) step 1, cheapest first:

1. **Deterministic** — extraction from the delta (file paths, URLs, commit SHAs, hashtags).
2. **Model-emitted** — tag symbols from the topic tag ([§5.1](../../5-model-interaction/5.1-topic-tag-emission-format/index.md)) are folded in directly.

The summarizer, which drafts the closing summary when a thread closes ([§3.5](../3.5-closing-a-thread/index.md)), does **not** select anchors and contributes no symbols: the anchor set is the deterministic projection of retained `history_symbols` ([§2.7.4](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)).

Symbols from task-class deltas ([§3.10.1](../3.10-transient-data-lifecycle/3.10.1-provisional-classification-at-delta-time.md)) enter the staging buffer ([§3.10.2](../3.10-transient-data-lifecycle/3.10.2-staging-buffer.md)); they reach `symbols.jsonl` only on citation-window promotion ([§3.10.4](../3.10-transient-data-lifecycle/3.10.4-promotion.md)).

Contents:

- [§3.3 A response without a topic tag never aborts the turn (missing-tag fallback)](a-response-without-a-topic-tag-never-aborts-the-turn-missing-tag-fallback.md)
- [§3.3 Empty-response fallback (any turn)](empty-response-fallback-any-turn.md)
