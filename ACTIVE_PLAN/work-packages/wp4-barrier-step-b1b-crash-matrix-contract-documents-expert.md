### WP4 — Barrier step B1b, crash matrix, contract documents (expert)

B1b passes `memops.IsRetiredState(rec.State)` as the coldness signal; unbind happens only here,
never at engagement; B2's verify includes folios (WP3).

Scope:
- `internal/memops/fileadapter/fileadapter_barrier.go`:
  - Add `settleExcerptTiers(ctx, day)`. It lists threads (`a.ListThreads(ctx,
    memops.ThreadFilter{})`, sorted by id) and calls `store.SettleExcerptTiers(a.paths, rec.ID,
    memops.IsRetiredState(rec.State))`. It logs one `barrier.folio-skip` per skipped name (`thr=`,
    `name=`, `reason=too-large|conflict|unreadable`) and one
    `barrier.folio-settled` (`day=`, `threads=`, `bound=`, `unbound=`). An error from
    `SettleExcerptTiers` (an I/O error, or `store.ErrExcerptNotUTF8`) is returned wrapped with the
    thread id and fails the barrier.
  - Call it in `runBarrier` between `crashpoint.At(cpBarrierPostArchival)` (line 169) and
    `barrierTail` (line 171), followed by a new `cpBarrierPostSettle =
    crashpoint.Register("barrier.postSettle.preDayCommit")` in the `var` block that starts at line 56.
  - In `completePending`, call it inside `if !sealed` after `drainArchivalPressure`
    (lines 804-808).
  - Update the header comment's B-step list (lines 35-53).
- `internal/memops/fileadapter/fileadapter_barrier_test.go`:
  - `TestBarrier_CrashWalk` (line 218): keep the existing `points` loop unchanged. Add a second
    table in the same test, run as subtests named `folio/<point>`, mapping each new crashpoint to
    a fixture:

    | Crashpoint | Fixture |
    |---|---|
    | `folio.settle.midLooseWrite` | unbind |
    | `folio.settle.postLooseWrite.preFolioWrite` | unbind |
    | `folio.settle.postFolioWrite.preLooseRemove` | bind |
    | `folio.settle.midLooseRemove` | bind |
    | `barrier.postSettle.preDayCommit` | bind |

    - **Bind fixture:** `newBarrierHome(t, 3)` (three retired threads; below `ArchiveHighWater =
      200`, so B1 archives nothing and `thr_1` survives B1), then `store.AppendThreadTurn` turns 2,
      3 and 4 on `thr_1`, so a non-archived retired thread holds 4 loose excerpts. Crash the
      barrier at `bDay1`.
    - **Unbind fixture:** the bind fixture, run through one complete barrier at `bDay1` (binds all
      of `thr_1`), then `thr_1`'s spine record set to `memops.ThreadActive` with
      `store.UpdateSpineRecord`, then the barrier crashed at `bDay2`.
    - Per subtest, after a fresh adapter's `Reconcile`: every pre-crash excerpt name of `thr_1`
      reads back once with identical bytes (`store.ReadThreadExcerpts`); the layout matches the
      placement rule (bind: `folio.jsonl` holds all 4 and `turns/` is gone; unbind: all 4 loose and
      no `folio.jsonl`); `assertBarrierTerminal(t, b, day)` holds. The archival assertions of the
      first table (`assertBatchCompleted`) do not apply.
  - `TestBarrier_OvernightHandEdit` (lines 693-735) asserts `threads/thr_1/turns/2.md` is in the
    day-commit tree (line 730). `thr_1` is retired, so B1b now binds that file. Assert instead that
    the day-commit's `threads/thr_1/folio.jsonl` contains a line named `2.md` with the hand-edited
    bytes.
  - New `TestFolioHandBrokenQuarantinedOnCompletion`: bind fixture through one complete barrier at
    `bDay1`; append a non-JSON line to `threads/thr_1/folio.jsonl`; at `bDay2`, `MaybeDayBarrier`
    returns an error and the `op=barrier` marker remains; a fresh adapter's `Reconcile` succeeds,
    its `ScopedRestored` contains `threads/thr_1/folio.jsonl`, the broken bytes sit in the
    barrier quarantine directory, and `store.ReadThreadExcerpts` returns `thr_1`'s 4 excerpts.
- `internal/scenarios/crash_matrix_test.go` `crashScenarioCoverage` (line 46): add
  `"barrier.postSettle.preDayCommit": "fileadapter.TestBarrier_CrashWalk"`, and change WP1's four
  `folio.settle.*` values to `"fileadapter.TestBarrier_CrashWalk (also
  store.TestFolioSettleCrashConverges)"`.
- `internal/store/init.go`:
  - `seedReadmeMD` (line 686): describe `turns/` as warm excerpts and `folio.jsonl` as bound cold
    excerpts.
  - The `.recall-cache/` comment in the managed gitignore block (line 740): name both tiers as
    sources.
