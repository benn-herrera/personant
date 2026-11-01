### Crash stability and the dual-repo substrate

The service can be hard-killed (SIGKILL, power loss) at any instant, so recovery is designed as a
**deterministic cold-start reconciliation** — no signal handler doing teardown work, no LLM in the
loop, no user acknowledgement. On-disk state alone determines the outcome. The load-bearing
property: **the worktree is the truth; git is a recovery-point index over it.** No canonical byte
ever lives *only* in git, so any git DB can be recreated from the worktree and losing a DB is never
losing content. (Spec §4.5.8, #94.)

Two git DBs track the one worktree, daily per-turn and disposable, primary day-grain and permanent
(cadence, cost bar and ≤1-turn durability: SPEC §3.11). The in-flight turn's raw
`(prompt, response)` bytes are protected by a durable append journal written *before* any canonical
write, so even the one at-risk turn's content survives.

Recovery reduces to a small state machine over on-disk observables (marker file, in-flight journal,
daily/primary HEAD trailers, watermark, daily-DB presence) that classifies into one cell and repairs
idempotently — a torn turn resets **only daily** to ≤1-turn-ago; a barrier or archival crash **rolls
forward** (never resets the worktree, which would destroy up to a day); a broken hand-edit is
**quarantined byte-exact, never deleted**. Ruled out permanently: loss-bar extension (commit
batching / ≤N-turn windows — ≤1 turn is the bar), daily-DB durability guarantees (disposable by
construction), and primary auto-recreation (a missing primary is refuse-to-open, not a benign
recreate).

Under opencode the bar is judged against turns the user saw complete (SPEC §4.5.8 (10)): a turn
opencode recorded as terminal that memory rolled back is lost until reconcile re-ingests it (see
"Start order and resume").

Mechanism names. A turn is two journal appends (`JournalTurn`, kinds `prompt` and `response`)
followed by `CommitTurn`, which stages only the recorded write set through `autogit.AddPaths` with
no integrity flags; `store.JournalOwner` reads the turn id from the journal's first record, and a
legacy `op=turn` marker file is still read. Core recovery (`internal/recovery`) only detects barrier
and archival cells and returns a typed `RecoveryReport.Pending` (`PendingCompletion{Kind, Day}`,
plus `ScopedRestored` for quarantined-and-restored paths); `fileadapter.Reconcile`, which owns
`ArchiveThreads` and both repo handles, completes them, which keeps `recovery` from importing the
adapter. `recovery.LocateDeletionCommit` (`internal/recovery/stamp.go`) finds an already-landed
deletion commit by a child-of-`ParentCommitHash` tree test; `store.TruncateJournal` carries the
truncate-no-fsync proof (`TestCell5_ResurrectedTruncatedJournal`). `store.Init` births `.git-daily`
only alongside a fresh primary, so a missing daily on an existing home keeps its legacy-upgrade
shape. The barrier mints its day-commit with `AllowEmptyCommits` after `CheckSpineIntegrity` on the
staged tree, asserts `CheckDerivedFresh` at B6, and derives lifecycle tags through
`autogit.TagStamp` / `ParseTagStamp` (the only formatters), reading the sealed day's log with the
tolerant `eventlog` reader and tolerating `ErrTagExists`. Recovery handles and plumbs the
`op=rebaseline` marker, but the mid-day arming trigger is not wired (ROADMAP.md). Packing: after
every `gcCheckEveryCommits` (64) daily commits the adapter reads daily's loose-object count and runs
gc at `gcLooseObjectThreshold` (5000); `MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`)
packs both repos on the day-off idle window. Session checkpoints target daily with a full `Add(".")`
sweep. The open sequence runs `memops.GateHomeFormat` before Reconcile;
`version.UnversionedHomeFormat` is pinned at 1 forever and is never an alias of
`version.CurrentHomeFormat`, so an unmigrated legacy home keeps resolving to format 1.

