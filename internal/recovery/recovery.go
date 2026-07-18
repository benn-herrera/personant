// Package recovery is the startup reconcile orchestrator for the
// personant substrate (#94, SPEC §4.5.8). Every open runs Reconcile
// between Init and the first session read; the clean path is a cheap
// no-op, and every unclean-shutdown shape maps to exactly one cell of
// the recovery state machine below (the MAD-converged design in
// mad-design/crash-stability/SOLUTION.md; the SPEC rewrite lands in R5).
//
// # The state machine (observables → cell)
//
// Observables at open: MARKER (the in-flight-op signal, op-typed), WT
// (canonical worktree dirtiness vs HEAD — see the logs/ carve-out
// below), HEADTURN (the turn id in HEAD's commit trailer, ⊥ when
// absent), ARCH (unstamped archive-index entries, evaluated AFTER
// worktree normalization), DERIVED (watermark vs HEAD).
//
// MARKER has two carriers (the R3-addendum marker-into-journal fold):
//
//   - MARKER(turn,T) := the turn journal is NON-EMPTY and its first
//     record carries turn id T (T=⊥ for a torn/corrupt first record —
//     still an in-flight turn, id unknown, which HEADTURN≠⊥ can never
//     equal, so it classifies as "not committed": cell 4/6, never a
//     false cell 5). The journal append's fsync makes the signal
//     durable; there is no per-turn marker file. (A legacy op=turn
//     marker FILE from pre-addendum code is still honored and cleared.)
//   - MARKER(archival|sleep|recovery) := the op-in-progress.json marker
//     file — batch ops only.
//
// The cells below are UNCHANGED in meaning; only the op=turn carrier
// moved from the marker file into the journal.
//
//	cell 1  marker absent, clean, derived fresh      → no-op
//	cell 2  marker absent, clean, derived stale      → rebuild derived, write watermark
//	cell 3  marker absent, DIRTY, watermark present  → hand-edit: NEVER reset (see below)
//	cell 4  op=turn, dirty, HEADTURN≠T               → torn turn: preserve+reset+sweep
//	cell 5  op=turn, clean, HEADTURN==T              → committed; clear marker, zero loss
//	cell 6  op=turn, clean, HEADTURN≠T               → nothing landed; preserve journal, clear
//	cell 7  cell 4 coinciding with unstamped ARCH    → cell 4 then the stamp pass
//	cell 8  op=archival, dirty                       → reset+sweep; redrain re-triggers later
//	cell 9  unstamped ARCH (any cell, post-normalization) → locate deletion commit, stamp;
//	        unlocatable ⇒ recovery.unrepairable, entry stays refused — never guessed
//	cell 10 op=sleep                                 → clear marker; substrate self-heals
//	cell 11 op=recovery (crash during recovery)      → re-run with the original marker; every
//	        phase is idempotent, so re-entry converges to the same terminal state
//	cell 12 marker absent, dirty, NO watermark ever  → greenfield/legacy: verify-gated
//	        adopt-commit, stamp repair, full rebuild, watermark
//
// # Cell 3 — why a markerless-dirty tree is NEVER reset
//
// This is the single most correctness-critical rule in the design.
// Canonical text is hand-editable BY DESIGN, so a dirty tree with no
// marker must be treated as a legitimate user edit and absorbed
// forward, never destroyed. That is safe because markerless dirt is
// provably not crash debris: the only writers of multi-file canonical
// state (turn close, archival, any future canonical-touching sleep op)
// are REQUIRED to open their scope before their first canonical write —
// the turn path by its fsynced first journal append, the batch ops by
// the marker file — and release it only after their commit lands, so
// any crash that could leave torn canonical writes necessarily leaves
// the in-flight signal too, and a dirty-and-markerless worktree cannot
// have been produced by a crash.
// Gating the reset on git-dirtiness instead of marker presence would
// silently destroy user edits; gating on the marker alone is what makes
// reset --hard a ≤1-turn-loss operation instead of a data-loss defect.
// (The one deliberate discriminator: markerless-dirty with NO watermark
// is cell 12 — a pre-upgrade home that has never been reconciled, whose
// uncommitted content turns are adopted forward, verify-gated. Once a
// home has been reconciled once, the watermark exists and markerless-
// dirty is always cell 3.)
//
// # The logs/ carve-out on the WT observable
//
// "Dirty" means tracked changes OUTSIDE logs/. The event log is tracked
// but append-only and is written continuously between recovery points,
// so logs-tail dirt is the steady operating state, not a crash
// signature — including it would make every open read as dirty and
// would misclassify a mid-model-call crash (cell 6, structurally clean)
// as a torn turn (cell 4). Resets still revert logs (they are tracked),
// which is why every reset is bracketed by the preserve/restore pass in
// logs.go: forensic log bytes beyond HEAD survive the rollback.
//
// # Idempotence and re-entrancy (cell 11)
//
// Before its first mutating action Reconcile replaces the observed
// marker with {op: recovery, orig: <observed op>} and clears it only
// after the terminal state is reached. A crash anywhere inside recovery
// therefore re-enters here, re-derives the effective original marker
// from orig, and re-runs; every phase (preserve-once, reset, sweep,
// merge-restore, stamp, rebuild, truncate) is individually idempotent.
// A Reconcile error leaves the recovery marker in place so the caller
// refuses to open and the retry converges.
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
//
// # Deferred carries (adjudicated in the R2 review)
//
// Status after R3: the verbs-bypass-Reconcile gap is CLOSED (cmd's
// substrate-reading verbs open through reconciledOps — same refuse-on-
// error semantics as chat); arm-on-Nth-hit (crashpoint.ArmOnHit) and the
// Add/Commit-gap kill point (fileadapter.CommitTurn.postAddPreCommit)
// are landed.
// Still open for R4: a kill point inside the ResetHard loop, and the
// exhaustive kill-point matrix itself.
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
	Cell8Archival      = "cell-8-archival"
	Cell9StampRepair   = "cell-9-stamp-repair"
	Cell10Sleep        = "cell-10-sleep"
	Cell11Reentry      = "cell-11-recovery-reentry"
	Cell12LegacyAdopt  = "cell-12-legacy-adopt"
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
// LoadSession. A non-nil error means the substrate must not be opened;
// the in-progress recovery marker is left behind so the next call
// re-enters and converges.
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

	head, err := autogit.HeadHash(ctx, paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: resolve HEAD (did Init run?): %w", err)
	}
	wt, err := autogit.Worktree(ctx, paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: worktree state: %w", err)
	}
	dirty := CanonicalDirty(wt.DirtyPaths)
	headTurn, err := autogit.HeadTurn(ctx, paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: read HEAD turn trailer: %w", err)
	}
	wm, wmPresent, err := store.ReadDerivedWatermark(paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: read derived watermark: %w", err)
	}
	unstamped, err := unstampedEntries(paths)
	if err != nil {
		return rep, fmt.Errorf("recovery: read archive index: %w", err)
	}

	// Cell 1 — the clean path. No marker (and no re-entry), canonically
	// clean, derived fresh, nothing unstamped, journal empty. The heal
	// and tmp-sweep above are idempotent and already done, so no
	// engagement is needed even when they acted.
	if !effPresent && !reentry && !dirty &&
		wmPresent && wm == head &&
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
	logEvent(paths, "begin", fmt.Sprintf("orig=%s turn=%s reentry=%t", origLabel(eff, effPresent), eff.Turn, reentry))
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
	switch {
	case effPresent && eff.Op == store.OpTurn:
		committed := headTurn != "" && headTurn == eff.Turn
		switch {
		case committed:
			// Cell 5: the turn's commit landed; the crash hit between
			// commit and journal-truncate (or an unsynced truncate
			// resurrected the just-committed turn's journal after power
			// loss — same observable, same handling). Zero loss; journal
			// is redundant with the commit and is truncated at phase 7.
			// (Divergence corner: a LEGACY op=turn marker FILE whose id
			// equals HEADTURN but whose non-empty journal carries a
			// DIFFERENT turn Tj would truncate Tj here unpreserved — but that
			// state is unreachable without external tampering, since
			// pre-addendum code wrote the marker file and journaled content
			// for one and the same turn.)
			rep.CellsHit = append(rep.CellsHit, Cell5TurnCommitted)
			if dirty {
				// Anomalous under the protocol (nothing canonical is written
				// for T after its commit) — so whatever this dirt is, it is
				// not turn-T debris. Treat it as cell 3 dirt: absorb, never
				// reset a committed turn's aftermath.
				rep.CellsHit = append(rep.CellsHit, Cell3HandEdit)
			}
		case dirty:
			// Cell 4 — the core torn turn.
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
			// deleted — see sweepUntrackedDebris for why this is heuristic,
			// not proof, under scoped per-turn commits; a torn turn whose
			// only canonical writes were NEW files presents exactly this
			// clean-tracked shape); (b) cell-11
			// convergence — a cell-4 pass killed after its reset re-enters
			// HERE (tree now clean), and skipping the sweep would leave the
			// first pass's debris behind forever.
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

	case effPresent && eff.Op == store.OpArchival:
		rep.CellsHit = append(rep.CellsHit, Cell8Archival)
		// Turn content is never at risk here (archival runs strictly
		// between turns), so a non-empty journal is a contract anomaly —
		// preserve it rather than destroy it, then proceed.
		if len(records) > 0 || torn > 0 {
			if err := preserveJournalContent(paths, &rep, "", records, q); err != nil {
				return rep, err
			}
		}
		if dirty {
			if err := resetSequence(ctx, paths, &rep, q); err != nil {
				return rep, err
			}
			canonicalClean = true
			logEvent(paths, "rollback", fmt.Sprintf("op=archival reverted=%d debris=%d",
				len(rep.RevertedPaths), len(rep.DebrisSwept)))
		} else {
			// Clean variant (crash between archival's commits): nothing to
			// reset, but the debris sweep still runs under the marker's
			// license — same cell-11 convergence rationale as cell 6.
			swept, serr := sweepUntrackedDebris(ctx, paths, q)
			if serr != nil {
				return rep, fmt.Errorf("recovery: sweep debris: %w", serr)
			}
			rep.DebrisSwept = swept
		}
		// The interrupted drain re-triggers on the next spine-pressure
		// check; nothing to re-drive here.

	case effPresent && eff.Op == store.OpSleep:
		// Cell 10: sleep touches only git internals and gitignored
		// caches; git's own gc is self-healing and the recall cache
		// self-heals on staleness. Any dirt is hand-edit territory —
		// absorbed, never reset.
		rep.CellsHit = append(rep.CellsHit, Cell10Sleep)
		// Sleep runs strictly between turns (like archival), so a non-empty
		// journal is a contract anomaly — preserve it rather than let phase 7
		// truncate it away. This closes the post-CommitTurn pressure-gc
		// window: power loss during MaybeGC's op=sleep scope can resurrect
		// turn T's not-yet-durably-truncated journal, and if T's own commit
		// is likewise not durable those journaled bytes are the only surviving
		// copy. Mirror cell 8's preserve-if-nonempty handling; the artifact
		// (or torn-journal quarantine) and its event log are the anomaly note.
		if len(records) > 0 || torn > 0 {
			if err := preserveJournalContent(paths, &rep, "", records, q); err != nil {
				return rep, err
			}
		}

	case dirty && !wmPresent:
		// Cell 12: greenfield/legacy first-open — no watermark has ever
		// been written, and the tree carries uncommitted content (an
		// old-cadence home's legitimate content turns). Adopt forward,
		// verify-gated; stamp repair and full rebuild follow below.
		rep.CellsHit = append(rep.CellsHit, Cell12LegacyAdopt)
		if err := adoptLegacy(ctx, paths, &rep); err != nil {
			return rep, err
		}
		canonicalClean = true

	case dirty:
		// Cell 3 — the hand-edit. Absorbed forward at the next FULL
		// sweep — the session-close/backstop Checkpoint or the day-
		// barrier sweep (TODO(R3b)) — NOT the next turn: per-turn
		// commits stage only the turn's own recorded write set
		// (R3-addendum item 3), so hand-edits stay dirty, untouched and
		// unreset, until a full Add(".") pass. Derived state reconciles
		// on the next trigger. See the package doc for why this must
		// never reset.
		rep.CellsHit = append(rep.CellsHit, Cell3HandEdit)

	case !wmPresent || wm != head:
		rep.CellsHit = append(rep.CellsHit, Cell2DerivedStale)

	default:
		// Only reachable via re-entry after a prior pass already
		// converged everything; record the clean terminal.
		rep.CellsHit = append(rep.CellsHit, Cell1Clean)
	}

	// Phase 5 — archival stamp repair, evaluated AFTER worktree
	// normalization (a reset can legitimately change the index file, so
	// the unstamped set is re-read here, not reused from phase 2).
	if err := stampRepair(ctx, paths, &rep); err != nil {
		return rep, err
	}

	// Phase 6 — derived state. Rebuild (and advance the watermark) only
	// from a canonically-clean tree: the watermark is a claim that
	// derived matches a specific commit, which a dirty tree can never
	// certify. Cell 3 therefore defers derived reconcile to the next
	// trigger, exactly as the state machine specifies.
	if canonicalClean {
		headNow, err := autogit.HeadHash(ctx, paths)
		if err != nil {
			return rep, fmt.Errorf("recovery: resolve HEAD post-normalization: %w", err)
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
// tracked bytes into quarantine, preserve log bytes beyond HEAD, reset
// tracked state to HEAD, sweep untracked in-flight debris (into the
// same quarantine), restore the log bytes. Every step is idempotent so
// a crash at any point re-converges through cell 11.
func resetSequence(ctx context.Context, paths store.PersonantPaths, rep *memops.RecoveryReport, q *quarantine) error {
	// Quarantine snapshot before the rollback — the logs-preserve pattern
	// applied to canonical files. The marker proves an op was in flight
	// at the crash, but not WHEN each dirty byte was written: a hand-edit
	// made during a stale marker window (crash → user edits → reopen →
	// this cell) is indistinguishable from op debris, so the revert keeps
	// a byte-exact copy of every dirty tracked path. logs/ is excluded —
	// the preserve/restore bracket below already carries log bytes across
	// the reset losslessly.
	wt, err := autogit.Worktree(ctx, paths)
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
	reverted, err := autogit.ResetHard(ctx, paths)
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
// worktree's content is structurally validated and committed as-is.
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
	if err := autogit.Add(ctx, paths, "."); err != nil {
		return fmt.Errorf("recovery: stage adopt: %w", err)
	}
	crashpoint.At(cpAdoptPreCommit)
	hash, err := autogit.CommitWithHash(ctx, paths, "recovery: adopt pre-existing content", 0, 0)
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
// recovery marker.
func decodeOrig(m store.Marker) (store.Marker, bool, error) {
	switch m.Orig {
	case "", origNone:
		return store.Marker{}, false, nil
	case string(store.OpTurn):
		return store.Marker{Op: store.OpTurn, Turn: m.Turn}, true, nil
	case string(store.OpArchival):
		return store.Marker{Op: store.OpArchival}, true, nil
	case string(store.OpSleep):
		return store.Marker{Op: store.OpSleep}, true, nil
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
	return store.Marker{Op: store.OpRecovery, Orig: string(eff.Op), Turn: eff.Turn}
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
