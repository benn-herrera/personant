package turn

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"personant/internal/memops"
	"personant/internal/recall/measure"
)

// joinInts renders a turn-number slice as a comma-separated token for the
// spine.intra-match-fire log line (e.g. "12,40,103"). Empty → "" .
func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// recallOfferK bounds how many top-ranked recall candidates turn close
// considers for an automatic fetch. C.6 measured top-1 recall ~73%,
// top-3 ~92%.
const recallOfferK = 3

// §3.4 auto-fetch bars. A candidate the matcher is confident about is
// fetched automatically; anything under its bar leaves only the
// match-fire record.
//
// The two bars are per TIER because the scales differ: layer 1 is a
// Jaccard set overlap, layers 2/3 (embedding + intra-thread) are cosine
// similarities. Both are grounded in the C.6 corpus calibration
// (internal/scenarios/testdata/recall_madlibs, re-measured 2026-08-05
// over 301 templates × synonym-depth M ∈ 1..4):
//
//   - SYMBOLIC. Precision by threshold: 0.990 at T=0.4, 1.000 at T=0.5
//     and T=0.6, at EVERY drift depth (0.3/0.4 and 0.5/0.6 are pairwise
//     identical operating points — discrete Jaccard score gaps). An
//     auto-accept needs the precision plateau, so the bar sits inside it,
//     one step above the highest measured point rather than on it: the
//     sweep's top row is 0.6, and an unasked promotion should not be
//     calibrated at the edge of the measured range.
//   - COSINE. Precision by cosine cutoff: 0.58 at 0.55 (the surface
//     floor, scoring.DefaultCosineThreshold), 0.76-0.82 at 0.60, 0.89-0.95
//     at 0.65 — monotone and still climbing at the top of the swept range.
//     0.75 is one further step, and it is corroborated independently: it
//     is the cosine at which the #111 live dense-cluster diagnosis found
//     leaves "clearly related, not kidding" (scoring.ClearlyRelated).
//     Deliberately NOT aliased to that constant — a within-thread
//     relevance-net knob and the auto-fetch bar must be free to move
//     apart — but the agreement of two independent measurements at the
//     same value is why this is the chosen bar.
//
// Embedding and intra-thread share ONE bar: they are the same cosine
// against the same embedding space (a thread-body vector vs. a
// turn-excerpt vector), and nothing in the calibration data separates
// them. Split them if evidence ever does.
//
// Both are §9 calibration windows, not frozen values.
const (
	// recallAutoSymbolic is the layer-1 Jaccard score at or above which a
	// candidate is fetched automatically.
	recallAutoSymbolic = 0.65
	// recallAutoCosine is the layer-2/3 cosine at or above which a
	// candidate is fetched automatically.
	recallAutoCosine = 0.75
)

// autoAccepts reports whether a candidate clears its tier's auto-fetch
// bar. The tier is read off which layer produced the unified ranking
// score (the same rule measure.Result ranking uses): a cosine hit —
// embedding, or intra-thread with a real positive chunk score — is scored
// on the cosine scale; everything else is a symbolic Jaccard score. A
// completeness-floor-only hit (§3.4 #123: intra turns at Score 0, no
// other layer) can never auto-accept, which is correct — it is a
// findability guarantee, not a relevance claim.
func autoAccepts(r measure.Result) bool {
	if r.Embedding != nil || (r.IntraThread != nil && r.IntraThread.Score > 0) {
		return r.Score >= recallAutoCosine
	}
	return r.Score >= recallAutoSymbolic
}

