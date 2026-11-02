# Review burndown — content dedup (post-sprint review of `77084ce..d83e6e5`)

Fix packages for the post-sprint review findings of the content-dedup plan. Line numbers are at
`d83e6e5`. Where a file has moved since, re-run the command quoted with the fact; the command, not
the number, is the fact.

**Precedence.** SPEC outranks the design and the plan. The design
([design/entry-point.md](design/entry-point.md)) is normative on mechanism; its §6 and §8 state the
rules R1 and R2 implement.

**Gates.** Every package touches Go code, so packages run one at a time in the shared working tree,
with no git worktree. Edit gate: `just build` plus each package's `just test-run` lines. A test
named in a done-line is created by that package if it does not exist. Every new test's name starts
with a prefix from its package's done-line regexp. **Coders never run `just test`.** The closing
checkpoint is `just test` plus one commit, run by the dispatching session after R6.

**Every dispatch reads first:** `THESIS.md`, `SPEC.md`, `ARCHITECTURE.md`, `CONVENTIONS.md` (via
`PROJECT_DOCS/`), and its own package section.

**Landing text.** Text to land in a contract leaf is given in fenced `markdown` blocks, links
already relative to that leaf.

## Status

| # | Package | Findings | Rating | Depends on | State |
|---|---|---|---|---|---|
| R1 | Composition walk and plan | W1+C1, W4, C4, C5, N9+C6a, compose-test `diffRecord` | expert | — | in progress |
| R2 | Tracked store, verify and barrier repair | W5+C3, N4, C6e, N5+C6d, crash case before the index write, B1c order wording | expert | — | not started |
| R3 | File-event input validation | N6+C2, N3+C6c, C6b | mechanical | — | not started |
| R4 | Harness: large-input rung and expansion check | W6, W7 | expert | R1 | not started |
| R5 | SPEC and module-map text | W2, W3, N1, N2, N10, N8 (module map), the R1 note rule | mechanical | R1, R2 | not started |
| R6 | Cleanup | leftovers, `TestApplyDiffMismatch`, N8 (package doc) | mechanical | — | not started |
| — | Checkpoint: `just test`, one commit (dispatching session) | | | R1–R6 | not started |
| — | Optional live check of R4 (dispatching session, needs the user's go) | | | R4 | not started |

Order: R1, R2, R3, R4, R5, R6.

---

## R1 — Composition walk and plan (expert)

**Scope.**
- `internal/dedup/plan.go` (`Plan`, `recentDiffs`, `literalBeats`) and
  `internal/dedup/plan_test.go`.
- `internal/memops/fileadapter/workset_compose.go` (`loadActiveThreadData`,
  `walkThreadExcerpts`, `readExpansion`) and `internal/memops/fileadapter/workset_compose_test.go`
  (`testLiteralMax`, `diffRecord`, new cases).
- `internal/workset/workset.go` and `internal/workset/workset_test.go`. Two new exports, which R4
  also uses:
  - `const ThreadSeparator = "\n---\n"`, which `renderLayerB` joins threads with and the walk
    charges;
  - `func ExpansionEndMarker(x Expansion) string`, the closing line of an expansion block that
    `renderExpansion` writes (today `[end file content #<id12>]` or
    `[end file diff #<from12> -> #<to12>]`), with `renderExpansion` built on it. It depends only on
    `x.Kind`, `x.ID`, `x.From` and `x.To`.
- `PROJECT_DOCS/ARCHITECTURE/the-layered-working-set/index.md`: the "File content" bullet (lines
  19–28).

**Findings and rulings.** These implement [design §6](design/6-composition.md) steps 2–3.
1. **W1 + C1. A size note overruns the share and the backstop cuts the newest excerpt.**
   - Rule: an excerpt is admitted only if its text, the separator before it, and a size note for
     each of its planned expansions fit the remaining share.
   - The newest excerpt is always admitted. Its notes are charged only while they fit; a note that
     does not fit is not rendered, and its identifier line names the content (R5 edit 2 states
     this in SPEC §3.9.2).
   - After admission, each note in render order becomes its content if the content's extra length
     over the note fits.
   - With this accounting, a thread's rendering exceeds its share only when the newest excerpt's
     text alone does.
   - The report lists only expansions rendered as content.
2. **Separator.** The walk charges `len(workset.ThreadSeparator)` for every thread, including the
   last, against the share. So k full threads plus their separators fit `LayerB` whenever
   k × share ≤ `LayerB`. The case where `PerThreadBudget`'s 4096 floor makes k × share exceed
   `LayerB` stays governed by SPEC §3.1 truncation.
3. **W4. `recentDiffs` filters before it windows, so it reaches back past newer changes.** Rule:
   take the N most recent changes per (project, path), in the existing order. Then drop those not
   kept or at or above `DiffLiteralThreshold` × the current size; dropped changes are not replaced
   by older ones. This is SPEC §3.9.2: "The N most recent changes … A diff at or above … does not
   render".
4. **C4. The shared-id literal winner is fixed before the walk, so if its position is not rendered
   no path gets the literal.**
   - Rule: `Plan` emits a literal candidate for every path under the gate, with no shared-id
     suppression.
   - The walk renders a literal only if its id has not yet rendered as a literal in this
     composition. Walk order decides: Layer B order, newest excerpt first, path order within an
     excerpt.
   - A losing candidate stays a note and keeps its diffs. `literalBeats` goes.
5. **C5. `LiteralMax` rounds down, so a file just under the threshold is excluded.** Rule:
   `LiteralMax = int(math.Ceil(dedup.LiteralShareThreshold * float64(perThread)))`. Then
   `size >= LiteralMax` is exactly "size ≥ threshold × share".
6. **N9 + C6a. A failed expansion read is charged in full and renders nothing.** Rule: a
   `readExpansion` failure renders the expansion as its size note, with the charge dropping back
   to the note's. It logs `dedup.error` and adds no report record.

**Facts.**
- W1, confirmed by probe: `.claude-temp/review-probes/run1.txt` records
  `TestZZProbeSizeNoteOverflowCutsNewest` — "CONFIRMED: newest excerpt tail cut; older excerpt +
  size note kept".
  - In `walkThreadExcerpts` (`workset_compose.go:291–329`), the Omitted branch runs
    `remaining -= workset.ExpansionLen(x)` with no fit check (lines 305–309).
  - `renderLayerB` truncates each thread with `truncateToBudget(body, perThread, …)`
    (`internal/workset/workset.go:333`). The body renders oldest first, so the cut lands on the
    newest excerpt.
- Separator: `grep -n '"\\n---\\n"' internal/workset/workset.go` → `338: joined := strings.Join(rendered, "\n---\n")`,
  followed by `truncateToBudget(joined, budget.LayerB, "Layer B")`. At the default budget `LayerB`
  is 233,750 and `233750 mod 3 = 2`, so three full threads overrun `LayerB` by 8 bytes, and the
  layer cut lands on the last thread's newest excerpt.
- W4: `recentDiffs` (`internal/dedup/plan.go:178–199`) filters to `c.Diff && float64(c.Size) <
  DiffLiteralThreshold*float64(currentSize)` before `qual[max(0, len(qual)-LiveDiffWindow):]`.
  Probe: `.claude-temp/review-dedup-probe/probe_test.go` `TestProbeDiffsReachPastWindow`.
- C4: `Plan` settles `literalOf` (plan.go:116–125) and adds a literal only
  `if literalOf[cur.entry.ID] == cur` (plan.go:144), before any walk runs.
- C5: `workset_compose.go:251` → `LiteralMax: int(dedup.LiteralShareThreshold * float64(perThread))`;
  `workset_compose_test.go:154` → `var testLiteralMax = int(dedup.LiteralShareThreshold * testShare)`
  repeats the formula.
- N9: `workset_compose.go:311–316` subtracts `n` before `readExpansion`; on error it logs and
  `continue`s.
- `diffRecord` (`workset_compose_test.go:543–551`) computes `Size` with `dedup.Diff`, the same call
  the store makes.
- References:
  - `git grep -l 'dedup.Plan(' -- '*.go'` → `internal/memops/fileadapter/workset_compose.go`;
  - `git grep -l 'recentDiffs' -- '*.go'` → `internal/dedup/plan.go`;
  - `git grep -l 'renderExpansion' -- '*.go'` → `internal/workset/workset.go`,
    `internal/workset/workset_test.go`.

  `Plan`'s signature does not change.

**Work.**
1. Rulings 1–6 and the two `workset` exports.
2. Tests:
   - `testLiteralMax` becomes an independent constant at a share whose product is fractional, for
     example share 4097 → `const testLiteralMax = 1025`. Add a case where a 1024-byte file renders
     its literal at that share.
   - `diffRecord` takes the expected size from the stored diff file (`os.Stat` of
     `store.TrackedDiffPath`) or from a literal constant, not from `dedup.Diff`.
   - New compose tests:
     - notes that would overrun the share are not admitted, and the newest excerpt's text is never
       cut while it fits;
     - three full threads stay within `LayerB`;
     - a shared id whose earlier-planned position is not walked renders at the other path;
     - a read failure renders the note and adds no report record.
   - `plan_test.go`: the "N most recent qualifying diffs" case becomes "N most recent changes, the
     gated ones dropped"; the shared-id cases expect both candidates.
   - `workset_test.go`: `ExpansionEndMarker` equals the last line of the rendered block, for both
     kinds.
3. Replace the ARCHITECTURE "File content" bullet with:

   ```markdown
   - **File content.** `ComposeWorkingSet` loads each Layer B thread's `files.jsonl` and runs one
     pure `dedup.Plan` across them. The plan gives each path under the size gate a literal
     candidate at its most recent position. It gives diffs at the path's N most recent changes,
     minus those not kept or at or above `dedup.diff-literal-threshold` of the current size. A
     path whose current size is at or above `dedup.literal-share-threshold` × the per-thread share
     gets neither. Threads are walked in Layer B order and their excerpts newest-first. An excerpt
     is admitted only if its text and a size note per planned expansion fit; the newest is always
     admitted. Notes then become content while the share allows. A content id renders as a literal
     once: the first taken wins. Only content that renders is read. `workset` renders it inside
     its excerpt, and `ComposeWorkingSet` returns a structured report of what rendered.
   ```

**Done-line.** `just build`;
`just test-run ./internal/dedup 'TestPlan'`;
`just test-run ./internal/memops/fileadapter 'TestComposeWorkingSet'`;
`just test-run ./internal/workset '.'`;
`just test-run ./internal/scenarios 'TestVerifyNoBudgetOverflow|TestFileExpansionCheck'`;
`just check-links`.

---

## R2 — Tracked store, verify and barrier repair (expert)

**Scope.**
- `internal/store/trackedfiles.go` (`ApplyFileEvents`, its `put` closure, a new crashpoint) and
  `internal/store/trackedfiles_test.go`.
- `internal/verify/verify.go` (`checkTrackedFiles`) and `internal/verify/verify_test.go`.
- `internal/memops/fileadapter/fileadapter_barrier.go`:
  - `spineQuarantineRepair`;
  - the `pruneTrackedFiles` comment at 267–270;
  - and `internal/memops/fileadapter/fileadapter_barrier_test.go`.
- `internal/scenarios/crash_matrix_test.go`: the `crashScenarioCoverage` entry, and a row in
  `TestCrashMatrix_WTurnFileEvents`.
- `PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md`
  (lines 56–64).

**Findings and rulings.** These implement
[design §8](design/8-barrier-step-b1c-and-crash-safety/index.md).
1. **W5 + C3. A blob or diff written since the last day-commit, then missing or corrupt, fails
   B2's verify on every open.** The repair restores the single file from primary HEAD, where it
   does not exist.
   - Rule: `spineQuarantineRepair` treats a thread's tracked store as one unit. Any finding whose
     path is `threads/<id>/files.jsonl`, or lies under `threads/<id>/files/`, quarantines both
     `files.jsonl` and `files/`, byte-exact, once per thread.
   - It restores `files.jsonl` with `autogit.Checkout` and `files/` with `autogit.CheckoutTree`
     from primary HEAD. When HEAD has neither, both stay removed.
   - Other findings keep today's per-file handling.
2. **N4. An unreadable or corrupt previous blob makes `ApplyFileEvents` abort the turn's events,
   and every later turn on that thread fails too.** Rule: record the change with `Diff:false` and
   `Size:0`, and continue with this event and the turn's others.
3. **C6e. `put` skips an existing file without checking it.** Rule: an existing blob whose bytes
   do not hash to its name, or an existing diff whose bytes differ from the diff being written, is
   rewritten with `WriteFileAtomic` and listed as written.
4. **N5 + C6d. Verify does not compare sizes.** Rule: add two findings, `len(blob) != entry.Size`,
   and a kept diff whose file size `!= change.Size`. The finding `Path` is the index,
   `store.TrackedIndexRel(id)`: the blob is hash-checked, so a mismatch blames the record.
5. **Crash between the store's file writes and its index write.**
   - Rule: register `fs.applyEvents.preIndex` in `ApplyFileEvents`, after the last blob or diff
     write and before `writeTrackedIndex`.
   - Map it in `crashScenarioCoverage` to `"scenarios.TestCrashMatrix_WTurnFileEvents"`.
   - Add the row `{"applyEventsPreIndex", "fs.applyEvents.preIndex", 0, false}` to that test's
     `cases`.
6. **Order wording.** `ListThreads` returns spine order, which is string order by id (`thr_10`
   before `thr_2`), not numeric. Correct the comment and the ARCHITECTURE sentence; order is
   immaterial because each thread is pruned independently.

**Facts.**
- W5, traced:
  - verify reports a missing blob at `store.TrackedBlobRel(rec.ID, e.ID)` (`verify.go:286–291`);
  - `spineQuarantineRepair` (`fileadapter_barrier.go:501–547`, called at 447 and 468) quarantines
    each finding's path; `quarantineAndRemove` returns `false, nil` for an absent path (lines
    1033–1040);
  - it restores with `autogit.Checkout(…, head, rel, …)`, whose `commit.File(filePath)` fails for
    a path absent from HEAD (`internal/autogit/autogit.go:386–388`);
  - the index still names the blob, so the re-verify fails at line 545, "spine still broken after
    quarantine-and-proceed", and the open is refused each time.
  - `autogit.CheckoutTree(ctx, paths, which, commitHash, dirPath, preFlags, postFlags)` exists
    (`internal/autogit/autogit.go:437`).