- `internal/autogit/autogit.go:548` comment: add `folio.jsonl` to the thread-directory contents.
- Contract documents, in this package's checkpoint (outcomes in SPEC, mechanism in ARCHITECTURE):
  - **SPEC §2.3**, `PROJECT_DOCS/SPEC/2-data-model/2.3-thread-file-format-threads-thr-id.md`: add
    the outcome that the tracked-file count does not grow with thread length, that excerpts outside
    a thread's warm window are held in one per-thread file and are returned byte-exact to readers,
    moving back to loose files at the next day barrier after the thread warms, and that what a
    reader gets never depends on which form holds an excerpt.
  - **SPEC §3.8.1**, `PROJECT_DOCS/SPEC/3-core-algorithms/3.8-deep-cold-archival/3.8.1-mechanism.md`
    line 3: the directory listing gains `folio.jsonl`.
  - **SPEC §4.5.8 (5)**,
    `PROJECT_DOCS/SPEC/4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md`:
    add **B1b** *excerpt tiering* between the B1 and B2 bullets (lines 159-166): worktree-only,
    byte-preserving, idempotent, landing in B2. In the sentence at lines 173-175 ("The canonical
    worktree is untouched by B2–B6 …"), add that B1b rearranges bytes without loss. In
    "Roll-forward completion" (from line 194): completion re-runs B1b while the day-commit has not
    landed.
  - **SPEC §5.5**, `PROJECT_DOCS/SPEC/5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md`
    lines 15-16: "body assembled from the warm excerpts and, when needed, the folio".
  - **ARCHITECTURE "Home layout"**,
    `PROJECT_DOCS/ARCHITECTURE/storage-and-record-formats/home-layout.md` line 9: replace "serial
    FIFO-windowed turn-excerpt files" with warm excerpts, and add `folio.jsonl` under
    `threads/thr_<id>/`.
  - **ARCHITECTURE "Thread directory"**,
    `PROJECT_DOCS/ARCHITECTURE/storage-and-record-formats/thread-directory-threads-thr-id.md`: the
    folio format, the placement rule, `HotExcerptTail`, the tier-transparent readers, and UTF-8:
    excerpt content is valid UTF-8 (it arrives through JSON decoding); bind asserts it and fails
    the barrier on a violation (`store.ErrExcerptNotUTF8`).
  - **ARCHITECTURE "Crash stability and the dual-repo substrate"**,
    `PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md`, the
    "Mechanism names." paragraph: `SettleExcerptTiers`, the fsync ordering, the convergence rule,
    and the hand-broken-folio route (invariant 7).
  - **ARCHITECTURE "Event log format"**,
    `PROJECT_DOCS/ARCHITECTURE/testing-as-the-lab-bench/event-log-format.md` line 36 (the `barrier`
    row): `folio-settled`, `folio-skip`.
  - **ARCHITECTURE "Module map"**, `PROJECT_DOCS/ARCHITECTURE/module-map.md` line 8 (the
    `internal/store/` row): add "excerpt folios".
  - README.md, if it describes the home layout (`grep -n 'turns/' README.md` returned nothing when
    this plan was written; recheck).

Facts and references:
- `git grep -n 'barrier.postArchival.preDayCommit' -- '*.go'`: the registration at
  `fileadapter_barrier.go:58`, `crash_matrix_test.go:67` and `fileadapter_barrier_test.go:229`.
  The name stays as it is, because its point still comes after archival and before the
  day-commit.
- `TestCrashPointCoverageGate` (`crash_matrix_test.go:115-126`) fails on any registered
  crashpoint missing from the map, and on any map entry that is not registered.
- `git grep -n 'turns/\|ThreadTurnsDir\|"turns"\|ThreadTurnRel' -- '*_test.go'`, outside
  `internal/store`: `internal/autogit/archive_recovery_test.go` and
  `internal/autogit/turn_commit_bench_test.go` build thread directories without running a barrier;
  the rest are metric names or comments. Only `fileadapter_barrier_test.go:698, 730`
  (`TestBarrier_OvernightHandEdit`) asserts a loose path after a barrier, and it is in scope above.
- SPEC §4.5.8 (5) INV-1: "archival and the barrier NEVER reset the worktree". B1b never resets.
- `newBarrierHome` (`fileadapter_barrier_test.go:52-75`) seeds each thread with one excerpt
  (`seedThread`, turn 1) and sets every thread `ThreadResolved`.

Done-line: `just build`; `just test-run ./internal/memops/fileadapter 'Barrier|TestFolio'`;
`just test-run ./internal/scenarios 'TestCrashPointCoverageGate'`;
`just test-run ./internal/scenarios/sim 'TestSimBarrier2Day'`; checkpoint `just test`, then
commit.

