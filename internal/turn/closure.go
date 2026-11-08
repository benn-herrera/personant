package turn

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
)

// DecayTurns / decayTime are the spec §2.6.1 default thresholds for
// engagement decay. A thread idle past EITHER threshold (OR semantics)
// is decay-eligible. Directive-file plumbing (§2.6.1) arrives later;
// until then they are plain constants.
//
// DecayTurns is EXPORTED as a public contract value (like EmbeddingDebtCap):
// the acceptance sim's runtime-mirror Layer-B references it to replicate
// the §3.5 turn-idle closure eviction in its cross-check (ARCHITECTURE.md's
// harness↔system coupling rule — a contract value the harness depends on is
// referenced, never hand-mirrored into a second const that can silently drift).
const (
	// DecayTurns is the count of non-engagement turns before a thread
	// closes.
	DecayTurns = 8
	// decayTime is the wall-clock equivalent of DecayTurns.
	decayTime = 7 * 24 * time.Hour
)

// decayEligible reports whether rec is a decay-eligible thread at the
// current turn and time, and returns a short detail string describing
// which signal fired (for the retire.prompt log line).
//
// A thread is eligible when its state is active (only active — wip /
// paused / blocked / retired states do not decay-prompt) AND it is idle
// past either threshold:
//
//   - Turn-based: TurnNumber - rec.LastEngagedTurn >= DecayTurns, applied
//     only when TurnNumber >= rec.LastEngagedTurn. A new session resets
//     TurnNumber, so a stored value larger than the current turn means
//     the record is from a prior session — skip the turn signal and rely
//     on wall-clock.
//   - Wall-clock: sysRef - parse(rec.LastEngaged) >= decayTime. An empty
//     or unparseable LastEngaged simply does not fire the wall-clock
//     signal.
//
// sysRef (not now) is the wall-clock reference for the idle measure. It
// is the most-recent activity across the system (max LastEngaged over
// the scanned threads), so wall-clock decay measures neglect *relative
// to the system's last use* rather than raw calendar time. This
// suppresses the B5/PRT3-F3 vacation wave: after a multi-week whole-
// system absence, every thread's last_engaged is ~vacation-ago, but so
// is sysRef, so a thread idle only because the user was away does not
// fire. A thread genuinely neglected while the user worked other threads
// in that window still fires, because some other thread's recent
// engagement pulls sysRef forward of it. The whole-system idle gap
// (now - sysRef) is deliberately excluded — nobody was using the system
// during it, so it is not thread-specific neglect. Turn-count decay is
// unaffected (no turns occur during an absence, so it cannot inflate).
//
// This predicate assumes engagement for the current turn has already
// been committed (closeTurnAndUpdateEngagement runs before the closure
// scan): a thread engaged this turn has LastEngagedTurn == TurnNumber, so
// its turn-based idle count is 0 and it cannot wrongly decay.
func decayEligible(rec memops.SpineRecord, turnNumber int, sysRef time.Time) (string, bool) {
	if rec.State != memops.ThreadActive {
		return "", false
	}
	// LastEngagedTurn == 0 is the "missing field" sentinel (a record
	// engaged in a prior session, or never engaged). Treating it as an
	// ordinary value is safe: TurnNumber is bumped to >= 1 before any
	// engagement fires, so no genuine engagement ever records turn 0 —
	// a real value and the sentinel can never collide.
	if turnNumber >= rec.LastEngagedTurn {
		if idle := turnNumber - rec.LastEngagedTurn; idle >= DecayTurns {
			return fmt.Sprintf("turns=%d", idle), true
		}
	}
	if rec.LastEngaged != "" {
		if t, err := time.Parse(time.RFC3339, rec.LastEngaged); err == nil {
			if idle := sysRef.Sub(t); idle >= decayTime {
				return fmt.Sprintf("idle=%s", idle.Round(time.Hour)), true
			}
		}
	}
	return "", false
}