- N4: `trackedfiles.go:219–222`:
  `old, err := ReadTrackedBlob(…); if err != nil { return written, fmt.Errorf(…) }`.
- C6e: `put` (trackedfiles.go:192–203) returns `nil` whenever `os.Stat(path)` succeeds.
- N5: `checkTrackedFiles` (`verify.go:271–305`) reads each blob through `ReadTrackedBlob`, which
  checks the hash only (trackedfiles.go:153–165), and checks diffs with `os.Stat` only (line 298).
- Order: the `pruneTrackedFiles` comment (`fileadapter_barrier.go:267–270`) says "in ListThreads
  order, which is numeric id order".
  - `ListThreads` returns `store.ReadSpine` order (`fileadapter.go:303–312`).
  - `WriteSpine` sorts with `WriteJSONL(…, func(r) string { return r.ID })`, whose comparator is
    `keyFn(sorted[i]) < keyFn(sorted[j])` (`internal/store/jsonl.go:76–78`), a string compare.
- Crash coverage: `TestCrashPointCoverageGate` requires every registered crashpoint in
  `crashScenarioCoverage` (B1c entries at 76–77). `TestCrashMatrix_WTurnFileEvents`
  (`crash_matrix_test.go:268`) is a table over `{name, point, hit, committed}`. It drives a turn
  carrying `fs.read` and `fs.write` deltas and asserts excerpt and store agree with daily HEAD.
