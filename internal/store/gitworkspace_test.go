package store

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit runs `git init -q` in dir, fatal'ing the test on failure.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v: %s", dir, err, out)
	}
}

// gitRun runs an arbitrary git subcommand in dir and fails the test on
// non-zero exit. Useful for adding remotes during fixture setup.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, stderr.String())
	}
}

func TestFindGitRoot_Found(t *testing.T) {
	hasGit(t)
	repo := t.TempDir()
	gitInit(t, repo)

	got, ok, err := FindGitRoot(repo)
	if err != nil {
		t.Fatalf("FindGitRoot: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	gotEval, _ := filepath.EvalSymlinks(got)
	repoEval, _ := filepath.EvalSymlinks(repo)
	if gotEval != repoEval {
		t.Errorf("got %q, want %q", gotEval, repoEval)
	}
}

func TestFindGitRoot_FromSubdir(t *testing.T) {
	hasGit(t)
	repo := t.TempDir()
	gitInit(t, repo)

	sub := filepath.Join(repo, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	got, ok, err := FindGitRoot(sub)
	if err != nil {
		t.Fatalf("FindGitRoot: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true from subdir")
	}
	gotEval, _ := filepath.EvalSymlinks(got)
	repoEval, _ := filepath.EvalSymlinks(repo)
	if gotEval != repoEval {
		t.Errorf("got %q, want %q", gotEval, repoEval)
	}
}

func TestFindGitRoot_NotFound(t *testing.T) {
	// Use t.TempDir directly without git init. The walk will go up to /
	// (or some ancestor), not finding our test sentinel — but it might
	// find a parent .git directory if the test runner itself sits inside
	// a git tree. To get a deterministic miss, make a directory whose
	// parents we can verify do not have .git up to a known stop. The
	// simplest way: ensure we're inside t.TempDir() and walk only that
	// — but FindGitRoot has no stop parameter.
	//
	// Instead, accept that on developer machines this may walk past
	// $TMPDIR into the project's checkout. Skip the assertion when that
	// happens; the behavior under test is "ok==false somewhere up the
	// tree", and there is no reliable way to assert miss when ancestors
	// of $TMPDIR may carry .git.
	dir := t.TempDir()
	got, ok, err := FindGitRoot(dir)
	if err != nil {
		t.Fatalf("FindGitRoot: %v", err)
	}
	if ok {
		// Verify the hit is *not* dir itself (we did not init it).
		dirEval, _ := filepath.EvalSymlinks(dir)
		gotEval, _ := filepath.EvalSymlinks(got)
		if gotEval == dirEval {
			t.Fatalf("found .git in %s but did not init it there", dir)
		}
		t.Logf("FindGitRoot walked above $TMPDIR to %s (expected on machines where $TMPDIR's ancestors carry .git); skipping miss assertion", got)
	}
}

func TestFindGitRoot_EmptyStart(t *testing.T) {
	got, ok, err := FindGitRoot("")
	if err != nil {
		t.Fatalf("FindGitRoot empty: %v", err)
	}
	if ok || got != "" {
		t.Errorf("expected ok=false got=\"\", got ok=%v got=%q", ok, got)
	}
}

func TestGetGitOriginURL_Origin(t *testing.T) {
	hasGit(t)
	repo := t.TempDir()
	gitInit(t, repo)
	gitRun(t, repo, "remote", "add", "origin", "git@github.com:foo/bar.git")

	got, err := GetGitOriginURL(repo)
	if err != nil {
		t.Fatalf("GetGitOriginURL: %v", err)
	}
	if got != "git@github.com:foo/bar.git" {
		t.Errorf("got %q, want %q", got, "git@github.com:foo/bar.git")
	}
}

func TestGetGitOriginURL_FallbackToFirstRemote(t *testing.T) {
	hasGit(t)
	repo := t.TempDir()
	gitInit(t, repo)
	gitRun(t, repo, "remote", "add", "upstream", "https://github.com/foo/bar.git")

	got, err := GetGitOriginURL(repo)
	if err != nil {
		t.Fatalf("GetGitOriginURL: %v", err)
	}
	if got != "https://github.com/foo/bar.git" {
		t.Errorf("got %q, want %q", got, "https://github.com/foo/bar.git")
	}
}

func TestGetGitOriginURL_NoRemote(t *testing.T) {
	hasGit(t)
	repo := t.TempDir()
	gitInit(t, repo)

	got, err := GetGitOriginURL(repo)
	if err != nil {
		t.Fatalf("GetGitOriginURL no-remote: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for no-remote repo, got %q", got)
	}
}

func TestGetGitOriginURL_EmptyRoot(t *testing.T) {
	_, err := GetGitOriginURL("")
	if err == nil {
		t.Fatal("expected error for empty gitRoot, got nil")
	}
}
