package fileadapter

import (
	"context"
	"strings"
	"testing"

	"personant/internal/autogit"
	"personant/internal/memops"
	"personant/internal/store"
)

// Tests for the crash-stability port surface: JournalTurn's marker
// side effect, CommitTurn's trailer/clear/truncate ordering, and the
// Reconcile delegation. The recovery state machine itself is covered in
// internal/recovery; these verify the adapter's wiring and contracts.

func TestJournalTurn_FirstCallSetsMarker(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.JournalTurn(ctx, "t1", memops.TurnContentPrompt, []byte("the prompt")); err != nil {
		t.Fatalf("JournalTurn prompt: %v", err)
	}
	m, present, err := store.ReadMarker(a.paths)
	if err != nil || !present {
		t.Fatalf("marker after first JournalTurn: present=%v err=%v", present, err)
	}
	if m.Op != store.OpTurn || m.Turn != "t1" {
		t.Errorf("marker = %+v, want op=turn turn=t1", m)
	}

	// Second call (response) rides the same marker scope.
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
		!strings.Contains(err.Error(), "conflicting in-flight marker") {
		t.Errorf("cross-turn journal under t1's marker: err=%v, want conflict refusal", err)
	}
	if records, _, err := store.ScanJournal(a.paths); err != nil || len(records) != 1 {
		t.Fatalf("ScanJournal after refused cross-turn append: records=%d err=%v, want the single t1 record", len(records), err)
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
	if _, present, _ := store.ReadMarker(a.paths); present {
		t.Error("marker not cleared by CommitTurn")
	}
	records, torn, err := store.ScanJournal(a.paths)
	if err != nil || len(records) != 0 || torn != 0 {
		t.Errorf("journal not truncated: records=%d torn=%d err=%v", len(records), torn, err)
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
	// benign no-op, but marker and journal MUST still be released or the
	// next open would misread a completed turn as torn.
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
	if _, present, _ := store.ReadMarker(a.paths); present {
		t.Error("marker not cleared")
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
		!strings.Contains(err.Error(), "conflicting in-flight marker") {
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
	if _, present, _ := store.ReadMarker(a.paths); present {
		t.Error("marker survived reconcile")
	}
}
