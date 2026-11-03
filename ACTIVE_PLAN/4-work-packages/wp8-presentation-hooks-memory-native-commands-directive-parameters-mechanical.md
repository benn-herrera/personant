### WP8: Presentation hooks, memory-native commands, directive parameters (mechanical)

- Presentation scope:
  `git grep -l -w -E 'OnPhase|OnReasoning|emitPhase|emitReasoning|PhaseComposing|PhaseWaiting|PhaseReissuing|PhaseRecall|PhaseClosing' -- '*.go'`
  → `turn/{engage,phase_test,stream,stream_reasoning_test,turn}.go` (plus `toolloop*.go`, gone in
  WP3). Delete the presentation rows of section 1 and every `emitPhase`/`emitReasoning` call;
  delete `phase_test.go`.
- `stream_reasoning_test.go`: delete `reasoningSink` and
  `TestStreamThroughFilter_ReasoningGoesToHookNotBody`. In
  `TestStreamThroughFilter_NilHookDropsReasoning`, rename it
  `TestStreamThroughFilter_ReasoningNeverReachesBody` and use the deleted test's interleaved
  chunks (`{Reasoning: "let me "}, {Reasoning: "think"}, {Content: "answer "}, {Reasoning: "wait,"}, {Content: "here."}`),
  asserting body `"answer here."`. In `TestReadPreamble_EmitsReasoningAndKeepsHeadClean`, rename
  it `TestReadPreamble_ReasoningKeepsHeadClean`, build `&State{}`, and drop the sink assertions;
  keep the head assertions.
- `internal/turn/commands.go`: delete the command rows of section 1. Then, for each of
  `writeThreadState`, `writeThreadRecord`, `checkpointCommand`, `frontmatterFromSpine`,
  `threadDisplay`, `threadGist`, run `git grep -n -w <name> -- 'internal/turn/*.go'`; delete the
  function if only its definition remains, and `display.go` if empty. Delete
  `commands_rename_test.go` and `commands_vocab_test.go`.
- Delete `MemoryOps.DirectiveParam`, `FileAdapter.DirectiveParam`, `store.ReadParameter`, the
  helpers only it calls, and `store/directives_test.go` (every test in it is a
  `TestReadParameter_*`: `grep -n '^func Test' internal/store/directives_test.go`).
- Contract edits, exact:
  - SPEC §4 index, first paragraph → "Personant's user surface is the `personant` command line,
    which runs outside opencode. Inside an opencode session Personant registers no command, asks
    the user nothing and offers nothing; memory work runs autonomically. Everything the user
    touches in a session — the terminal UI, line editing, shell escape, permissions, tools, model
    and provider selection — is opencode's."
  - SPEC §4.2 index: replace everything after the heading with "Unused. opencode's commands are
    opencode's, and Personant adds none; thread and project operations run autonomically (§3.4,
    §3.5, §4.5). `/compact` and `/clear` do not discard memory (§1.1)." Delete
    `vocabulary-bridge.md` and its lines in `4.2-memory-native-commands/index.md` and
    `SPEC/entry-point.md`. Its id rule is not moved: nothing in a session types an id back, and
    §2.2.2 already ends the display fallback at the id.
  - SPEC §2.2.2: delete the sentence from "`/topic rename`" to "are untouched." and the sentence
    "The user-facing rendering is one shape everywhere — `thr_N: display — gist` (§3.4)."
  - SPEC §3.0.1: "Layer A2 digest content, memory-command effects (`/back-to`, etc.) and
    directive-file reloads are not chain deltas" → "Layer A2 digest content and directive-file
    reloads are not chain deltas"; "what they surface arrives through" stays.
  - SPEC §3.10.1: delete the paragraph from "One user override:" to "(§3.10.4)." and replace it
    with "The agent and the user have no override; retention flows through decision-delta
    authorship (§3.10.4)."
  - SPEC §2.6.1 header line → "**v0.1 reads no parameter from directive files.** Every key below
    is recognized and listed with its default, but is fixed at that default: setting it in a
    directive file has no effect."
  - ARCHITECTURE "Integration shape": delete the "**Memory-native commands.**" bullet and both
    "**Open:** U4" paragraphs; in their place add the bullet "- **No commands.** The plugin
    registers no command and asks the user nothing; memory work runs autonomically (SPEC §4)."
  - ARCHITECTURE "The layered working set" index: "Of the §2.6.1 parameters only
    `closure.ack-mode` and `recall.ack-mode` are served by the directive layer (resolved at
    session start); the rest are compiled-in defaults." → "No §2.6.1 parameter is served by the
    directive layer; all are compiled-in defaults."
  - CONVENTIONS "Document conventions": "(§0, §4.3.1, §4.3.2, §4.3.4," → "(§0, §4.2, §4.3.1,
    §4.3.2, §4.3.4," (WP7 has already added §4.3).
- Done-line: `just build`; `just test-run ./internal/turn`; `just test-run ./internal/store`;
  `git grep -n -w -E 'OnPhase|OnReasoning|emitPhase|emitReasoning|CreateTopic|RenameTopic|PauseThread|ResumeThread|BackToThread|ResolveThreadRef|DirectiveParam|ReadParameter' -- '*.go'`
  → empty; `git grep -n -E 'Vocabulary bridge|memory-command effects|/topic rename' -- PROJECT_DOCS`
  → empty. Checkpoint.

