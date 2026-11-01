package autogit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/store"
)

func TestIndexEntryCount(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	paths := store.PathsForHome(home)
	paths.GitDaily = filepath.Join(t.TempDir(), "git-daily") // outside the worktree, so it is not staged
	if err := InitDaily(ctx, paths); err != nil {
		t.Fatalf("InitDaily: %v", err)
	}
	base, err := IndexEntryCount(paths, Daily)
	if err != nil {
		t.Fatalf("IndexEntryCount baseline: %v", err)
	}
	if base != 0 {
		t.Fatalf("fresh repo: got %d entries, want 0", base)
	}

	const k = 5
	if err := os.MkdirAll(filepath.Join(home, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := range k {
		rel := filepath.Join("sub", fmt.Sprintf("f%d.md", i))
		if err := os.WriteFile(filepath.Join(home, rel), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := Add(ctx, paths, Daily, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := IndexEntryCount(paths, Daily)
	if err != nil {
		t.Fatalf("IndexEntryCount: %v", err)
	}
	if got != base+k {
		t.Fatalf("got %d entries, want %d (baseline %d + %d)", got, base+k, base, k)
	}
}
