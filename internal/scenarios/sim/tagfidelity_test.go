package sim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
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
	plan := []planTurn{{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"alpha", "beta"}, content: "Q"}}
	observed := map[string][]observedTurn{"t1": {{engaged: []string{"thr_1"}}}}
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
	plan := []planTurn{{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string][]observedTurn{"t1": {{engaged: []string{"thr_1"}}}}
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
	plan := []planTurn{{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string][]observedTurn{"t1": {{engaged: []string{"thr_5"}}}}
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
	plan := []planTurn{{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string][]observedTurn{"t1": {{engaged: []string{"thr_1"}}}}
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
	plan := []planTurn{{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"zeta"}, content: "Q"}}
	observed := map[string][]observedTurn{"t1": {{engaged: []string{"thr_1"}}}}
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
		{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"a"}, content: "Q"},
		{instant: "t2", reengage: true, gradeable: true, topicSym: []string{"b"}, content: "Q"},
	}
	observed := map[string][]observedTurn{
		"t1": {{created: true}},
		"t2": {{tagMissing: true}},
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
		{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"alpha"}, content: "Q"},
		// t2: defaulted marker alone alongside engaged (defensive: the grader
		// must not depend on tag-missing accompanying it).
		{instant: "t2", reengage: true, gradeable: true, topicSym: []string{"alpha"}, content: "Q"},
	}
	observed := map[string][]observedTurn{
		"t1": {{engaged: []string{"thr_1"}, tagMissing: true, tagDefaulted: true}},
		"t2": {{engaged: []string{"thr_1"}, tagDefaulted: true}},
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
	plan := []planTurn{{instant: "t1", reengage: false, gradeable: true, topicSym: []string{"a"}, content: "Q"}}
	observed := map[string][]observedTurn{"t1": {{created: true}}}
	res := gradeTagFidelity(context.Background(), nil, plan, observed, nil, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.reengageIntent != 0 || res.hits != 0 || res.misses != 0 {
		t.Fatalf("new-topic intent must be ignored: got intent=%d hits=%d misses=%d", res.reengageIntent, res.hits, res.misses)
	}
}

// writeTagFidelityLog writes one event-log day file into a fresh logs dir and
// returns the dir — the buildObservedTurns fixture helper.
func writeTagFidelityLog(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-05-04.log"), []byte(body), 0o644); err != nil {
		t.Fatalf("write log fixture: %v", err)
	}
	return dir
}

// TestBuildObservedTurns_PerTurnSegmentation: two turns in ONE RFC3339 second
// — a clean engagement then a tag omission — must come back as TWO observed
// turns in executed order, not one merged group (the burndown item-4 smear:
// 380 tag-missing lines collapsed into 256 instant groups on the Jul-16 run).
func TestBuildObservedTurns_PerTurnSegmentation(t *testing.T) {
	const at = "2026-05-04T08:12:10Z"
	dir := writeTagFidelityLog(t, []string{
		at + " context.modified source=user.prompt bytes=47",
		at + " context.modified source=model.response bytes=1421",
		at + " thread.engaged thr_3 turn_count=3",
		at + " context.modified source=user.prompt bytes=52",
		at + " topic.tag-missing source=model.response bytes=900",
		at + " thread.tag-defaulted thr=thr_3 cause=missing-tag reprompted=yes",
		at + " thread.engaged thr_3 turn_count=4",
	})
	observed, degraded := buildObservedTurns(dir)
	if degraded {
		t.Fatalf("boundary-bearing log classified degraded")
	}
	turns := observed[at]
	if len(turns) != 2 {
		t.Fatalf("got %d observed turns at %s, want 2 (per-turn segmentation)", len(turns), at)
	}
	first, second := turns[0], turns[1]
	if first.tagMissing || first.tagDefaulted || len(first.engaged) != 1 || first.engaged[0] != "thr_3" {
		t.Errorf("first turn = %+v, want clean engaged thr_3", first)
	}
	if !second.tagMissing || !second.tagDefaulted {
		t.Errorf("second turn = %+v, want the omission markers", second)
	}
}

// TestBuildObservedTurns_DegradedFallback: a log with NO per-turn boundary
// lines (old format) falls back to instant-merged grouping — one turn per
// instant, every marker folded in — and is labeled degraded.
func TestBuildObservedTurns_DegradedFallback(t *testing.T) {
	const at = "2026-05-04T08:12:10Z"
	dir := writeTagFidelityLog(t, []string{
		at + " thread.engaged thr_3 turn_count=3",
		at + " topic.tag-missing source=model.response bytes=900",
		"2026-05-04T08:13:00Z thread.created thr_4 anchors=4 project=prj_1",
	})
	observed, degraded := buildObservedTurns(dir)
	if !degraded {
		t.Fatalf("boundary-free log must be classified degraded")
	}
	if turns := observed[at]; len(turns) != 1 || !turns[0].tagMissing || len(turns[0].engaged) != 1 {
		t.Errorf("degraded group at %s = %+v, want one merged turn (engaged+tagMissing)", at, turns)
	}
	if turns := observed["2026-05-04T08:13:00Z"]; len(turns) != 1 || !turns[0].created {
		t.Errorf("degraded group at 08:13:00 = %+v, want one created turn", turns)
	}
}

