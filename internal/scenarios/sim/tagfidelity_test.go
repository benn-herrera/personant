package sim

import (
	"context"
	"fmt"
	"testing"
	"time"

	"personant/internal/scenarios"
)

// fakeEmbedder returns canned vectors keyed by text. An unknown text yields an
// error (so embedLiveThreads/adjudicateRank fall back to unadjudicated), which
// lets a test prove that path too.
type fakeEmbedder struct{ vecs map[string][]float64 }

func (f fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		v, ok := f.vecs[t]
		if !ok {
			return nil, fmt.Errorf("fakeEmbedder: no vector for %q", t)
		}
		out[i] = v
	}
	return out, nil
}

// TestGradeTagFidelity_FastPathHit: the plan intended re-engagement, the model
// engaged a thread whose symbols overlap the prompted topic set → symbolic
// fast-path HIT, no embed call (a nil embedder must NOT change the outcome).
func TestGradeTagFidelity_FastPathHit(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: true, topicSym: []string{"alpha", "beta"}, content: "Q"}}
	observed := map[string]observedTurn{"t1": {engaged: []string{"thr_1"}}}
	live := []liveThread{{id: "thr_1", sym: []string{"alpha", "gamma"}, text: "thr1"}}

	res := gradeTagFidelity(context.Background(), nil, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.hits != 1 || res.misses != 0 || res.unadjudicated != 0 {
		t.Fatalf("fast-path hit: got hits=%d misses=%d unadj=%d, want 1/0/0", res.hits, res.misses, res.unadjudicated)
	}
	if res.reengageIntent != 1 {
		t.Errorf("reengageIntent = %d, want 1", res.reengageIntent)
	}
}

// TestGradeTagFidelity_ResidueRankHit: no symbolic overlap → residue; the tagged
// thread is the top cosine match for the prompted content → rank HIT.
func TestGradeTagFidelity_ResidueRankHit(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string]observedTurn{"t1": {engaged: []string{"thr_1"}}}
	live := []liveThread{
		{id: "thr_1", sym: []string{"alpha"}, text: "d1"},
		{id: "thr_2", sym: []string{"beta"}, text: "d2"},
	}
	emb := fakeEmbedder{vecs: map[string][]float64{
		"Q":  {1, 0},
		"d1": {1, 0}, // cos 1 → rank 1
		"d2": {0, 1}, // cos 0
	}}
	res := gradeTagFidelity(context.Background(), emb, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.hits != 1 || res.misses != 0 || res.borderline != 0 || res.unadjudicated != 0 {
		t.Fatalf("residue rank-hit: got hits=%d misses=%d bl=%d unadj=%d, want 1/0/0/0",
			res.hits, res.misses, res.borderline, res.unadjudicated)
	}
}

// TestGradeTagFidelity_ResidueRankMiss: no symbolic overlap and the tagged
// thread ranks below the near-top band (rank > K) → rank MISS.
func TestGradeTagFidelity_ResidueRankMiss(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string]observedTurn{"t1": {engaged: []string{"thr_5"}}}
	live := []liveThread{
		{id: "thr_1", sym: []string{"a"}, text: "d1"},
		{id: "thr_2", sym: []string{"b"}, text: "d2"},
		{id: "thr_3", sym: []string{"c"}, text: "d3"},
		{id: "thr_4", sym: []string{"d"}, text: "d4"},
		{id: "thr_5", sym: []string{"e"}, text: "d5"},
	}
	emb := fakeEmbedder{vecs: map[string][]float64{
		"Q":  {1, 0},
		"d1": {1, 0}, // cos 1.000  rank 1
		"d2": {9, 1}, // ~0.996     rank 2
		"d3": {4, 1}, // ~0.970     rank 3
		"d4": {2, 1}, // ~0.894     rank 4
		"d5": {0, 1}, // 0          rank 5 (the engaged thread)
	}}
	res := gradeTagFidelity(context.Background(), emb, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.misses != 1 || res.hits != 0 || res.borderline != 0 {
		t.Fatalf("residue rank-miss: got hits=%d misses=%d bl=%d, want 0/1/0", res.hits, res.misses, res.borderline)
	}
}

// TestGradeTagFidelity_ResidueBorderline: the tagged thread sits on the k/(k+1)
// boundary with a sub-epsilon cosine gap → BORDERLINE (not forced into hit/miss).
// Uses rankK=1 so the boundary is between rank 1 and rank 2.
func TestGradeTagFidelity_ResidueBorderline(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string]observedTurn{"t1": {engaged: []string{"thr_1"}}}
	live := []liveThread{
		{id: "thr_1", sym: []string{"a"}, text: "d1"},
		{id: "thr_2", sym: []string{"b"}, text: "d2"},
	}
	// cos(Q,d1)=1.000, cos(Q,d2)=10/sqrt(101)=0.99504 → gap 0.00496 < eps 0.01.
	emb := fakeEmbedder{vecs: map[string][]float64{
		"Q":  {1, 0},
		"d1": {1, 0},
		"d2": {10, 1},
	}}
	res := gradeTagFidelity(context.Background(), emb, plan, observed, live, 1, tagFidelityBorderlineEps)
	if res.borderline != 1 || res.hits != 0 || res.misses != 0 {
		t.Fatalf("borderline: got hits=%d misses=%d bl=%d, want 0/0/1", res.hits, res.misses, res.borderline)
	}
}

