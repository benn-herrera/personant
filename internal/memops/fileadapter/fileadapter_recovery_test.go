package fileadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"personant/internal/autogit"
	"personant/internal/memops"
	"personant/internal/store"
)

// Tests for the crash-stability port surface: JournalTurn's scope-open
// side effect (the marker-into-journal fold), CommitTurn's
// trailer/truncate ordering, and the Reconcile delegation. The recovery
// state machine itself is covered in internal/recovery; these verify
// the adapter's wiring and contracts.

func TestJournalTurn_FirstAppendOpensScope(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "t1", memops.TurnContentPrompt, []byte("the prompt")); err != nil {
		t.Fatalf("JournalTurn prompt: %v", err)
	}
	// R3-addendum fold: the non-empty journal IS the in-flight-turn
	// signal; NO marker file is written for op=turn.
	if _, present, err := store.ReadMarker(a.paths); err != nil || present {
		t.Fatalf("marker file after first JournalTurn: present=%v err=%v, want absent (journal carries the signal)", present, err)
	}
	owner, inFlight, err := store.JournalOwner(a.paths)
	if err != nil || !inFlight || owner != "t1" {
		t.Fatalf("JournalOwner = (%q,%v,%v), want (t1,true,nil)", owner, inFlight, err)
	}

	// Second call (response) rides the same scope.
	if err := a.JournalTurn(ctx, "t1", memops.TurnContentResponse, []byte("the response")); err != nil {
		t.Fatalf("JournalTurn response: %v", err)
	}
	records, torn, err := store.ScanJournal(a.paths)
	if err != nil || torn != 0 {
		t.Fatalf("ScanJournal: torn=%d err=%v", torn, err)
	}
	if len(records) != 2 || records[0].Kind != store.JournalPrompt || records[1].Kind != store.JournalResponse {
		t.Errorf("journal records = %+v", records)
	}
	if string(records[0].Bytes) != "the prompt" {
		t.Errorf("prompt bytes = %q", records[0].Bytes)
	}
}

func TestJournalTurn_ContractViolations(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "", memops.TurnContentPrompt, []byte("x")); err == nil {
		t.Error("empty turnID accepted")
	}
	if err := a.JournalTurn(ctx, "t1", memops.TurnContentKind("bogus"), []byte("x")); err == nil {
		t.Error("unknown content kind accepted")
	}
	// A conflicting in-flight scope is refused, not silently adopted.
	if err := a.JournalTurn(ctx, "t1", memops.TurnContentPrompt, []byte("x")); err != nil {
		t.Fatalf("JournalTurn t1: %v", err)
	}
	if err := a.JournalTurn(ctx, "t2", memops.TurnContentPrompt, []byte("x")); err == nil ||
		!errors.Is(err, memops.ErrConflictingMarker) {
		t.Errorf("cross-turn journal under t1's scope: err=%v, want conflict refusal", err)
	}
	if records, _, err := store.ScanJournal(a.paths); err != nil || len(records) != 1 {
		t.Fatalf("ScanJournal after refused cross-turn append: records=%d err=%v, want the single t1 record", len(records), err)
	}
	// A batch-op marker file refuses the turn verbs outright.
	if err := store.TruncateJournal(a.paths); err != nil {
		t.Fatalf("TruncateJournal: %v", err)
	}
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpArchival}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := a.JournalTurn(ctx, "t3", memops.TurnContentPrompt, []byte("x")); err == nil ||
		!errors.Is(err, memops.ErrConflictingMarker) {
		t.Errorf("journal under archival marker: err=%v, want conflict refusal", err)
	}
}

