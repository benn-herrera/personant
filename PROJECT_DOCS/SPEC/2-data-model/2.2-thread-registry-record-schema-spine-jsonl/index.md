### 2.2 Thread registry record schema (`spine.jsonl`)

Unused → ARCHITECTURE «Storage and record formats» (the record layout and field names). A person
inspecting the store finds every thread as one record in the **thread registry**, a single file
sorted by id, so a change to one thread shows as a one-line diff. A record carries the thread's
project, **anchors** (the few **symbols** that currently identify it), summary, description, state,
timestamps, turn count and recall-fire count (the number of accepted recalls of the thread, [§3.4](../../3-core-algorithms/3.4-recall-matching/index.md)).
Names beginning `spine` (`spine.jsonl`, `spine.entry-max-chars`, the `spine.*`
events) are the thread registry's. The `description` is the triggering utterance, set once at creation and never
rewritten; the `summary` is set from the thread's first response at creation and replaced when the thread closes ([§3.5](../../3-core-algorithms/3.5-closing-a-thread/index.md)) by the **summarizer**'s draft (the model call that
drafts a thread's closing summary, [§5.2](../../5-model-interaction/5.2-summarizer-prompt-for-closing-summary.md)) and bounded by `spine.entry-max-chars`. The anchors are 0 to
`anchor.projection-max` (default 8, a provisional value calibrated in [§9](../../9-measurement-and-validation-regime/index.md)) normalized symbols
(the keys recall matches on: file paths, URLs, named concepts, hashtags, [§2.7](../2.7-symbol-normalization-and-categories/index.md)), re-derived at the close of every owner turn (a turn the thread owns, [§3.2](../../3-core-algorithms/3.2-topic-tagging-and-engagement-update/index.md))
from the thread's retained symbol history ([§2.7.4](../2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)); the model never controls their count ([§5.1](../../5-model-interaction/5.1-topic-tag-emission-format/index.md)).
`personant verify` enforces the structural constraints and flags an over-ceiling anchor count.

Contents:

- [§2.2.1 Thread states](2.2.1-thread-states.md)
- [§2.2.2 Display form](2.2.2-display-form.md)
