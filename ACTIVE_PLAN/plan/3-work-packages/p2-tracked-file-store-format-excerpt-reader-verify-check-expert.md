### P2 — Tracked-file store format, excerpt reader, verify check (expert)

**Scope.**
- `internal/store/`: `trackedfiles.go` (new; the store), `relpaths.go` (the new rel paths), `thread_io.go` (the newest-first reader; `ReadThreadBody` rebuilt on it), and their
  tests. `threadfiles.go` is not edited; P6 deletes it.
- `internal/memops/memops_types.go`: `FileEvent{Kind, Path, Content}` and its kinds (read, write),
  which the store and, from P3, the port share (design [§4](../4-acceptance-criteria.md), `internal/memops` row).
- `internal/verify/verify.go` and `verify_test.go`: the tracked-file check.

**Design to read:** [§3](index.md) invariants 4, 7, 10, [§4](../4-acceptance-criteria.md) (`internal/store` and `internal/memops` rows),
§5.1, §5.2, §8 "Turn path", §12 criterion 8.

**Facts.**
- `grep -n "^func WriteJSONL\|^func ReadJSONL\|jsonlMaxLineBytes" internal/store/jsonl.go` →
  `ReadJSONL[T any](path string) ([]T, error)` at 28, and
  `WriteJSONL[T any](path string, records []T, keyFn func(T) string) error` at 69. The comment
  above `WriteJSONL`: "sorted by keyFn ascending … Atomic … Duplicate keys are detected before any
  write and returned as an error". It is the index writer; key = `path`.
- `grep -n "^func WriteFileAtomic" internal/store/atomic.go` → `57:func WriteFileAtomic(path string, data []byte) error`.
  Blobs and diffs are written with it, before the index (invariant 7).
- `grep -n "^const ThreadTurnWindow" internal/store/thread_io.go` → `36:const ThreadTurnWindow = 512`.
- `ReadThreadBody` (`internal/store/thread_io.go`, the function after `LoadThreadFrontmatter`) walks
  `threadExcerptRefs(paths, threadID, ThreadTurnWindow)` newest-first. It stops once
  `total >= byteBudget`, then joins the excerpts chronologically with `"\n\n"`. The new reader
  yields `memops.ThreadExcerpt{TurnNumber, Text}` newest-first within the same window and lets the
  caller stop. `ReadThreadBody` is re-expressed on it, so one walk exists; its behavior, budget rule
  included, is unchanged.
- `ReadThreadBody` callers stay: `git grep -l 'ReadThreadBody' -- '*.go'` →
  `internal/memops/fileadapter/fileadapter_archive.go`, `fileadapter_test.go`,
  `workset_compose.go`, `internal/prompt/excerptgenre_probe_test.go`, `internal/store/folio_test.go`,
  `thread_io.go`, `thread_io_test.go`, `internal/turn/emptyresponse_test.go`,
  `internal/workset/workset.go`, `workset_test.go`. Its signature does not change.
- `memops` imports only `personant/internal/version` (`go list -deps ./internal/memops | grep personant/`),
  and `store` already imports `memops`. So `memops.FileEvent` is visible to the store with no new
  edge.
- `verify.Verify` runs `checkThreads(&report, paths, spine)` then `checkFolios(&report, paths, spine)`
  (`internal/verify/verify.go:81–82`). The tracked-file check is added beside them. B2's gate runs
  `verify.Verify` (`git grep -n "verify\.Verify" -- internal/memops/fileadapter/fileadapter_barrier.go`
  → lines 408, 425, 506), so a store that fails the check fails the barrier's verify gate.
- Verify does not read `files.json` today (`grep -n -i "files" internal/verify/verify.go` →
  comment lines only, none naming `files.json`). The new check reads only the new store.

**Work.**
1. Store layout and write order per design §5.1–§5.2, `size` included.
   - `ApplyFileEvents(paths, threadID, turn, at, events)` writes the files and returns the
     repo-relative paths it wrote, for the adapter's turn scope.
   - A loader validates sort order and `path` uniqueness.
   - A blob reader checks `ContentID(bytes)` against the name and returns an error on mismatch.
   - The store record maps to `dedup`'s Plan entry in one place in `store`; the adapter never
     copies fields by hand.
2. The newest-first excerpt reader, and `ReadThreadBody` on top of it.
3. Verify: each live thread's index decodes sorted and unique. Every blob and `diff:true` file it
   names exists, and every blob hashes to its name. Verify uses the store's loader and blob
   reader for this; it does not import `dedup` (`verify → store` is an existing edge). A violation is a `VerifyReport` error naming the
   thread and file. A thread with no index is clean.

**Tests.**
- Store:
  - first write, then a change, gives one blob, one diff and one index line;
  - an unchanged read moves only `turn`/`at`;
  - `changes` trims to N;
  - a skipped diff records `diff:false` and writes no file;
  - a missing index loads as no files;
  - an unsorted or duplicate index fails to load;
  - a tampered blob fails the read.
- Reader: early stop, the window bound, and the existing `ReadThreadBody` tests unchanged.
- Verify: missing blob, missing diff, hash mismatch, unsorted index; a clean store passes.

**Done-line.** `just build`;
`just test-run ./internal/store 'TestFileStore|TestReadThreadBody|TestAppendThreadTurnAndReadBody|TestLoadThreadAssemblesBody|TestExcerptsNewestFirst'`;
`just test-run ./internal/verify 'TestVerify|TestTrackedFiles'`.

**Checkpoint C1.** `just test`, then commit. Nothing in production writes the store yet, so no
contract document changes at C1.

---

