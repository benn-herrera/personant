package turn

import (
	"context"
	"fmt"
	"sort"
	"time"

	"personant/internal/clock"
	"personant/internal/curator"
	"personant/internal/memops"
)

// decayTurns / decayTime are the spec §2.6.1 default thresholds for
// engagement decay. A thread idle past EITHER threshold (OR semantics)
// is decay-eligible. Directive-file plumbing (§2.6.1) arrives later;
// until then they are plain constants.
const (
	// decayTurns is the count of non-engagement turns before a thread
	// is offered for closure.
	decayTurns = 8
	// decayTime is the wall-clock equivalent of decayTurns.
	decayTime = 7 * 24 * time.Hour
)

// ClosureOutcome is the verdict a ClosureResolver returns for a §3.5
// closure offer.
type ClosureOutcome int

const (
	ClosureResolved  ClosureOutcome = iota // retire, state=resolved
	ClosureDecided                         // retire, state=decided
	ClosureAbandoned                       // retire, state=abandoned
	ClosureWIP                             // keep open, state=wip
	ClosureDefer                           // no change, re-ask later
)

// ClosureOffer is the §3.5 closure offer surfaced at turn close for one
// decayed thread. Summary and Anchors are the curator's draft; State is
// the thread's current state (always active — only active threads
// decay-prompt).
type ClosureOffer struct {
	ThreadID string
	Summary  string
	Anchors  []string
	State    memops.ThreadState
}

// ClosureResolution is the user's (or scripted harness's) verdict on a
// ClosureOffer.
type ClosureResolution struct {
	Outcome ClosureOutcome
}

// ClosureResolver is the seam between the §3.5 closure flow and the
// experience layer. The chat REPL implements it interactively; the
// scenario harness implements it from a script. A nil resolver on State
// means closure stays detection-disabled — no decay scan, no offer.
type ClosureResolver func(ctx context.Context, offer ClosureOffer) (ClosureResolution, error)

// closeStateForOutcome maps a retire-class outcome to its thread state.
// ClosureWIP and ClosureDefer are not retire-class and are handled
// separately.
func closeStateForOutcome(o ClosureOutcome) (memops.ThreadState, bool) {
	switch o {
	case ClosureResolved:
		return memops.ThreadResolved, true
	case ClosureDecided:
		return memops.ThreadDecided, true
	case ClosureAbandoned:
		return memops.ThreadAbandoned, true
	default:
		return "", false
	}
}

