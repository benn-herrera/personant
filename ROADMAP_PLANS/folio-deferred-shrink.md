# Folio: deferred shrink after unbind

Status: partly planned, not under way. It lands after the excerpt-folio plan (WP0–WP5: folio
format, tier-transparent reads, settle step B1b, verify checks, measurement) has completed. Where
this plan cites a contract document, it means that document as WP4 leaves it: the folio
mechanism is then recorded in
`PROJECT_DOCS/ARCHITECTURE/storage-and-record-formats/thread-directory-threads-thr-id.md` and in
`PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md`, with the B1b
outcome in SPEC §4.5.8 (5).

## Problem

Unbind moves a re-warmed thread's newest `store.HotExcerptTail` (528) excerpts from its folio
back to loose files at the day barrier. `moveExcerpts` (`internal/store/folio.go`) writes each one
through `store.WriteFileAtomic` (`internal/store/atomic.go`), which calls `File.Sync` on the temp
file. On darwin, Go's `File.Sync` is `fcntl(F_FULLFSYNC)`, which flushes the whole device cache and
takes several ms. At 528 files that is 2–3 s for each re-warmed thread, paid by the turn that
triggers the barrier. Two directory fsyncs follow, then the folio rewrite.

Those syncs exist for one reason. In the same pass, the folio is rewritten without the unbound
entries, so the loose copies have to be durable before the folio drops them. That ordering only
matters for an OS crash or power loss. A process crash loses no page-cache writes, and the crash
matrix (SPEC §4.5.8 (9)) models only process crashes.

## Design

The fix removes the hazard rather than paying to guard it. Unbind writes the loose copies and
leaves their folio entries in place. A folio entry with a loose duplicate is dropped at a later
barrier, once the loose copy has been sealed into a day-commit. At no point is the folio the only
thing that has forgotten an excerpt, so unbind needs no fsync.

### The settle pass

For each live thread, `store.SettleExcerptTiers` runs these steps:

1. **Unbind.** Write each folio-only entry whose target is loose as a loose file. Use temp file +
   rename with no fsync, and no directory fsync afterwards. The folio is not touched.
2. **Folio rewrite**, only when there are binds or drops. The new folio is
   (old − dropped duplicates) ∪ bound. Write it with `WriteFileAtomic`, which fsyncs the data before
   the rename, then read it back, as today. If the result is empty, remove the file.
3. **Loose removal**, only when there is something to remove: the loose files of bound entries, and
   loose duplicates whose target is the folio. Fsync the thread directory once before the first
   removal. Remove `turns/` once it is empty.

A name in both tiers with identical bytes is resolved by its target tier:

- **Target folio** (the thread retired again, or the excerpt fell out of the hot tail): remove the
  loose copy. The folio entry was made durable by the barrier that wrote it.
- **Target loose:** drop the folio entry only when it is *droppable* (next section). Otherwise both
  copies stay.

Bytes that differ remain a conflict, exactly as today: nothing moves, the skip is logged, and
readers use the loose copy.

### Droppability

A folio entry is droppable when all three of these hold:

- (a) its loose copy was already present when this pass listed `turns/`;
- (b) the loose bytes equal the folio entry's bytes;
- (c) primary HEAD's tree holds a blob at `threads/<id>/turns/<name>` whose hash equals the hash of
  the loose bytes.

Just before the folio rewrite, each kept loose copy gets a plain `fsync(2)` (`syscall.Fsync`, not
`F_FULLFSYNC`). The rewrite's own `F_FULLFSYNC` then flushes the device cache, so the copies are on
media before the rename that forgets them.

Each part of the condition carries specific weight:

- (c) is "sealed into a day-commit", and it can be checked from git state at settle time. During
  B1b, primary HEAD is the previous day-commit, or a B1 archival commit that was staged before B1b
  ran. So a copy written in this barrier is never sealed, including when completion re-runs B1b
  after a crash. A crash anywhere in barrier N therefore produces the same day-commit N tree as a
  crash-free run (invariant 5).
