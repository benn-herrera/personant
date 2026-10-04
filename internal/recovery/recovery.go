// Package recovery is the startup reconcile orchestrator for the
// personant substrate (#94, SPEC §4.5.8). Every open runs Reconcile
// between Init and the first session read; the clean path is a cheap
// no-op, and every unclean-shutdown shape maps to exactly one cell of
// the recovery state machine below (the MAD-converged design in
// mad-design/crash-stability/SOLUTION.md as amended by
// ROADMAP_PLANS/dual-repo-barrier.md).
//
// # Dual-repo model (#94 R3b)
//
// Two git DBs track the one shared worktree: DAILY (.git-daily,
// per-turn, disposable — nuked and reborn at each day barrier) and
// PRIMARY (.git, day-grain career history — barrier-exclusive with the
// recovery-repair exemption). THE WORKTREE IS THE TRUTH: no canonical
// byte lives only in git, so losing the daily DB is never losing
// content. Losing PRIMARY, by contrast, is unrecoverable career history
// — refuse to open, never auto-recreate.
//
// # The state machine (observables → cell)
//
// Observables at open: MARKER (the in-flight-op signal, op-typed),
// DAILY (present / missing / half-created — probed defensively FIRST,
// before any daily-repo read; an open failure on a corrupt .git-daily
// is the half-created signal, never an error, F7), WT (canonical
// worktree dirtiness vs DAILY HEAD — see the logs/ carve-out below),
// HEADTURN (the turn id in daily HEAD's commit trailer, ⊥ when absent),
// ARCH (unstamped archive-index entries on primary, evaluated AFTER
// worktree normalization), DERIVED (watermark vs daily HEAD).
//
// MARKER has two carriers (the R3-addendum marker-into-journal fold):
//
//   - MARKER(turn,T) := the turn journal is NON-EMPTY and its first
//     record carries turn id T (T=⊥ for a torn/corrupt first record —
//     still an in-flight turn, id unknown, which HEADTURN≠⊥ can never
//     equal, so it classifies as "not committed": cell 4/6, never a
//     false cell 5). (A legacy op=turn marker FILE from pre-addendum
//     code is still honored and cleared.)
//
//   - MARKER(archival|sleep|recovery|barrier|rebaseline) := the
//     op-in-progress.json marker file — batch ops only.
//
//     cell 1  marker absent, daily present+clean, derived fresh → no-op
//     cell 2  marker absent, clean, derived stale      → rebuild derived, stamp daily HEAD
//     cell 3  marker absent, DIRTY vs daily, watermark present → hand-edit: NEVER reset
//     cell 4  op=turn, dirty, HEADTURN≠T               → torn turn: preserve+reset(daily)+sweep
//     cell 5  op=turn, clean, HEADTURN==T              → committed; clear marker, zero loss
//     cell 6  op=turn, clean, HEADTURN≠T               → nothing landed; preserve journal, clear
//     cell 7  cell 4 coinciding with unstamped ARCH    → cell 4 then the stamp pass
//     cell 8  op=archival                              → DETECT ONLY (F1/F4): typed Pending
//     returned to the adapter, which roll-forward COMPLETES the batch — no reset,
//     no re-archive-as-drift, marker left in place for the completion
//     cell 9  unstamped ARCH (any cell, post-normalization) → locate deletion commit, stamp;
//     unlocatable ⇒ recovery.unrepairable, entry stays refused — never guessed
//     cell 10 op=sleep                                 → clear marker; substrate self-heals
//     cell 11 op=recovery (crash during recovery)      → re-run with the original marker; every
//     phase is idempotent, so re-entry converges to the same terminal state
//     cell 12 dirty, NO watermark ever                 → greenfield/legacy: verify-gated
//     adopt-commit to PRIMARY (Personant-Day trailer), stamp repair, full rebuild
//     op=barrier                                       → DETECT ONLY (F4): typed Pending to the
//     adapter, which runs the idempotent §2.3 barrier-recovery routine; the DAILY
//     observable is irrelevant to classification (the barrier owns daily's lifecycle)
//     op=rebaseline                                    → rm -rf .git-daily + morning-init
//     unconditionally; no primary touch (§6.2 knob, F7)
//     morning-init rows (marker absent, daily missing/half-created):
//     greenfield (no watermark)      → [adopt if primary-dirty] + morning-init + rebuild
//     benign morning (watermark)     → morning-init; rebuild iff derived stale
//     interrupted init / corrupt dir → rm -rf; morning-init
//     Daily-missing is NORMAL, never a data-loss signature.
//
// # Cell 3 — why a markerless-dirty tree is NEVER reset
//
// This is the single most correctness-critical rule in the design.
// Canonical text is hand-editable BY DESIGN, so a dirty tree with no
// marker must be treated as a legitimate user edit and absorbed
// forward, never destroyed. That is safe because markerless dirt is
// provably not crash debris: the only writers of multi-file canonical
// state (turn close → daily; archival/barrier/adopt → primary) are
// REQUIRED to open their scope before their first canonical write —
// the turn path by its fsynced first journal append, the batch ops by
// the marker file — and release it only after their commit lands, so
// any crash that could leave torn canonical writes necessarily leaves
// the in-flight signal too. The unreachability proof holds PER-REPO.
// Hand-edits are absorbed at the next FULL sweep (session-close
// backstop Checkpoint or the barrier's B1/B2 full Add(".")), not the
// next scoped turn commit.
//
// # The logs/ carve-out on the WT observable
//
// "Dirty" means tracked changes OUTSIDE logs/. The event log is tracked
// but append-only and is written continuously between recovery points,
// so logs-tail dirt is the steady operating state, not a crash
// signature. Resets still revert logs (they are tracked in daily),
// which is why every reset is bracketed by the preserve/restore pass in
// logs.go: forensic log bytes beyond HEAD survive the rollback.
//
// # Idempotence and re-entrancy (cell 11)
//
// Before its first mutating action Reconcile replaces the observed
// marker with {op: recovery, orig: <observed op>} and clears it only
// after the terminal state is reached. The op=barrier / op=archival
// markers are the EXCEPTION (F4): they are returned typed and left in
// place — the marker itself is the completion's idempotent re-entry
// token, and the ADAPTER clears it as its completion's final step.
//
// # Journal discipline
//
// Recovery never appends to the turn journal — a torn final record
// would be merged into by any append. The journal is scan-then-truncate
// only: well-formed records are preserved to a surfaced artifact
// (artifact.go) and the file is truncated; torn lines are counted and
// reported, never repaired in place.
//
// # Quarantine — recovery never deletes
//
// Every destructive filesystem action is non-lossy: swept files (tmp-
// residue, untracked debris) are MOVED into recovery/quarantine/
// <reconcile-timestamp>/<original-relative-path>, and each dirty
// tracked path's bytes are snapshotted there before a reset reverts
// it. See the quarantine type in logs.go for the full rationale.
package recovery

