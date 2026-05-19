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
//   - Wall-clock: now - parse(rec.LastEngaged) >= decayTime. An empty or
//     unparseable LastEngaged simply does not fire the wall-clock signal.
//
// This predicate assumes engagement for the current turn has already
// been committed (closeTurnAndUpdateEngagement runs before the closure
// scan): a thread engaged this turn has LastEngagedTurn == TurnNumber, so
// its turn-based idle count is 0 and it cannot wrongly decay.
func decayEligible(rec memops.SpineRecord, turnNumber int, now time.Time) (string, bool) {
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
			if idle := now.Sub(t); idle >= decayTime {
				return fmt.Sprintf("idle=%s", idle.Round(time.Hour)), true
			}
		}
	}
	return "", false
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
		detail, ok := decayEligible(rec, state.TurnNumber, now)
		if !ok {
			continue
		}
		eligible = append(eligible, candidate{rec: rec, detail: detail})
	}
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].rec.ID < eligible[j].rec.ID
	})

	for _, c := range eligible {
		if err := state.Ops.Log(ctx, "retire", "prompt",
			"thr="+c.rec.ID+" inactivity="+c.detail); err != nil {
			return fmt.Errorf("closure: log retire.prompt: %w", err)
		}

		thr, err := state.Ops.LoadThread(ctx, c.rec.ID)
		if err != nil {
			// Distinct from curator-error: the curator was never
			// consulted; the substrate load failed before it.
			_ = state.Ops.Log(ctx, "retire", "load-error",
				"thr="+c.rec.ID+" err="+sanitizeDetail(err.Error()))
			continue
		}

		draft, err := state.Curator.DraftClosure(ctx, thr)
		if err != nil {
			_ = state.Ops.Log(ctx, "retire", "curator-error",
				"thr="+c.rec.ID+" err="+sanitizeDetail(err.Error()))
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
			_ = state.Ops.Log(ctx, "retire", "resolver-error",
				"thr="+c.rec.ID+" err="+sanitizeDetail(err.Error()))
			continue
		}

		if err := applyClosureResolution(ctx, state, c.rec.ID, draft, resolution); err != nil {
			_ = state.Ops.Log(ctx, "retire", "apply-error",
				"thr="+c.rec.ID+" err="+sanitizeDetail(err.Error()))
			continue
		}
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
		return state.Ops.Log(ctx, "retire", "defer", "thr="+threadID)
	}

	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("closure: find thread %s: %w", threadID, err)
	}
	if !found {
		return fmt.Errorf("closure: thread %s not in spine", threadID)
	}
	fm, err := state.Ops.LoadThreadFrontmatter(ctx, threadID)
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

	// Mirror the spine and frontmatter so they stay in sync (the
	// updateExistingThread pattern). Closure records no new turn — it is
	// a frontmatter-only update, so ThreadWrite.TurnExcerpt is empty and
	// the turns/ directory is left untouched.
	anchors := append([]string(nil), draft.Anchors...)
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
		Spine:       rec,
		Frontmatter: fm,
	}); err != nil {
		return fmt.Errorf("closure: write thread %s: %w", threadID, err)
	}

	// The defer grace, if any, is now moot — the thread has been
	// resolved or demoted out of the active set.
	delete(state.closureDeferUntil, threadID)

	state.ActiveThreads = removeString(state.ActiveThreads, threadID)
	state.DormantThreads = removeString(state.DormantThreads, threadID)

	if res.Outcome == ClosureWIP {
		// WIP leaves the active window but stays a known thread.
		state.DormantThreads = append([]string{threadID}, state.DormantThreads...)
		if len(state.DormantThreads) > dormantThreadsCap {
			state.DormantThreads = state.DormantThreads[:dormantThreadsCap]
		}
		return state.Ops.Log(ctx, "retire", "ack", "thr="+threadID+" resolution=wip")
	}

	return state.Ops.Log(ctx, "retire", "complete",
		"thr="+threadID+" resolution="+string(newState))
}