- (a) covers a case (c) cannot see. If a sealed loose copy is deleted by hand during the day, the
  next unbind writes it again. HEAD still matches it, but this pass's write is not durable.
- (b) keeps the existing conflict rule. A copy hand-edited after sealing differs from the folio and
  is never dropped.
- The plain fsync turns "the copy is a barrier-interval old and has been written back" from an
  assumption about timing into a guarantee the machine provides. Two day barriers can sit seconds
  apart: a poll at 23:59 on day N+1 seals day N, and a poll at 00:00 seals day N+1. On a day-old
  file, the fsync finds no dirty pages. See open point 2.

Options rejected:

- **Whole-directory tree-hash equality**, comparing `autogit.TreeHashAt` against
  `autogit.WorktreeTreeHash` on `turns/`. Any turn appended today breaks the equality, and a
  re-warmed thread is one that is being used, so it would almost never drop.
- **"Never drop in the pass that wrote the copy" alone, with no git check.** It is enough against a
  process crash. But after a crash, completion's re-run would drop in the same barrier, which makes
  day-commit N depend on whether a crash happened, and it would drop before the bytes are in primary
  history.
- **A per-entry `unbound_day` mark in the folio line.** It needs no git read, but it costs a format
  key, a folio rewrite on every unbind pass, and a rule for marks removed by hand.

### Bind

Bind keeps its ordering and syncs: folio data fsync, then thread-directory fsync, then loose
removals. These syncs close a real hazard. Bound excerpts are often unsealed, because a thread
created and retired today, or one more than 528 turns past its tail today, has excerpts that have
never been in a day-commit. If a loose unlink became durable before the folio's rename, a power
loss would lose those bytes outright: the daily repo is nuked at B4, and go-git does not fsync it
anyway. The cost is two `F_FULLFSYNC` per binding thread, not per file.

Deferring loose removal until sealed would remove these syncs too, but it would put every
retiring thread's loose files back into the next day's index. That works against the folio plan's
outcome, which is an index entry count that does not grow.

### Write primitive

Unbind's writes must be atomic against process death. A plain `os.WriteFile` that is killed between
its truncate and its write leaves a short loose copy. That copy reads as a conflict, readers serve
it, and nothing repairs it.

Incumbent search: `git grep -n 'os.Rename(' -- '*.go' ':!*_test.go'` returns five sites. Only
`measure.writeFileAtomic` (`internal/recall/measure/veccache.go:415`) does temp + rename without
fsync. It names its temp `.tmp-*` through `os.CreateTemp`, which recovery's residue sweep does not
recognise: `internal/recovery/logs.go:218` matches `strings.HasPrefix(d.Name(), "tmp-")`. It also
lives in a package `store` must not import. The variant is therefore an unexported no-sync sibling
in `internal/store/atomic.go` that shares `WriteFileAtomic`'s body: same `tmp-<base>` temp, same
two `store.WriteFileAtomic.*` crashpoints, no second copy of the sequence.

### Code placement

- `internal/store` stays git-free. `SettleExcerptTiers` gains a parameter
  `sealed func(name string, content []byte) bool`. A nil value means nothing is sealed, which is
  what store tests and any caller without git get.
- `SettleOutcome` gains `Dropped`. `Unbound` now counts only loose copies written.
- `internal/autogit` owns git hashing. Add a subtree listing next to `TreeHashAt`
  (`autogit.go:524`) that returns name → blob hash for one directory at one commit; an absent
  directory gives an empty result, not an error. Factor the open/commit/tree/`FindEntry` prelude
  so the two functions share it. Add a blob-hash helper that `hashDirTree`'s inline
  `plumbing.ComputeHash(plumbing.BlobObject, data)` (`autogit.go:603`) also uses.
  Incumbent search: `grep -n '^func ' internal/autogit/*.go` shows `TreeHashAt` (subtree hash only,
  no entries), `CheckoutTree` (writes the worktree) and `WorktreeTreeHash` (worktree side). None of
  them lists the blobs at a commit.
