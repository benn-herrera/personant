package turn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// stubCurator is a deterministic Curator for the closure tests: a fixed
// summary and a fixed anchor set, no model round-trip.
type stubCurator struct {
	summary string
	anchors []string
}

func (s stubCurator) DraftClosure(_ context.Context, thread memops.Thread) (curator.ClosureDraft, error) {
	summary := s.summary
	if summary == "" {
		summary = "stub summary for " + thread.Meta.ID
	}
	anchors := s.anchors
	if anchors == nil {
		anchors = thread.Meta.Anchors
	}
	return curator.ClosureDraft{Summary: summary, Anchors: anchors}, nil
}

// errCurator is a Curator whose DraftClosure always fails.
type errCurator struct{}

func (errCurator) DraftClosure(_ context.Context, _ memops.Thread) (curator.ClosureDraft, error) {
	return curator.ClosureDraft{}, errors.New("synthetic curator failure")
}

// emptyDraftCurator drafts a blank summary — the model returned nothing
// usable, without erroring.
type emptyDraftCurator struct{}

func (emptyDraftCurator) DraftClosure(_ context.Context, _ memops.Thread) (curator.ClosureDraft, error) {
	return curator.ClosureDraft{Summary: "   "}, nil
}

// seedClosureThread writes a thread + spine record with explicit
// state / last-engaged-turn / last-engaged-timestamp, for decay tests,
// with four anchors and one engaged turn.
func seedClosureThread(t *testing.T, paths store.PersonantPaths, project, thrID string, state memops.ThreadState, lastEngagedTurn int, lastEngaged string) {
	t.Helper()
	seedClosureThreadShape(t, paths, project, thrID, state, lastEngagedTurn, lastEngaged,
		[]string{"alpha", "beta", "gamma", "delta"}, 1)
}

// seedClosureThreadShape is seedClosureThread with the anchor set and the
// engaged-turn count under the test's control.
func seedClosureThreadShape(t *testing.T, paths store.PersonantPaths, project, thrID string, state memops.ThreadState, lastEngagedTurn int, lastEngaged string, anchors []string, turnCount int) {
	t.Helper()
	rec := memops.SpineRecord{
		ID:              thrID,
		Project:         project,
		Anchors:         anchors,
		Summary:         thrID,
		State:           state,
		Created:         "2026-04-01T00:00:00Z",
		LastEngaged:     lastEngaged,
		StateChanged:    "2026-04-01T00:00:00Z",
		TurnCount:       turnCount,
		LastEngagedTurn: lastEngagedTurn,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", thrID, err)
	}
	// Seed history_symbols matching the anchors so the closure projection
	// (closure stores a final authoritative projection from
	// history_symbols, not the curator draft) has a deterministic canonical
	// source. All count=1 / first_seen=1 / no class flags → the projection
	// orders them by Normalized ascending.
	hist := make([]memops.HistorySymbol, len(anchors))
	for i, a := range anchors {
		hist[i] = memops.HistorySymbol{Raw: a, Normalized: a, FirstSeenTurn: 1, Count: 1, Source: memops.SourceModel}
	}
	thr := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:              rec.ID,
			Project:         rec.Project,
			Anchors:         rec.Anchors,
			Summary:         rec.Summary,
			State:           rec.State,
			Created:         rec.Created,
			LastEngaged:     rec.LastEngaged,
			StateChanged:    rec.StateChanged,
			TurnCount:       rec.TurnCount,
			LastEngagedTurn: rec.LastEngagedTurn,
			HistorySymbols:  hist,
		},
		Body: "# " + thrID + "\n\n## Turn 1 · 2026-04-01T00:00:00Z · [" +
			strings.Join(anchors, ", ") + "]\n\n**user:** seed\n\n**agent:** seed reply\n",
	}
	if err := store.SeedThread(paths, thr); err != nil {
		t.Fatalf("seed thread %s: %v", thrID, err)
	}
}

