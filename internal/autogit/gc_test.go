package autogit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/store"
)

// looseObjectCount counts loose object files under .git/objects — every
// two-hex-char fanout directory's entries. Packfiles live under
// .git/objects/pack and the loose/info dirs are excluded.
func looseObjectCount(t *testing.T, paths store.PersonantPaths) int {
	t.Helper()
	objRoot := filepath.Join(paths.Home, ".git", "objects")
	dirents, err := os.ReadDir(objRoot)
	if err != nil {
		t.Fatalf("read objects dir: %v", err)
	}
	count := 0
	for _, de := range dirents {
		name := de.Name()
		if !de.IsDir() || name == "pack" || name == "info" {
			continue
		}
		if len(name) != 2 { // fanout dirs are exactly two hex chars
			continue
		}
		sub, err := os.ReadDir(filepath.Join(objRoot, name))
		if err != nil {
			t.Fatalf("read fanout %s: %v", name, err)
		}
		count += len(sub)
	}
	return count
}

// packFileCount counts *.pack files under .git/objects/pack.
func packFileCount(t *testing.T, paths store.PersonantPaths) int {
	t.Helper()
	packs, err := filepath.Glob(filepath.Join(paths.Home, ".git", "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatalf("glob packs: %v", err)
	}
	return len(packs)
}

// makeCommits writes n distinct files (under a unique prefix) and commits
// each, producing loose objects (blob + tree + commit per commit) for GC to
// pack. Each call uses a distinct prefix so repeated calls within one test
// always dirty the tree (never a clean-tree empty commit).
func makeCommits(t *testing.T, paths store.PersonantPaths, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		f := filepath.Join(paths.Home, fmt.Sprintf("gcfile_%s_%d.md", prefix, i))
		if err := os.WriteFile(f, []byte(fmt.Sprintf("commit body %s %d\n", prefix, i)), 0o644); err != nil {
			t.Fatalf("write commit file %d: %v", i, err)
		}
		if err := Add(context.Background(), paths, Primary, "."); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
		if err := Commit(context.Background(), paths, Primary, "gc test commit", 0, 0); err != nil {
			t.Fatalf("Commit %d: %v", i, err)
		}
	}
}

// TestGC_PacksLooseObjects: on a repo with many loose objects, GC packs
// them into a packfile and removes the loose copies. The loose-object pile
// must shrink and a packfile must appear.
func TestGC_PacksLooseObjects(t *testing.T) {
	paths := scaffoldHome(t)
	makeCommits(t, paths, "p", 6)

	looseBefore := looseObjectCount(t, paths)
	packsBefore := packFileCount(t, paths)
	if looseBefore == 0 {
		t.Fatalf("precondition: expected loose objects before GC, got 0")
	}

	if err := GC(context.Background(), paths, Primary); err != nil {
		t.Fatalf("GC: %v", err)
	}

	looseAfter := looseObjectCount(t, paths)
	packsAfter := packFileCount(t, paths)
	if looseAfter >= looseBefore {
		t.Errorf("loose objects did not shrink: before=%d after=%d", looseBefore, looseAfter)
	}
	if packsAfter <= packsBefore {
		t.Errorf("no new packfile appeared: before=%d after=%d", packsBefore, packsAfter)
	}
}

// TestGC_RepeatedGCNoStalePackError: a second GC over a repo that already
// has a packfile (from the first GC) must NOT error. RepackObjects deletes
// the old pack and writes a new one; if Prune reused the same in-memory
// storer it would resolve reachable objects through the stale (deleted)
// pack and fail "packfile not found". GC re-opens the repo before pruning,
// so the second pass stays clean — this guards that re-open fix. (The sim's
// first sleep cycle hit this exact path: store.Init leaves an initial pack,
// so the very first gc is a repack-against-existing-pack.)
func TestGC_RepeatedGCNoStalePackError(t *testing.T) {
	paths := scaffoldHome(t)
	makeCommits(t, paths, "first", 6)

	if err := GC(context.Background(), paths, Primary); err != nil {
		t.Fatalf("first GC: %v", err)
	}
	if got := packFileCount(t, paths); got < 1 {
		t.Fatalf("first GC produced no packfile (precondition for stale-pack test)")
	}

	// More commits → fresh loose objects, then a second GC. This repacks
	// against the existing pack — the condition that triggered the stale
	// in-memory pack-layout error before the prune re-open fix.
	makeCommits(t, paths, "second", 4)
	if err := GC(context.Background(), paths, Primary); err != nil {
		t.Fatalf("second GC (repack against existing pack) errored — stale-pack regression: %v", err)
	}
}

// TestGC_FreshRepoIsCleanNoOp: a freshly-initialized repo (one initial
// commit, very few loose objects) GCs without error and remains valid —
// GC never fails the caller on a small/empty pile.
func TestGC_FreshRepoIsCleanNoOp(t *testing.T) {
	paths := scaffoldHome(t)
	if err := GC(context.Background(), paths, Primary); err != nil {
		t.Fatalf("GC on fresh repo: %v", err)
	}
	// HEAD must still resolve — GC must not corrupt the repo.
	_ = repoHead(t, paths)
}

// TestGC_CanceledContext: a canceled context aborts before any git work.
func TestGC_CanceledContext(t *testing.T) {
	paths := scaffoldHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := GC(ctx, paths, Primary); err == nil {
		t.Fatalf("GC with canceled context: want error, got nil")
	}
}