- References:
  - `git grep -l 'spineQuarantineRepair' -- '*.go'` → `internal/memops/fileadapter/fileadapter_barrier.go`;
  - `git grep -l 'checkTrackedFiles' -- '*.go'` → `internal/verify/verify.go`.

  No exported signature changes.

**Work.**
1. Rulings 1–6.
2. Tests:
   - `TestBarrier_RepairRestoresTrackedStoreUnit`: a blob written today and then deleted fails
     B2. Completion restores `files.jsonl` and `files/` from HEAD, and the barrier completes.
   - `TestFileStoreUnreadablePreviousBlobRecordsNoDiff`.
   - `TestFileStoreRewritesCorruptExistingBlob`.
   - `TestTrackedFilesSizeMismatch`: blob and diff, each with `Path` `threads/thr_1/files.jsonl`.
   - The new `TestCrashMatrix_WTurnFileEvents` row.
3. In the ARCHITECTURE crash-stability leaf, replace the text from "Barrier step B1c" (line 56)
   through "all walked by `TestBarrier_CrashWalk`." (line 64) with:

   ```markdown
   Barrier step B1c (`pruneTrackedFiles`, between B1b and B2) calls `store.PruneTrackedFiles` per
   spine record, in spine order, with the record's `TurnCount`. It rewrites a thread's
   `files.jsonl` only if an entry drops, then deletes the files no kept entry names, so a crash
   leaves orphans, never a dangling index. Completion re-runs it under the same condition as B1b.
   An unreadable index is skipped (`dedup.prune-skip reason=unreadable`) and fails B2's verify
   gate. Any tracked-file finding makes completion's `spineQuarantineRepair` quarantine the
   thread's `files.jsonl` and `files/` together and restore both from primary HEAD. Crashpoints:
   the four `folio.settle.*` and `barrier.prune.postIndex` in `store`, and
   `barrier.postSettle.preDayCommit` and `barrier.postPrune.preDayCommit`, all walked by
   `TestBarrier_CrashWalk`; and `fs.applyEvents.preIndex` in `store`, between a turn's tracked-file
   writes and its index write, walked by `TestCrashMatrix_WTurnFileEvents`.
   ```