// TestGradeTagFidelity_SameInstantSeparation is the item-4 smear fix proof:
// two re-engagement plan turns in one second — the model tagged the first
// cleanly and omitted on the second — must grade as ONE hit and ONE
// plan-joined omission, not two omissions (the merged-group behavior).
func TestGradeTagFidelity_SameInstantSeparation(t *testing.T) {
	plan := []planTurn{
		{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"alpha"}, content: "Q1"},
		{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"alpha"}, content: "Q2"},
	}
	observed := map[string][]observedTurn{
		"t1": {
			{engaged: []string{"thr_1"}},
			{engaged: []string{"thr_1"}, tagMissing: true, tagDefaulted: true},
		},
	}
	live := []liveThread{{id: "thr_1", sym: []string{"alpha"}, text: "thr1"}}

	res := gradeTagFidelity(context.Background(), nil, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.hits != 1 || res.tagMissing != 1 || res.misses != 1 {
		t.Fatalf("same-instant separation: got hits=%d tagMissing=%d misses=%d, want 1/1/1", res.hits, res.tagMissing, res.misses)
	}
	if res.joinAmbiguous != 0 {
		t.Errorf("joinAmbiguous = %d, want 0 (counts agree)", res.joinAmbiguous)
	}
}

// TestGradeTagFidelity_JoinAmbiguousCountMismatch: an instant whose observed
// turn count disagrees with the plan's (an injected refinement turn landed in
// the same second) is never force-joined — its reengage plan turns land in the
// joinAmbiguous bin and no grade is issued.
func TestGradeTagFidelity_JoinAmbiguousCountMismatch(t *testing.T) {
	plan := []planTurn{{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"alpha"}, content: "Q"}}
	observed := map[string][]observedTurn{
		"t1": {
			{engaged: []string{"thr_1"}},
			{tagMissing: true},
		},
	}
	live := []liveThread{{id: "thr_1", sym: []string{"alpha"}, text: "thr1"}}

	res := gradeTagFidelity(context.Background(), nil, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.joinAmbiguous != 1 {
		t.Fatalf("joinAmbiguous = %d, want 1", res.joinAmbiguous)
	}
	if res.reengageIntent != 0 || res.hits != 0 || res.misses != 0 || res.tagMissing != 0 {
		t.Errorf("ambiguous instant must grade nothing: %+v", res)
	}
}

// TestGradeTagFidelity_UngradeablePlanTurnHoldsOrdinal: a canonical step whose
// scripted response carried no parseable plan tag still executes a turn, so it
// must HOLD its ordinal slot — the gradeable turn behind it joins the correct
// observed turn.
func TestGradeTagFidelity_UngradeablePlanTurnHoldsOrdinal(t *testing.T) {
	plan := []planTurn{
		{instant: "t1"}, // ungradeable: holds ordinal 0
		{instant: "t1", reengage: true, gradeable: true, topicSym: []string{"alpha"}, content: "Q"},
	}
	observed := map[string][]observedTurn{
		"t1": {
			{tagMissing: true},           // the ungradeable turn's outcome — must NOT be graded
			{engaged: []string{"thr_1"}}, // the gradeable turn's outcome — a fast-path hit
		},
	}
	live := []liveThread{{id: "thr_1", sym: []string{"alpha"}, text: "thr1"}}

	res := gradeTagFidelity(context.Background(), nil, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	if res.hits != 1 || res.tagMissing != 0 || res.misses != 0 {
		t.Fatalf("ordinal hold: got hits=%d tagMissing=%d misses=%d, want 1/0/0", res.hits, res.tagMissing, res.misses)
	}
}

// TestAnchorOverlapFromMeta pins the item-5 vacuity signal: with ZERO
// persisted source=deterministic history symbols the mean Jaccard is
// 0-against-empty-set by construction — detTotal==0 is the caller's
// "unmeasured" gate — while a run with deterministic symbols measures
// normally.
func TestAnchorOverlapFromMeta(t *testing.T) {
	sym := func(n string, src memops.SymbolSource) memops.HistorySymbol {
		return memops.HistorySymbol{Raw: n, Normalized: n, Source: src, Count: 1}
	}

	// Vacuous: model anchors only (the Jul-16 live-run shape).
	vacuous := []memops.ThreadMeta{
		{ID: "thr_1", HistorySymbols: []memops.HistorySymbol{sym("a", memops.SourceModel)}},
		{ID: "thr_2", HistorySymbols: []memops.HistorySymbol{sym("b", memops.SourceModel), sym("c", memops.SourceUser)}},
	}
	mean, obs, detTotal := anchorOverlapFromMeta(vacuous)
	if detTotal != 0 {
		t.Fatalf("vacuous detTotal = %d, want 0", detTotal)
	}
	if mean != 0 || obs != 2 {
		t.Errorf("vacuous mean/obs = %.3f/%d, want 0.000/2 (obs counts model-anchor threads either way)", mean, obs)
	}

	// Measured: one thread with a shared model+deterministic symbol set.
	measured := []memops.ThreadMeta{
		{ID: "thr_1", HistorySymbols: []memops.HistorySymbol{
			sym("a", memops.SourceModel), sym("b", memops.SourceModel),
			sym("a", memops.SourceDeterministic), // NB distinct entries share a normalized form only in this fixture
		}},
	}
	mean, obs, detTotal = anchorOverlapFromMeta(measured)
	if detTotal != 1 || obs != 1 {
		t.Fatalf("measured detTotal/obs = %d/%d, want 1/1", detTotal, obs)
	}
	if want := 1.0 / 2.0; absDiff(mean, want) > 1e-9 {
		t.Errorf("measured mean = %.3f, want %.3f (|{a}| / |{a,b}|)", mean, want)
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