// allLogs returns every day log concatenated; unlike readDayLog it
// accepts an empty logs directory.
func allLogs(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

// TestDecayEligible exercises the decay predicate directly: turn-based
// firing at the threshold, wall-clock firing, the session-reset guard,
// and the active-only restriction.
func TestDecayEligible(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	old := now.Add(-decayTime - time.Hour).Format(time.RFC3339)

	tests := []struct {
		name       string
		state      memops.ThreadState
		lastTurn   int
		turnNumber int
		engaged    string
		want       bool
	}{
		{"turn-based fires at threshold", memops.ThreadActive, 2, 2 + DecayTurns, recent, true},
		{"turn-based one short does not fire", memops.ThreadActive, 2, 2 + DecayTurns - 1, recent, false},
		{"wall-clock fires past threshold", memops.ThreadActive, 0, 1, old, true},
		{"wall-clock recent does not fire", memops.ThreadActive, 0, 1, recent, false},
		{"session-reset guard: stored turn larger, recent clock", memops.ThreadActive, 100, 1, recent, false},
		{"session-reset guard: stored turn larger, old clock still fires via wall-clock", memops.ThreadActive, 100, 1, old, true},
		{"empty last-engaged does not error, no wall-clock signal", memops.ThreadActive, 2, 2 + DecayTurns - 1, "", false},
		{"unparseable last-engaged does not error", memops.ThreadActive, 2, 2 + DecayTurns - 1, "not-a-date", false},
		{"wip thread never decay-prompts", memops.ThreadWIP, 0, 100, old, false},
		{"paused thread never decay-prompts", memops.ThreadPaused, 0, 100, old, false},
		{"resolved thread never decay-prompts", memops.ThreadResolved, 0, 100, old, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := memops.SpineRecord{
				State:           tt.state,
				LastEngagedTurn: tt.lastTurn,
				LastEngaged:     tt.engaged,
			}
			_, got := decayEligible(rec, tt.turnNumber, now)
			if got != tt.want {
				t.Errorf("decayEligible() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClosure_NothingClosesWithoutCurator — with no curator installed the
// scan is a no-op: no retire.prompt, and the decayed thread stays active.
func TestClosure_NothingClosesWithoutCurator(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 0, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil, nil))
	state.TurnNumber = 100 // well past the decay threshold

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}

	rec, found, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if !found || rec.State != memops.ThreadActive {
		t.Errorf("thr_1 state = %q found=%v, want unchanged active", rec.State, found)
	}
	if log := allLogs(t, paths); strings.Contains(log, "retire.prompt") {
		t.Errorf("closure ran without a curator:\n%s", log)
	}
}

// TestClosure_DecayedThreadClosesResolved — with only a curator installed,
// a decayed active thread of any shape (thin, anchor-rich, long-engaged)
// is rewritten with state=resolved and the curator's summary, stores
// anchors projected from history_symbols, and leaves both layers.
func TestClosure_DecayedThreadClosesResolved(t *testing.T) {
	tests := []struct {
		name      string
		anchors   []string
		turnCount int
		want      string // projected anchors
	}{
		{"thin", []string{"alpha", "beta", "gamma", "delta"}, 1, "alpha,beta,delta,gamma"},
		{"anchor-rich", []string{"a", "b", "c", "d", "e", "f"}, 1, "a,b,c,d,e,f"},
		{"long-engaged", []string{"a", "b"}, 15, "a,b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, meta := newTestHome(t)
			seedClosureThreadShape(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "",
				tt.anchors, tt.turnCount)

			state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil, nil))
			pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
			state.TurnNumber = 1 + DecayTurns
			state.ActiveThreads = []string{"thr_1"}
			state.DormantThreads = []string{"thr_1"}
			state.Curator = stubCurator{summary: "the thread's gist", anchors: []string{"x", "y", "z", "w"}}

			if err := surfaceClosureCandidates(context.Background(), state); err != nil {
				t.Fatalf("surfaceClosureCandidates: %v", err)
			}

			rec, found, err := state.Ops.FindThread(context.Background(), "thr_1")
			if err != nil || !found {
				t.Fatalf("FindThread thr_1: found=%v err=%v", found, err)
			}
			if rec.State != memops.ThreadResolved {
				t.Errorf("spine state = %q, want resolved", rec.State)
			}
			if rec.Summary != "the thread's gist" {
				t.Errorf("spine summary = %q, want curator draft", rec.Summary)
			}
			if got := strings.Join(rec.Anchors, ","); got != tt.want {
				t.Errorf("spine anchors = %s, want %s (projected from history_symbols, not the draft)", got, tt.want)
			}
			if containsString(state.ActiveThreads, "thr_1") || containsString(state.DormantThreads, "thr_1") {
				t.Errorf("closed thread still in the working set: B=%v C=%v",
					state.ActiveThreads, state.DormantThreads)
			}

			log := readDayLog(t, paths)
			for _, want := range []string{"retire.prompt thr=thr_1 inactivity=", "retire.complete thr=thr_1 resolution=resolved"} {
				if !strings.Contains(log, want) {
					t.Errorf("log missing %q\n%s", want, log)
				}
			}
		})
	}
}

