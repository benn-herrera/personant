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

**Shared contracts fixed here (R2/R3 both consume; single source):**
- Commit trailer `Personant-Turn: <turn-id>` on per-turn commits —
  recovery reads HEADTURN from it.
- Marker file `$PERSONANT_HOME/op-in-progress.json` (gitignored;
  survives reset): `{op: turn|archival|sleep|recovery, turn?, orig?}`.
- Journal records: JSONL, `{turn, kind: prompt|response, at, bytes}` —
  fsync per append; truncate-to-empty on CommitTurn; torn final line
  skipped by scan.
- Derived watermark `$PERSONANT_HOME/derived-watermark` (gitignored):
  built-from-commit hash; written only by the regeneration path.
- Kill-point seam: named deterministic hooks (`crashpoint.At(name)`,
  test-armed, production no-op) registered at every point the R4 matrix
  kills; a mechanical coverage gate fails the suite if a registered
  point lacks a scenario.

## Wave R1 — substrate primitives (no policy)

`internal/store`: `marker.go` (op-typed read/write/clear, atomic-write
convention), `journal.go` (append/scan/truncate per contract above);
eventlog additive tail-heal (last byte ≠ `\n` ⇒ append one `\n`, never
truncate) + shared tolerant tail-skip reader; `init.go` gitignore
entries (journal, marker, watermark); the crashpoint seam package
(test-armed hooks, no-op in production). Unit tests incl. torn-tail
fixtures. *Opus coder; edit gate; wave checkpoint.*

## Wave R2 — recovery orchestrator + port surface

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

## Wave R3 — turn pipeline + operation re-sequencing

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

## Wave R4 — crash-injection architecture

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

## Wave R5 — SPEC §4.5.8 rewrite + doc sync

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
