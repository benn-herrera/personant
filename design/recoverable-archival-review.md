# Adversarial data-integrity review — §3.8 recoverable archival (#99)

Reviewer: adversarial security/data-integrity pass over `git diff c9a741d..HEAD`
(I1 ccd325b → I5 569b2e5 + SPEC 8920c13), 2026-05-29. Read-only; findings only.

## Headline (the result that lets #99 stand)

**No CRITICAL, no unmitigated data-loss hazard on the happy path.** Verified:
the capture commit pins thread bytes into a committed tree *before* any removal;
the deletion commit's parent always holds them (reachable via `git log` regardless
of the index); `archive.archived` is logged only *after* all three commits durably
land, so happy-path forgiveness/accounting cannot hide loss. Archival does not
destroy accumulated memory. Recovery aborts (no partial spine write) on tree-hash
mismatch.

The exposure is concentrated in (a) crash windows that hand obligations to #94
startup-reconcile, and (b) a cluster of latent integrity-token + measurement
fragilities — none triggerable by today's `.md`/`.json` thread content, but the
kind that bite silently later.

## Triage (Claude, for the build)

- **#94's job** (crash-window reconcile obligations): F1, F2 (crash arm), F7.
  Added to task #94.
- **#99 hardening — worth landing before production reliance** (task #101):
  - **F3 (HIGH, the priority)** — integrity token uses `WorktreeTreeHash` (hashes
    on-disk, ignores `.gitignore`, derives mode from `os.Stat`) at capture, but
    `CheckoutTree` restores from the committed tree at `0o644`. A future
    gitignored or executable file in a thread dir → a *legitimate* recovery
    FALSE-fails (unrecoverable-in-practice) while passing all current tests. Fix
    (reviewer-identified): capture with `TreeHashAt` (committed-tree, gitignore-safe
    — already exists) instead of `WorktreeTreeHash`, and preserve committed file
    mode on restore. Latent today; a real correctness bug in the integrity guarantee.
  - **F4 (MED)** — mid-loop `eventlog.Log` failure under-counts `archivedThreadIDs`
    for durably-archived threads → false `recall_unexplained_absence` (and the
    symmetric danger: a real loss masked). The forensic log must be emitted for
    *exactly* the durably-committed set, with no partial-emit window.
  - **F8 (LOW)** — recovery gauge leaves recovered threads in `archivedThreadIDs`;
    `VerifyThreadAccounting`'s "both on-spine and archived" signal is safe only by
    gauge-runs-after-invariants ordering. A recovered thread should leave the
    archived accounting set (or accounting treat a stamped `RecoveredAt` as
    "no longer archived") so order can't manufacture a false corruption signal.
- **Design clarifications / roadmap-coupled** (noted, not blocking):
  - **F5 (MED, latent)** — `RemoveSpineRecords` silent-ignore + separate spine reads
    for validate vs remove = TOCTOU shape; safe single-threaded. Archived-index
    membership and spine-removal should derive from one atomic spine snapshot.
  - **F6 (MED, roadmap)** — `ParentCommitHash` takes `ParentHashes[0]` unconditionally;
    a future merge commit (v2.0 submind-via-clone integrates via `git push + merge`)
    makes that the wrong parent → recovery from wrong lineage. The design's own Q3
    flagged storing `parent_commit_hash` explicitly; impl chose walk-parents. Also:
    empty-`TreeHash` drift entries + recovery (empty-vs-empty hash match) is not
    obviously a distinct outcome from a real verified recovery.
  - **F7 (LOW/MED)** — `symbols.jsonl` is gitignored yet `CheckDerivedFresh` gates
    every archival/recovery commit: verified-fresh-on-disk but never committed. The
    post-commit "tree is derived-fresh" story is half-true (digests committed,
    symbols.jsonl not). Not data loss (rebuildable); clarify for #94 whether a
    committed archival tree is self-consistent or needs a symbols rebuild.
  - **F9 (LOW)** — capture commit `Add(".")` pins the *entire* worktree, not just
    thread dirs → archival is the de-facto commit point for arbitrary uncommitted
    work, under an "archive: capture" message. Either commit only owned paths, or
    explicitly own that archival is a global commit boundary at turn-close.

---

## Findings (verbatim from the reviewer)

### F1 — HIGH (depends on #94): crash between deletion commit and stamp commit → un-stamped, unrecoverable-by-lookup index entry
`ArchiveThreads` (`fileadapter_archive.go:249–274`): the deletion commit lands
(HEAD moved, bytes gone from worktree, spine rewritten); index entries are stamped
with `CommitHash` only after. A crash / swallowed post-flag error / `AppendArchiveEntries`
or stamp-commit failure in that window leaves `archive/index.jsonl` entries with
`CommitHash == ""`. `RecoverThread` correctly refuses (`:306–311`). Bytes NOT lost
(reachable via `git log`); thread unrecoverable *through the port* until reconcile
re-stamps. **#94 must:** detect empty-`CommitHash` entries and reconstruct the hash
from history (match `OriginalPath` against the commit whose tree removed that dir).
Invariant: *no committed index entry may have an empty `CommitHash` after reconcile.*

