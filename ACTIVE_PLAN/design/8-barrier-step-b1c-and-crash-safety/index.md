## 8. Barrier step B1c and crash safety

Barrier steps (B1 drain, B1b settle, B2 day-commit with its verify gate), the completion routine
and the marker are described in ARCHITECTURE
[Crash stability and the dual-repo substrate](../../../PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md).

- **Placement.** B1c (`pruneTrackedFiles`) runs after B1b, before B2. It is a worktree-only change,
  so B2's `Add(".")` captures the deletions in primary.
- **Order.** It visits each spine record in spine order (`ListThreads`, which is `spine.jsonl`'s
  string order by id). The order does not matter: threads are pruned independently. It uses
  `turnCount` from the spine record. For each thread:
  1. rewrite `files.jsonl` without the dropped entries, only if anything changed;
  2. then delete the unreferenced files.

  A crash between the two leaves orphan files, never an index that names an absent file.
- **Idempotent; completion re-runs it.** The step is a pure function of canonical state, so a re-run
  converges. As for B1b, completion re-runs it while the day-commit has not landed.
- **Never deletes on uncertainty; recovery.** An unreadable index makes B1c skip the thread
  entirely and log `dedup.prune-skip reason=unreadable`. B2's verify gate then fails on the
  thread's tracked-file finding, and the marker is kept. The next open's completion runs
  `spineQuarantineRepair` (`internal/memops/fileadapter/fileadapter_barrier.go:501`). Each
  tracked-file verify finding names the offending file:
  - the index, when it is unreadable, unsorted, has a duplicate path or records a size its file
    does not have;
  - the missing blob or diff path, when one is absent;
  - the blob, when its bytes do not hash to its name.

  The repair treats a thread's tracked store as one unit. Any finding under
  `threads/<id>/files.jsonl` or `threads/<id>/files/` quarantines both, byte-exact, and restores
  both together from primary HEAD (`autogit.Checkout` for the index, `autogit.CheckoutTree` for
  `files/`). When HEAD has neither, both stay removed. It then re-verifies. Restoring the unit
  matters because a blob written since the last day-commit is absent from HEAD: restoring only the
  file would leave the index naming it. The repair leaves the store at its last day-commit, which
  is consistent with that day's excerpts.
- **Crashpoints.** Add `barrier.prune.postIndex` (in `store`, between the index rewrite and the
  deletes) and `barrier.postPrune.preDayCommit`, both walked by `TestBarrier_CrashWalk`. The turn
  path adds `fs.applyEvents.preIndex` (in `store`, after `ApplyFileEvents`' last blob or diff write
  and before its index write), walked by a row of `TestCrashMatrix_WTurnFileEvents`.
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