- `internal/memops/fileadapter`, in B1b's `settleExcerptTiers` (added by WP4): read primary HEAD
  once per barrier (`autogit.HeadHash`, already used at `fileadapter_barrier.go:433`). For each
  thread, build a predicate that loads that thread's `turns/` listing on first call only, so a
  thread with no target-loose duplicates costs no git read. `barrier.folio-settled` gains
  `dropped=`.

### Revised invariants

The folio invariants are recorded in the thread-directory ARCHITECTURE document after WP4.
Invariants 1, 2, 4, 7 and 8 are unchanged. The revised texts are:

- **3. Never neither, never twice.** At every instant, including any crash point, each excerpt
  name is readable from at least one tier. Readers return each name once: when a name is in both
  tiers, they read the loose copy. Unbind never removes a folio entry. A folio entry whose target is
  loose leaves the folio only after its loose copy is sealed (invariant 6).
- **5. Placement is a pure function of canonical state.** The target tier of each excerpt depends
  only on the spine record's state and the thread's set of excerpt names. Whether a duplicate folio
  entry is dropped depends also on primary HEAD's tree, which is canonical history. Threads are
  processed in ascending id order and entries in ascending (number, name) order. Folio contents,
  order and barrier commits are therefore deterministic (SPEC §1.5), and a crash inside barrier N
  leaves day-commit N identical to a crash-free run.
- **6. Settle converges from any crash state.** Rerunning settle finishes the moves. A name in both
  tiers with identical bytes is either an unfinished move or an unbound excerpt waiting to be
  dropped. When its target is the folio, settle removes the loose copy. When its target is loose,
  settle drops the folio entry only if the loose copy predates the pass and is held with the same
  blob at its loose path in primary HEAD. Otherwise both stay, and a later barrier drops the entry.
- **9 (new). Syncs order only what can be lost.** A folio rewrite fsyncs its data before the
  rename. Loose files are removed only after a thread-directory fsync in the same pass. Each loose
  copy whose folio entry is being dropped is fsynced before the folio rewrite. Unbind's loose writes
  are atomic against process death and are not synced: nothing depends on their durability.

### Crashpoints

| Crashpoint | Fires (per thread) | Needs |
|---|---|---|
| `folio.settle.midLooseWrite` | after the first unbound loose copy, before the second | ≥ 2 unbinds |
| `folio.settle.postLooseWrite` (renamed from `….preFolioWrite`) | after step 1 | ≥ 1 unbind |
| `folio.settle.postFolioWrite.preLooseRemove` | after step 2's folio write or removal | any bind or drop |
| `folio.settle.midLooseRemove` | after the first loose removal, before the second | ≥ 2 removals |
| `barrier.postSettle.preDayCommit` | once per barrier, after B1b | always |

The rename is needed because a folio write no longer follows step 1 in general: a thread that only
unbinds writes no folio at all. No point is added. The new crash state, a crash during the drop
pass, is the `postFolioWrite` point reached through a drop. The pre-drop fsync changes no state, so
it has nothing to crash between.

### Read path

No change. `readableExcerpts` already prefers the loose copy for a name in both tiers. After
unbind, the loose count is at least H, so `threadExcerptRefs` window reads never decode the folio.
`ReadThreadExcerpts` decodes a folio that is larger by the duplicates for about a day, and for those
names it reads the same loose files as before.

### Verify

A name in both tiers with identical bytes is now a normal transient state, so it produces no
finding. This replaces WP3's "settle pending" warning, which would otherwise fire on every verify
of a re-warmed thread. Verify still warns on a name in both tiers with different bytes (conflict;
the loose copy is read), and still reports errors for a malformed folio, out-of-order names and
repeated names. Verify still reads loose bytes only for names that are in both tiers. Verify
cannot evaluate droppability, because it has no git or placement input. A duplicate that never
clears is caught by the tests below, not by verify.

### Effects

- **Folio size.** A re-warmed thread's folio keeps the bytes of its newest 528 excerpts for one
  barrier interval: one day while the service runs daily, longer if it does not.
