package exact_test

import (
	"context"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/recall/exact"
	"personant/internal/store"
)

// fakeSource is a deterministic in-memory excerptSource (no I/O) so the
// lexical-scan logic — match enumeration, ordering, line/offset locating,
// the case-insensitive option — is tested against the unit directly rather
// than through the full substrate stack. A real-substrate end-to-end case
// (TestGrepThreads_RealSubstrate) covers the port wiring.
type fakeSource struct {
	excerpts map[string][]memops.ThreadExcerpt
	summary  map[string]string
}

func (f fakeSource) LoadThreadExcerpts(_ context.Context, id string) ([]memops.ThreadExcerpt, error) {
	return f.excerpts[id], nil
}

func (f fakeSource) LoadThreadMeta(_ context.Context, id string) (memops.ThreadMeta, error) {
	return memops.ThreadMeta{ID: id, Project: "prj_1", Summary: f.summary[id]}, nil
}

// TestGrepThreads_Exhaustive: every occurrence of the pattern across every
// turn of every supplied thread is returned, in (thread, turn, offset)
// order. This is the whole point of the tier — exact + exhaustive, no
// truncation or ranking.
func TestGrepThreads_Exhaustive(t *testing.T) {
	src := fakeSource{
		excerpts: map[string][]memops.ThreadExcerpt{
			"thr_2": {
				{TurnNumber: 3, Text: "quaternion rotation\nno match here"},
				{TurnNumber: 7, Text: "a quaternion and another quaternion"},
			},
			"thr_1": {
				{TurnNumber: 5, Text: "the quaternion algebra"},
			},
		},
	}

	got, err := exact.GrepThreads(context.Background(), src,
		[]string{"thr_2", "thr_1"}, "quaternion", exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads: %v", err)
	}

	// 4 occurrences total: thr_1/5 (×1), thr_2/3 (×1), thr_2/7 (×2).
	// Ordered by thread id, then turn, then offset.
	want := []struct {
		thread string
		turn   int
		match  string
	}{
		{"thr_1", 5, "quaternion"},
		{"thr_2", 3, "quaternion"},
		{"thr_2", 7, "quaternion"},
		{"thr_2", 7, "quaternion"},
	}
	if len(got) != len(want) {
		t.Fatalf("match count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ThreadID != w.thread || got[i].TurnNumber != w.turn || got[i].MatchText != w.match {
			t.Errorf("match[%d] = {%s turn=%d %q}, want {%s turn=%d %q}",
				i, got[i].ThreadID, got[i].TurnNumber, got[i].MatchText, w.thread, w.turn, w.match)
		}
	}
	// The two thr_2/turn-7 hits must be in ascending byte offset (deterministic).
	if got[2].Offset >= got[3].Offset {
		t.Errorf("two hits on the same excerpt not offset-ordered: %d, %d", got[2].Offset, got[3].Offset)
	}
}

// TestGrepThreads_LineLocating: a match on a later line reports the correct
// 1-based line number and the full line text.
func TestGrepThreads_LineLocating(t *testing.T) {
	src := fakeSource{
		excerpts: map[string][]memops.ThreadExcerpt{
			"thr_1": {{TurnNumber: 1, Text: "first line\nsecond has tensor\nthird line"}},
		},
	}
	got, err := exact.GrepThreads(context.Background(), src, []string{"thr_1"}, "tensor", exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %d: %+v", len(got), got)
	}
	if got[0].Line != 2 {
		t.Errorf("Line = %d, want 2", got[0].Line)
	}
	if got[0].LineText != "second has tensor" {
		t.Errorf("LineText = %q, want %q", got[0].LineText, "second has tensor")
	}
}

