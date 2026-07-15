package sim

import (
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/turn"
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
	within := []float64{1000, 16384, memops.DefaultTokenCeiling - 1, 42000}
	if fail := checkTokenCeiling(within); fail != "" {
		t.Fatalf("under-ceiling case should PASS, got failure: %s", fail)
	}

	// MUTATION: one oversize turn pushes the assembled request past the ceiling.
	// The gate MUST fail — max over the series exceeds the bound.
	oversize := append(append([]float64(nil), within...), memops.DefaultTokenCeiling+1)
	if fail := checkTokenCeiling(oversize); fail == "" {
		t.Errorf("MUTATION (one turn at ceiling+1 = %d) must FAIL the token-ceiling gate, but checkTokenCeiling "+
			"returned no failure — the gate is not reading the full assembled request (FM3/FM4)",
			memops.DefaultTokenCeiling+1)
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
// (depth >= ThreadTurnWindow) AND is within the most-recent (1+pending)×
// turn.EmbeddingDebtCap below the window floor. With pending=0 (the acceptance-rung
// case) the band is the plain 1×cap; with pending=P>0 (the slow-embedder BD-8 case)
// it widens to (1+P)×cap — the band the runtime's in-flight-widened lexical floor
// covers. This is the band where ONLY the lexical floor can hit (the embedding fine
// tier has no vector yet) — the exact subset the B1 completeness gate targets. Both
// bounds read the runtime's exported constants directly, so there is a single
// source of truth and no mirror to drift (#126).
func TestInDebtWindowDepth(t *testing.T) {
	w := store.ThreadTurnWindow
	dcap := turn.EmbeddingDebtCap
	cases := []struct {
		depth, pending int
		want           bool
	}{
		// pending=0: the plain 1×cap band (byte-identical to the pre-B2 classifier).
		{0, 0, false},              // never scrolled out
		{w - 1, 0, false},          // still in the assembly window
		{w, 0, true},               // first scrolled-out excerpt — dead-zone floor
		{w + dcap - 1, 0, true},    // last 1×cap dead-zone excerpt
		{w + dcap, 0, false},       // just past 1×cap — flushed, fine tier covers
		{w + dcap + 100, 0, false}, // deep history — flushed long ago
		// pending=1: the (1+1)×cap = 2×cap widened band (BD-8, one flush in flight).
		{w + dcap, 1, true},       // now inside the widened band — the whole point of B2
		{w + 2*dcap - 1, 1, true}, // last 2×cap dead-zone excerpt
		{w + 2*dcap, 1, false},    // past 2×cap even with one pending — flushed
		// pending=2: (1+2)×cap = 3×cap — matches the measure BD-8 queued-batch case.
		{w + 2*dcap, 2, true},
		{w + 3*dcap - 1, 2, true},
		{w + 3*dcap, 2, false},
		// negative pending is clamped to 0 (defensive).
		{w + dcap, -1, false},
	}
	for _, c := range cases {
		if got := inDebtWindowDepth(c.depth, c.pending); got != c.want {
			t.Errorf("inDebtWindowDepth(%d, pending=%d) = %v, want %v (window=%d cap=%d)",
				c.depth, c.pending, got, c.want, w, dcap)
		}
	}
}

// TestCompletenessFloor_SpanConditionality is the B1 / D7 span-conditionality
// proof: the completeness gate SKIPs (logs, does not fail) when the workload's
// realized main-thread turn count cannot reach the dead zone, and stays live
// (anti-vacuity FAIL on zero) once the span qualifies. The qualifying threshold
// is derived from the public runtime constants, never a hardcoded rung list.
func TestCompletenessFloor_SpanConditionality(t *testing.T) {
	threshold := completenessQualifyThreshold()
	if want := store.ThreadTurnWindow + turn.EmbeddingDebtCap; threshold != want {
		t.Fatalf("qualify threshold = %d, want ThreadTurnWindow+EmbeddingDebtCap = %d", threshold, want)
	}

	// Below threshold: structurally cannot probe → span does NOT qualify → the
	// gate skips (the machinery-test / short-rung case). At and above: it qualifies.
	spanCases := []struct {
		mainTurns int
		qualifies bool
	}{
		{0, false},               // main thread never created
		{threshold - 1, false},   // one short of qualifying — SKIP
		{threshold, true},        // exactly qualifying
		{threshold + 5000, true}, // a long rung — well past
	}
	for _, c := range spanCases {
		if got := completenessSpanQualifies(c.mainTurns); got != c.qualifies {
			t.Errorf("completenessSpanQualifies(%d) = %v, want %v (threshold=%d)",
				c.mainTurns, got, c.qualifies, threshold)
		}
	}

	// Once the span qualifies, the anti-vacuity arm is live: a 0-probe tally FAILS
	// (checkCompletenessFloor(0,0) — the "could-have-probed-and-didn't" defect).
	// This is the second half of the acceptance criterion: a qualifying span with
	// zero probes still fails.
	if fail := checkCompletenessFloor(0, 0); fail == "" {
		t.Error("a qualifying span with 0/0 dead-zone probes must FAIL the anti-vacuity gate, got no failure")
	}

	// The qualifying-span estimate scales linearly and rounds to the hour: a 24h
	// span whose main thread reached exactly threshold turns already qualifies (1:1);
	// a 24h span at half the threshold needs ~2× the span.
	if got := qualifyingCompletenessSpan(24*time.Hour, threshold, threshold); got != 24*time.Hour {
		t.Errorf("qualifyingCompletenessSpan(24h, threshold, threshold) = %s, want 24h", got)
	}
	if got := qualifyingCompletenessSpan(24*time.Hour, threshold/2, threshold); got != 48*time.Hour {
		t.Errorf("qualifyingCompletenessSpan(24h, threshold/2, threshold) = %s, want 48h", got)
	}
	if got := qualifyingCompletenessSpan(24*time.Hour, 0, threshold); got != 0 {
		t.Errorf("qualifyingCompletenessSpan with 0 main turns = %s, want 0 (nothing to scale)", got)
	}
}
