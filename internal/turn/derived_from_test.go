package turn

import (
	"context"
	"reflect"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// symSet is a test helper: builds a normalized-symbol set from a list.
func symSet(norms ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(norms))
	for _, n := range norms {
		s[n] = struct{}{}
	}
	return s
}

// TestPopulateDerivedFrom_SingleAndMultiOrigin — a symbol coinciding with
// one surfaced thread acquires that thread's id; coinciding with two
// acquires both, sorted (§2.7.3).
func TestPopulateDerivedFrom_SingleAndMultiOrigin(t *testing.T) {
	merged := []memops.HistorySymbol{
		{Normalized: "alpha"}, // in thr_X only
		{Normalized: "beta"},  // in thr_X and thr_Y
		{Normalized: "delta"}, // in neither → organic
	}
	surfaced := map[string]map[string]struct{}{
		"thr_X": symSet("alpha", "beta"),
		"thr_Y": symSet("beta"),
	}
	populateDerivedFrom(merged, surfaced, "thr_self")

	if got := merged[0].DerivedFrom; !reflect.DeepEqual(got, []string{"thr_X"}) {
		t.Errorf("alpha DerivedFrom: got %#v want [thr_X]", got)
	}
	// beta matches two surfaced threads → sorted union.
	if got := merged[1].DerivedFrom; !reflect.DeepEqual(got, []string{"thr_X", "thr_Y"}) {
		t.Errorf("beta DerivedFrom: got %#v want [thr_X thr_Y]", got)
	}
	// delta matches nothing → organic (nil).
	if got := merged[2].DerivedFrom; got != nil {
		t.Errorf("delta DerivedFrom: got %#v want nil (organic)", got)
	}
}

// TestPopulateDerivedFrom_Organic — a symbol matching no surfaced thread is
// left organic (nil DerivedFrom), the common case.
func TestPopulateDerivedFrom_Organic(t *testing.T) {
	merged := []memops.HistorySymbol{{Normalized: "novel"}}
	surfaced := map[string]map[string]struct{}{"thr_X": symSet("other")}
	populateDerivedFrom(merged, surfaced, "thr_self")
	if merged[0].DerivedFrom != nil {
		t.Errorf("organic symbol DerivedFrom: got %#v want nil", merged[0].DerivedFrom)
	}
}

// TestPopulateDerivedFrom_Monotonic — a symbol re-emitted in a later turn
// that newly coincides with a fresh recall unions the new origin WITHOUT
// dropping a prior one (origins accumulate, never clear).
func TestPopulateDerivedFrom_Monotonic(t *testing.T) {
	// Turn N: the symbol already carries thr_OLD (a prior turn's origin).
	merged := []memops.HistorySymbol{
		{Normalized: "carried", DerivedFrom: []string{"thr_OLD"}},
	}
	// Turn N+M: thr_NEW is freshly recalled and also carries the symbol.
	surfaced := map[string]map[string]struct{}{"thr_NEW": symSet("carried")}
	populateDerivedFrom(merged, surfaced, "thr_self")

	want := []string{"thr_NEW", "thr_OLD"} // sorted union, prior origin retained
	if got := merged[0].DerivedFrom; !reflect.DeepEqual(got, want) {
		t.Errorf("monotonic DerivedFrom: got %#v want %#v", got, want)
	}

	// Idempotent: running again with the same surfaced set does not duplicate.
	populateDerivedFrom(merged, surfaced, "thr_self")
	if got := merged[0].DerivedFrom; !reflect.DeepEqual(got, want) {
		t.Errorf("idempotent re-run DerivedFrom: got %#v want %#v", got, want)
	}
}

// TestPopulateDerivedFrom_NotSelf — the engaged thread's own id is never
// added as an origin even when it is (defensively) present in the surfaced
// set: a thread is not its own origin.
func TestPopulateDerivedFrom_NotSelf(t *testing.T) {
	merged := []memops.HistorySymbol{{Normalized: "shared"}}
	surfaced := map[string]map[string]struct{}{
		"thr_self": symSet("shared"), // self defensively present
		"thr_X":    symSet("shared"),
	}
	populateDerivedFrom(merged, surfaced, "thr_self")
	if got := merged[0].DerivedFrom; !reflect.DeepEqual(got, []string{"thr_X"}) {
		t.Errorf("not-self DerivedFrom: got %#v want [thr_X] (self excluded)", got)
	}
}

// TestPopulateDerivedFrom_EmptySurfacedNoOp — no recall this turn leaves
// every symbol organic.
func TestPopulateDerivedFrom_EmptySurfacedNoOp(t *testing.T) {
	merged := []memops.HistorySymbol{{Normalized: "x", DerivedFrom: nil}}
	populateDerivedFrom(merged, nil, "thr_self")
	if merged[0].DerivedFrom != nil {
		t.Errorf("no-recall turn must not populate DerivedFrom; got %#v", merged[0].DerivedFrom)
	}
}

// TestSurfacedSymbolSets_MirrorsScorerSet — the I/O half: a recall-accepted
// thread's loaded symbol set is Anchors ∪ history_symbols Normalized — the
// same surface the §3.4 scorer matches against. selfID is never loaded.
func TestSurfacedSymbolSets_MirrorsScorerSet(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_self", []string{"omega"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.recallSurfaced = map[string]struct{}{"thr_1": {}, "thr_self": {}}
	// Both must be Layer-B resident to be eligible (residency gate).
	state.ActiveThreads = []string{"thr_1", "thr_self"}

	sets := surfacedSymbolSets(context.Background(), state, "thr_self")
	if _, ok := sets["thr_self"]; ok {
		t.Errorf("selfID must not be loaded as an origin: %v", sets)
	}
	got := sets["thr_1"]
	if got == nil {
		t.Fatalf("thr_1 set missing: %v", sets)
	}
	for _, want := range []string{"alpha", "beta"} {
		if _, ok := got[want]; !ok {
			t.Errorf("thr_1 symbol set missing %q: %v", want, got)
		}
	}
}

