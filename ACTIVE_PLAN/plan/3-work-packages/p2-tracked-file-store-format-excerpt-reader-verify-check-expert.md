### P2 — Tracked-file store format, excerpt reader, verify check (expert)

**Scope.**
- `internal/store/`:
  - `internal/store/trackedfiles.go` (new; the store);
  - `internal/store/relpaths.go` (the new rel paths);
  - `internal/store/thread_io.go` (the newest-first reader, with `ReadThreadBody` rebuilt on it);
  - their tests.

  `internal/store/threadfiles.go` is not edited; P6 deletes it.
- `internal/memops/memops_types.go`: `FileEvent{Kind, Path, Content}` and its kinds (read, write),
  which the store and, from P3, the port share
  ([design §4](../../design/4-module-skeleton.md), `internal/memops` row).
- `internal/verify/verify.go` and `internal/verify/verify_test.go`: the tracked-file check.

**Design to read:** [design §3](../../design/3-invariants.md) invariants 4, 7, 10;
[design §4](../../design/4-module-skeleton.md) (`internal/store` and `internal/memops` rows);
[design §5.1](../../design/5-store-format-and-retention/5.1-layout.md);
[design §5.2](../../design/5-store-format-and-retention/5.2-write-at-turn-close-applyfileevents.md);
[design §8](../../design/8-barrier-step-b1c-and-crash-safety/index.md) "Never deletes on uncertainty;
recovery" and "Turn path"; [design §12](../../design/12-acceptance-criteria.md) criterion 8.

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
  `internal/memops/fileadapter/fileadapter_archive.go`, `internal/memops/fileadapter/fileadapter_test.go`,
  `internal/memops/fileadapter/workset_compose.go`, `internal/prompt/excerptgenre_probe_test.go`,
  `internal/store/folio_test.go`, `internal/store/thread_io.go`, `internal/store/thread_io_test.go`,
  `internal/turn/emptyresponse_test.go`, `internal/workset/workset.go`,
  `internal/workset/workset_test.go`. Its signature does not change.
- `memops` imports only `personant/internal/version` (`go list -deps ./internal/memops | grep personant/`),
  and `store` already imports `memops`. So `memops.FileEvent` is visible to the store with no new
  edge.
- `verify.Verify` runs `checkThreads(&report, paths, spine)` then `checkFolios(&report, paths, spine)`
  (`internal/verify/verify.go:81–82`). The tracked-file check is added beside them. B2's gate runs
  `verify.Verify` (`git grep -n "verify\.Verify" -- internal/memops/fileadapter/fileadapter_barrier.go`
  → lines 408, 425, 506), so a store that fails the check fails the barrier's verify gate.
- Recovery from a failed gate is path-driven. `spineQuarantineRepair`
  (`internal/memops/fileadapter/fileadapter_barrier.go:467`) maps each finding's `Path`
  (`a.findingRel(f.Path)`) to a file it quarantines and then restores from primary HEAD. So each
  tracked-file finding's `Path` must name the offending file (design §8).
- Verify does not read `files.json` today (`grep -n -i "files" internal/verify/verify.go` →
  comment lines only, none naming `files.json`). The new check reads only the new store.

**Work.**
1. Store layout and write order per design §5.1–§5.2: both `size` fields, the no-entry first
   case (no change recorded), and `diff:<kept>`.
   - `ApplyFileEvents(paths, threadID, turn, at, events)` writes the files and returns the
     repo-relative paths it wrote, for the adapter's turn scope.
   - A loader validates sort order and `path` uniqueness.
   - A blob reader checks `ContentID(bytes)` against the name and returns an error on mismatch.
   - The store record maps to `dedup`'s Plan entry in one place in `store`; the adapter never
     copies fields by hand.
2. The newest-first excerpt reader, and `ReadThreadBody` on top of it.
3. Verify: each live thread's index decodes sorted and unique. Every blob and `diff:true` file it
   names exists, and every blob hashes to its name. Verify uses the store's loader and blob reader
   for this; it does not import `dedup` (`verify → store` is an existing edge). A violation is a
   `VerifyReport` error whose `Path` names the offending file. `Path` is home-relative and
   slash-separated, as existing verify findings are: `checkThreads` uses
   `filepath.Join("threads", rec.ID, "thread.md")` (`internal/verify/verify.go:217`), and
   `findingRel` (`internal/memops/fileadapter/fileadapter_barrier.go:568–575`) maps such a path to
   the file to quarantine. For thread `thr_1`, the finding's `Path` is:
   - the index, `threads/thr_1/files.jsonl`, when it is unreadable, unsorted or has a duplicate
     path;
   - the missing file, `threads/thr_1/files/<id>` or `threads/thr_1/files/<from>-<to>.diff`, when
     one is absent;
   - the blob, `threads/thr_1/files/<id>`, when its bytes do not hash to its name.

   A thread with no index is clean.

**Tests.**
- Store (`internal/store/trackedfiles_test.go`, names `TestFileStore…`):
  - a first write creates the entry with empty `changes` and one blob;
  - a later change adds one diff and one `changes` record with `diff:true` and the diff's `size`;
  - an unchanged read moves only `turn`/`at`;
  - `changes` trims to N;
  - a skipped diff records `diff:false`, `size` 0, and writes no file;
  - a missing index loads as no files;
  - an unsorted or duplicate index fails to load;
  - a tampered blob fails the read.
- Reader (`internal/store/thread_io_test.go`, names `TestExcerptsNewestFirst…`): early stop, the
  window bound; the existing `ReadThreadBody` tests pass unchanged.
- Verify (`internal/verify/verify_test.go`, names `TestTrackedFiles…`): unreadable index,
  unsorted index, duplicate path, missing blob, missing diff, hash mismatch. Each asserts the
  finding's exact `Path` from Work 3; for example, the missing-blob test on thread `thr_1`
  expects `threads/thr_1/files/<id of the removed blob>`. A clean store passes.

**Done-line.** `just build`;
`just test-run ./internal/store 'TestFileStore|TestReadThreadBody|TestAppendThreadTurnAndReadBody|TestLoadThreadAssemblesBody|TestExcerptsNewestFirst'`;
`just test-run ./internal/verify 'TestVerify|TestTrackedFiles'`.

**Checkpoint C1.** `just test`, then commit. Nothing in production writes the store yet, so no
contract document changes at C1.

---
