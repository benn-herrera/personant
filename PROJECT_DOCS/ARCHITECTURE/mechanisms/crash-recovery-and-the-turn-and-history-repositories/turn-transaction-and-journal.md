#### Turn transaction and journal

A turn is written to the turn repository in three ordered steps:

1. Append the prompt record to the journal (`JournalTurn`, kind `prompt`, fsync) before the model
   call. The first append arms the in-flight signal.
2. Append the response record (kind `response`, fsync) before any authoritative write. A
   journal-append failure aborts the turn before any authoritative write.
3. At turn close, `CommitTurn` commits the turn's scoped write set to the turn repository with the
   `Personant-Turn: <id>` trailer, then truncates the journal; the truncate is the scope release.
   A commit failure leaves the journal non-empty and fails loudly into recovery.

Staging is scoped to the turn's recorded write set (`spine.jsonl`, touched thread directories, the
day's event log, recorded at each write site and never guessed) through `autogit.AddPaths` with no
integrity flags, so it costs O(write set) with no tree walk and turn-repository commits are not
validated. Creating or closing a thread rides the per-turn
commit and folds into the day at rollover; creating a project takes a checkpoint (SPEC §3.11). A hand-edit is therefore absorbed at the next full
staging pass: a checkpoint, or the archival and day-commit steps of the day rollover.
The §5.5 re-prompt caps live entirely inside a turn, ahead of step 3.

The journal is `turn-journal.jsonl` in the store directory (flat, operational, gitignored): one
record per line, `{turn, kind: prompt|response, at, bytes}`, fsync per append. The truncate to
empty at turn commit carries no fsync (`store.TruncateJournal`). A power-loss resurrection of a
stale-but-truncated journal is the redundant-journal case, case 5 (`turn == head turn id`)
(`TestCell5_ResurrectedTruncatedJournal`; Cell N is case N), and the next turn's first append fsync makes the
truncate durable before any new authoritative dirt can exist. A torn append is bounded to that one
turn's content, which is the ≤1-turn bar. A torn final line is skipped by the scan, never parsed.
On a torn turn the journal bytes are written to a recovery artifact and reported in the event log
(`recovery.journal-recovered`). They are never auto-replayed into the authoritative files, because a
turn's derivation (tag parse, symbol extraction, engagement) is not replayable.

