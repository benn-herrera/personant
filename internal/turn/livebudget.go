package turn

import (
	"fmt"
	"unicode/utf8"

	"personant/internal/memops"
	"personant/internal/model"
)

// Live-turn budget sub-policy (#127, design §3.2).
//
// The whole-request token ceiling (I1) is enforced by bounding every
// component the request is assembled from. workset.Compose bounds the
// memory layers (system prompt) in bytes; this file bounds the LIVE-TURN
// components workset never sees — the current user input, the replayed
// history tail, and current-turn tool.result deltas — against
// Budget.LiveTurnReserve. It NEVER reaches into the memory-layer budgets
// to borrow space (I2): the live turn and memory are reserved against
// each other, never silently traded.
//
// Bounding policy differs by component authorship (design §3.4 / Q3):
//   - userInput is USER-AUTHORED → an oversized paste is REJECTED with a
//     user-facing message, never silently truncated (the user decides
//     what to keep).
//   - the history tail and tool.result deltas are NON-user-authored (the
//     user cannot split them) → bounded by truncation/recency, not
//     rejected.

// ErrInputExceedsBudget is returned by RunWithInfo when a single
// user-authored userInput exceeds the live-turn reserve. Its message is
// user-facing (surfaced verbatim to the REPL); callers present it rather
// than treating it as an internal fault.
type inputExceedsBudgetError struct {
	inputBytes  int
	budgetBytes int
}

func (e *inputExceedsBudgetError) Error() string {
	return fmt.Sprintf(
		"input exceeds the context budget — filter it or split it into smaller turns (input %d bytes, budget %d bytes)",
		e.inputBytes, e.budgetBytes)
}

// IsInputExceedsBudget reports whether err is the oversize-user-input
// rejection (the §3.4/Q3 reject path). The REPL uses this to present the
// message as user guidance rather than a crash.
func IsInputExceedsBudget(err error) bool {
	_, ok := err.(*inputExceedsBudgetError)
	return ok
}

// Live-turn reserve sub-shares. The reserve is split so the three
// live-turn components are reserved against each other (none can starve
// the others), mirroring how the memory layers split the system-prompt
// budget. userInput gets the largest share (it carries the turn's
// intent); the history tail and tool-result deltas get the remainder.
// These are calibration starting points (§9.4), not load-bearing
// constants — only their sum (= LiveTurnReserve) is the I2 guarantee.
const (
	liveTurnPctUserInput   = 50
	liveTurnPctHistoryTail = 35
	liveTurnPctToolResult  = 15
)

// liveTurnShares carves Budget.LiveTurnReserve into the three component
// shares. A zero reserve (uninitialized budget) returns zero shares; the
// caller treats a zero userInput share as "no bound configured" and
// neither rejects nor truncates (fail-open, matching the pre-#127
// behavior of an unset budget).
func liveTurnShares(b memops.Budget) (userInput, historyTail, toolResult int) {
	r := b.LiveTurnReserve
	if r <= 0 {
		return 0, 0, 0
	}
	userInput = r * liveTurnPctUserInput / 100
	historyTail = r * liveTurnPctHistoryTail / 100
	toolResult = r * liveTurnPctToolResult / 100
	return userInput, historyTail, toolResult
}

// checkUserInput enforces the §3.4/Q3 reject policy on user-authored
// input: if userInput's byte length exceeds its live-turn share, it
// returns inputExceedsBudgetError. A zero share (unset budget) fails
// open. This is the only live-turn component that rejects; the rest are
// bounded by truncation/recency below.
func checkUserInput(userInput string, b memops.Budget) error {
	share, _, _ := liveTurnShares(b)
	if share <= 0 {
		return nil
	}
	if len(userInput) > share {
		return &inputExceedsBudgetError{inputBytes: len(userInput), budgetBytes: share}
	}
	return nil
}

// boundToolResultDeltas truncates-with-marker the content of current-turn
// tool.result deltas so a single verbose tool result (design §1 m1)
// cannot balloon the request via the chain. It is bounded truncation, not
// rejection — the content is non-user-authored (the user cannot split a
// tool's output), so honesty-with-marker beats interruption. Returns a
// new slice; the input is not mutated. A zero share fails open.
func boundToolResultDeltas(deltas []Delta, b memops.Budget) []Delta {
	_, _, share := liveTurnShares(b)
	if share <= 0 || len(deltas) == 0 {
		return deltas
	}
	out := make([]Delta, len(deltas))
	copy(out, deltas)
	for i := range out {
		if out[i].Source != memops.SourceToolResult {
			continue
		}
		if len(out[i].Content) > share {
			out[i].Content = truncateWithMarker(out[i].Content, share)
		}
	}
	return out
}

// boundHistoryTail recency-bounds the replayed history to the history-tail
// share: it keeps the most-recent user/assistant pairs whose cumulative
// byte size fits the share, dropping the oldest pairs first. The result
// stays pair-aligned (I5) — the LLM-protocol alternation invariant is
// preserved. history is assumed even-length and user/assistant-alternating
// (RunWithInfo's append + FIFO cap maintain this). A zero share fails
// open (returns history unchanged).
func boundHistoryTail(history []model.Message, b memops.Budget) []model.Message {
	_, share, _ := liveTurnShares(b)
	if share <= 0 || len(history) == 0 {
		return history
	}
	// Walk newest→oldest in pairs, accumulating bytes; keep the longest
	// recent suffix that fits. pairBytes(i) is the byte cost of the pair
	// at history[i], history[i+1].
	used := 0
	keepFrom := len(history)
	for i := len(history) - 2; i >= 0; i -= 2 {
		cost := len(history[i].Content) + len(history[i+1].Content)
		if used+cost > share {
			break
		}
		used += cost
		keepFrom = i
	}
	if keepFrom == 0 {
		return history
	}
	// Copy the kept suffix into a fresh slice so the returned value does
	// not alias state.History's backing array.
	tail := history[keepFrom:]
	out := make([]model.Message, len(tail))
	copy(out, tail)
	return out
}

// truncateWithMarker cuts s to a rune boundary that fits within budget
// bytes and appends a truncation marker. It mirrors workset.truncateToBudget's
// degrade-not-corrupt contract (I5) for live-turn content. A non-positive
// budget returns "".
func truncateWithMarker(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(s) <= budget {
		return s
	}
	marker := fmt.Sprintf("\n... [tool result truncated; budget=%d bytes] ...\n", budget)
	if len(marker) >= budget {
		return truncateRunes(marker, budget)
	}
	return truncateRunes(s, budget-len(marker)) + marker
}

// truncateRunes returns the longest prefix of s whose byte length is ≤ n
// that ends on a UTF-8 rune boundary.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
