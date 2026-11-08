#### Vocabulary bridge

Commands and their output say **thread**, the term used throughout this SPEC; there is no separate
user-facing word. Where a command acts on "the active thread", that is the thread at the front of
the active set (the most recently engaged). The command name `/topics` keeps its spelling. A thread's id
(`thr_N`) is rendered to the user unchanged because they type it back.

| Command | Purpose |
|---|---|
| `/topics [all]` | list the active project's threads. Grouped ENGAGED / PAUSED / DORMANT / RECENTLY CLOSED, recency-sorted within each group, one line per thread in the [§3.4](../../3-core-algorithms/3.4-recall-matching/index.md) thread-reference shape: `thr_N`, display name, gist, relative age. Recently-closed threads are in the default view, with the `/back-to` hint, because an auto-accepted [§3.5](../../3-core-algorithms/3.5-closing-a-thread/index.md) closing is revised that way. The default view is bounded (20 threads, then a count of what it withheld); `all` lifts the bound and the closed-window cutoff |
| `/done` | request the closing ack on the active thread ([§3.5](../../3-core-algorithms/3.5-closing-a-thread/index.md)); always fully interactive |
| `/closures` | drain the [§3.5](../../3-core-algorithms/3.5-closing-a-thread/index.md) queue of exception closings now. The same drain runs at the [§3.5](../../3-core-algorithms/3.5-closing-a-thread/index.md) drain points. An empty queue says so |
| `/pause` | mark active thread as `paused` ([§2.2.1](../../2-data-model/2.2-thread-registry-record-schema-spine-jsonl/2.2.1-thread-states.md)) |
| `/resume` | resume a paused thread |
| `/back-to <thr_id>` | re-engage a closed thread — the revision path for an auto-accepted closing; see `/topics` |
| `/project` | print active project (id, name, the project's directory, remote URL if any) |
| `/project switch <name-or-id>` | switch active project to a known one |
| `/project rename <new-name>` | rename the active project; updates `name` only (id, path, remote URL unchanged) — see [§4.5](../4.5-project-identity-and-the-project-s-directory/index.md) |
| `/stats` | memory service stats (active threads, slot fill, recent recall events, etc.) |

