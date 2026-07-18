package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// CLI reconcile wiring (#94, Wave R3 — the R2 verbs-bypass-Reconcile
// carry): substrate-reading verbs open through reconciledOps, so an
// unclean-shutdown home is repaired before the verb reads it, and a home
// recovery cannot repair refuses the verb.

// TestVerifyReconcilesUncleanHome: a home left with an in-flight turn
// scope — a non-empty journal, the op=turn signal (the mid-model-call
// crash shape, cell 6) — is reconciled by `personant verify` before the
// report runs: journal preserved to a recovery artifact and truncated.
func TestVerifyReconcilesUncleanHome(t *testing.T) {
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
	if err := ops.JournalTurn(context.Background(), "t7", memops.TurnContentPrompt, []byte("orphaned prompt")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	// Precondition: the unclean shape is on disk (in-flight journal).
	if owner, inFlight, err := store.JournalOwner(paths); err != nil || !inFlight || owner != "t7" {
		t.Fatalf("precondition scope: owner=%q inFlight=%v err=%v", owner, inFlight, err)
	}

	runCLI(t, home, "verify", "--quiet")

	if _, inFlight, err := store.JournalOwner(paths); err != nil || inFlight {
		t.Fatalf("scope after verify: inFlight=%v err=%v (verify did not reconcile)", inFlight, err)
	}
	if recs, torn, err := store.ScanJournal(paths); err != nil || torn != 0 || len(recs) != 0 {
		t.Fatalf("journal after verify: records=%d torn=%d err=%v (want truncated)", len(recs), torn, err)
	}
	// The journaled bytes were preserved, not dropped.
	entries, err := os.ReadDir(paths.RecoveryDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("recovery artifacts after verify: entries=%d err=%v (want preserved content)", len(entries), err)
	}
}

// TestVerbRefusesUnreconcilableHome: a corrupt in-flight marker cannot be
// read as "no op running" — Reconcile errors and the verb refuses to
// operate rather than reading possibly-torn state.
func TestVerbRefusesUnreconcilableHome(t *testing.T) {
	home := t.TempDir()
	paths := store.PathsForHome(home)
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := os.WriteFile(paths.OpMarker, []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}

	for _, args := range [][]string{
		{"verify"},
		{"index", "check"},
		{"index", "rebuild"},
		{"archive", "list"},
	} {
		if err := execCLI(home, args...); err == nil || !strings.Contains(err.Error(), "reconcile") {
			t.Errorf("%v on unreconcilable home = %v, want reconcile refusal", args, err)
		}
	}
}
