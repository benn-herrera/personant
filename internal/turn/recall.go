package turn

import (
	"context"
	"fmt"
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

// recallOfferK is the number of recall candidates surfaced to the user
// at turn close. C.6 measured top-1 recall ~73%, top-3 ~92%; surface
// the top 3 and let the user pick rather than forcing a single-candidate
// guess.
const recallOfferK = 3

// DeclineReason is the fixed-enum reason captured when a recall
// candidate is not accepted. Deliberately small for v0.1.
type DeclineReason string

const (
	DeclineNotRelevant  DeclineReason = "not-relevant"
	DeclineWrongProject DeclineReason = "wrong-project"
	DeclineAlreadyKnown DeclineReason = "already-known"
)

// RecallOffer is the set of recall candidates surfaced at turn close,
// ranked best-first and capped at recallOfferK. Indexes into Candidates
// are stable for a RecallResolution to reference.
type RecallOffer struct {
	Candidates []measure.Result
}

// RecallResolution is the user's (or scripted harness's) verdict on a
// RecallOffer. Accept holds indexes into RecallOffer.Candidates of
// threads to pull into Layer B; any candidate not in Accept is declined,
// and Reason is recorded against the declined remainder.
type RecallResolution struct {
	Accept []int
	Reason DeclineReason
}

// RecallResolver is the seam between the recall stack and the
// experience layer. The chat REPL implements it interactively; the
// scenario harness implements it from a pre-declared script. A nil
// resolver on State means recall stays log-only — no offer surfaced.
type RecallResolver func(ctx context.Context, offer RecallOffer) (RecallResolution, error)

// surfaceRecallCandidates runs the §3.4 recall stack for this turn,
// logs the per-layer matches, and — when a RecallResolver is installed
// — surfaces the top recallOfferK candidates as an offer the caller
// resolves into accept/decline decisions. With no resolver it stays
// log-only (the harness default for unmeasured steps).
//
// On accept, applyRecallResolution increments SpineRecord.RecallFires
// (and the mirrored ThreadMeta.RecallFires) via
// MemoryOps.RecordRecallFire — the §2.2 "matches that resulted in
// fetch" counter. A counter-write failure is logged and swallowed; it
// never aborts turn close.
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

	if state.RecallResolver == nil || len(results) == 0 {
		return nil
	}
	offer := RecallOffer{Candidates: results[:min(len(results), recallOfferK)]}
	if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "offer", fmt.Sprintf("count=%d", len(offer.Candidates))); err != nil {
		return fmt.Errorf("log recall.offer: %w", err)
	}
	resolution, err := state.RecallResolver(ctx, offer)
	if err != nil {
		return fmt.Errorf("recall: resolve offer: %w", err)
	}
	return applyRecallResolution(ctx, state, offer, resolution)
}

// applyRecallResolution pulls accepted candidates into Layer B and logs
// an accept/decline verdict against every offered candidate. Declined
// candidates carry res.Reason (defaulting to DeclineNotRelevant when
// empty).
func applyRecallResolution(ctx context.Context, state *State, offer RecallOffer, res RecallResolution) error {
	accepted := make(map[int]bool, len(res.Accept))
	for _, i := range res.Accept {
		if i < 0 || i >= len(offer.Candidates) {
			return fmt.Errorf("recall: resolution accept index %d out of range [0,%d)", i, len(offer.Candidates))
		}
		accepted[i] = true
	}
	for i, c := range offer.Candidates {
		if accepted[i] {
			// A recall-accept promotion is a working-window mutation, so it
			// goes through the SAME §3.0.5 fetch chokepoint every other Layer-B
			// fetch does (G-F3 / "no gaps"): fetchThroughChain loads the thread,
			// declines a cross-project candidate (§3.2 — an embedding/intra hit
			// can name a foreign thread the symbolic project filter never saw,
			// so the accept path needs the same guard the §5.5 reprompt has),
			// fires the thread.fetched delta through onContextDelta, and
			// promotes into Layer B (recording the §2.7.3 recallSurfaced origin
			// — an accept runs at turn-close, so its attribution lands one turn
			// later). On any outcome that prevents promotion (fetch-miss or
			// cross-project decline, both already logged) it returns false and
			// we skip this candidate — no accept log, no RecallFires bump —
			// exactly as the reprompt path skips an unpromotable thread.
			if !fetchThroughChain(ctx, state, c.ThreadID) {
				continue
			}
			if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "accept",
				fmt.Sprintf("thr=%s layers=%s", c.ThreadID, strings.Join(c.Layers(), "+"))); err != nil {
				return fmt.Errorf("log recall.accept: %w", err)
			}
			// An accept is a fetch — bump the §2.2 RecallFires counter.
			// A counter-write failure must not abort turn close or skip
			// the remaining accepted threads: log it and continue.
			if err := state.Ops.RecordRecallFire(ctx, c.ThreadID); err != nil {
				_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "fire-error",
					"thr="+c.ThreadID+" err="+memops.SanitizeDetail(err.Error()))
			}
			continue
		}
		reason := res.Reason
		if reason == "" {
			reason = DeclineNotRelevant
		}
		if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "decline",
			fmt.Sprintf("thr=%s reason=%s", c.ThreadID, reason)); err != nil {
			return fmt.Errorf("log recall.decline: %w", err)
		}
	}
	return nil
}
