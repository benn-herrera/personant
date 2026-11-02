## 12. Acceptance criteria

1. **In-place rendering.** In a composed working set, each (project, path) under the
   literal-share threshold renders as a literal at most once, and each content id at most once. The
   literal sits inside the excerpt of the file's most recent read/write turn that the context
   holds. The N most recent changes the context holds render as diffs at their turns. Every other
   file event renders only as its identifier line.
2. **No stored or rendered structure grows with edit count.** After any number of edits to one path
   followed by one barrier, the thread holds exactly one blob for it, at most N diffs, and one index
   line.
3. **Retention never depends on workspace git.** A mock run whose workspace is never committed ends
   with the most-edited thread's store equal to its current blobs plus at most N diffs per kept
   path, plus the index. Contrast: `files.json` was 2.75 MB at mock day 54 in
   `.claude-temp/growth/files/day54.json`. No code path under `dedup`, the store's tracked-file code,
   or adapter compose/barrier executes `git`.
4. **Per-turn cost is flat** (SPEC §9.1). Turn-normalized CPU of `ComposeWorkingSet` and of
   turn-close `RecordFileEvents` shows no rise from the 9d mock profile to the 20d or the 31d one
   beyond run-to-run noise, and no `dedup.(*Chain)` frame appears.
5. **Excerpts are never rewritten.** Excerpt and folio bytes are never modified by the dedup
   mechanism. B1b settle behavior is unchanged.
6. **Crash safety.** A torn turn leaves excerpt and store consistent with daily HEAD. A crash at
   either B1c crashpoint converges on completion, and no file named by an index is ever deleted.
7. **Migration.** A format-2 home opens under the new binary and is restamped to 3. After its first
   barrier it holds no `files.json`.
8. **Verify.** `personant verify` fails a thread whose index names a missing blob or diff file, or a
   blob whose bytes do not hash to its name.
9. **Removal.** `git grep -n -e LiveWindow -e "dedup.Chain" -e AgeFileChains -e GetFileVersion -e
   BlobReachable -e ShowFileAtCommit -e chain-age -e "tracked files ===" -- '*.go'` returns nothing.
10. **Over-share files never expand.** A file whose current content is at or above
    `dedup.literal-share-threshold` × the per-thread Layer B share renders no literal and no diff
    anywhere in the composed working set, and each of its positions shows its identifier line. A
    file just under the threshold, in the same context, does render its literal.

---

