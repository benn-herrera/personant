package fileadapter

import (
	"context"
	"strings"
	"testing"

	"personant/internal/crashpoint"
	"personant/internal/store"
)

// Tests for the op=archival / op=sleep marker scopes (#94, Wave R3):
// ArchiveThreads runs under its own typed marker (set before the first
// destructive mutation, cleared after the stamp commit) and refuses to
// run inside another operation's scope; Consolidate brackets the gc the
// same way.

// TestArchiveThreads_MarkerScope drives the batch to the armed
// archival.postMarker crashpoint (marker just written, nothing destroyed
// yet), asserts the on-disk op=archival marker a crash there would leave,
// then reconciles and re-runs the batch to completion — the redrain path
// — asserting the scope is released at the end.
func TestArchiveThreads_MarkerScope(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()
	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nbody\n")

	// Crash at archival.postMarker: the marker is on disk, op=archival.
	disarm := crashpoint.Arm("archival.postMarker")
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("armed archival.postMarker did not fire")
			}
			c, ok := r.(*crashpoint.Crash)
			if !ok || c.Point != "archival.postMarker" {
				t.Fatalf("unexpected panic: %v", r)
			}
		}()
		_, _ = a.ArchiveThreads(ctx, []string{"thr_1"})
	}()
	disarm()

	m, present, err := store.ReadMarker(a.paths)
	if err != nil || !present || m.Op != store.OpArchival {
		t.Fatalf("marker at archival.postMarker: present=%v op=%q err=%v (want op=archival)", present, m.Op, err)
	}

	// A conflicting in-flight scope refuses a new batch (contract check).
	if _, err := a.ArchiveThreads(ctx, []string{"thr_1"}); err == nil ||
		!strings.Contains(err.Error(), "conflicting in-flight marker") {
		t.Fatalf("ArchiveThreads under a foreign marker = %v, want conflicting-marker refusal", err)
	}

	// Reconcile clears the archival scope (cell 8/10 family) and the
	// redrain completes cleanly, releasing the marker at the end.
	if _, err := a.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, present, err := store.ReadMarker(a.paths); err != nil || present {
		t.Fatalf("marker after Reconcile: present=%v err=%v", present, err)
	}
	res, err := a.ArchiveThreads(ctx, []string{"thr_1"})
	if err != nil {
		t.Fatalf("redrain ArchiveThreads: %v", err)
	}
	if len(res.Outcomes) != 1 || !(res.Outcomes[0].Archived || res.Outcomes[0].Skipped) {
		t.Fatalf("redrain outcomes = %+v", res.Outcomes)
	}
	if _, present, err := store.ReadMarker(a.paths); err != nil || present {
		t.Fatalf("marker after completed batch: present=%v err=%v", present, err)
	}
}

// TestConsolidate_SleepMarkerScope: Consolidate refuses a conflicting
// in-flight scope and leaves no marker behind on the normal path.
func TestConsolidate_SleepMarkerScope(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.Consolidate(ctx, "test"); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if _, present, err := store.ReadMarker(a.paths); err != nil || present {
		t.Fatalf("marker after Consolidate: present=%v err=%v", present, err)
	}

	// A foreign in-flight scope is refused — here the op=turn signal, a
	// non-empty journal (R3-addendum fold).
	if err := store.AppendJournal(a.paths, "t9", store.JournalPrompt, []byte("in flight")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	err := a.Consolidate(ctx, "test")
	if err == nil || !strings.Contains(err.Error(), "conflicting in-flight marker") {
		t.Fatalf("Consolidate under in-flight turn scope = %v, want conflicting-marker refusal", err)
	}
}
