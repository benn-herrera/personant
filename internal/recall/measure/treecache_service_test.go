package measure_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
)

// This file is the #111 Inc C Service-level test surface for the persisted
// .tree sidecar: it drives a real Service over a fileadapter home, so the
// .tree is written under .recall-cache/threads/ (gitignored), reloaded on a
// fresh session WITHOUT a rebuild, and rebuilt-identically after the cache is
// deleted (W5). The RebuildTrees return count is the build-counter the brief
// asks for: >0 means a thread was (re)clustered via BuildTree this pass, 0
// means every tree was reused (loaded verbatim from .tree, or unchanged).

// bigTurnBody builds a thread body with n distinct per-turn excerpts — enough
// scrolled-out chunks (n >= TreeBuildThreshold) that a summary tree is built.
// Each turn's content is a distinct token stream so the fine tier has n
// genuinely different leaf vectors.
func bigTurnBody(n int) string {
	var b strings.Builder
	tokens := []string{"quasar", "glacier", "enzyme", "neutrino", "trefoil", "monsoon", "ledger", "plasma"}
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "## Turn %d\n%s-%d %s-%d topic-%d\n\n",
			i, tokens[i%len(tokens)], i, tokens[(i+3)%len(tokens)], i, i)
	}
	return b.String()
}

// indexAndRebuild flushes the engaged thread, waits for the indexer to publish
// its fine tier, runs the sleep-cycle RebuildTrees, and returns the rebuild
// count. A non-zero count means the tree was clustered fresh this pass.
func indexAndRebuild(t *testing.T, svc *measure.Service, threadID string, turncount int, probe string) int {
	t.Helper()
	flushAndWait(t, svc, threadID, turncount, probe)
	rebuilt, err := svc.RebuildTrees(context.Background())
	if err != nil {
		t.Fatalf("RebuildTrees: %v", err)
	}
	return rebuilt
}

// TestTreeCache_Service_LoadNotRebuild is the O(changed)-ish gate: session 1
// builds a thread's tree (RebuildTrees > 0) and persists the .tree; session 2
// over the same home LOADS the .tree at startup and a RebuildTrees pass on the
// unchanged thread rebuilds NOTHING (count 0) — the tree came off disk, not
// from a fresh BuildTree.
func TestTreeCache_Service_LoadNotRebuild(t *testing.T) {
	paths, ops := newRecallHome(t)
	const turns = 48 // > TreeBuildThreshold (32)
	body := bigTurnBody(turns)
	probe := "quasar-8 neutrino-11 topic-8"
	seedThread(t, paths, "thr_big", []string{"thr_big"}, body)

	// Session 1: index, build the tree, persist, close.
	svc1 := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc1.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 1: %v", err)
	}
	if built := indexAndRebuild(t, svc1, "thr_big", turns, probe); built != 1 {
		t.Fatalf("session 1 RebuildTrees = %d, want 1 (the above-threshold thread built fresh)", built)
	}
	if err := svc1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// The .tree sidecar exists on disk, beside the .vec, under .recall-cache.
	treePath := filepath.Join(paths.RecallCache, "threads", "thr_big.tree")
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("expected thr_big.tree on disk after session 1: %v", err)
	}

	// Session 2: Prepare loads the persisted .tree. A RebuildTrees pass on the
	// unchanged leaf set rebuilds nothing — the tree was loaded, not rebuilt.
	svc2 := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc2.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 2: %v", err)
	}
	t.Cleanup(func() { _ = svc2.Close() })
	if again, err := svc2.RebuildTrees(context.Background()); err != nil {
		t.Fatalf("session 2 RebuildTrees: %v", err)
	} else if again != 0 {
		t.Errorf("session 2 rebuilt %d trees, want 0 (the unchanged tree loaded from .tree)", again)
	}

	// And the descent is live from session start: the engaged intra-pass still
	// recalls the probed early content (the loaded tree drives it; divergence
	// from the flat scan is 0 by W1).
	if d, _ := svc2.IntraThreadDivergence(context.Background(), probe, "thr_big"); d != 0 {
		t.Errorf("session 2 descent diverged from flat (loaded tree): %d, want 0", d)
	}
}