**Done-line.** `just build`;
`just test-run ./internal/store 'TestFileStore|TestPruneTrackedFiles'`;
`just test-run ./internal/verify 'TestTrackedFiles'`;
`just test-run ./internal/memops/fileadapter 'TestBarrier_|TestFolioHandBrokenQuarantinedOnCompletion'`;
`just test-run ./internal/scenarios 'TestCrashPointCoverageGate|TestCrashMatrix_WTurnFileEvents'`;
`just check-links`.

---

## R3 — File-event input validation (mechanical)

**Scope.**
- `internal/turn/chain.go` (`bufferFileEdit`, line 140) and `internal/turn/turn_test.go`.
- `internal/memops/fileadapter/fileadapter.go` (`RecordFileEvents` at 707, `checkFileEvents` at
  726) and `internal/memops/fileadapter/recordfileevents_test.go`.

**Findings and rules.**
1. **N6 + C2. A path with invalid UTF-8 is written as U+FFFD, which creates a duplicate index key
   and jams the thread.** Rule: reject a path where `!utf8.ValidString(path)`, next to the `\n`
   check:
   - in `bufferFileEdit`, log `fs.edit-no-path` with detail `source=<source> reason=invalid-utf8-path`;
   - in `checkFileEvents`, return reason `invalid-utf8-path`.
