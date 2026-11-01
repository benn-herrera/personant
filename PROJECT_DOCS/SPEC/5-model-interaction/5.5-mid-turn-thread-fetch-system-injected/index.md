### 5.5 Mid-turn thread fetch (system-injected)

**Open:** U3 — under opencode the mechanism below does not fire; a recall tool the model invokes is
the alternative. Until decided, the text below binds.

When a topic tag references one or more `thr_<n>` not currently in Layer B, the runtime fetches
those threads' contents and injects them into context **before the model generates its response
body**. The model does not invoke a tool to request the fetch.

Mechanism:

1. Model emits topic tag at response start.
2. Runtime parses tag (per [§5.1.2](../5.1-topic-tag-emission-format/5.1.2-parser.md)) before consuming any response body.
3. For each `thr_<n>` in the tag not currently in Layer B:
   - Read `threads/thr_<n>/` (frontmatter from `thread.md`, body assembled from the recency-windowed
     warm excerpts and, when needed, the per-thread folio `folio.jsonl`; see [§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)).
   - Truncate to budget allowance (subject to [§3.1](../../3-core-algorithms/3.1-working-set-composition/index.md) layer caps).
   - Inject as a `thread.fetched` context delta (per [§3.0](../../3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/index.md)).
4. Re-prompt the model with the augmented context; the response body is generated against it.

Contents:

- [§5.5 Combined mid-turn re-prompt bound (with §3.3 recovery)](combined-mid-turn-re-prompt-bound-with-3-3-recovery.md)
