package web

import (
	"context"
	"fmt"
	"sync"

	"personant/internal/clock"
	"personant/internal/tools"
)

// The LOCAL query cap.
//
// It is enforced HERE, in our code, and not by trusting a provider
// dashboard to be watched. A metered API plus a model in a retry loop is
// a real and cheap-to-hit failure mode — this project has already seen
// loop artifacts in the serving layer — and the dashboard tells you about
// it after the budget is gone.
//
// Scope, stated honestly: the counters live in the PROCESS. A per-turn
// cap is exact (turn numbers are monotonic within a session, and the
// per-turn counter resets when it sees a new one). A per-day cap holds
// for the running session and resets if personant is restarted. That is
// the intended trade: persisting a quota counter would mean a new
// substrate file, a port method to read and write it, and its own
// recovery semantics — real cost against a failure mode (a user
// relaunching repeatedly to defeat their own cap) that is not the one
// being defended against. The runaway-loop case is intra-session, and
// intra-session is where this is exact.
const (
	// DefaultMaxSearchesPerTurn bounds one turn. The turn loop already
	// caps tool ROUNDS at 4, but a single round may carry any number of
	// calls, so rounds alone bound nothing.
	DefaultMaxSearchesPerTurn = 5

	// DefaultMaxSearchesPerDay bounds a day of sessions. Generous for a
	// person working with an assistant, and far under the free tier
	// (~20k/month ≈ 660/day) that a loop would otherwise eat in an hour.
	DefaultMaxSearchesPerDay = 100
)

// quota is the per-turn / per-day counter pair. It is safe for
// concurrent use: tool rounds run sequentially today, but a quota that
// silently miscounts under a future concurrent dispatch is a bug nobody
// would look for.
type quota struct {
	perTurn int
	perDay  int

	mu       sync.Mutex
	turn     int    // the turn the per-turn counter belongs to
	turnUsed int    //
	day      string // YYYY-MM-DD, from the timeline clock
	dayUsed  int
}

func newQuota(perTurn, perDay int) *quota {
	if perTurn <= 0 {
		perTurn = DefaultMaxSearchesPerTurn
	}
	if perDay <= 0 {
		perDay = DefaultMaxSearchesPerDay
	}
	return &quota{perTurn: perTurn, perDay: perDay}
}

// take consumes one unit or returns the refusal to hand back to the
// model. The refusal is an ERROR rather than a plain result on purpose:
// the turn loop already logs a handler error as §6.4 `tool.error`, so
// routing the cap through the same path gets it logged with no new event
// vocabulary and no logging seam threaded into this package.
func (q *quota) take(ctx context.Context) error {
	turn := tools.TurnFromContext(ctx)
	day := clock.Timeline().UTC().Format("2006-01-02")

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.day != day {
		q.day, q.dayUsed = day, 0
	}
	if q.turn != turn {
		q.turn, q.turnUsed = turn, 0
	}
	if q.dayUsed >= q.perDay {
		return capError("day", q.perDay, "the daily allowance resets at 00:00 UTC")
	}
	if q.turnUsed >= q.perTurn {
		return capError("turn", q.perTurn, "the per-turn allowance resets on your next turn")
	}
	q.turnUsed++
	q.dayUsed++
	return nil
}

func capError(window string, limit int, resets string) error {
	return fmt.Errorf("local query cap reached: personant allows %d %s searches per %s and the search was NOT performed. "+
		"This is personant's own limit, not the search provider's, and it says nothing about whether results exist. "+
		"Do not retry the search — %s. Answer from what you have and say so",
		limit, ToolNameSearch, window, resets)
}