// decayEligible reports whether rec is a decay-eligible thread at the
// current turn and time, and returns a short detail string describing
// which signal fired (for the retire.prompt log line).
//
// A thread is eligible when its state is active (only active — wip /
// paused / blocked / retired states do not decay-prompt) AND it is idle
// past either threshold:
//
//   - Turn-based: TurnNumber - rec.LastEngagedTurn >= decayTurns, applied
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
		if idle := turnNumber - rec.LastEngagedTurn; idle >= decayTurns {
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

// surfaceClosureCandidates runs the §3.5 decay-triggered closure scan
// for this turn. It is a no-op unless BOTH state.Curator and
// state.ClosureResolver are installed: closure detection is fully
// disabled — no decay scan, no log line — unless both are present.
// This is a deliberate divergence from recall, which keeps a log-only
// mode when its resolver is nil. Closure has no log-only mode: decay
// detection with no curator and no resolver would emit only a marginal
// retire.prompt line, and drafting the closure summary requires the
// curator regardless.
//
// It lists the active project's threads, filters to decay-eligible
// active threads, and for each (in deterministic thread-ID order)
// drafts a closure summary via the curator and resolves it into a
// retire / wip / defer outcome. A curator error or a resolver error for
// one thread is logged and swallowed — closure is opportunistic and
// must never abort the turn.
//
// The scan runs after closeTurnAndUpdateEngagement has committed this
// turn's engagement, so a thread engaged this turn cannot mis-decay
// (see decayEligible).
func surfaceClosureCandidates(ctx context.Context, state *State) error {
	if state.Curator == nil || state.ClosureResolver == nil {
		return nil
	}

	// Closure is active-project-scoped: an empty ThreadFilter returns
	// threads across every project, which would retire threads in
	// sibling projects. Scoping to the active project matches the
	// project discipline updateExistingThread enforces.
	recs, err := state.Ops.ListThreads(ctx, memops.ThreadFilter{Project: state.ActiveProject.ID})
	if err != nil {
		return fmt.Errorf("closure: list threads: %w", err)
	}

	now := clock.Timeline()
	// Wall-clock decay measures neglect relative to the system's most
	// recent activity, not raw calendar time, so a whole-system absence
	// (vacation) does not make every active thread decay-eligible at once.
	// See decayEligible / systemReference (B5 / PRT3-F3).
	sysRef := systemReference(recs, now)
	type candidate struct {
		rec    memops.SpineRecord
		detail string
	}
	var eligible []candidate
	for _, rec := range recs {
		// Honor an unexpired defer-suppression grace.
		if until, deferred := state.closureDeferUntil[rec.ID]; deferred && state.TurnNumber < until {
			continue
		}
		detail, ok := decayEligible(rec, state.TurnNumber, sysRef)
		if !ok {
			continue
		}
		eligible = append(eligible, candidate{rec: rec, detail: detail})
	}
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].rec.ID < eligible[j].rec.ID
	})

	// A retire.prompt log-write failure must not starve the remaining
	// closure offers this turn. The former return-on-error aborted the whole
	// scan on the FIRST transient write hiccup, so a single failure could
	// deny closure to every later decayed thread. Degrade per candidate:
	// skip this offer (its retire.prompt marker is the offer's forensic
	// anchor, so proceeding without it would surface an unlogged offer) and
	// surface the condition ONCE after the scan — not silently swallowed,
	// not per-candidate spam. The other per-candidate substrate calls below
	// already degrade-and-continue; this aligns the log with that contract.
	var logFailures int
	for _, c := range eligible {
		if err := state.Ops.Log(ctx, memops.LogCategoryRetire, "prompt",
			"thr="+c.rec.ID+" inactivity="+c.detail); err != nil {
			logFailures++
			continue
		}

		thr, err := state.Ops.LoadThread(ctx, c.rec.ID)
		if err != nil {
			// Distinct from curator-error: the curator was never
			// consulted; the substrate load failed before it.
			_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "load-error",
				"thr="+c.rec.ID+" err="+memops.SanitizeDetail(err.Error()))
			continue
		}

		draft, err := state.Curator.DraftClosure(ctx, thr)
		if err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "curator-error",
				"thr="+c.rec.ID+" err="+memops.SanitizeDetail(err.Error()))
			continue
		}

		offer := ClosureOffer{
			ThreadID: c.rec.ID,
			Summary:  draft.Summary,
			Anchors:  draft.Anchors,
			State:    c.rec.State,
		}
		resolution, err := state.ClosureResolver(ctx, offer)
		if err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "resolver-error",
				"thr="+c.rec.ID+" err="+memops.SanitizeDetail(err.Error()))
			continue
		}

		if err := applyClosureResolution(ctx, state, c.rec.ID, draft, resolution); err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "apply-error",
				"thr="+c.rec.ID+" err="+memops.SanitizeDetail(err.Error()))
			continue
		}
	}
	if logFailures > 0 {
		return fmt.Errorf("closure: %d retire.prompt log write(s) failed; offers skipped", logFailures)
	}
	return nil
}