// TestGrepThreads_CaseInsensitive: the option matches regardless of case;
// without it, only the exact case matches.
func TestGrepThreads_CaseInsensitive(t *testing.T) {
	src := fakeSource{
		excerpts: map[string][]memops.ThreadExcerpt{
			"thr_1": {{TurnNumber: 1, Text: "Quaternion and quaternion and QUATERNION"}},
		},
	}
	sensitive, err := exact.GrepThreads(context.Background(), src, []string{"thr_1"}, "quaternion", exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads (sensitive): %v", err)
	}
	if len(sensitive) != 1 {
		t.Errorf("case-sensitive: want 1 match (lowercase only), got %d: %+v", len(sensitive), sensitive)
	}

	insensitive, err := exact.GrepThreads(context.Background(), src, []string{"thr_1"}, "quaternion", exact.Options{CaseInsensitive: true}, nil)
	if err != nil {
		t.Fatalf("GrepThreads (insensitive): %v", err)
	}
	if len(insensitive) != 3 {
		t.Errorf("case-insensitive: want 3 matches, got %d: %+v", len(insensitive), insensitive)
	}
}

// TestGrepThreads_ZeroMatches: a pattern matching nothing returns empty, not
// an error.
func TestGrepThreads_ZeroMatches(t *testing.T) {
	src := fakeSource{
		excerpts: map[string][]memops.ThreadExcerpt{
			"thr_1": {{TurnNumber: 1, Text: "nothing relevant here"}},
		},
	}
	got, err := exact.GrepThreads(context.Background(), src, []string{"thr_1"}, "manifold", exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no matches, got %d: %+v", len(got), got)
	}
}

// TestGrepThreads_RegexpPattern: the pattern is a Go regexp, not a literal.
func TestGrepThreads_RegexpPattern(t *testing.T) {
	src := fakeSource{
		excerpts: map[string][]memops.ThreadExcerpt{
			"thr_1": {{TurnNumber: 1, Text: "rev1 rev22 revX"}},
		},
	}
	got, err := exact.GrepThreads(context.Background(), src, []string{"thr_1"}, `rev[0-9]+`, exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 numeric-rev matches, got %d: %+v", len(got), got)
	}
	if got[0].MatchText != "rev1" || got[1].MatchText != "rev22" {
		t.Errorf("matched substrings = %q, %q; want rev1, rev22", got[0].MatchText, got[1].MatchText)
	}
}

// TestGrepThreads_BadPattern: an uncompilable regexp is a returned error.
func TestGrepThreads_BadPattern(t *testing.T) {
	got, err := exact.GrepThreads(context.Background(), fakeSource{}, []string{"thr_1"}, "(unclosed", exact.Options{}, nil)
	if err == nil {
		t.Fatalf("want compile error, got nil (matches=%+v)", got)
	}
}

// TestGrepThreads_MetadataMatch: a hit in the thread's metadata summary
// (thread.md) is returned at the metadata turn sentinel, ahead of turn hits.
func TestGrepThreads_MetadataMatch(t *testing.T) {
	src := fakeSource{
		summary:  map[string]string{"thr_1": "manifold curvature study"},
		excerpts: map[string][]memops.ThreadExcerpt{"thr_1": {{TurnNumber: 4, Text: "manifold again"}}},
	}
	got, err := exact.GrepThreads(context.Background(), src, []string{"thr_1"}, "manifold", exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 matches (summary + turn), got %d: %+v", len(got), got)
	}
	if got[0].TurnNumber != 0 {
		t.Errorf("metadata match should sort first at turn 0, got turn %d", got[0].TurnNumber)
	}
	if got[1].TurnNumber != 4 {
		t.Errorf("turn match = %d, want 4", got[1].TurnNumber)
	}
}

