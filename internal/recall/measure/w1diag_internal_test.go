package measure

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops/fileadapter"
	"personant/internal/recall/scoring"
	"personant/internal/store"
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

// readRecallLog returns the concatenated contents of every per-day log file
// under the home's logs/ directory — the substrate the recall.W1-diag line is
// written to. A live run greps these for `recall.W1-diag`; this test reads
// them the same way.
func readRecallLog(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

// forcedMissTree builds a root with 11 high-cosine decoy subtrees (centroids
// aligned to the query axis) plus 1 target subtree whose centroid is
// heterogeneous (cosine-misses the query) but which contains the single
// best-cosine leaf. With the production beam (k=BeamWidth=8) the 11 decoys
// outrank the target at the root level, so the beam prunes the target subtree
// and MISSES its best leaf — a real divergence from the flat scan. Used to
// drive a deterministic strict-miss into the W1-diag path. (buildBalancedTree
// keys on plain centroids, no exemplar-set propagation, so the prune is
// reproducible; the production d8e32b3 exemplar fix is what prevents this in a
// real semantically-clustered tree — here we bypass it on purpose to exercise
// the forensic emission.)
func forcedMissTree(query []float64, dim int) ([]scoring.ChunkVector, *scoring.SummaryNode) {
	var allLeaves []scoring.ChunkVector
	var roots []*scoring.SummaryNode
	turn := 0

	// 11 decoy subtrees: leaves aligned to the query axis (high cosine), each
	// strictly below the target's best leaf so the target leaf is the unique
	// flat-scan #1.
	for d := 0; d < 11; d++ {
		var leaves []scoring.ChunkVector
		for j := 0; j < 3; j++ {
			v := make([]float64, dim)
			v[0] = 0.80 + 0.001*float64(d) // < target best (1.0), high query cosine
			v[1] = 0.10
			leaves = append(leaves, scoring.ChunkVector{TurnNumber: turn, Vector: v})
			turn++
		}
		node := &scoring.SummaryNode{Children: leafNodesFor(leaves)}
		node.Vector = centroidVec(node.Children)
		roots = append(roots, node)
		allLeaves = append(allLeaves, leaves...)
	}

	// 1 target subtree: the best leaf is exactly the query (cosine 1.0), but
	// the subtree is heterogeneous (3 off-axis leaves), so its centroid
	// cosine-misses the query and the subtree sorts below the 11 decoys.
	bestTurn := turn
	targetLeaves := []scoring.ChunkVector{
		{TurnNumber: turn, Vector: append([]float64{1, 0}, make([]float64, dim-2)...)},
	}
	turn++
	for axis := 2; axis < dim && len(targetLeaves) < 4; axis++ {
		v := make([]float64, dim)
		v[axis] = 1.0
		targetLeaves = append(targetLeaves, scoring.ChunkVector{TurnNumber: turn, Vector: v})
		turn++
	}
	target := &scoring.SummaryNode{Children: leafNodesFor(targetLeaves)}
	target.Vector = centroidVec(target.Children)
	roots = append(roots, target)
	allLeaves = append(allLeaves, targetLeaves...)

	root := &scoring.SummaryNode{Children: roots}
	root.Vector = centroidVec(roots)
	_ = bestTurn
	return allLeaves, root
}

// TestW1Diag_TallyAndForensicLineCoEmit is the #119 regression guard for the
// missing per-probe forensic emission. On a 1-month live rung the run tally
// reported strict_miss=1 but NO recall.W1-diag line reached logs/, so the
// missed-leaf cosines were unreadable. Root cause: the tally was bumped before
// (and independent of) the log emission, which sat on a separate, droppable
// path. This test drives a KNOWN strict-miss divergence through
// intraThreadDivergence against a REAL substrate and asserts BOTH halves emit
// together: the per-instance tally bumps AND a greppable recall.W1-diag line
// with the class/flat/missed cosines lands in logs/. A regression that lets
// the count bump without the line (or vice versa) fails here.
func TestW1Diag_TallyAndForensicLineCoEmit(t *testing.T) {
	const dim = 6
	query := make([]float64, dim)
	query[0] = 1.0

	leaves, tree := forcedMissTree(query, dim)
	if len(leaves) < scoring.TreeBuildThreshold {
		t.Fatalf("test setup: %d leaves < TreeBuildThreshold %d (tree would be unused)",
			len(leaves), scoring.TreeBuildThreshold)
	}
	snap := snapWithFine("thr_eng", leaves, tree)
	if !snap.treeUsable("thr_eng") {
		t.Fatal("treeUsable=false for the forced-miss tree; the divergence hook would no-op (W8)")
	}

	// Confirm the construction genuinely diverges at the production beam — the
	// gate is not vacuous. If this is 0 the topology no longer forces a miss
	// and the rest of the test would be a false pass.
	descent := scoring.DescendChunks(query, tree, scoring.DescendOptions{Limit: Kf})
	flat := scoring.ProposeChunks(query, leaves, scoring.ChunkOptions{Limit: Kf})
	if turnSetDifference(descent, flat) == 0 {
		t.Fatal("forced-miss tree did not diverge at the production beam; adjust the decoy count")
	}

	// Drive the production divergence hook against a REAL fileadapter so the
	// log path is exercised end-to-end (not a nil-ops short-circuit).
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	svc := &Service{ops: fileadapter.NewFileAdapter(paths)}

	div, class := svc.intraThreadDivergence(context.Background(), query, snap, "thr_eng")
	if div == 0 {
		t.Fatal("intraThreadDivergence returned 0 on a forced-miss tree")
	}
	if class == "" {
		t.Fatal("intraThreadDivergence returned an empty class on a divergent probe — the tally would not bump")
	}

	// Half 1: the per-instance tally bumped exactly once, matching the class.
	tally := svc.w1DiagStrictMiss.Load() + svc.w1DiagTie.Load() + svc.w1DiagTreeMismatch.Load()
	if tally != 1 {
		t.Errorf("W1 class tally = %d, want 1 (one classified divergence)", tally)
	}

	// Half 2: the per-probe forensic line reached the substrate log and is
	// greppable, carrying the class + cosine detail (so a future run can tell a
	// strict-miss from a tie). This is the half that regressed.
	logs := readRecallLog(t, paths)
	if !strings.Contains(logs, "recall.W1-diag") {
		t.Fatalf("no recall.W1-diag line in logs/ despite tally=%d — the forensic emission regressed.\nlogs:\n%s", tally, logs)
	}
	if !strings.Contains(logs, "class="+string(class)) || !strings.Contains(logs, "missed=") {
		t.Errorf("recall.W1-diag line missing class/missed detail; got:\n%s", logs)
	}
}
