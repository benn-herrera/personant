package sim

// Embedding-in-loop head-to-head machinery test (#98). This is the
// UNTAGGED, network-free proof that the symbolic-vs-embedding head-to-head
// plumbing computes and emits its metrics correctly — it runs under
// `make test` and gates CI. It uses model.MockEmbedder (deterministic
// feature-hashing, no network), so it validates the WIRING and the METRIC
// MATH, NOT recall QUALITY: a feature-hashing vectorizer is not semantic,
// so it cannot demonstrate the drift-robustness #96 is about. The REAL
// gap-closure measurement is run by the user via
// `make sim LIVE_EMBEDDING=true DURATION=…` against the live nomic
// embedder on reaper — this test cannot and does not claim that result.
//
// What it proves:
//   - the Scenario.Recaller seam installs an embedding measure.Service and
//     the harness Prepares + keeps its index current (AddThread per
//     thread creation), so threads born mid-run are embeddable/recallable;
//   - the parallel embed_recall_fidelity_* series is populated (steps +
//     the precision/recall/F1 histograms) alongside the symbolic series on
//     the SAME workload;
//   - the per-hop embedding buckets (wander_embed_recall_byhops_h*) are
//     emitted for every hop the symbolic probe observed — the gap-closure
//     view's machinery;
//   - the mock acceptance run (no embedder) emits NONE of these, so the
//     symbolic-only summary and gates are unchanged.

import (
	"fmt"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/scenarios"
)

// mockEmbedderRecaller is the Scenario.Recaller factory for the head-to-head
// machinery test: it installs an embedding-enabled measure.Service backed by
// the deterministic MockEmbedder. It satisfies threadIndexer (AddThread on
// measure.Service), so the harness drives per-thread-creation index upkeep.
func mockEmbedderRecaller(ops memops.MemoryOps) measure.Recaller {
	return measure.NewService(ops, model.NewMockEmbedder())
}

// TestSimEmbeddingHeadToHead_Machinery drives a short deterministic sim
// rung with a MockEmbedder-backed recaller installed and asserts the
// embedding head-to-head metrics are computed and emitted. It does NOT
// assert recall quality (MockEmbedder is not semantic) — only that the
// machinery runs end-to-end and stays network-free.
func TestSimEmbeddingHeadToHead_Machinery(t *testing.T) {
	corpus := loadCorpusSlots(t)
	h := runSimRung(t, "sim-embed-machinery", simDayDuration, corpus, mockEmbedderRecaller, false, nil, "")

	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}

	// 1) The embedding head-to-head series must be populated: the run scored
	//    embedding recall on at least one ground-truth step, in lockstep with
	//    the symbolic adversarial series (both fire on a recall-opportunity
	//    step). A zero here means the embedder/index seam never engaged.
	embedSteps := m.Counters["embed_recall_fidelity_steps"]
	if embedSteps == 0 {
		t.Fatal("embed_recall_fidelity_steps == 0: the embedding head-to-head never scored a step — " +
			"the Recaller seam or the spine.embed-match-fire parse is not wired")
	}
	// The embedding layer scores exactly the steps the symbolic layer scores
	// (same ExpectedRecallMatches != nil gate), so the adversarial symbolic
	// step count is the apples-to-apples companion. They need not be equal
	// (RecallStrict vs RecallMeasureOnly split the symbolic series), but the
	// embedding count must not exceed the total measured-ground-truth steps.
	symSteps := m.Counters["recall_fidelity_adversarial_steps"] + m.Counters["recall_fidelity_measured_steps"]
	if embedSteps > symSteps {
		t.Errorf("embed_recall_fidelity_steps %d exceeds total symbolic measured steps %d — "+
			"the head-to-head is scoring steps the symbolic path did not", embedSteps, symSteps)
	}

	// 2) The three embedding metric histograms must each carry one sample per
	//    scored step — the metric MATH ran, not just the counter.
	for _, key := range []string{
		"embed_recall_fidelity_recall",
		"embed_recall_fidelity_precision",
		"embed_recall_fidelity_f1",
	} {
		if n := int64(len(m.Histograms[key])); n != embedSteps {
			t.Errorf("%s has %d samples, want %d (one per scored step)", key, n, embedSteps)
		}
	}
	// recall/precision/F1 are ratios — every sample must be in [0,1].
	for _, key := range []string{
		"embed_recall_fidelity_recall",
		"embed_recall_fidelity_precision",
		"embed_recall_fidelity_f1",
	} {
		for i, v := range m.Histograms[key] {
			if v < 0 || v > 1 {
				t.Errorf("%s[%d] = %.4f, out of [0,1]", key, i, v)
			}
		}
	}

	// 3) Per-hop embedding buckets: for every hop the symbolic wander probe
	//    observed (origin-recall obs > 0), the embedding bucket must be
	//    present and a valid ratio. This is the per-hop gap-closure view's
	//    machinery — emitted on the SAME denominator as the symbolic curve.
	probeHopsSeen := 0
	for hop := 0; hop < wanderMaxHops; hop++ {
		key := fmt.Sprintf("_h%d", hop)
		obs := m.Gauges[metricWanderOriginRecallByHops+key+"_obs"]
		if obs == 0 {
			continue
		}
		probeHopsSeen++
		v, ok := m.Gauges[metricWanderEmbedRecallByHops+key]
		if !ok {
			t.Errorf("hop %d had %d symbolic probe obs but no %s embedding bucket emitted",
				hop, int(obs), metricWanderEmbedRecallByHops+key)
			continue
		}
		if v < 0 || v > 1 {
			t.Errorf("%s = %.4f, out of [0,1]", metricWanderEmbedRecallByHops+key, v)
		}
	}
	if probeHopsSeen == 0 {
		t.Error("no wander probe hops observed over the rung — cannot confirm per-hop embedding buckets emit")
	}
}

// TestSimNoEmbedder_HeadToHeadAbsent is the negative control: the mock
// acceptance path (nil Recaller → symbolic-only default) must emit NONE of
// the embedding head-to-head series, so the symbolic-only summary and the
// acceptance gates are provably unaffected by the #98 wiring.
func TestSimNoEmbedder_HeadToHeadAbsent(t *testing.T) {
	corpus := loadCorpusSlots(t)
	h := runSimRung(t, "sim-embed-absent", simDayDuration, corpus, nil, false, nil, "")

	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}
	if got := m.Counters["embed_recall_fidelity_steps"]; got != 0 {
		t.Errorf("embed_recall_fidelity_steps = %d on the no-embedder run, want 0 — "+
			"the embedding head-to-head must not run without an installed embedder", got)
	}
	for _, key := range []string{
		"embed_recall_fidelity_recall",
		"embed_recall_fidelity_precision",
		"embed_recall_fidelity_f1",
	} {
		if n := len(m.Histograms[key]); n != 0 {
			t.Errorf("%s has %d samples on the no-embedder run, want 0", key, n)
		}
	}
	// The symbolic adversarial series must STILL be present — the mock run is
	// unchanged, recall is still measured symbolic-only.
	if m.Counters["recall_fidelity_adversarial_steps"] == 0 {
		t.Error("recall_fidelity_adversarial_steps == 0 on the mock run — the symbolic measurement regressed")
	}
}

// compile-time guard: the scenarios.Scenario.Recaller field accepts our
// factory shape, so a signature drift fails the build, not a run.
var _ = func() func(memops.MemoryOps) measure.Recaller {
	var sc scenarios.Scenario
	sc.Recaller = mockEmbedderRecaller
	return sc.Recaller
}
