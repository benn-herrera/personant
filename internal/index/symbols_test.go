package index

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

func mkSpine(id, project string, recallFires int, anchors ...string) memops.SpineRecord {
	return memops.SpineRecord{
		ID: id, Project: project, Anchors: anchors,
		Summary: "s", State: memops.ThreadActive,
		Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
		TurnCount: 1, RecallFires: recallFires,
	}
}

// mkThread builds a minimal ThreadFrontmatter with the given ID and
// history_symbols. Other required fields are stubbed; tests that
// exercise spine ordering provide the matching SpineRecord separately.
func mkThread(id, project string, history ...memops.HistorySymbol) memops.ThreadFrontmatter {
	return memops.ThreadFrontmatter{
		ID: id, Project: project,
		Summary: "s", State: memops.ThreadActive,
		Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
		TurnCount: 1, RecallFires: 0,
		HistorySymbols: history,
	}
}

func mkHistorySym(normalized string, source memops.SymbolSource) memops.HistorySymbol {
	return memops.HistorySymbol{
		Raw:           normalized,
		Normalized:    normalized,
		FirstSeenTurn: 1,
		Count:         1,
		Source:        source,
	}
}

func TestBuildSymbolsEmpty(t *testing.T) {
	got := BuildSymbols(nil, nil)
	if len(got) != 0 {
		t.Fatalf("empty inputs: got %d records, want 0", len(got))
	}
}

func TestBuildSymbolsSingleThreadFourAnchors(t *testing.T) {
	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 0, "alpha", "beta", "gamma", "delta"),
	}
	got := BuildSymbols(spine, nil)
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
		if r.SourceDominant != memops.SourceCurator {
			t.Errorf("symbol %q: source_dominant %q, want curator", r.Symbol, r.SourceDominant)
		}
	}
}

func TestBuildSymbolsSharedAnchorAcrossThreads(t *testing.T) {
	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 0, "alpha", "beta"),
		mkSpine("thr_2", "prj_1", 0, "alpha", "gamma"),
	}
	got := BuildSymbols(spine, nil)
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
	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 1, "alpha"),
		mkSpine("thr_2", "prj_1", 5, "alpha"),
		mkSpine("thr_3", "prj_1", 5, "alpha"), // tie with thr_2
		mkSpine("thr_4", "prj_1", 0, "alpha"),
	}
	got := BuildSymbols(spine, nil)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	want := []string{"thr_2", "thr_3", "thr_1", "thr_4"}
	if !reflect.DeepEqual(got[0].Threads, want) {
		t.Errorf("threads = %v, want %v", got[0].Threads, want)
	}
}

// TestBuildSymbolsAnchorInIsAnIndependentSlice is a regression guard:
// mutating Threads through one alias must not be visible through
// AnchorIn (or vice versa).
func TestBuildSymbolsAnchorInIsAnIndependentSlice(t *testing.T) {
	spine := []memops.SpineRecord{mkSpine("thr_1", "prj_1", 0, "alpha")}
	got := BuildSymbols(spine, nil)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	got[0].Threads[0] = "MUTATED"
	if got[0].AnchorIn[0] == "MUTATED" {
		t.Fatal("anchor_in shares backing array with threads")
	}
}

