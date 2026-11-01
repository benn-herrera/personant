package autogit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitindex "github.com/go-git/go-git/v5/plumbing/format/index"

	"personant/internal/store"
)

// TestAddPaths_MatchesPerPathAdd stages one write set two ways over the
// same worktree — go-git's per-path AddWithOptions{SkipStatus}, each call
// a full index round trip, and AddPaths — into two git-dirs that hold the
// same baseline, and requires identical index entries and commit trees.
// Sharing the worktree keeps the stat fields go-git records (inode, dev,
// times) the same for both.
func TestAddPaths_MatchesPerPathAdd(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	perPath := store.PathsForHome(home)
	perPath.GitDaily = filepath.Join(home, ".git-perpath")
	batched := store.PathsForHome(home)
	batched.GitDaily = filepath.Join(home, ".git-addpaths")

	write := func(rel, body string) {
		t.Helper()
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	baseline := []string{"modified.md", "unchanged.md", "deleted.md"}
	for i := range 300 {
		baseline = append(baseline, fmt.Sprintf("wide/%04d.md", i))
	}
	for _, rel := range baseline {
		write(rel, "baseline "+rel+"\n")
	}
	for _, p := range []store.PersonantPaths{perPath, batched} {
		if err := InitDaily(ctx, p); err != nil {
			t.Fatalf("InitDaily %s: %v", p.GitDaily, err)
		}
		addPerPath(t, p, baseline)
		if err := Commit(ctx, p, Daily, "baseline", 0, 0); err != nil {
			t.Fatalf("baseline commit %s: %v", p.GitDaily, err)
		}
	}

	write("modified.md", "modified body\n")
	write("new.md", "new file\n")
	write("sub/dir/new.md", "file in a new subdirectory\n")
	write("wide/0150.md", "modified in a wide directory\n")
	write("wide/0300.md", "new in a wide directory\n")
	if err := os.Remove(filepath.Join(home, "deleted.md")); err != nil {
		t.Fatalf("remove deleted.md: %v", err)
	}
	rels := []string{
		"modified.md", "unchanged.md", "deleted.md", "new.md",
		"sub/dir/new.md", "wide/0150.md", "wide/0300.md",
	}
	// Each add reads the file and records its atime on darwin; one read
	// first leaves atime settled for both stagings under relatime.
	for _, rel := range rels {
		if _, err := os.ReadFile(filepath.Join(home, rel)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("prime %s: %v", rel, err)
		}
	}

	addPerPath(t, perPath, rels)
	if err := AddPaths(ctx, batched, Daily, rels); err != nil {
		t.Fatalf("AddPaths: %v", err)
	}

	want := loadIndexEntries(t, perPath)
	got := loadIndexEntries(t, batched)
	if len(got) != len(want) {
		t.Fatalf("AddPaths index has %d entries, per-path add has %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("entry %d differs:\n AddPaths: %+v\n per-path: %+v", i, got[i], want[i])
		}
	}
	names := map[string]plumbing.Hash{}
	for _, e := range got {
		names[e.Name] = e.Hash
	}
	if _, ok := names["deleted.md"]; ok {
		t.Error("deleted.md is still in the AddPaths index")
	}
	if _, ok := names["sub/dir/new.md"]; !ok {
		t.Error("sub/dir/new.md is missing from the AddPaths index")
	}

	if commitTree(t, batched) != commitTree(t, perPath) {
		t.Error("commit trees differ between AddPaths and per-path add")
	}
}

// addPerPath stages rels one AddWithOptions call at a time.
func addPerPath(t *testing.T, paths store.PersonantPaths, rels []string) {
	t.Helper()
	repo, err := openRepo(paths, Daily)
	if err != nil {
		t.Fatalf("open %s: %v", paths.GitDaily, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree %s: %v", paths.GitDaily, err)
	}
	for _, rel := range rels {
		if err := wt.AddWithOptions(&git.AddOptions{Path: rel, SkipStatus: true}); err != nil {
			t.Fatalf("add %q to %s: %v", rel, paths.GitDaily, err)
		}
	}
}

func loadIndexEntries(t *testing.T, paths store.PersonantPaths) []gitindex.Entry {
	t.Helper()
	repo, err := openRepo(paths, Daily)
	if err != nil {
		t.Fatalf("open %s: %v", paths.GitDaily, err)
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatalf("index %s: %v", paths.GitDaily, err)
	}
	out := make([]gitindex.Entry, len(idx.Entries))
	for i, e := range idx.Entries {
		out[i] = *e
	}
	return out
}

// commitTree commits the staged index and returns the commit's tree hash.
func commitTree(t *testing.T, paths store.PersonantPaths) plumbing.Hash {
	t.Helper()
	if err := Commit(context.Background(), paths, Daily, "turn", 0, 0); err != nil {
		t.Fatalf("commit %s: %v", paths.GitDaily, err)
	}
	repo, err := openRepo(paths, Daily)
	if err != nil {
		t.Fatalf("open %s: %v", paths.GitDaily, err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("HEAD %s: %v", paths.GitDaily, err)
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("commit object %s: %v", paths.GitDaily, err)
	}
	return c.TreeHash
}
