package recall

import (
	"reflect"
	"testing"

	"personant/internal/memops"
)

// makeSpine is a helper that constructs a SpineRecord with the fields
// the recall matcher actually consults; other fields are zeroed.
func makeSpine(id, project string, anchors []string, recallFires int) memops.SpineRecord {
	return memops.SpineRecord{
		ID:          id,
		Project:     project,
		Anchors:     anchors,
		RecallFires: recallFires,
	}
}

// makeFM constructs a ThreadFrontmatter with anchors + history_symbols
// (normalized form only — that is what the matcher reads).
func makeFM(id, project string, anchors, historyNorms []string) memops.ThreadFrontmatter {
	hist := make([]memops.HistorySymbol, 0, len(historyNorms))
	for _, n := range historyNorms {
		hist = append(hist, memops.HistorySymbol{Raw: n, Normalized: n})
	}
	return memops.ThreadFrontmatter{
		ID:             id,
		Project:        project,
		Anchors:        anchors,
		HistorySymbols: hist,
	}
}

func TestProposeFromIndex_AnchorMatch_AboveThreshold(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta"}, nil),
	}
	got := ProposeFromIndex(spine, threads, []string{"alpha", "beta"}, Options{})
	want := []Candidate{
		{ThreadID: "thr_1", Score: 0.5, MatchedSymbols: []string{"alpha", "beta"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestProposeFromIndex_BelowThresholdDropped(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta", "epsilon"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta", "epsilon"}, nil),
	}
	got := ProposeFromIndex(spine, threads, []string{"alpha"}, Options{})
	if len(got) != 0 {
		t.Fatalf("expected empty; got %#v", got)
	}
}

func TestProposeFromIndex_HistorySymbolsContribute(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta"}, []string{"gamma"}),
	}

	// 1/3 ≈ 0.333 < 0.4 → empty.
	got := ProposeFromIndex(spine, threads, []string{"gamma"}, Options{})
	if len(got) != 0 {
		t.Fatalf("expected empty for query=[gamma]; got %#v", got)
	}

	// Query {alpha, gamma} ∩ thread {alpha, beta, gamma} = 2; union = 3;
	// 2/3 ≈ 0.667 ≥ 0.4 → returns thr_1.
	got = ProposeFromIndex(spine, threads, []string{"gamma", "alpha"}, Options{})
	if len(got) != 1 || got[0].ThreadID != "thr_1" {
		t.Fatalf("expected one thr_1 result; got %#v", got)
	}
	wantMatched := []string{"alpha", "gamma"}
	if !reflect.DeepEqual(got[0].MatchedSymbols, wantMatched) {
		t.Fatalf("matched symbols: got %#v want %#v", got[0].MatchedSymbols, wantMatched)
	}
}

func TestProposeFromIndex_ExcludeFiltersResult(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta"}, nil),
	}
	opts := Options{Exclude: map[string]struct{}{"thr_1": {}}}
	got := ProposeFromIndex(spine, threads, []string{"alpha", "beta"}, opts)
	if len(got) != 0 {
		t.Fatalf("expected empty; got %#v", got)
	}
}

func TestProposeFromIndex_ProjectFilter(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta"}, 0),
		makeSpine("thr_2", "prj_2", []string{"alpha", "beta", "gamma", "delta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta", "gamma", "delta"}, nil),
		makeFM("thr_2", "prj_2", []string{"alpha", "beta", "gamma", "delta"}, nil),
	}
	got := ProposeFromIndex(spine, threads, []string{"alpha", "beta"}, Options{Project: "prj_1"})
	if len(got) != 1 || got[0].ThreadID != "thr_1" {
		t.Fatalf("expected only thr_1; got %#v", got)
	}
}

func TestProposeFromIndex_OrderingByScoreThenRecallFires(t *testing.T) {
	// Three threads:
	//   thr_a: anchors {alpha,beta,gamma}, query {alpha,beta,gamma},
	//          intersection 3, union 3, score 1.0  (will be 0.6 below)
	// To target precise scores, set anchors to known shapes:
	//   thr_a anchors = {alpha,beta,gamma,delta,extra}; query
	//   {alpha,beta,gamma} → ∩=3, ∪=5, score=0.6.
	//   thr_b anchors = {alpha,beta,gamma,delta}; query same →
	//   ∩=3, ∪=4, score=0.75. That overshoots 0.5.
	// Use larger thread sets to nail 0.5 exactly:
	//   For 0.5: need ∩=k, ∪=2k. With query of 3, k=2 and ∪=4
	//   means anchors share 2 with query, total anchors 3 (then
	//   ∪ = 3+3-2 = 4). E.g. anchors {alpha,beta,zzz}.
	// So:
	//   thr_a: anchors {alpha,beta,gamma,d,e,f,g,h,h2,h3} → with q
	//   {alpha,beta,gamma}: ∩=3, ∪=3+10-3=10, score=0.3 (below thr).
	// Easier: pick query length 4 and anchor sets to land scores
	// 0.6, 0.5, 0.5.
	//
	// Query: {a,b,c,d} (|Q|=4).
	//   thr_a anchors {a,b,c,x,y,z}: ∩=3, ∪=4+6-3=7 → 3/7 ≈ 0.43.
	//   Aim for 0.6: need ∩/∪=0.6 with |Q|=4. Try ∩=3, ∪=5:
	//     anchors size = 5 + 3 - 4 = 4. anchors {a,b,c,x}: ∩=3,
	//     ∪=4+4-3=5, score=0.6.  ✓
	//   Aim for 0.5 with |Q|=4: ∩=2, ∪=4, anchors size=2:
	//     anchors {a,b}: ∩=2, ∪=4+2-2=4, score=0.5.  ✓ (twice)
	spine := []memops.SpineRecord{
		makeSpine("thr_a", "prj_1", []string{"a", "b", "c", "x"}, 0),
		makeSpine("thr_b", "prj_1", []string{"a", "b"}, 7),
		makeSpine("thr_c", "prj_1", []string{"a", "b"}, 3),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_a", "prj_1", []string{"a", "b", "c", "x"}, nil),
		makeFM("thr_b", "prj_1", []string{"a", "b"}, nil),
		makeFM("thr_c", "prj_1", []string{"a", "b"}, nil),
	}
	got := ProposeFromIndex(spine, threads, []string{"a", "b", "c", "d"}, Options{})

	if len(got) != 3 {
		t.Fatalf("expected 3 candidates; got %d (%#v)", len(got), got)
	}
	if got[0].ThreadID != "thr_a" {
		t.Fatalf("first should be thr_a (score 0.6); got %s (%v)", got[0].ThreadID, got[0].Score)
	}
	if got[1].ThreadID != "thr_b" {
		t.Fatalf("second should be thr_b (recall_fires 7); got %s", got[1].ThreadID)
	}
	if got[2].ThreadID != "thr_c" {
		t.Fatalf("third should be thr_c (recall_fires 3); got %s", got[2].ThreadID)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("score ordering broken: thr_a=%v thr_b=%v", got[0].Score, got[1].Score)
	}
	if got[1].Score != got[2].Score {
		t.Fatalf("expected tied scores for thr_b and thr_c; got %v vs %v", got[1].Score, got[2].Score)
	}
}