// TestBuildSymbolsCombinesAnchorsAndHistory exercises the core merge
// behavior: anchors contribute curator-source thread membership and
// anchor_in membership; history_symbols contribute thread membership
// only (never anchor_in) with their stored source.
func TestBuildSymbolsCombinesAnchorsAndHistory(t *testing.T) {
	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 0, "alpha", "beta"),
	}
	threads := []memops.ThreadFrontmatter{
		mkThread("thr_1", "prj_1", mkHistorySym("gamma", memops.SourceModel)),
	}
	got := BuildSymbols(spine, threads)
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3: %#v", len(got), got)
	}
	bySym := make(map[string]store.SymbolRecord, len(got))
	for _, r := range got {
		bySym[r.Symbol] = r
	}

	for _, sym := range []string{"alpha", "beta"} {
		r, ok := bySym[sym]
		if !ok {
			t.Fatalf("missing anchor symbol %q", sym)
		}
		if !reflect.DeepEqual(r.Threads, []string{"thr_1"}) {
			t.Errorf("%s.threads = %v, want [thr_1]", sym, r.Threads)
		}
		if !reflect.DeepEqual(r.AnchorIn, []string{"thr_1"}) {
			t.Errorf("%s.anchor_in = %v, want [thr_1]", sym, r.AnchorIn)
		}
		if r.SourceDominant != memops.SourceCurator {
			t.Errorf("%s.source_dominant = %q, want curator", sym, r.SourceDominant)
		}
	}

	gamma, ok := bySym["gamma"]
	if !ok {
		t.Fatal("missing history-only symbol gamma")
	}
	if !reflect.DeepEqual(gamma.Threads, []string{"thr_1"}) {
		t.Errorf("gamma.threads = %v, want [thr_1]", gamma.Threads)
	}
	if len(gamma.AnchorIn) != 0 {
		t.Errorf("gamma.anchor_in = %v, want empty (history-only)", gamma.AnchorIn)
	}
	if gamma.SourceDominant != memops.SourceModel {
		t.Errorf("gamma.source_dominant = %q, want model", gamma.SourceDominant)
	}
}

// TestBuildSymbolsDominantSourceAggregation pins the §2.7.3 precedence
// when the same normalized symbol appears as a curator anchor in one
// thread and a user-source history_symbol in another:
// curator > user → SourceCurator wins, threads is the union, anchor_in
// is just the anchor side.
func TestBuildSymbolsDominantSourceAggregation(t *testing.T) {
	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 5, "alpha"),
		mkSpine("thr_2", "prj_1", 1), // no anchors, just engagement bookkeeping
	}
	threads := []memops.ThreadFrontmatter{
		mkThread("thr_2", "prj_1", mkHistorySym("alpha", memops.SourceUser)),
	}
	got := BuildSymbols(spine, threads)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1: %#v", len(got), got)
	}
	r := got[0]
	if r.Symbol != "alpha" {
		t.Fatalf("symbol = %q, want alpha", r.Symbol)
	}
	if r.SourceDominant != memops.SourceCurator {
		t.Errorf("source_dominant = %q, want curator (curator > user)", r.SourceDominant)
	}
	// thr_1 has recall_fires=5, thr_2 has 1 → thr_1 first.
	wantThreads := []string{"thr_1", "thr_2"}
	if !reflect.DeepEqual(r.Threads, wantThreads) {
		t.Errorf("threads = %v, want %v", r.Threads, wantThreads)
	}
	if !reflect.DeepEqual(r.AnchorIn, []string{"thr_1"}) {
		t.Errorf("anchor_in = %v, want [thr_1]", r.AnchorIn)
	}
}

// TestBuildSymbolsHistoryOnlyMultiThread exercises a symbol that only
// appears via history_symbols across two threads with different
// sources; the dominant source wins, anchor_in stays empty, threads
// follows recall_fires ordering.
func TestBuildSymbolsHistoryOnlyMultiThread(t *testing.T) {
	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 2),
		mkSpine("thr_2", "prj_1", 9),
	}
	threads := []memops.ThreadFrontmatter{
		mkThread("thr_1", "prj_1", mkHistorySym("zeta", memops.SourceDeterministic)),
		mkThread("thr_2", "prj_1", mkHistorySym("zeta", memops.SourceModel)),
	}
	got := BuildSymbols(spine, threads)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	r := got[0]
	if r.SourceDominant != memops.SourceModel {
		t.Errorf("source_dominant = %q, want model (model > deterministic)", r.SourceDominant)
	}
	wantThreads := []string{"thr_2", "thr_1"} // recall 9 > 2
	if !reflect.DeepEqual(r.Threads, wantThreads) {
		t.Errorf("threads = %v, want %v", r.Threads, wantThreads)
	}
	if len(r.AnchorIn) != 0 {
		t.Errorf("anchor_in = %v, want empty", r.AnchorIn)
	}
}

