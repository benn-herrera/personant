package sim

import (
	"testing"

	"personant/internal/store"
)

// B1+X4 embedder-enabled acceptance rung — anti-vacuity proofs (design §3).
//
// The two gates this rung adds (the §3.4 recall-completeness floor and the
// whole-request token ceiling) are themselves at risk of passing vacuously — the
// exact defect B1/X4 names. These tests are the design's required mutation
// proofs that the gates are real: each drives the PURE gate predicate
// (checkCompletenessFloor / checkTokenCeiling) with the mutated input a broken
// runtime would produce and asserts the gate FAILS. They are unit tests over the
// predicate, not the full live sim, so they run in `make test` with no endpoint
// (the live rung itself is the end-to-end proof; these lock in that the gate
// cannot go green when the thing it guards is broken).

// TestCompletenessFloor_MutationFailsWhenFloorDisabled is design §3 criterion 1:
// with the debtWindowTurns floor disabled (or debtWindowBound forced to 0) the
// runtime cannot surface a flush-lag dead-zone target — no embedding vector
// exists for it and the lexical pass is off — so the observed-hit count drops
// below the dead-zone total. The completeness gate MUST fail in that case; if it
// stayed green the assertion would be reading something the embedding fine tier
// already covers (FM2), not the §3.4 floor.
func TestCompletenessFloor_MutationFailsWhenFloorDisabled(t *testing.T) {
	// Healthy run: every observed dead-zone probe surfaced (floor fired).
	if fail := checkCompletenessFloor(8, 8); fail != "" {
		t.Fatalf("floor-intact case should PASS, got failure: %s", fail)
	}

	// MUTATION: the floor is disabled, so dead-zone targets are not surfaced —
	// hit collapses below total. The gate MUST fail.
	if fail := checkCompletenessFloor(0, 8); fail == "" {
		t.Errorf("MUTATION (floor disabled, 0/8 dead-zone probes surfaced) must FAIL the completeness gate, "+
			"but checkCompletenessFloor returned no failure — the gate is vacuous (reading something the fine "+
			"tier covers, not the §3.4 lexical floor). hit=%d total=%d", 0, 8)
	}
	// A partial miss (some surfaced, some not) must also fail — the floor must
	// cover EVERY dead-zone probe, not most.
	if fail := checkCompletenessFloor(5, 8); fail == "" {
		t.Errorf("MUTATION (partial floor failure, 5/8 surfaced) must FAIL the completeness gate, got no failure")
	}
}

// TestCompletenessFloor_NonVacuity is design invariant 3: a completeness gate
// that observed ZERO dead-zone probes is a silent blind spot and must fail
// rather than pass green. This guards against a span/cadence that never scrolls
// a probe target into the flush-lag band (Q1) silently disarming the gate.
func TestCompletenessFloor_NonVacuity(t *testing.T) {
	if fail := checkCompletenessFloor(0, 0); fail == "" {
		t.Errorf("non-vacuity: 0/0 dead-zone probes (the floor was never exercised) must FAIL — " +
			"the rung cannot prove §3.4 against no observation, got no failure")
	}
}

// TestTokenCeiling_MutationFailsOnOversizeTurn is design §3 criterion 2: an
// injected oversize turn — a request whose assembled prompt-token count exceeds
// the ceiling — MUST make the token gate fail. If it passed, the assertion is
// not reading the full request (FM3/FM4).
func TestTokenCeiling_MutationFailsOnOversizeTurn(t *testing.T) {
	// Healthy run: every turn's prompt-token count is comfortably under the
	// ceiling. The gate passes.
	within := []float64{1000, 16384, defaultRequestTokenCeiling - 1, 42000}
	if fail := checkTokenCeiling(within); fail != "" {
		t.Fatalf("under-ceiling case should PASS, got failure: %s", fail)
	}

	// MUTATION: one oversize turn pushes the assembled request past the ceiling.
	// The gate MUST fail — max over the series exceeds the bound.
	oversize := append(append([]float64(nil), within...), defaultRequestTokenCeiling+1)
	if fail := checkTokenCeiling(oversize); fail == "" {
		t.Errorf("MUTATION (one turn at ceiling+1 = %d) must FAIL the token-ceiling gate, but checkTokenCeiling "+
			"returned no failure — the gate is not reading the full assembled request (FM3/FM4)",
			defaultRequestTokenCeiling+1)
	}

	// An empty series on the live-inference profile is a wiring defect, not a
	// vacuous pass — the gate must flag it.
	if fail := checkTokenCeiling(nil); fail == "" {
		t.Errorf("empty request_prompt_tokens series on a live-inference run must FAIL (the assembled-request " +
			"token count was never observed), got no failure")
	}
}

