package turn

import (
	"sort"
	"testing"

	"personant/internal/memops"
)

func TestCoalesceUnion(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("alpha", "alpha", memops.SourceUser)
	b.addSymbol("alpha", "alpha", memops.SourceUser) // dup, same source
	b.addSymbol("beta", "beta", memops.SourceModel)
	b.addThread("thr_42")
	b.addThread("thr_42")
	b.addThread("thr_99")

	got := b.symbolList()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("symbols: got %v want [alpha beta]", got)
	}

	gotT := b.threadList()
	sort.Strings(gotT)
	if len(gotT) != 2 || gotT[0] != "thr_42" || gotT[1] != "thr_99" {
		t.Errorf("threads: got %v want [thr_42 thr_99]", gotT)
	}
}

func TestCoalesceReset(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("x", "x", memops.SourceModel)
	b.addThread("thr_1")
	b.reset()
	if len(b.symbols) != 0 || len(b.threads) != 0 {
		t.Errorf("reset did not clear: %+v", b)
	}
}

func TestCoalesceIgnoresEmpty(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("", "", memops.SourceModel)
	b.addThread("")
	if len(b.symbols) != 0 || len(b.threads) != 0 {
		t.Errorf("empty entries should not be added: %+v", b)
	}
}

// TestCoalesceDominantSource exercises the §2.7.3 precedence:
// curator > user > model > deterministic. Within one turn, when a
// normalized symbol is contributed by multiple sources, the dominant
// source wins regardless of insertion order.
func TestCoalesceDominantSource(t *testing.T) {
	cases := []struct {
		name   string
		first  memops.SymbolSource
		second memops.SymbolSource
		want   memops.SymbolSource
	}{
		{"model then user → user", memops.SourceModel, memops.SourceUser, memops.SourceUser},
		{"user then model → user", memops.SourceUser, memops.SourceModel, memops.SourceUser},
		{"user then curator → curator", memops.SourceUser, memops.SourceCurator, memops.SourceCurator},
		{"curator then deterministic → curator", memops.SourceCurator, memops.SourceDeterministic, memops.SourceCurator},
		{"deterministic then model → model", memops.SourceDeterministic, memops.SourceModel, memops.SourceModel},
		{"model then model → model", memops.SourceModel, memops.SourceModel, memops.SourceModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newCoalesceBuffer()
			b.addSymbol("X", "x", tc.first)
			b.addSymbol("X", "x", tc.second)
			got := b.symbols["x"].Source
			if got != tc.want {
				t.Errorf("dominant of (%q, %q): got %q want %q", tc.first, tc.second, got, tc.want)
			}
		})
	}
}

// TestCoalesceFirstRawWins documents that the surface form is preserved
// from the first sighting; the source-precedence merge does not also
// overwrite raw.
func TestCoalesceFirstRawWins(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("First Form", "first-form", memops.SourceModel)
	b.addSymbol("FIRST FORM", "first-form", memops.SourceUser) // higher source, different surface
	got := b.symbols["first-form"]
	if got.Raw != "First Form" {
		t.Errorf("raw: got %q want %q", got.Raw, "First Form")
	}
	if got.Source != memops.SourceUser {
		t.Errorf("source should still upgrade: got %q want %q", got.Source, memops.SourceUser)
	}
}
