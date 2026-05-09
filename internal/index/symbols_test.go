package index

import (
	"reflect"
	"testing"

	"personant/internal/store"
)

func mkSpine(id, project string, recallFires int, anchors ...string) store.SpineRecord {
	return store.SpineRecord{
		ID: id, Project: project, Anchors: anchors,
		Summary: "s", State: store.ThreadActive,
		Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
		TurnCount: 1, RecallFires: recallFires,
	}
}

func TestBuildSymbolsEmptySpine(t *testing.T) {
	got := BuildSymbols(nil)
	if len(got) != 0 {
		t.Fatalf("empty spine: got %d records, want 0", len(got))
	}
}

func TestBuildSymbolsSingleThreadFourAnchors(t *testing.T) {
	spine := []store.SpineRecord{
		mkSpine("thr_1", "prj_1", 0, "alpha", "beta", "gamma", "delta"),
	}
	got := BuildSymbols(spine)
	if len(got) != 4 {
		t.Fatalf("got %d records, want 4: %#v", len(got), got)
	}
	wantSyms := []string{"alpha", "beta", "delta", "gamma"} // lexical
	for i, r := range got {
		if r.Symbol != wantSyms[i] {
			t.Errorf("position %d: symbol %q, want %q", i, r.Symbol, wantSyms[i])
		}
		if !reflect.DeepEqual(r.Threads, []string{"thr_1"}) {
			t.Errorf("symbol %q: threads %v, want [thr_1]", r.Symbol, r.Threads)
		}
		if !reflect.DeepEqual(r.AnchorIn, []string{"thr_1"}) {
			t.Errorf("symbol %q: anchor_in %v, want [thr_1]", r.Symbol, r.AnchorIn)
		}
		if r.SourceDominant != store.SourceCurator {
			t.Errorf("symbol %q: source_dominant %q, want curator", r.Symbol, r.SourceDominant)
		}
	}
}

func TestBuildSymbolsSharedAnchorAcrossThreads(t *testing.T) {
	spine := []store.SpineRecord{
		mkSpine("thr_1", "prj_1", 0, "alpha", "beta"),
		mkSpine("thr_2", "prj_1", 0, "alpha", "gamma"),
	}
	got := BuildSymbols(spine)
	bySym := make(map[string]store.SymbolRecord, len(got))
	for _, r := range got {
		bySym[r.Symbol] = r
	}
	alpha, ok := bySym["alpha"]
	if !ok {
		t.Fatal("missing symbol alpha")
	}
	wantThreads := []string{"thr_1", "thr_2"} // recall=0 ties, lexical
	if !reflect.DeepEqual(alpha.Threads, wantThreads) {
		t.Errorf("alpha.threads = %v, want %v", alpha.Threads, wantThreads)
	}
	if !reflect.DeepEqual(alpha.AnchorIn, wantThreads) {
		t.Errorf("alpha.anchor_in = %v, want %v", alpha.AnchorIn, wantThreads)
	}
}

func TestBuildSymbolsThreadsSortedByRecallFires(t *testing.T) {
	spine := []store.SpineRecord{
		mkSpine("thr_1", "prj_1", 1, "alpha"),
		mkSpine("thr_2", "prj_1", 5, "alpha"),
		mkSpine("thr_3", "prj_1", 5, "alpha"), // tie with thr_2
		mkSpine("thr_4", "prj_1", 0, "alpha"),
	}
	got := BuildSymbols(spine)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	want := []string{"thr_2", "thr_3", "thr_1", "thr_4"}
	if !reflect.DeepEqual(got[0].Threads, want) {
		t.Errorf("threads = %v, want %v", got[0].Threads, want)
	}
}

func TestBuildSymbolsAnchorInIsAnIndependentSlice(t *testing.T) {
	// Regression guard: mutating Threads through one alias must not be
	// visible through AnchorIn (or vice versa).
	spine := []store.SpineRecord{mkSpine("thr_1", "prj_1", 0, "alpha")}
	got := BuildSymbols(spine)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	got[0].Threads[0] = "MUTATED"
	if got[0].AnchorIn[0] == "MUTATED" {
		t.Fatal("anchor_in shares backing array with threads")
	}
}