// TestBuildSymbolsHistorySymbolWithEmptyNormalizedIsIgnored guards
// against a malformed thread frontmatter where a HistorySymbol has no
// Normalized key. Such an entry would otherwise create a "" symbol
// bucket; index building tolerates it by skipping.
func TestBuildSymbolsHistorySymbolWithEmptyNormalizedIsIgnored(t *testing.T) {
	spine := []memops.SpineRecord{mkSpine("thr_1", "prj_1", 0)}
	threads := []memops.ThreadFrontmatter{
		mkThread("thr_1", "prj_1",
			memops.HistorySymbol{Raw: "junk", Normalized: "", Source: memops.SourceModel}),
	}
	got := BuildSymbols(spine, threads)
	if len(got) != 0 {
		t.Fatalf("got %d records, want 0 (empty-normalized history symbol must be skipped): %#v", len(got), got)
	}
}

// TestRebuildSymbolsRoundtrips seeds a spine and a thread file on disk,
// runs RebuildSymbols, and verifies the on-disk symbols.jsonl matches
// what BuildSymbols produces from the same inputs.
func TestRebuildSymbolsRoundtrips(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	if err := os.MkdirAll(paths.ThreadsDir, 0o755); err != nil {
		t.Fatalf("mkdir threads: %v", err)
	}

	spine := []memops.SpineRecord{
		mkSpine("thr_1", "prj_1", 5, "alpha", "beta"),
		mkSpine("thr_2", "prj_1", 2, "gamma"),
	}
	if err := store.WriteSpine(paths.Spine, spine); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}

	thr1 := memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID: "thr_1", Project: "prj_1",
			Anchors: []string{"alpha", "beta"},
			Summary: "s", State: memops.ThreadActive,
			Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
			TurnCount: 1, RecallFires: 5,
			HistorySymbols: []memops.HistorySymbol{
				mkHistorySym("alpha", memops.SourceCurator),
				mkHistorySym("delta", memops.SourceUser),
			},
		},
		Body: "body\n",
	}
	thr2 := memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID: "thr_2", Project: "prj_1",
			Anchors: []string{"gamma"},
			Summary: "s", State: memops.ThreadActive,
			Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
			TurnCount: 1, RecallFires: 2,
			HistorySymbols: []memops.HistorySymbol{
				mkHistorySym("gamma", memops.SourceCurator),
				mkHistorySym("delta", memops.SourceModel),
			},
		},
		Body: "body\n",
	}
	if err := store.SeedThread(paths, thr1); err != nil {
		t.Fatalf("SaveThread thr_1: %v", err)
	}
	if err := store.SeedThread(paths, thr2); err != nil {
		t.Fatalf("SaveThread thr_2: %v", err)
	}

	written, err := RebuildSymbols(paths)
	if err != nil {
		t.Fatalf("RebuildSymbols: %v", err)
	}
	read, err := store.ReadSymbols(paths.Symbols)
	if err != nil {
		t.Fatalf("ReadSymbols: %v", err)
	}
	if !reflect.DeepEqual(written, read) {
		t.Fatalf("on-disk symbols.jsonl mismatches BuildSymbols output:\n got: %#v\nwant: %#v", read, written)
	}

	// Spot-check key invariants the round-trip must preserve.
	bySym := make(map[string]store.SymbolRecord, len(read))
	for _, r := range read {
		bySym[r.Symbol] = r
	}
	delta, ok := bySym["delta"]
	if !ok {
		t.Fatal("expected symbol delta (history-only across two threads)")
	}
	// delta is history-only → anchor_in empty.
	if len(delta.AnchorIn) != 0 {
		t.Errorf("delta.anchor_in = %v, want empty", delta.AnchorIn)
	}
	// delta sources: user (thr_1) and model (thr_2) → user wins.
	if delta.SourceDominant != memops.SourceUser {
		t.Errorf("delta.source_dominant = %q, want user", delta.SourceDominant)
	}
	// delta threads: thr_1 (recall 5) before thr_2 (recall 2).
	wantDeltaThreads := []string{"thr_1", "thr_2"}
	if !reflect.DeepEqual(delta.Threads, wantDeltaThreads) {
		t.Errorf("delta.threads = %v, want %v", delta.Threads, wantDeltaThreads)
	}

	// alpha is anchor in thr_1 AND history with SourceCurator in thr_1
	// (no extra threads). anchor_in = [thr_1], threads = [thr_1].
	alpha := bySym["alpha"]
	if !reflect.DeepEqual(alpha.AnchorIn, []string{"thr_1"}) {
		t.Errorf("alpha.anchor_in = %v, want [thr_1]", alpha.AnchorIn)
	}
	if !reflect.DeepEqual(alpha.Threads, []string{"thr_1"}) {
		t.Errorf("alpha.threads = %v, want [thr_1]", alpha.Threads)
	}
	if alpha.SourceDominant != memops.SourceCurator {
		t.Errorf("alpha.source_dominant = %q, want curator", alpha.SourceDominant)
	}
}

