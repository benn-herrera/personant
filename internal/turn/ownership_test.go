package turn

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// seedActiveThread writes a spine record + thread file for thrID with the
// given turn count and anchors, and returns the record. Helper for the
// ownership tests, which need pre-existing threads the model can re-tag.
func seedActiveThread(t *testing.T, paths store.PersonantPaths, project, thrID string, turnCount int, anchors []string) {
	t.Helper()
	rec := memops.SpineRecord{
		ID:           thrID,
		Project:      project,
		Anchors:      anchors,
		Summary:      thrID + " summary",
		State:        memops.ThreadActive,
		Created:      "2026-04-01T00:00:00Z",
		LastEngaged:  "2026-04-01T00:00:00Z",
		StateChanged: "2026-04-01T00:00:00Z",
		TurnCount:    turnCount,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", thrID, err)
	}
	thr := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:           rec.ID,
			Project:      rec.Project,
			Anchors:      rec.Anchors,
			Summary:      rec.Summary,
			State:        rec.State,
			Created:      rec.Created,
			LastEngaged:  rec.LastEngaged,
			StateChanged: rec.StateChanged,
			TurnCount:    rec.TurnCount,
		},
		Body: "# " + thrID + "\n\n## Turn 1 · 2026-04-01T00:00:00Z · [" +
			anchors[0] + "]\n\n**user:** seed\n\n**agent:** seed reply\n",
	}
	if err := store.SeedThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// turnFileCount returns the number of turn-excerpt files in a thread's
// turns/ directory — the count of turns whose content the thread owns.
func turnFileCount(t *testing.T, paths store.PersonantPaths, thrID string) int {
	t.Helper()
	body, err := store.LoadThread(paths, thrID)
	if err != nil {
		t.Fatalf("LoadThread %s: %v", thrID, err)
	}
	return countTurnHeaders(body.Body)
}

func countTurnHeaders(body string) int {
	n := 0
	for i := 0; i+7 <= len(body); i++ {
		if body[i:i+7] == "## Turn" {
			n++
		}
	}
	return n
}

