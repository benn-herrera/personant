## The layered working set

SPEC §3.1 states the outcomes: the token ceiling, the layer table and percentages, the live-turn
reserve, the four slot answers (budget, internal priority, overflow form, underusage), the lending
rule, the opencode-content slots and the tool-output cap. This section keeps the mechanism.

- **Derivation.** `context.token-budget` is a const (`memops.DefaultTokenCeiling`) plus a
  constructor override. The post-flight `usage.prompt_tokens` count is the real check; the byte
  shares drive pre-flight truncation. Of the §2.6.1 parameters only `closure.ack-mode` and
  `recall.ack-mode` are served by the directive layer (resolved at session start); the rest are
  compiled-in defaults.
- **Truncation.** Each rendered layer truncates to its byte budget at a UTF-8 rune boundary and
  appends the honest overflow marker. Layer C's dormant set is not count-capped: its byte budget
  truncates it at composition time (#127).
- **Eviction order under budget pressure:** C-oldest, then B-oldest (compressed to summary). A and
  E are never evicted.
- **Tool output.** `boundTaskResultDeltas` (`internal/turn/livebudget.go`) applies the cap to the
  delta, so the bounded content is what the request carries and what the working window holds.
- **File content.** `ComposeWorkingSet` loads each Layer B thread's `files.jsonl` and runs one
  pure `dedup.Plan` across them. The plan gives each path under the size gate a literal
  candidate at its most recent position. It gives diffs at the path's N most recent changes,
  minus those not kept or at or above `dedup.diff-literal-threshold` of the current size. A
  path whose current size is at or above `dedup.literal-share-threshold` × the per-thread share
  gets neither. Threads are walked in Layer B order and their excerpts newest-first. An excerpt
  is admitted only if its text and a size note per planned expansion fit; the newest is always
  admitted. Notes then become content while the share allows. A content id renders as a literal
  once: the first taken wins. Only content that renders is read. `workset` renders it inside
  its excerpt, and `ComposeWorkingSet` returns a structured report of what rendered.
- **Scaffolding.** Static prompt scaffolding (about 1.7 KB: preamble, topic-tag directive, recovery
  reminders, headers) is charged to no layer share and rides on top of the byte proxy.
  `TestA4PreflightAssembledBytesWithinBudget` bounds it (`Total + measured scaffolding`); a fixed
  deduction would zero out the tiny ceilings the acceptance tests exercise.
- **Prefix stability.** Fixed slots are composed first, so the request keeps a stable prefix across
  turns, which preserves the provider's prompt cache.

**Open:** per-slot answers and the `conventions_paths` overlap with the instructions slot (see
"Injection handling"; SPEC §2.5.1, §3.1).

Layers E and A are the recognition surface; Layer B is active engagement (fetch flow: "No
speculative prefetch").

Contents:

- [Names and recency state](names-and-recency-state.md)