2. **N3 + C6c. The `fs.commit` hash is not validated, so an empty or non-hex hash renders
   `[file commit @]`, and a `\n` in it breaks the one-line form.** Rule: in `bufferFileEdit`, an
   `fs.commit` whose `Meta["hash"]` is not 7 to 64 hex digits (`[0-9a-fA-F]`) is logged as
   `fs.edit-no-path` with detail `source=fs.commit reason=bad-commit-hash` and is not buffered.
3. **C6b. Rejected input logs `fs.write-error` twice, once in the adapter and once in the turn.**
   Rule: `RecordFileEvents` no longer logs. It returns an error whose message contains the reason,
   for example `fileadapter: record file events thr_1: newline-in-path` (the existing form at
   `fileadapter.go:714`). `recordFileEvents` (`internal/turn/engage.go:384`, the log at 401–403)
   logs it once.

**Facts.**
- N6, confirmed by probe: `.claude-temp/review-probes/run1.txt` records
  `TestZZProbeInvalidUTF8Path` — "CONFIRMED: index unreadable after two events on one path: …
  duplicate path "a�.go"". JSON encoding replaces invalid UTF-8 with U+FFFD, so two raw paths
  become one key.
- `bufferFileEdit` (`chain.go:140–172`) checks only `path == ""` and `strings.Contains(path, "\n")`,
  and copies `delta.Meta["hash"]` unchecked.
- `dedup.IdentifierLine` (`internal/dedup/identifier.go`) renders `id[:min(n, len(id))]`.
- C6b: `fileadapter.go:713` logs `fs.write-error … reason=`; `engage.go:401–403` logs
  `fs.write-error … err=` for the same failure.
- `TestRecordFileEventsRejectsInvalidInput` (`recordfileevents_test.go:16–54`) asserts the
  adapter's log line `fs.write-error thr=<thread> reason=<reason>` (line 45). That assertion
  changes with rule 3.
- An `Ops` wrapper that embeds `memops.MemoryOps` and overrides one method is an existing pattern:
  `failCommitOps` in `internal/chat/chat_test.go:486–492`.
- References:
  - `git grep -l 'checkFileEvents' -- '*.go'` → `internal/memops/fileadapter/fileadapter.go`;
  - `git grep -l 'bufferFileEdit' -- '*.go'` → `internal/turn/chain.go`.

  No signature changes.

**Tests.**
- `TestRecordFileEventsRejectsInvalidInput`:
  - gains the case `{"invalid UTF-8 path", "thr_1", 2, recordAt, read("a\xff.go"), "invalid-utf8-path"}`;
  - each case asserts that the returned error's message contains the reason, and that the event
    log has no `fs.write-error` line;
  - the "no index written" assertion stays.
- `TestRunFileEditStoreErrorLoggedOnce` (`internal/turn/turn_test.go`):
  - a turn with a file edit runs against an `Ops` wrapper that embeds the real adapter and whose
    `RecordFileEvents` returns `errors.New("record file events thr_1: injected")`;
  - the turn completes, and the event log holds exactly one `fs.write-error` line, carrying
    `err=` and `injected`.
- `TestRunFileEditInvalidUTF8PathIsRejected`.
- `TestRunFileCommitBadHashIsRejected`: an empty hash, a non-hex hash, and a hash containing `\n`
  each write no identifier line and log one `fs.edit-no-path … reason=bad-commit-hash`.

**Done-line.** `just build`;
`just test-run ./internal/turn 'TestRunFileEdit|TestRunFileCommit|TestRunBuffersFileEdits'`;
`just test-run ./internal/memops/fileadapter 'TestRecordFileEvents'`.

---

## R4 — Harness: large-input rung and expansion check (expert)

**Precondition.** R1 has landed: `workset.ExpansionEndMarker` and `workset.ThreadSeparator` exist.

**Scope.**
- `internal/scenarios/sim/workload.go`:
  - `largeToolResultDelta` at 3371;
  - the injection comment at 3331–3334;
  - the `LargeInputBytes` comment at 247–270;
  - `defaultLargeInputBytes` at 3399–3405;
  - a new sizing function.
- `internal/scenarios/sim/sim_rung_test.go` (the constant and comment at 19–32, the knob at
  182–185), `internal/scenarios/sim/sim_live_test.go:201`, and a new unit test in the sim package.
- `internal/scenarios/invariants.go` (`VerifyNoBudgetOverflow` at 514, `checkFileExpansions` at
  572) and `internal/scenarios/invariants_test.go`.