// systemReference returns the wall-clock decay reference: the most-recent
// activity across the scanned threads (max parseable LastEngaged),
// clamped to not exceed now. Threads with an empty or unparseable
// LastEngaged contribute nothing. When no thread carries a usable
// timestamp the reference is now — i.e. no system-idle suppression, the
// pre-fix behavior. The clamp guards against a clock skew or a future-
// dated last_engaged pushing the reference past now (which would inflate,
// not suppress, decay).
//
// This is the B5/PRT3-F3 system-idle signal: now - systemReference is the
// whole-system absence gap, which decayEligible excludes from per-thread
// wall-clock idle.
func systemReference(recs []memops.SpineRecord, now time.Time) time.Time {
	ref := time.Time{}
	for _, rec := range recs {
		if rec.LastEngaged == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, rec.LastEngaged)
		if err != nil {
			continue
		}
		if t.After(ref) {
			ref = t
		}
	}
	if ref.IsZero() || ref.After(now) {
		return now
	}
	return ref
}

// closureCandidate is one decay-eligible thread plus the signal that
// fired, for the retire.prompt / retire.pending log line.
type closureCandidate struct {
	rec    memops.SpineRecord
	detail string
}

// decayCandidates lists the active project's decay-eligible threads in
// deterministic thread-ID order.
func decayCandidates(ctx context.Context, state *State) ([]closureCandidate, error) {
	// Closure is active-project-scoped: an empty ThreadFilter returns
	// threads across every project, which would retire threads in
	// sibling projects. Scoping to the active project matches the
	// project discipline updateExistingThread enforces.
	recs, err := state.Ops.ListThreads(ctx, memops.ThreadFilter{Project: state.ActiveProject.ID})
	if err != nil {
		return nil, fmt.Errorf("closure: list topics: %w", err)
	}

	// Wall-clock decay measures neglect relative to the system's most
	// recent activity, not raw calendar time, so a whole-system absence
	// (vacation) does not make every active thread decay-eligible at once.
	// See decayEligible / systemReference (B5 / PRT3-F3).
	sysRef := systemReference(recs, clock.Timeline())
	var eligible []closureCandidate
	for _, rec := range recs {
		detail, ok := decayEligible(rec, state.TurnNumber, sysRef)
		if !ok {
			continue
		}
		eligible = append(eligible, closureCandidate{rec: rec, detail: detail})
	}
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].rec.ID < eligible[j].rec.ID
	})
	return eligible, nil
}

// draftClosure loads a thread and asks the curator for its closing
// summary, logging the two distinct failure modes (substrate load vs.
// curator) under their own §2.8 actions. ok=false means the caller must
// skip this thread.
func draftClosure(ctx context.Context, state *State, threadID string) (string, bool) {
	thr, err := state.Ops.LoadThread(ctx, threadID)
	if err != nil {
		// Distinct from curator-error: the curator was never consulted;
		// the substrate load failed before it.
		_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "load-error",
			"thr="+threadID+" err="+memops.SanitizeDetail(err.Error()))
		return "", false
	}
	summary, err := state.Curator.DraftClosure(ctx, thr)
	if err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "curator-error",
			"thr="+threadID+" err="+memops.SanitizeDetail(err.Error()))
		return "", false
	}
	return summary, true
}

