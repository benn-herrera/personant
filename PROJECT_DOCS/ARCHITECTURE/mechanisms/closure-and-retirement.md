### Closure and retirement

Triggers:

- Engagement decay: no engagement for `engagement.decay-turns` OR `engagement.decay-time`
  wall-clock (either threshold; OR semantics).
- An explicit close request (`/done`; **Open:** U4 — the command).

Flow (SPEC §3.5): curator drafts retirement summary → the candidate is classified. A **routine**
closure is applied without asking, with one visible line and an `ack=auto` event. An **exception**
(anchor-rich, long-engaged, or a failed or empty draft) is queued silently. The queue is derived,
never stored — a decay-eligible thread that classified as an exception — so it is crash-safe and
correct across restarts. A drained closure writes the thread file, generates the spine entry, and
Layer B/C eviction follows (rationale for auto-applying routine closures: "The model drafts,
deterministic code writes").

**Open:** U4 — the channel and the boundary for the exception drain, the `[e]dit` choice, `always`
mode and the pending-queue announcement. An opencode session never exits, so the clean-exit
boundary the drain used has no counterpart.

Deep cold archival (implemented; `internal/memops/fileadapter/fileadapter_archive.go`,
`cmd/archive.go` `archive list`/`archive recover`): when spine cardinality pressure builds, the
runtime archives oldest retired threads off-spine via `git rm` + `git commit` + an
`archive/index.jsonl` entry. (Spec §3.8.)

A batch meets SPEC §3.8.1's three results as follows: each `threads/thr_<n>/` is confirmed tracked
at HEAD before its tree hash is taken; the capture commit is `Add(".")` of the full worktree into
primary and precedes the membership append; removals are staged by `os.RemoveAll` plus
`autogit.Add(".")`, and `RemoveSpineRecords` performs the single spine rewrite; the deletion commit
runs `CheckSpineIntegrity` before and `CheckDerivedFresh` after. Recovery (`RecoverThread`,
`autogit.CheckoutTree`) refuses with `ErrArchiveEntryNotFound` or `ErrArchiveIntegrity` and never
writes a partial thread. `RecoverThread` and `ArchiveThreads` remain public port operations; a
standalone `ArchiveThreads` is crash-recoverable through the same roll-forward.

