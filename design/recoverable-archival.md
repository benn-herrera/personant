# Recoverable git-based archival (§3.8) — design + increment plan

**Task #99.** Replace the v0.1 deletion STUB in `FileAdapter.ArchiveThread` with
SPEC §3.8 recoverable git-based archival. Pulled from v0.2 into the v0.1
acceptance path because the long-rung recall measurement is currently taken
against a substrate that destroys dormant threads (see §6, measurement honesty).

DESIGN ONLY. Grounded against: `internal/memops/fileadapter/fileadapter.go`,
`internal/autogit/autogit.go`, `internal/turn/archival.go`,
`internal/store/spine_ops.go`, `internal/scenarios/{recall_fidelity,harness,harness_run}.go`,
`SPEC.md` §3.8 / §2.1 / §2.3, `ARCHITECTURE.md`.

---

## 0. Grounding facts (verified, not assumed)

- `ArchiveThread` (fileadapter.go:281) today: measures body size → `os.RemoveAll(ThreadDir)`
  → `fmCache.Invalidate` → `store.RemoveSpineRecord` (full spine rewrite) → logs
  `archive.simulated-delete` + `archive.warning`. No git. No index.
- The drain (`surfaceArchivalCandidates`, turn/archival.go:60) loops `ArchiveThread`
  per coldest retired thread until spine count ≤ `archiveLowWater` (150), then
  regenerates the derived index ONCE post-batch. Per-thread `RemoveSpineRecord` is
  O(spine) ⇒ batch is O(K·spine) full rewrites.
- The substrate IS a git repo: `store.Init` calls `git.PlainInit` (init.go:221).
- `autogit` already exposes `Add`, `Commit` (with pre/post `GitCheckFlags`), and
  `Checkout(commitHash, filePath, ...)` — the `git show <commit>:<path>` equivalent,
  via tree-walk + raw write. **But `Checkout` operates on ONE file**; a thread is a
  directory. **And the fileadapter does not currently import autogit at all** —
  this is the first adapter caller of the autogit seam.
- `PersonantPaths` has NO archive-index field (paths.go). `SPEC.md:58` already
  reserves `archive/index.jsonl` as canonical-but-empty-until-v0.2.
- A thread is a DIRECTORY (`threads/thr_<n>/` = `thread.md` + `turns/` + `files.json`),
  confirmed by ArchiveThread's own `os.RemoveAll(ThreadDir)` and SPEC §2.3. §3.8.1's
  `git rm threads/thr_<n>.md` + single `blob_hash` is STALE.