import (
	"context"
	"fmt"
	"strings"

	"personant/internal/autogit"
	"personant/internal/crashpoint"
	"personant/internal/eventlog"
	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/store"
)

// Cell names reported in RecoveryReport.CellsHit. Stable identifiers —
// tests and forensics key on them.
const (
	Cell1Clean         = "cell-1-clean"
	Cell2DerivedStale  = "cell-2-derived-stale"
	Cell3HandEdit      = "cell-3-hand-edit"
	Cell4TornTurn      = "cell-4-torn-turn"
	Cell5TurnCommitted = "cell-5-turn-committed"
	Cell6TurnNoWrites  = "cell-6-turn-uncommitted"
	// Cell8Archival is appended by the ADAPTER's completion (F1/F4) when
	// it roll-forward-finishes a detected in-flight op=archival batch —
	// core recovery only detects (RecoveryReport.Pending) and never adds
	// this cell itself.
	Cell8Archival     = "cell-8-archival"
	Cell9StampRepair  = "cell-9-stamp-repair"
	Cell10Sleep       = "cell-10-sleep"
	Cell11Reentry     = "cell-11-recovery-reentry"
	Cell12LegacyAdopt = "cell-12-legacy-adopt"
	// CellMorningInit fires whenever the daily DB is (re)born from the
	// worktree: benign morning, interrupted init, greenfield first-open,
	// the op=rebaseline row, and the op=turn-with-daily-missing corner.
	CellMorningInit = "cell-morning-init"
	// CellRebaseline is the op=rebaseline crash row (§6.2 knob, F7).
	CellRebaseline = "cell-rebaseline"
	// CellBarrier is appended by the ADAPTER's completion when it runs
	// the §2.3 idempotent barrier-recovery routine for a detected
	// op=barrier marker (same detect-vs-complete split as Cell8Archival).
	CellBarrier = "cell-barrier"
)

