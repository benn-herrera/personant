#### A response without a topic tag never aborts the turn (missing-tag fallback)

A response with no valid
topic tag never aborts the turn. Two regimes, split by whether the turn's deltas require owner
binding (buffered [§3.9](../3.9-working-set-content-dedup/index.md) file reads or writes):

- **No binding required** (plain conversational turn): no
  re-prompt; the turn's content is kept like any other turn's ([§3.4](../3.4-recall-matching/index.md)); `topic.tag-missing` is
  logged for calibration.
- **Binding required**: two fallback stages.
  1. *Single re-prompt* (`topic.re-prompt cause=missing-tag`): the in-flight stream is aborted and
     the request re-issued with a terse system-side reminder appended ([§5.1.3](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.3-prompt-template.md)). Capped at 1 per
     turn for this cause (combined bound: [§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md)).
  2. *Owner-default* (`thread.tag-defaulted`): if the re-prompted response still lacks a tag, the
     turn binds to the most recently engaged valid active-set thread, by active-set order ([§3.1](../3.1-working-set-composition/index.md);
     the first resolving candidate wins) — the same choice after a restart, since active-set order
     survives relaunch. When no active-set candidate resolves, a new thread is created from the turn.
     The excerpt and the [§3.9](../3.9-working-set-content-dedup/index.md) edits are never dropped, and content handled by the fallback still
     flows through the [§3.0](../3.0-all-content-enters-through-one-pipeline-deltas/index.md) pipeline ([§3.0.5](../3.0-all-content-enters-through-one-pipeline-deltas/3.0.5-implementation-contract.md)).

  A turn that read a file is binding-required, like one that wrote one.

