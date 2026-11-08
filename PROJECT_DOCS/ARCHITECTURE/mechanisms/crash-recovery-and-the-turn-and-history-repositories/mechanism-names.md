#### Mechanism names

Core recovery (`internal/recovery`) only detects
day-rollover and archival cases and returns a typed `RecoveryReport.Pending`
(`PendingCompletion{Kind, Day}`, plus `ScopedRestored` for quarantined-and-restored paths);
`fileadapter.Reconcile`, which owns `ArchiveThreads` and both repository handles, completes them,
which keeps `recovery` from importing the adapter. `recovery.LocateDeletionCommit`
(`internal/recovery/stamp.go`) finds an already-landed deletion commit by a
child-of-`ParentCommitHash` tree test. The day rollover mints its day-commit with
`AllowEmptyCommits` after `CheckSpineIntegrity` on the staged tree, asserts `CheckDerivedFresh` in
the freshness-assert step, and derives lifecycle tags through `autogit.TagStamp` / `ParseTagStamp`
(the only formatters), reading the sealed day's log with the tolerant `eventlog` reader and
tolerating `ErrTagExists`. The excerpt-storage step (`settleExcerptTiers`, between the archival
step's batch and the day-commit step) calls `store.SettleExcerptTiers` per thread registry record in
ascending id order with `memops.IsRetiredState` (a closed thread) as the signal to bundle every
excerpt; completion re-runs it after the archival batch while the day-commit has not landed. Per
thread it writes each excerpt that belongs in the loose tier as a loose file and fsyncs `turns/` and
the thread directory, then writes the excerpt bundle (`WriteFileAtomic`, which does not fsync the
parent) and fsyncs the thread directory, reads it back, and only then removes the loose files now
bundled — so every excerpt is readable from at least one tier at every crash point. Convergence: a
name in both tiers with identical bytes is an unfinished move and settle keeps the target-tier copy;
different bytes are a hand edit, and neither copy moves (`folio-skip reason=conflict`). A torn
excerpt-bundle temp file (`tmp-folio.jsonl`) is quarantined by recovery's unconditional `tmp-`
debris cleanup. A hand-broken excerpt bundle is skipped by settle (`reason=unreadable`) and fails
the day-commit step's integrity check on the live run, operation marker kept; the next open's
completion runs `spineQuarantineRepair`, quarantining the bundle byte-exact and scoped-restoring it
from history repository HEAD (or removing it as a path absent from history repository HEAD), which is consistent with the
thread's loose files because tiers change only at a day rollover. The file-retention step
(`pruneTrackedFiles`, between the excerpt-storage step and the day-commit step) calls
`store.PruneTrackedFiles` per thread registry record, in registry order, with the record's
`TurnCount`. It rewrites a thread's `files.jsonl` only if an entry drops, then deletes the files no
kept entry names, so a crash leaves orphans, never a dangling index. Completion re-runs it under the
same condition as the excerpt-storage step. An unreadable index is skipped
(`dedup.prune-skip reason=unreadable`) and fails the day-commit step's integrity check. Any
tracked-file finding makes completion's `spineQuarantineRepair` quarantine the thread's
`files.jsonl` and `files/` together and restore both from history repository HEAD. Crashpoints: the
four `folio.settle.*` and `barrier.prune.postIndex` in `store`, and
`barrier.postSettle.preDayCommit` and `barrier.postPrune.preDayCommit`, all walked by
`TestBarrier_CrashWalk`; and `fs.applyEvents.preIndex` in `store`, between a turn's tracked-file
writes and its index write, walked by `TestCrashMatrix_WTurnFileEvents`. Recovery handles and plumbs
the `op=rebaseline` operation marker, the mid-day re-baseline that would delete and recreate the
turn repository at a commit boundary when its loose objects cross a threshold, without touching the
history repository or advancing the day; recovery routes an `op=rebaseline` crash straight to
turn-repository init. The mid-day arming trigger is not wired (ROADMAP.md).
Packing: `MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`) packs both repositories. After every `gcCheckEveryCommits` (64) turn-repository commits the adapter reads the turn
repository's loose-object count and runs `Consolidate` at `gcLooseObjectThreshold` (5000); the simulation's day off (the harness's `SleepCycle` step) also runs it. Packing is safe at any time because archived blobs stay reachable through the deletion
commit's parent and Personant never rewrites the history repository's commits (the turn repository
is disposable). No aggressive repack (`git gc --aggressive`) is scheduled. Session checkpoint commits target the turn repository with a full `Add(".")` staging
pass. The open sequence runs `memops.GateHomeFormat` before Reconcile;
`version.UnversionedHomeFormat` is pinned at 1 forever and is never an alias of
`version.CurrentHomeFormat`, so an unmigrated legacy store directory keeps resolving to format
revision 1. `GateHomeFormat` migrates an older store directory by walking the table
`homeFormatMigrations` (steps keyed by from-revision; 1→2 and 2→3 change only the format revision:
revision 2 adds optional excerpt bundles, revision 3 the tracked-file store, whose legacy
`files.json` the file-retention step removes), the caller writes the format revision before
Reconcile, and a crash between that write and the log leaves a current-revision store directory that
needs nothing further.