- **Daily index count.** Unchanged from the folio plan. The folio is one entry either way, and the
  loose copies appear at the same barrier as before.
- **Primary.** The unbind barrier writes no new folio blob, because the folio is unchanged. The
  slimmed folio blob lands one barrier later. A thread that re-retires before the drop never
  rewrites its folio, where today it writes two folio blobs. Loose blobs dedupe against earlier
  history wherever the excerpt was ever loose at a day-commit.
- **Remaining `F_FULLFSYNC` on the re-warm path:**
  - unbind barrier: none for a thread that only unbinds;
  - drop barrier: one for the folio rewrite, plus a plain fsync per dropped duplicate (not a cache
    flush; normally finds nothing to write).

  Elsewhere in the barrier, `WriteFileAtomic` callers sync per file, not per excerpt: marker,
  watermark, and B3's derived files such as `internal/index/digests.go:232`. B2's commit is not
  synced, since go-git v5.19.3 `storage/filesystem` has no `.Sync()` call
  (`grep -rn '\.Sync()' "$(go env GOMODCACHE)/github.com/go-git/go-git/v5@v5.19.3/storage/filesystem"`
  returns nothing). The turn that triggers the barrier keeps its own journal and excerpt syncs.

## Failure modes this closes

- Dropping a folio entry in the barrier that wrote its loose copy, including completion's re-run:
  closed by condition (c) and invariant 5.
- Dropping against a copy re-written in this pass: closed by condition (a).
- A torn loose copy after a process kill wins reads: closed by temp + rename (invariant 9).
- Removing bind's syncs together with unbind's: invariant 9 keeps them.
- `store` learning git: closed by the injected predicate.

## Sequencing and touch list

The answers below were recorded against the working tree with `--untracked`, because `folio.go`
was uncommitted WP1 work at the time. Re-run without `--untracked` once WP5 has landed.

- `git grep -n 'cpFolio\|folio\.settle\.' -- '*.go'`: registrations at `internal/store/folio.go`
  :32-35, firing sites at :454, 466, 483 and 489, rows at `internal/store/folio_test.go`:344-348,
  and the coverage map at `internal/scenarios/crash_matrix_test.go`:60-63. WP4 adds
  `fileadapter_barrier_test.go` rows to the map.
- `git grep -n 'SettleExcerptTiers\|moveExcerpts(\|unbindExcerpts(\|bindExcerpts(' -- '*.go'`:
  `folio.go`:310, 335, 341 and 368, test call sites in `folio_test.go`, and WP4's call in
  `fileadapter_barrier.go`.
- `git grep -n 'fsyncDir(' -- '*.go'`: `folio.go`:460, 463 and 480, plus `journal.go`:98 and 256,
  which are untouched.

Steps. Each ends at `just build` plus the touched `just test-run`.

1. **Benchmark first.** Add `BenchmarkFolioSettleRewarm` to `internal/store/folio_test.go`. It has
   two sub-benchmarks: `unbind` (a retired thread of H + 16 excerpts settles warm) and `drop` (that
   result settled again with an all-sealed predicate; this one exists only after step 3). Record
   `just bench ./internal/store 'BenchmarkFolioSettleRewarm'` on the unchanged code.
2. **autogit.** Add the subtree blob listing and the blob-hash helper, and add `TestTreeBlobsAt` in
   `internal/autogit`, which cross-checks against the helper and an absent directory.
3. **Retention semantics, syncs kept.** Change `moveExcerpts` so that unbind keeps the folio
   entries, add droppability and `Dropped`, add the `sealed` parameter, and wire it in B1b. The
   existing unbind syncs stay. This step's tests must pass before step 4.
4. **Remove the unbind syncs.** Precondition: step 3's tests are green. Switch unbind to the no-sync
   variant. Delete the two directory fsyncs after step 1 and make the thread-directory fsync
   conditional on removals. Add the pre-drop plain fsync. Rename the crashpoint.