// origNone encodes "no operation marker was present" in the recovery
// marker's Orig field.
const origNone = "none"

// Crashpoints for the R4 matrix — recovery must survive being killed
// inside itself (cell 11). Registered at import time for the coverage
// gate.
var (
	cpResetDonePreSweep = crashpoint.Register("recovery.resetDone.preSweep")
	cpLogsRestored      = crashpoint.Register("recovery.logsRestored.preCleanup")
	cpStampPreCommit    = crashpoint.Register("recovery.stampRepair.preCommit")
	cpAdoptPreCommit    = crashpoint.Register("recovery.adopt.preCommit")
	cpPreMarkerClear    = crashpoint.Register("recovery.done.preMarkerClear")
)

// Reconcile inspects the substrate at paths, repairs any unclean-
// shutdown damage per the state machine above, and reports what it did.
// It assumes Init has already run (idempotent scaffold: git repo,
// directories, gitignore) — the chat/cmd wiring is Init → Reconcile →
// (barrier if new-day) → LoadSession. A non-nil error means the
// substrate must not be opened; the in-progress recovery marker is left
// behind so the next call re-enters and converges.
//
// F4 seam: an op=barrier or op=archival marker is NOT completed here —
// core recovery DETECTS it (on marker presence alone) and returns a
// typed RecoveryReport.Pending with a nil error and the marker left in
// place; the ADAPTER (which owns ArchiveThreads, both repo handles and
// the day-commit) completes it and re-invokes Reconcile once for the
// terminal classification.
func Reconcile(ctx context.Context, paths store.PersonantPaths) (memops.RecoveryReport, error) {
	var rep memops.RecoveryReport
	// One quarantine destination per pass; the directory is created only
	// if something is actually quarantined, so clean opens stay O(1).
	q := newQuarantine(paths, &rep)

	// Phase 0 — orthogonal log-tail heal (additive, never truncates).
	// Runs before anything else writes an event line, so a torn tail can
	// never be merged into.
	healed, err := healLogTails(paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: heal log tails: %w", err)
	}
	rep.LogTailsHealed = healed
	for _, name := range healed {
		logEvent(paths, "log-tail-repaired", "file="+name)
	}

	// Phase 1 — UNCONDITIONAL atomic-write temp-residue sweep. Not gated
	// on any marker cell: WriteFileAtomic does not fsync the parent dir
	// after its rename, so under power loss a tmp- sibling can survive
	// even a shutdown every other observable calls clean.
	swept, err := sweepTmpResidue(paths, q)
	if err != nil {
		return rep, fmt.Errorf("recovery: sweep tmp residue: %w", err)
	}
	rep.TmpSwept = swept

	// Phase 2 — observables.
	m, markerPresent, err := store.ReadMarker(paths)
	if err != nil {
		// A present-but-corrupt marker is never "no op running". Refuse to
		// open; the file is left untouched for forensics.
		return rep, fmt.Errorf("recovery: %w", err)
	}
	records, torn, err := store.ScanJournal(paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: scan journal: %w", err)
	}
	rep.TornJournalRecords = torn

	eff, effPresent := m, markerPresent
	reentry := false
	switch {
	case markerPresent && m.Op == store.OpRecovery:
		reentry = true
		eff, effPresent, err = decodeOrig(m)
		if err != nil {
			return rep, fmt.Errorf("recovery: %w", err)
		}
	case !markerPresent && (len(records) > 0 || torn > 0):
		// MARKER(turn,T) — the journal carrier (see the package doc): a
		// non-empty journal is the in-flight-turn signal, its first record
		// the turn id. All-torn journal ⇒ T=⊥ ("" — which never equals a
		// real HEADTURN, so the turn reads as uncommitted: cell 4/6).
		turnID := ""
		if len(records) > 0 {
			turnID = records[0].Turn
		}
		eff, effPresent = store.Marker{Op: store.OpTurn, Turn: turnID}, true
	}
	// (A legacy op=turn marker FILE — markerPresent with m.Op==OpTurn —
	// falls through as eff=m unchanged: pre-addendum homes still honor it.)

	// F4 detection seam: op=barrier / op=archival are ADAPTER-completed.
	// Detection keys on marker presence ALONE (never on the unstamped-
	// entry count); the marker — carrying `day` — is left in place as the
	// completion's idempotent re-entry token, and the return is (rep, nil)
	// because the substrate WILL be made safe by the adapter that called
	// us. The DAILY observable is irrelevant here: the barrier owns
	// daily's lifecycle regardless of its current state.
	if effPresent && (eff.Op == store.OpBarrier || eff.Op == store.OpArchival) {
		kind := memops.PendingArchival
		if eff.Op == store.OpBarrier {
			kind = memops.PendingBarrier
		}
		rep.Pending = &memops.PendingCompletion{Kind: kind, Day: eff.Day}
		if reentry {
			rep.CellsHit = append(rep.CellsHit, Cell11Reentry)
		}
		logEvent(paths, "pending", fmt.Sprintf("op=%s day=%d", eff.Op, eff.Day))
		return rep, nil
	}

	// PRIMARY must exist and resolve — a missing/corrupt primary is
	// irreplaceable career history: refuse to open, never auto-recreate.
	if _, err := autogit.HeadHash(ctx, paths, autogit.Primary); err != nil {
		return rep, fmt.Errorf("recovery: resolve primary HEAD (did Init run?): %w", err)
	}

	// DAILY observable — probed defensively BEFORE any daily-repo read
	// (F7): an open failure on a corrupt .git-daily is the half-created
	// signal, not an error.
	daily := autogit.ProbeDaily(paths)

	var dailyHead, headTurn string
	dirty := false
	if daily == autogit.DailyPresent {
		dailyHead, err = autogit.HeadHash(ctx, paths, autogit.Daily)
		if err != nil {
			return rep, fmt.Errorf("recovery: resolve daily HEAD: %w", err)
		}
		wt, err := autogit.Worktree(ctx, paths, autogit.Daily)
		if err != nil {
			return rep, fmt.Errorf("recovery: worktree state: %w", err)
		}
		dirty = CanonicalDirty(wt.DirtyPaths)
		headTurn, err = autogit.HeadTurn(ctx, paths, autogit.Daily)
		if err != nil {
			return rep, fmt.Errorf("recovery: read daily HEAD turn trailer: %w", err)
		}
	}
	wm, wmPresent, err := store.ReadDerivedWatermark(paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: read derived watermark: %w", err)
	}
	unstamped, err := unstampedEntries(paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: read archive index: %w", err)
	}

	// Cell 1 — the clean path. No marker (and no re-entry), daily present
	// and canonically clean, derived fresh vs DAILY HEAD, nothing
	// unstamped, journal empty. O(1).
	if !effPresent && !reentry && daily == autogit.DailyPresent && !dirty &&
		wmPresent && wm == dailyHead &&
		len(unstamped) == 0 && len(records) == 0 && torn == 0 {
		rep.CellsHit = append(rep.CellsHit, Cell1Clean)
		return rep, nil
	}

	// Engage: from here on recovery mutates. Replace the observed marker
	// with the recovery marker carrying the original op, so a crash
	// inside any phase below re-enters cell 11 with full context.
	if !reentry {
		if err := store.WriteMarker(paths, encodeRecoveryMarker(eff, effPresent)); err != nil {
			return rep, fmt.Errorf("recovery: engage: %w", err)
		}
	}
	logEvent(paths, "begin", fmt.Sprintf("orig=%s turn=%s daily=%s reentry=%t",
		origLabel(eff, effPresent), eff.Turn, daily, reentry))
	if reentry {
		rep.CellsHit = append(rep.CellsHit, Cell11Reentry)
	}
	if effPresent {
		rep.ClearedOp = string(eff.Op)
		rep.ClearedTurn = eff.Turn
	}

	// Phase 3 — pending preserved-logs restore. If a prior recovery pass
	// crashed between its reset and its log restore, the preserved
	// copies are still staged; restoring them FIRST makes every later
	// phase see the fully-recovered logs regardless of which cell now
	// fires.
	if err := restorePreservedLogs(paths); err != nil {
		return rep, fmt.Errorf("recovery: restore preserved logs: %w", err)
	}

	// Phase 4 — cell dispatch.
	canonicalClean := !dirty
	needMorningInit := daily != autogit.DailyPresent
	switch {
	case effPresent && eff.Op == store.OpRebaseline:
		// §6.2 knob crash (F7): recreate daily from the worktree
		// UNCONDITIONALLY — no primary touch. Recreating an already-fresh
		// daily is intentionally accepted (cheap, disposable-daily-
		// consistent).
		rep.CellsHit = append(rep.CellsHit, CellRebaseline)
		needMorningInit = true
		canonicalClean = true

	case effPresent && eff.Op == store.OpTurn && daily != autogit.DailyPresent:
		// op=turn signal with the daily DB missing/half-created: the
		// turn's per-turn record is gone, but its content is in the
		// journal (preserved below) and the pre-turn worktree is in
		// primary's last day-commit + subsequent daily baseline — treat
		// as cell 6 (nothing landed structurally), preserve journal, then
		// fall into morning-init to rebuild daily.
		rep.CellsHit = append(rep.CellsHit, Cell6TurnNoWrites)
		if err := preserveJournalContent(paths, &rep, eff.Turn, records, q); err != nil {
			return rep, err
		}
		canonicalClean = true

	case effPresent && eff.Op == store.OpTurn:
		committed := headTurn != "" && headTurn == eff.Turn
		switch {
		case committed:
			// Cell 5: the turn's commit landed on daily; the crash hit
			// between commit and journal-truncate (or an unsynced truncate
			// resurrected the just-committed turn's journal after power
			// loss — same observable, same handling). Zero loss; journal
			// is redundant with the commit and is truncated at phase 7.
			rep.CellsHit = append(rep.CellsHit, Cell5TurnCommitted)
			if dirty {
				// Anomalous under the protocol (nothing canonical is written
				// for T after its commit) — so whatever this dirt is, it is
				// not turn-T debris. Treat it as cell 3 dirt: absorb, never
				// reset a committed turn's aftermath.
				rep.CellsHit = append(rep.CellsHit, Cell3HandEdit)
			}
		case dirty:
			// Cell 4 — the core torn turn. Reset targets DAILY (≤1 turn).
			rep.CellsHit = append(rep.CellsHit, Cell4TornTurn)
			if err := preserveJournalContent(paths, &rep, eff.Turn, records, q); err != nil {
				return rep, err
			}
			if err := resetSequence(ctx, paths, &rep, q); err != nil {
				return rep, err
			}
			canonicalClean = true
			logEvent(paths, "rollback", fmt.Sprintf("turn=%s reverted=%d debris=%d",
				eff.Turn, len(rep.RevertedPaths), len(rep.DebrisSwept)))
		default:
			// Cell 6: no tracked canonical write landed (e.g. crash during
			// the model call). Structurally nothing to reset; the journal
			// holds whatever content existed (prompt at minimum). The
			// untracked-debris sweep still runs — twice licensed: (a) the
			// in-flight-turn signal is present, so untracked non-ignored
			// files are candidate in-flight debris (quarantined, never
			// deleted); (b) cell-11 convergence — a cell-4 pass killed
			// after its reset re-enters HERE (tree now clean), and skipping
			// the sweep would leave the first pass's debris behind forever.
			rep.CellsHit = append(rep.CellsHit, Cell6TurnNoWrites)
			if err := preserveJournalContent(paths, &rep, eff.Turn, records, q); err != nil {
				return rep, err
			}
			swept, serr := sweepUntrackedDebris(ctx, paths, q)
			if serr != nil {
				return rep, fmt.Errorf("recovery: sweep debris: %w", serr)
			}
			rep.DebrisSwept = swept
		}

	case effPresent && eff.Op == store.OpSleep:
		// Cell 10: sleep touches only git internals and gitignored
		// caches; git's own gc is self-healing (per-repo — the daily gc is
		// the common case) and the recall cache self-heals on staleness.
		// Any dirt is hand-edit territory — absorbed, never reset.
		rep.CellsHit = append(rep.CellsHit, Cell10Sleep)
		// Sleep runs strictly between turns (like archival), so a non-empty
		// journal is a contract anomaly — preserve it rather than let phase 7
		// truncate it away.
		if len(records) > 0 || torn > 0 {
			if err := preserveJournalContent(paths, &rep, "", records, q); err != nil {
				return rep, err
			}
		}

	case daily != autogit.DailyPresent:
		// Markerless daily-missing/half-created rows (§2.5). Daily-missing
		// is NORMAL — never a data-loss signature; the worktree is truth
		// and primary holds career history. A non-empty journal here would
		// have been caught by the op=turn branch above, so the journal is
		// empty. Three sub-rows, all converging on morning-init below:
		//   - greenfield (no watermark): if the tree carries uncommitted
		//     content vs PRIMARY, adopt it forward first (cell 12);
		//   - benign morning (watermark present): just morning-init;
		//   - interrupted init / corrupt dir: rm -rf + morning-init
		//     (MorningInit nukes first, so no special case).
		if !wmPresent {
			priWT, werr := autogit.Worktree(ctx, paths, autogit.Primary)
			if werr != nil {
				return rep, fmt.Errorf("recovery: primary worktree state: %w", werr)
			}
			if CanonicalDirty(priWT.DirtyPaths) {
				rep.CellsHit = append(rep.CellsHit, Cell12LegacyAdopt)
				if err := adoptLegacy(ctx, paths, &rep); err != nil {
					return rep, err
				}
				// Cell 12 always runs the one-time FULL rebuild: a legacy
				// home's derived state is untrusted by definition. (The
				// watermark stamp happens inside morning-init below; the
				// rebuild here cannot stamp a daily that does not exist yet.)
				if err := RebuildDerived(ctx, paths, index.Options{Quiet: true}); err != nil {
					return rep, fmt.Errorf("recovery: legacy rebuild: %w", err)
				}
				rep.DerivedRebuilt = true
			}
		}
		canonicalClean = true

	case dirty && !wmPresent:
		// Cell 12 with a live daily (an Init-scaffolded but never-
		// reconciled home carrying uncommitted content): adopt forward
		// into PRIMARY, verify-gated, then re-baseline daily off the
		// adopted state (morning-init below) so daily HEAD == current
		// canonical and the watermark is stamped honestly.
		rep.CellsHit = append(rep.CellsHit, Cell12LegacyAdopt)
		if err := adoptLegacy(ctx, paths, &rep); err != nil {
			return rep, err
		}
		// The one-time full rebuild — a never-reconciled home's derived
		// state is untrusted by definition (see the daily-missing arm).
		if err := RebuildDerived(ctx, paths, index.Options{Quiet: true}); err != nil {
			return rep, fmt.Errorf("recovery: legacy rebuild: %w", err)
		}
		rep.DerivedRebuilt = true
		needMorningInit = true
		canonicalClean = true

	case dirty:
		// Cell 3 — the hand-edit. Absorbed forward at the next FULL
		// sweep — the session-close backstop Checkpoint or the barrier's
		// B1/B2 full Add(".") — NOT the next turn: per-turn commits stage
		// only the turn's own recorded write set (R3-addendum item 3), so
		// hand-edits stay dirty, untouched and unreset, until a full sweep.
		// Derived state reconciles on the next trigger. See the package
		// doc for why this must never reset.
		rep.CellsHit = append(rep.CellsHit, Cell3HandEdit)

	case !wmPresent || wm != dailyHead:
		rep.CellsHit = append(rep.CellsHit, Cell2DerivedStale)

	default:
		// Only reachable via re-entry after a prior pass already
		// converged everything; record the clean terminal.
		rep.CellsHit = append(rep.CellsHit, Cell1Clean)
	}

	// Phase 4b — morning-init: (re)birth the daily DB from the current
	// worktree wherever the dispatch above requires it. MorningInit nukes
	// any half-created remnant first, baseline-commits the worktree, and
	// stamps the watermark honestly (rebuilding derived first iff stale
	// vs the reborn baseline — the R2→R3b migration path fires exactly
	// once here).
	if needMorningInit {
		rep.CellsHit = append(rep.CellsHit, CellMorningInit)
		baseline, rebuilt, merr := MorningInit(ctx, paths)
		if merr != nil {
			return rep, fmt.Errorf("recovery: morning-init: %w", merr)
		}
		rep.DerivedRebuilt = rep.DerivedRebuilt || rebuilt
		logEvent(paths, "morning-init", fmt.Sprintf("baseline=%s rebuilt=%t", baseline, rebuilt))
	}

	// Phase 5 — archival stamp repair, evaluated AFTER worktree
	// normalization (a reset can legitimately change the index file, so
	// the unstamped set is re-read here, not reused from phase 2).
	if err := stampRepair(ctx, paths, &rep); err != nil {
		return rep, err
	}

	// Phase 6 — derived state. Rebuild (and advance the watermark) only
	// from a canonically-clean tree: the watermark is a claim that
	// derived matches a specific DAILY commit, which a dirty tree can
	// never certify. Cell 3 therefore defers derived reconcile to the
	// next trigger, exactly as the state machine specifies.
	if canonicalClean {
		headNow, err := autogit.HeadHash(ctx, paths, autogit.Daily)
		if err != nil {
			return rep, fmt.Errorf("recovery: resolve daily HEAD post-normalization: %w", err)
		}
		wmNow, wmNowPresent, err := store.ReadDerivedWatermark(paths)
		if err != nil {
			return rep, fmt.Errorf("recovery: re-read watermark: %w", err)
		}
		if !wmNowPresent || wmNow != headNow {
			if err := RebuildDerived(ctx, paths, index.Options{Quiet: true}); err != nil {
				return rep, fmt.Errorf("recovery: rebuild derived: %w", err)
			}
			rep.DerivedRebuilt = true
		}
	}

	// Phase 7 — release the journal. Content that needed preserving was
	// preserved above; anything left is redundant with a committed turn
	// (cell 5: a crash between commit and truncate, or a power-loss
	// resurrection of an unsynced truncate) or already surfaced.
	if err := store.TruncateJournal(paths); err != nil {
		return rep, fmt.Errorf("recovery: truncate journal: %w", err)
	}

	// Phase 8 — terminal: clear the recovery marker.
	crashpoint.At(cpPreMarkerClear)
	if err := store.ClearMarker(paths); err != nil {
		return rep, fmt.Errorf("recovery: clear marker: %w", err)
	}
	logEvent(paths, "complete", fmt.Sprintf("cells=%s reset=%t stamped=%d unrepairable=%d",
		strings.Join(rep.CellsHit, ","), rep.ResetPerformed, len(rep.StampRepaired), len(rep.Unrepairable)))
	return rep, nil
}

