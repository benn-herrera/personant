# Crash-stability implementation plan (#94)

**Source design:** MAD three-model debate (fable/opus/gemini), full
structural convergence 2026-07-17 — `mad-design/crash-stability/SOLUTION.md`
(local-only, gitignored audit artifact; this plan excerpts every
load-bearing decision so the repo is self-sufficient). Arbitration
rulings (both closed): journal = **JSONL**, flat top-level
`$PERSONANT_HOME/turn-journal.jsonl`, operational/gitignored; legacy
unstamped archive cells take the **general adopt-forward repair path**
(no special case; population expected-empty under release sequencing).

**Durability bar:** ≤1 turn structural loss under any kill timing;
journaled content bytes recovered from prompt-time onward;
`reset --hard` fires iff the marker is present (never on dirtiness —
markerless-dirty is a legitimate hand-edit, absorbed forward).

**Ruled out permanently (user, 2026-07-17) — excluded, not deferred:**
- **Async/deferred per-turn commit.** A commit lagging its canonical
  writes reintroduces the torn-window ambiguity the marker/commit
  protocol exists to eliminate. Commit ordering is synchronous, always.
- **Loss-bar extension (commit batching / ≤N-turn windows).** The MAD
  SOLUTION named per-N batching as a calibration fallback; closed by
  the user: ≤1 turn is the bar, full stop. Cost reduction comes from
  cheaper synchronous protocol steps (fsync-count reduction, scoped
  staging, day-barrier pruning), never from widening the loss window.

**Amendments (2026-07-17, user-directed):** (a) R3-addendum —
marker-into-journal fold for op=turn, truncate-fsync drop, scoped
per-turn add with full sweep at backstop/barrier, count-triggered gc;
(b) R3b — DUAL-REPO scheme (supersedes the briefly-considered
day-branch squash-merge; ratified 2026-07-17): two git DBs track the
same worktree at different cadences. `.git-daily/` (custom git-dir via
go-git) receives the per-turn commits and is NUKED AND RE-CREATED from
current file state at each day barrier; `.git/` (primary) receives one
day-grain commit at the barrier plus archival commits — so archival
anchors are permanent by construction (no squash pruning, no
keep-commits) and primary stays a single small pack forever
(~365 commits/year). Load-bearing property: THE WORKTREE IS THE TRUTH
and the daily DB is disposable scaffolding — every barrier crash window
recovers by "commit worktree to primary if not yet committed; recreate
daily"; no ordering can lose bytes. Barrier sequence: (1) archival
batch against primary (if pressure), (2) day commit worktree → primary
(`Personant-Day` trailer), (3) rm -rf .git-daily, (4) re-init daily +
initial commit of current state. R3b design pass covers: autogit
dual-handle op-scoping (turn ops → daily; barrier/archival → primary);
recovery-observable mapping across two repos (daily-missing = normal
morning, NOT a crash signature; op=barrier marker cells); derived-
watermark re-anchoring (primary day-commit + intraday delta or content
hash — daily hashes evaporate); morning-init cost measured; optional
mid-day re-baseline knob (recovery needs only HEAD; nuke+recreate at
any commit boundary caps loose-object drift, trading intraday git
archaeology the event log already covers).

**Shared contracts fixed here (R2/R3 both consume; single source;
amended by the R3-addendum — per-turn durability-protocol cost
reduction):**
- Commit trailer `Personant-Turn: <turn-id>` on per-turn commits —
  recovery reads HEADTURN from it.
- Marker file `$PERSONANT_HOME/op-in-progress.json` (gitignored;
  survives reset): **batch ops only** —
  `{op: archival|sleep|recovery, turn?, orig?}`. The per-turn marker is
  FOLDED INTO THE JOURNAL: a NON-EMPTY `turn-journal.jsonl` IS the
  in-flight-turn signal, its first record carrying the turn id
  (`store.JournalOwner`); the first append's existing fsync makes the
  signal durable for free. `store.WriteMarker` refuses `op=turn`; a
  legacy `op=turn` marker file still reads and reconciles.
- Journal records: JSONL, `{turn, kind: prompt|response, at, bytes}` —
  fsync per append; truncate-to-empty on CommitTurn (the truncate IS
  the scope release — no separate marker clear); torn final line
  skipped by scan. The truncate carries NO fsync: a power-loss
  resurrection of a stale-but-truncated journal is cell 5's
  redundant-journal case (turn == HEADTURN), and the next turn's first
  append fsync makes the truncate durable before any new canonical
  dirt can exist (see store.TruncateJournal's proof comment +
  TestCell5_ResurrectedTruncatedJournal).
