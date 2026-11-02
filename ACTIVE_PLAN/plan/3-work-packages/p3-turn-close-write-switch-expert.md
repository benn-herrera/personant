### P3 — Turn-close write switch (expert)

**Scope.**
- `internal/memops/memops.go`: add `RecordFileEvents(ctx, threadID, turn, at, events)` to the
  port. The old file methods stay until P6.
- `internal/memops/fileadapter/fileadapter.go`: implement `RecordFileEvents`.
- `internal/turn/chain.go`: `fileEditKind` distinguishes read from write; `bufferFileEdit` rejects a
  path containing `\n`.
- `internal/turn/excerpt.go`: `renderTurnExcerpt` takes the turn's identifier lines.
- `internal/turn/engage.go`: the two owner render sites, and `applyFileEdits` replaced by one
  `RecordFileEvents` call.
- Turn and scenario tests that read the old store (listed below).
- `internal/scenarios/invariants.go`, `restart.go`, `invariants_test.go`: `VerifyDedupConsistency`
  removed.
- `internal/scenarios/sim/workload.go`: comment fix.
- The contract leaves listed under "Contract".

**Design to read:**
[design E1](../../design/1-established-facts/e1-where-file-content-gets-into-the-model-s-context.md),
[design E2](../../design/1-established-facts/e2-what-identifiers-can-attach-to.md);
[design §2](../../design/2-design-in-one-paragraph.md);
[design §3](../../design/3-invariants.md) invariants 2, 8, 10;
[design §4](../../design/4-module-skeleton.md) (`internal/turn` and port rows);
[design §5.1](../../design/5-store-format-and-retention/5.1-layout.md) (`turn`, `at`, `path`);
[design §5.2](../../design/5-store-format-and-retention/5.2-write-at-turn-close-applyfileevents.md);
[design §7](../../design/7-fate-of-current-mechanisms.md) rows for `RecordFileWrite`,
`fs.commit-untracked`, `VerifyDedupConsistency` and `buildWorkPreEvents`;
[design §8](../../design/8-barrier-step-b1c-and-crash-safety/index.md) "Turn path".

**Facts.**
- The port has one implementer. `git grep -n -E "func \([^)]*\) (AgeFileChains|RecordFileWrite|RecordFileCommit|GetFileVersion)\(" -- '*.go'`
  → only `internal/memops/fileadapter/fileadapter.go` (708, 731, 834, 918). Wrappers that embed the
  interface inherit the new method: `git grep -n -E "memops\.MemoryOps$" -- '*.go'` lists embedded
  fields at `internal/chat/chat_test.go:487`, `internal/turn/chain_taskclass_test.go:23` and
  `internal/turn/pipeline_test.go:32`. None overrides a file method (`grep -n "^func (" internal/turn/chain_taskclass_test.go internal/turn/pipeline_test.go`
  → `EmitDelta`, `JournalTurn`, `CommitTurn`, `CreateThread`, `EngageThread`, `ArchiveThreads`,
  `MaybeDayBarrier`, `ConsultStream`). Conformance asserts:
  `internal/memops/fileadapter/fileadapter.go:155` and `fileadapter_test.go:94`.
- Owner excerpt sites. `git grep -n 'renderTurnExcerpt' -- '*.go'` → calls at
  `internal/turn/engage.go:661` (`renderTurnExcerpt(mergeTurn, now, turnAnchors, userInput, responseBody)`,
  inside `if owner`) and `engage.go:768` (`renderTurnExcerpt(1, now, …)`, inside `if owner`); the
  definition at `internal/turn/excerpt.go:32`. Comment-only mentions are in
  `internal/prompt/excerptgenre_probe_test.go`, `internal/prompt/history_probe_test.go` and
  `internal/store/thread_io.go`. `git grep -l 'renderTurnExcerpt' -- '*.go'` → those five files.
- The owner's excerpt number is `mergeTurn` (`engage.go:585–587`: `mergeTurn := rec.TurnCount`,
  `+1` for the owner) or `1` for a new owner thread. `now` is
  `clock.Timeline().Format(time.RFC3339)` (`engage.go:146`). These are the `turn` and `at` of
  `RecordFileEvents` (design §5.1). How they reach the call is the coder's choice.
- The application site. `engage.go:309–311`:
  `if len(state.fileEdits) > 0 { applyFileEdits(ctx, state, engaged[0]) }`. `engaged[0]` is the
  owner (`engage.go:202–204` comment). `applyFileEdits` (`engage.go:402`) is the only reference to
  its name: `git grep -l 'applyFileEdits' -- '*.go'` → `internal/turn/engage.go`.
- Buffering. `internal/turn/chain.go:149` `bufferFileEdit` maps both `SourceFSRead` and
  `SourceFSWrite` to `fileEditWrite`, so read and write are not distinguished today.
  `git grep -l -e fileEditWrite -e fileEditCommit -- '*.go'` → `internal/turn/chain.go`,
  `internal/turn/engage.go`. An empty path already logs `fs.edit-no-path reason=missing-meta-path`.
- `turn` gains a direct `dedup` import for the identifier lines. `go list -deps ./internal/turn | grep internal/dedup`
  → `personant/internal/dedup`, so the build already has it (through `store`).
- Turn scope. `func (a *FileAdapter) touchTurnScope(rels ...string)` is at
  `internal/memops/fileadapter/fileadapter.go:105`; `RecordFileWrite` calls it after save (`:720`).