// TestSurfacedSymbolSets_EvictedFromLayerB_NotEligible is the over-attribution
// guard: a thread that was recalled (and so is in recallSurfaced) but has since
// been evicted from Layer B (no longer in state.ActiveThreads) must NOT be a
// candidate origin — its symbols would falsely attribute work emitted long
// after it left context. Eligibility = recallSurfaced ∩ ActiveThreads.
func TestSurfacedSymbolSets_EvictedFromLayerB_NotEligible(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_resident", []string{"alpha"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_evicted", []string{"beta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	// Both were recalled at some point this session...
	state.recallSurfaced = map[string]struct{}{"thr_resident": {}, "thr_evicted": {}}
	// ...but only thr_resident remains in Layer B; thr_evicted was displaced.
	state.ActiveThreads = []string{"thr_resident"}

	sets := surfacedSymbolSets(context.Background(), state, "thr_self")
	if _, ok := sets["thr_evicted"]; ok {
		t.Errorf("evicted-from-Layer-B thread must not be an eligible origin: %v", sets)
	}
	if _, ok := sets["thr_resident"]; !ok {
		t.Errorf("resident recalled thread must remain eligible: %v", sets)
	}

	// Opportunistic prune: the evicted id is dropped from recallSurfaced so the
	// map stays bounded; it can only return via a fresh recall.
	if _, ok := state.recallSurfaced["thr_evicted"]; ok {
		t.Errorf("evicted id should be pruned from recallSurfaced; got %v", state.recallSurfaced)
	}
	if _, ok := state.recallSurfaced["thr_resident"]; !ok {
		t.Errorf("resident id must be retained in recallSurfaced; got %v", state.recallSurfaced)
	}
}

// TestFetchThreadForReprompt_SameTurnDerivedFrom is the §5.5 mid-turn-fetch
// end-to-end proof: a fetch promotes parent X (carrying symbol S) into Layer B
// BEFORE the turn's merge, so X is resident when the engaged thread merges S —
// and S's DerivedFrom acquires X within the SAME turn (no one-turn lag, the
// property that distinguishes §5.5 fetch from §3.4 accept). The fetch path
// reaches promoteToLayerB via fetchThreadForReprompt; the chokepoint now owns
// the recallSurfaced marking, so this exercises exactly the production wiring.
func TestFetchThreadForReprompt_SameTurnDerivedFrom(t *testing.T) {
	paths, meta := newTestHome(t)
	// Parent X carries S; the engaged thread starts without it.
	seedThreadWithAnchors(t, paths, meta.ID, "thr_X", []string{"shared_sym"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_engaged", []string{"engaged_own"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))

	// §5.5 mid-turn fetch of X. This is the path that calls promoteToLayerB:
	// it both pulls X into ActiveThreads AND marks it recallSurfaced — all
	// BEFORE this turn's merge runs.
	if !fetchThreadForReprompt(context.Background(), state, "thr_X") {
		t.Fatalf("fetchThreadForReprompt(thr_X) returned false; want success")
	}
	if _, ok := state.recallSurfaced["thr_X"]; !ok {
		t.Fatalf("fetch must mark thr_X recallSurfaced via promoteToLayerB; got %v", state.recallSurfaced)
	}
	if len(state.ActiveThreads) == 0 || state.ActiveThreads[0] != "thr_X" {
		t.Fatalf("fetch must promote thr_X to front of ActiveThreads; got %v", state.ActiveThreads)
	}

	// Same turn: the engaged thread emits S. Run the production merge-time
	// attribution exactly as engageOwner/createNewThread do — surfacedSymbolSets
	// (residency-gated load) feeding populateDerivedFrom.
	merged := mergeHistorySymbols(nil, []coalescedSymbol{
		{Normalized: "shared_sym", Raw: "shared_sym", Source: memops.SourceModel},
	}, 1)
	populateDerivedFrom(merged, surfacedSymbolSets(context.Background(), state, "thr_engaged"), "thr_engaged")

	if len(merged) != 1 {
		t.Fatalf("expected one merged symbol; got %d", len(merged))
	}
	if got := merged[0].DerivedFrom; !reflect.DeepEqual(got, []string{"thr_X"}) {
		t.Errorf("same-turn §5.5 fetch: shared_sym DerivedFrom = %#v, want [thr_X] within the SAME turn", got)
	}
}

// TestUnionSorted — the sorted-dedup union primitive: insert keeps order,
// duplicate is a no-op returning the same slice.
func TestUnionSorted(t *testing.T) {
	got := unionSorted([]string{"thr_2", "thr_5"}, "thr_3")
	if !reflect.DeepEqual(got, []string{"thr_2", "thr_3", "thr_5"}) {
		t.Errorf("insert middle: got %#v", got)
	}
	got = unionSorted([]string{"thr_2", "thr_5"}, "thr_5")
	if !reflect.DeepEqual(got, []string{"thr_2", "thr_5"}) {
		t.Errorf("duplicate no-op: got %#v", got)
	}
	got = unionSorted(nil, "thr_1")
	if !reflect.DeepEqual(got, []string{"thr_1"}) {
		t.Errorf("nil base: got %#v", got)
	}
}