func TestCommitTurn_TrailerClearTruncate(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	// A full mini-turn: journal, canonical write, commit.
	if err := a.JournalTurn(ctx, "t7", memops.TurnContentPrompt, []byte("p")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	rec := validSpine("thr_1", "prj_default")
	if err := a.CreateThread(ctx, memops.ThreadWrite{
		Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\nhello\n",
	}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if err := a.CommitTurn(ctx, "t7", "1 thread created"); err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}

	turn, err := autogit.HeadTurn(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadTurn: %v", err)
	}
	if turn != "t7" {
		t.Errorf("HEAD trailer = %q, want t7", turn)
	}
	// The truncate IS the scope release: journal empty, no in-flight turn.
	records, torn, err := store.ScanJournal(a.paths)
	if err != nil || len(records) != 0 || torn != 0 {
		t.Errorf("journal not truncated: records=%d torn=%d err=%v", len(records), torn, err)
	}
	if _, inFlight, err := store.JournalOwner(a.paths); err != nil || inFlight {
		t.Errorf("scope still in flight after CommitTurn: inFlight=%v err=%v", inFlight, err)
	}
}

func TestCommitTurn_NoCanonicalChangeStillReleases(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "t9", memops.TurnContentPrompt, []byte("chit-chat")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	headBefore, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	// Nothing canonical was written this turn: the empty commit is a
	// benign no-op, but the journal MUST still be truncated (the scope
	// release) or the next open would misread a completed turn as torn.
	if err := a.CommitTurn(ctx, "t9", "no structural change"); err != nil {
		t.Fatalf("CommitTurn on clean tree: %v", err)
	}
	headAfter, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if headBefore != headAfter {
		t.Errorf("empty turn moved HEAD: %s → %s", headBefore, headAfter)
	}
	records, _, _ := store.ScanJournal(a.paths)
	if len(records) != 0 {
		t.Error("journal not truncated")
	}
}

func TestCommitTurn_ConflictingMarkerRefused(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpArchival}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := a.CommitTurn(ctx, "t1", "x"); err == nil ||
		!errors.Is(err, memops.ErrConflictingMarker) {
		t.Errorf("CommitTurn under archival marker: err=%v, want conflict refusal", err)
	}
}

func TestReconcile_AdapterDelegatesAndRecovers(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	// First reconcile establishes the watermark (greenfield cell 2).
	rep, err := a.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !rep.DerivedRebuilt {
		t.Error("first reconcile did not rebuild derived state")
	}

	// A journaled-but-uncommitted turn (marker + journal, clean tree):
	// the next reconcile must clear it and preserve the prompt.
	if err := a.JournalTurn(ctx, "t2", memops.TurnContentPrompt, []byte("in-flight prompt")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	rep, err = a.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile after simulated crash: %v", err)
	}
	if rep.ClearedOp != "turn" || rep.ClearedTurn != "t2" {
		t.Errorf("cleared op/turn = %q/%q", rep.ClearedOp, rep.ClearedTurn)
	}
	if rep.PreservedContentPath == "" {
		t.Error("in-flight prompt not preserved")
	}
	if _, inFlight, _ := store.JournalOwner(a.paths); inFlight {
		t.Error("in-flight turn scope survived reconcile")
	}
}

// ---------- ReleaseTurn (pre-canonical abort release, R3 review F3) ----------

// TestReleaseTurn_LogsOnlyDirtyReleasesWithoutCommit: the common abort
// shape — journal holding the prompt (the in-flight scope), only logs/
// dirty — releases the scope by truncating the journal: no commit is
// minted (a provider-outage retry loop must not emit commit-per-error;
// the log dirt absorbs into a later commit).
func TestReleaseTurn_LogsOnlyDirtyReleasesWithoutCommit(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "t3", memops.TurnContentPrompt, []byte("doomed prompt")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	// The event log is written continuously between recovery points —
	// dirty it, as any real turn does before it can fail.
	if err := a.Log(ctx, memops.LogCategorySystem, "test-noise", "x"); err != nil {
		t.Fatalf("Log: %v", err)
	}
	headBefore, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}

	if err := a.ReleaseTurn(ctx, "t3"); err != nil {
		t.Fatalf("ReleaseTurn: %v", err)
	}

	headAfter, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if headAfter != headBefore {
		t.Errorf("logs-only release minted a commit: %s → %s", headBefore, headAfter)
	}
	if records, torn, err := store.ScanJournal(a.paths); err != nil || len(records) != 0 || torn != 0 {
		t.Errorf("journal not truncated: records=%d torn=%d err=%v", len(records), torn, err)
	}

	// Releasing when no scope exists (the prompt journal append itself
	// failed, so the journal is empty) is benign.
	if err := a.ReleaseTurn(ctx, "t4"); err != nil {
		t.Errorf("ReleaseTurn without an open scope: %v", err)
	}
}

