## 12. Acceptance criteria

Each criterion names the observation that shows it.

1. **In-place rendering.** In a composed working set, each (project, path) under the
   literal-share threshold renders as a literal at most once, and each content id at most once.
   The literal sits inside the excerpt of the file's most recent read/write position; if the walk
   does not reach that position, or the literal does not fit there, no literal of that file
   renders. The N most recent changes the context holds render as diffs at their turns. Every other
   file event renders only as its identifier line.
   *Observed by:* the compose tests (`TestComposeWorkingSet…`) and the expansion-report check run
   on every mock sim step (`TestFileExpansionCheck…`, and the `dedup-expansions:` summary line).
2. **No stored or rendered structure grows with edit count.** After any number of edits to one path
   followed by one barrier, the thread holds exactly one blob for it, at most N diffs, and one index
   line. *Observed by:* the B1c barrier test (`TestBarrier_Prune…`).
3. **Retention never runs git against the user's workspace.** No code path under `dedup`, the
   store's tracked-file code, adapter compose or barrier step B1c executes git against the user's
   workspace. A mock run whose workspace is never committed ends with the most-edited thread's
   store equal to its current blobs plus at most N diffs per kept path, plus the index.
   *Observed by:* the two criterion-3 greps in the removal package's done-line, and the end-of-run
   store listing of the measurement ([§13](13-measurement-the-a11a-method.md)).
4. **Per-turn cost is flat** (SPEC §9.1). The turn-normalized CPU of `ComposeWorkingSet`,
   turn-close `RecordFileEvents` and `loadActiveThreadData` has a per-turn ratio of at most 1.25
   from the 9d mock profile to the 20d one, and from the 9d to the 31d one. No `dedup.(*Chain)` or
   `loadTrackedFiles` frame appears. *Observed by:* [§13](13-measurement-the-a11a-method.md).
5. **Excerpts are never rewritten.** Excerpt and folio bytes are never modified by the dedup
   mechanism. B1b settle behavior is unchanged. *Observed by:* a compose test and the B1c barrier
   test, each asserting `turns/` and `folio.jsonl` byte-identical before and after; and the
   existing B1b tests, unchanged and passing.
6. **Crash safety.** A torn turn leaves excerpt and store consistent with daily HEAD. A crash at
   either B1c crashpoint converges on completion, and no file named by an index is ever deleted.
   *Observed by:* the W-TURN crash case with file events (`TestCrashMatrix_WTurn…`) and the B1c
   points in `TestBarrier_CrashWalk`.
7. **Migration.** A format-2 home opens under the new binary and is restamped to 3. After its first
   barrier it holds no `files.json`. *Observed by:* `TestHomeFormat_Migrate2To3RemovesLegacySidecar`.
8. **Verify.** `personant verify` fails a thread whose index is unreadable, unsorted or has a
   duplicate path, names a missing blob or diff file, or names a blob whose bytes do not hash to its
   name. Each finding names the offending file. *Observed by:* the verify tests of the store package
   step.
9. **Removal.** `git grep -n -e LiveWindow -e "dedup.Chain" -e AgeFileChains -e GetFileVersion -e
   BlobReachable -e ShowFileAtCommit -e chain-age -e "tracked files ===" -- '*.go'` returns nothing.
10. **Over-share files never expand.** A file whose current content is at or above
    `dedup.literal-share-threshold` × the share renders no literal and no diff anywhere in the
    composed working set, and each of its positions shows its identifier line. A file just under
    the threshold, in the same context, does render its literal. *Observed by:* the compose test
    for the threshold boundary and the expansion-report check.

---
