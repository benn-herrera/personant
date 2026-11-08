#### Day rollover

The rollover fires on new-day detection — the day index exceeds the head day number — checked at
service-run open, after startup recovery, and before each turn. It runs under an
`op=barrier {day: N}` marker. Its steps, in order:

1. **Rollover marker step:** write the `op=barrier` marker. It owns the whole sequence, including
   the archival step, which opens no nested `op=archival` scope.
2. **Archival step:** archival-if-pressure against the history repository via the batch archive
   (see "Closing a thread"; no nested marker). Batch membership is recorded durably before any
   deletion, and its capture commit stages the full store working tree into the history repository,
   which makes the turn-repository removal step safe (capture-before-removal rule). The step is
   skipped while the registry is not under size pressure, and the day-commit step is then the sole
   store-working-tree capture.
3. **Excerpt-storage step:** each live thread's turn excerpts move between individual files and its
   excerpt bundle to match the placement rule (see "Thread directory"). Store working tree only,
   byte-preserving and idempotent: every excerpt stays readable at every instant, and the moves
   land in the day-commit step's commit. Tiers change at no other time.
4. **File-retention step:** each live thread's tracked-file store drops entries whose latest read
   or write has left the recent-excerpt window, every file no kept entry names, and a legacy
   `files.json`. Store working tree only and idempotent; the deletions land in the day-commit
   step's commit.
5. **Day-commit step:** commit the store working tree to the history repository with
   `Personant-Day: N`, staging everything including overnight hand-edits. The staged tree is
   validated for thread registry integrity before the commit is minted (history-writer rule). The
   step is guarded by the day-commit guard, and an empty day still produces exactly one trailered
   day-commit, so the head day number is always defined.
6. **Derived-rebuild step:** rebuild derived files from the now-committed store working tree,
   re-setting the watermark against the pre-removal turn repository HEAD.
7. **Turn-repository removal step:** `rm -rf .git-daily` (idempotent).
8. **Turn-repository re-init step:** create the turn repository with a baseline commit of the
   current store working tree, giving the baseline hash H_d.
9. **Freshness-assert step:** set the watermark to H_d (watermark rule), assert that derived files
   are fresh, and clear the marker.

Only the archival and day-commit steps mutate the history repository; the turn-repository removal
and re-init steps are pure turn-repository lifecycle; the derived-rebuild and freshness-assert
steps touch derived files and the watermark. The authoritative content of the store working tree is
untouched from the day-commit step through the freshness-assert step. It is only reduced by the
archival step's removals, whose bytes are preserved in the history repository, and by the
file-retention step, which deletes on purpose, including file versions written and superseded since
the last day-commit, whose bytes never reach the history repository. The excerpt-storage step only
rearranges it, without loss.

**Turn-repository init** (`git init .git-daily`, a baseline commit of the store working tree, and
the watermark set to the baseline hash) is exactly the turn-repository re-init and freshness-assert
steps in isolation. It costs O(store working tree bytes), meaning the live, non-archived threads,
independent of career-history length.

**Lifecycle git tags.** After the day-commit step lands, inside the `op=barrier` scope and before
the marker is cleared, the rollover derives git tags on the history repository from the sealed
day's event log: `thread.created` → `thread/<id>/created`, `retire.complete` →
`thread/<id>/retired` and `project.created` → `project/<id>/created`, all targeting that day's
day-commit, and `archive.archived` → `thread/<id>/archived`, targeting the archival commit recorded
in the thread's archive-index entry. Project open, close and switch are not mapped. The ref name is
`<kind>/<id>/<event>_<stamp>`, e.g. `thread/thr_42/created_2026-05-08T03-12-00Z`; `<stamp>` is the
event's UTC-normalized RFC3339 instant with time colons rewritten to hyphens (git refs forbid `:`),
so names are unique and sort lexically as chronologically. Derivation is idempotent (skip if the
exact name exists) and best-effort: a read, parse or tag fault logs `barrier.tag-error` and is
skipped, never failing the seal. Day-scoping: the rollover for day N fires the next day, so
`created`, `retired` and project lines are those with event day index N, while `archived` (emitted
by the rollover itself) is not day-filtered; life events on earlier idle gap-days, completed days
before N on which no rollover ran, go untagged.

