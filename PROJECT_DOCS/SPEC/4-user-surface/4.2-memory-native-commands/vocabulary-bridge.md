#### Vocabulary bridge

User-facing surfaces say **topic**; a topic is stored as a **thread**
(`thr_N`). "Thread" remains the term below the user surface — data model, algorithms, the [§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md) event
log, and the id scheme, which is rendered to the user unchanged because they type it back.

| Command | Purpose |
|---|---|
| `/topic <name>` | force a new topic with the given working name. The first word is read as a subcommand (see `/topic rename`), mirroring `/project`'s parse; a topic cannot be CREATED with a name whose first word is `rename` |
| `/topic rename <new-name>` | rename the ACTIVE topic's [§2.2.2](../../2-data-model/2.2-spine-record-schema-spine-jsonl/2.2.2-display-form.md) display name. **Display-cosmetic**: the id, the anchors, `history_symbols`, the [§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md) `thr=` details, recall matching and every stored turn excerpt are unchanged. Writes `description` (and `summary` when it duplicates `description` — the shape `/topic` creation leaves) to both the canonical [§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md) frontmatter and the derived spine record. Old names are NOT aliases: `/back-to <old-name>` stops resolving. Duplicate display names are permitted; the ambiguity surfaces in the name resolver. Not a [§3.11](../../3-core-algorithms/3.11-substrate-commit-cadence/index.md) structural change, so it rides the next checkpoint commit; logged `thread.renamed` |
| `/topics [all]` | list the active project's topics. Grouped ENGAGED / PAUSED / DORMANT / RECENTLY CLOSED, recency-sorted within each group, one line per topic in the [§3.4](../../3-core-algorithms/3.4-recall-matching/index.md) topic-reference shape: `thr_N`, display name, gist, relative age. Recently-closed topics are in the default view, with the `/back-to` hint, because an auto-accepted [§3.5](../../3-core-algorithms/3.5-closure-flow/index.md) closure is revised that way. The default view is bounded (20 topics, then a count of what it withheld); `all` lifts the bound and the closed-window cutoff |
| `/done` | request closure ack on the active topic ([§3.5](../../3-core-algorithms/3.5-closure-flow/index.md)); always fully interactive |
| `/closures` | drain the [§3.5](../../3-core-algorithms/3.5-closure-flow/index.md) pending-review queue now. The same drain runs at the [§3.5](../../3-core-algorithms/3.5-closure-flow/index.md) boundary. An empty queue says so |
| `/pause` | mark active topic as `paused` ([§2.2.1](../../2-data-model/2.2-spine-record-schema-spine-jsonl/2.2.1-thread-states.md)) |
| `/resume` | resume a paused topic |
| `/back-to <thr_id>` | re-engage a closed topic — the revision path for an auto-accepted closure; see `/topics` |
| `/no-revisit` | tighten threshold on the most recent recall surface ([§3.4](../../3-core-algorithms/3.4-recall-matching/index.md)) |
| `/project` | print active project (id, name, root path, remote URL if any) |
| `/project switch <name-or-id>` | switch active project to a known one |
| `/project rename <new-name>` | rename the active project; updates `name` only (id, path, remote URL unchanged) — see [§4.5](../4.5-project-identity-and-project-root/index.md) |
| `/stats` | runtime stats (active topics, layer fill, recent recall events, etc.) |

