package memops

import "testing"

func TestDefaultBudgetSumsToTotal(t *testing.T) {
	b := DefaultBudget()
	sum := b.LayerE + b.LayerA1 + b.LayerA2 + b.LayerB + b.LayerC + b.CurrentTurn
	if sum > b.Total {
		t.Fatalf("layer sum %d exceeds total %d", sum, b.Total)
	}
	// Integer truncation can lose at most 5 bytes (one per percentage
	// component); the layers should still account for nearly all of the
	// total budget. A larger gap means the percentage table or the
	// integer-rounding contract has shifted.
	if b.Total-sum > 6 {
		t.Errorf("rounding loss too high: total=%d sum=%d", b.Total, sum)
	}
}

func TestDefaultBudgetPercentagesMatchSpec(t *testing.T) {
	b := DefaultBudget()
	cases := map[string]struct {
		got, want int
	}{
		"E":            {b.LayerE, b.Total * 8 / 100},
		"A1":           {b.LayerA1, b.Total * 8 / 100},
		"B":            {b.LayerB, b.Total * 50 / 100},
		"C":            {b.LayerC, b.Total * 15 / 100},
		"current_turn": {b.CurrentTurn, b.Total * 15 / 100},
	}
	for layer, c := range cases {
		if c.got != c.want {
			t.Errorf("layer %s: got %d want %d", layer, c.got, c.want)
		}
	}
	// A2 is the residue.
	expectedA2 := b.Total * defaultPctLayerA2 / 100
	if b.LayerA2 != expectedA2 {
		t.Errorf("LayerA2: got %d want %d (residue percentage %d)",
			b.LayerA2, expectedA2, defaultPctLayerA2)
	}
	if defaultPctLayerA2 <= 0 {
		t.Errorf("residue percentage must be positive; got %d", defaultPctLayerA2)
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
	if b.Total != DefaultByteBudget {
		t.Errorf("Total: got %d want %d", b.Total, DefaultByteBudget)
	}
}