- Per-turn staging is SCOPED (R3-addendum item 3): CommitTurn stages
  only the turn's recorded write set (spine.jsonl, touched thread
  files, the day's event log — recorded at each adapter write site,
  never guessed) via `autogit.AddPaths`, O(write-set) with no tree
  walk. The DAY-grain full `Add(".")` sweep is the session-close/
  backstop Checkpoint's job AND a day-barrier responsibility —
  TODO(R3b): give the day barrier an explicit full-sweep Checkpoint so
  multi-day sessions absorb hand-edits at day close, not only at
  session close. Consequences: hand-edits are absorbed at the next
  FULL sweep, not the next turn; markerless-dirty stays never-reset;
  recovery's untracked-debris identification is heuristic bounded by
  quarantine, no longer proof-clean (sweepUntrackedDebris honesty
  note).
- Sleep-cycle gc fires on the day-off cadence AND on a loose-object
  count threshold (`gcLooseObjectThreshold`, checked every
  `gcCheckEveryCommits` per-turn commits — R3-addendum item 4).
- Derived watermark `$PERSONANT_HOME/derived-watermark` (gitignored):
  built-from-commit hash; written only by the regeneration path.
- Kill-point seam: named deterministic hooks (`crashpoint.At(name)`,
  test-armed, production no-op) registered at every point the R4 matrix
  kills; a mechanical coverage gate fails the suite if a registered
  point lacks a scenario.

## Wave R1 — substrate primitives (no policy) — DONE (55f767c, with R2)

`internal/store`: `marker.go` (op-typed read/write/clear, atomic-write
convention), `journal.go` (append/scan/truncate per contract above);
eventlog additive tail-heal (last byte ≠ `\n` ⇒ append one `\n`, never
truncate) + shared tolerant tail-skip reader; `init.go` gitignore
entries (journal, marker, watermark); the crashpoint seam package
(test-armed hooks, no-op in production). Unit tests incl. torn-tail
fixtures. *Opus coder; edit gate; wave checkpoint.*

## Wave R2 — recovery orchestrator + port surface — DONE (55f767c)

`internal/autogit`: `ResetHard(ctx) (revertedPaths, err)` — the single
new git verb; reverted paths keep derived rebuild O(changed).
`internal/recovery` (new): observables (MARKER / worktree-dirty /
HEADTURN-from-trailer / unstamped-ARCH post-normalization / watermark),
the 12-cell state machine + orthogonal log-heal (cells excerpted in the
SPEC rewrite, R5; the SOLUTION table is authoritative until then),
phase sequencing, `RecoveryReport`. Core rules: cell-3 markerless-dirty
NEVER resets; cell-4 torn turn = snapshot `logs/` beyond HEAD →
ResetHard → sweep untracked canonical-namespace debris (excluding
`logs/`) → re-append log bytes → preserve+surface journal → clear/
truncate; cell-9 unstamped repair = locate deletion commit (child of
stored `ParentCommitHash` whose tree drops `original_path`), stamp, else
`recovery.unrepairable` refused-never-guessed; cell-11 re-entrant
recovery idempotent-converges; cell-12 = verify-gated adopt-commit +
general repair + full rebuild + watermark. `internal/memops` +3 port
methods: `Reconcile(ctx) (RecoveryReport, error)`,
`JournalTurn(ctx, turnID, kind, bytes)` (first call sets marker as side
effect — no Begin/End bracket), `CommitTurn(ctx, turnID, reason)`
(commit + marker-clear + journal-truncate); fileadapter implementation;
`cmd`/`chat`: Init → Reconcile → LoadSession, report banner,
refuse-to-open on unreconciled. Cell-driven unit tests on synthetic
homes (every cell constructed on disk → Reconcile → assert; double-
reconcile idempotence). *Fable coder; ADVERSARIAL REVIEW (the
marker-gated reset is the single most correctness-critical rule);
wave checkpoint.*

## Wave R3 — turn pipeline + operation re-sequencing — DONE (1d76780 R3+addendum; 7ce5aef R3b dual-repo day barrier)

`internal/turn`: journal prompt (fsync) before the model call; journal
response before canonical writes; `CommitTurn` at close with the turn
trailer; journal-append failure aborts the turn pre-canonical;
CommitTurn failure leaves the marker set (fails loudly into recovery).
Archival re-sequenced strictly BETWEEN turns under its own
`op=archival` marker scope — never inside a turn's window; sleep-cycle
ops (`RebuildTrees`, gc) under `op=sleep`. §3.11 session-close
Checkpoint demoted to belt-and-braces backstop (normally no-op).
REPL: recovery banner + preserved-content surfacing (recovery artifact
+ announcement; NEVER auto-replay into canonical — a turn is bytes plus
non-replayable derivation). Instrumentation: per-turn commit latency +
loose-object growth gauges (the cost-accounting measurement plan; sim
reads them; gc interplay is a watch metric). *Fable coder; adversarial
review; wave checkpoint.*