- Measurement workarounds in place: `RecallExpectedForgiven` (#97, recall_fidelity.go:235)
  and `TargetRecoverable` (#96, harness_run.go:220) both key off `h.archivedThreadIDs`,
  which is folded from the `archive.simulated-delete` log line (harness_run.go:286).

---

## 1. Invariants

1. **Recoverability.** Every archived thread is recoverable to a byte-exact copy of
   its pre-archival on-disk directory. The deletion commit is the canonical store;
   `archive/index.jsonl` is the lookup. Losing the index must not lose the data —
   the commit is reachable by `git log` regardless.
2. **Integrity-verified recovery.** Recovery verifies the recovered directory against
   a stored **tree hash** (not a per-file blob hash). Mismatch aborts and surfaces an
   error; it never writes a partially-recovered thread onto the spine.
3. **Atomicity per drain.** One archival drain produces a consistent end state: either
   a thread is fully archived (gone from worktree + spine, present in index + commit)
   or it is not touched. An interrupted drain leaves a recoverable, reconcilable state
   (§7, crash safety). No state where a thread is off-spine but absent from both the
   index and a reachable commit.
4. **Canonical, sorted archive index.** `archive/index.jsonl` is canonical (SPEC §2.1),
   one entry per archived thread, **sorted by `thr_id`** for line-grain diffs. Entries
   are append-only forensic breadcrumbs — kept after recovery, never rewritten on
   recovery.
5. **Spine consistency.** A thread is on the spine XOR in the archive index, never both,
   never neither-after-being-known. Recovery re-adds a fresh spine record AND retains
   the index entry (the documented §3.8.3 exception to the XOR — index entry is then a
   historical marker, not a "currently archived" claim; recovery stamps it).
6. **Drift-free derived state.** `symbols.jsonl` + per-project `digest.json` are
   regenerated ONCE per drain (existing behavior preserved). The drain commit must pass
   `CheckDerivedFresh` — so derived regen happens BEFORE the final commit, not after.
7. **Batched cost.** Per-drain spine mutation is ONE rewrite and git work is bounded
   commits per drain — NOT O(K) full spine rewrites. (§4, performance.)

---

## 2. §3.8 SPEC deltas required

List for the SPEC editor. Each is a stale-spec reconciliation, not a redesign.

- **D1 (§3.8.1 step 1–2): thread is a directory.** Replace "confirms
  `threads/thr_<n>.md` is in git's working tree" and "`git rm threads/thr_<n>.md`"
  with: confirm `threads/thr_<n>/` is tracked at HEAD; stage the recursive removal of
  the whole directory (`thread.md`, `turns/`, `files.json`).
- **D2 (§3.8.1 step 4 + §3.8.3 step 3): tree hash, not blob hash.** The index field
  `blob_hash` becomes `tree_hash` — the git tree object hash of `threads/thr_<n>/` as
  it existed at the deletion commit's PARENT (i.e. the last commit where the thread was
  present). Recovery verification re-walks the recovered directory and compares its
  computed tree hash against `tree_hash`. `original_path` becomes the directory path
  `threads/thr_<n>/`.
- **D3 (§3.8.1): autogit seam, not shell.** State explicitly that steps 2–3 and 5–6 go
  through `internal/autogit` (go-git), never shell git, never a pre-commit hook —
  consistent with ARCHITECTURE.md §8.3.
- **D4 (§3.8.1 step 6 + batch): batched drain commit.** The mechanism describes a single
  thread. Add a note: the cardinality-pressure trigger archives a BATCH; the spine
  rewrite and the derived-index regen happen ONCE for the batch, and the drain produces
  a bounded number of commits (§4), not one commit per thread per the naive reading.
- **D5 (§3.8.3 step 2): directory recovery.** `git show <commit>:<path>` recovers a
  single blob; recovering a directory walks the tree and writes every entry. Phrase as
  "restore the full `threads/thr_<n>/` subtree from the deletion commit's parent."
- **D6 (new, §3.8.4): measurement semantics.** Add the recall-treatment rule from §6
  below (archived-recoverable is off-recall until recovered; archive-index anchors are
  NOT a live recall surface in v0.1).

---

## 3. Archival mechanism (batched)

### 3.1 Port surface

`ArchiveThread` stays the stable seam — **signature unchanged**. But the per-thread
git-commit-per-call shape is wrong for the batch. Two options; recommend **(B)**:

- **(A)** Keep `ArchiveThread(ctx, threadID)` doing the full git dance per call. Simple
  port, but forces either a commit-per-thread (K commits/drain, slow, noisy history) or
  hidden cross-call batching state in the adapter (a buffer that some later call must
  flush — fragile, and the drain has no "flush" call).
- **(B) [RECOMMEND]** Add a batch op to the port and have the drain call it once:
  ```go
  // ArchiveThreads archives a batch of retired threads atomically-per-drain:
  // stages recursive removal of each thread dir, captures each thread's
  // pre-removal tree hash, removes all spine records in ONE rewrite, appends
  // sorted archive-index entries, regenerates derived state, and commits the
  // whole batch (worktree removals + spine + index + derived) as one autogit
  // commit gated by CheckDerivedFresh|CheckSpineIntegrity. Returns per-thread
  // outcomes so a partial failure is visible. Order-independent; sorts internally.
  ArchiveThreads(ctx context.Context, threadIDs []string) (ArchiveResult, error)
  ```
  Keep `ArchiveThread(ctx, id)` as a thin wrapper over `ArchiveThreads([]{id})` for the
  CLI single-thread path and existing tests. The drain (turn/archival.go) collects its
  coldest-first slice and calls `ArchiveThreads` ONCE.

  This makes invariant 3 (atomicity-per-drain) and invariant 7 (batched cost) structural
  rather than emergent, and it kills the per-thread `RemoveSpineRecord` O(K·spine) cost
  (§4). The `RemoveSpineRecord` single-id helper stays for the CLI/wrapper path; the
  batch path needs a new `store.RemoveSpineRecords(paths, ids []string)` (one read, one
  filtered rewrite).

### 3.2 Batch algorithm (adapter-internal, `ArchiveThreads`)

For the batch `ids` (sorted):
1. **Validate + capture tree hashes.** For each id: find spine record (skip-with-result
   if absent), confirm `threads/<id>/` tracked at HEAD, compute the directory's current
   git tree hash (the value verification will check on recovery). Capture
   `spine_summary`, `anchors`, `project` from the spine record.
2. **Stage recursive removals.** For each id, stage removal of `threads/<id>/`
   recursively. **Required autogit extension — see §5 (E1).** Invalidate `fmCache`
   per id.
3. **One spine rewrite.** `store.RemoveSpineRecords(paths, ids)` — single read, single
   filtered `WriteSpine`.
4. **Append sorted index entries.** Append one `archive/index.jsonl` entry per id,
   then re-sort the file by `thr_id` (entries are few and the file is canonical-sorted;
   a read-merge-write keeping sort order is fine — this is not a hot path).
5. **Regenerate derived state** ONCE (`index.Rebuild`) so the commit is `CheckDerivedFresh`.
6. **Stage spine + index + derived + thread removals; commit ONCE** via
   `autogit.Commit(ctx, paths, "archive: <K> threads (<id-range>)",
   pre=CheckSpineIntegrity, post=CheckDerivedFresh|CheckSpineIntegrity)`.
   - Pre-check `CheckSpineIntegrity` on the OLD state would fail if the spine is already
     drifted — acceptable (don't archive on a broken spine). The derived-fresh post-check
     catches a regen that diverged.
7. **Emit log lines.** Replace `archive.simulated-delete` with `archive.archived`
   (forensic + harness fold key — see §6/§8). Keep `bytes=` for demand-sizing continuity.
   DROP the `archive.warning` "NO recovery path" line entirely.

**Atomicity note.** go-git's worktree commit is a single index→tree→commit operation;
staging all removals + the spine + index + derived files and committing once means the
git HEAD moves atomically. The window of risk is between the spine `WriteSpine` (step 3,
on-disk) and the commit (step 6): a crash there leaves the worktree ahead of HEAD but
internally consistent and fully recoverable by startup reconcile (§7).

### 3.3 Archive index store ops (new, `internal/store`)

A small `archive_index.go`: `LoadArchiveIndex(paths)`, `AppendArchiveEntries(paths, []ArchiveEntry)`
(merge + sort by thr_id + atomic temp-rename write), `FindArchiveEntry(paths, thrID)`.
`ArchiveEntry` domain type lives in `memops` (substrate-agnostic, per ARCHITECTURE.md
port policy): `{ThrID, CommitHash, TreeHash, ArchivedAt, OriginalPath, SpineSummary,
Anchors, Project, RecoveredAt (zero until recovered)}`. Add `PersonantPaths.ArchiveIndex`
= `Home/archive/index.jsonl` and create `archive/` in `store.Init`.

---

## 4. Performance — the per-turn latency climb

Observed 33→37ms across the 7d→30d ladder. The stub's per-thread `RemoveSpineRecord` is
a full O(spine) read+rewrite, and a drain archives K threads ⇒ O(K·spine) rewrites per
drain, K growing with cardinality. The batch design collapses this to **one** spine
read+rewrite per drain (step 3) and **one** commit per drain (step 6). go-git commit
cost is real but paid once per drain, not per thread.

**Net:** the drain goes from O(K·spine) spine rewrites + 0 git, to O(spine) spine rewrite
+ O(1) commits + the unavoidable O(total worktree) cost of one go-git commit. The go-git
commit walks the staged index; for a large home tree this is the new dominant term and
must be measured on the ladder — but it is paid once per drain (drains are infrequent:
archival only arms above `archiveHighWater`=200). **Acceptance criterion AC4 makes this
measurable** rather than assumed.

---

## 5. Required autogit extensions

The autogit doc (autogit.go:144–153) already anticipates this: "For archive recovery —
which needs *specific* verification of the restored blob hash — build a purpose-specific
wrapper that composes Checkout with its own post-checkout verification." Extend, don't
fight, that pattern.

- **E1 — recursive staged removal.** `autogit.Add` stages additions/modifications via
  `wt.Add`/`AddWithOptions{All}`. There is no staged *removal* of a tracked directory.
  Add `autogit.Remove(ctx, paths, patterns...)` that stages recursive removal of tracked
  paths (go-git `wt.Remove` per file walked under the dir, or `Filesystem.Remove` +
  re-add-all). Needed by §3.2 step 2. (Alternatively: `os.RemoveAll` the dirs then
  `autogit.Add(".")` with `All:true` — go-git's `All` stages deletions of tracked files.
  This is SIMPLER and likely sufficient; verify `AddWithOptions{All:true}` stages
  deletions. If it does, E1 is just "RemoveAll then Add('.')" with NO new autogit
  function — prefer that. Flagged as the first thing to confirm at implementation.)
- **E2 — directory restore for recovery.** `autogit.Checkout` restores ONE file from a
  commit. Recovery needs the whole `threads/<id>/` subtree. Add
  `autogit.CheckoutTree(ctx, paths, commitHash, dirPath, preFlags, postFlags)` that
  tree-walks the directory entry in the commit and writes every blob (the natural
  generalization of the existing `Checkout` tree-walk). Recovery composes this with its
  own tree-hash verification in a purpose-specific wrapper (NOT a new GitCheckFlag —
  per the doc's explicit guidance).
- **E3 — tree-hash compute.** A helper to compute the git tree hash of a worktree
  directory (for capture at archival and re-verification at recovery). go-git can hash a
  tree; if exposing this cleanly is awkward, the recovery verification can instead diff
  the recovered subtree against `git show <parent-commit>:<dir>` byte-for-byte. **Tree
  hash is the cleaner invariant; confirm go-git ergonomics at implementation, fall back
  to subtree byte-diff if needed.**

No other autogit surface change. `Commit` already takes the flag pairs the batch needs.

---

## 6. Measurement-honesty resolution (the critical question)

This is the reason #99 is acceptance-blocking, and the part with genuine design contention.

### 6.1 (a) Does opportunistic recall consider archived-recoverable threads?

**Recommendation: NO for v0.1 — archived = off-recall until manually recovered.** The
archive-index anchors/summary are a forensic + `personant archive list/recover` surface,
NOT a live §3.4 recall match surface. Rationale:

- §3.4 recall scans the spine + per-thread frontmatter via `fmCache.LoadAll`. Archived
  threads are off-spine with no frontmatter loaded — folding the archive index into the
  recall scan means a second match surface with different fields (index anchors are a
  *snapshot*, not the evolving `history_symbols`), different scoring inputs, and a new
  "offer a recovery-fetch" ack path. That is a real §3.4 feature, not a stub replacement.
- ARCHITECTURE.md: archival is "deep cold... recoverable by explicit fetch." Explicit
  fetch = the user/CLI recovers it; it is deliberately NOT in the ambient recall loop.
  Making it a live recall surface contradicts the "deep cold" framing.
- The cardinality-pressure trigger archives the COLDEST retired threads precisely because
  they are least likely to be recall targets. Re-surfacing them ambiently re-introduces
  the cardinality pressure archival exists to relieve.

So: **archived-but-recoverable threads are genuinely off-recall in v0.1.** A future v0.2
"archive recall" (match against index anchors → offer recovery-fetch) is a clean addendum
on top of this design (the index already carries anchors + summary for exactly that), but
it is OUT of scope for #99 and should be a separate roadmap item.

### 6.2 (b) What should long-rung recall measurement count? Fate of #96/#97.

Today both workarounds treat archived = unrecallable = *forgiven* (dropped from scoring).
Under recoverable archival that classification is STILL CORRECT for the runtime's *live*
recall numbers — an archived thread genuinely cannot fire `spine.match-fire` (it is off
the spine, off the recall scan, per 6.1(a)). The runtime's §3.4 algorithm is not at fault
for not surfacing it. **So #96/#97 are KEPT, not retired — but REDEFINED:**

- The forgiveness key changes from `archive.simulated-delete` to `archive.archived`
  (the new log line). Same fold mechanism (`h.archivedThreadIDs`), new event name.
- The SEMANTIC label changes from "deleted, gone forever" to "archived-recoverable, off
  the live recall surface." The forgiveness is no longer hiding data loss; it is correctly
  excluding off-recall threads from a measure of *live recall*.
- The "unexplained absence" branch (off-spine, NOT in archive log ⇒ kept as a real miss)
  stays exactly as-is and is now MORE meaningful: with recoverable archival, the ONLY
  legitimate reason a thread leaves the spine is archival, so an off-spine-not-archived
  thread is a genuine integrity bug the measurement should still catch.

### 6.3 (c) New metric: distinguish "recoverable, would-need-fetch" from "genuinely gone"

With the stub, "forgiven" conflated two things; now they separate cleanly:

- **`recall_archived_recoverable`** (counter): expected/target threads excluded from a
  recall score because they are archived-recoverable. This is the honest replacement for
  the old `recall_fidelity_archival_forgiven` — renamed to drop "forgiven" (which implied
  loss) and to assert recoverability. The harness verifies recoverability is REAL by, at
  the final-gauge phase, recovering a sample of archived threads and checking tree-hash
  integrity (drives AC1).
- **`recall_unexplained_absence`** (counter): expected/target off-spine AND not in the
  archive index — the genuine-bug signal (should stay 0).
- **`archive_recovered_verified`** / **`archive_recover_integrity_fail`** (counters): the
  harness drives a recovery-fetch on sampled archived threads (§8 harness path) and counts
  verified vs. integrity-failed recoveries. A nonzero integrity-fail is an acceptance
  FAILURE. This is the metric that turns "recoverable in principle" into "recoverable,
  measured."

**Harness recovery-fetch path (concrete).** Add a final-gauge phase step (sibling of
`peakHistorySymbols`) that, after the ladder run: reads `archive/index.jsonl`, samples N
entries, calls the new `RecoverThread` port op for each, verifies the recovered directory's
tree hash against the index `tree_hash`, asserts the thread is back on the spine with
`state=wip`, records `archive_recovered_verified`. This exercises the recovery path under
realistic accumulated state — the integration-test signal that AC1 demands. It does NOT
need to run every turn; once per ladder run at the gauge phase is the right cost/coverage
point.

---

## 7. Recovery mechanism + integrity verification

### 7.1 Port op

```go
// RecoverThread restores an archived thread from its deletion commit, verifies
// the recovered directory against the stored tree hash, re-adds a fresh spine
// record (state=wip, last_engaged=now), and commits the recovery. Returns
// ErrArchiveEntryNotFound if thrID is not in the archive index, and a distinct
// ErrArchiveIntegrity if the recovered tree hash does not match the index.
RecoverThread(ctx context.Context, thrID string) (SpineRecord, error)
```

### 7.2 Algorithm (adapter)

1. `store.FindArchiveEntry(paths, thrID)` → entry (or `ErrArchiveEntryNotFound`).
2. `autogit.CheckoutTree(ctx, paths, entry.CommitHash, entry.OriginalPath, pre=0, post=0)`
   — restore the subtree from the deletion commit. (The blobs live in the deletion
   commit's PARENT tree; go-git's `commit.File`/tree access reaches parent blobs via the
   commit object graph — confirm the deletion-commit-vs-parent addressing at implementation;
   the deletion commit's tree no longer contains the dir, so recovery must read the
   PARENT commit's tree. Store `parent_commit_hash` in the index if simpler than walking
   parents — flagged as open Q3.)
3. Compute the recovered directory's tree hash; compare to `entry.TreeHash`. Mismatch ⇒
   delete the partial restore, return `ErrArchiveIntegrity` (invariant 2: never write a
   partial thread onto the spine).
4. Build a fresh spine record: reuse `entry.ThrID` (IDs are stable forever — recovery
   does NOT mint a new id), `state=wip`, `last_engaged=now`, anchors/summary from the
   recovered frontmatter (canonical) — NOT from the index snapshot (which may be stale).
   `store.AppendSpineRecord`.
5. `fmCache.Put` the recovered frontmatter.
6. Stamp `entry.RecoveredAt` in the index (entry KEPT as breadcrumb, invariant 4) and
   commit worktree restore + spine + index together via `autogit.Commit(pre=0,
   post=CheckDerivedFresh|CheckSpineIntegrity)`. Derived regen before commit so the
   recovered thread re-enters symbols.jsonl.

### 7.3 Crash safety (relate to #94, don't solve it)

The drain's risk window (spine on disk ahead of HEAD, §3.2) and recovery's window are both
covered by SPEC §4.5.8 startup reconcile, which already "reconciles derived state on open."
Two reconcile obligations this design ADDS, to hand to #94 (don't implement here, don't
fight it):

- **Spine-ahead-of-HEAD on archival:** a thread removed from `spine.jsonl` (on disk) but
  whose removal was not committed ⇒ worktree-vs-HEAD diff on `spine.jsonl`. Reconcile:
  the canonical state is the worktree spine (the drain's intent); re-stage and commit, OR
  `git checkout` the spine back to HEAD if the thread dir is still present. The
  thread-dir-present-but-off-spine case is recoverable either way (data not lost).
- **Index-ahead-of-HEAD:** an `archive/index.jsonl` entry whose deletion commit does not
  exist ⇒ drop the uncommitted entry (the dir is still in the worktree). This is the only
  case that could orphan an index entry, and it is detectable (commit_hash not in
  `git log`) and self-healing (the thread is still on disk + spine).

Crucially: because the deletion commit captures the blob BEFORE the worktree removal is
committed, there is **no window where the thread bytes are unreachable** — the parent
commit always holds them until `git gc` (which §3.8.2 keeps them reachable through).

---

## 8. Module skeleton — what changes

| File | Change | Negative constraint |
|---|---|---|
| `internal/memops/memops.go` | Add `ArchiveThreads`, `RecoverThread` to the port; add `ArchiveEntry`, `ArchiveResult` domain types, `ErrArchiveEntryNotFound`, `ErrArchiveIntegrity`. Keep `ArchiveThread`. | No substrate-leaking names (no `commit_hash` as a port concept beyond the opaque `ArchiveEntry`). |
| `internal/memops/fileadapter/fileadapter.go` | Reimplement `ArchiveThread` as `ArchiveThreads([]{id})` wrapper; implement batch `ArchiveThreads` (§3.2) and `RecoverThread` (§7); first autogit import in the adapter. | Adapter does NOT shell git; does NOT regen derived state per-thread (once per batch). |
| `internal/autogit/autogit.go` | E1 (or confirm `Add(".",All)` stages deletions), E2 `CheckoutTree`, E3 tree-hash helper. | No new `GitCheckFlag` for recovery verification — purpose-specific wrapper per the existing doc. |
| `internal/store/spine_ops.go` | Add `RemoveSpineRecords(paths, ids)` — one read, one filtered rewrite. | Keep single-id `RemoveSpineRecord` for the CLI/wrapper path. |
| `internal/store/archive_index.go` (new) | `Load/Append/Find` over `archive/index.jsonl`; sorted-by-thr_id atomic write. | Index is canonical; never a derived file; never rewritten on recovery except the `RecoveredAt` stamp. |
| `internal/store/paths.go`, `init.go` | Add `ArchiveIndex` path; create `archive/` in `Init`. | — |
| `internal/turn/archival.go` | Drain collects coldest-first slice, calls `ArchiveThreads` ONCE; remove per-thread loop over `ArchiveThread`; keep the post-batch regen ONLY if the adapter does not already do it inside the batch (it now does — so REMOVE the drain's separate regen; flag this dedup). | Drain stays opportunistic + non-fatal; stays GLOBAL scope. |
| `internal/scenarios/recall_fidelity.go` (#97), `harness_run.go` (#96) | Re-key fold from `archive.simulated-delete` to `archive.archived`; rename `recall_fidelity_archival_forgiven` → `recall_archived_recoverable`; keep the forgiveness logic (§6.2). | Do NOT make the archive index a live recall surface (6.1). |
| `internal/scenarios/*` final-gauge | Add recovery-fetch verification phase (§6.3). | Once per ladder run, not per turn. |
| `cmd/archive.go` (new) | `personant archive list` (reads index) + `personant archive recover <thr_id>` (calls `RecoverThread`). | CLI is a thin port caller; no archival logic in cmd. |

---

## 9. Acceptance criteria

- **AC1 (recoverability + integrity).** A thread archived during a ladder run is
  recoverable via `RecoverThread` to a tree-hash-verified byte-exact directory, re-added
  to the spine as `wip`. Harness records `archive_recovered_verified > 0`,
  `archive_recover_integrity_fail == 0` at the gauge phase.
- **AC2 (canonical sorted index, breadcrumb retention).** `archive/index.jsonl` is
  sorted by `thr_id`, one entry per archived thread, and recovered threads RETAIN their
  index entry (with `RecoveredAt` stamped).
- **AC3 (spine consistency / no data loss).** No thread is ever off-spine AND absent from
  the archive index after a committed drain (`recall_unexplained_absence == 0`). The
  deletion commit is reachable in `git log` for every index entry.
- **AC4 (batched cost — latency).** Per-drain spine work is one rewrite (not O(K)); the
  30d→120d ladder shows per-turn latency no longer climbing with cardinality attributable
  to archival (the 33→37ms trend flattens or its archival component is shown bounded).
  Measurable, not assumed.
- **AC5 (measurement honesty).** Long-rung recall is measured against a substrate that
  PRESERVES dormant threads; archived-recoverable threads are correctly excluded from
  *live* recall scores (`recall_archived_recoverable`) and distinguished from genuine
  absence (`recall_unexplained_absence`).
- **AC6 (determinism intact).** The sim's logical-clock determinism is preserved: git
  commit timestamps use `clock.Timeline()` (as eventlog/RecordFileCommit already do), and
  the autogit author signature is deterministic in test (`store.CommitSignature`).
  Confirm go-git commit ordering/hashes do not leak wall-clock into any measured series.

---

## 10. Risks + open questions

- **Q1 — does `AddWithOptions{All:true}` stage tracked-file deletions?** If yes, E1
  collapses to `os.RemoveAll(dir)` + `Add(".")` and no new autogit removal function is
  needed. First thing to confirm; changes the autogit surface.
- **Q2 — go-git tree-hash ergonomics (E3).** If computing a worktree directory's tree
  hash cleanly is awkward, fall back to byte-diffing the recovered subtree against the
  parent commit's subtree. Either satisfies invariant 2; pick at implementation.
- **Q3 — deletion-commit vs parent-commit addressing.** The deletion commit's tree no
  longer contains the thread dir; recovery must read the PARENT. Decide: walk parents at
  recovery, or store `parent_commit_hash` in the index. Storing it is simpler and more
  robust to future history (but adds an index field — minor).
- **Q4 — go-git commit cost at 120d scale (AC4).** One commit per drain walks the staged
  index; if the home tree is large this could itself become a per-drain spike. Mitigated
  by drains being infrequent (arm above 200 spine records) but must be measured. If it
  bites, the fallback is to commit only the changed paths' subtree — a later optimization,
  not a v0.1 redesign.
- **Risk — first autogit caller in the adapter.** Wiring autogit into fileadapter is new
  coupling. It is the correct seam (the adapter owns substrate git), but it means the
  fileadapter unit tests now need a real git repo (they already get one via `store.Init`'s
  `PlainInit`). Existing `TestArchiveThread_*` tests must be rewritten — they currently
  assert deletion; they should assert archive-then-recover round-trips.

### Recommendation: implementable in gated increments — with ONE narrow MAD check.

The mechanism (§3, §5, §7) is a well-bounded reimplementation against an autogit seam
that already anticipated this exact use case; it is implementable in gated increments:
**(I1)** autogit E1–E3 + `store` archive-index/spine-batch ops + paths, with unit tests;
**(I2)** adapter `ArchiveThreads` + `RecoverThread`, round-trip unit test replacing the
deletion tests; **(I3)** drain rewire + dedup the post-batch regen; **(I4)** harness
re-key #96/#97 + new metrics + recovery-fetch gauge phase; **(I5)** `cmd/archive.go`.

The ONE part carrying genuine contention is **§6.1(a) — whether archived-recoverable
threads are a live recall surface.** This design recommends NO (off-recall until explicit
recovery), and that choice propagates into the fate of #96/#97 and the whole measurement
story that makes #99 acceptance-blocking. It is a coherent, defensible call grounded in
the "deep cold / explicit fetch" framing — but it is exactly the kind of decision the
asymmetric-cost discipline says to prove, because it sets what the long-rung recall
numbers MEAN. **Recommendation: a short, scoped MAD on 6.1(a) only** — "should v0.1
opportunistic recall match the archive index and offer a recovery-fetch, or is archival
genuinely off-recall?" — before locking I4. The rest needs no debate. If the MAD affirms
"off-recall" (the likely outcome), I4 proceeds as designed; if it lands on "archive recall
is in scope for v0.1," that is a §3.4 feature addition and a larger increment, and the
index's anchors/summary fields (already in this design) are the hook it would build on.
