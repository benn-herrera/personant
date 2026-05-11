package turn

import (
	"context"
	"fmt"
	"strings"

	"personant/internal/memops"
)

// surfaceRecallCandidates runs the §3.4 cheap-pre-filter (symbolic
// Jaccard) over this turn's coalesced symbol set and logs each
// candidate as a spine.match-fire event. v0.1 only logs; no UI
// surface and no SpineRecord.RecallFires increment (that field
// reserves "matches that resulted in fetch" per §2.2; until a UI
// surface lands a match isn't a fetch).
//
// engagedSet is the set of thread IDs already engaged in this turn —
// passed to memops.RecallOptions.Exclude so an engaged thread doesn't
// shadow the turn's own engagement signal. Threads in
// state.ActiveThreads/DormantThreads but not engaged this turn are
// NOT excluded; that is the point of opportunistic recall.
//
// On error: returns it. The caller treats recall failure as
// non-fatal (recall is opportunistic) and logs/swallows. Empty
// candidate list: no events, no error.
func surfaceRecallCandidates(ctx context.Context, state *State, engagedSet map[string]struct{}) error {
	query := state.coalesce.symbolList()
	candidates, err := state.Ops.ProposeRecall(ctx, query, memops.RecallOptions{
		Project: state.ActiveProject.ID,
		Exclude: engagedSet,
	})
	if err != nil {
		return fmt.Errorf("recall propose: %w", err)
	}
	querySize := len(state.coalesce.symbols)
	for _, c := range candidates {
		details := fmt.Sprintf("%s score=%.2f matched=%s query_size=%d",
			c.ThreadID,
			c.Score,
			strings.Join(c.MatchedSymbols, ","),
			querySize,
		)
		if err := state.Ops.Log(ctx, "spine", "match-fire", details); err != nil {
			return fmt.Errorf("log spine.match-fire: %w", err)
		}
	}
	return nil
}
