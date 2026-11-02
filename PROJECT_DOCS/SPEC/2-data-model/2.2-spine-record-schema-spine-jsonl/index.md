### 2.2 Spine record schema (`spine.jsonl`)

Unused → ARCHITECTURE «Storage and record formats». Every thread has one bookkeeping record in
a single file sorted by id, so diffs stay line-grain: project, anchors, summary, description,
state, timestamps, turn count and recall-fire count. The `description` is the triggering
utterance, set once at creation and never rewritten; the `summary` is curator-drafted at
retirement ([§3.5](../../3-core-algorithms/3.5-closure-flow/index.md)) and bounded by `spine.entry-max-chars`. The anchors are 0 to
`anchor.projection-max` (default 8, a [§9](../../9-measurement-and-validation-regime/index.md) calibration window) normalized symbols, re-derived every
owner turn at close from the thread's active symbol history ([§2.7.4](../2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)); the model never controls
their count ([§5.1](../../5-model-interaction/5.1-topic-tag-emission-format/index.md)). `personant verify` enforces the structural constraints and flags an
over-ceiling anchor count.

Contents:

- [§2.2.1 Thread states](2.2.1-thread-states.md)
- [§2.2.2 Display form](2.2.2-display-form.md)