// resetSequence is the marker-gated destructive repair: snapshot dirty
// tracked bytes into quarantine, preserve log bytes beyond daily HEAD,
// reset tracked state to DAILY HEAD (≤1 turn — never primary, INV-1),
// sweep untracked in-flight debris (into the same quarantine), restore
// the log bytes. Every step is idempotent so a crash at any point
// re-converges through cell 11.
func resetSequence(ctx context.Context, paths store.PersonantPaths, rep *memops.RecoveryReport, q *quarantine) error {
	// Quarantine snapshot before the rollback — the logs-preserve pattern
	// applied to canonical files. The marker proves an op was in flight
	// at the crash, but not WHEN each dirty byte was written: a hand-edit
	// made during a stale marker window (crash → user edits → reopen →
	// this cell) is indistinguishable from op debris, so the revert keeps
	// a byte-exact copy of every dirty tracked path. logs/ is excluded —
	// the preserve/restore bracket below already carries log bytes across
	// the reset losslessly.
	wt, err := autogit.Worktree(ctx, paths, autogit.Daily)
	if err != nil {
		return fmt.Errorf("recovery: worktree pre-reset: %w", err)
	}
	for _, rel := range wt.DirtyPaths {
		if strings.HasPrefix(rel, "logs/") {
			continue
		}
		if err := q.snapshot(rel); err != nil {
			return fmt.Errorf("recovery: quarantine snapshot: %w", err)
		}
	}
	if err := preserveLogs(ctx, paths); err != nil {
		return fmt.Errorf("recovery: preserve logs: %w", err)
	}
	reverted, err := autogit.ResetHard(ctx, paths, autogit.Daily)
	if err != nil {
		return fmt.Errorf("recovery: reset: %w", err)
	}
	rep.ResetPerformed = true
	rep.RevertedPaths = reverted
	crashpoint.At(cpResetDonePreSweep)
	swept, err := sweepUntrackedDebris(ctx, paths, q)
	if err != nil {
		return fmt.Errorf("recovery: sweep debris: %w", err)
	}
	rep.DebrisSwept = swept
	if err := restorePreservedLogs(paths); err != nil {
		return fmt.Errorf("recovery: restore logs: %w", err)
	}
	return nil
}

