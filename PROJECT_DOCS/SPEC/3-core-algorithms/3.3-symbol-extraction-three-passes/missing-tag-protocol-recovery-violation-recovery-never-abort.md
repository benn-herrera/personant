#### Missing-tag protocol recovery (violation → recovery, never abort)

A response with no valid
topic tag never aborts the turn. Two regimes, split by whether the turn's deltas require owner
binding (buffered [§3.9](../3.9-working-set-content-dedup/index.md) file edits):

- **No binding required** (plain conversational turn): the turn proceeds tag-less — no engagement
  update, no excerpt, no owner, no re-prompt; `topic.tag-missing` is logged for calibration.
- **Binding required**: two recovery stages.
  1. *Single re-prompt* (`topic.re-prompt cause=missing-tag`): the in-flight stream is aborted and
     the request re-issued with a terse system-side reminder appended ([§5.1.3](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.3-prompt-template.md)), via the [§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md)
     re-issue path. Capped at 1 per turn for this cause (combined bound: [§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md)).
  2. *Owner-default* (`thread.tag-defaulted`): if the re-prompted response still lacks a tag, the
     turn binds to the most recently engaged valid Layer-B thread, by Layer B order ([§3.1](../3.1-working-set-composition/index.md); the first
     resolving candidate wins) — the same choice after a restart, since Layer B order survives
     relaunch. When no Layer-B candidate resolves, a new thread is created from the turn. The
     excerpt and the [§3.9](../3.9-working-set-content-dedup/index.md) edits are never dropped, and recovered content still flows through the
     [§3.0](../3.0-context-modification-events-the-architectural-primitive/index.md) chain ([§3.0.5](../3.0-context-modification-events-the-architectural-primitive/3.0.5-implementation-contract.md)). The forensic `thread.tag-defaulted` line carries `thr=`,
     `cause=missing-tag`, `reprompted=yes|no`.

  `fs.read` deltas buffer a write entry exactly like `fs.write` ([§3.9](../3.9-working-set-content-dedup/index.md)), so a tag-less read-only turn
  is binding-required.