// TestMixedTagExistingOwnsNewMetaOnly — a [thr_1, *new-topic*] tag: the
// existing thr_1 owns (gets the excerpt + turn_count++); the new thread
// is created metadata-only (no excerpt, TurnCount 0) with a non-empty
// Description; the turn is NOT duplicated.
func TestMixedTagExistingOwnsNewMetaOnly(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, meta.ID, "thr_1", 3, anchors)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1, *new-topic* [alpha, beta, gamma, delta]*\nMixed reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_1"} // suppress §5.5 mid-turn fetch
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "owner-and-new", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// thr_1 owns: turn_count 3 → 4, exactly one new excerpt appended.
	owner, found, err := store.FindSpineRecord(paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("find thr_1: %v found=%v", err, found)
	}
	if owner.TurnCount != 4 {
		t.Errorf("owner turn_count: got %d want 4", owner.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 2 {
		t.Errorf("owner turn files: got %d want 2 (seed + this turn)", got)
	}

	// The new thread (thr_2) is metadata-only: TurnCount 0, no excerpt,
	// non-empty Description.
	newRec, found, err := store.FindSpineRecord(paths, "thr_2")
	if err != nil || !found {
		t.Fatalf("find thr_2: %v found=%v", err, found)
	}
	if newRec.TurnCount != 0 {
		t.Errorf("new-thread turn_count: got %d want 0 (metadata-only)", newRec.TurnCount)
	}
	if newRec.Description == "" {
		t.Errorf("new-thread Description empty; want the triggering utterance")
	}
	if got := turnFileCount(t, paths, "thr_2"); got != 0 {
		t.Errorf("new-thread turn files: got %d want 0 (owns no turn)", got)
	}
}

// TestMultiExistingFirstOwnsRestEngaged — a [thr_1, thr_2] tag: thr_1
// (lowest id, the owner) gets the excerpt + turn_count++; thr_2 gets
// recency + history_symbols but NO excerpt and NO turn_count++.
func TestMultiExistingFirstOwnsRestEngaged(t *testing.T) {
	paths, meta := newTestHome(t)
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	seedActiveThread(t, paths, meta.ID, "thr_1", 5, anchors)
	seedActiveThread(t, paths, meta.ID, "thr_2", 9, anchors)

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: thr_1, thr_2 [alpha, beta, gamma, delta]*\nMulti reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_1", "thr_2"} // suppress mid-turn fetch
	pinClock(t, now)

	if _, err := Run(context.Background(), state, "multi", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Owner thr_1: turn_count 5 → 6, one new excerpt (seed + this turn).
	owner, _, _ := store.FindSpineRecord(paths, "thr_1")
	if owner.TurnCount != 6 {
		t.Errorf("owner thr_1 turn_count: got %d want 6", owner.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 2 {
		t.Errorf("owner thr_1 turn files: got %d want 2", got)
	}

	// Engaged-non-owner thr_2: turn_count unchanged at 9, no new excerpt,
	// but recency updated.
	eng, _, _ := store.FindSpineRecord(paths, "thr_2")
	if eng.TurnCount != 9 {
		t.Errorf("engaged thr_2 turn_count: got %d want 9 (no turn added)", eng.TurnCount)
	}
	if got := turnFileCount(t, paths, "thr_2"); got != 1 {
		t.Errorf("engaged thr_2 turn files: got %d want 1 (seed only; no new excerpt)", got)
	}
	if eng.LastEngaged != now.Format(time.RFC3339) {
		t.Errorf("engaged thr_2 last_engaged: got %q want %q", eng.LastEngaged, now.Format(time.RFC3339))
	}
	// history_symbols merged onto thr_2 (the anchors appear).
	thr2, err := store.LoadThread(paths, "thr_2")
	if err != nil {
		t.Fatalf("load thr_2: %v", err)
	}
	got := map[string]struct{}{}
	for _, h := range thr2.Meta.HistorySymbols {
		got[h.Normalized] = struct{}{}
	}
	for _, want := range anchors {
		if _, ok := got[want]; !ok {
			t.Errorf("engaged thr_2 history_symbols missing %q: got %v", want, got)
		}
	}
}

// TestPureNewTopicOwnsTurn — a pure *new-topic* tag: the new thread owns
// the turn (excerpt + TurnCount 1) and has a non-empty Description.
func TestPureNewTopicOwnsTurn(t *testing.T) {
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nGenesis reply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if _, err := Run(context.Background(), state, "brand new topic", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec, found, err := store.FindSpineRecord(paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("find thr_1: %v found=%v", err, found)
	}
	if rec.TurnCount != 1 {
		t.Errorf("new-owner turn_count: got %d want 1", rec.TurnCount)
	}
	if rec.Description == "" {
		t.Errorf("new-owner Description empty; want the triggering utterance")
	}
	if got := turnFileCount(t, paths, "thr_1"); got != 1 {
		t.Errorf("new-owner turn files: got %d want 1 (owns this turn)", got)
	}
}

// TestOwnerGuardRejectsSecondClaim — a second excerpt-write for one turn
// returns ErrTurnAlreadyOwned. The guard is a structural backstop against
// a future double-write bug; this drives it directly.
func TestOwnerGuardRejectsSecondClaim(t *testing.T) {
	state := &State{}
	if err := claimTurnOwner(state, "thr_1"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// Idempotent re-claim by the same owner is allowed (recovery re-entry).
	if err := claimTurnOwner(state, "thr_1"); err != nil {
		t.Fatalf("idempotent re-claim by same owner: %v", err)
	}
	// A different thread claiming the same turn is rejected.
	err := claimTurnOwner(state, "thr_2")
	if !errors.Is(err, ErrTurnAlreadyOwned) {
		t.Fatalf("second claim by different thread: got %v want ErrTurnAlreadyOwned", err)
	}
}

// TestDescriptionRoundTrips — Description set at creation persists through
// the file adapter on both the spine record and the thread frontmatter.
func TestDescriptionRoundTrips(t *testing.T) {
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nReply."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	const utterance = "tell me about the trefoil knot"
	if _, err := Run(context.Background(), state, utterance, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec, found, err := store.FindSpineRecord(paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("find: %v found=%v", err, found)
	}
	if rec.Description != utterance {
		t.Errorf("spine Description: got %q want %q", rec.Description, utterance)
	}
	thr, err := store.LoadThread(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Meta.Description != utterance {
		t.Errorf("frontmatter Description: got %q want %q", thr.Meta.Description, utterance)
	}
}
