### P5 — Barrier step B1c, format 2 → 3 (expert)

**Precondition.** P4's evidence: its compose tests pass, and P4's report quotes the
`just DURATION=1d sim` line `dedup-expansions: literal=<n> diff=<n>` with `literal` above zero.

**Scope.**
- `internal/store/trackedfiles.go`: `PruneTrackedFiles` with crashpoint
  `barrier.prune.postIndex`, and its test.
- `internal/memops/fileadapter/fileadapter_barrier.go`: the B1c step and
  `barrier.postPrune.preDayCommit`, in both the fresh run and the completion re-run.
- `internal/memops/fileadapter/fileadapter_barrier_test.go`: `TestBarrier_CrashWalk` points and a
  B1c test.
- `internal/scenarios/crash_matrix_test.go`: `crashScenarioCoverage` entries, and a W-TURN case
  with file events.
- `internal/memops/homeformat.go`, `internal/version/version.go`, `internal/memops/homeformat_test.go`.
- An archive round-trip test in `internal/memops/fileadapter/fileadapter_test.go`.
- The criterion-7 migration test in `internal/memops/fileadapter/fileadapter_homeformat_test.go`.
- The contract leaves listed under "Contract".

**Design to read:** [design §5.3](../../design/5-store-format-and-retention/5.3-retention.md);
[design §8](../../design/8-barrier-step-b1c-and-crash-safety/index.md) and
[design §8.1](../../design/8-barrier-step-b1c-and-crash-safety/8.1-migration-format-2-to-3.md);
[design §12](../../design/12-acceptance-criteria.md) criteria 2, 5, 6, 7.

**Facts.**
- Barrier order. `internal/memops/fileadapter/fileadapter_barrier.go` `runBarrier` runs
  `drainArchivalPressure`, `crashpoint.At(cpBarrierPostArchival)`, `settleExcerptTiers`,
  `crashpoint.At(cpBarrierPostSettle)`, then `barrierTail` (lines 172–184). B1c goes after
  `cpBarrierPostSettle` and before `barrierTail`. In the completion path (`case memops.PendingBarrier`,
  around line 851) it is re-run inside `if !sealed { … settleExcerptTiers … }`, as B1b is.
- Crashpoint registration pattern: `cpBarrierPostSettle = crashpoint.Register("barrier.postSettle.preDayCommit")`
  (`fileadapter_barrier.go:63`). The store registers its own crashpoints the same way
  (`internal/store/folio.go:32–35`, the four `folio.settle.*` points).
- Coverage gate. `internal/scenarios/crash_matrix_test.go` `TestCrashPointCoverageGate` requires
  every registered name to map to a covering scenario in `crashScenarioCoverage` (file comment,
  lines 20–24). The B1b points map to `"fileadapter.TestBarrier_CrashWalk"` (lines 61–75).
  `TestBarrier_CrashWalk` has a top-level `points` list (`fileadapter_barrier_test.go:224–240`) and
  a settle section (`folioPoints`, from about line 302) that kills on a fixture giving the step
  work. B1c points go in a section of the same kind, on a fixture with prunable files.
- Spine turn count. Retention uses each spine record's `TurnCount` (design §5.3). The store's
  `turn` is the same counter: the owner's `mergeTurn` is `rec.TurnCount` advanced
  (`internal/turn/engage.go:585–587`, `rec.TurnCount = mergeTurn` at 651).
- Legacy sidecar name. `internal/store/relpaths.go:17` `threadFilesFileName = "files.json"`. B1c
  deletes that file; the constant stays for that purpose after P6.
- Format registry. `internal/memops/homeformat.go:44`
  `var homeFormatMigrations = []HomeFormatMigration{{From: 1, To: 2}}`;
  `internal/version/version.go:40` `const CurrentHomeFormat = 2`.
  `TestGateHomeFormat` passes `current` explicitly per case. Two cases assume no 2 → 3 step and
  fail once it is registered: `grep -n 'current: 3' internal/memops/homeformat_test.go` → 81
  ("older home with no registered step is refused", `onDisk: 2`) and 88 ("chain breaks partway",
  `onDisk: 1`). Both move to `current: 4`. The cases with `Format: 2` (lines 54, 61, 69, 75) pass
  `current: 2` explicitly and stay valid. Other tests compare against `version.CurrentHomeFormat`
  symbolically (`git grep -n CurrentHomeFormat -- '*_test.go'`) and need no edit.
- Archival covers subdirectories already. A thread directory holds `turns/`, and
  `hashDirTree` recurses (`internal/autogit/autogit.go:592–597`, the `de.IsDir()` arm). So `files/`
  takes the same path. The round-trip test is the evidence.
- Torn turns. New blobs are untracked until the turn commit, and a torn turn's reset quarantines
  untracked files (design §8 "Turn path"). No new mechanism is needed, but no test exercises it:
  `TestCrashMatrix_WTurn` (`internal/scenarios/crash_matrix_test.go:206`) drives
  `turn.RunWithInfo(context.Background(), h.State, nil, "crash-probe turn", io.Discard)`, with `nil`
  pre-events and so no file events. Criterion 6's torn-turn half needs a W-TURN case whose turn
  carries `fs.read` / `fs.write` deltas.