// adoptLegacy is cell 12's verify-gated adopt-commit: the dirty
// worktree's content is structurally validated and committed as-is to
// PRIMARY, carrying the Personant-Day trailer for the CURRENT day — the
// sole bootstrap carve-out of the uniform-trailer rule (F3: no
// marker-day and no prior HEADDAY exists on a greenfield/legacy home).
// Derived drift is NOT part of the gate — a legacy home's derived state
// is stale by definition and is rebuilt immediately after; only
// structural (schema/constraint) errors refuse the adopt.
func adoptLegacy(ctx context.Context, paths store.PersonantPaths, rep *memops.RecoveryReport) error {
	report, err := verifyStructural(paths)
	if err != nil {
		return fmt.Errorf("recovery: legacy verify: %w", err)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("recovery: legacy adopt refused: %d structural error(s), first: %s: %s",
			len(report.Errors), report.Errors[0].Path, report.Errors[0].Message)
	}
	if err := autogit.Add(ctx, paths, autogit.Primary, "."); err != nil {
		return fmt.Errorf("recovery: stage adopt: %w", err)
	}
	crashpoint.At(cpAdoptPreCommit)
	msg := autogit.WithDayTrailer("recovery: adopt pre-existing content", autogit.CurrentDay())
	hash, err := autogit.CommitWithHash(ctx, paths, autogit.Primary, msg, 0, 0)
	if err != nil {
		return fmt.Errorf("recovery: adopt commit: %w", err)
	}
	rep.AdoptCommit = hash
	logEvent(paths, "adopt", "commit="+hash)
	return nil
}

