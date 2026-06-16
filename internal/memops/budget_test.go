package memops

import "testing"

// The memory-side shares are percentages of the MEMORY byte budget
// (Total − LiveTurnReserve), per #127's re-denomination. The live-turn
// reserve is carved from Total first.
func memoryBudget(b Budget) int { return b.Total - b.LiveTurnReserve }

func TestDefaultBudgetSumsToTotal(t *testing.T) {
	b := DefaultBudget()
	sum := b.LayerE + b.LayerA1 + b.LayerA2 + b.LayerB + b.LayerC + b.LiveTurnReserve
	if sum > b.Total {
		t.Fatalf("layer+reserve sum %d exceeds total %d", sum, b.Total)
	}
	// Integer truncation can lose a few bytes per percentage component
	// (one carve for live-turn, five for the memory partition). The shares
	// should still account for nearly all of the byte total. A larger gap
	// means the percentage table or the integer-rounding contract shifted.
	if b.Total-sum > 8 {
		t.Errorf("rounding loss too high: total=%d sum=%d", b.Total, sum)
	}
}

func TestDefaultBudgetPercentagesMatchSpec(t *testing.T) {
	b := DefaultBudget()
	mem := memoryBudget(b)
	// Live-turn reserve is 15% of Total.
	if want := b.Total * defaultPctLiveTurn / 100; b.LiveTurnReserve != want {
		t.Errorf("LiveTurnReserve: got %d want %d", b.LiveTurnReserve, want)
	}
	cases := map[string]struct {
		got, want int
	}{
		"E":  {b.LayerE, mem * defaultPctLayerE / 100},
		"A1": {b.LayerA1, mem * defaultPctLayerA1 / 100},
		"B":  {b.LayerB, mem * defaultPctLayerB / 100},
		"C":  {b.LayerC, mem * defaultPctLayerC / 100},
	}
	for layer, c := range cases {
		if c.got != c.want {
			t.Errorf("layer %s: got %d want %d", layer, c.got, c.want)
		}
	}
	// A2 is the residue of the 18% high tier (high − A1).
	if want := mem * defaultPctLayerA2 / 100; b.LayerA2 != want {
		t.Errorf("LayerA2: got %d want %d (residue percentage %d)", b.LayerA2, want, defaultPctLayerA2)
	}
	if defaultPctLayerA2 <= 0 {
		t.Errorf("residue percentage must be positive; got %d", defaultPctLayerA2)
	}
	// A1 + A2 form the 18% high tier.
	if defaultPctLayerA1+defaultPctLayerA2 != defaultPctHighA {
		t.Errorf("high tier mismatch: A1(%d)+A2(%d) != %d",
			defaultPctLayerA1, defaultPctLayerA2, defaultPctHighA)
	}
	// Memory partition totals 100%.
	if got := defaultPctLayerE + defaultPctHighA + defaultPctLayerC + defaultPctLayerB; got != 100 {
		t.Errorf("memory partition does not total 100%%: got %d", got)
	}
}

func TestDefaultBudgetCapsArePositive(t *testing.T) {
	b := DefaultBudget()
	if b.BTopK <= 0 {
		t.Errorf("BTopK must be positive; got %d", b.BTopK)
	}
	if b.PerProjectDigestBytes <= 0 {
		t.Errorf("PerProjectDigestBytes must be positive; got %d", b.PerProjectDigestBytes)
	}
}

// TestDefaultBudgetDerivedFromTokenCeiling certifies the byte Total is
// derived from the token ceiling via the conservative ratio (I4): the
// token ceiling is the authoritative number, bytes are derived.
func TestDefaultBudgetDerivedFromTokenCeiling(t *testing.T) {
	b := DefaultBudget()
	if b.TokenCeiling != DefaultTokenCeiling {
		t.Errorf("TokenCeiling: got %d want %d", b.TokenCeiling, DefaultTokenCeiling)
	}
	want := int(float64(DefaultTokenCeiling) * bytesPerTokenConservative)
	if b.Total != want {
		t.Errorf("Total: got %d want %d (ceiling %d × %.1f B/tok)",
			b.Total, want, DefaultTokenCeiling, bytesPerTokenConservative)
	}
}

// A6 — response headroom (I3): TokenCeiling + ResponseReserve ≤ window,
// and ResponseReserve equals the request's MaxTokens (DefaultRequest uses
// 16384, mirrored by DefaultResponseReserveTokens). Cheap partition
// arithmetic, no model needed.
func TestA6_ResponseHeadroom(t *testing.T) {
	b := DefaultBudget()
	if b.ResponseReserveTokens != DefaultResponseReserveTokens {
		t.Errorf("ResponseReserveTokens: got %d want %d", b.ResponseReserveTokens, DefaultResponseReserveTokens)
	}
	// The gemma-4 model window the ceiling is sized below.
	const window = 256 * 1024
	if b.TokenCeiling+b.ResponseReserveTokens > window {
		t.Errorf("TokenCeiling(%d) + ResponseReserve(%d) = %d exceeds window %d",
			b.TokenCeiling, b.ResponseReserveTokens, b.TokenCeiling+b.ResponseReserveTokens, window)
	}
	// The reserve must be a real headroom below the ceiling, not zero.
	if b.ResponseReserveTokens <= 0 {
		t.Errorf("ResponseReserveTokens must be positive; got %d", b.ResponseReserveTokens)
	}
}

// A7 — spec/code agreement: the percentage constants must equal the
// values SPEC §2.6.1 layer.budget.percentages documents
// ({E:12, A1:10, A2:variable, B:55, C:15, live_turn:15}). A mismatch is
// the m4-class doc/code drift the review flagged; keep this in lockstep
// with the SPEC edit.
func TestA7_SpecPercentagesMatchCode(t *testing.T) {
	want := map[string]int{
		"E":         12,
		"A1":        10,
		"B":         55,
		"C":         15,
		"live_turn": 15,
		"high":      18, // A1 + A2 high tier
	}
	got := map[string]int{
		"E":         defaultPctLayerE,
		"A1":        defaultPctLayerA1,
		"B":         defaultPctLayerB,
		"C":         defaultPctLayerC,
		"live_turn": defaultPctLiveTurn,
		"high":      defaultPctHighA,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: code=%d spec=%d (SPEC §2.6.1 and the code have drifted)", k, got[k], w)
		}
	}
}

// BudgetForCeiling is the single source of truth for the partition (I7):
// an explicit ceiling produces a proportionally-scaled budget, and a
// non-positive ceiling falls back to the default.
func TestBudgetForCeiling(t *testing.T) {
	small := BudgetForCeiling(100000)
	big := BudgetForCeiling(400000)
	if small.TokenCeiling != 100000 || big.TokenCeiling != 400000 {
		t.Fatalf("ceiling not honored: small=%d big=%d", small.TokenCeiling, big.TokenCeiling)
	}
	// 4× ceiling → ~4× byte Total (exact under the integer ratio).
	if big.Total != 4*small.Total {
		t.Errorf("Total not proportional to ceiling: small=%d big=%d", small.Total, big.Total)
	}
	// Non-positive ceiling falls back to the default.
	fallback := BudgetForCeiling(0)
	if fallback.TokenCeiling != DefaultTokenCeiling {
		t.Errorf("zero ceiling should fall back to default: got %d", fallback.TokenCeiling)
	}
}
