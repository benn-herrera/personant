package fileadapter

import (
	"context"
	"errors"
	"testing"

	"personant/internal/autogit"
	"personant/internal/memops"
	"personant/internal/store"
)

// headHash is a test helper returning the current HEAD commit hash.
func headHash(t *testing.T, a *FileAdapter) string {
	t.Helper()
	h, err := autogit.HeadHash(context.Background(), a.paths, autogit.Daily)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	return h
}

// TestCheckpoint_DirtyTreeAdvancesHEAD: a Checkpoint over a dirty worktree
// (an uncommitted thread create) writes a commit — HEAD must advance.
func TestCheckpoint_DirtyTreeAdvancesHEAD(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	before := headHash(t, a)

	// CreateThread writes thread.md + spine but does NOT commit (the §3.11
	// cadence owns commits), so the worktree is now dirty.
	rec := validSpine("thr_1", "prj_1")
	if err := a.CreateThread(ctx, memops.ThreadWrite{
		Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "first turn",
	}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	if err := a.Checkpoint(ctx, "structural: +1 thread, -0 retired"); err != nil {
		t.Fatalf("Checkpoint (dirty): %v", err)
	}

	after := headHash(t, a)
	if after == before {
		t.Fatalf("HEAD did not advance after Checkpoint over a dirty tree (still %s)", before)
	}
}

// TestCheckpoint_CleanTreeIsBenignNoOp: a Checkpoint over a clean worktree
// (nothing staged-or-changed) is a no-op — no error, HEAD unchanged. This is
// the ErrEmptyCommit-swallow path (archival already committed, or a
// content-less structural change).
func TestCheckpoint_CleanTreeIsBenignNoOp(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// store.Init left a committed, clean tree. A first Checkpoint commits
	// nothing.
	before := headHash(t, a)
	if err := a.Checkpoint(ctx, "session-close"); err != nil {
		t.Fatalf("Checkpoint (clean) returned error, want benign no-op: %v", err)
	}
	if got := headHash(t, a); got != before {
		t.Fatalf("clean-tree Checkpoint advanced HEAD: %s -> %s", before, got)
	}

	// And a second Checkpoint immediately after a real one is likewise a
	// no-op: commit a dirty tree, then re-checkpoint the now-clean tree.
	rec := validSpine("thr_1", "prj_1")
	if err := a.CreateThread(ctx, memops.ThreadWrite{
		Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "first turn",
	}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if err := a.Checkpoint(ctx, "structural"); err != nil {
		t.Fatalf("Checkpoint (dirty): %v", err)
	}
	committed := headHash(t, a)
	if err := a.Checkpoint(ctx, "session-close"); err != nil {
		t.Fatalf("Checkpoint (clean-after-commit): %v", err)
	}
	if got := headHash(t, a); got != committed {
		t.Fatalf("redundant Checkpoint advanced HEAD: %s -> %s", committed, got)
	}
}

// TestCheckpoint_RefusedUnderInFlightScope (R3 review F1, R3-addendum
// fold): a checkpoint under ANY in-flight scope — a non-empty turn
// journal or a batch-op marker — is refused. A trailer-less commit of a
// scope-retained turn's torn prefix would reconcile as cell 6 ("nothing
// landed") and make the torn state permanent. The refusal wraps
// memops.ErrConflictingMarker so the chat session-close can recognize it
// and report "preserved for recovery" instead of a fault.
func TestCheckpoint_RefusedUnderInFlightScope(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// The torn-prefix shape: canonical dirt from an uncommitted turn,
	// with the turn's scope still open — a non-empty journal (CommitTurn
	// failed before its truncate).
	rec := validSpine("thr_1", "prj_1")
	if err := a.CreateThread(ctx, memops.ThreadWrite{
		Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "torn turn",
	}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if err := store.AppendJournal(a.paths, "t9", store.JournalPrompt, []byte("torn turn prompt")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	before := headHash(t, a)
	err := a.Checkpoint(ctx, "session-close")
	if err == nil || !errors.Is(err, memops.ErrConflictingMarker) {
		t.Fatalf("Checkpoint under in-flight turn scope: err=%v, want ErrConflictingMarker", err)
	}
	if got := headHash(t, a); got != before {
		t.Fatalf("refused Checkpoint still advanced HEAD: %s -> %s", before, got)
	}

	// A batch-op marker refuses identically.
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpSleep}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := a.Checkpoint(ctx, "session-close"); err == nil || !errors.Is(err, memops.ErrConflictingMarker) {
		t.Fatalf("Checkpoint under op=sleep marker: err=%v, want ErrConflictingMarker", err)
	}
	if err := store.ClearMarker(a.paths); err != nil {
		t.Fatalf("ClearMarker: %v", err)
	}

	// With the scope released the same tree checkpoints normally — the
	// guard, not the dirt, was the blocker.
	if err := store.TruncateJournal(a.paths); err != nil {
		t.Fatalf("TruncateJournal: %v", err)
	}
	if err := a.Checkpoint(ctx, "session-close"); err != nil {
		t.Fatalf("Checkpoint after scope release: %v", err)
	}
	if got := headHash(t, a); got == before {
		t.Fatal("post-release Checkpoint did not commit the dirty tree")
	}
}