**Findings and rulings.**
1. **W6. The large-input rung injects a 48 KiB `fs.read`, over the 19,479-byte literal limit, so
   it adds nothing and the rung passes vacuously.** The filler stays `fs.read`: pre-prompt deltas
   never reach the request (`internal/turn/turn.go:896–898`, `994–997`). Rules:
   - A new function, `largeInputBytesFor(b memops.Budget) int`, returns
     `⌈dedup.LiteralShareThreshold × workset.PerThreadBudget(b.LayerB, b.BTopK)⌉ − 1`.
   - The rung sets `cfg.LargeInputBytes` from it, using the budget the rung's `turn.State` is built
     with, or `memops.DefaultBudget()` where the rung leaves it default.
   - `largeInputRungBytes` and `defaultLargeInputBytes` are removed. A zero `LargeInputBytes` with
     `LargeInputEveryN > 0` falls back to `largeInputBytesFor(memops.DefaultBudget())`.
   - The comments state the purpose: tracked-file literals just under the share threshold enter
     the composed Layer B from the next turn, so the live X4 rung measures real provider tokens of
     file content against the byte-derived budget.
2. **W7. `checkFileExpansions` validates the report against itself, not the rendered layer.**
   Rules:
   - The check moves into its own function, `verifyFileExpansions(h *Harness, ws memops.WorksetLayers, threads []dedup.PlanThread) error`,
     called by `VerifyNoBudgetOverflow` on the compose it already runs, so a step still composes
     once. The metrics move with it.
   - `threads` is the Layer B threads in Layer B order, each built as `dedup.PlanThread{ID, Project:
     store.LoadThreadFrontmatter(…).Project, Entries: store.ReadTrackedIndex(…)}`.
   - `checkFileExpansions(r memops.FileExpansionReport, layerB string, threads []dedup.PlanThread) error`
     adds three checks to the existing ones:
     - each reported expansion's end marker appears in `layerB`. The marker is
       `workset.ExpansionEndMarker(workset.Expansion{Kind: workset.ExpansionKind(x.Kind), ID: x.ID, From: x.From, To: x.To})`;
       `memops.FileExpansion.Kind` holds the same strings, `literal` and `diff`.
     - each literal record's turn equals the turn of its path's index entry, the one with the
       latest `at` across the Layer B threads (on equal `at`, the earlier thread);
     - each (project, path) has at most `dedup.LiveDiffWindow` diff records.

**Facts.**
- W6:
  - `largeToolResultDelta` (`workload.go:3371–3380`) returns `Source: memops.SourceFSRead`.
  - `largeInputRungBytes = 48 * 1024` (`sim_rung_test.go:32`) is 49,152 bytes; the default
    literal limit is 19,479.
  - `git grep -n "defaultLargeInputBytes\|LargeInputBytes\|largeInputRungBytes" -- '*.go'` →
    `sim_live_test.go:201`, `sim_rung_test.go:19,22,32,184`, `workload.go:247,257,270,3314,3336,3373,3399,3400,3405`.
  - The knob is on only when a live chat client is installed (`sim_rung_test.go:182–185`), so mock
    runs are unaffected and no mock run observes W6.
- W7: `checkFileExpansions` (`invariants.go:572–603`) reads only `r.Expansions` and
  `r.LiteralMax`.
- References:
  - `git grep -l 'checkFileExpansions' -- '*.go'` → `internal/scenarios/invariants.go`,
    `internal/scenarios/invariants_test.go`;
  - `git grep -l 'largeToolResultDelta' -- '*.go'` → `internal/scenarios/sim/workload.go`.

**Tests.**
- `TestFileExpansionCheck_FailsOnMissingEndMarker`.
- `TestFileExpansionCheck_FailsOnLiteralNotAtLatest`.
- `TestFileExpansionCheck_FailsOnTooManyDiffs`.
- The existing `TestFileExpansionCheck_*` cases, updated to the new arguments.
- `TestLargeInputBytesFor` (sim package): at `memops.DefaultBudget()` it returns 19,478. For a
  budget whose per-thread share is 4,097 it returns 1,024. The rung's configured
  `LargeInputBytes` is below its budget's literal limit.

**Done-line.** `just build`;
`just test-run ./internal/scenarios 'TestFileExpansionCheck|TestVerifyNoBudgetOverflow'`;
`just test-run ./internal/scenarios/sim 'TestLargeInputBytesFor'`.

**Dispatching session, after R4.**
- Regression check on R1's walk and R4's check under mock: `just DURATION=1d sim` passes, and its
  output carries `dedup-expansions: literal=<n> diff=<n>` with `literal` above zero.
- Optional live observation of W6, which **needs the user's go**: it runs live inference against
  the reaper.local providers configured in `test/rundata/test.providers.toml`. The command is
  `just sim LIVE_INFERENCE=true DURATION=1d`; its expansion count includes the filler paths.

---

## R5 — SPEC and module-map text (mechanical)

**Precondition.** R1 and R2 have landed (the text states their behavior).

