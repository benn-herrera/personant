package workset

// Budget describes the per-layer byte allocation for the working set
// (spec §3.1, parameters from §2.6.1).
//
// v0.1 uses bytes as a token proxy. Real tokenizer integration is
// deferred until empirical pressure requires it (see §6.5).
//
// Total = LayerE + LayerA1 + LayerA2 + LayerB + LayerC + CurrentTurn.
// CurrentTurn is the user-input + model-response budget, not part of
// the system prompt; it is tracked in Budget for accounting symmetry
// with §2.6.1's `layer.budget.percentages` and is not consumed by
// Compose.
type Budget struct {
	Total int // total context byte-budget

	LayerE      int // directives + conventions (§2.6.1: E=8% of Total)
	LayerA1     int // current project's spine display (§2.6.1: A1=8%)
	LayerA2     int // cross-project digests (§2.6.1: variable; bounded per-project)
	LayerB      int // active thread bodies (§2.6.1: B=50%)
	LayerC      int // dormant thread summaries (§2.6.1: C=15%)
	CurrentTurn int // user input + model response space (§2.6.1: current_turn=15%)

	// BTopK is the max number of threads in Layer B (§2.6.1's
	// layer.b-top-k; default 3).
	BTopK int

	// PerProjectDigestBytes caps a single project's A2 line (§2.6.1's
	// cross-project.digest-per-project-bytes; default 150).
	PerProjectDigestBytes int
}

// DefaultByteBudget is the v0.1 total context byte-budget. ~16K tokens
// at ~4 chars/token. Will be exposed to the directive layer as
// `context.byte-budget` once directive plumbing lands in Phase 3+.
const DefaultByteBudget = 65536

// Default percentage allocations for each layer (§2.6.1
// layer.budget.percentages). LayerA2 is "variable" in the spec; we
// allocate the residue (100% - sum of fixed layers) to A2 so the layers
// total exactly DefaultByteBudget.
const (
	defaultPctLayerE      = 8
	defaultPctLayerA1     = 8
	defaultPctLayerB      = 50
	defaultPctLayerC      = 15
	defaultPctCurrentTurn = 15
	// A2 is the residue: 100 - 8 - 8 - 50 - 15 - 15 = 4%.
	defaultPctLayerA2 = 100 - defaultPctLayerE - defaultPctLayerA1 -
		defaultPctLayerB - defaultPctLayerC - defaultPctCurrentTurn

	// DefaultBTopK matches §2.6.1's layer.b-top-k.
	DefaultBTopK = 3

	// DefaultPerProjectDigestBytes matches §2.6.1's
	// cross-project.digest-per-project-bytes.
	DefaultPerProjectDigestBytes = 150
)

// DefaultBudget returns a Budget computed from DefaultByteBudget at the
// §2.6.1 percentages. Rounding is integer truncation; the layers sum
// to ≤ Total (any rounding remainder is discarded rather than padded
// onto an arbitrary layer, so the budget never overshoots).
func DefaultBudget() Budget {
	total := DefaultByteBudget
	return Budget{
		Total:                 total,
		LayerE:                total * defaultPctLayerE / 100,
		LayerA1:               total * defaultPctLayerA1 / 100,
		LayerA2:               total * defaultPctLayerA2 / 100,
		LayerB:                total * defaultPctLayerB / 100,
		LayerC:                total * defaultPctLayerC / 100,
		CurrentTurn:           total * defaultPctCurrentTurn / 100,
		BTopK:                 DefaultBTopK,
		PerProjectDigestBytes: DefaultPerProjectDigestBytes,
	}
}