**Work.**
1. `PruneTrackedFiles(paths, threadID, turnCount)`, built on `dedup.Retain`:
   - rewrite the index only if an entry drops;
   - `crashpoint.At` the `barrier.prune.postIndex` point;
   - delete unreferenced files under `files/`, and `files.json`;
   - an unreadable index deletes nothing and returns a skip reason.
2. B1c in the adapter:
   - spine records in numeric id order (`thr_2` before `thr_10`);
   - log `dedup.pruned` (thr, files, bytes) and `dedup.prune-skip` (thr, reason);
   - the crashpoint after the step;
   - the completion re-run.
3. Format: add `{From: 2, To: 3}` and set `CurrentHomeFormat = 3`; it is a restamp only.
4. Tests:
   - `TestBarrier_Prune…`: after many edits and one barrier, the thread holds one blob, ≤ N diffs
     and one index line (criterion 2), and its `turns/` and `folio.jsonl` bytes are identical
     before and after the barrier (criterion 5);
   - entries past the window are dropped;
   - `TestBarrier_PruneUnreadableIndexRecovers`: an unreadable index makes B1c skip the thread
     (`dedup.prune-skip reason=unreadable`) and fails B2's verify gate with the index as the
     finding's path. The next open's completion (`spineQuarantineRepair`) restores the index from
     primary HEAD, and the barrier then completes (design §8);
   - a crash at either point converges on completion and never deletes an indexed file;
   - `TestHomeFormat_Migrate2To3RemovesLegacySidecar` (`fileadapter_homeformat_test.go`): a home
     stamped 2 and holding a thread `files.json` passes `memops.GateHomeFormat` with
     `StampFormat: 3, MigratedFrom: 2`, is restamped through `StampHomeFormat`, and holds no
     `files.json` after its first `MaybeDayBarrier` (criterion 7);
   - an archive round-trip of a thread with `files/`;
   - a W-TURN crash case in `crash_matrix_test.go`, named with the prefix
     `TestCrashMatrix_WTurn`, with file events in the turn: after recovery
     the owner excerpt and the store agree with daily HEAD, and `verify.Verify` reports no
     tracked-file error.

**Contract.** The text to land is in
[design §9](../../design/9-proposed-spec-text-replaces-the-leaves-under-project-docs-spec-3-core-algorithms-3-9-working-set-content-dedup.md)
(SPEC) and [design §10](../../design/10-architecture-changes-same-change.md) (ARCHITECTURE).
- SPEC §3.9.1: `3.9.1-persistent-storage-thread-file-body-log.md` is replaced by
  `3.9.1-persistent-storage-and-retention.md`, with the text in design §9. Every link to the old
  file name is updated: `grep -rln "3.9.1-persistent-storage-thread-file-body-log" PROJECT_DOCS *.md`
  → at `c23b036`:
  `PROJECT_DOCS/SPEC/3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/3.0.2-the-hook-chain.md`,
  `PROJECT_DOCS/SPEC/3-core-algorithms/3.9-working-set-content-dedup/index.md`,
  `PROJECT_DOCS/SPEC/3-core-algorithms/3.8-deep-cold-archival/3.8.3-recovery.md`,
  `PROJECT_DOCS/SPEC/3-core-algorithms/3.10-transient-data-lifecycle/3.10.6-what-stays-transient.md`,
  `PROJECT_DOCS/SPEC/entry-point.md`; plus
  `PROJECT_DOCS/SPEC/2-data-model/2.3-thread-file-format-threads-thr-id.md` (added by P3), and any
  SPEC §3.9.1 link P4 added. Re-run the command; after the fix it returns nothing.
- SPEC §3.8.1 (`3.8.1-mechanism.md`) and SPEC §3.10.6 (`3.10.6-what-stays-transient.md`): the
  edits in design §9.
- SPEC §2.6.1: the `dedup.diff-literal-threshold` entry, including its continuation comment,
  becomes the design's wording.
- ARCHITECTURE `mechanisms/crash-stability-and-the-dual-repo-substrate.md`: the three edits in
  design §10 (B1c sentences, crashpoint list, registry).
- ARCHITECTURE `testing-as-the-lab-bench/event-log-format.md`: add `pruned` and `prune-skip` to the
  `dedup` row (P6 removes the `chain-*` actions).

**Done-line.** `just build`;
`just test-run ./internal/store 'TestPruneTrackedFiles'`;
`just test-run ./internal/memops/fileadapter 'TestBarrier_CrashWalk|TestBarrier_Prune|TestArchiveThenRecover|TestHomeFormat_'`;
`just test-run ./internal/memops 'TestGateHomeFormat|TestHomeFormatMigrationChainReachesCurrent'`;
`just test-run ./internal/scenarios 'TestCrashPointCoverageGate|TestCrashMatrix_WTurn'`;
`just check-links`.

---

