package turn

import (
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
)

// budgetWithLiveTurn returns a Budget whose LiveTurnReserve is exactly r
// bytes, so the sub-share arithmetic is testable in isolation.
func budgetWithLiveTurn(r int) memops.Budget {
	return memops.Budget{LiveTurnReserve: r, TokenCeiling: 1000}
}

func TestLiveTurnSharesSumWithinReserve(t *testing.T) {
	b := budgetWithLiveTurn(1000)
	u, h, tr := liveTurnShares(b)
	if u <= 0 || h <= 0 || tr <= 0 {
		t.Fatalf("shares must be positive: u=%d h=%d tr=%d", u, h, tr)
	}
	// I2: the three shares partition the reserve, never exceed it.
	if u+h+tr > b.LiveTurnReserve {
		t.Errorf("shares %d+%d+%d exceed reserve %d", u, h, tr, b.LiveTurnReserve)
	}
}

func TestLiveTurnSharesZeroReserveFailsOpen(t *testing.T) {
	u, h, tr := liveTurnShares(memops.Budget{})
	if u != 0 || h != 0 || tr != 0 {
		t.Errorf("zero reserve must yield zero shares: %d %d %d", u, h, tr)
	}
}

// checkUserInput: reject when user-authored input exceeds its share;
// accept when within; fail open on an unset budget.
func TestCheckUserInputReject(t *testing.T) {
	b := budgetWithLiveTurn(1000) // userInput share = 500
	share, _, _ := liveTurnShares(b)

	within := strings.Repeat("a", share)
	if err := checkUserInput(within, b); err != nil {
		t.Errorf("input == share should pass: %v", err)
	}

	over := strings.Repeat("a", share+1)
	err := checkUserInput(over, b)
	if err == nil {
		t.Fatalf("input > share must reject")
	}
	if !IsInputExceedsBudget(err) {
		t.Errorf("reject must be classifiable: %v", err)
	}
	// User-facing message names the budget guidance.
	if !strings.Contains(err.Error(), "exceeds the context budget") {
		t.Errorf("message not user-facing: %q", err.Error())
	}

	// Unset budget fails open (pre-#127 unbounded behavior).
	if err := checkUserInput(strings.Repeat("a", 1<<20), memops.Budget{}); err != nil {
		t.Errorf("unset budget must fail open: %v", err)
	}
}

// boundTaskResultDeltas truncates oversized task-class result content
// with a marker, leaves within-budget content and non-task-result deltas
// untouched, and does not mutate the input slice.
//
// The user.shell-capture arm is the §4.4 regression case: the bound
// originally covered tool.result ONLY, so a `# cat bigfile` blew the
// current-turn budget unbounded despite §2.8 marking user.shell-capture
// "subject to §6.5 byte cap". The fs.write arm is its counterweight — a
// task-class source that must NOT be truncated, because that Content is
// the file body applyFileEdit stores, not model-window filler.
func TestBoundTaskResultDeltas(t *testing.T) {
	b := budgetWithLiveTurn(1000) // task-result share = 150
	_, _, share := liveTurnShares(b)

	big := strings.Repeat("x", share*4)
	in := []Delta{
		{Source: memops.SourceToolResult, Content: big},
		{Source: memops.SourceUserShellCapture, Content: big},
		{Source: memops.SourceToolResult, Content: "small"},
		{Source: memops.SourceFSWrite, Content: big}, // file body → never truncated
		{Source: memops.SourceUserPrompt, Content: big},
	}
	out := boundTaskResultDeltas(in, b)

	for _, i := range []int{0, 1} {
		if len(out[i].Content) > share {
			t.Errorf("delta %d (%s) not bounded: %d > %d", i, out[i].Source, len(out[i].Content), share)
		}
		if !strings.Contains(out[i].Content, "truncated") {
			t.Errorf("delta %d (%s) truncation not marked: %q", i, out[i].Source, out[i].Content)
		}
	}
	if out[2].Content != "small" {
		t.Errorf("within-budget tool.result must be untouched")
	}
	if out[3].Content != big {
		t.Errorf("fs.write content must be untouched — it is the stored file body")
	}
	if out[4].Content != big {
		t.Errorf("decision-class delta must be untouched")
	}
	// Input slice unmutated.
	if len(in[0].Content) != len(big) {
		t.Errorf("input slice mutated")
	}
}

// boundHistoryTail keeps the most-recent pairs that fit the history-tail
// share, drops the oldest, and stays pair-aligned (I5).
func TestBoundHistoryTailRecencyBoundPairAligned(t *testing.T) {
	b := budgetWithLiveTurn(1000) // history-tail share = 350
	_, share, _ := liveTurnShares(b)

	// Six pairs, each ~100 bytes → 12 messages, ~600 bytes total. The
	// share (350) fits the most-recent 3 pairs (~300), drops the oldest 3.
	const pairs = 6
	per := strings.Repeat("u", 50)
	pera := strings.Repeat("a", 50)
	var hist []model.Message
	for i := 0; i < pairs; i++ {
		hist = append(hist,
			model.Message{Role: "user", Content: per + itoaThreadID(i)},
			model.Message{Role: "assistant", Content: pera + itoaThreadID(i)},
		)
	}
	out := boundHistoryTail(hist, b)

	if len(out)%2 != 0 {
		t.Fatalf("result not pair-aligned: len=%d", len(out))
	}
	// Roles still alternate user/assistant.
	for i := 0; i < len(out); i += 2 {
		if out[i].Role != "user" || out[i+1].Role != "assistant" {
			t.Fatalf("pair %d not user/assistant: %q/%q", i/2, out[i].Role, out[i+1].Role)
		}
	}
	// Kept the recent suffix, dropped the oldest.
	if len(out) >= len(hist) {
		t.Errorf("expected truncation: kept %d of %d", len(out), len(hist))
	}
	// The most-recent pair (index 5) survives; the oldest (index 0) drops.
	last := out[len(out)-1].Content
	if !strings.HasSuffix(last, itoaThreadID(pairs-1)) {
		t.Errorf("most-recent pair not retained; last=%q", last)
	}
	for _, m := range out {
		if strings.HasSuffix(m.Content, itoaThreadID(0)) {
			t.Errorf("oldest pair should have been dropped: %q", m.Content)
		}
	}

	// Byte size of the kept tail is within the share.
	used := 0
	for _, m := range out {
		used += len(m.Content)
	}
	if used > share {
		t.Errorf("kept tail %d exceeds share %d", used, share)
	}

	// Unset budget fails open (returns full history).
	if got := boundHistoryTail(hist, memops.Budget{}); len(got) != len(hist) {
		t.Errorf("unset budget must return full history: got %d want %d", len(got), len(hist))
	}
}
