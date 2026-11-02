### E1. Where file content gets into the model's context

- **`fs.*` delta content never reaches the request directly.** `grep -n "preEvents"
  internal/turn/turn.go` returns 695, 696, 709, 747, 890, 891. Within `RunWithInfo`'s body (which
  starts at 747) the uses are 890–891 only: the events are bounded and fed to `onContextDelta`.
  The request is assembled at `internal/turn/turn.go:987–991` from the system prompt,
  `historyTail`, `userInput` and `toolMessages`, and nothing else.
- **No production code in our loop produces `fs.*` deltas.** `git grep -l -e SourceFSRead -e
  SourceFSWrite -e SourceFSCommit -- '*.go'` returns `internal/memops/event_sources.go`,
  `internal/turn/chain.go`, `internal/scenarios/sim/workload.go`, and three test files. The one
  production `RunWithInfo` caller, `internal/chat/chat.go:803`, passes `pre`, which holds SPEC §4.4
  `#` captures (`internal/chat/chat.go:786–788`). The sim is the only producer. Under opencode the
  producer would be the plugin.
- **Tool results in the current turn** are `tool.result` messages from the registered tools (web
  only). They are bounded by the per-result byte cap that the code marks "§6.5 byte cap"
  (`internal/turn/toolloop.go:381`, applied by `boundTaskResultDeltas`). They carry no
  tracked-file content.
- **Excerpts hold no tool output.** `renderTurnExcerpt` (`internal/turn/excerpt.go:32–55`) writes
  the header, `**user:**` and `**agent:**`, and nothing else. `History` holds `userInput` and
  `full.Content` (`internal/turn/turn.go:1413`). File bytes reach an excerpt or the history only
  when the user or the model quotes them.
- **The tracked-files section is the only literal source, and it is appended, not placed.**
  `renderThreadBody` (`internal/workset/workset.go:389`) appends `=== tracked files ===` after the
  body. `renderLayerB` (`internal/workset/workset.go:312–313`) then truncates the whole thread to
  its per-thread share, so the section is the first thing cut. So it does not duplicate excerpt
  text. It is a separate list that holds the current literal, 3 diffs and one identifier line per
  historical version.
- **In long runs the section is computed every turn and then truncated away.**
  - Default per-thread share: (200000 tok × 2.5 B/tok × 0.85) × 0.55 / 3 = 77,916 B. Sources:
    `internal/memops/memops_types.go:302,322,333,340,348`.
  - A 31-day mock run's main thread (`thr_2`) had 1,065 loose excerpts totalling 304,424 B (286 B
    mean), measured with `ls turns | wc -l; cat turns/* | wc -c` in its run home.
  - `ReadThreadBody` reads newest-first until the share is reached
    (`internal/store/thread_io.go:199–209`), so the body alone fills the share and the appended
    section falls past the cut.
  - Caveat: this is inferred from the code and the measured sizes. Compose was not run. With fewer
    than 3 active threads the share is larger and part of the section survives.
- **Growth evidence** (pre-change `files.json` snapshots of a 54-day mock run): the one tracked
  file had 94 versions on day 5, 597 on day 30 and 1,125 on day 54 (116 literals; current content
  39,097 bytes). A `LiveWindow` benchmark over those snapshots went from 2.0 ms to 188.6 ms per
  call.
- **Current-turn file events render from the next turn.** Compose runs at
  `internal/turn/turn.go:912`, before close. `applyFileEdits` runs at close
  (`internal/turn/engage.go:309–311`). This design keeps that behavior.