### F2 — HIGH (depends on #94): drain swallows a POST-deletion-commit failure
The drain (`turn/archival.go:104–109`) swallows ALL `ArchiveThreads` errors as
opportunistic/non-fatal, justified by "pre-flag fails before any mutation." That is
**FALSE for the post-deletion-commit window**: a post-flag verification failure
(`:251`) or stamp-sequence error (`:264–273`) has already committed the spine removal
+ worktree deletion. The turn proceeds as if nothing happened; K threads durably
archived with un-stamped entries (compounds F1) and `result` discarded so log lines
never emit (compounds F4). The error contract must distinguish "failed before any
commit (safe to swallow)" from "failed after a commit landed (substrate mutated —
surface to reconcile)."

### F3 — HIGH: tree-hash integrity token silently depends on `.gitignore` + file-mode invariants nothing enforces
Recovery integrity rests on `WorktreeTreeHash` (`autogit.go:410–465`) matching at
capture and recovery-verify. (1) **Gitignore divergence:** `WorktreeTreeHash` hashes
every on-disk file (ignores `.gitignore`); the capture commit `Add(".")` honors it.
A future thread-dir file matching a gitignore rule → captured hash includes it,
`CheckoutTree` (`:328–349`) never restores it → `VerifyTreeHash` FALSE-fails a
recoverable thread (`:326–330`). (2) **File-mode divergence:** `WorktreeTreeHash`
derives mode from `os.Stat` (`:448–451`); `CheckoutTree` writes `0o644` (`:346`). An
executable thread file → different tree hash → false integrity failure. Latent
(`.md`/`.json` today). Fix: capture via `TreeHashAt` (committed tree, gitignore-safe —
exists; capture path uses `WorktreeTreeHash` at `fileadapter_archive.go:193`) and
restore preserving committed mode.

### F4 — MED: mid-loop `eventlog.Log` failure manufactures false `recall_unexplained_absence`
Step 7 (`fileadapter_archive.go:279–286`) emits one `archive.archived` per id in a
loop; a mid-loop failure returns error (`:283`) after all K are durably archived.
Threads after the failure are committed-archived but unlogged → harness (`:309–310`)
under-counts `archivedThreadIDs` → oracle (`recall_fidelity.go:264–268`,
`harness_run.go:237–248`) counts them off-spine-and-not-archived →
`recall_unexplained_absence` trips on correctly-archived threads (and the symmetric
masked-real-loss is worse). The forensic evidence must cover exactly the committed set.

### F5 — MED (latent): `RemoveSpineRecords` silent-ignore + non-atomic validate/remove reads
`RemoveSpineRecords` (`spine_ops.go:91–117`) silently ignores missing ids, never
errors. Validation read (`:114`) and removal read (inside `RemoveSpineRecords`, `:99`)
are separate `ReadSpine` snapshots. Single-threaded today = latent. Archived-index
membership and spine-removal must derive from one atomic spine snapshot.

### F6 — MED: parent addressing + empty-tree-hash recovery
`ParentCommitHash` (`autogit.go:165–168`) takes `ParentHashes[0]` unconditionally; a
future merge commit → wrong parent, silent wrong-lineage recovery. Drift threads
archived with empty `TreeHash` (`fileadapter_archive.go:131–134`, `:188–197`):
recovery's empty-vs-empty compare is not a distinct outcome from a verified recovery.
Enforce single-parent (or store explicit parent hash, per design Q3); recovering
"nothing" must be explicit.

### F7 — LOW/MED: `symbols.jsonl` gitignored yet gates commits
`seedGitignore` (`init.go:418`) excludes `symbols.jsonl`; every archival/recovery
commit gates on `CheckDerivedFresh` (`fileadapter_archive.go:251`, `:381`) which
passes (fresh on disk) but the file is never committed. Digests committed +
verified; symbols.jsonl verified-but-uncommitted. Not loss (rebuildable); the
post-commit derived-fresh story is half-true. Clarify for #94.

### F8 — LOW: recovery gauge leaves recovered threads in `archivedThreadIDs`
`runRecoveryGauge` (`harness_run.go:335–372`) re-adds to spine but never clears
`archivedThreadIDs`; `VerifyThreadAccounting` (`invariants.go:314–348`) flags
on-spine-and-archived as corruption. Safe ONLY because the gauge runs after the final
invariant sweep (`:331–334`) — an ordering coincidence, not an invariant.

### F9 — LOW: capture commit `Add(".")` pins entire worktree
`fileadapter_archive.go:165–183` stages `Add(".")` — commits all worktree changes
under "archive: capture N thread(s)." Archival becomes the de-facto commit point for
arbitrary unrelated work; a half-written unrelated file would be committed under an
archival message into the recovery parent lineage.

## Out of scope (not reviewed)
#94 reconcile itself; authn/secrets (none here); go-git CVE/license audit; concurrency
(single-threaded today — F5/F6 noted latent vs future concurrency + v2.0 submind merge).
