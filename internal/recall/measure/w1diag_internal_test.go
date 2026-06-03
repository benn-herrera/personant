package measure

import (
	"context"
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

// TestEmitW1Diag_ReturnsClassItBumps is the #111 single-classification guard:
// emitW1Diag must RETURN the same class it bumps on the Service atomic. The
// harness consumes the returned class to accumulate the run-total tally (the
// instance-churn-proof reader), so a returned class that disagreed with the
// bumped verdict would re-introduce the very divergence the fix removes — two
// numbers describing the same probe that can drift apart. Classification
// happens exactly once (classifyW1Divergence), and both the atomic and the
// return read that one verdict; this test pins that invariant.
//
// ops is nil (no substrate to log to), so emitW1Diag takes the early-return
// path that still bumps the atomic and returns the class — the contract the
// harness depends on.
func TestEmitW1Diag_ReturnsClassItBumps(t *testing.T) {
	leaf := func(turn int) *scoring.SummaryNode {
		return &scoring.SummaryNode{Leaf: scoring.ChunkVector{TurnNumber: turn}}
	}
	cand := func(turn int, score float64) scoring.ChunkCandidate {
		return scoring.ChunkCandidate{TurnNumber: turn, Score: score}
	}

	// Tree leaves {1,2}: flat ranks leaf 1 (cosine 0.80) which descent missed,
	// descent substituted leaf 2 (cosine 0.60). Strictly higher missed cosine,
	// both present in the tree → strict-miss.
	tree := &scoring.SummaryNode{Children: []*scoring.SummaryNode{leaf(1), leaf(2)}}
	flat := []scoring.ChunkCandidate{cand(1, 0.80), cand(2, 0.60)}
	descent := []scoring.ChunkCandidate{cand(2, 0.60)}

	s := &Service{} // ops nil → no log; atomic + return still exercised
	got := s.emitW1Diag(context.Background(), tree, descent, flat)
	if got != w1StrictMiss {
		t.Fatalf("emitW1Diag returned class %q, want %q", got, w1StrictMiss)
	}
	// The returned class must match the bucket the atomic was bumped on — one
	// classification, surfaced and bumped consistently.
	if n := s.w1DiagStrictMiss.Load(); n != 1 {
		t.Errorf("w1DiagStrictMiss = %d, want 1 (the bumped bucket must match the returned class)", n)
	}
	if n := s.w1DiagTie.Load() + s.w1DiagTreeMismatch.Load(); n != 0 {
		t.Errorf("non-strict-miss buckets bumped: tie+tree_mismatch = %d, want 0", n)
	}
}