**Roll-forward completion** (a crash inside the rollover). Every crash window recovers by
roll-forward, never a reset. The `op=barrier` marker, with its `day: N`, is the idempotent re-entry
token, and every sub-action is guarded and idempotent, so re-entry converges. The archival step's
completion re-derives the durable batch-membership worklist: ensure-removed →
ensure-registry-removed → regen → the deletion commit (only if none has landed, else back-fill the
located one) → back-fill. A deletion commit that already landed is back-filled, not duplicated
(where roll-forward meets case 9), and completion must not infer state from
store-working-tree-clean-versus-HEAD, because an overnight hand-edit would be misread as
uncommitted removals. Completion also re-runs the archival step's pressure drain, so a rollover
killed before archival does not defer the day's archival, and re-runs the excerpt-storage and
file-retention steps while the day-commit has not landed, finishing any moves or deletions the
crash interrupted.

**Day-commit-shape guard.** New-day detection reads the head day number off any trailered
history-repository commit; the guard's idempotence check fires only on a day-commit-shape HEAD for
the target day. A trailered but non-day-shape HEAD (a roll-forward back-fill, an adopt) advances
detection but does not satisfy the guard, so the real day-commit is still minted. Recovery is
fork-free across a completion on the following day (N+1) and across adopt-then-rollover kill
timings. Only completed days, strictly before the current clock day, are sealed, each exactly once
(gap-day at-most-once).

**Quarantine-and-proceed.** If the day-commit step's thread registry integrity check or the
freshness-assert step's derived assert fails and still fails across one idempotent re-drive:
(1) identify the offending paths; (2) quarantine their bytes byte-exact under
`recovery/quarantine/<stamp>/`; (3) scoped-restore exactly those paths from history repository
HEAD — path-scoped, never `reset --hard` and never a whole-store-working-tree restore; derived
paths, absent from the history repository, are regenerated and verified instead, and an offending
path absent from HEAD is quarantined and removed; (4) re-run the failing step once; (5) if it still
fails, refuse to open with the marker left in place as the retry token, and name the quarantine
directory in the report. This is safe because the restore is scoped and quarantined first (no-reset
rule) and history repository HEAD always passes thread registry integrity validation
(history-writer rule).

**Archival crash completion.** Archival fires only at the rollover's archival step, so apart from
the recovery paths the history-writer rule names, the history repository is written only at
rollover. Completion is deterministic because membership is recorded before deletion (archive-index
entries are appended with `parent_commit_hash`, `tree_hash`, `original_path` and the thread
registry snapshot, and an empty `commit_hash`, before any thread directory is removed, leaving a
durable worklist) and because an already-landed deletion commit is resolved as the child of the
entry's `parent_commit_hash` by tree comparison, which is robust to overnight hand-edit dirt, so
the roll-forward back-fills the existing commit instead of minting a duplicate
(`recovery.LocateDeletionCommit`, see "Mechanism names"). Outside a rollover only tests call `ArchiveThreads`; such a
batch runs under its own `op=archival` marker, and case 8 is likewise
detect-then-complete, reporting it pending on marker presence alone while the memory service runs
the same roll-forward tail. Turn and structural commits land in the turn repository and fold into
the day at the rollover, so archival, which writes the history repository, never races a turn.

**Intra-day derived staleness.** The turn path never sets the watermark; the day rollover owns
freshness (derived-rebuild and freshness-assert steps). Between rollovers, derived files track the
turn repository's per-turn state via the watermark, and a markerless hand-edit stays deferred
(case 3) until the next full staging pass.

**Greenfield and legacy.** A greenfield store (no watermark, no turn repository) takes the case 12
verify-gated adopt-commit to the history repository, with `Personant-Day: <current day>` as the
sole bootstrap carve-out, then back-fill repair, a full rebuild and a
fresh turn-repository baseline. An `op=turn` marker file left by an older binary is honored and
cleared. A missing turn repository on an existing store directory is the legacy-upgrade shape and
stays absent through init (`store.Init` creates `.git-daily`, the turn repository, only alongside
a fresh history repository), so case 12's adopt gate and the benign-morning row (turn-repository init with
no adopt commit) discriminate correctly. Legacy archive entries awaiting back-fill take the general
adopt-forward repair path, with no special case.

