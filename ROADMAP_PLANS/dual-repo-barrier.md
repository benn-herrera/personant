# Dual-repo day barrier (R3b) — implementation-ready design

**Status:** design, pre-implementation — adversarially reviewable, decision-complete. **Supersedes /
amends:** the single-repo recovery scheme in `mad-design/crash-stability/SOLUTION.md` and
`design/crash-stability-implementation.md` (R3b amendment block). Consumes the R3-addendum op=turn
contract (amendment (a): marker folds into journal presence for op=turn; scoped per-turn add; full
sweep at backstop/barrier; count-triggered gc). **Ruled out permanently (do not reopen):**
async/deferred per-turn commit; loss-bar extension (commit batching / ≤N-turn windows). ≤1-turn
structural loss is the bar, full stop. See §9.

---

## 0. The load-bearing property and the invariants that enforce it

> **THE WORKTREE IS THE TRUTH. The daily DB is disposable scaffolding.** No byte of canonical
> content lives *only* in git. Every canonical byte is a file under `$PERSONANT_HOME`; git is a
> recovery-point index over those files. Therefore any DB — daily *or* primary — can be recreated
> from the worktree, and losing a DB is never losing content.

Two git DBs track the one shared worktree at two cadences:

| DB | git-dir | cadence | role | who writes it |
|----|---------|---------|------|---------------|
| **daily** | `.git-daily/` | per-turn | today's microscope — fine-grained recovery points; NUKED + reborn each day | every mid-day mutation (turn, structural, Checkpoint incl. session-close backstop — F2) |
| **primary** | `.git/` | per-day | career history — permanent archival anchors; ~365 commits/year forever | the day barrier ONLY (B1 archival + B2 day-commit), plus the recovery-repair exemption (cell-9 stamp, cell-12 adopt, F1 roll-forward) — see INV-5 |

The design is correct iff a coder never violates these invariants. Each exists to prevent a specific
way of building a wrong system:

- **INV-1 (no worktree reset off primary).** Archival and the barrier MUST NEVER reset the worktree.
  Their recovery is *re-drive / repair*, never `reset --hard`. *Prevents:* resetting the worktree to
  primary HEAD — which lags by up to a full day — silently destroying the entire day's turns. Only
  op=turn recovery resets, and only ever to **daily** HEAD (≤1 turn).
- **INV-2 (idempotent, guarded day-commit).** The day-commit is guarded by `primary HEAD is a
  DAY-COMMIT (DayCommitMessage shape match) AND its Personant-Day == target day`; if already
  present, it is skipped. The guard is NOT "HEAD carries the trailer" — every primary commit carries
  a trailer (F3), so a trailered-but-non-day-shape HEAD (a roll-forward stamp or an adopt) must NOT
  satisfy it. *Prevents:* barrier re-drive after a crash minting a second day-commit for the same
  day, forking primary history.
- **INV-3 (commit-before-nuke).** `.git-daily` is nuked (B4) ONLY after the current worktree is
  captured to primary (B1 archival capture commit and/or B2 day-commit have landed). *Prevents:*
  nuking the only per-turn record before the day is safe in primary. (Even a violation loses only
  git *granularity*, not content — worktree is truth — but the invariant keeps the ≤1-turn bar
  honest across the barrier.)
- **INV-4 (watermark tracks daily).** The derived watermark is the **daily HEAD hash**, re-stamped
  by (a) derived rebuild and (b) barrier / morning-init re-init (B6). *Prevents:* stamping it
  against primary HEAD (the R2 habit), which would force a spurious full derived rebuild on every
  post-barrier open. The barrier additionally **rebuilds** derived once (B3) before re-stamping —
  the pre-barrier worktree may carry overnight hand-edits that Reconcile's cell 3 deliberately
  deferred (§3), so a re-stamp without a rebuild could certify stale derived.
- **INV-5 (repo op-scoping, §1).** Turn/reset/status/mid-session structural/session-close backstop →
  **daily**. **Primary is barrier-exclusive**, with a single **recovery-repair exemption**. The
  exhaustive set of primary writers is:
  1. the barrier's B1 archival batch (capture / deletion / stamp commits),
  2. the barrier's B2 day-commit,
  3. recovery's cell-9 stamp-repair,
  4. recovery's cell-12 legacy/greenfield adopt-commit,
  5. recovery's F1 mid-archival roll-forward completion (which lands the deletion + stamp commits an
     interrupted B1 never finished). Nothing else writes primary — in particular Checkpoint
     (mid-session and session-close) now targets **daily** (F2), so the "backstop → primary" writer
     of the prior draft is GONE. *Prevents:* per-turn or per-session commits landing in primary,
     bloating it to sub-day granularity and defeating the bounded-primary property.
- **INV-6 (sibling gitdir is ignored, §5).** Both `.git/` and `.git-daily/` are in the managed
  `.gitignore` block. *Prevents:* the daily repo seeing `.git/` (or primary seeing `.git-daily/`) as
  untracked debris and either committing it or having the recovery debris-sweep quarantine a live
  git-dir.

Non-functional acceptance: morning-init is **O(active working-set bytes)** — independent of
career-history length (archival caps the working set) — and clean-open stays **O(1)**, crash-open
**O(changed)**.

---

## 1. Repo roles & op scoping

### 1.1 Operation → DB map (INV-5)

| Operation | DB | Commit trailer | Integrity flags |
|-----------|-----|----------------|-----------------|
| per-turn commit (CommitTurn) | **daily** | `Personant-Turn: <id>` | none (0,0) |
| turn reset (torn-turn recovery) | **daily** | — | n/a |
| worktree status (turn-scope WT observable) | **daily** | — | n/a |
| **day-commit** (barrier B2) | **primary** | `Personant-Day: <N>` | `CheckSpineIntegrity` (post) — **derived-fresh is NOT checked here** (F5); the barrier's B3 rebuild + B6 assert own it |
| archival capture/deletion/stamp (barrier B1) | **primary** | `Personant-Day: <N>` + archival message | existing flags (capture pre = SpineIntegrity; deletion post = Derived\|Spine; stamp post = Spine) |
| legacy/greenfield adopt-commit (cell 12) | **primary** | `Personant-Day: <current day>` — **the sole bootstrap carve-out** (no marker-day, no prior HEADDAY exists) | verify-gated (structural) |
| recovery stamp-repair / roll-forward (cell 9, F1) | **primary** | `Personant-Day:` = HEADDAY-at-repair-time (standalone cell-9 stamp-repair — never advances the day) / marker-day (F1 in-flight barrier/archival roll-forward completion) | recovery precedes validity — no flags |
| mid-session structural (project-create, RecoverThread restore) | **daily** | `Personant-Turn`-less (captured by its own full-sweep Checkpoint commit to daily; folded into the day at the barrier) | none |
| Checkpoint — mid-session AND session-close backstop (F2) | **daily** | — | none (belt-and-braces; normally a no-op — daily is already current at close) |

Rationale for daily = no integrity flags, primary = gated: daily is disposable and per-turn cost
must stay flat (this is the existing CommitTurn `0,0` rationale, generalized). Integrity gating
moves to the **once-a-day** barrier — one full `verify.Verify` (`CheckSpineIntegrity`, on the
day-commit) plus one `index.Check` (`CheckDerivedFresh`, on the B6 assert after the B3 rebuild) per
day instead of per turn. This is the payoff that lets per-turn commits be cheap.

**Uniform primary-trailer invariant (F3).** *Every* primary commit — day-commit, all three archival
commits, adopt-commit, and the recovery stamp/roll-forward commits — carries Personant-Day = the day
being sealed or repaired (marker-day for in-flight barrier/archival completion;
HEADDAY-at-repair-time for standalone stamp-repair; current day only for greenfield adopt
bootstrap). Every one of these writers already knows the current day (the single-clock day index),
so the trailer is free to attach. Consequence: **HEADDAY (the `Personant-Day` of primary HEAD) is
ALWAYS defined** for any non-bare primary, and there is no tag fallback anywhere (§4). The empty-day
case (a barrier that captured nothing) still produces exactly one day-commit, minted with
`AllowEmptyCommits` (§2.2 B2) — so the invariant "exactly one trailered day-commit per barrier,
always" holds with no exceptions.

