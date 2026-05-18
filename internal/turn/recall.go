package turn

import (
	"context"
	"fmt"
	"strings"

	"personant/internal/recall"
)

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
	Candidates []recall.Result
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
// (and the mirrored ThreadFrontmatter.RecallFires) via
// MemoryOps.RecordRecallFire — the §2.2 "matches that resulted in
// fetch" counter. A counter-write failure is logged and swallowed; it
// never aborts turn close.
//
// Recall runs entirely behind the recall.Recaller interface — this
// function knows nothing of symbolic Jaccard, embedding cosine, or the
// embedding index. It hands the Recaller the turn's symbols and text
// and logs whatever merged candidates come back, per layer.
//
// engagedSet (threads engaged this turn) is excluded — an engaged
// thread doesn't shadow the turn's own engagement signal. A Recaller
// error is non-fatal: the caller logs and swallows it (recall is
// opportunistic and must never abort the turn).
func surfaceRecallCandidates(ctx context.Context, state *State, userInput string, engagedSet map[string]struct{}) error {
	if state.Recaller == nil {
		return nil
	}
	results, err := state.Recaller.Recall(ctx, recall.Request{
		QuerySymbols: state.coalesce.symbolList(),
		QueryText:    userInput,
		Project:      state.ActiveProject.ID,
		Exclude:      engagedSet,
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
			if err := state.Ops.Log(ctx, "spine", "match-fire", details); err != nil {
				return fmt.Errorf("log spine.match-fire: %w", err)
			}
		}
		if r.Embedding != nil {
			details := fmt.Sprintf("%s score=%.3f query_chars=%d",
				r.ThreadID, r.Embedding.Score, queryChars)
			if err := state.Ops.Log(ctx, "spine", "embed-match-fire", details); err != nil {
				return fmt.Errorf("log spine.embed-match-fire: %w", err)
			}
		}
	}

	if state.RecallResolver == nil || len(results) == 0 {
		return nil
	}
	offer := RecallOffer{Candidates: results[:min(len(results), recallOfferK)]}
	if err := state.Ops.Log(ctx, "recall", "offer", fmt.Sprintf("count=%d", len(offer.Candidates))); err != nil {
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
			promoteToLayerB(state, c.ThreadID)
			if err := state.Ops.Log(ctx, "recall", "accept",
				fmt.Sprintf("thr=%s layers=%s", c.ThreadID, strings.Join(c.Layers(), "+"))); err != nil {
				return fmt.Errorf("log recall.accept: %w", err)
			}
			// An accept is a fetch — bump the §2.2 RecallFires counter.
			// A counter-write failure must not abort turn close or skip
			// the remaining accepted threads: log it and continue.
			if err := state.Ops.RecordRecallFire(ctx, c.ThreadID); err != nil {
				_ = state.Ops.Log(ctx, "recall", "fire-error",
					"thr="+c.ThreadID+" err="+sanitizeDetail(err.Error()))
			}
			continue
		}
		reason := res.Reason
		if reason == "" {
			reason = DeclineNotRelevant
		}
		if err := state.Ops.Log(ctx, "recall", "decline",
			fmt.Sprintf("thr=%s reason=%s", c.ThreadID, reason)); err != nil {
			return fmt.Errorf("log recall.decline: %w", err)
		}
	}
	return nil
}
