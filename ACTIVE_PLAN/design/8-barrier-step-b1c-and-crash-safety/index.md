## 8. Barrier step B1c and crash safety

- **Placement.** B1c (`pruneTrackedFiles`) runs after B1b, before B2. It is a worktree-only change,
  so B2's `Add(".")` captures the deletions in primary.
- **Order.** It visits each spine record in ascending id order, with `turnCount` from the spine. For
  each thread:
  1. rewrite `files.jsonl` without the dropped entries, only if anything changed;
  2. then delete the unreferenced files.

  A crash between the two leaves orphan files, never an index that names an absent file.
- **Idempotent; completion re-runs it.** The step is a pure function of canonical state, so a re-run
  converges. As for B1b, completion re-runs it while the day-commit has not landed.
- **Never deletes on uncertainty.** An unreadable index skips the thread entirely and logs
  `dedup.prune-skip reason=unreadable`. B2's verify gate then fails on it, the same path a
  hand-broken folio takes.
- **Crashpoints.** Add `barrier.prune.postIndex` and `barrier.postPrune.preDayCommit`, both walked
  by `TestBarrier_CrashWalk`.
- **Turn path.**
  - Every new blob, diff and index write goes into the turn scope after it succeeds, so it takes the
    fast `AddPaths` path.
  - A torn turn resets to daily HEAD and quarantines untracked blobs (`resetSequence`,
    `sweepUntrackedDebris`), so excerpt and store revert together.
  - Store writes use `WriteFileAtomic`; a torn temp file is swept by `sweepTmpResidue`.
- **Archival.** It captures and restores `files.jsonl` and `files/` as part of the thread subtree.
  `hashDirTree` already recurses.

Contents:

- [§8 Migration (format 2 → 3)](migration-format-2-3.md)