**Scope.** Five leaves under `PROJECT_DOCS/`, named below.

**Edits.**
1. **W2. SPEC §4.5.8 lacks B1c, and its "untouched by B2–B6" sentence is now false.** Leaf:
   `SPEC/4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md`.
   - After the B1b bullet (lines 163–166), add:

     ```markdown
     - **B1c** *tracked-file retention*: each live thread's tracked-file store
       ([§3.9.1](../../3-core-algorithms/3.9-working-set-content-dedup/3.9.1-persistent-storage-and-retention.md))
       drops entries whose latest read or write has left the assembly window, every file no kept
       entry names, and a legacy `files.json`. Worktree only and idempotent; the deletions land in
       B2's day-commit.
     ```

   - Lines 177–179 currently read:

     ```markdown
     Only B1/B2 mutate primary; B4/B5 are pure daily lifecycle; B3/B6 touch derived + watermark. The
     canonical worktree is untouched by B2–B6 (only *reduced* by B1's archival removals, whose bytes are
     preserved in primary, and *rearranged* without loss by B1b).
     ```

     The second sentence becomes:

     ```markdown
     The canonical worktree is untouched by B2–B6. It is only *reduced* by B1's archival removals,
     whose bytes are preserved in primary, and by B1c's retention, which deletes on purpose,
     including file versions written and superseded since the last day-commit, whose bytes never
     reach primary. B1b only *rearranges* it, without loss.
     ```

   - Lines 208–209: "and **re-runs B1b** while the day-commit has not landed, finishing any excerpt
     moves the crash interrupted." becomes "and **re-runs B1b and B1c** while the day-commit has not
     landed, finishing any excerpt moves or retention deletions the crash interrupted."
2. **W3 + N2 + the R1 note rule, SPEC §3.9.2.** SPEC places replacement at the identifier line,
   while the code renders after the excerpt text. Its Open note also contradicts its bullet. Leaf:
   `SPEC/3-core-algorithms/3.9-working-set-content-dedup/3.9.2-live-context-composition-working-window.md`.
   - The opening two sentences currently read:

     ```markdown
     The positions a composed context holds are the identifier lines of the excerpts it assembles
     ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)). Replacement happens there, in place, and only for positions the context holds:
     ```

     They become:

     ```markdown
     A file event's position is the excerpt that holds its identifier line
     ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)). Content for a position
     renders within that excerpt, after the excerpt's text; the identifier line itself stays as
     written. This happens only for positions the context holds:
     ```

   - In the N-most-recent bullet, "A diff at or above `dedup.diff-literal-threshold` of the file's
     current size does not render." becomes "A change whose diff was not kept, or is at or above
     `dedup.diff-literal-threshold` of the file's current size, does not render, and no older change
     renders in its place."
   - The sentence beginning "Content under the share threshold that does not fit" becomes:

     ```markdown
     Content under the share threshold that does not fit renders as its identifier with its size,
     except where the excerpt holding its position leaves no room even for that size note; there
     its identifier line alone names it. Either way it is content evicted for budget in the
     [§1.4](../../1-operating-model/1.4-evicted-content.md) sense, named at the version it had, and
     the store keeps those bytes ([§3.9.1](3.9.1-persistent-storage-and-retention.md)).
     ```

     The SPEC edit is the resolution, rather than changing R1's rule. Rendering the note would let
     it overrun the share, and the render-time cut would then fall on the newest excerpt, which is
     the defect R1 fixes. The identifier line already names the content at its version, which is
     what SPEC §1.4 requires.
   - In the Open note, "Such a file is named by its identifier and size; no route yet brings its
     content back." becomes "Such a file is shown only by its identifier lines; no route yet brings
     its content back."
3. **N1, SPEC §3.10.6. It lists the `fs.commit` pointer as never persisted, but the identifier line
   now persists its hash prefix and path.** Leaf:
   `SPEC/3-core-algorithms/3.10-transient-data-lifecycle/3.10.6-what-stays-transient.md`. After the
   first bullet, add:

   ```markdown
   - Carve-out: each `fs.read`, `fs.write` and `fs.commit` event leaves one identifier line in its
     turn's excerpt: operation, a content-id or commit-hash prefix, and path
     ([§3.9.1](../3.9-working-set-content-dedup/3.9.1-persistent-storage-and-retention.md)). That
     line is persisted; the event's raw bytes are not.
   ```

4. **N10, SPEC §3.9.1. Its storage bound holds for the worktree, while primary history keeps each
   day's retained blobs.** Leaf: `3.9.1-persistent-storage-and-retention.md`. After the
   paragraph beginning "**Bounds are Personant's own.**" (lines 23–26), add:

   ```markdown
   The bound is on the working tree. Primary history keeps each day-commit's tracked-file store,
   as it keeps every canonical file.
   ```

