// M1 git-minimization recoverability acceptance tests (design §4): the
// GetFileVersion recovery path + the reachability-gated AgeFileChains. The
// headline invariant under test is "no chain is dropped without an
// application-reachable recovery path" — aging is REFUSED (chain retained,
// refusal logged) when a commit hash is unreachable in the workspace repo,
// and an aged-out reachable file is recoverable end-to-end through the port.
package fileadapter

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

// hasGitT skips the test if git is unavailable.
func hasGitT(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
}

// newWorkspaceRepo creates a git repo at a fresh temp dir, commits
// path:=content, and returns (repoRoot, commitHash). It is the user's
// workspace repo — separate from the adapter's home tree.
func newWorkspaceRepo(t *testing.T, path, content string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, stderr.String())
		}
	}
	run("init", "-q")
	full := filepath.Join(repo, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("-c", "user.email=t@example.com", "-c", "user.name=t", "add", path)
	run("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "add")
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return repo, string(bytes.TrimSpace(out))
}

// seedThreadInProject creates a project rooted at workspaceRoot and a thread
// in it, so the adapter's workspaceGitRoot resolver can walk thread →
// project → CurrentRootPath → git root. Returns the thread ID.
func seedThreadInProject(t *testing.T, a *FileAdapter, ctx context.Context, workspaceRoot string) string {
	t.Helper()
	projID := "prj_ws"
	if err := a.CreateProject(ctx, memops.ProjectMeta{
		ID:              projID,
		Name:            "ws",
		CurrentRootPath: workspaceRoot,
		Created:         rfc3339Now(),
		LastActive:      rfc3339Now(),
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	rec := validSpine("thr_1", projID)
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\n\nx\n"}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	return rec.ID
}

// (readEventLog lives in fileadapter_test.go — shared across the package's
// tests; it concatenates every *.log under the adapter's LogsDir.)

// agePastTurnWindow returns a currentTurn that is past the turn-side
// retention window for a file committed at committedTurn.
func agePastTurnWindow(committedTurn int) int {
	return committedTurn + store.FileChainRetentionTurns + 1
}

// A1: end-to-end recovery through the port for an aged-out reachable file.
func TestM1_RecoverAgedOutReachable(t *testing.T) {
	hasGitT(t)
	ctx := context.Background()
	a := newAdapter(t)

	const path = "src/f.txt"
	const content = "committed-bytes\n"
	repo, hash := newWorkspaceRepo(t, path, content)
	thr := seedThreadInProject(t, a, ctx, repo)

	// Record the write + commit in the §3.9 store, then age it out.
	if err := a.RecordFileWrite(ctx, thr, path, content); err != nil {
		t.Fatalf("RecordFileWrite: %v", err)
	}
	if err := a.RecordFileCommit(ctx, thr, path, hash, 5); err != nil {
		t.Fatalf("RecordFileCommit: %v", err)
	}
	aged, _, err := a.AgeFileChains(ctx, thr, agePastTurnWindow(5))
	if err != nil {
		t.Fatalf("AgeFileChains: %v", err)
	}
	if len(aged) != 1 || aged[0] != path {
		t.Fatalf("aged = %v, want [%s] (reachable commit must age)", aged, path)
	}
	// Chain is gone on disk.
	tf, err := store.LoadThreadFiles(a.paths, thr)
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if e, ok := tf.Entry(path); !ok || e.Chain.Len() != 0 {
		t.Fatalf("chain not aged out: entry=%v", e)
	}

	// Recovery reads the committed bytes from the workspace repo.
	got, err := a.GetFileVersion(ctx, thr, path, hash)
	if err != nil {
		t.Fatalf("GetFileVersion: %v", err)
	}
	if got != content {
		t.Errorf("recovered content = %q, want %q", got, content)
	}
}

// A2: aging REFUSED + chain retained + chain-age-refused logged when the
// commit hash is unreachable.
func TestM1_AgingRefusedWhenUnreachable(t *testing.T) {
	hasGitT(t)
	ctx := context.Background()
	a := newAdapter(t)

	const path = "f.txt"
	repo, _ := newWorkspaceRepo(t, path, "real\n")
	thr := seedThreadInProject(t, a, ctx, repo)

	// Commit pointer references a hash that was NEVER committed in the repo.
	const orphan = "0123456789012345678901234567890123456789"
	if err := a.RecordFileWrite(ctx, thr, path, "chain-literal\n"); err != nil {
		t.Fatalf("RecordFileWrite: %v", err)
	}
	if err := a.RecordFileCommit(ctx, thr, path, orphan, 5); err != nil {
		t.Fatalf("RecordFileCommit: %v", err)
	}

	aged, freed, err := a.AgeFileChains(ctx, thr, agePastTurnWindow(5))
	if err != nil {
		t.Fatalf("AgeFileChains: %v", err)
	}
	if len(aged) != 0 || freed != 0 {
		t.Fatalf("aged=%v freed=%d, want refusal (none aged)", aged, freed)
	}
	// Chain is RETAINED.
	tf, err := store.LoadThreadFiles(a.paths, thr)
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if e, ok := tf.Entry(path); !ok || e.Chain.Len() == 0 {
		t.Fatalf("chain dropped despite unreachable hash (invariant violated): entry=%v", e)
	}
	// Refusal is logged.
	log := readEventLog(t, a)
	if !strings.Contains(log, "chain-age-refused") {
		t.Errorf("no chain-age-refused line in event log:\n%s", log)
	}
	if !strings.Contains(log, "hash="+orphan) {
		t.Errorf("refusal log missing hash detail:\n%s", log)
	}
}

// A3: retained-chain recovery needs no git — point the resolver at a
// non-existent workspace and still succeed via the chain fast path.
func TestM1_RetainedChainRecoveryNoGit(t *testing.T) {
	ctx := context.Background()
	a := newAdapter(t)

	const path = "f.txt"
	const content = "live-literal\n"
	const hash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	// Project root points at a path with no git repo — recovery must not
	// touch it because the chain is still retained.
	bogusRoot := filepath.Join(t.TempDir(), "no-such-workspace")
	thr := seedThreadInProject(t, a, ctx, bogusRoot)

	if err := a.RecordFileWrite(ctx, thr, path, content); err != nil {
		t.Fatalf("RecordFileWrite: %v", err)
	}
	if err := a.RecordFileCommit(ctx, thr, path, hash, 5); err != nil {
		t.Fatalf("RecordFileCommit: %v", err)
	}
	// No aging — chain is present. Recovery returns Chain.Current().
	got, err := a.GetFileVersion(ctx, thr, path, hash)
	if err != nil {
		t.Fatalf("GetFileVersion (retained chain): %v", err)
	}
	if got != content {
		t.Errorf("recovered = %q, want %q (chain fast path)", got, content)
	}
}

// A4: unreachable recovery is a clean sentinel via errors.Is, never ("",nil).
func TestM1_UnreachableIsSentinel(t *testing.T) {
	hasGitT(t)
	ctx := context.Background()
	a := newAdapter(t)

	const path = "f.txt"
	repo, _ := newWorkspaceRepo(t, path, "real\n")
	thr := seedThreadInProject(t, a, ctx, repo)

	// A hash never committed and no retained chain matching it.
	const orphan = "0123456789012345678901234567890123456789"
	got, err := a.GetFileVersion(ctx, thr, path, orphan)
	if !errors.Is(err, memops.ErrFileVersionUnreachable) {
		t.Fatalf("err = %v, want ErrFileVersionUnreachable", err)
	}
	if got != "" {
		t.Errorf("content = %q, want empty on unreachable", got)
	}

	// Absent-path-at-reachable-commit is also the sentinel, not ("",nil):
	// the commit is reachable but the queried path is not in its tree.
	repo2, hash2 := newWorkspaceRepo(t, "present.txt", "z\n")
	a2 := newAdapter(t)
	thr2 := seedThreadInProject(t, a2, ctx, repo2)
	_, err = a2.GetFileVersion(ctx, thr2, "absent.txt", hash2)
	if !errors.Is(err, memops.ErrFileVersionUnreachable) {
		t.Fatalf("absent-path err = %v, want ErrFileVersionUnreachable", err)
	}
}

// A6: aging is idempotent — reachable ages once then no-ops; refused retains
// both times and logs both refusals.
func TestM1_AgingIdempotent(t *testing.T) {
	hasGitT(t)
	ctx := context.Background()

	// Reachable: age once, second pass is a no-op (chain already gone).
	t.Run("reachable_ages_once", func(t *testing.T) {
		a := newAdapter(t)
		const path = "f.txt"
		const content = "c\n"
		repo, hash := newWorkspaceRepo(t, path, content)
		thr := seedThreadInProject(t, a, ctx, repo)
		if err := a.RecordFileWrite(ctx, thr, path, content); err != nil {
			t.Fatal(err)
		}
		if err := a.RecordFileCommit(ctx, thr, path, hash, 5); err != nil {
			t.Fatal(err)
		}
		aged1, _, err := a.AgeFileChains(ctx, thr, agePastTurnWindow(5))
		if err != nil || len(aged1) != 1 {
			t.Fatalf("first pass: aged=%v err=%v, want one aged", aged1, err)
		}
		aged2, freed2, err := a.AgeFileChains(ctx, thr, agePastTurnWindow(5))
		if err != nil {
			t.Fatalf("second pass: %v", err)
		}
		if len(aged2) != 0 || freed2 != 0 {
			t.Errorf("second pass re-aged: aged=%v freed=%d, want no-op", aged2, freed2)
		}
	})

	// Refused: retain both times, two refusal lines logged.
	t.Run("refused_retains_both", func(t *testing.T) {
		a := newAdapter(t)
		const path = "f.txt"
		repo, _ := newWorkspaceRepo(t, path, "real\n")
		thr := seedThreadInProject(t, a, ctx, repo)
		const orphan = "0123456789012345678901234567890123456789"
		if err := a.RecordFileWrite(ctx, thr, path, "lit\n"); err != nil {
			t.Fatal(err)
		}
		if err := a.RecordFileCommit(ctx, thr, path, orphan, 5); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			aged, _, err := a.AgeFileChains(ctx, thr, agePastTurnWindow(5))
			if err != nil {
				t.Fatalf("pass %d: %v", i, err)
			}
			if len(aged) != 0 {
				t.Fatalf("pass %d aged %v, want refusal", i, aged)
			}
		}
		// Chain still present.
		tf, _ := store.LoadThreadFiles(a.paths, thr)
		if e, ok := tf.Entry(path); !ok || e.Chain.Len() == 0 {
			t.Fatalf("chain dropped across refused passes: entry=%v", e)
		}
		// Two refusal lines.
		if n := strings.Count(readEventLog(t, a), "chain-age-refused"); n != 2 {
			t.Errorf("chain-age-refused count = %d, want 2", n)
		}
	})
}

// Git-absent degradation (Q4): when the workspace root cannot be resolved to
// a git repo, every candidate is refused-and-retained (no second copy to
// minimize against). Exercised here via an unresolvable root, which the
// resolver reports as ok=false (the same path a git-less environment takes).
func TestM1_NoWorkspaceGitRetainsAll(t *testing.T) {
	ctx := context.Background()
	a := newAdapter(t)
	const path = "f.txt"
	const hash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	bogusRoot := filepath.Join(t.TempDir(), "no-git-here")
	thr := seedThreadInProject(t, a, ctx, bogusRoot)
	if err := a.RecordFileWrite(ctx, thr, path, "lit\n"); err != nil {
		t.Fatal(err)
	}
	if err := a.RecordFileCommit(ctx, thr, path, hash, 5); err != nil {
		t.Fatal(err)
	}
	aged, _, err := a.AgeFileChains(ctx, thr, agePastTurnWindow(5))
	if err != nil {
		t.Fatalf("AgeFileChains: %v", err)
	}
	if len(aged) != 0 {
		t.Fatalf("aged=%v, want all retained when workspace git unresolvable", aged)
	}
	if !strings.Contains(readEventLog(t, a), "chain-age-refused") {
		t.Error("no refusal logged for unresolvable workspace root")
	}
}