// TestTreeCache_Service_DeleteRebuilds is W5/WA4: deleting the entire
// .recall-cache (incl every .tree) costs only CPU — the next sleep cycle
// rebuilds an equivalent tree from the canonical leaves, and the intra-pass
// recall (the leaves descent returns) is unchanged before and after.
func TestTreeCache_Service_DeleteRebuilds(t *testing.T) {
	paths, ops := newRecallHome(t)
	const turns = 48
	body := bigTurnBody(turns)
	probe := "trefoil-12 glacier-1 topic-12"
	seedThread(t, paths, "thr_big", []string{"thr_big"}, body)

	// Session 1: build, persist, capture the descent result, close.
	svc1 := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc1.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 1: %v", err)
	}
	if built := indexAndRebuild(t, svc1, "thr_big", turns, probe); built != 1 {
		t.Fatalf("session 1 RebuildTrees = %d, want 1", built)
	}
	r1, ok := findResult(recallEngaged(t, svc1, probe, "thr_big"), "thr_big")
	if !ok || r1.IntraThread == nil {
		t.Fatalf("session 1 intra-thread hit missing")
	}
	div1, _ := svc1.IntraThreadDivergence(context.Background(), probe, "thr_big")
	if err := svc1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// Delete the whole cache — .vec and .tree both gone.
	if err := os.RemoveAll(paths.RecallCache); err != nil {
		t.Fatalf("rm cache: %v", err)
	}

	// Session 2: Prepare rebuilds the leaf tier (full miss); the .tree is gone,
	// so a sleep-cycle RebuildTrees rebuilds it from the canonical leaves.
	svc2 := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc2.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare 2: %v", err)
	}
	t.Cleanup(func() { _ = svc2.Close() })
	if built := indexAndRebuild(t, svc2, "thr_big", turns, probe); built != 1 {
		t.Fatalf("session 2 RebuildTrees = %d, want 1 (rebuilt from canonical after cache delete)", built)
	}

	// Identical recall after the rebuild: the rebuilt tree descends to the same
	// best turn, and the W1 divergence-from-flat is 0 just as before.
	r2, ok := findResult(recallEngaged(t, svc2, probe, "thr_big"), "thr_big")
	if !ok || r2.IntraThread == nil {
		t.Fatalf("session 2 intra-thread hit missing after rebuild")
	}
	if r1.IntraThread.Turns[0] != r2.IntraThread.Turns[0] {
		t.Errorf("best turn differs after rebuild: was %v, now %v", r1.IntraThread.Turns[0], r2.IntraThread.Turns[0])
	}
	div2, _ := svc2.IntraThreadDivergence(context.Background(), probe, "thr_big")
	if div1 != 0 || div2 != 0 {
		t.Errorf("descent diverged from flat: session1=%d session2=%d, want 0/0 (W1)", div1, div2)
	}
}

// recallEngaged runs one engaged-thread Recall and returns the merged results.
func recallEngaged(t *testing.T, svc *measure.Service, probe, engaged string) []measure.Result {
	t.Helper()
	results, err := svc.Recall(context.Background(), measure.Request{
		QueryText: probe,
		Engaged:   engaged,
		Exclude:   map[string]struct{}{engaged: {}},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	return results
}

// TestTreeCache_GitignoreLocation asserts the .tree lives under .recall-cache
// (already gitignored) — no new tracked path is introduced (W5/G4). The path
// is composed from store.PersonantPaths.RecallCache, the gitignored dir.
func TestTreeCache_GitignoreLocation(t *testing.T) {
	paths := store.PathsForHome(t.TempDir())
	treeDir := filepath.Join(paths.RecallCache, "threads")
	if !strings.Contains(treeDir, ".recall-cache") {
		t.Fatalf(".tree dir %q is not under .recall-cache (would not be gitignored)", treeDir)
	}
}
