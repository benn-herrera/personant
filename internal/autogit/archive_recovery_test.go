package autogit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/store"
)

// pinClock freezes clock.Timeline at a fixed instant for the duration of
// the test, so any commit signature (store.CommitSignature stamps When
// from clock.Timeline) is deterministic and no wall-clock leaks in (AC6).
func pinClock(t *testing.T) time.Time {
	t.Helper()
	when := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return when })
	t.Cleanup(restore)
	return when
}

// initRepo scaffolds a fresh substrate (PlainInit + bootstrap commit) and
// returns its paths and an open repo handle.
func initRepo(t *testing.T) (store.PersonantPaths, *git.Repository) {
	t.Helper()
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	return paths, repo
}

func commitAll(t *testing.T, paths store.PersonantPaths, repo *git.Repository, msg string) plumbing.Hash {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		t.Fatalf("add all: %v", err)
	}
	h, err := wt.Commit(msg, &git.CommitOptions{Author: store.CommitSignature(repo)})
	if err != nil {
		t.Fatalf("commit %q: %v", msg, err)
	}
	return h
}

// writeThreadDir lays down a representative thread directory:
// thread.md + turns/<n>.md + files.json. Returns the repo-relative path.
func writeThreadDir(t *testing.T, paths store.PersonantPaths, id string) string {
	t.Helper()
	rel := filepath.ToSlash(filepath.Join("threads", id))
	abs := filepath.Join(paths.Home, "threads", id)
	if err := os.MkdirAll(filepath.Join(abs, "turns"), 0o755); err != nil {
		t.Fatalf("mkdir thread dir: %v", err)
	}
	files := map[string]string{
		"thread.md":  "---\nid: " + id + "\n---\n# " + id + "\n",
		"turns/1.md": "first turn excerpt for " + id + "\n",
		"turns/2.md": "second turn excerpt for " + id + "\n",
		"files.json": "{\"files\":{}}\n",
	}
	for name, content := range files {
		p := filepath.Join(abs, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return rel
}

// TestArchiveRecoveryRoundTrip is the I1 keystone: it proves the recovery
// path is sound (E2 + E3 + Q1 + Q3) before I2 builds the adapter on top.
//
// Sequence:
//  1. Create a thread directory, commit it.
//  2. Capture its tree hash via TreeHashAt (E3 capture side) — and confirm
//     it matches WorktreeTreeHash of the live on-disk dir (E3 verify side
//     agrees with E3 capture side).
//  3. os.RemoveAll the dir + Add(".") + commit — staging the recursive
//     removal (Q1: AddWithOptions{All:true} stages deletions). The
//     deletion commit's tree no longer contains the dir.
//  4. Recover by CheckoutTree from the deletion commit's PARENT (Q3: the
//     deletion commit dropped the dir; the bytes live in the parent).
//  5. Verify the restored dir's tree hash matches the captured one via
//     VerifyTreeHash (invariant 2 / integrity-verified recovery).
func TestArchiveRecoveryRoundTrip(t *testing.T) {
	when := pinClock(t)
	ctx := context.Background()
	paths, repo := initRepo(t)

	dirPath := writeThreadDir(t, paths, "thr_1")
	addCommit := commitAll(t, paths, repo, "add thr_1")

	// E3 capture: tree hash of the dir as committed.
	captured, err := TreeHashAt(ctx, paths, Primary, addCommit.String(), dirPath)
	if err != nil {
		t.Fatalf("TreeHashAt: %v", err)
	}
	if captured == "" {
		t.Fatal("captured tree hash is empty")
	}
	// E3 capture/verify agreement: WorktreeTreeHash of the live dir matches
	// the git-stored subtree hash. This is what makes the verify side a
	// trustworthy integrity check.
	live, err := WorktreeTreeHash(paths, dirPath)
	if err != nil {
		t.Fatalf("WorktreeTreeHash (live): %v", err)
	}
	if live != captured {
		t.Fatalf("WorktreeTreeHash %s != TreeHashAt %s — E3 capture/verify disagree", live, captured)
	}

	// Q1: stage recursive removal via os.RemoveAll + Add(All), then commit.
	if err := os.RemoveAll(filepath.Join(paths.Home, filepath.FromSlash(dirPath))); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	delCommit := commitAll(t, paths, repo, "archive: thr_1")

	// AC6: the deletion commit's author time is the pinned clock, not wall.
	dc, err := repo.CommitObject(delCommit)
	if err != nil {
		t.Fatalf("load deletion commit: %v", err)
	}
	if !dc.Author.When.Equal(when) {
		t.Fatalf("deletion commit author time %v != pinned %v — wall-clock leaked", dc.Author.When, when)
	}

	// The deletion commit's tree must NOT contain the dir (Q3 premise).
	delTree, err := dc.Tree()
	if err != nil {
		t.Fatalf("deletion commit tree: %v", err)
	}
	if _, err := delTree.Tree(dirPath); err == nil {
		t.Fatal("deletion commit still contains the thread dir — Q1 staging failed")
	}

	// Q3: recover from the deletion commit's PARENT, where the bytes live.
	if len(dc.ParentHashes) != 1 {
		t.Fatalf("expected exactly 1 parent, got %d", len(dc.ParentHashes))
	}
	parent := dc.ParentHashes[0].String()

	// Sanity: the dir is really gone from disk before recovery.
	if _, err := os.Stat(filepath.Join(paths.Home, filepath.FromSlash(dirPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dir still on disk before recovery: stat err=%v", err)
	}

	// E2: restore the whole subtree from the parent commit.
	if err := CheckoutTree(ctx, paths, Primary, parent, dirPath, 0, 0); err != nil {
		t.Fatalf("CheckoutTree: %v", err)
	}

	// Invariant 2: the restored dir's tree hash matches the captured token.
	if err := VerifyTreeHash(paths, dirPath, captured); err != nil {
		t.Fatalf("VerifyTreeHash after recovery: %v", err)
	}

	// Byte-exact spot check on a representative file.
	got, err := os.ReadFile(filepath.Join(paths.Home, "threads", "thr_1", "turns", "2.md"))
	if err != nil {
		t.Fatalf("read restored turn: %v", err)
	}
	if string(got) != "second turn excerpt for thr_1\n" {
		t.Fatalf("restored turn content mismatch: %q", got)
	}
}

// TestVerifyTreeHashMismatchIsIntegrityError confirms a tampered/restored
// dir that does not match the captured hash yields memops.ErrArchiveIntegrity
// (the distinct integrity signal recovery aborts on).
func TestVerifyTreeHashMismatchIsIntegrityError(t *testing.T) {
	pinClock(t)
	paths, repo := initRepo(t)
	dirPath := writeThreadDir(t, paths, "thr_1")
	commitAll(t, paths, repo, "add thr_1")

	err := VerifyTreeHash(paths, dirPath, "0000000000000000000000000000000000000000")
	if !errors.Is(err, memops.ErrArchiveIntegrity) {
		t.Fatalf("expected ErrArchiveIntegrity, got %v", err)
	}
}

// TestCheckoutTreeMissingDirErrors confirms restoring a subtree that does
// not exist in the named commit is a surfaced error, not a silent no-op.
func TestCheckoutTreeMissingDirErrors(t *testing.T) {
	pinClock(t)
	ctx := context.Background()
	paths, repo := initRepo(t)
	writeThreadDir(t, paths, "thr_1")
	c := commitAll(t, paths, repo, "add thr_1")

	if err := CheckoutTree(ctx, paths, Primary, c.String(), "threads/thr_404", 0, 0); err == nil {
		t.Fatal("expected error restoring a nonexistent subtree")
	}
}