// surfaceClosureCandidates runs the §3.5 decay-triggered closure scan
// for this turn. It is a no-op without state.Curator: closing a thread
// stores the curator's summary, so with no curator nothing decays out.
//
// Per decayed thread, in deterministic thread-ID order: retire.prompt,
// the curator draft, then closeThreadResolved. A draft that fails or
// comes back empty logs retire.curator-error and writes nothing; the
// thread stays decay-eligible, so the next scan retries it.
//
// The scan runs after closeTurnAndUpdateEngagement has committed this
// turn's engagement, so a thread engaged this turn cannot mis-decay
// (see decayEligible). Every write rides the turn transaction already
// open around turn close — the scan invents no commit of its own
// (§4.5.8).
func surfaceClosureCandidates(ctx context.Context, state *State) error {
	if state.Curator == nil {
		return nil
	}
	eligible, err := decayCandidates(ctx, state)
	if err != nil {
		return err
	}

	// A retire.prompt log-write failure must not starve the remaining
	// candidates this turn: skip this one (retire.prompt is its forensic
	// anchor, so closing without it would leave an unlogged close) and
	// report the count once after the scan.
	var logFailures int
	for _, c := range eligible {
		if err := state.Ops.Log(ctx, memops.LogCategoryRetire, "prompt",
			"thr="+c.rec.ID+" inactivity="+c.detail); err != nil {
			logFailures++
			continue
		}
		summary, ok := draftClosure(ctx, state, c.rec.ID)
		if !ok {
			continue
		}
		if strings.TrimSpace(summary) == "" {
			_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "curator-error",
				"thr="+c.rec.ID+" err=empty draft summary")
			continue
		}
		if err := closeThreadResolved(ctx, state, c.rec.ID, summary); err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "apply-error",
				"thr="+c.rec.ID+" err="+memops.SanitizeDetail(err.Error()))
		}
	}
	if logFailures > 0 {
		return fmt.Errorf("closure: %d retire log write(s) failed; candidates skipped", logFailures)
	}
	return nil
}

// closeThreadResolved is the §3.5 closure write: it rewrites the spine +
// file with state=resolved, the curator's summary, the projected anchors,
// a fresh state_changed, and the body unchanged; evicts the thread from
// both Layer B (ActiveThreads) and Layer C (DormantThreads); and logs
// retire.complete.
func closeThreadResolved(ctx context.Context, state *State, threadID string, summary string) error {
	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("closure: find topic %s: %w", threadID, err)
	}
	if !found {
		return fmt.Errorf("closure: topic %s not in spine", threadID)
	}
	fm, err := state.Ops.LoadThreadMeta(ctx, threadID)
	if err != nil {
		return fmt.Errorf("closure: load topic %s: %w", threadID, err)
	}

	now := clock.Timeline().Format(time.RFC3339)

	// Mirror the spine and metadata so they stay in sync (the
	// updateExistingThread pattern). Closure records no new turn — it is
	// a meta-only update, so ThreadWrite.TurnExcerpt is empty and the
	// turns/ directory is left untouched.
	//
	// Closure runs a final authoritative projection for the stored spine
	// anchors, derived from the thread's history_symbols via ProjectAnchors —
	// the same canonical source the per-turn projection uses, so a closing
	// thread's headline cannot silently diverge from its live headline.
	//
	// Closure advances no turn; project at the thread's current turn count
	// so LastActiveTurn stays monotone. The mutated symbols and the changed
	// watermark are written back so a closing projection that shifts the
	// headline is recorded.
	anchors, projectedSyms, changed := projectAnchorsForTurn(ctx, state, fm.HistorySymbols, rec.TurnCount)
	fm.HistorySymbols = capHistorySymbols(projectedSyms)
	if changed {
		rec.AnchorsProjectedAtTurn = rec.TurnCount
	}
	rec.State = memops.ThreadResolved
	rec.Summary = summary
	rec.Anchors = anchors
	rec.StateChanged = now

	fm.ID = rec.ID
	fm.Project = rec.Project
	fm.State = memops.ThreadResolved
	fm.Summary = summary
	fm.Anchors = append([]string(nil), anchors...)
	fm.StateChanged = now
	fm.LastEngaged = rec.LastEngaged
	fm.LastEngagedTurn = rec.LastEngagedTurn
	fm.Created = rec.Created
	fm.TurnCount = rec.TurnCount
	fm.RecallFires = rec.RecallFires

	if err := state.Ops.EngageThread(ctx, memops.ThreadWrite{
		Spine: rec,
		Meta:  fm,
	}); err != nil {
		return fmt.Errorf("closure: write topic %s: %w", threadID, err)
	}
	// §3.11: a closure write is a structural change. Count it; the
	// turn-close cadence check commits once per turn regardless of how many
	// threads a closure-storm (#82) resolved.
	state.structuralRetires++

	evictThread(state, threadID)

	return state.Ops.Log(ctx, memops.LogCategoryRetire, "complete",
		"thr="+threadID+" resolution="+string(memops.ThreadResolved))
}