func TestProposeFromIndex_LimitTruncates(t *testing.T) {
	// Five threads, all with anchors == query → score 1.0 each.
	query := []string{"alpha", "beta"}
	spine := make([]memops.SpineRecord, 0, 5)
	threads := make([]memops.ThreadFrontmatter, 0, 5)
	for _, id := range []string{"thr_1", "thr_2", "thr_3", "thr_4", "thr_5"} {
		spine = append(spine, makeSpine(id, "prj_1", []string{"alpha", "beta"}, 0))
		threads = append(threads, makeFM(id, "prj_1", []string{"alpha", "beta"}, nil))
	}
	got := ProposeFromIndex(spine, threads, query, Options{Limit: 2})
	if len(got) != 2 {
		t.Fatalf("expected 2 (limit); got %d", len(got))
	}
	// Tie-break is lexical on ID after recall_fires (all 0).
	if got[0].ThreadID != "thr_1" || got[1].ThreadID != "thr_2" {
		t.Fatalf("expected thr_1, thr_2; got %s, %s", got[0].ThreadID, got[1].ThreadID)
	}
}

func TestProposeFromIndex_EmptyQueryReturnsNothing(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta"}, nil),
	}
	if got := ProposeFromIndex(spine, threads, nil, Options{}); len(got) != 0 {
		t.Fatalf("nil query: expected empty; got %#v", got)
	}
	if got := ProposeFromIndex(spine, threads, []string{}, Options{}); len(got) != 0 {
		t.Fatalf("empty query: expected empty; got %#v", got)
	}
	// All-empty-string entries should also produce nothing — uniqueNonEmpty
	// drops them, leaving |Q| = 0.
	if got := ProposeFromIndex(spine, threads, []string{"", ""}, Options{}); len(got) != 0 {
		t.Fatalf("query of empty strings: expected empty; got %#v", got)
	}
}

func TestProposeFromIndex_MatchedSymbolsSorted(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta", "zeta", "delta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta", "zeta", "delta"}, nil),
	}
	got := ProposeFromIndex(spine, threads, []string{"zeta", "alpha"}, Options{})
	if len(got) != 1 {
		t.Fatalf("expected one result; got %#v", got)
	}
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got[0].MatchedSymbols, want) {
		t.Fatalf("matched symbols: got %#v want %#v", got[0].MatchedSymbols, want)
	}
}

func TestProposeFromIndex_FrontmatterMissingFallsBackToAnchors(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta", "c", "d"}, 0),
	}
	// Note: threads slice deliberately omits thr_1.
	got := ProposeFromIndex(spine, nil, []string{"alpha", "beta"}, Options{})
	if len(got) != 1 || got[0].ThreadID != "thr_1" {
		t.Fatalf("expected thr_1 from anchors-only fallback; got %#v", got)
	}
	if got[0].Score != 0.5 {
		t.Fatalf("expected score 0.5 (2/4); got %v", got[0].Score)
	}
}

func TestProposeFromIndex_ZeroIntersectionDropped(t *testing.T) {
	spine := []memops.SpineRecord{
		makeSpine("thr_1", "prj_1", []string{"alpha", "beta"}, 0),
	}
	threads := []memops.ThreadFrontmatter{
		makeFM("thr_1", "prj_1", []string{"alpha", "beta"}, nil),
	}
	// Even with threshold 0, no overlap → no match.
	got := ProposeFromIndex(spine, threads, []string{"xyz"}, Options{Threshold: -1})
	if len(got) != 0 {
		t.Fatalf("expected empty (zero intersection); got %#v", got)
	}
}

// Integration coverage of the disk-loading path lives in
// fileadapter.TestProposeRecall_PassThrough — substrate I/O is the
// adapter's concern, not this package's, once the port migration (MAD
// C3) landed.
