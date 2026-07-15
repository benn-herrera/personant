package sim

import (
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/scenarios"
	"personant/internal/testsupport"
)

// TestShadowLayerB_ReverseDivergence drives a mock workload through the real
// harness and asserts the BD-4 reverse cross-check (shadow-held /
// runtime-evicted) sits at 0 — the generator's runtime-mirror Layer-B now
// models the two documented legitimate eviction sources (measurement-carrier
// displacement and §3.5 decay closure) that the plain engage()-only shadow
// does not, so any residual is a real runtime/mirror LRU disagreement.
//
// Forward divergence (the existing hard #8 gate: runtime-resident absent from
// the engage()-only shadow) is asserted here too, so a regression in either
// direction is caught by the EDIT GATE without a full rung walk.
func TestShadowLayerB_ReverseDivergence(t *testing.T) {
	for _, span := range []time.Duration{24 * time.Hour, 14 * 24 * time.Hour} {
		span := span
		t.Run(span.String(), func(t *testing.T) {
			// The 14d arm runs ~20 min — on its own it pushes the sim package
			// past `make test`'s 30m per-package timeout budget (the suite is
			// the CHECKPOINT GATE; a whole package must finish inside 30m). So
			// it ALWAYS COMPILES but only EXECUTES under the slow-sim opt-in;
			// the 1d arm (~1 min) always runs and catches a regression in
			// either divergence direction in the default suite.
			if span >= 14*24*time.Hour {
				testsupport.RequireSlowSim(t)
			}
			corpus := loadCorpusSlots(t)
			cfg := WorkloadConfig{Seed: simSeed, Duration: span, Corpus: corpus}
			sc := GenerateWorkload(cfg)
			sc.Name = withRunTimestampSuffix(sc.Name)
			sc.HeavyInvariantCadence = simHeavyInvariantCadence
			sc.MemoryCapBytes = simMemoryCapBytes
			gen := sc.StepSource.(*generator)

			start := clock.Profiling()
			scenarios.RunScenario(t, sc)
			t.Logf("%s mock rung in %s: forward div=%d reverse div=%d",
				span, clock.Since(start),
				gen.layerBShadowDivergence, gen.layerBShadowReverseDivergence)

			if gen.layerBShadowDivergence != 0 {
				t.Errorf("forward Layer-B shadow divergence = %d, want 0 (burndown #8)",
					gen.layerBShadowDivergence)
			}
			if gen.layerBShadowReverseDivergence != 0 {
				t.Errorf("reverse Layer-B shadow divergence = %d, want 0 (BD-4 B3)",
					gen.layerBShadowReverseDivergence)
			}
		})
	}
}
