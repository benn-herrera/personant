package fileadapter

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

// sortMetas returns metas sorted by ID for order-independent comparison.
// store.LoadAllThreadFrontmatter and frontmatterCache.LoadAll both walk
// ListThreadIDs (lexically sorted) so order already matches, but sorting
// makes the equivalence assertions robust to that being an accident.
func sortMetas(ms []memops.ThreadMeta) []memops.ThreadMeta {
	out := make([]memops.ThreadMeta, len(ms))
	copy(out, ms)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// seedThreadMeta writes thread.md frontmatter directly via the store
// (an out-of-band write, NOT through the adapter) and returns the meta.
func seedThreadMeta(t *testing.T, paths store.PersonantPaths, id string, lifecycle memops.SymbolLifecycle, everCentral bool) memops.ThreadMeta {
	t.Helper()
	now := rfc3339Now()
	fm := memops.ThreadMeta{
		ID:           id,
		Project:      "prj_1",
		Anchors:      []string{"alpha", "beta", "gamma", "delta"},
		Summary:      "seeded " + id,
		State:        memops.ThreadActive,
		Created:      now,
		LastEngaged:  now,
		StateChanged: now,
		TurnCount:    3,
		HistorySymbols: []memops.HistorySymbol{
			{Raw: "alpha", Normalized: "alpha", FirstSeenTurn: 1, Count: 2, Source: memops.SourceModel, Lifecycle: lifecycle, EverCentral: everCentral, LastActiveTurn: 1},
			{Raw: "beta", Normalized: "beta", FirstSeenTurn: 2, Count: 1, Source: memops.SourceModel, Lifecycle: memops.LifecycleActive, LastActiveTurn: 2},
		},
	}
	if err := store.SaveThreadFrontmatter(paths, id, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter %s: %v", id, err)
	}
	return fm
}

// TestFMCache_ObservationalEquivalence proves cache.LoadAll returns the
// same set+field values as store.LoadAllThreadFrontmatter — on a cold
// cache (all misses) and again on a warm cache (all hits).
func TestFMCache_ObservationalEquivalence(t *testing.T) {
	a := newAdapter(t)

	seedThreadMeta(t, a.paths, "thr_1", memops.LifecycleActive, false)
	seedThreadMeta(t, a.paths, "thr_2", memops.LifecycleSuperseded, true)
	seedThreadMeta(t, a.paths, "thr_3", memops.LifecycleActive, true)

	want, err := store.LoadAllThreadFrontmatter(a.paths, nil)
	if err != nil {
		t.Fatalf("store.LoadAllThreadFrontmatter: %v", err)
	}

	cold, err := a.fmCache.LoadAll(a.paths, nil)
	if err != nil {
		t.Fatalf("cache.LoadAll (cold): %v", err)
	}
	if !reflect.DeepEqual(sortMetas(want), sortMetas(cold)) {
		t.Fatalf("cold LoadAll != store.LoadAllThreadFrontmatter\nwant %+v\ngot  %+v", want, cold)
	}

	warm, err := a.fmCache.LoadAll(a.paths, nil)
	if err != nil {
		t.Fatalf("cache.LoadAll (warm): %v", err)
	}
	if !reflect.DeepEqual(sortMetas(want), sortMetas(warm)) {
		t.Fatalf("warm LoadAll != baseline\nwant %+v\ngot  %+v", want, warm)
	}
}

// recallSymbols returns the matched-symbol union ProposeRecall reports for
// a single query symbol against the whole substrate. It is the observable
// surface the anti-staleness test asserts on.
func proposeContains(t *testing.T, a *FileAdapter, query []string, threadID string) bool {
	t.Helper()
	// Low threshold so a single matched symbol against a small thread set
	// clears the bar; the test asserts on stale-vs-fresh symbol membership,
	// not on Jaccard tuning.
	cands, err := a.ProposeRecall(context.Background(), query, memops.RecallOptions{Project: "prj_1", Threshold: 0.01})
	if err != nil {
		t.Fatalf("ProposeRecall: %v", err)
	}
	for _, c := range cands {
		if c.ThreadID == threadID {
			return true
		}
	}
	return false
}

// TestFMCache_AntiStalenessCoherence is the Inc-6 guard: drive create →
// engage(change symbols) → archive entirely through the adapter and prove
// ProposeRecall always reflects the current on-disk symbols, never a stale
// cached parse.
func TestFMCache_AntiStalenessCoherence(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	fm := validFrontmatter(rec)
	fm.HistorySymbols = []memops.HistorySymbol{
		{Raw: "oldsym", Normalized: "oldsym", FirstSeenTurn: 1, Count: 1, Source: memops.SourceModel, Lifecycle: memops.LifecycleActive, LastActiveTurn: 1},
	}
	fm.Anchors = []string{"oldsym", "beta", "gamma", "delta"}
	rec.Anchors = fm.Anchors
	if err := a.CreateThread(ctx, memops.ThreadWrite{Spine: rec, Meta: fm, TurnExcerpt: "## Turn 1\n\nbody\n"}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	// Create write-through: recall on the old symbol finds the thread.
	if !proposeContains(t, a, []string{"oldsym"}, "thr_1") {
		t.Fatal("after CreateThread, ProposeRecall(oldsym) did not find thr_1")
	}

	// Engage with CHANGED symbols. If the cache served a stale parse, the
	// new symbol would not match and the old one would still match.
	fm2 := fm
	fm2.TurnCount = 2
	fm2.HistorySymbols = []memops.HistorySymbol{
		{Raw: "newsym", Normalized: "newsym", FirstSeenTurn: 2, Count: 1, Source: memops.SourceModel, Lifecycle: memops.LifecycleActive, LastActiveTurn: 2},
	}
	fm2.Anchors = []string{"newsym", "beta", "gamma", "delta"}
	rec2 := rec
	rec2.TurnCount = 2
	rec2.Anchors = fm2.Anchors
	if err := a.EngageThread(ctx, memops.ThreadWrite{Spine: rec2, Meta: fm2, TurnExcerpt: "## Turn 2\n\nmore\n"}); err != nil {
		t.Fatalf("EngageThread: %v", err)
	}

	if !proposeContains(t, a, []string{"newsym"}, "thr_1") {
		t.Fatal("after EngageThread, ProposeRecall(newsym) did not find thr_1 — STALE cache hit")
	}
	if proposeContains(t, a, []string{"oldsym"}, "thr_1") {
		t.Fatal("after EngageThread, ProposeRecall(oldsym) still found thr_1 — STALE cache hit")
	}

	// Archive: ProposeRecall must no longer return it, and a fresh LoadAll
	// must drop it from the live set.
	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}
	if proposeContains(t, a, []string{"newsym"}, "thr_1") {
		t.Fatal("after ArchiveThread, ProposeRecall still found thr_1")
	}
	metas, err := a.fmCache.LoadAll(a.paths, nil)
	if err != nil {
		t.Fatalf("LoadAll after archive: %v", err)
	}
	for _, m := range metas {
		if m.ID == "thr_1" {
			t.Fatal("after ArchiveThread, LoadAll still returned thr_1")
		}
	}
}

// TestFMCache_InvalidateHooks documents the coherence invariant in
// executable form: an out-of-band store write is invisible to a warm cache
// until InvalidateThread / InvalidateAll is called.
func TestFMCache_InvalidateHooks(t *testing.T) {
	a := newAdapter(t)

	fm := seedThreadMeta(t, a.paths, "thr_1", memops.LifecycleActive, false)
	// Warm the cache.
	if _, err := a.fmCache.LoadAll(a.paths, nil); err != nil {
		t.Fatalf("warm LoadAll: %v", err)
	}

	// Out-of-band write (bypasses the adapter) changes the summary.
	fm.Summary = "changed out of band"
	if err := store.SaveThreadFrontmatter(a.paths, "thr_1", fm); err != nil {
		t.Fatalf("out-of-band SaveThreadFrontmatter: %v", err)
	}

	// Still stale: cache served the old parse.
	got, err := a.fmCache.LoadAll(a.paths, nil)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got[0].Summary == "changed out of band" {
		t.Fatal("cache reflected an out-of-band write without invalidation — invariant broken")
	}

	// InvalidateThread corrects it.
	a.InvalidateThread("thr_1")
	got, err = a.fmCache.LoadAll(a.paths, nil)
	if err != nil {
		t.Fatalf("LoadAll after InvalidateThread: %v", err)
	}
	if got[0].Summary != "changed out of band" {
		t.Fatalf("after InvalidateThread, cache still stale: %q", got[0].Summary)
	}

	// Same for InvalidateAll: another out-of-band write, corrected by reset.
	fm.Summary = "changed again"
	if err := store.SaveThreadFrontmatter(a.paths, "thr_1", fm); err != nil {
		t.Fatalf("out-of-band SaveThreadFrontmatter: %v", err)
	}
	a.InvalidateAll()
	got, err = a.fmCache.LoadAll(a.paths, nil)
	if err != nil {
		t.Fatalf("LoadAll after InvalidateAll: %v", err)
	}
	if got[0].Summary != "changed again" {
		t.Fatalf("after InvalidateAll, cache still stale: %q", got[0].Summary)
	}
}