5. **N8, ARCHITECTURE `module-map.md` line 13. It says "dependency leaf", but `dedup` imports
   go-git `plumbing`.** "pure, dependency leaf" becomes "pure; depends only on go-git `plumbing`".

**Facts.** Each current sentence is quoted above from the leaf and line given, at `d83e6e5`.
- `grep -n "untouched by B2" PROJECT_DOCS/SPEC/4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md`
  → 178.
- Completion re-runs B1c in code: `grep -n "pruneTrackedFiles(ctx" internal/memops/fileadapter/fileadapter_barrier.go`
  → 188 (fresh run) and 888 (completion).

**Done-line.** `just build`; `just check-links`.

---

## R6 — Cleanup (mechanical)

**Scope and edits.**

| File | Edit |
|---|---|
| `internal/memops/fileadapter/fileadapter_test.go:507` | comment "(the turn-excerpt body, files.json)" → "(the turn excerpts, files.jsonl and files/)" |
| `internal/autogit/turn_commit_bench_test.go:135` | `add(dir+"/files.json", …)` → `add(dir+"/files.jsonl", …)` |
| `internal/autogit/turn_commit_bench_test.go:141` | `add(benchMainThread+"/files.json", …)` → `add(benchMainThread+"/files.jsonl", …)` |
| `internal/autogit/archive_recovery_test.go:62,74` | comment "thread.md + turns/<n>.md + files.json" → "thread.md + turns/<n>.md + files.jsonl + files/<id>". The fixture entry `"files.json": "{\"files\":{}}\n"` is replaced by two entries: `"files.jsonl": "{\"path\":\"a.go\",\"id\":\"ce013625030ba8dba906f756967f9e9ca394464a\",\"size\":6,\"turn\":2,\"at\":\"2026-05-18T12:00:00Z\",\"changes\":[]}\n"` and `"files/ce013625030ba8dba906f756967f9e9ca394464a": "hello\n"`, so the round trip covers the `files/` subdirectory |
| `internal/dedup/diff.go:182` | "and lets apply reconstruct a missing trailing newline byte-exactly" → "so a missing trailing newline is represented byte-exactly" |
| `internal/dedup/diff.go:5–6` (package doc) | "no dependency on other internal packages, and no knowledge of file names, the index format or rendered labels" → "no dependency on other internal packages, and no knowledge of file names, the index format or expansion delimiters; the only text it renders is an excerpt identifier line" |
| `internal/dedup/diff_test.go:175` | delete `TestApplyDiffMismatch`, which tests the test-only oracle `applyDiff` |

**Facts.**
- `git grep -n 'files\.json\b' -- '*.go' | grep -v jsonl` at `d83e6e5` lists the in-scope lines
  above.
- It also lists lines that stay because they test or name the legacy sidecar B1c removes:
  - `internal/store/relpaths.go:17`;
  - `internal/store/trackedfiles.go:270`;
  - `internal/store/trackedfiles_test.go:326,394`;
  - `internal/memops/fileadapter/fileadapter_barrier_test.go:429,443,445,483,509,510`;
  - `internal/memops/fileadapter/fileadapter_homeformat_test.go:72,80,82,112`.
- `git grep -l 'TestApplyDiffMismatch' -- '*.go'` → `internal/dedup/diff_test.go` only.

**Done-line.** `just build`; `just test-run ./internal/dedup '.'`;
`just test-run ./internal/autogit 'TestArchive|^$'`; `just test-run ./internal/memops/fileadapter '^$'`
(`'^$'` compiles the test binary and runs nothing).

---

## Not packaged

- **N7, collapsing the expansion types and enums: skipped.**
  - `memops.FileExpansion` and `dedup.Expansion` have the same fields and one converter
    (`fileExpansionRecord`). `memops.FileEventKind` and `dedup.FileEventKind` have one converter
    (`recordFileEvents` in `engage.go`).
  - Collapsing them would make `memops`, the port package, import `dedup`. `memops` depends only on
    `personant/internal/version` (`go list -deps ./internal/memops | grep personant/`), and `dedup`
    brings go-git `plumbing`. That costs more than two single-site converters save.
  - `workset.Expansion` is a rendering type (`Content`, `Omitted`) and stays.
- **Excerpt numbering density: verified, no package.**
  - An owner excerpt takes `mergeTurn = rec.TurnCount + 1`, and `rec.TurnCount = mergeTurn`
    (`internal/turn/engage.go:573–576, 639`).
  - A non-owner engagement leaves `TurnCount` unchanged, and a torn turn resets excerpt and spine
    together.
  - So excerpt numbers are dense, 1..TurnCount, and `Retain`'s `turn > TurnCount − ThreadTurnWindow`
    selects the same excerpts as the reader's last-512 window.
- **`ListThreads` order: does not hold as stated.** It is spine string order; corrected as wording
  in R2 ruling 6, since order does not affect pruning.