// TestReleaseTurn_CanonicalDirtFallsBackToCommit: canonical dirt beyond
// logs/ means the nothing-canonical-happened precondition does not hold;
// the release must fall back to the full CommitTurn path (trailer'd
// commit) rather than leave torn-looking bytes markerless.
func TestReleaseTurn_CanonicalDirtFallsBackToCommit(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "t5", memops.TurnContentPrompt, []byte("p")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	rec := validSpine("thr_1", "prj_default")
	if err := a.CreateThread(ctx, memops.ThreadWrite{
		Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\nhello\n",
	}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	headBefore, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}

	if err := a.ReleaseTurn(ctx, "t5"); err != nil {
		t.Fatalf("ReleaseTurn: %v", err)
	}

	headAfter, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if headAfter == headBefore {
		t.Error("canonical-dirty release did not commit")
	}
	turn, err := autogit.HeadTurn(ctx, a.paths)
	if err != nil {
		t.Fatalf("HeadTurn: %v", err)
	}
	if turn != "t5" {
		t.Errorf("fallback commit trailer = %q, want t5", turn)
	}
	if _, inFlight, _ := store.JournalOwner(a.paths); inFlight {
		t.Error("scope not released by the fallback commit")
	}
}

// TestReleaseTurn_ConflictingMarkerRefused: a scope belonging to a
// different turn is never released — the wedged scope must survive for
// Reconcile.
func TestReleaseTurn_ConflictingMarkerRefused(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "t1", memops.TurnContentPrompt, []byte("x")); err != nil {
		t.Fatalf("JournalTurn: %v", err)
	}
	err := a.ReleaseTurn(ctx, "t2")
	if err == nil || !errors.Is(err, memops.ErrConflictingMarker) {
		t.Fatalf("ReleaseTurn t2 under t1's scope: err=%v, want ErrConflictingMarker", err)
	}
	owner, inFlight, _ := store.JournalOwner(a.paths)
	if !inFlight || owner != "t1" {
		t.Errorf("t1's scope disturbed by refused release: owner=%q inFlight=%v", owner, inFlight)
	}
}

// TestCommitTurn_ScopedStagingLeavesHandEditsDirty is the R3-addendum
// item-3 policy proof: per-turn commits stage only the turn's recorded
// write set, so a hand-edit made outside the adapter (a) is never
// committed by a turn, (b) is never reverted or reset, and (c) stays
// visibly dirty until the FULL-sweep backstop Checkpoint absorbs it.
func TestCommitTurn_ScopedStagingLeavesHandEditsDirty(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// Hand-edit a tracked canonical file outside the adapter.
	const sentinel = "HAND EDIT — must stay dirty across scoped turn commits\n"
	userMD := filepath.Join(a.paths.DirectivesDir, "user.md")
	if err := os.WriteFile(userMD, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("hand-edit user.md: %v", err)
	}

	// Several full turns, each landing a scoped per-turn commit.
	for i := 1; i <= 3; i++ {
		turnID := fmt.Sprintf("t%d", i)
		if err := a.JournalTurn(ctx, turnID, memops.TurnContentPrompt, []byte("p")); err != nil {
			t.Fatalf("JournalTurn %s: %v", turnID, err)
		}
		rec := validSpine(fmt.Sprintf("thr_%d", i), "prj_default")
		if err := a.CreateThread(ctx, memops.ThreadWrite{
			Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\nhello\n",
		}); err != nil {
			t.Fatalf("CreateThread %d: %v", i, err)
		}
		if err := a.CommitTurn(ctx, turnID, ""); err != nil {
			t.Fatalf("CommitTurn %s: %v", turnID, err)
		}
		// The turn's own writes landed (trailer moved)...
		headTurn, err := autogit.HeadTurn(ctx, a.paths)
		if err != nil || headTurn != turnID {
			t.Fatalf("HEAD trailer after turn %d = %q err=%v", i, headTurn, err)
		}
		// ...but the hand-edit is untouched, uncommitted, and unreset.
		got, err := os.ReadFile(userMD)
		if err != nil || string(got) != sentinel {
			t.Fatalf("hand-edit bytes after turn %d: %q err=%v", i, got, err)
		}
		wt, err := autogit.Worktree(ctx, a.paths)
		if err != nil {
			t.Fatalf("Worktree: %v", err)
		}
		if !slices.Contains(wt.DirtyPaths, "directives/user.md") {
			t.Fatalf("hand-edit not dirty after turn %d (scoped commit absorbed it?): dirty=%v", i, wt.DirtyPaths)
		}
	}

	// The backstop Checkpoint (full Add(".") sweep) absorbs it.
	if err := a.Checkpoint(ctx, "session-close"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	wt, err := autogit.Worktree(ctx, a.paths)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if slices.Contains(wt.DirtyPaths, "directives/user.md") {
		t.Fatalf("backstop Checkpoint did not absorb the hand-edit: dirty=%v", wt.DirtyPaths)
	}
	if got, err := os.ReadFile(userMD); err != nil || string(got) != sentinel {
		t.Fatalf("hand-edit bytes after backstop: %q err=%v", got, err)
	}
}
