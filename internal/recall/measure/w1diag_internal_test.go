package measure

import (
	"testing"

	"personant/internal/recall/scoring"
)

// TestClassifyW1Divergence locks in the #111 §7.1 diagnostic classifier: it
// must label each descent-vs-flat divergence as strict-miss (a strictly
// higher-cosine leaf was pruned → real recall loss), tie (an equal-cosine
// leaf was substituted → not lost recall), or tree-mismatch (the flat scan
// ranked a leaf the tree does not contain → staleness/build edge).
//
// tree-mismatch is checked FIRST and independently of cosine: a missed leaf
// absent from the tree is a structural edge whatever its score. The
// strict-miss/tie boundary is the best-missed-vs-best-substituted cosine
// comparison at w1Epsilon.
func TestClassifyW1Divergence(t *testing.T) {
	// The tree contains turns 1..5 in every case unless a row overrides it.
	tree := map[int]struct{}{1: {}, 2: {}, 3: {}, 4: {}, 5: {}}
	cand := func(turn int, score float64) scoring.ChunkCandidate {
		return scoring.ChunkCandidate{TurnNumber: turn, Score: score}
	}

	tests := []struct {
		name        string
		missed      []scoring.ChunkCandidate
		substituted []scoring.ChunkCandidate
		tree        map[int]struct{}
		want        w1Class
	}{
		{
			name:        "strict-miss: missed cosine strictly beats substituted",
			missed:      []scoring.ChunkCandidate{cand(1, 0.80)},
			substituted: []scoring.ChunkCandidate{cand(2, 0.60)},
			tree:        tree,
			want:        w1StrictMiss,
		},
		{
			name:        "tie: missed and substituted within epsilon",
			missed:      []scoring.ChunkCandidate{cand(1, 0.7000000000)},
			substituted: []scoring.ChunkCandidate{cand(2, 0.7000000005)}, // < w1Epsilon apart
			tree:        tree,
			want:        w1Tie,
		},
		{
			name:        "tie: exactly equal cosines",
			missed:      []scoring.ChunkCandidate{cand(1, 0.73)},
			substituted: []scoring.ChunkCandidate{cand(2, 0.73)},
			tree:        tree,
			want:        w1Tie,
		},
		{
			name:        "tie: substituted strictly higher (descent gained a better leaf, no loss)",
			missed:      []scoring.ChunkCandidate{cand(1, 0.60)},
			substituted: []scoring.ChunkCandidate{cand(2, 0.80)},
			tree:        tree,
			want:        w1Tie,
		},
		{
			name: "strict-miss: descent dropped a positive-cosine leaf with no substitute",
			// A missed leaf present in the tree with a real positive cosine and
			// nothing substituted for it is a genuine loss: descent failed to
			// surface a leaf the flat scan did, and put nothing comparable in
			// its place (bestSub == 0). That is a strict-miss, not a tie.
			missed:      []scoring.ChunkCandidate{cand(3, 0.50)},
			substituted: nil,
			tree:        tree,
			want:        w1StrictMiss,
		},
		{
			name:        "tree-mismatch: a missed leaf is absent from the tree",
			missed:      []scoring.ChunkCandidate{cand(99, 0.90)}, // 99 not in tree
			substituted: []scoring.ChunkCandidate{cand(2, 0.10)},
			tree:        tree,
			want:        w1TreeMismatch,
		},
		{
			name:        "tree-mismatch wins over a strict cosine gap",
			missed:      []scoring.ChunkCandidate{cand(1, 0.80), cand(42, 0.95)}, // 42 absent
			substituted: []scoring.ChunkCandidate{cand(2, 0.10)},
			tree:        tree,
			want:        w1TreeMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyW1Divergence(tt.missed, tt.substituted, tt.tree, w1Epsilon)
			if got != tt.want {
				t.Errorf("classifyW1Divergence = %q, want %q", got, tt.want)
			}
		})
	}
}
