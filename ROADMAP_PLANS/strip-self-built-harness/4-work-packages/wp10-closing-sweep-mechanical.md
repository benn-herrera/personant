### WP10: Closing sweep (mechanical)

ROADMAP.md, exact:

- U3 entry → "- [ ] **U3 Model-supplied anchors.** Sessions set thread boundaries (**U12**), so
  the topic tag's thread list has no job. Open: whether the model still contributes per-turn
  anchors (`source = model`), in band at response start (which needs the stream
  abort-and-re-issue and transcript stripping) or through a tool, or not at all; and whether a
  recall tool the model calls replaces the §5.5 fetch trigger. Tagged at SPEC §3.0.1, §3.1,
  §5.1, §5.5, §9.1. **Decide in A10.**"
- U4 entry → "- [x] **U4 Memory-native commands and the ack channel: none.** Personant registers
  no command in opencode and asks the user nothing: no ack channel, offers, review queue, project
  prompts or recovery banner. The memory work runs autonomically (SPEC §3.4, §3.5, §4.5)."
- U8 entry → "- [x] **U8 Interrupted prompts and retraction: Personant's rule.** A retracted prompt
  never enters memory, whatever opencode's transcript shows: Personant composes its own context,
  and the transcript does not decide what enters it. Follow-up pass: SPEC §4.3.3, §4.5.8 (10);
  ARCHITECTURE 'Invariant for the production inversion', 'Start order and resume', 'Event log
  format'."
- After U11, add "- [x] **U12 Session as thread.** A project is opencode's project directory and
  a bundle of threads; an opencode session is one thread. Recall works across a project's
  threads, and across projects for career-level memory. Sessions, not in-band topic tags, set
  thread boundaries: a returned-to session keeps its thread, and a new session on an old topic
  relies on recall. Follow-up pass: SPEC §1.1, §3.0.4, §3.2, §3.3, §5.1, §5.5, §9.1, and an
  optional opencode session ID on the thread (frontmatter and spine) with a home-format bump.
  Plan: A10."
- A8: "If **U8** keeps retraction, add a record that distinguishes a retraction rollback from a
  crash rollback (reconcile skips retracted turns but no record tells the two apart)." → "Add a
  record that distinguishes a retraction rollback from a crash rollback, so reconcile re-ingests
  crash rollbacks and never a retracted turn (**U8**)."
- A10 entry → "- [ ] **A10. Session-as-thread routing** per **U12**, settling **U3**. Turns route
  to the session's thread; the topic tag's thread list, phantom resolution, missing-tag recovery
  and the tag-triggered §5.5 fetch go; the session ID is stored on the thread; the sim
  generator's unit becomes the session, and the tag-fidelity measures go. Needs session IDs from
  the plugin (A5). Re-check the missing `derived_from` lineage under opencode
  (`OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §11) now that recall fetches autonomically. The
  parser and tag-fidelity residuals of the old topic-tag item apply only if U3 keeps an in-band
  tag. (SPEC §1.1, §3.2, §5.1, §5.5, §9.1.)"
- A12 entry → "- [ ] **A12. Install and config.** `personant init` writes the opencode-side
  settings (`compaction.auto: false`, the plugin entry) in opencode's own format, and the
  `~/.config/personant/config.toml` template once A12a fixes what `[embedding] model` names; the
  home holds no settings file. Implement the CLI verbs SPEC §4.1 keeps but `cmd/` lacks
  (`search`, `deps`, `log`), or remove them from SPEC. (SPEC §2.1, §4.1, §8; ARCHITECTURE
  "Single spelunking location".)"
- B1: `- [ ]` → `- [x]`.
- "Banded/auto-mode sim rung": `- [ ]` → `- [x]`, and append "Covered: the mock gate runs the
  autonomic closure and recall paths."
- "Directive-file plumbing for the remaining §2.6.1 parameters." entry → "- [ ] **Directive-file
  plumbing for the §2.6.1 parameters.** No §2.6.1 parameter is read from directive files; every
  key (`context.token-budget`, `staging.window-turns`, `history.cap-per-thread`,
  `anchor.projection-max`, `spine.entry-max-chars`, and so on) is a compiled-in constant. The
  reader is built with its first consumer. (SPEC §2.6.1, §3.10.3, §3.1.)"

Contract and README, exact:

- SPEC §4.3.3 index: the paragraph "**Open:** U8 — whether memory forgets … the rule below
  binds." → "Personant composes its own context: what opencode's transcript shows does not
  decide what enters memory, so the rule below holds where the transcript keeps the interrupted
  prompt."
- SPEC §4.5.8 (10), first bullet, after its §4.3.3 link: "is not re-ingested while the
  retraction rule stands. **Open:** U8 — reconcile's treatment of retracted turns follows the
  interrupted-prompt decision." → "is never re-ingested." (the link stays as it is).
- ARCHITECTURE "Invariant for the production inversion": "**Open:** U8 — what `AbortTurn` does
  with an interrupted turn. Until decided, SPEC §4.3.3 binds: a retracted prompt" → "`AbortTurn`
  follows SPEC §4.3.3: a retracted prompt" (the rest of the sentence stays).
- ARCHITECTURE "Start order and resume": "**Open:** U8 — whether reconcile skips turns rolled back
  by retraction; no record distinguishes a retraction rollback from a crash rollback." →
  "Reconcile never re-ingests a turn rolled back by retraction; no record yet distinguishes a
  retraction rollback from a crash rollback (ROADMAP A8)."
- ARCHITECTURE "Event log format", `system` row: delete "; **Open:** U8 — emitted only if
  retraction stands" and "; same Open".
- CONVENTIONS `PROJECT_DOCS/CONVENTIONS/repo-state/versioning-and-acceptance.md`: after "The
  substrate line is semver; its MINOR bumps are developer fiat at the milestone gates." insert
  "Its PATCH is bumped once per landed change that alters the runtime's behaviour or the
  `MemoryOps` port — once per plan, in the plan's closing package, when the change lands through
  a plan. Changes confined to tests, the harness, recipes or documents leave it unchanged."
- README "Where Settings Live": "The current code does not match them yet (ROADMAP A12)." → "The
  current code does not match them yet (ROADMAP A12, A12a)."

Code: `internal/version/version.go` `Substrate = "0.2.8"`.

- Done-line: `just build`; `just test-run ./internal/version`; `just test-run ./cmd Version`;
  `git grep -n '"0.2.7"' -- '*.go'` → empty;
  `git grep -n -E 'internal/(chat|term|shell|tools|ping|modellist)|test-fe|fe-scope|go-readability|html-to-markdown|providers\.toml|ack-mode|RecallAck|ClosureAck|last-active|remote_urls|Open:\*\* U[4568]' -- PROJECT_DOCS README.md AGENTS.md ROADMAP.md justfile '*.go'`
  → only ROADMAP lines naming them as history and `version.go`'s format-4 doc line.
  `OPENCODE_RESEARCH/` is not searched: it is dated evidence. Checkpoint.