// --- store helpers (LoadAllThreadFrontmatter, ListThreadIDs) -----------
// Tests for these live alongside thread_io_test.go but are exercised
// here through the index path; the dedicated unit tests for the helpers
// themselves are in internal/store.

func TestLoadAllThreadFrontmatterTolerantOnParseError(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	if err := os.MkdirAll(paths.ThreadsDir, 0o755); err != nil {
		t.Fatalf("mkdir threads: %v", err)
	}

	// Two well-formed threads.
	for _, id := range []string{"thr_1", "thr_2"} {
		th := memops.Thread{
			Frontmatter: memops.ThreadFrontmatter{
				ID: id, Project: "prj_1",
				Summary: "s", State: memops.ThreadActive,
				Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
			},
			Body: "b\n",
		}
		if err := store.SeedThread(paths, th); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	// One malformed thread — thread.md missing the closing delimiter.
	badDir := filepath.Join(paths.ThreadsDir, "thr_3")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad: %v", err)
	}
	bad := filepath.Join(badDir, "thread.md")
	if err := os.WriteFile(bad, []byte("---\nid: thr_3\nproject: prj_1\nno-close\n"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	var skipMessages []string
	logf := func(format string, args ...any) {
		skipMessages = append(skipMessages, format)
	}
	got, err := store.LoadAllThreadFrontmatter(paths, logf)
	if err != nil {
		t.Fatalf("LoadAllThreadFrontmatter: %v (must tolerate parse errors)", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d frontmatter entries, want 2 (the two well-formed)", len(got))
	}
	if len(skipMessages) != 1 {
		t.Errorf("got %d skip messages, want 1: %v", len(skipMessages), skipMessages)
	}
}

func TestLoadAllThreadFrontmatterMissingDir(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	// Note: do NOT mkdir ThreadsDir — fresh init state.

	got, err := store.LoadAllThreadFrontmatter(paths, nil)
	if err != nil {
		t.Fatalf("LoadAllThreadFrontmatter: %v (missing dir is fresh-init, not error)", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil slice on missing ThreadsDir", got)
	}
}

func TestListThreadIDsSortedAndFiltered(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	if err := os.MkdirAll(paths.ThreadsDir, 0o755); err != nil {
		t.Fatalf("mkdir threads: %v", err)
	}

	// Create a mix of canonical thread directories and noise.
	for _, name := range []string{"thr_3", "thr_1", "thr_10", "notes", "thr_abc"} {
		p := filepath.Join(paths.ThreadsDir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	// A stray loose file under threads/ must be ignored (only dirs count).
	if err := os.WriteFile(filepath.Join(paths.ThreadsDir, "stray.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	got, err := store.ListThreadIDs(paths)
	if err != nil {
		t.Fatalf("ListThreadIDs: %v", err)
	}
	want := []string{"thr_1", "thr_10", "thr_3"} // lexical, ignores non-canonical
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Also verify the result is in fact sorted (defensive — exercises
	// the documented contract independently of the want-comparison).
	if !sort.StringsAreSorted(got) {
		t.Errorf("ListThreadIDs not sorted: %v", got)
	}
}

func TestListThreadIDsMissingDir(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	got, err := store.ListThreadIDs(paths)
	if err != nil {
		t.Fatalf("ListThreadIDs missing dir: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil on missing dir", got)
	}
}
