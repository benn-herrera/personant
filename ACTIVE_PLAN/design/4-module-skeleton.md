## 4. Module skeleton

Dependency direction: `turn → memops (port), dedup` (identifier lines);
`fileadapter → store, workset, dedup`; `store → dedup`; `verify → store` (existing edge; blob hashing
goes through the store's blob reader, so verify gains no `dedup` import); `workset →` (no `dedup`
import any longer); `dedup → go-git plumbing` only.

| Module | Owns | Must not |
|---|---|---|
| `internal/dedup` (pure leaf) | `ContentID(bytes)` (git blob id, via `plumbing.ComputeHash`); the identifier-line renderer for a turn's events; forward unified `Diff(old,new)` with the existing `lcsLineProductMax` guard and the `diff-literal-threshold` keep/skip decision; `Plan(threads, literalMax) → expansions per (thread, turn)`, with the `LiteralShareThreshold` constant (invariant 11); `Retain(entries, turnCount, window) → keep set` used by B1c | do I/O; know file names or the JSONL format; know rendering labels |
| `internal/store` (a new file; `threadfiles.go` is deleted) | the store format ([§5](5-store-format-and-retention/index.md)): index read/write, blob and diff write/read, `ApplyFileEvents`, `PruneTrackedFiles`; a newest-first excerpt reader that lets the caller stop | call git; decide what renders; compute budgets |
| `internal/memops` (port) | `RecordFileEvents(ctx, threadID, turn, at, events)` and `FileEvent{Kind, Path, Content}` | keep `RecordFileWrite`, `RecordFileCommit`, `AgeFileChains`, `GetFileVersion` |
| `internal/memops/fileadapter` | compose fetch: load indexes → `dedup.Plan` → budgeted newest-first walk loading only planned expansions; turn-scope recording of new store files; barrier step B1c | render presentation text itself (it calls `workset`'s per-excerpt renderer to size units) |
| `internal/workset` | presentation: excerpts in chronological order, expansions inside their excerpt with delimiters; the per-excerpt render function the adapter uses for sizing | read substrate; plan; append a separate tracked-files section |
| `internal/turn` | turn-close orchestration: identifier lines into the owner excerpt (from `state.fileEdits` via `dedup`), then one `RecordFileEvents` for the owner's read/write events; reject `\n` paths at buffer time | compute ids its own way; call aging; apply edits to a thread other than the owner |

---

