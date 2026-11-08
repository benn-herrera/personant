#### Empty-response fallback (any turn)

A response with **zero visible content** (no tag and no body
— e.g. a model that spent its completion budget on hidden reasoning and returned `finish=stop`) is a
protocol failure whether or not binding is required (an all-whitespace response longer than the
[§5.1.2](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.2-parser.md) scan window is missing its topic tag, not empty — [§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md)):

A response that carries tool calls is not empty even when it has no visible text; it never spends
the empty-response re-prompt.

1. *Single re-prompt* (`topic.re-prompt cause=empty-response`): the ended stream is discarded and
   the request re-issued with the empty-response reminder ([§5.1.3](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.3-prompt-template.md)) appended — its own per-cause cap
   of 1 under the combined bound ([§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md)). Fires on ANY turn, conversational or binding.
2. *Accept the empty*: if the re-prompted response is also empty, the turn proceeds with it. On a
   **binding-required** turn it falls through to owner-default above; the recorded excerpt's agent
   section is **empty** (never synthesized placeholder content), and is logged `thread.tag-defaulted`. On a
   **conversational** turn the empty body surfaces to the caller unchanged. Either way the memory service
   emits a `system.empty-response` forensic line.

   The empty cause **subsumes** the missing-tag cause: a persistent-empty response never also spends
   the missing-tag re-prompt.