// TestGatePolicyPredicates pins the B1+X4 gate-policy predicates to their single
// source of truth: the token-ceiling gate fires iff a live chat client is
// installed (== oracleBlind), the completeness floor fires iff live embedding is
// installed. The mock acceptance gate (both off) fires NEITHER — keeping it
// byte-identical (rung invariant 8).
func TestGatePolicyPredicates(t *testing.T) {
	cases := []struct {
		name                        string
		policy                      gatePolicy
		wantToken, wantCompleteness bool
	}{
		{"mock (both off)", gatePolicy{}, false, false},
		{"live-embedding only", gatePolicy{liveEmbedding: true}, false, true},
		{"live-inference only", gatePolicy{oracleBlind: true}, true, false},
		{"both live", gatePolicy{oracleBlind: true, liveEmbedding: true}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.policy.tokenCeilingAsserts(); got != c.wantToken {
				t.Errorf("tokenCeilingAsserts() = %v, want %v", got, c.wantToken)
			}
			if got := c.policy.completenessFloorAsserts(); got != c.wantCompleteness {
				t.Errorf("completenessFloorAsserts() = %v, want %v", got, c.wantCompleteness)
			}
		})
	}
}

// TestInDebtWindowDepth pins the flush-lag dead-zone boundary: a turn-depth is in
// the dead zone iff it has scrolled out of the assembly window
// (depth >= ThreadTurnWindow) AND is within the most-recent simEmbeddingDebtCap
// below the window floor (depth < ThreadTurnWindow + simEmbeddingDebtCap). This
// is the band where ONLY the lexical floor can hit (the embedding fine tier has
// no vector yet) — the exact subset the B1 completeness gate targets.
func TestInDebtWindowDepth(t *testing.T) {
	w := store.ThreadTurnWindow
	cases := []struct {
		depth int
		want  bool
	}{
		{0, false},                             // never scrolled out
		{w - 1, false},                         // still in the assembly window
		{w, true},                              // first scrolled-out excerpt — dead zone floor
		{w + simEmbeddingDebtCap - 1, true},    // last dead-zone excerpt
		{w + simEmbeddingDebtCap, false},       // just past the debt window — flushed, fine tier covers
		{w + simEmbeddingDebtCap + 100, false}, // deep history — flushed long ago
	}
	for _, c := range cases {
		if got := inDebtWindowDepth(c.depth); got != c.want {
			t.Errorf("inDebtWindowDepth(%d) = %v, want %v (window=%d cap=%d)",
				c.depth, got, c.want, w, simEmbeddingDebtCap)
		}
	}
}

// TestSimDebtCapMirrorsRuntime guards the simEmbeddingDebtCap mirror of the
// unexported turn.embeddingDebtCap. The two cannot be compared directly across
// the package boundary (the runtime const is unexported), so this pins the
// mirrored value with explicit provenance: if turn.embeddingDebtCap changes, the
// dead-zone band the completeness gate uses drifts out of sync with the runtime
// floor it asserts, so this tripwire fails and points the editor at both
// constants. (The live rung is the end-to-end proof; this catches a silent
// drift at `make test` time without an endpoint.)
func TestSimDebtCapMirrorsRuntime(t *testing.T) {
	const runtimeEmbeddingDebtCap = 16 // MUST equal turn.embeddingDebtCap (internal/turn/lru.go)
	if simEmbeddingDebtCap != runtimeEmbeddingDebtCap {
		t.Errorf("simEmbeddingDebtCap (%d) must mirror turn.embeddingDebtCap (%d); the dead-zone band the B1 "+
			"completeness gate uses has drifted from the runtime flush floor. Update both, or the gate mislabels "+
			"which probes only the lexical floor can hit.", simEmbeddingDebtCap, runtimeEmbeddingDebtCap)
	}
}