// TestGradeTagFidelity_ResidueUnadjudicated: no symbolic overlap and no embedder
// → the residue is UNADJUDICATED (counted, never guessed), and it is held out of
// the hits/misses tally.
func TestGradeTagFidelity_ResidueUnadjudicated(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string]observedTurn{"t1": {engaged: []string{"thr_1"}}}
	live := []liveThread{{id: "thr_1", sym: []string{"alpha"}, text: "d1"}}

	res := gradeTagFidelity(context.Background(), nil, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.unadjudicated != 1 || res.hits != 0 || res.misses != 0 {
		t.Fatalf("unadjudicated: got hits=%d misses=%d unadj=%d, want 0/0/1", res.hits, res.misses, res.unadjudicated)
	}
}

// TestGradeTagFidelity_SpuriousAndMissing: a re-engagement-intent turn the model
// answered with *new-topic* is a spurious-new-topic MISS; one with no parseable
// tag is a tag-missing MISS. Both feed the miss tally.
func TestGradeTagFidelity_SpuriousAndMissing(t *testing.T) {
	plan := []planTurn{
		{instant: "t1", reengage: true, topicSym: []string{"a"}, content: "Q"},
		{instant: "t2", reengage: true, topicSym: []string{"b"}, content: "Q"},
	}
	observed := map[string]observedTurn{
		"t1": {created: true},
		"t2": {tagMissing: true},
	}
	res := gradeTagFidelity(context.Background(), nil, plan, observed, nil, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.spuriousNewTopic != 1 || res.tagMissing != 1 || res.misses != 2 {
		t.Fatalf("spurious/missing: got spurious=%d missing=%d misses=%d, want 1/1/2",
			res.spuriousNewTopic, res.tagMissing, res.misses)
	}
	if res.reengageIntent != 2 {
		t.Errorf("reengageIntent = %d, want 2", res.reengageIntent)
	}
}

// TestGradeTagFidelity_DefaultedTurnIsOmissionNeverHit: a D6 owner-defaulted
// turn emits BOTH thread.engaged (the runtime-bound owner) AND
// topic.tag-missing/thread.tag-defaulted at one instant. The engaged id is
// the runtime's recovery choice, not a model tag — even when its symbols
// overlap the prompted topic (which would be a fast-path hit if the engaged
// case were consulted), the turn must classify as a model omission (its own
// bin) and a miss.
func TestGradeTagFidelity_DefaultedTurnIsOmissionNeverHit(t *testing.T) {
	plan := []planTurn{
		// t1: full defaulted shape — tag-missing + tag-defaulted + engaged.
		{instant: "t1", reengage: true, topicSym: []string{"alpha"}, content: "Q"},
		// t2: defaulted marker alone alongside engaged (defensive: the grader
		// must not depend on tag-missing accompanying it).
		{instant: "t2", reengage: true, topicSym: []string{"alpha"}, content: "Q"},
	}
	observed := map[string]observedTurn{
		"t1": {engaged: []string{"thr_1"}, tagMissing: true, tagDefaulted: true},
		"t2": {engaged: []string{"thr_1"}, tagDefaulted: true},
	}
	// thr_1 overlaps the prompted symbols — the bait the old case ordering
	// took as a fast-path hit.
	live := []liveThread{{id: "thr_1", sym: []string{"alpha", "gamma"}, text: "thr1"}}

	res := gradeTagFidelity(context.Background(), nil, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.hits != 0 || res.misses != 2 || res.tagMissing != 2 {
		t.Fatalf("defaulted turns: got hits=%d misses=%d tagMissing=%d, want 0/2/2 "+
			"(owner-default credited as a model tag?)", res.hits, res.misses, res.tagMissing)
	}
	if res.reengageIntent != 2 {
		t.Errorf("reengageIntent = %d, want 2", res.reengageIntent)
	}
}

// TestGradeTagFidelity_NewTopicIntentIgnored: a plan turn with new-topic intent
// is NOT a re-engagement turn and must not enter any tally.
func TestGradeTagFidelity_NewTopicIntentIgnored(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: false, topicSym: []string{"a"}, content: "Q"}}
	observed := map[string]observedTurn{"t1": {created: true}}
	res := gradeTagFidelity(context.Background(), nil, plan, observed, nil, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.reengageIntent != 0 || res.hits != 0 || res.misses != 0 {
		t.Fatalf("new-topic intent must be ignored: got intent=%d hits=%d misses=%d", res.reengageIntent, res.hits, res.misses)
	}
}

// TestJaccard pins the anchor-overlap set math (the metric-3 primitive).
func TestJaccard(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want float64
	}{
		{"identical", []string{"x", "y"}, []string{"y", "x"}, 1.0},
		{"disjoint", []string{"x"}, []string{"y"}, 0.0},
		{"partial", []string{"x", "y"}, []string{"y", "z"}, 1.0 / 3.0},
		{"empty both", nil, nil, 0.0},
		{"empty one", []string{"x"}, nil, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jaccard(tt.a, tt.b); absDiff(got, tt.want) > 1e-9 {
				t.Errorf("jaccard(%v,%v) = %.4f, want %.4f", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestCosine pins the measurement-side cosine (rank-adjudication primitive).
func TestCosine(t *testing.T) {
	if got := cosine([]float64{1, 0}, []float64{1, 0}); absDiff(got, 1.0) > 1e-9 {
		t.Errorf("parallel cosine = %.4f, want 1", got)
	}
	if got := cosine([]float64{1, 0}, []float64{0, 1}); absDiff(got, 0.0) > 1e-9 {
		t.Errorf("orthogonal cosine = %.4f, want 0", got)
	}
	if got := cosine([]float64{0, 0}, []float64{1, 1}); got != 0 {
		t.Errorf("zero-norm cosine = %.4f, want 0", got)
	}
	if got := cosine([]float64{1, 2}, []float64{1, 2, 3}); got != 0 {
		t.Errorf("mismatched-length cosine = %.4f, want 0", got)
	}
}

// TestTagFidelityMetricKeysRegistered is the metric-key registry extension for
// A2: the three tag-fidelity series keys must be present, non-empty, distinct,
// and carry their contracted string values (a rename is then one edit in
// metric_keys.go, caught here if a consumer drifts).
func TestTagFidelityMetricKeysRegistered(t *testing.T) {
	want := map[string]string{
		"ReengageMissRate":     scenarios.MetricTagFidelityReengageMissRate,
		"SpuriousNewTopicRate": scenarios.MetricTagFidelitySpuriousNewTopicRate,
		"AnchorOverlap":        scenarios.MetricTagFidelityAnchorOverlap,
	}
	expect := map[string]string{
		"ReengageMissRate":     "tag_fidelity_reengage_miss_rate",
		"SpuriousNewTopicRate": "tag_fidelity_spurious_new_topic_rate",
		"AnchorOverlap":        "tag_fidelity_anchor_overlap",
	}
	seen := map[string]string{}
	for name, key := range want {
		if key == "" {
			t.Errorf("%s: registry key is empty", name)
		}
		if key != expect[name] {
			t.Errorf("%s: registry key = %q, want %q", name, key, expect[name])
		}
		if prev, dup := seen[key]; dup {
			t.Errorf("%s and %s share key %q — keys must be distinct", name, prev, key)
		}
		seen[key] = name
	}
}

// TestTagFidelity_MockRunEmitsNoSeries is the vacuity guard: a mock rung must NOT
// emit any tag-fidelity series (a scripted mock's tags are true-by-construction,
// so the series would be vacuous — the request_prompt_tokens liveClient-gating
// precedent). It (1) confirms the mock-run metrics blob carries none of the keys,
// then (2) calls the grader with liveInference=false and confirms the guard
// no-ops (the blob is unchanged and still key-free).
func TestTagFidelity_MockRunEmitsNoSeries(t *testing.T) {
	corpus := loadCorpusSlots(t)
	h := runSimRung(t, "tagfid-vacuity-1d", 24*time.Hour, corpus, nil, false, nil, "")

	keys := []string{
		scenarios.MetricTagFidelityReengageMissRate,
		scenarios.MetricTagFidelitySpuriousNewTopicRate,
		scenarios.MetricTagFidelityAnchorOverlap,
	}
	assertNoTagFidelityKeys := func(where string) {
		m, err := readMetrics(h.MetricsPath)
		if err != nil {
			t.Fatalf("%s: read metrics blob: %v", where, err)
		}
		for _, k := range keys {
			if _, ok := m.Gauges[k]; ok {
				t.Errorf("%s: tag-fidelity key %q present on a mock run (series must be live-inference-only)", where, k)
			}
		}
	}
	assertNoTagFidelityKeys("after mock rung")

	// The guard: invoking the grader with liveInference=false must not emit.
	cfg := WorkloadConfig{Seed: simSeed, Duration: 24 * time.Hour, Corpus: corpus}
	recordTagFidelityMetrics(t, h, cfg, false)
	assertNoTagFidelityKeys("after guarded grader call")
}