### 1.2 autogit dual-handle delta

Today every autogit verb hardcodes `git.PlainOpen(paths.Home)` — implicitly `.git`. That is the
single largest structural touch (14 sites in `autogit.go`, 3 in `reset.go`, plus `recovery/logs.go`
and `recovery/stamp.go`). The dual-handle delta:

- Introduce an explicit selector so the target repo is **greppable at every call site**:
  ```go
  type Repo int
  const ( Primary Repo = iota; Daily )
  ```
- A single internal helper resolves it:
  ```go
  // openRepo opens paths.Home's worktree against the chosen git-dir.
  // Primary → <Home>/.git (git.PlainOpen). Daily → <Home>/.git-daily via
  // filesystem.NewStorage(osfs.New(<Home>/.git-daily)) + git.Open(storer, osfs.New(Home)).
  func openRepo(paths store.PersonantPaths, which Repo) (*git.Repository, error)
  ```
  go-git supports a git-dir divorced from the worktree via `git.Open(storer, worktreeFS)`;
  `.git-daily` is the storer dir, `paths.Home` is the worktree. `PlainOpen`/`PlainInit` are NOT
  usable for the daily handle (they assume `.git`).
- Verbs that gain a `Repo` param (thread it as the argument after `paths`): `Add`, `Commit`,
  `CommitWithHash`, `HeadHash`, `HeadTurn`, `Worktree`, `ResetHard`, `GC`, `Tag`. Tree/history verbs
  used only by archival (`Checkout`, `CheckoutTree`, `TreeHashAt`, `ParentCommitHash`,
  `VerifyTreeHash`) bind to **Primary** — either fix the constant internally or take the param and
  let callers pass `Primary` for self-documentation. Recommendation: take the param everywhere for
  uniformity and greppability.
- `paths` gains one field: `GitDaily string // Home/.git-daily`.

**Policy-flag semantics per repo.** `GitCheckFlags` (`CheckDerivedFresh`, `CheckSpineIntegrity`) are
meaningful only for **Primary** ops (day-commit, archival, adopt) — the permanent history must never
immortalize a broken spine or stale derived state. **Daily** commits always pass `0,0`: a daily
commit is a disposable per-turn snapshot, gated by nothing, and its consistency is (re)checked at
the day-commit barrier. `ResetHard` still takes no flags on either repo (reset precedes validity —
the existing rationale), but note it is only ever *called* against **Daily** now (INV-1 forbids
resetting the worktree off Primary).

---

## 2. Barrier sequence & crash cells

### 2.1 When the barrier fires

