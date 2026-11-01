#### Anchor projection at owner-turn close

After the owner's `history_symbols` merge, the runtime
re-derives the thread's `anchors` projection ([§2.7.4](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)) in the order **merge → project → evict**.
**Idempotent-write guard:** the spine line is changed (and `anchors_projected_at_turn` bumped to the
owner-turn index) **only when the projected anchor set differs** from the prior set.
**Engaged-but-not-owner threads do not re-project.** Closure ([§3.5](../3.5-closure-flow/index.md)) runs a final authoritative
projection from `history_symbols`.

