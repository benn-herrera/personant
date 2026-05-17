package turn

import (
	"context"
	"fmt"
	"strings"

	"personant/internal/recall"
)

// surfaceRecallCandidates runs the §3.4 recall stack for this turn and
// logs the matches. v0.1 only logs; there is no UI surface and no
// SpineRecord.RecallFires increment (that field reserves "matches that
// resulted in fetch" per §2.2; until a UI surface lands a match isn't
// a fetch).
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
	return nil
}