// applyClosureResolution applies one §3.5 closure verdict to a thread.
//
//   - Retire (resolved / decided / abandoned): rewrites the spine + file
//     with the retired state, the curator's summary + anchors, a fresh
//     state_changed, and the body unchanged; evicts the thread from both
//     Layer B (ActiveThreads) and Layer C (DormantThreads).
//   - WIP: same write but State=wip; the thread leaves the active window
//     but stays a known thread at the head of DormantThreads.
//   - Defer: no state write; arms the defer-suppression grace so the
//     thread does not re-prompt every turn.
func applyClosureResolution(ctx context.Context, state *State, threadID string, draft curator.ClosureDraft, res ClosureResolution) error {
	if res.Outcome == ClosureDefer {
		state.closureDeferUntil[threadID] = state.TurnNumber + decayTurns
		return state.Ops.Log(ctx, memops.LogCategoryRetire, "defer", "thr="+threadID)
	}

	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("closure: find thread %s: %w", threadID, err)
	}
	if !found {
		return fmt.Errorf("closure: thread %s not in spine", threadID)
	}
	fm, err := state.Ops.LoadThreadMeta(ctx, threadID)
	if err != nil {
		return fmt.Errorf("closure: load thread %s: %w", threadID, err)
	}

	now := clock.Timeline().Format(time.RFC3339)

	var newState memops.ThreadState
	switch res.Outcome {
	case ClosureWIP:
		newState = memops.ThreadWIP
	default:
		s, ok := closeStateForOutcome(res.Outcome)
		if !ok {
			return fmt.Errorf("closure: unrecognized outcome %d for %s", res.Outcome, threadID)
		}
		newState = s
	}

	// Mirror the spine and metadata so they stay in sync (the
	// updateExistingThread pattern). Closure records no new turn — it is
	// a meta-only update, so ThreadWrite.TurnExcerpt is empty and the
	// turns/ directory is left untouched.
	//
	// R2 (Build-Plan): closure runs a final AUTHORITATIVE projection for the
	// STORED spine anchors (SOLUTION §2), derived from the thread's
	// history_symbols via ProjectAnchors — the same canonical source the
	// per-turn projection uses, so a closing thread's headline cannot
	// silently diverge from its live headline. The curator's draft.Anchors
	// (SelectAnchors) remains the user-facing closure OFFER display (the
	// human-facing gist, surfaced in surfaceClosureCandidates); only the
	// persisted anchors come from projection. This is the plan's defensible
	// default for SOLUTION's "final authoritative projection" seam.
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
	rec.State = newState
	rec.Summary = draft.Summary
	rec.Anchors = anchors
	rec.StateChanged = now

	fm.ID = rec.ID
	fm.Project = rec.Project
	fm.State = newState
	fm.Summary = draft.Summary
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
		return fmt.Errorf("closure: write thread %s: %w", threadID, err)
	}
	// §3.11: a §3.5 closure write (retire or WIP — both transition the
	// thread's persisted state out of the active window) is a structural
	// change. Count it; the turn-close cadence check (turn.go step 5e) commits
	// once per turn regardless of how many threads a closure-storm (#82)
	// resolved. Defer reaches no write and is correctly not counted. Retire
	// and WIP are counted separately so the commit reason reports each
	// honestly — a WIP demotion is not a retirement (the thread stays open).
	if res.Outcome == ClosureWIP {
		state.structuralWIPs++
	} else {
		state.structuralRetires++
	}

	// The defer grace, if any, is now moot — the thread has been
	// resolved or demoted out of the active set.
	delete(state.closureDeferUntil, threadID)

	state.ActiveThreads = removeString(state.ActiveThreads, threadID)
	state.DormantThreads = removeString(state.DormantThreads, threadID)

	if res.Outcome == ClosureWIP {
		// WIP leaves the active window but stays a known thread. The
		// dormant slice is bounded by Layer C's byte budget at render time
		// (#127 dropped the v0.1 count cap), so no count truncation here.
		state.DormantThreads = append([]string{threadID}, state.DormantThreads...)
		return state.Ops.Log(ctx, memops.LogCategoryRetire, "ack", "thr="+threadID+" resolution=wip")
	}

	return state.Ops.Log(ctx, memops.LogCategoryRetire, "complete",
		"thr="+threadID+" resolution="+string(newState))
}