// TestGrepThreads_RealSubstrate exercises the primitive end-to-end over the
// real file substrate via the fileadapter (which satisfies excerptSource),
// proving the port wiring and that retained excerpts on disk are scanned.
func TestGrepThreads_RealSubstrate(t *testing.T) {
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	seedThreadWithTurns(t, paths, "thr_1", []string{
		"early on we chose quaternion math",
		"a later turn, no keyword",
		"back to quaternion details",
	})

	ops := fileadapter.NewFileAdapter(paths)
	got, err := exact.GrepThreads(context.Background(), ops, []string{"thr_1"}, "quaternion", exact.Options{}, nil)
	if err != nil {
		t.Fatalf("GrepThreads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 matches across retained turns, got %d: %+v", len(got), got)
	}
	if got[0].TurnNumber != 1 || got[1].TurnNumber != 3 {
		t.Errorf("matched turns = %d, %d; want 1, 3", got[0].TurnNumber, got[1].TurnNumber)
	}
}

func seedThreadWithTurns(t *testing.T, paths store.PersonantPaths, id string, turns []string) {
	t.Helper()
	ts := "2026-04-01T00:00:00Z"
	rec := memops.SpineRecord{
		ID: id, Project: "prj_1", Summary: id, State: memops.ThreadActive,
		Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: len(turns),
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", id, err)
	}
	if err := store.SaveThreadFrontmatter(paths, id, memops.ThreadMeta{
		ID: id, Project: "prj_1", Summary: id, State: memops.ThreadActive,
		Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: len(turns),
	}); err != nil {
		t.Fatalf("seed frontmatter %s: %v", id, err)
	}
	for i, text := range turns {
		if err := store.AppendThreadTurn(paths, id, i+1, text); err != nil {
			t.Fatalf("append turn %d to %s: %v", i+1, id, err)
		}
	}
}

// TestMatchExcerptsBySymbols covers the #123 recall-completeness lexical
// matcher: token-level, case-insensitive, whole-word, multi-word-anchor
// splitting, and turn ordering/de-dup — the independently verifiable logic of
// the bounded debt-window pass.
func TestMatchExcerptsBySymbols(t *testing.T) {
	ex := func(turn int, text string) memops.ThreadExcerpt {
		return memops.ThreadExcerpt{TurnNumber: turn, Text: text}
	}
	tests := []struct {
		name     string
		excerpts []memops.ThreadExcerpt
		symbols  []string
		want     []int
	}{
		{
			name:     "single token whole-word match",
			excerpts: []memops.ThreadExcerpt{ex(3, "the quasar spectrum"), ex(7, "unrelated content")},
			symbols:  []string{"quasar"},
			want:     []int{3},
		},
		{
			name:     "case-insensitive",
			excerpts: []memops.ThreadExcerpt{ex(5, "Redshift And Luminosity")},
			symbols:  []string{"redshift"},
			want:     []int{5},
		},
		{
			name:     "multi-word anchor splits on hyphen, matches prose surface form",
			excerpts: []memops.ThreadExcerpt{ex(2, "quasar redshift spectroscopy")},
			symbols:  []string{"quasar-redshift"},
			want:     []int{2},
		},
		{
			name:     "whole-word only — no substring false positive",
			excerpts: []memops.ThreadExcerpt{ex(4, "the brainstem region")},
			symbols:  []string{"ai"}, // must not match inside "brainstem"
			want:     nil,
		},
		{
			name:     "multiple turns matched, sorted ascending",
			excerpts: []memops.ThreadExcerpt{ex(9, "enzyme kinetics"), ex(1, "enzyme catalysis")},
			symbols:  []string{"enzyme"},
			want:     []int{1, 9},
		},
		{
			name:     "any-token match across distinct symbols",
			excerpts: []memops.ThreadExcerpt{ex(1, "glacier moraine"), ex(2, "neutrino flavor")},
			symbols:  []string{"glacier", "neutrino"},
			want:     []int{1, 2},
		},
		{name: "no symbols", excerpts: []memops.ThreadExcerpt{ex(1, "anything")}, symbols: nil, want: nil},
		{name: "no excerpts", excerpts: nil, symbols: []string{"x"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exact.MatchExcerptsBySymbols(tt.excerpts, tt.symbols)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
