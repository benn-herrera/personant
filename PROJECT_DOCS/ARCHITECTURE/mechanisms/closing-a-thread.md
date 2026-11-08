### Closing a thread

Triggers:

- Engagement decay: no engagement for `engagement.decay-turns` OR `engagement.decay-time`
  wall-clock (either threshold; OR semantics). The turn count is `TurnNumber − last_engaged_turn`, both in the service run's turn counter, which starts again at 0 on every relaunch (see "Start order and resume", U9). The stored `last_engaged_turn` therefore means nothing to a later run. A stored value above the current counter skips the turn threshold; a lower one is compared as if it came from the same run (`decayEligible`, `internal/turn/closure.go`). Across a relaunch, only the wall-clock threshold is a durable decay timer.
- An explicit close request (`/done`; **Open:** U4 — the command).

Flow (SPEC §3.5): the summarizer drafts the closing summary; a closing writes the
thread file and updates the thread registry, and demotion out of the active and dormant sets follows.

Cold storage archival (`internal/memops/fileadapter/fileadapter_archive.go`;
`cmd/archive.go` `archive list`/`archive recover`): when thread registry size pressure builds, the
memory service archives the oldest closed threads into cold storage, out of the thread registry, by
removing their directories, committing the removal and recording an `archive/index.jsonl` entry. (SPEC §3.8.)

A batch meets the three results of SPEC §3.8.1 as follows:

- *Membership recorded before deletion.* One entry per thread (see "Archive index") is durably
  appended to `archive/index.jsonl` with an empty `commit_hash` but full `parent_commit_hash`,
  `tree_hash`, `original_path` and thread registry snapshot, before any thread directory is
  removed. That entry set is the worklist a crash completion consumes. Each `threads/thr_<n>/` is
  confirmed tracked at HEAD before its tree hash is taken.
- *Bytes reachable after.* The capture commit is `Add(".")` of the full store working tree into the
  history repository and precedes the membership append. It lands the day's cumulative content,
  which makes removing the turn repository at rollover safe, and becomes the parent of the deletion
  commit whatever the turn repository's commit cadence. Each directory's git tree hash is captured
  there, and a restore verifies against it. Removals are staged by `os.RemoveAll` plus
  `autogit.Add(".")`, and `RemoveSpineRecords` performs the single thread registry rewrite, with
  derived state regenerated once for the batch. The capture commit runs `CheckSpineIntegrity`
  before; the deletion commit runs `CheckDerivedFresh` and `CheckSpineIntegrity` after.
- *Bounded commits.* A final back-fill commit records the deletion commit's hash as `commit_hash`
  in the entries awaiting back-fill, so a batch adds a bounded number of commits.

Restore (`RecoverThread`, `autogit.CheckoutTree`):

1. Look up the entry by `thr_id`; refuse with `ErrArchiveEntryNotFound` if absent, and refuse an
   entry whose `commit_hash` is empty.
2. Restore the full `threads/thr_<n>/` subtree from the entry's `parent_commit_hash` commit; an
   entry lacking it (written before the field existed) is restored from the deletion commit's first parent (`autogit.ParentCommitHash`), the capture commit, since Personant makes no merge commits.
3. Recompute the restored directory's tree hash and verify it against `tree_hash`. A mismatch
   deletes the partial restore and fails with `ErrArchiveIntegrity`, so a partial thread is never
   written onto the thread registry (SPEC §3.8.3).
4. Add a thread registry record reusing the same `thr_<n>` id (the registry stays sorted by id),
   with `state = wip` and `last_engaged` updated; anchors and summary come from the restored
   frontmatter, not the archive snapshot.
5. Set `recovered_at` on the entry and commit the restored files, the registry and the archive
   index to the turn repository (`autogit.Daily`, message `archive: recover …`), which folds them
   into the day at the next rollover.

Archived threads are off live recall because the §3.4 scan covers the thread registry and
per-thread frontmatter, and an archived thread has neither. `RecoverThread` and `ArchiveThreads`
are public port operations; no product path calls `ArchiveThreads` outside the rollover (tests do), and such a call is crash-recoverable through the same
roll-forward (case 8).