- Tests that read the old store after a turn
  (`git grep -l 'LoadThreadFiles' -- 'internal/turn/*_test.go' 'internal/scenarios/*_test.go'`):
  - `internal/turn/turn_test.go`: `TestRunBuffersFileEditsIntoThreadStore`,
    `TestRunFileCommitUntrackedIsNonFatal`, `TestRunFileEditWithoutTopicTagOwnerDefaultsToNewThread`,
    `TestRunFileEditWithTopicTagSucceeds`;
  - `internal/turn/emptyresponse_test.go`: `TestRunEmptyResponsePersistentBindingOwnerDefault`,
    `TestRunEmptyThenFetchThenTagRepromptAllCausesCapped`;
  - `internal/turn/tagrecovery_test.go`: `TestRunTagRepromptRecoversBinding`,
    `TestRunOwnerDefaultBindsMostRecentlyEngaged`, `TestRunRepromptCapOnePerCauseBothCausesOneTurn`,
    `TestRunOwnerDefaultSurvivesRestartRecency`, `TestRunFetchFirstThenTagReprompt`;
  - `internal/scenarios/scenarios_test.go`: `TestScenario_TagOmissionRepromptRecovers`,
    `TestScenario_TagOmissionDoubleMissOwnerDefault`.

  They switch to the new store's loader. `TestRunFileCommitUntrackedIsNonFatal` becomes a test
  that a commit of a never-read path writes its identifier line, adds no store entry, and does
  not fail the turn.
- `VerifyDedupConsistency` duplicates the new verify check. `git grep -l 'VerifyDedupConsistency' -- '*.go'`
  → `internal/scenarios/invariants.go` (`heavyDefaultInvariants`, 83), `restart.go` (115) and
  `invariants_test.go` (320, 341). `VerifySpineIntegrity`, which runs `verify.Verify`, is in the
  same two lists (`invariants.go` `heavyDefaultInvariants`, `restart.go:111`). So it is deleted,
  not rewritten, along with its two tests.
- `internal/scenarios/sim/workload.go:3331–3333` has the comment "so the inflated bytes ride into
  THIS turn's assembled request". That is false: `preEvents` reach only `onContextDelta`, and the
  request is built from the system prompt, history tail, user input and tool messages
  (`grep -n "preEvents" internal/turn/turn.go` → 695, 696, 709, 747, 890, 891; the uses within
  `RunWithInfo`'s body are 890–891 only). The comment is corrected to say
  the filler becomes a tracked path and renders from the next turn's composition.

**Work.**
1. The port method and `memops.FileEvent` (from P2). The adapter runs the boundary checks of
   invariant 10, calls `store.ApplyFileEvents`, and passes every path written to
   `touchTurnScope`. Violations are logged through the event log and return an error.
2. Turn:
   - identifier lines from `state.fileEdits` (every read, write and commit, in buffer order),
     rendered by `dedup`, go into the owner excerpt only, before `**user:**`;
   - one `RecordFileEvents` call carries the owner's read/write events (commits never reach the
     store); an error is logged as `fs.write-error`, as `applyFileEdits` does today, and the turn
     continues;
   - `fs.commit-untracked` is no longer emitted.
   - The `AgeFileChains` loop (`engage.go:313–324`) stays until P6. With no `files.json` written it
     is a no-op on a new home.
3. The test migrations and the `VerifyDedupConsistency` deletion listed above, and the comment fix.
4. New tests:
   - `TestRenderTurnExcerpt…`: the lines sit between the header and `**user:**`, and an excerpt
     with no events is byte-identical to today's;
   - a turn with a read, a write and a commit of one path writes three lines, in order, in the
     owner excerpt only, and one store entry whose `turn` is the owner's excerpt number
     (`TestRunBuffersFileEditsIntoThreadStore`, extended);
   - a path with `\n` is logged as `fs.edit-no-path` with detail
     `source=<source> reason=newline-in-path` (the empty-path case's detail form, `chain.go:157–158`)
     and buffered nowhere;
   - `TestRecordFileEvents…`: each invariant-10 violation is rejected and logged; written paths
     reach the turn scope.

**Contract.** The text to land is in
[design §9](../../design/9-proposed-spec-text-replaces-the-leaves-under-project-docs-spec-3-core-algorithms-3-9-working-set-content-dedup.md)
(SPEC) and [design §10](../../design/10-architecture-changes-same-change.md) (ARCHITECTURE). Links
are relative paths from each leaf. Any link to SPEC §3.9.1 targets the leaf's current file name,
`3-core-algorithms/3.9-working-set-content-dedup/3.9.1-persistent-storage-thread-file-body-log.md`,
so `just check-links` passes; P5 renames the leaf and fixes the links.
- SPEC §2.3 (`2-data-model/2.3-thread-file-format-threads-thr-id.md`): the two edits in design §9.
- SPEC §3.0.2 (`3.0.2-the-hook-chain.md`) step 3: the text in design §9.
- ARCHITECTURE `storage-and-record-formats/thread-directory-threads-thr-id.md`: the bullet
  replacement, the excerpt example and the "Tracked-file store" subsection, as written in design
  §10. Its "Retention" bullet describes B1c, which lands in P5 within the same checkpoint.
- ARCHITECTURE `storage-and-record-formats/home-layout.md`, `key-abstractions.md`, and
  `the-3-0-hook-chain-load-bearing-primitive/index.md` step 3: as written in design §10.
- ARCHITECTURE `testing-as-the-lab-bench/event-log-format.md`: the `fs` row, as written in design
  §10.

**Done-line.** `just build`;
`just test-run ./internal/turn 'TestRunBuffersFileEdits|TestRunFileCommit|TestRunFileEdit|TestRunEmpty|TestRunTagReprompt|TestRunOwnerDefault|TestRunRepromptCap|TestRunFetchFirst|TestRunExistingThreadAppendsExcerpt|TestRenderTurnExcerpt'`;
`just test-run ./internal/memops/fileadapter 'TestRecordFileEvents'`;
`just test-run ./internal/scenarios 'TestScenario_TagOmission|TestVerify'`; `just check-links`.

---