// TestClosure_EmptyDraftIsRetried — a draft that comes back empty or
// fails writes nothing and logs retire.curator-error; the thread stays
// decay-eligible, and the next scan with a usable draft closes it.
func TestClosure_EmptyDraftIsRetried(t *testing.T) {
	tests := []struct {
		name  string
		first curator.Curator
	}{
		{"empty draft", emptyDraftCurator{}},
		{"curator error", errCurator{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, meta := newTestHome(t)
			seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

			state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil, nil))
			pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
			state.TurnNumber = 1 + DecayTurns
			state.ActiveThreads = []string{"thr_1"}
			state.Curator = tt.first

			if err := surfaceClosureCandidates(context.Background(), state); err != nil {
				t.Fatalf("surfaceClosureCandidates must swallow draft failures, got: %v", err)
			}
			rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
			if rec.State != memops.ThreadActive {
				t.Errorf("thread state after failed draft = %q, want unchanged active", rec.State)
			}
			if !containsString(state.ActiveThreads, "thr_1") {
				t.Errorf("thread evicted after failed draft: %v", state.ActiveThreads)
			}
			log := readDayLog(t, paths)
			if !strings.Contains(log, "retire.curator-error thr=thr_1") {
				t.Errorf("log missing retire.curator-error\n%s", log)
			}
			if strings.Contains(log, "retire.complete") {
				t.Errorf("failed draft closed the thread\n%s", log)
			}

			state.TurnNumber++
			state.Curator = stubCurator{summary: "retried gist"}
			if err := surfaceClosureCandidates(context.Background(), state); err != nil {
				t.Fatalf("surfaceClosureCandidates (retry): %v", err)
			}
			rec, _, _ = state.Ops.FindThread(context.Background(), "thr_1")
			if rec.State != memops.ThreadResolved || rec.Summary != "retried gist" {
				t.Errorf("after retry: state=%q summary=%q, want resolved with the retried draft", rec.State, rec.Summary)
			}
		})
	}
}

// TestClosure_CrossProjectScanLeavesSiblingUntouched — the decay scan is
// active-project-scoped: an idle, decay-eligible active thread in a
// SIBLING project must not be touched (S1).
func TestClosure_CrossProjectScanLeavesSiblingUntouched(t *testing.T) {
	paths, meta := newTestHome(t)
	other := memops.ProjectMeta{ID: "prj_2", Name: "beta", CurrentRootPath: paths.Home}
	if err := store.SaveProjectMeta(paths, other); err != nil {
		t.Fatalf("save prj_2 meta: %v", err)
	}
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")
	seedClosureThread(t, paths, other.ID, "thr_2", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + DecayTurns
	state.Curator = stubCurator{}

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}

	rec1, found, err := state.Ops.FindThread(context.Background(), "thr_1")
	if err != nil || !found {
		t.Fatalf("FindThread thr_1: found=%v err=%v", found, err)
	}
	if rec1.State != memops.ThreadResolved {
		t.Errorf("thr_1 state = %q, want resolved", rec1.State)
	}
	rec2, found, err := state.Ops.FindThread(context.Background(), "thr_2")
	if err != nil || !found {
		t.Fatalf("FindThread thr_2: found=%v err=%v", found, err)
	}
	if rec2.State != memops.ThreadActive {
		t.Errorf("sibling-project thr_2 state = %q, want unchanged active", rec2.State)
	}
}

