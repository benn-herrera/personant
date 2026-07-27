package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// TestArchiveListRecoverRoundTrip drives the real CLI surface end-to-end:
// seed + archive a thread through the port, then exercise `archive list`
// (the entry appears) and `archive recover <id>` (it round-trips back onto
// the spine as wip with the index breadcrumb retained, RecoveredAt stamped).
func TestArchiveListRecoverRoundTrip(t *testing.T) {
	restore := clock.SetTimeline(func() time.Time {
		return time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	})
	t.Cleanup(restore)

	home := t.TempDir()
	paths := store.PathsForHome(home)
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	ops := fileadapter.NewFileAdapter(paths)
	ctx := context.Background()

	const thrID = "thr_1"
	seedThreadForCLI(t, ops, thrID, "prj_1")
	if _, err := ops.ArchiveThreads(ctx, []string{thrID}); err != nil {
		t.Fatalf("ArchiveThreads: %v", err)
	}

	// `archive list` — the archived entry appears.
	out := runCLI(t, home, "archive", "list")
	if !strings.Contains(out, thrID) {
		t.Fatalf("archive list omitted %s:\n%s", thrID, out)
	}
	if strings.Contains(out, "RECOVERED") {
		t.Fatalf("archive list marked %s RECOVERED before recovery:\n%s", thrID, out)
	}

	// `archive recover <id>` — round-trips back onto the spine as wip.
	out = runCLI(t, home, "archive", "recover", thrID)
	if !strings.Contains(out, thrID) || !strings.Contains(out, "wip") {
		t.Fatalf("recover output missing id/state:\n%s", out)
	}

	rec, found, err := store.FindSpineRecord(paths, thrID)
	if err != nil {
		t.Fatalf("FindSpineRecord: %v", err)
	}
	if !found {
		t.Fatalf("%s not back on spine after recover", thrID)
	}
	if rec.State != memops.ThreadWIP {
		t.Errorf("recovered state = %q, want wip", rec.State)
	}

	// Index entry retained as a breadcrumb with RecoveredAt stamped.
	idx, err := store.LoadArchiveIndex(paths)
	if err != nil {
		t.Fatalf("LoadArchiveIndex: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("index has %d entries after recover, want 1 (breadcrumb retained)", len(idx))
	}
	if idx[0].RecoveredAt == "" {
		t.Errorf("recovered entry missing RecoveredAt stamp")
	}

	// `archive list` now marks the entry RECOVERED.
	out = runCLI(t, home, "archive", "list")
	if !strings.Contains(out, "RECOVERED") {
		t.Errorf("archive list did not mark %s RECOVERED after recovery:\n%s", thrID, out)
	}
}

// TestArchiveRecoverNotFound asserts the cmd surfaces a non-zero error for an
// unknown id (the adapter maps the missing entry to ErrArchiveEntryNotFound;
// the cmd translates it to a clean user message). Integrity-failure mapping is
// covered at the adapter layer (I2 tests).
func TestArchiveRecoverNotFound(t *testing.T) {
	home := t.TempDir()
	if err := store.Init(store.PathsForHome(home), store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	err := execCLI(home, "archive", "recover", "thr_404")
	if err == nil {
		t.Fatal("recover of unknown id returned nil error, want non-zero")
	}
	if !strings.Contains(err.Error(), "thr_404") {
		t.Errorf("error %q does not name the missing id", err)
	}
}

// TestArchiveListEmpty asserts an empty index prints a friendly line, not an
// error.
func TestArchiveListEmpty(t *testing.T) {
	home := t.TempDir()
	if err := store.Init(store.PathsForHome(home), store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	out := runCLI(t, home, "archive", "list")
	if !strings.Contains(out, "no archived threads") {
		t.Errorf("empty index did not print the friendly line:\n%s", out)
	}
}

// seedThreadForCLI creates a minimal valid thread through the port.
func seedThreadForCLI(t *testing.T, ops *fileadapter.FileAdapter, id, project string) {
	t.Helper()
	now := clock.Timeline().Format(time.RFC3339)
	rec := memops.SpineRecord{
		ID:           id,
		Project:      project,
		Anchors:      []string{"alpha", "beta"},
		Summary:      "test thread",
		State:        memops.ThreadActive,
		Created:      now,
		LastEngaged:  now,
		StateChanged: now,
		TurnCount:    1,
	}
	meta := memops.ThreadMeta{
		ID:           rec.ID,
		Project:      rec.Project,
		Anchors:      rec.Anchors,
		Summary:      rec.Summary,
		State:        rec.State,
		Created:      rec.Created,
		LastEngaged:  rec.LastEngaged,
		StateChanged: rec.StateChanged,
		TurnCount:    rec.TurnCount,
	}
	w := memops.ThreadWrite{Spine: rec, Meta: meta, TurnExcerpt: "## Turn 1\n\nx\n"}
	if err := ops.CreateThread(context.Background(), w); err != nil {
		t.Fatalf("seed CreateThread %s: %v", id, err)
	}
}

// runCLI executes the root command with args against home and returns captured
// stdout, failing the test on a non-nil command error.
func runCLI(t *testing.T, home string, args ...string) string {
	t.Helper()
	out, err := captureCLI(home, args...)
	if err != nil {
		t.Fatalf("cli %v: %v", args, err)
	}
	return out
}

// execCLI runs the root command and returns its error, discarding stdout.
func execCLI(home string, args ...string) error {
	_, err := captureCLI(home, args...)
	return err
}

// captureCLI redirects os.Stdout (the commands print via fmt.Print*), runs the
// root command with --home wired to the temp store, and returns stdout + the
// command error. flagHome is reset after the run to avoid leaking across tests.
func captureCLI(home string, args ...string) (string, error) {
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs(append([]string{"--home", home}, args...))
	runErr := rootCmd.Execute()

	w.Close()
	os.Stdout = orig
	// Persistent flags keep their parsed value between Execute() calls in
	// the same process — reset every one, or a test that passes an override
	// silently arms the tests that follow it.
	flagHome = ""
	flagAllowNewerHome = false

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String(), runErr
}