// surfaceRecallCandidates runs the §3.4 recall stack for this turn, logs
// the per-layer matches, and fetches each of the top recallOfferK
// candidates that clears its tier's auto-fetch bar (autoAccepts) through
// acceptRecallCandidate. A candidate under the bar leaves only its
// match-fire record. A non-intra candidate already resident in Layer B is
// skipped: it is already in context, and fetching it again would re-fire
// every turn the topic continued. An intra-thread hit is exempt — its
// subject is the engaged thread's early content, which residency says
// nothing about.
//
// Recall runs entirely behind the measure.Recaller interface — this
// function knows nothing of symbolic Jaccard, embedding cosine, or the
// embedding index. It hands the Recaller the turn's symbols and text
// and logs whatever merged candidates come back, per layer.
//
// engagedSet (threads engaged this turn) is excluded — an engaged
// thread doesn't shadow the turn's own engagement signal. A Recaller
// error is non-fatal: the caller logs and swallows it (recall is
// opportunistic and must never abort the turn).
//
// engagedOwner is the thread the user is currently in (the turn's owner).
// It is passed as Request.Engaged so the §4.1 step-3 intra-thread pass
// scans its scrolled-out early-content chunks (the #109 case) even though
// it is in engagedSet at the thread level. Empty when nothing was engaged.
func surfaceRecallCandidates(ctx context.Context, state *State, userInput string, engagedSet map[string]struct{}, engagedOwner string) error {
	if state.Recaller == nil {
		return nil
	}
	results, err := state.Recaller.Recall(ctx, measure.Request{
		QuerySymbols: state.coalesce.symbolList(),
		QueryText:    userInput,
		Project:      state.ActiveProject.ID,
		Exclude:      engagedSet,
		Engaged:      engagedOwner,
		// Bound for the §3.4 recall-completeness lexical pass (#123): the
		// embedding-debt CAP, the max scrolled-out tail that may be awaiting
		// its async fine-tier flush. Passed only when an embedder-capable
		// recaller is installed (debtWindowBound), so the fine tier — and thus
		// the lag dead zone — actually exists; a symbolic-only setup has no
		// fine tier and keeps its byte-identical prior behaviour (0 → no debt
		// pass). The cap, not the live debt, so the floor stays continuous
		// across a debt-cap flush's in-flight window (see Request docs).
		EngagedDebtWindow: debtWindowBound(state, engagedOwner),
		// Turn-excerpts of the engaged thread an accepted recall already
		// pulled into the working window this session — accept suppression.
		// Without it the intra pass re-proposes the excerpt just fetched,
		// every turn the topic continues:
		// the engaged thread cannot be excluded at the thread level (the intra
		// pass targets it BY id), and the accept left no mark. See
		// State.recallWindowTurns.
		EngagedInWindow: state.recallWindowTurns[engagedOwner],
	})
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}

	querySize := len(state.coalesce.symbols)
	queryChars := len(strings.TrimSpace(userInput))
	for _, r := range results {
		// Per-layer logging — the spine.match-fire / spine.embed-match-fire
		// vocabulary is preserved so the recall-fidelity test harness and
		// the corpus measurements keep parsing it unchanged.
		if r.Symbolic != nil {
			details := fmt.Sprintf("%s score=%.2f matched=%s query_size=%d",
				r.ThreadID, r.Symbolic.Score,
				strings.Join(r.Symbolic.MatchedSymbols, ","), querySize)
			if err := state.Ops.Log(ctx, memops.LogCategorySpine, "match-fire", details); err != nil {
				return fmt.Errorf("log spine.match-fire: %w", err)
			}
		}
		if r.Embedding != nil {
			details := fmt.Sprintf("%s score=%.3f query_chars=%d",
				r.ThreadID, r.Embedding.Score, queryChars)
			if err := state.Ops.Log(ctx, memops.LogCategorySpine, "embed-match-fire", details); err != nil {
				return fmt.Errorf("log spine.embed-match-fire: %w", err)
			}
		}
		// Intra-thread (#109) fine-tier hit on the engaged thread: an early,
		// scrolled-out turn-excerpt matched the query. Mirrors the
		// embed-match-fire emission (design §7) — the additive
		// spine.intra-match-fire event the §8 oracle scores. Only non-user
		// values reach the format string: the thread id, the matched
		// turn-excerpt number(s), and the best chunk score.
		if r.IntraThread != nil {
			details := fmt.Sprintf("%s score=%.3f turns=%s query_chars=%d",
				r.ThreadID, r.IntraThread.Score, joinInts(r.IntraThread.Turns), queryChars)
			if err := state.Ops.Log(ctx, memops.LogCategorySpine, "intra-match-fire", details); err != nil {
				return fmt.Errorf("log spine.intra-match-fire: %w", err)
			}
		}
	}

	for _, r := range results[:min(len(results), recallOfferK)] {
		if !autoAccepts(r) {
			continue
		}
		if r.IntraThread == nil && slices.Contains(state.ActiveThreads, r.ThreadID) {
			continue
		}
		acceptRecallCandidate(ctx, state, r)
	}
	return nil
}

// acceptRecallCandidate pulls one candidate into Layer B and records the
// fetch. It is the ONE recall accept path, so nothing can skip the fetch
// chokepoint, the RecallFires counter, or the suppression mark.
//
// A recall-accept promotion is a working-window mutation, so it goes
// through the SAME §3.0.5 fetch chokepoint every other Layer-B fetch does
// (G-F3 / "no gaps"): fetchThroughChain loads the thread, declines a
// cross-project candidate (§3.2 — an embedding/intra hit can name a
// foreign thread the symbolic project filter never saw, so the accept
// path needs the same guard the §5.5 reprompt has), fires the
// thread.fetched delta through onContextDelta, and promotes into Layer B
// (recording the §2.7.3 recallSurfaced origin — an accept runs at
// turn-close, so its attribution lands one turn later). On any outcome
// that prevents promotion (fetch-miss or cross-project decline, both
// already logged) it returns false with no accept log, no RecallFires
// bump and no suppression mark — exactly as the reprompt path skips an
// unpromotable thread.
//
// A log-write failure is logged-and-swallowed rather than returned: the
// promotion has already happened, and aborting here would strand the
// remaining candidates of the same turn.
func acceptRecallCandidate(ctx context.Context, state *State, c measure.Result) bool {
	if !fetchThroughChain(ctx, state, c.ThreadID) {
		return false
	}
	if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "accept",
		fmt.Sprintf("thr=%s layers=%s", c.ThreadID, strings.Join(c.Layers(), "+"))); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "error",
			"accept-log thr="+c.ThreadID+" err="+memops.SanitizeDetail(err.Error()))
	}
	// An accept is a fetch — bump the §2.2 RecallFires counter. A
	// counter-write failure must not abort turn close or skip the remaining
	// accepted threads: log it and continue.
	if err := state.Ops.RecordRecallFire(ctx, c.ThreadID); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "fire-error",
			"thr="+c.ThreadID+" err="+memops.SanitizeDetail(err.Error()))
	}
	markRecallWindowTurns(state, c)
	return true
}

// markRecallWindowTurns records the engaged-thread turn-excerpts an
// accepted intra-thread candidate brought into the working window, so the
// §3.4 intra pass stops re-proposing them (State.recallWindowTurns). A
// candidate with no intra hit carries no turns and marks nothing — the
// thread-level layers are already handled by Layer-B residency.
func markRecallWindowTurns(state *State, c measure.Result) {
	if c.IntraThread == nil || len(c.IntraThread.Turns) == 0 {
		return
	}
	if state.recallWindowTurns == nil {
		state.recallWindowTurns = make(map[string]map[int]struct{})
	}
	turns := state.recallWindowTurns[c.ThreadID]
	if turns == nil {
		turns = make(map[int]struct{}, len(c.IntraThread.Turns))
		state.recallWindowTurns[c.ThreadID] = turns
	}
	for _, n := range c.IntraThread.Turns {
		turns[n] = struct{}{}
	}
}
