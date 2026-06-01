package fileadapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsolidate_RunsAndLogs: Consolidate calls through to substrate gc on
// a real (post-init) home and records a consolidate.sleep-cycle event line
// with the supplied reason and gc=ok. The call must succeed — a healthy
// repo's gc has no reason to fail.
func TestConsolidate_RunsAndLogs(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.Consolidate(ctx, "sleep-cycle: day-off"); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	log := readEventLog(t, a)
	if !strings.Contains(log, "consolidate.sleep-cycle reason=sleep-cycle: day-off gc=ok") {
		t.Errorf("expected consolidate.sleep-cycle line with reason and gc=ok\n%s", log)
	}
}

// TestConsolidate_GCFailureNonFatal: a gc failure must not abort the caller
// — Consolidate logs gc=err and returns nil. We provoke the failure by
// pointing the adapter's home at a non-repository directory: autogit.GC's
// PlainOpen fails, which Consolidate treats as non-fatal.
func TestConsolidate_GCFailureNonFatal(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// Corrupt the repo so gc's PlainOpen fails: remove the .git directory.
	// The LogsDir is separate from .git, so the event-log write still
	// succeeds and we can assert gc=err.
	gitDir := filepath.Join(a.paths.Home, ".git")
	if err := os.RemoveAll(gitDir); err != nil {
		t.Fatalf("remove .git: %v", err)
	}

	if err := a.Consolidate(ctx, "sleep-cycle: day-off"); err != nil {
		t.Fatalf("Consolidate must be non-fatal on gc failure, got: %v", err)
	}

	log := readEventLog(t, a)
	if !strings.Contains(log, "gc=err") {
		t.Errorf("expected gc=err on a failed gc\n%s", log)
	}
}
