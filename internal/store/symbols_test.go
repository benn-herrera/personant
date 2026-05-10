package store

import "testing"

func TestNormalizeIdentifier(t *testing.T) {
	cases := []string{
		"internal/store/symbols.go",
		"FooBarBaz",
		"thr_42",
		"  spaced  out  ",
		"abc123XYZ",
		"a/b/c.go:42",
		"",
	}
	for _, in := range cases {
		got := Normalize(in, SymbolIdentifier)
		if got != in {
			t.Errorf("identifier round-trip: Normalize(%q) = %q, want %q", in, got, in)
		}
	}
}

func TestNormalizeEntity(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"basic two-word", "Cosserat sector", "cosserat-sector"},
		{"single word", "Trefoil", "trefoil"},
		{"stop-word leading", "the trefoil", "trefoil"},
		{"stop-word phrase", "of the body", "body"},
		{"stop-word multi", "as is the body", "body"},
		{"period kept", "Mr. Smith!", "mr.-smith"},
		{"underscore kept", "some_var", "some_var"},
		{"hyphen kept in token", "body-topology", "body-topology"},
		{"all stop words", "the of in on", ""},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"unicode letter preserved", "ångström", "ångström"},
		{"unicode mixed case", "Ångström", "ångström"},
		{"punct around stops", "of-the-body", "of-the-body"}, // hyphenated, not whitespace-split: stops not dropped
		{"colon stripped", "foo:bar baz", "foobar-baz"},
		{"parens stripped", "(3,2)-torus knot", "32-torus-knot"},
		{"digits kept", "h2o molecule", "h2o-molecule"},
		{"trailing whitespace", "trefoil   ", "trefoil"},
		{"single-token stop word preserved", "the", "the"},
		{"single-token stop word preserved 2", "is", "is"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in, SymbolEntity)
			if got != tc.want {
				t.Errorf("Normalize(%q, entity) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"leading hash stripped", "#claim-001", "claim-001"},
		{"no hash", "claim-001", "claim-001"},
		{"whitespace to hyphen", "#multi word", "multi-word"},
		{"multiple spaces collapse", "#multi   word", "multi-word"},
		{"uppercase folded", "#FOO", "foo"},
		{"period stripped", "#foo.bar", "foobar"},
		{"underscore stripped", "#foo_bar", "foobar"},
		{"hyphen kept", "#foo-bar", "foo-bar"},
		{"trailing whitespace", "#foo ", "foo"},
		{"empty", "", ""},
		{"only hash", "#", ""},
		{"unicode lowercased", "#Über", "über"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in, SymbolTag)
			if got != tc.want {
				t.Errorf("Normalize(%q, tag) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeUnknownCategoryPassthrough(t *testing.T) {
	// A category outside the spec §2.7.1 set must not panic; passthrough is
	// the least-destructive behavior on a programming error.
	got := Normalize("Some Value", SymbolCategory("phrasal"))
	if got != "Some Value" {
		t.Errorf("unknown category passthrough: got %q, want %q", got, "Some Value")
	}
}
