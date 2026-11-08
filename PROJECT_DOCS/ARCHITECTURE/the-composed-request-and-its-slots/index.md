## The composed request and its slots

Standard pattern: THESIS "The building blocks", row "Context composition".

SPEC §3.1 states the outcomes: the token ceiling, the slot table and percentages, the live-turn
reserve, the four slot policies (budget, internal priority, overflow form, unused-share rule), the
unused-share reallocation, the opencode-content slots and the tool-output cap. This section keeps
the mechanism.

- **Derivation.** `context.token-budget` is a const (`memops.DefaultTokenCeiling`) plus a
  constructor override. The post-flight prompt token count is the real check (opencode reports it in
  `session.step.ended`; see "Model access and inference telemetry"); the byte
  shares drive pre-flight truncation. A slot's byte share is the ceiling times a conservative
  ~2.5 bytes per token, counting text bytes only; the pre-flight check covers this composition plus
  the live-turn sub-policy. Of the §2.6.1 parameters only `closure.ack-mode` and
  `recall.ack-mode` are served by the directive files; the rest are compiled-in defaults.
- **Slot sources and rendering** (`internal/workset`):
  - *directives:* `directives/defaults.md`, `directives/user.md` and
    `directives/prj_<n>/project.md` of the active project, plus the project's `conventions_paths`;
    a section-divided concatenation with each directive file's YAML frontmatter stripped.
  - *registry:* `spine.jsonl` filtered to `project == active`, one display line per record (see
    "Display lines are how the model recognizes threads").
  - *other-projects:* `projects/<id>/digest.json` of every other project except `prj_default`, one
    line each, `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active`
    descending, each line capped at `cross-project.digest-per-project-bytes`.
  - *active-threads:* `threads/thr_<id>/` for each id in the active set, up to `layer.b-top-k`,
    most recently engaged first, each rendered as a heading, a frontmatter line and the body; a
    thread's share is the slot's share ÷ `layer.b-top-k` and an oversized thread is truncated on its
    own.
  - *dormant-threads:* the `spine.jsonl` records of the dormant set, one display line per record.
- **Truncation.** Each rendered slot truncates to its byte budget at a UTF-8 rune boundary and
  appends the honest truncation notice (SPEC §3.1 truncation policy). The dormant set is not count-capped: the dormant-threads
  slot's byte budget truncates it at composition time.
- **Demotion order under budget pressure:** the dormant-threads slot's oldest thread, then the
  active-threads slot's oldest (compressed to summary). The directives, registry and
  other-projects slots are never evicted from the request.
- **Tool output.** `boundTaskResultDeltas` (`internal/turn/livebudget.go`) applies the cap to the
  delta, so the capped content is what the request carries and what the composed request holds.
- **File content.** `ComposeWorkingSet` loads each active-set thread's `files.jsonl` and runs one
  pure `dedup.Plan` across them. The plan gives each path that passes the size check a
  full-content candidate at its most recent position. It gives diffs at the path's N most recent
  changes, minus those not kept or at or above `dedup.diff-literal-threshold` of the current size.
  A path whose current size is at or above `dedup.literal-share-threshold` × the per-thread share
  gets neither. Threads are walked in active-set order and their excerpts newest-first. An excerpt
  is admitted only if its text and a size note per planned expansion fit; the newest is always
  admitted. Notes then become content while the share allows. A content id renders as full content
  once: the first taken wins. Only content that renders is read. `workset` renders it inside
  its excerpt, and `ComposeWorkingSet` returns a structured report of what rendered.
- **Scaffolding.** Static prompt scaffolding (about 1.7 KB: preamble, topic-tag directive, fallback
  reminders, headers) is charged to no slot share and rides on top of the byte proxy.
  `TestA4PreflightAssembledBytesWithinBudget` caps it (`Total + measured scaffolding`); a fixed
  deduction would zero out the tiny ceilings the acceptance tests exercise.
- **Prefix stability.** Fixed slots are composed first, so the request keeps a stable prefix across
  turns, which preserves the provider's prompt cache.

**Open:** per-slot policies and the `conventions_paths` overlap with the instructions slot (see
"Injection handling"; SPEC §2.5.1, §3.1).

The directives, registry and other-projects slots are what the model recognizes threads from; the
active-threads slot is active engagement (fetch flow: "No speculative prefetch").

Contents:

- [Active-set and dormant-set order](active-set-and-dormant-set-order.md)