// CanonicalDirty reports whether any tracked change lies outside the
// logs/ namespace — the WT observable with the append-only-log
// carve-out (see the package doc). Exported because the fileadapter's
// ReleaseTurn gates its no-commit release on the SAME observable: one
// definition means the release predicate and the recovery predicate
// cannot drift.
func CanonicalDirty(dirtyPaths []string) bool {
	for _, p := range dirtyPaths {
		if !strings.HasPrefix(p, "logs/") {
			return true
		}
	}
	return false
}

// decodeOrig recovers the effective original marker from a re-entered
// recovery marker. OpBarrier/OpRebaseline are in the known set (F6) so a
// double-crash-during-recovery converges instead of hard-failing; a
// genuinely unknown orig still hard-fails.
func decodeOrig(m store.Marker) (store.Marker, bool, error) {
	switch m.Orig {
	case "", origNone:
		return store.Marker{}, false, nil
	case string(store.OpTurn):
		return store.Marker{Op: store.OpTurn, Turn: m.Turn}, true, nil
	case string(store.OpArchival):
		return store.Marker{Op: store.OpArchival, Day: m.Day}, true, nil
	case string(store.OpSleep):
		return store.Marker{Op: store.OpSleep}, true, nil
	case string(store.OpBarrier):
		return store.Marker{Op: store.OpBarrier, Day: m.Day}, true, nil
	case string(store.OpRebaseline):
		return store.Marker{Op: store.OpRebaseline}, true, nil
	default:
		return store.Marker{}, false, fmt.Errorf("recovery marker carries unknown orig %q", m.Orig)
	}
}

// encodeRecoveryMarker builds the cell-11 re-entrancy marker from the
// observed original.
func encodeRecoveryMarker(eff store.Marker, present bool) store.Marker {
	if !present {
		return store.Marker{Op: store.OpRecovery, Orig: origNone}
	}
	return store.Marker{Op: store.OpRecovery, Orig: string(eff.Op), Turn: eff.Turn, Day: eff.Day}
}

func origLabel(eff store.Marker, present bool) string {
	if !present {
		return origNone
	}
	return string(eff.Op)
}

// logEvent writes one recovery.* event line. Best-effort by design: the
// event log is forensics, and a log-write failure must never abort a
// recovery that is otherwise converging (the failure itself would then
// block every subsequent open).
func logEvent(paths store.PersonantPaths, action, details string) {
	_ = eventlog.Log(paths, memops.LogCategoryRecovery, action, details)
}