5. **Verify.** Identical name-in-both produces no finding.
6. **Contract documents** in the same checkpoint:
   - the thread-directory ARCHITECTURE document: retention and the drop condition;
   - the crash-stability ARCHITECTURE document's "Mechanism names" paragraph: the ordering of
     invariant 9;
   - `PROJECT_DOCS/ARCHITECTURE/testing-as-the-lab-bench/event-log-format.md`: `dropped=`;
   - SPEC §4.5.8 (7), only as open point 1 settles it.

Tests to change or add:

- `internal/store/folio_test.go`:
  - `TestFolioRoundTrip`: after full unbind, the folio still holds every bound line. An all-sealed
    settle then empties the folio and removes the file. Reads are unchanged at each stage.
  - `TestFolioPlacement`: the re-warm stage gives `{Unbound: H}` with the folio intact. Settling
    with nil gives no moves. Settling with all-sealed gives `{Dropped: H}`. Re-retiring with the
    duplicates present removes the loose copies.
  - `TestFolioSettleCrashConverges`: unbind rows expect duplicates after re-settle. Add a drop row
    at `postFolioWrite`.
  - New `TestFolioSettleKeepsCopiesWrittenThisPass`: an all-sealed predicate on a folio-only
    re-warm drops nothing. This test must fail without condition (a).
- `internal/memops/fileadapter/fileadapter_barrier_test.go`:
  - `TestBarrier_CrashWalk` `folio/*`: the unbind fixture expects 4 loose files and a folio holding
    4. Add a drop fixture: the unbind fixture completed at `bDay2`, then crashed at `bDay3` on
    `postFolioWrite`, expecting 4 loose files and no folio.
  - New `TestFolioUnbindDayCommitDeterministic`: for each unbind crashpoint and
    `barrier.postSettle.preDayCommit`, the `bDay2` day-commit's `TreeHashAt` of `threads/thr_1`
    equals the crash-free run's.
- `internal/verify/verify_test.go`: WP3's "settle pending" test becomes a no-finding case. The
  conflict test stays.
- `internal/scenarios/crash_matrix_test.go`: rename the map key.

Done-line:

- `just build`
- `just test-run ./internal/store 'TestFolio|TestWriteFileAtomic'`
- `just test-run ./internal/autogit 'TestTreeBlobsAt'`
- `just test-run ./internal/memops/fileadapter 'Barrier|TestFolio'`
- `just test-run ./internal/verify 'TestFolio'`
- `just test-run ./internal/scenarios 'TestCrashPointCoverageGate'`
- `just test-run ./internal/scenarios/sim 'TestSimBarrier2Day'`
- `just bench ./internal/store 'BenchmarkFolioSettleRewarm'`: `unbind` at most 10% of step 1's
  figure on the same machine, and `drop` reported. If `drop` exceeds that same 10% bound, open
  point 2 decides.
- `just DURATION=9d sim`: mean `barrier_duration_ms` not above WP5's after-run (that run's
  directory is in WP5's commit message).
- Checkpoint `just test`, then commit.

## Open points for the user

1. **Power-loss residual for unbind copies.** Without a sync, a power loss within the OS writeback
   window after an unbind can leave a re-warmed excerpt's loose copy torn or empty. Its bytes stay
   intact in the folio and in primary history, and nothing is dropped, because (b) fails. But
   settle reports a conflict, verify warns, and readers serve the torn copy until someone deletes
   it. Today a loose excerpt is never torn, because every write is fsynced.
   *Recommendation:* accept it, and record it in SPEC §4.5.8 (7) "Power-loss — qualified" as
   residual (c), with the remedy (delete the loose copy). The window is seconds, once a day, on
   re-warmed threads only. Closing it would take a plain fsync per copy plus one `F_FULLFSYNC` per
   pass; that cost is unmeasured, and it would bring back part of the barrier cost this plan
   removes.
2. **The pre-drop plain fsync.** It turns a timing assumption into a guarantee, and on day-old
   files it should cost close to nothing.
   *Recommendation:* keep it, provided the `drop` benchmark meets the done-line bound. If it does
   not, remove it and add "two barriers within the writeback window" to residual (c), because the
   bytes would still be in primary history at the loose path.
