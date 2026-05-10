package store

import "testing"

// TestDominantSource pins the §2.7.3 precedence:
// curator > user > model > deterministic. Empty source ranks below all,
// so any concrete source upgrades an unset value.
func TestDominantSource(t *testing.T) {
	cases := []struct {
		name string
		a, b SymbolSource
		want SymbolSource
	}{
		{"curator beats user", SourceCurator, SourceUser, SourceCurator},
		{"user beats curator (commutative)", SourceUser, SourceCurator, SourceCurator},
		{"user beats model", SourceUser, SourceModel, SourceUser},
		{"model beats user (commutative)", SourceModel, SourceUser, SourceUser},
		{"model beats deterministic", SourceModel, SourceDeterministic, SourceModel},
		{"deterministic beats model (commutative)", SourceDeterministic, SourceModel, SourceModel},
		{"curator beats deterministic", SourceCurator, SourceDeterministic, SourceCurator},
		{"same source returns itself", SourceModel, SourceModel, SourceModel},
		{"empty loses to any concrete", "", SourceDeterministic, SourceDeterministic},
		{"any concrete beats empty (commutative)", SourceModel, "", SourceModel},
		{"both empty returns empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DominantSource(tc.a, tc.b); got != tc.want {
				t.Errorf("DominantSource(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
