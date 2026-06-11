package turn

import "testing"

// TestEmbeddingDebtAccessor pins the §6.5 debt-window read-only accessor
// (#13b): the oracle observes ACTUAL flush-lag state through EmbeddingDebt
// rather than re-deriving the boundary from the unexported embeddingDebtCap.
// nil-map / unknown-thread → 0; a tracked thread reports its live count.
func TestEmbeddingDebtAccessor(t *testing.T) {
	tests := []struct {
		name  string
		debt  map[string]int // nil = the well-formed empty case
		query string
		want  int
	}{
		{
			name:  "nil map yields zero",
			debt:  nil,
			query: "thr_1",
			want:  0,
		},
		{
			name:  "unknown thread yields zero",
			debt:  map[string]int{"thr_1": 7},
			query: "thr_missing",
			want:  0,
		},
		{
			name:  "tracked thread reports live debt",
			debt:  map[string]int{"thr_1": 7},
			query: "thr_1",
			want:  7,
		},
		{
			name:  "just-flushed thread reports zero",
			debt:  map[string]int{"thr_1": 0},
			query: "thr_1",
			want:  0,
		},
		{
			name:  "debt just under the cap",
			debt:  map[string]int{"thr_1": embeddingDebtCap - 1},
			query: "thr_1",
			want:  embeddingDebtCap - 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &State{embeddingDebt: tt.debt}
			if got := s.EmbeddingDebt(tt.query); got != tt.want {
				t.Errorf("EmbeddingDebt(%q) = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

// TestEmbeddingDebtAccessorReadOnly confirms the accessor does not
// instantiate or mutate the debt map (a read of an unknown thread on a
// nil map must not allocate the map as a side effect).
func TestEmbeddingDebtAccessorReadOnly(t *testing.T) {
	s := &State{}
	if got := s.EmbeddingDebt("thr_1"); got != 0 {
		t.Fatalf("EmbeddingDebt on empty state = %d, want 0", got)
	}
	if s.embeddingDebt != nil {
		t.Errorf("EmbeddingDebt mutated state: embeddingDebt is non-nil after a read")
	}
}