// TestDecayEligible_VacationSuppression covers the B5/PRT3-F3 fix: wall-
// clock decay must measure neglect relative to the system's most-recent
// activity (systemReference), not raw calendar time, so a whole-system
// absence does not make every active thread decay-eligible at once.
//
//	(a) idle 8d while the SYSTEM was also idle ~8d (vacation) → no fire.
//	(b) idle 8d while the system was actively used on another thread in
//	    that window → fire (genuine neglect).
//	(c) turn-count decay fires independently of (a)/(b).
func TestDecayEligible_VacationSuppression(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	eightDaysAgo := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	oneHourAgo := now.Add(-time.Hour).Format(time.RFC3339)

	// (a) Vacation: every thread's last_engaged is ~8 days ago, so the
	// system reference is also ~8 days ago — neglect relative to it is ~0.
	t.Run("vacation: whole-system absence does not fire", func(t *testing.T) {
		recs := []memops.SpineRecord{
			{ID: "thr_1", State: memops.ThreadActive, LastEngaged: eightDaysAgo, LastEngagedTurn: 1},
			{ID: "thr_2", State: memops.ThreadActive, LastEngaged: eightDaysAgo, LastEngagedTurn: 1},
		}
		sysRef := systemReference(recs, now)
		for _, rec := range recs {
			// turnNumber kept just under the turn threshold so only the
			// wall-clock branch can fire — this isolates the fix.
			if _, ok := decayEligible(rec, 1+DecayTurns-1, sysRef); ok {
				t.Errorf("%s became decay-eligible after a whole-system absence; want suppressed", rec.ID)
			}
		}
	})

	// (b) Genuine neglect: thr_old has been idle 8 days, but thr_active was
	// engaged an hour ago, so the system was in active use that whole
	// window. thr_old's neglect relative to that use is ~8 days → fires.
	t.Run("genuine neglect during active use fires", func(t *testing.T) {
		recs := []memops.SpineRecord{
			{ID: "thr_old", State: memops.ThreadActive, LastEngaged: eightDaysAgo, LastEngagedTurn: 1},
			{ID: "thr_active", State: memops.ThreadActive, LastEngaged: oneHourAgo, LastEngagedTurn: 50},
		}
		sysRef := systemReference(recs, now)
		if _, ok := decayEligible(recs[0], 1+DecayTurns-1, sysRef); !ok {
			t.Errorf("thr_old neglected during active use did not fire; want decay-eligible")
		}
		// The recently-engaged thread is not eligible on either signal.
		if _, ok := decayEligible(recs[1], 50, sysRef); ok {
			t.Errorf("thr_active engaged an hour ago became decay-eligible; want not")
		}
	})

	// (c) Turn-count decay is independent of the wall-clock suppression:
	// even in the vacation arrangement (sysRef ~8d ago, wall-clock
	// suppressed), a thread that is turn-idle past the threshold fires.
	t.Run("turn-count decay fires independently of wall-clock", func(t *testing.T) {
		recs := []memops.SpineRecord{
			{ID: "thr_1", State: memops.ThreadActive, LastEngaged: eightDaysAgo, LastEngagedTurn: 1},
		}
		sysRef := systemReference(recs, now)
		detail, ok := decayEligible(recs[0], 1+DecayTurns, sysRef)
		if !ok {
			t.Fatalf("turn-count decay did not fire; want eligible")
		}
		if !strings.HasPrefix(detail, "turns=") {
			t.Errorf("detail = %q, want a turns= signal (turn-count branch)", detail)
		}
	})
}

// TestClosure_VacationClosesNothing is the end-to-end companion to
// TestDecayEligible_VacationSuppression: on resume after a whole-system
// absence, the decay scan must not close a thread idle only because the
// user was away (B5/PRT3-F3).
func TestClosure_VacationClosesNothing(t *testing.T) {
	paths, meta := newTestHome(t)
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	eightDaysAgo := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)

	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, eightDaysAgo)
	seedClosureThread(t, paths, meta.ID, "thr_2", memops.ThreadActive, 1, eightDaysAgo)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, model.NewScriptedMock(nil, nil))
	pinClock(t, now)
	// TurnNumber 1 mimics a fresh post-resume session: turn-count decay
	// cannot fire (1 < 1+DecayTurns), so any close would be wall-clock.
	state.TurnNumber = 1
	state.Curator = stubCurator{}

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}
	for _, id := range []string{"thr_1", "thr_2"} {
		rec, _, _ := state.Ops.FindThread(context.Background(), id)
		if rec.State != memops.ThreadActive {
			t.Errorf("%s state after vacation resume = %q, want active", id, rec.State)
		}
	}
	if log := allLogs(t, paths); strings.Contains(log, "retire.prompt") {
		t.Errorf("vacation resume logged a retire.prompt\n%s", log)
	}
}

// containsString reports whether ss contains v.
func containsString(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
