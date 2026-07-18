package autogit

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// seedTrackedFile writes a file under paths.Home and commits it (with
// everything else pending), returning its repo-relative slash path.
func writeHomeFile(t *testing.T, home, rel, content string) string {
	t.Helper()
	abs := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

func readHomeFile(t *testing.T, home, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestResetHard_RevertsTrackedOnly(t *testing.T) {
	ctx := context.Background()
	paths := scaffoldHome(t)

	// Baseline: two tracked files committed.
	writeHomeFile(t, paths.Home, "threads/a.md", "committed-a\n")
	writeHomeFile(t, paths.Home, "threads/b.md", "committed-b\n")
	if err := Add(ctx, paths, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Commit(ctx, paths, "baseline", 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	head := repoHead(t, paths)

	// Damage: modify a tracked file, delete another, stage a brand-new
	// file, drop an untracked file, and write a gitignored operational
	// file (the marker) — the last three must all survive untouched.
	writeHomeFile(t, paths.Home, "threads/a.md", "TORN WRITE\n")
	if err := os.Remove(filepath.Join(paths.Home, "threads", "b.md")); err != nil {
		t.Fatalf("remove b.md: %v", err)
	}
	writeHomeFile(t, paths.Home, "threads/staged-new.md", "staged\n")
	if err := Add(ctx, paths, "threads/staged-new.md"); err != nil {
		t.Fatalf("Add staged-new: %v", err)
	}
	writeHomeFile(t, paths.Home, "threads/untracked.md", "untracked\n")
	writeHomeFile(t, paths.Home, "op-in-progress.json", `{"op":"turn","turn":"t9"}`)

	reverted, err := ResetHard(ctx, paths)
	if err != nil {
		t.Fatalf("ResetHard: %v", err)
	}
	want := []string{"threads/a.md", "threads/b.md"}
	if !reflect.DeepEqual(reverted, want) {
		t.Errorf("revertedPaths = %v, want %v", reverted, want)
	}
	if got := readHomeFile(t, paths.Home, "threads/a.md"); got != "committed-a\n" {
		t.Errorf("a.md not reverted: %q", got)
	}
	if got := readHomeFile(t, paths.Home, "threads/b.md"); got != "committed-b\n" {
		t.Errorf("b.md not restored: %q", got)
	}
	// Untracked, staged-new (now untracked), and gitignored files survive.
	for _, rel := range []string{"threads/staged-new.md", "threads/untracked.md", "op-in-progress.json"} {
		if _, err := os.Stat(filepath.Join(paths.Home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s did not survive reset: %v", rel, err)
		}
	}
	if repoHead(t, paths) != head {
		t.Error("ResetHard moved HEAD")
	}

	// Post-reset worktree state: no tracked dirt; exactly the two
	// non-ignored leftovers are untracked (the marker is gitignored and
	// must appear in neither list).
	wt, err := Worktree(ctx, paths)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if len(wt.DirtyPaths) != 0 {
		t.Errorf("tracked dirt after reset: %v", wt.DirtyPaths)
	}
	wantUntracked := []string{"threads/staged-new.md", "threads/untracked.md"}
	if !reflect.DeepEqual(wt.Untracked, wantUntracked) {
		t.Errorf("untracked = %v, want %v", wt.Untracked, wantUntracked)
	}
}

func TestWorktree_IgnoredFilesInvisible(t *testing.T) {
	ctx := context.Background()
	paths := scaffoldHome(t)

	// All four crash-substrate operational files are gitignored; none
	// may register as dirt or debris (load-bearing: they must survive
	// recovery's sweep and never trigger cell-3/4 dispatch).
	writeHomeFile(t, paths.Home, "op-in-progress.json", `{"op":"sleep"}`)
	writeHomeFile(t, paths.Home, "turn-journal.jsonl", "{}\n")
	writeHomeFile(t, paths.Home, "derived-watermark", "abc\n")
	writeHomeFile(t, paths.Home, "recovery/recovered-turn-t1-abcd.md", "preserved\n")

	wt, err := Worktree(ctx, paths)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if len(wt.DirtyPaths) != 0 || len(wt.Untracked) != 0 {
		t.Errorf("gitignored operational files leaked into status: dirty=%v untracked=%v",
			wt.DirtyPaths, wt.Untracked)
	}
}

func TestTurnTrailerRoundTrip(t *testing.T) {
	ctx := context.Background()
	paths := scaffoldHome(t)

	// Fresh home: HEAD is the init commit — no trailer, HeadTurn is ⊥.
	turn, err := HeadTurn(ctx, paths)
	if err != nil {
		t.Fatalf("HeadTurn: %v", err)
	}
	if turn != "" {
		t.Errorf("HeadTurn on trailer-less HEAD = %q, want \"\"", turn)
	}

	writeHomeFile(t, paths.Home, "threads/x.md", "x\n")
	if err := Add(ctx, paths, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Commit(ctx, paths, TurnCommitMessage("t42", "2 events"), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	turn, err = HeadTurn(ctx, paths)
	if err != nil {
		t.Fatalf("HeadTurn: %v", err)
	}
	if turn != "t42" {
		t.Errorf("HeadTurn = %q, want t42", turn)
	}
}

func TestParseTurnTrailer(t *testing.T) {
	cases := []struct {
		name, msg, want string
	}{
		{"absent", "checkpoint: session-close\n", ""},
		{"present", "turn t7: ok\n\nPersonant-Turn: t7\n", "t7"},
		{"last-wins", "Personant-Turn: t1\n\nPersonant-Turn: t2\n", "t2"},
		{"indented", "subject\n\n  Personant-Turn: t3\n", "t3"},
		{"prefix-not-line", "notPersonant-Turn: t9\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseTurnTrailer(tc.msg); got != tc.want {
				t.Errorf("ParseTurnTrailer(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
	if got := ParseTurnTrailer(TurnCommitMessage("t7", "")); got != "t7" {
		t.Errorf("TurnCommitMessage round-trip = %q, want t7", got)
	}
}
