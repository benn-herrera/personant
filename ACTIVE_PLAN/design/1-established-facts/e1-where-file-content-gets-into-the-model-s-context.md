### E1. Where file content gets into the model's context

- **`fs.*` delta content never reaches the request directly.** `grep -n "preEvents"
  internal/turn/turn.go` returns 695, 696, 709, 747, 890, 891. Within `RunWithInfo`'s body (which
  starts at 747) the uses are 890–891 only: the events are bounded and fed to `onContextDelta`. The request is assembled at 987–991 from the system prompt, `historyTail`,
  `userInput` and `toolMessages`, and nothing else.
- **No production code in our loop produces `fs.*` deltas.** `git grep -l -e SourceFSRead -e
  SourceFSWrite -e SourceFSCommit -- '*.go'` returns `memops/event_sources.go`, `turn/chain.go`,
  `scenarios/sim/workload.go`, and three test files. The one production `RunWithInfo` caller,
  `chat.go:803`, passes `pre`, which holds §4.4 `#` captures (`chat.go:786–788`). The sim is the
  only producer. Under opencode the producer would be the plugin.
- **Tool results in the current turn** are `tool.result` messages from the registered tools (web
  only). They are capped at the §3.1 limit (`toolloop.go:381–403`). They carry no tracked-file
  content.
- **Excerpts hold no tool output.** `renderTurnExcerpt` (`internal/turn/excerpt.go:32–55`) writes
  the header, `**user:**` and `**agent:**`, and nothing else. `History` holds `userInput` and
  `full.Content` (`turn.go:1413`). File bytes reach an excerpt or the history only when the user or
  the model quotes them.
- **The tracked-files section is the only literal source, and it is appended, not placed.**
  `renderThreadBody` (`internal/workset/workset.go:389`) appends `=== tracked files ===` after the
  body. `renderLayerB` (`workset.go:312–313`) then truncates the whole thread to its per-thread
  share, so the section is the first thing cut. So it does not duplicate excerpt text. It is a
  separate list that holds the current literal, 3 diffs and one identifier line per historical
  version.
- **In long runs the section is computed every turn and then truncated away.**
  - Default per-thread share: (200000 tok × 2.5 B/tok × 0.85) × 0.55 / 3 = 77,916 B. Sources:
    `memops_types.go:302,322,333,340,348`.
  - The run home `test/rundata/sim-workload-seed24095-dur744h0m0s.261101153613/threads/thr_2` has
    1065 loose excerpts totalling 304,424 B (286 B mean), measured with `ls turns | wc -l; cat
    turns/* | wc -c`.
  - `ReadThreadBody` reads newest-first until the share is reached (`thread_io.go:199–209`), so the
    body alone fills the share and the appended section falls past the cut.
  - Caveat: this is inferred from the code and the measured sizes. Compose was not run. With fewer
    than 3 active threads the share is larger and part of the section survives.
- **Growth evidence** (`.claude-temp/growth/files/day*.json`, summarized with a `python3 -I` JSON
  walk): the one tracked file had 94 versions on day 5, 597 on day 30 and 1125 on day 54 (116
  literals; current literal 38,891 B). The `LiveWindow` benchmark went from 2.0 ms to 188.6 ms per
  call (`.claude-temp/growth/bench-threadfiles.txt`).
- **Current-turn file events render from the next turn.** Compose runs at turn.go:912, before close.
  `applyFileEdits` runs at close (`engage.go:309–311`). This design keeps that behavior.