The day barrier is a normal operation (like archival), fired by the application on **new-day
detection**, not a recovery-only concept. Detection uses the single-clock day index (`SimDayIndex` /
`clock.Timeline()` day): the barrier for day *N* fires when the current clock day exceeds HEADDAY
(the day of primary's HEAD `Personant-Day` trailer). HEADDAY is ⊥ only for a **bare** primary — one
that has never taken any primary commit (greenfield, before the first barrier) — in which case the
barrier fires for the first completed day. Under the uniform-trailer invariant (§1.1) every primary
commit is trailered, so on any non-bare primary HEADDAY is a defined integer read directly off HEAD.
Checked at session open (after Reconcile) and before each turn. In the sim, `OnSimDayClose` is the
trigger; in production, the open-time + pre-turn check.

**The HEADDAY-⊥ wedge is now unreachable (F3).** The prior draft let an empty day produce *no*
day-commit (a bare `personant-day/N` tag instead), and let some primary commits go un-trailered — so
HEADDAY could be ⊥ or stale on a primary that had in fact advanced, wedging new-day detection
(re-fire forever, or fork a second day-commit) and giving recovery a tag-vs-trailer discrimination
it sometimes got wrong (the D4 tag-recovery gap). With every primary commit trailered and every
barrier minting exactly one (possibly empty) day-commit, HEADDAY monotonically tracks the last
barriered day with no tag path to disagree with it — the wedge has no reachable state.

Ordering at open is load-bearing: **Init → Reconcile → (barrier if new-day) → LoadSession.**
Reconcile first repairs any torn last turn against *daily* (§2.4 cell 4), so the barrier day-commits
a **reconciled, consistent** worktree into permanent primary history — never a torn turn.

**New-day poll semantics (no re-fire).** The poll seals only **completed** days — those strictly
before the current clock day; the barrier's target is the day being closed, never the day in
progress. After sealing day N with the clock already at N+1, the poll must not re-fire for N: the
guard is the day-commit-shape predicate for the **target** day (§2.2 B2, INV-2), and once N is a
day-commit the only unsatisfied target is N+1. Targets advance strictly forward, so each completed
day is sealed exactly once.

### 2.2 The ordered steps

Under an `op=barrier` marker (`{op: barrier, day: N, phase: …}`; `day` is load-bearing — recovery
reads it; `phase` is forensic, recovery does not depend on fine phase tracking). The barrier is
**homed in the adapter** (fileadapter), which owns `ArchiveThreads`, both repo handles, and the
day-commit — core recovery only *detects and re-requests* it (§2.6, F4).

- **B0.** Write marker `{op: barrier, day: N}` (`store.OpBarrier`, F6). The no-in-flight-scope check
  runs *before* B0; from B0 on, the op=barrier marker OWNS the whole sequence including the B1 batch
  — B1 does NOT open a nested `op=archival` scope (F4).
- **B1.** *Archival-if-pressure*, against **primary**, via the internal `archiveBatch` variant that
  runs **within the already-open barrier scope** (no nested marker, no refuse-on-existing-marker —
  F4). The batch **records its membership durably before any deletion** by appending the
  archive-index entries (empty `CommitHash`, full `ParentCommitHash`/`TreeHash`/`OriginalPath`/spine
  snapshot) *before* the first `RemoveAll` — this is the F1 batch-membership record (see §2.3 and
  the ordering in the code contradiction note). The **capture commit `Add(".")`s the full current
  worktree into primary**, so after it lands primary already holds the day's cumulative content
  (this is what makes B4's nuke safe, INV-3). Every B1 commit carries `Personant-Day: N`. Skipped
  when spine cardinality is under the low-water mark; then B2 is the sole worktree-capture.
- **B2.** *Day-commit* worktree → **primary** with `Personant-Day: N`. Stages **everything,
  including any overnight hand-edits** Reconcile deferred (F5). Post-flag **`CheckSpineIntegrity`
  only** — NOT `CheckDerivedFresh` (derived may lag the just-staged hand-edits; B3 rebuilds and B6
  asserts). Guarded (INV-2): if primary HEAD is already a day-commit for N (DayCommitMessage shape
  match AND its Personant-Day == N), skip (idempotent re-drive). Uses `AllowEmptyCommits` (F3): a
  barrier that captured nothing since B1 still mints one trailered day-commit, so HEADDAY is always
  defined and there is no tag path (§4).
- **B3.** *Rebuild derived + re-stamp* (F5). Rebuild `symbols.jsonl` / digests from the
  now-committed worktree — absorbing overnight hand-edits and B1's removals — so derived is fresh
  before any freshness assertion. (If B1 ran, its own regen already freshened derived and this is a
  near-no-op; if B1 was skipped, this is where hand-edit drift is reconciled.) This step is
  `RebuildDerived`, which also re-stamps the watermark against the *current* (pre-nuke) daily HEAD;
  the final watermark value is set at B6 against the reborn daily.
- **B4.** `rm -rf .git-daily` (nuke). Idempotent (rm of absent path = no-op).
- **B5.** *Re-init daily*: `git init .git-daily` + baseline commit of the current worktree
  (`Personant-Turn`-less baseline) → baseline hash H_d. Because B3 already made derived fresh on
  disk, the baseline captures fresh derived too.
- **B6.** Stamp watermark = H_d (INV-4); **assert derived-fresh** (`CheckDerivedFresh` — the
  once-a-day `index.Check` moved off B2 per F5); clear marker.

Only B1/B2 mutate primary; B4/B5 are pure daily-DB lifecycle; B3/B6 touch derived + watermark
(operational, disposable). The canonical worktree is untouched by B2–B6 and only reduced (never lost
— bytes in primary) by B1's archival removals.

### 2.3 Per-step crash windows (worktree-as-truth proof)

For each window: no worktree byte can be lost, and the completion action is **roll-forward** — never
a reset (INV-1). The op=barrier marker (with its `day: N`) is the idempotent re-entry token;
recovery re-drives from wherever it finds the sequence and every sub-action is idempotent.

**B1 mid-archival sub-windows (F1).** This is where the prior draft's "reset then
re-archive-as-drift" was wrong. The correct action is to **complete the batch** — the recorded batch
membership (the unstamped archive-index entries, durable *before* the first `RemoveAll`) is the
deterministic worklist, and the capture commit already holds every byte.

| Sub-window | On-disk state | Roll-forward completion |
|---|---|---|
| crash before any `RemoveAll` (membership recorded, or not even that) | capture commit landed (or was empty); no dir removed; spine intact | if unstamped entries exist, complete them (steps below); if not, no deletion began — the batch simply re-forms on the next pressure check. **No half-applied deletion is possible**, because membership is recorded strictly before the first removal |
| mid-`RemoveAll` | some batch dirs removed, uncommitted; spine intact; unstamped index entries present | `RemoveAll` every member (absent dir = no-op), `RemoveSpineRecords`, regen derived, deletion commit, stamp |
| post-`RemoveAll`, pre-spine-rewrite | all batch dirs removed; spine intact | `RemoveSpineRecords`, regen, deletion commit, stamp |
| post-spine-rewrite, pre-deletion-commit | dirs + spine removed, uncommitted; index unstamped | regen (idempotent), deletion commit, stamp |
| post-deletion-commit, pre-stamp | deletion commit landed; index entries unstamped | **do not re-commit** — locate the existing deletion commit via `locateDeletionCommit` (a **child-of-`ParentCommitHash` tree test**, robust to overnight hand-edit dirt) and stamp. **Do NOT discriminate on worktree-clean-vs-HEAD:** a post-crash overnight hand-edit makes the worktree dirty vs HEAD and would be misread as uncommitted removals, minting a **duplicate** deletion commit. This is the exact join point where roll-forward degenerates into the existing cell-9 stamp-repair |
| post-stamp | batch fully archived, committed, stamped | nothing to complete; proceed to B2 |

The completion routine is one function (homed in the adapter, F4): ensure-removed →
ensure-spine-removed → regen → (deletion commit iff `locateDeletionCommit` returns not-found, else
stamp the located one) → stamp. It is safe to run against a batch at any of the sub-windows above
and safe to re-enter.

**Whole-barrier crash windows.**

| Crash window | On-disk state | Roll-forward | Byte-loss proof |
|---|---|---|---|
| before B1 | daily intact (all per-turn commits); primary at day N−1 | re-run barrier from B1 | worktree untouched; daily still holds every turn |
| mid-B1 | see the sub-window table above | complete the batch, then B2–B6 | archived bytes live in the capture commit (the entry's `ParentCommitHash`) |
| after B1, before B2 | primary holds content (capture/deletion/stamp landed); day-id not yet on a day-commit | run B2–B6 | content already durable in primary |
| after B2, before B3 | primary day-committed (`Personant-Day: N`); daily still present; derived maybe stale | skip B2 (primary HEAD is day-commit N); do B3–B6 | everything already in primary |
| mid-B3 (rebuild) | day-committed; derived partially rebuilt | re-run B3 (rebuild idempotent); B4–B6 | derived is disposable; worktree unchanged |
| mid-B4 (nuke partial) | day-committed; `.git-daily` corrupt/partial | `rm -rf` again; B5; B6 | primary complete; daily is disposable |
| mid-B5 (daily half-created) | primary complete; `.git-daily` exists, no valid HEAD | `rm -rf`; B5 again; B6 | nothing committed to a headless daily; worktree is truth |
| after B5, before B6 | daily reborn; watermark stale; marker lingering | stamp watermark = daily HEAD; assert derived-fresh; clear marker | no canonical mutation pending |
| B2 spine check fails persistently | B2's post-flag `CheckSpineIntegrity` fails and still fails across one idempotent re-drive (a spine-breaking overnight hand-edit) | quarantine-and-proceed (§2.7): quarantine the offending paths' bytes, scoped-restore those paths from primary HEAD, re-run B2 once; still failing → refuse-to-open, marker intact | offending bytes preserved verbatim in quarantine; primary HEAD is always spine-good (every primary writer carries the pre-flag) |
| B6 derived assert fails persistently | B6's `CheckDerivedFresh` fails and still fails across one idempotent re-drive (derived corruption a rebuild cannot resolve) | quarantine-and-proceed (§2.7): quarantine offending derived paths, scoped-restore from primary HEAD, re-run B6 once; still failing → refuse-to-open, marker intact | derived is disposable and re-derivable; offending bytes quarantined; canonical worktree untouched |

**op=barrier recovery is a single idempotent routine** regardless of `phase`, executed by the
adapter (F4):
1. Complete any in-flight B1 batch from the unstamped-entry worklist (the sub-window completion
   above).
2. If primary HEAD is not a day-commit for N: run B2 (AllowEmptyCommits, guarded).
3. B3 rebuild derived; B4 nuke; B5 re-init daily baseline; B6 stamp watermark + assert
   derived-fresh; clear the op=barrier marker.

Because every sub-action is guarded/idempotent and the worktree is never reset, re-entry (a crash
*inside* barrier recovery, which leaves the op=barrier marker in place) converges — the cell-11
re-entrancy discipline, carried by the op=barrier marker itself rather than an op=recovery wrapper.

**Kill-timing walks (why detection-vs-guard split does not fork).** Two adversarial timings prove
the day-commit-shape guard (INV-2) never mints a duplicate and never wedges detection:

1. *Morning-N+1 completion of a day-N barrier.* A barrier for day N was interrupted mid-B1 and is
   completed the next morning, with the clock already at N+1. Roll-forward lands the deletion +
   stamp commits — the stamp commit is trailered N but is **non-day-shape**. The INV-2 shape-guard
   therefore does NOT see a day-commit for N, so B2 mints the real day-commit N (marker-day N, from
   the op=barrier marker's `day`). *Then* the new-day check runs and, seeing the clock at N+1 and
   HEAD now a day-commit for N, fires the barrier for N+1 → B2 mints day-commit N+1. Two distinct
   day-commits (N then N+1), no fork.
2. *Adopt-on-day-N then barrier.* A greenfield/legacy adopt lands an adopt commit trailered N but
   **non-day-shape**. When the day-N barrier runs, the shape-guard cannot be satisfied by the adopt
   commit, so a real day-commit N is minted alongside it. No skip, no fork.

### 2.4 Recovery-cell extensions

New observable: **DAILY ∈ {present, missing, half-created}** (`present` = `.git-daily` opens with a
valid HEAD; `half-created` = exists but no valid HEAD; `missing` = absent). **Evaluated defensively
and FIRST, before any daily-repo read (F7):** the DAILY probe attempts the daily handle open; a
**go-git open failure on a corrupt/partial `.git-daily` is that half-created signal, NOT a
propagated error** — a corrupt or half-nuked daily is always a recreate-from-worktree candidate (the
daily is disposable by construction, §9), so it can never wedge an open. Only after DAILY resolves
to `present` does any code read daily HEAD / daily WT. Existing observables (MARKER, WT, HEADTURN,
DERIVED) are **evaluated against the correct repo**:

- **op=turn cells (4/5/6)** — WT dirtiness and HEADTURN are computed against **daily** (turns commit
  to daily). `ResetHard` targets **daily** (≤1 turn). Journal preserve/surface unchanged. If DAILY
  is missing/half while the op=turn signal (a non-empty turn-journal) is present: the turn's
  per-turn record is gone, but its content is in the journal (preserved) and the pre-turn worktree
  is in primary's last day-commit + subsequent daily baseline — treat as cell 6 (nothing landed
  structurally), preserve journal, then fall into morning-init to rebuild daily.
- **cell 1 clean** — no marker, daily WT clean vs **daily** HEAD, daily HEAD == watermark, no
  unstamped (primary), journal empty, DAILY present. O(1).
- **cell 2 derived-stale** — rebuild derived; stamp watermark = **daily** HEAD.
- **cell 3 hand-edit** — markerless-dirty vs **daily** HEAD, watermark present. NEVER reset.
  Absorbed at the next FULL sweep (session-close / backstop Checkpoint, or the barrier's B1/B2 full
  `Add(".")`) — **not** the next daily turn commit; scoped per-turn staging leaves a hand-edit dirty
  and unreset until a full sweep. The unreachability proof holds per-repo: the only writers that
  leave torn canonical state (turn→daily; barrier/archival/adopt→primary) all set a marker first, so
  markerless-dirty is a legitimate hand-edit for **both** repos.
- **cell 8 archival (standalone op=archival)** — **BACKTRACK from R2, now superseded by ROLL-FORWARD
  (F1):** cell 8 never resets the worktree (a `reset --hard` to primary HEAD — a day behind — would
  destroy the day, INV-1). But it also no longer merely "clears the marker and leaves the worktree"
  — that would leave a *half-archived* batch (some dirs removed, spine partly rewritten, index
  unstamped, no deletion commit) permanently torn. Instead: recovery **detects** the in-flight batch
  on op=archival marker presence alone and returns it typed; the **adapter completes it** with the
  same roll-forward routine the barrier uses (§2.3 sub-windows, §2.6). No reset, no
  re-archive-as-drift path. (Standalone archival is rare under R3b — the drain is barrier-homed per
  §3-vs-code contradiction #5 — but the public `ArchiveThreads` still exists, so this path stays
  specified.) See §Retractions.
- **op=barrier cells (new, adapter-homed, F4)** — op=barrier marker present ⇒ core recovery returns
  it typed (`RecoveryReport.Pending`, §2.6) and the **adapter** runs the §2.3 idempotent
  barrier-recovery routine. This subsumes daily-missing-mid-barrier: the DAILY observable is
  irrelevant to classification when the marker is op=barrier (the barrier owns daily's lifecycle
  regardless of its current state).
- **cell 12 greenfield/legacy** — evaluated against **primary** (adopt commits to primary); after
  adopt + stamp-repair + full rebuild, run **morning-init** (create daily baseline) and stamp
  watermark = daily baseline. A greenfield home has no `.git-daily` — morning-init is how daily is
  born.
- **cell 10 sleep** — unchanged; gc now runs per-repo (daily gc is the common case; primary gc
  at/after the barrier). Marker cleared; both self-heal.

### 2.5 daily-missing discrimination (normal morning vs mid-barrier)

This is the deliverable question. **Discriminator: the op=barrier marker, plus primary's
`Personant-Day` (HEADDAY) trailer vs the current day.**

| MARKER | DAILY | watermark | classification | action |
|--------|-------|-----------|----------------|--------|
| barrier | any (incl. corrupt) | any | **mid-barrier crash** | typed to the adapter (§2.6); §2.3 idempotent barrier-recovery |
| rebaseline | any (present/missing/half-created) | any | **mid re-baseline crash** (§6.2 knob) | `rm -rf .git-daily` + morning-init **unconditionally**; no primary touch; clear marker (recreating an already-fresh daily is intentionally accepted — cheap, disposable-daily-consistent) |
| absent | missing | absent | **greenfield / first-ever** | morning-init + full derived rebuild + stamp |
| absent | missing | present | **benign morning** (daily lost/never-reborn; primary + worktree intact) | morning-init; a full rebuild runs iff derived is stale vs the reborn baseline (typically once on the R2→R3b transition, §3), else stamp only |
| absent | half-created/corrupt | any | **interrupted init, no marker** (external kill during a prior morning-init, or a corrupt daily dir) | `rm -rf`; morning-init |
| absent | present | present | normal — fall through to cells 1–6 against daily | — |

The row that makes "**daily-missing is normal, NOT a crash signature**" literally true: *absent
marker + missing daily + present watermark*. It is benign because primary holds the career history
and the worktree is truth; recovery just mints a fresh daily. Daily-missing is *never* a data-loss
signature — that distinction (disposable daily vs catastrophic primary) is the whole point of the
split. **Primary** missing, by contrast, is a hard-fail: refuse to open, surface, never
auto-recreate (career history is irreplaceable).

**Morning-init** (create `.git-daily`, baseline-commit the worktree, stamp watermark = baseline
hash) is exactly B5+B6 in isolation, reused as a recovery/first-open primitive. It runs inside the
adapter's `Reconcile` completion so no code path ever observes a daily-less substrate.

### 2.6 Barrier homing — the recovery/adapter seam (F4)

The barrier and its crash-completion require `ArchiveThreads`, the day-commit, both repo handles,
and daily lifecycle. All of these live at the **adapter** (`internal/memops/fileadapter`).
`internal/recovery` sits *below* the adapter and must not import it (that is the import cycle F4
avoids). So the responsibility splits:

- **Core recovery (`internal/recovery`) DETECTS, never completes.** After reading the marker, if
  `m.Op == store.OpBarrier` or `OpArchival` (on **marker presence alone — not gated on the
  unstamped-entry count**; the completion routine still uses the unstamped worklist to know *what*
  to finish, but detection keys on the marker alone), Reconcile runs only the always-safe phase-0/1
  heal + tmp-sweep, then returns a typed request and the observed marker **left in place**:
  ```go
  // in memops.RecoveryReport
  Pending *PendingCompletion // nil unless a batch/barrier must be finished
  ScopedRestored []string    // paths scoped-restored from primary HEAD under
                             // §2.7 quarantine-and-proceed; empty on the normal path
  // where
  type PendingCompletion struct {
      Kind PendingKind // PendingBarrier | PendingArchival
      Day  int         // barrier target day N (0 for archival)
  }
  ```
  Core recovery does NOT engage an op=recovery marker over op=barrier: the op=barrier marker
  (carrying `day`) is itself the idempotent re-entry token, so a crash during completion re-detects
  and re-requests. Return is `(rep, nil)` — a nil error, because the substrate *will* be made safe
  by the adapter that called Reconcile; a non-nil error stays reserved for "cannot open."

- **The adapter (`fileadapter.Reconcile`) COMPLETES.** It calls `recovery.Reconcile`; if
  `rep.Pending != nil` it runs the matching completion (barrier: §2.3 routine; archival: the batch
  roll-forward tail) and then **re-invokes `recovery.Reconcile` once** to obtain the terminal clean
  classification (now the marker is cleared), merging `CellsHit`. Completion clears the op=barrier /
  op=archival marker only at its final step, so the loop is bounded: a crash mid-completion leaves
  the marker and the *next process* re-enters detection, never an unbounded in-process spin.
  - **(a) Error propagation.** Detection itself returns `(rep, nil)` — a nil error, because the
    substrate *will* be made safe by the adapter. An error from the adapter's *completion*
    (including the §2.7 persistent-failure refuse) propagates as a `Reconcile` error →
    **refuse-to-open**.
  - **(b) Loop bound.** Completion clears the marker **LAST**. So on success the re-invoked
    `recovery.Reconcile` sees **no marker** and classifies terminal-clean — **at most two passes**.
    On a persistent- failure refuse the marker **stays** and the completion returns its error
    **without re-invoking** `recovery.Reconcile` — the next process re-enters detection via the
    surviving marker.

- **Marker-nesting resolution (F4).** `ArchiveThreads` splits into:
  - public `ArchiveThreads` — `checkNoInFlightScope` → `WriteMarker(op= archival)` → `archiveBatch`
    → `ClearMarker` (the standalone contract, unchanged for callers);
  - internal `archiveBatch(ctx, ids)` — the mutation body with **no marker I/O and no scope
    refusal**. B1 and both completion routines call `archiveBatch` directly, *inside* the
    already-open op=barrier (or op=archival) scope. This is an explicit internal variant, **not a
    global bypass** of the scope guard — the guard still fires for every public entry point.

The one substrate concession `RecoveryReport` already makes (opaque commit tokens) is extended by
`Pending` — an opaque "finish this" request, not substrate mechanics. The chat/cmd caller is
unchanged: it still calls the port's `Reconcile` and reads one report.

### 2.7 Persistent verify-failure posture (quarantine-and-proceed)

B2 carries a post-flag `CheckSpineIntegrity` and B6 a `CheckDerivedFresh` assert. Normally an
overnight hand-edit that breaks either is resolved by the barrier itself (B2 stages it, B3 rebuilds
derived). The residual case is a **post-flag failure that persists across one idempotent re-drive**
— e.g. a hand-edit that leaves the spine genuinely broken, or derived corruption a rebuild cannot
fix. The posture is **quarantine-and-proceed**, never a day-destroying reset:

1. **Identify** the offending paths from the verify output.
2. **Quarantine** their current bytes under `recovery/quarantine/<stamp>/` — byte-exact, so the
   hand-edit is preserved verbatim for later inspection.
3. **Scoped-restore** exactly those paths from primary HEAD (a path-scoped checkout — **never**
   `reset --hard`, **never** a whole-worktree restore). If an offending path is **absent from HEAD's
   tree** it is an orphan add (no good version exists): quarantine it and **remove** it — removal
   restores consistency because there is nothing valid to restore to.
4. **Re-run** the failing step (B2 or B6) once.
5. **Still failing** → **refuse-to-open**, marker **LEFT IN PLACE** (it is the idempotent retry
   token), report names the quarantine dir + the offending paths, **no `LoadSession`**.

This is consistent with INV-1: the restore is **scoped to the specific proven-broken paths,
quarantined first**, not a whole-worktree reset that would destroy the day. It is safe because
**primary HEAD is always spine-good** — every primary writer carries the `CheckSpineIntegrity`
pre-flag, so no broken commit can ever land — which makes HEAD a valid source for the scoped restore
of exactly the offending paths.

The completion path is: on a persistent B2/B6 failure the adapter runs the quarantine +
scoped-restore + single re-run above; on success the barrier proceeds; on continued failure the
adapter surfaces refuse-to-open with the marker intact (no `LoadSession`).

---

## 3. Watermark re-anchoring

**Chosen contract: watermark = daily HEAD hash (single value).** Re-stamped by exactly two writers:
(a) `RebuildDerived`, (b) the barrier / morning-init re-init (B5). Read by Reconcile's clean-open
check: `DAILY present ∧ daily WT clean ∧ daily HEAD == watermark ⇒ derived fresh`.

Why this over the alternatives (every "or" resolved):

- **vs. {primary day-commit hash + daily HEAD} pair** — the primary day-commit hash carries **no
  freshness information about derived state**. Derived artifacts (`symbols.jsonl`, digests) track
  *per-turn* canonical content, which lives in *daily*; primary snapshots that only once/day. Adding
  the primary hash buys nothing for the O(1)/O(changed) contract and adds a second value to keep
  coherent. Rejected on structure-is-cost.
- **vs. canonical content hash** — hashing the whole worktree on every open is O(all), defeating
  O(1) clean-open. It re-computes what the daily HEAD tree hash already gives for free. Rejected.
- **vs. rebuild-at-barrier** — the watermark's *value* is still a daily HEAD hash, not a rebuild
  trigger. But the prior draft's claim that the barrier "never changes content, so re-stamp without
  rebuild" is **wrong in one case and is corrected here (F5):** the pre-barrier worktree can carry
  **overnight hand-edits** that Reconcile's cell 3 deliberately deferred (it never rebuilds derived
  on a dirty markerless tree). The barrier day-commits those edits (B2), so derived can be stale
  against the committed tree. The barrier therefore **rebuilds derived once (B3)** — O(active
  working-set), same order as morning-init, once/day — and only then stamps (B6) and asserts
  freshness. When B1 ran, its internal regen already freshened derived and B3 is a near-no-op; the
  rebuild is only load-bearing on the archival-skipped + hand-edit path. This is why B2 carries
  `CheckSpineIntegrity` only and the derived-fresh assertion moved to B6.

**Why daily, not primary (the R2 trap).** Derived tracks per-turn canonical state = daily's
granularity. A clean mid-day shutdown leaves `daily HEAD == last turn == current canonical`;
stamping against daily makes next open O(1). Stamping against primary (R2's `HeadHash(paths)` →
`.git`) would read stale-by-up-to-a-day and force a full rebuild every open.

**Cross-barrier continuity.** B5 mints a new daily baseline hash while the worktree content is
unchanged, so B6 MUST re-stamp watermark = baseline hash; otherwise every post-barrier open sees
`daily HEAD != watermark` and does a spurious full rebuild.

**Migration from the R2 watermark (consistent with §2.5, F7).** R2 stores a **primary** commit hash.
First R3b open: `.git-daily` does not exist ⇒ benign-morning/greenfield path ⇒ morning-init creates
daily and the watermark is re-stamped to the daily baseline. The stale R2 value never equals a daily
HEAD (different repo), so it reads as stale and a full derived rebuild runs on that first transition
(the "iff derived is stale" condition in the §2.5 benign-morning row — on the R2→R3b transition it
is stale, so the rebuild fires exactly once). **No migration code** — the benign-morning/cell-2 path
absorbs it. `store.WriteDerivedWatermark` / `ReadDerivedWatermark` signatures are unchanged; only
the *source* of the hash changes.

**Required code change (surface):** `recovery.RebuildDerived` currently stamps
`autogit.HeadHash(ctx, paths)` = **primary** HEAD. Under R3b it must stamp **daily** HEAD
(`HeadHash(ctx, paths, Daily)`). This is the one-line heart of the re-anchoring.

---

## 4. HEADTURN / trailer mapping

| Trailer | Repo | Reader | Meaning at cell eval |
|---------|------|--------|----------------------|
| `Personant-Turn: <id>` | **daily** commits | `HeadTurn(…, Daily)` → HEADTURN | torn-turn discrimination (cells 4/5/6): `HEADTURN == marker.Turn` ⇒ committed (cell 5); `<` ⇒ torn/uncommitted (cell 4/6) |
| `Personant-Day: <N>` | **every** primary commit (day-commit, all archival commits, adopt, recovery repairs) carries it (F3) | `HeadDay(…, Primary)` → HEADDAY | barrier idempotence guard (INV-2); new-day detection; "is this day already permanent?" |

- **Recovery predicates per repo.** Turn cells read HEADTURN from **daily**. The barrier / new-day
  logic reads HEADDAY from **primary**. A daily commit never carries `Personant-Day`; a primary
  commit never carries `Personant-Turn` (turns don't reach primary except folded into a day-commit's
  tree, not its message).
- **Primary's HEADTURN-equivalent** is **HEADDAY**: primary has no per-turn identity, so "what is
  HEAD's turn?" has no answer on primary — the analogous question is "what day is HEAD?", answered
  by `Personant-Day`. Cell evaluation never asks primary for a turn id.
- Reuse the existing `TurnTrailerKey`/`ParseTurnTrailer` machinery; add `DayTrailerKey =
  "Personant-Day"`, `DayCommitMessage(dayID)`, `ParseDayTrailer`, `HeadDay`. `HeadDay(…, Primary)`
  reads the trailer off primary HEAD — **no tag fallback (F3)**. The empty-day case is handled by
  minting an `AllowEmptyCommits` day-commit (§2.2 B2), so a trailer always exists on any non-bare
  primary; HEADDAY is ⊥ only for a bare (never-committed) primary. A day-commit message helper is
  also the natural place to append the batch/archival trailers so every primary commit path is
  trailered uniformly.
- **Detection vs. idempotence guard must not be conflated.** New-day *detection* reads the
  `Personant-Day` trailer off HEAD **regardless of commit shape** (`HeadDay` works on any trailered
  primary commit — day-commit, archival, adopt, or roll-forward). The INV-2 *idempotence guard* is
  stricter: it fires only on a `DayCommitMessage`-shape HEAD whose `Personant-Day == N`. A
  trailered-but-non-day-shape HEAD (e.g. a roll-forward stamp or an adopt) advances HEADDAY for
  detection but does NOT satisfy the guard, so the real day-commit is still minted.

---

## 5. Gitignore / layout

- **`.git-daily/` and `.git/` both go in the managed `.gitignore` block** (INV-6). git auto-ignores
  only a repo's *own* git-dir: the **daily** handle would otherwise see `.git/` as untracked, and
  **primary** would see `.git-daily/` as untracked — either could commit a sibling git-dir or have
  the recovery debris-sweep quarantine a *live* DB. Add both explicitly to `seedGitignoreBody`
  (primary's `.git/` line is redundant-harmless for primary, load-bearing for daily). This is a
  subtle, critical, easily-missed point — call it out in the SPEC rewrite.
- **`$PERSONANT_HOME` §2.1 layout table** gains a row: `.git-daily/  — operational, gitignored: the
  disposable per-turn recovery DB; nuked + reborn each day barrier; never career history.` Contrast
  the existing `.git/` row (primary, permanent).
- **paths.go:** add `GitDaily string // Home/.git-daily` to `PersonantPaths` and `makePaths`.
- **Single-spelunking note (for humans + the SPEC):** to inspect *career* history, `git -C
  ~/.personant log` (primary, day-grain + archival anchors). To inspect *today's* turns, `git
  --git-dir ~/.personant/.git-daily log` (daily, per-turn, evaporates at the next barrier).
  Primary = the résumé; daily = today under the microscope.

---

## 6. Cost & measurement

### 6.1 Morning-init cost model

Morning-init = `git init .git-daily` + `Add(".")` + baseline commit = hash every tracked
(non-ignored) worktree file + build tree objects + write one commit. **Cost is O(active working-set
bytes), independent of career-history length** — it hashes the *worktree*, not primary history, and
archival caps the working set (spine cardinality high/low-water).

Bounding with current sim-home sizes: post-archival steady state is on the order of low-hundreds of
active threads; each thread dir (thread.md + up to `history.cap-per-thread`=40 turn excerpts +
files.json) ≈ tens of KB; worktree canonical text ≈ single-digit MB. At git SHA-1+zlib throughput
(hundreds of MB/s), that is **well under 100 ms**. A pathological ~100 MB working set → ~1 s.
**Years-scale estimate:** because archival holds the working set roughly constant, morning-init
stays flat across years — a 5-year-old home morning-inits in the same time as a 5-day-old one; only
*primary* grows (~365 day-commits/yr + archival anchors, one small pack).

### 6.2 Mid-day re-baseline knob

Spec'd, **default-off**. A single session that runs across many days without ever hitting a barrier
(no new-day detection firing) would let daily loose objects grow unbounded. The knob nukes+recreates
daily at a mid-day commit boundary when daily's loose-object count crosses a threshold. Safe by
INV-3 + worktree-as-truth: recovery needs only daily HEAD, so nuke+recreate at any commit boundary
is lossless. **Arms** only when: (a) daily loose-object count > threshold AND (b) no barrier is
imminent. Default-off because the daily barrier already bounds loose-object accrual to one day and
the sleep-cycle gc packs within a day; the knob is for the pathological long-single-session case
only. It trades away intraday git archaeology — which the event log already covers (§9).

**Runs under a marker (F7).** The nuke+recreate runs under its **own op**, `store.OpRebaseline` —
NOT op=barrier (which would wrongly invoke day-commit completion; a re-baseline never touches
primary and never advances the day). Recovery routes an op=rebaseline crash straight to morning-init
(recreate daily), with no primary mutation — the same recreate-from-worktree action the markerless
half-created row already takes, but explicitly scoped so a mid-nuke crash is unambiguously a
daily-lifecycle op rather than being inferred. `OpRebaseline` joins the known op set (F6) and
`decodeOrig`'s switch.

### 6.3 Gauges

Carry over (now per-repo where relevant): per-turn commit latency (P50/P99, daily), loose-object
growth rate. **Add:**
- `daily_loose_object_count` — the re-baseline-knob trigger + gc watch.
- `barrier_duration` — B0→B6 wall time (Profiling clock).
- `morning_init_duration` — the §6.1 measured cost.
- `day_commit_bytes` / `day_commit_duration` — primary growth per day.

All ride the existing `metric_keys.go` registry pattern.

---

## 7. Crash-injection deltas (R4)

**New kill points** (register via `crashpoint.Register`, one per step boundary; the mechanical
coverage gate fails the suite if any lacks a scenario).

Barrier top-level: `barrier.preArchival`, `barrier.postArchival.preDayCommit`,
`barrier.postDayCommit.preRebuild`, `barrier.postRebuild.preNuke`, `barrier.midNuke`,
`barrier.postNuke.preInit`, `barrier.postInit.preWatermark`, `barrier.postWatermark.preMarkerClear`.

**B1 archival sub-window kill points (F1 — these are the load-bearing new ones).** `archiveBatch`
gains a kill point at every sub-window boundary in §2.3: `archiveBatch.postCapture.preMembership`
(capture commit landed, index entries NOT yet appended — proves membership-before-deletion holds: no
deletion has begun), `archiveBatch.postMembership.preRemove` (batch membership durable, no dir
removed yet), `archiveBatch.midRemove` (some dirs removed), `archiveBatch.postRemove.preSpine`,
`archiveBatch.postSpine.preDeletionCommit`, `archiveBatch.postDeletionCommit.preStamp`,
`archiveBatch.postStamp`. Each must have a scenario that kills there, then drives the adapter's
completion and asserts a fully-archived, stamped, single-deletion-commit terminal — **and asserts no
re-archive-as-drift and no reset occurred**.

Morning-init: `morninginit.preBaseline`, `morninginit.postBaseline.preWatermark`.

**Fixtures** (mid-barrier on-disk states, constructed directly on synthetic homes, driven through
the adapter's `Reconcile` — the `RestartWithCrash` sibling): (a) primary day-committed, daily NOT
nuked; (b) daily nuked, not re-init (missing); (c) daily half-created (headless); (c′) daily
**corrupt** dir (open fails — F7: must classify half-created, not error); (d) **each** B1 sub-window
from the table above (unstamped entries + partial removals in every combination); (e) benign-morning
(marker absent, daily missing, watermark present); (f) greenfield (no daily, no watermark); (g)
overnight hand-edit present at barrier entry (F5: B2 must stage it, B3 must rebuild, B6 must assert
fresh); (h) mid-re-baseline crash (op=rebaseline, daily half-created — F7); (i) a spine-breaking
overnight hand-edit present at barrier entry (persists past one B2 re-drive — forces the §2.7
quarantine-and-proceed path); (j) derived corruption forcing a persistent B6 `CheckDerivedFresh`
failure. For (i)/(j) assert: the quarantine dir is populated (offending bytes byte-exact), the
offending paths are scoped-restored from primary HEAD, and the barrier either completes on the
single re-run OR refuses-to-open with the op=barrier marker intact.

**Assertions** (every barrier fixture, additive to the existing suite):
- **No worktree byte loss** vs a pre-barrier oracle snapshot (the core bar — INV-1 in test form).
  **No reset occurred** on any barrier/archival fixture (roll-forward only, F1) — `RevertedPaths` is
  **ALWAYS empty** for barrier fixtures. The new report field `ScopedRestored []string` is non-empty
  **ONLY** on the persistent-failure fixtures (i)/(j) — it names the paths scoped-restored from
  primary HEAD under §2.7 quarantine-and- proceed, and stays empty everywhere else.
- **Exactly one** `Personant-Day: N` day-commit in primary after recovery (INV-2 — no duplicate
  day-commit on re-drive); and **HEADDAY == N is defined** with no `personant-day/*` tag present (F3
  — tag path gone).
- **F1 batch completion:** every batch member is fully archived (off worktree + off spine, in the
  index with a non-empty `CommitHash`, reachable in exactly one deletion commit) — never
  half-archived, never re-archived as a drift (empty-TreeHash) entry.
- `daily HEAD == watermark` and DAILY present after recovery (INV-4); **derived-fresh (`index.Check`
  OK)** after the barrier completes (F5).
- Primary holds every day-N canonical byte (capture/day-commit-before-nuke — INV-3).
- ≤1-turn-loss preserved *across* the barrier boundary (a turn torn just before the barrier is still
  ≤1 loss, its journal preserved).
- Idempotent second `Reconcile` converges to the same terminal state.
- `.git-daily` / `.git` never appear as tracked or as swept debris (INV-6 regression guard).
- A **corrupt `.git-daily`** fixture opens cleanly (recreated), never refuses-to-open (F7).

Suite-budget: barrier fixtures are unit-grade (synthetic homes, no sim rungs), inside the 30-min
checkpoint budget. A multi-day mid-barrier-crash sim rung joins as opt-in only if it fits.

---

## 8. SPEC §4.5.8 rewrite deltas (R5)

Changes vs the SOLUTION outline (which was single-repo):

1. **New subsection: Repo roles & op-scoping** (§1 here) — the two-DB model, the op→DB table, the
   barrier-writes-primary rule. This is net-new.
2. **Detection tuple** gains the **DAILY** observable and **HEADDAY**; the clean-open predicate is
   rewritten against *daily* HEAD (§2.4/§3).
3. **State-machine table** gains op=barrier + op=rebaseline cells and the daily-missing /
   half-created / **corrupt** discrimination table (§2.5, F7); **cell 8 rewritten**
   (worktree-preserving **roll-forward completion**, no reset — F1 supersedes the R2 reset
   retraction); turn cells annotated "vs daily"; cell 12 annotated "vs primary + morning-init"; note
   that barrier/archival completion is **adapter-homed** (§2.6, F4) with core recovery only
   detecting + typing `Pending`.
4. **Turn transaction protocol** — CommitTurn targets daily; the integrity gate moves from per-turn
   to the **barrier** (spine on B2, derived on B6 — §1.1, F5).
5. **New subsection: the day barrier** — the **B0–B6** sequence (B3 rebuild + B6 assert are the F5
   additions; B2 is `AllowEmptyCommits`, spine-only per F3/F5), the F1 crash sub-windows +
   roll-forward routine, the recovery/adapter seam (§2.6), the morning-init primitive (§2).
6. **Watermark** — re-anchored to daily HEAD; barrier **rebuilds** derived (B3) then re-stamps (B6);
   migration-from-R2 note (§3). Replaces the "built-from-commit hash" prose (still true, but the
   commit is now daily's).
7. **§2.1 layout** — add `.git-daily/` row; gitignore both git-dirs (§5).
8. **§3.11 cadence** — day-commit supersedes the per-day recovery point on primary; **Checkpoint
   (mid-session AND session-close backstop) → daily** (F2), so primary is barrier-exclusive; the
   backstop stays a "normally no-op" because daily is already current at close.
9. **Honest-coverage tier table** — add the daily-disposability tier: "daily-DB loss is never
   content loss; primary-DB loss is unrecoverable (refuse-to-open)."
10. **§2.8 events** — add `barrier.*` (begin, archived, day-committed, rebuilt, nuked, reborn,
    complete, error), `morning-init`, and `rebaseline` to the recovery/substrate event vocabulary.
11. **Marker plumbing (F6)** — `store.Marker` gains `Op` values `OpBarrier` and `OpRebaseline` and a
    `Day int` field; `WriteMarker`'s known-op set and `decodeOrig`'s switch add both (unknown-orig
    hard-fail stays for genuinely unknown ops — the barrier/rebaseline additions make
    double-crash-during-recovery converge instead of hard-failing). State the exhaustive **INV-5
    primary-writer list** (§0): barrier B1/B2 + recovery cell-9 stamp / cell-12 adopt / F1
    roll-forward — nothing else.

---

## 9. Explicit non-goals

- **Async / deferred per-turn commit** — ruled out permanently (user, 2026-07-17). Commit ordering
  is synchronous, always. Not reopened here.
- **Loss-bar extension (commit batching / ≤N-turn windows)** — ruled out permanently. ≤1-turn
  structural loss is the bar. Cost reduction comes from cheaper synchronous steps (daily = no
  integrity flags, barrier pruning, scoped add), never from widening the loss window.
- **Daily-DB durability guarantees** — the daily DB is **disposable by construction**. It carries no
  durability guarantee of its own: it may be nuked, lost, corrupted, or absent at any time, and
  recovery treats that as benign (recreate it). The durability guarantee lives entirely in the
  worktree (truth) + primary (permanent career history). Do not add code that tries to "protect" or
  "repair-in-place" the daily DB — recreate it.
- **Cross-day turn-grain git archaeology** — primary is **day-grain only** by design; per-turn
  history evaporates at each barrier. Turn-grain forensics across days is the **event log's** job
  (`logs/YYYY-MM-DD.log`), not git. The mid-day re-baseline knob (§6.2) explicitly trades intraday
  git archaeology away for the same reason.
- **Primary auto-recreation** — a missing/corrupt **primary** DB is NOT a daily-style benign
  recreate; it is refuse-to-open + surface. Primary is irreplaceable career history.

---

## Contradictions in current code (surface, do not fix)

1. **autogit hardcodes `git.PlainOpen(paths.Home)` (`.git`)** at ~14 sites in `autogit.go`, 3 in
   `reset.go`, plus `recovery/logs.go:366` and `recovery/stamp.go:127`. No repo selector exists.
   Dual-handle (§1.2) is the largest structural touch.
2. **`recovery.RebuildDerived` stamps watermark = primary HEAD** (`autogit.HeadHash(ctx, paths)`).
   Under R3b it must stamp **daily** HEAD (§3). One-line heart of the re-anchoring; also affects the
   `derived.go` "no recovery point to stamp" fallback (now keyed on daily).
3. **`recovery` cell-8 resets the worktree** (`resetSequence` → `autogit.ResetHard`). Under
   dual-repo this would destroy the day (INV-1). Cell 8 must become worktree-preserving (§2.4).
   BACKTRACK from R2 — see Retractions.
4. **`fileadapter.CommitTurn` does `Add(".")` full-worktree stage and commits to `.git`** (primary).
   Must commit to **daily** with the scoped per-turn add of R3-addendum amendment (a).
5. **`turn.go` step 5d fires mid-turn archival** (`surfaceArchivalCandidates`). R3b re-sequences
   archival to **barrier-only (B1)** so primary is barrier-exclusive (INV-5). The pressure
   computation stays; the drain moves to the barrier.
6. **`fileadapter.Checkpoint` commits to `paths.Home` (`.git`) and is called mid-session**
   (`chat.go:975` project-create; `chat.go:341` session-close; `turn/commands.go:345`). Under **F2
   all Checkpoints target daily** — mid-session structural AND session-close backstop. So
   `Checkpoint`'s `Add`/`Commit` pass `Daily`; **no `Repo` arg or caller split is needed** (simpler
   than the prior draft's primary/daily split). Primary is barrier-exclusive.
7. **`store.init.go` `seedGitignoreBody` ignores neither git-dir explicitly.** Add `.git/` and
   `.git-daily/` (§5, INV-6).
8. **`ArchiveThreads` deletes (`os.RemoveAll`, `fileadapter_archive.go` step 2) BEFORE it appends
   the index entries (step 4).** F1 requires batch membership durable *before* the first deletion.
   The index-entry append (with `ParentCommitHash`/`TreeHash`/`OriginalPath`/spine snapshot, empty
   `CommitHash`) must **move to before the `RemoveAll` loop**. All entry fields are already
   available at that point (`caps` holds the spine snapshot; `captureHash`/`treeHashes` are computed
   right after the capture commit). The `ArchiveThreads` crash-safety doc block (lines ~163–209)
   must be rewritten for the F1 sub-windows.
9. **`autogit.Commit`/`CommitWithHash` reject empty commits** (`git.ErrEmptyCommit`), and callers
   swallow that. B2 needs the opposite — an `AllowEmptyCommits` day-commit (F3) so an empty day
   still mints a trailered commit. Add a variant (or a bool/option) that sets
   `git.CommitOptions.AllowEmptyCommits` (present in go-git v5.19).
10. **`store.Marker` has no `OpBarrier`/`OpRebaseline` and no `Day` field** (`marker.go`);
    `validOpKinds` and `decodeOrig` (`recovery.go:551`) do not know them. Add both ops + `Day int`
    (F6).
11. **`recovery.Reconcile` has no `Pending` return and completes every cell itself;
    `memops.RecoveryReport` has no `Pending` field.** The F4 seam needs core recovery to detect
    op=barrier / mid-archival and return `Pending` typed, with the **adapter** completing (it owns
    `ArchiveThreads` + both repo handles). Add `RecoveryReport.Pending` and the
    adapter-drives-completion loop in `fileadapter.Reconcile`.
12. **`ArchiveThreads` always writes its own `op=archival` marker and refuses on any in-flight
    scope** (`checkNoInFlightScope`). B1 and the F1 completion run *inside* the op=barrier scope, so
    the mutation body must split into an internal `archiveBatch` (no marker I/O, no refusal) called
    by both the public `ArchiveThreads` wrapper and the barrier / completion paths (F4). This is an
    internal variant, not a global bypass.
13. **Primary-bound commits do not carry `Personant-Day`.** The archival commits
    (`fileadapter_archive.go`), the cell-12 adopt (`recovery.go:525`), and the cell-9 stamp
    (`stamp.go:98`) all commit with no day trailer. F3 requires every primary commit to carry
    `Personant-Day` = the day being sealed or repaired (marker-day for in-flight barrier/archival
    completion; HEADDAY-at-repair-time for standalone stamp-repair; current day only for greenfield
    adopt — see F3/§1.1). These commit sites need that day threaded in (the barrier/recovery caller
    knows it).
14. **`recovery` cell 8 (`op=archival`) still resets** via `resetSequence` and then leaves the batch
    for a later re-drive. F1 replaces this with adapter roll-forward completion (contradiction #11
    machinery). The reset call on the op=archival branch (`recovery.go:360-366`) is removed;
    detection returns `Pending` on marker presence alone (op=archival ⇒ `Pending`), not gated on the
    unstamped-entry count. (This is the same reset the R2 retraction already flagged for cell 8 — F1
    supersedes "worktree-preserving but leave-it" with "worktree-preserving AND complete-it".)

## Post-review amendments (2026-07 R3b fixup pass)

Implementation deltas vs the text above, adopted at the review burn-down; invariants unchanged
(INV-2 is strengthened):

1. **B2's spine gate runs on the STAGED tree BEFORE the mint** (the text above says "post-flag").
   The staged tree is byte-for-byte what the commit would capture, so the gate checks the same state
   — but a spine-broken day-commit can now never land, making §2.7's "primary HEAD is always
   spine-good" universal. Consequences: the §2.7 scoped-restore source is plain primary HEAD, no
   parent special-case (step 3 above already said HEAD); the §2.7 repair is a pure worktree action
   with NO repair commit, so a **completed barrier always ends with primary HEAD = the day-commit
   for N** (no same-day re-fire off a trailered-but-non-day-shape HEAD), a **refused open leaves
   primary untouched** (no day-commit/repair accretion while wedged), and exactly-one-day-commit
   (INV-2) holds even across a repair. `barrierTail` keeps a cheap tail expectation check — re-mint
   iff HEAD is non-day-shape at marker-clear time (currently unreachable; guards any future B3–B6
   primary writer).
2. **The §2.3 completion routine re-runs the B1 pressure drain** after completing any in-flight
   batch — the "before B1 → re-run barrier from B1" row, folded into the numbered routine: a barrier
   killed at `barrier.preArchival` must not silently defer the day's archival pressure to the next
   barrier. Gated on the day-commit for N not having landed (post-B2 windows can never have
   candidates, and archival commits atop a landed day-commit would recreate the non-day-shape
   wedge).
3. **`store.Init` births `.git-daily` ONLY alongside a fresh primary** (true greenfield). On an
   existing home a missing daily is the legacy-upgrade shape and stays absent through Init, so the
   §2.5 rows and cell-12's verify-gated adopt discriminate as designed — an Init-minted baseline
   committed un-adopted legacy content into a clean-looking daily, skipping the adopt gate and
   downgrading the benign-morning stamp-only path to a spurious full rebuild.
4. **cell-9 stamp-repair re-entry re-runs the scoped daily absorb** (zero-unstamped path): a crash
   between the index write and the absorb otherwise leaves the stamped index as standing cell-3
   dirt. Idempotent — an index already at daily HEAD is a swallowed empty commit.
5. **`ScopedRestored` covers regeneration for derived paths**: under the §2.7 B6 posture the
   quarantined derived artifacts (gitignored, never in primary HEAD) are made whole by the
   post-quarantine rebuild and recorded only after the re-check passes — restore-from-HEAD for
   canonical paths, regenerate-and-verify for derived ones.

## Retractions (from the R2 dispatched design)

- **Cell 8 "`reset --hard HEAD` → sweep debris".** WITHDRAWN. Reason: under the dual-repo scheme
  archival commits to **primary**, which lags the worktree by up to a full day, so a worktree reset
  to primary HEAD destroys the day (INV-1). Correct approach (F1): cell 8 is not only
  worktree-preserving but **roll-forward completing** — recovery detects the in-flight batch on
  marker presence alone (op=archival ⇒ `Pending`; the completion routine still consults the
  unstamped worklist to know what to finish) and the adapter finishes it (`RemoveAll` → spine →
  deletion commit → stamp; §2.3, §2.6). "Clear the marker and leave the worktree for a later
  re-drive" is INSUFFICIENT — it would leave a half-archived batch (dirs removed, spine partly
  rewritten, index unstamped, no deletion commit) torn indefinitely. The batch-membership record
  (index entries appended *before* the first removal, contradiction #8) is what makes completion
  deterministic. Same priority as a Critical finding.