## Wave R4 — crash-injection architecture — DONE (36db9e2)

Arm the R1 seam: kill points through `WriteFileAtomic` (torn-write
variant), `JournalTurn`, per-turn commit, each archival commit
(capture/deletion/stamp), derived rebuild loop. Harness:
`RestartWithCrash` sibling of `RestartSession` (discard in-memory state,
drive `Reconcile` against the on-disk result). Matrix: every W-TURN
ordering/prefix (pre-marker → post-truncate, 7 named points + torn
write); W-ARCH between each commit; W-LOG torn event-log line + torn
journal record (heal + no-merge assertions); W-SLEEP mid-gc/mid-rebuild;
cell-12 legacy fixtures; every cell asserts: full invariant suite + a
STRENGTHENED `VerifyThreadMetaMatchesSpine` (spine↔frontmatter
`turn_count`/`state` agreement — currently blind to the torn-turn
signature) + ≤1-turn-loss vs pre-crash oracle + exact journal-byte
recovery + recall-completeness consistency + idempotent second
reconcile. Mechanical coverage gate: registered-point-without-scenario
fails the suite. Suite-budget rule: the matrix is unit-grade (synthetic
homes, no sim rungs) and must stay inside the 30m checkpoint budget; a
mid-run-crash SIM rung joins as opt-in (`PERSONANT_SLOW_SIM_TESTS`)
with a 1d default-suite variant only if it fits the budget. *Opus
coder; wave checkpoint.*

## Wave R5 — SPEC §4.5.8 rewrite + doc sync — DONE (docs-only, this pass)

Rewrite §4.5.8 from the converged design (state machine table, journal
carve-out — operational class yet durability-load-bearing in the
journal→commit window, marker/watermark/trailer contracts, cell-12
sentence + expected-empty note per the arbitration ruling); §2.1 layout
entries; §2.8 `recovery.*` event rows; §3.11 cadence update (per-turn
commit supersedes; structural commits absorbed); ARCHITECTURE mechanisms
entry + anti-pattern note (never gate reset on dirtiness); AGENTS.md
repo-state. Docs-only ⇒ commits under the mechanical-diff exception
(edit gate). *Opus coder.*

## Process

Sequential R1→R2→R3→R4→R5 (R4's seam points are laid down in R1–R3 as
the code is written; R4 arms and exhausts them). Edit gates per item;
checkpoint gate per wave commit (R5 exempt, docs-only). Reviews on R2
and R3. Cost data to watch across R3/R4: per-turn commit latency,
loose-object accrual vs sleep-gc cadence, clean-open O(1) verification.

## Carry-forward (post-#94, honestly open)

- **Intra-day derived-watermark question (R4-flagged).** The turn path
  never stamps the watermark; the barrier owns freshness (§4.5.8 (7)).
  This is STATED design behavior, not a gap — but if a future need
  wants clean-open O(1) *within* a day after a markerless hand-edit
  (rather than deferring to the next full sweep), revisit whether an
  intra-day watermark stamp is worth its cost. Parked deliberately.
- **§6.2 mid-day re-baseline arming.** The recovery side is wired
  (`op=rebaseline` marker in the known-op set + `decodeOrig`; a crash
  routes to `rm -rf .git-daily` + morning-init, `CellRebaseline`). The
  *producer* — the mid-day trigger that arms the knob when daily
  loose-objects cross a threshold and no barrier is imminent — is NOT
  yet wired. Default-off by design; wire only if the pathological
  long-single-session case shows up in practice.
- **Crash-injection fixtures (i)/(j) refinements** (dual-repo-barrier.md
  §7): the persistent-verify-failure fixtures (spine-breaking overnight
  hand-edit; derived corruption forcing a B6 assert failure) exercise
  the quarantine-and-proceed / refuse-to-open path. If real-use surfaces
  additional persistent-failure shapes, extend these fixtures rather
  than widening measurement-side forgiveness.
- **Multi-day mid-barrier-crash SIM rung.** R4 shipped the unit-grade
  matrix + a 2-day barrier-crossing rung (`TestSimBarrier2Day`, default
  suite). A deeper multi-day mid-barrier-crash sim rung remains opt-in
  only if it fits the budget (dual-repo-barrier.md §7 suite-budget).
