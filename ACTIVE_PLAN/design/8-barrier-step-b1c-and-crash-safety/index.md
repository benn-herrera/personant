## 8. Barrier step B1c and crash safety

Barrier steps (B1 drain, B1b settle, B2 day-commit with its verify gate), the completion routine
and the marker are described in ARCHITECTURE
[Crash stability and the dual-repo substrate](../../../PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md).

- **Placement.** B1c (`pruneTrackedFiles`) runs after B1b, before B2. It is a worktree-only change,
  so B2's `Add(".")` captures the deletions in primary.
- **Order.** It visits each spine record in numeric id order (`thr_2` before `thr_10`), with
  `turnCount` from the spine record. For each thread:
  1. rewrite `files.jsonl` without the dropped entries, only if anything changed;
  2. then delete the unreferenced files.

  A crash between the two leaves orphan files, never an index that names an absent file.
- **Idempotent; completion re-runs it.** The step is a pure function of canonical state, so a re-run
  converges. As for B1b, completion re-runs it while the day-commit has not landed.
- **Never deletes on uncertainty; recovery.** An unreadable index makes B1c skip the thread
  entirely and log `dedup.prune-skip reason=unreadable`. B2's verify gate then fails on the
  thread's tracked-file finding, and the marker is kept. The next open's completion runs the same
  repair a hand-broken folio gets, `spineQuarantineRepair`
  (`internal/memops/fileadapter/fileadapter_barrier.go:467`). For each finding's path, that repair
  quarantines the file byte-exact, then restores it from primary HEAD or, when HEAD lacks it,
  removes it as an orphan add. It then re-verifies. For that to work, each tracked-file verify
  finding names the offending file:
  - the index, when it is unreadable, unsorted or has a duplicate path;
  - the missing blob or diff path, when one is absent;
  - the blob, when its bytes do not hash to its name.

  The repair leaves the store at its last day-commit, which is consistent with that day's excerpts.
- **Crashpoints.** Add `barrier.prune.postIndex` (in `store`, between the index rewrite and the
  deletes) and `barrier.postPrune.preDayCommit`, both walked by `TestBarrier_CrashWalk`.
- **Turn path.**
  - Every new blob, diff and index write goes into the turn scope after it succeeds, so it takes the
    fast `AddPaths` path.
  - A torn turn resets to daily HEAD and quarantines untracked blobs (`resetSequence`,
    `sweepUntrackedDebris`), so excerpt and store revert together.
  - Store writes use `WriteFileAtomic`; a torn temp file is swept by `sweepTmpResidue`.
- **Archival.** It captures and restores `files.jsonl` and `files/` as part of the thread subtree.
  `hashDirTree` already recurses.

Contents:

- [§8.1 Migration (format 2 → 3)](8.1-migration-format-2-to-3.md)
