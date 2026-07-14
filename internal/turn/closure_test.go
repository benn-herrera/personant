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

// fixedOutcomeResolver returns a ClosureResolver that always answers
// with the given outcome.
func fixedOutcomeResolver(o ClosureOutcome) ClosureResolver {
	return func(_ context.Context, _ ClosureOffer) (ClosureResolution, error) {
		return ClosureResolution{Outcome: o}, nil
	}
}

// seedClosureThread writes a thread + spine record with explicit
// state / last-engaged-turn / last-engaged-timestamp, for decay tests.
func seedClosureThread(t *testing.T, paths store.PersonantPaths, project, thrID string, state memops.ThreadState, lastEngagedTurn int, lastEngaged string) {
	t.Helper()
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	rec := memops.SpineRecord{
		ID:              thrID,
		Project:         project,
		Anchors:         anchors,
		Summary:         thrID,
		State:           state,
		Created:         "2026-04-01T00:00:00Z",
		LastEngaged:     lastEngaged,
		StateChanged:    "2026-04-01T00:00:00Z",
		TurnCount:       1,
		LastEngagedTurn: lastEngagedTurn,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine %s: %v", thrID, err)
	}
	// Seed history_symbols matching the anchors so the closure projection
	// (anchor-lifecycle Inc 2: closure stores a final authoritative
	// projection from history_symbols, not the curator draft) has a
	// deterministic canonical source. All count=1 / first_seen=1 / no
	// class flags → the projection orders them by Normalized ascending.
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
		{"turn-based fires at threshold", memops.ThreadActive, 2, 2 + decayTurns, recent, true},
		{"turn-based one short does not fire", memops.ThreadActive, 2, 2 + decayTurns - 1, recent, false},
		{"wall-clock fires past threshold", memops.ThreadActive, 0, 1, old, true},
		{"wall-clock recent does not fire", memops.ThreadActive, 0, 1, recent, false},
		{"session-reset guard: stored turn larger, recent clock", memops.ThreadActive, 100, 1, recent, false},
		{"session-reset guard: stored turn larger, old clock still fires via wall-clock", memops.ThreadActive, 100, 1, old, true},
		{"empty last-engaged does not error, no wall-clock signal", memops.ThreadActive, 2, 2 + decayTurns - 1, "", false},
		{"unparseable last-engaged does not error", memops.ThreadActive, 2, 2 + decayTurns - 1, "not-a-date", false},
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

// TestSurfaceClosure_DisabledWithoutCuratorOrResolver — closure stays a
// no-op when either the Curator or the ClosureResolver is nil.
func TestSurfaceClosure_DisabledWithoutCuratorOrResolver(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 0, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.TurnNumber = 100 // well past the decay threshold

	// No curator, no resolver → no-op.
	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates (both nil): %v", err)
	}

	// Curator only → still no-op.
	state.Curator = stubCurator{}
	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates (resolver nil): %v", err)
	}

	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), "retire.prompt") {
			t.Errorf("closure ran with detection disabled:\n%s", string(data))
		}
	}
}

// TestSurfaceClosure_RetireWritesStateAndEvicts — a decayed active
// thread resolved to ClosureResolved is rewritten with state=resolved,
// the curator's summary, and is evicted from both layers.
func TestSurfaceClosure_RetireWritesStateAndEvicts(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + decayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.DormantThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "the thread's gist", anchors: []string{"x", "y", "z", "w"}}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)

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
	// R2 (anchor-lifecycle Inc 2): closure stores a final AUTHORITATIVE
	// projection derived from history_symbols, NOT the curator's draft
	// anchors (x,y,z,w). The seeded history (alpha/beta/gamma/delta, all
	// count=1) projects in Normalized order. The curator draft remains the
	// user-facing closure OFFER display, not the stored spine anchors.
	if strings.Join(rec.Anchors, ",") != "alpha,beta,delta,gamma" {
		t.Errorf("spine anchors = %v, want projected from history_symbols", rec.Anchors)
	}
	if containsString(state.ActiveThreads, "thr_1") {
		t.Errorf("thr_1 retired but still in ActiveThreads: %v", state.ActiveThreads)
	}
	if containsString(state.DormantThreads, "thr_1") {
		t.Errorf("thr_1 retired but still in DormantThreads: %v", state.DormantThreads)
	}

	logBody := readDayLog(t, paths)
	for _, want := range []string{"retire.prompt thr=thr_1", "retire.complete thr=thr_1 resolution=resolved"} {
		if !strings.Contains(logBody, want) {
			t.Errorf("log missing %q\n%s", want, logBody)
		}
	}
}

// TestSurfaceClosure_WIPDemotes — ClosureWIP keeps the thread but
// moves it out of the active window into DormantThreads.
func TestSurfaceClosure_WIPDemotes(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + decayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = stubCurator{}
	state.ClosureResolver = fixedOutcomeResolver(ClosureWIP)

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}

	rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if rec.State != memops.ThreadWIP {
		t.Errorf("spine state = %q, want wip", rec.State)
	}
	if containsString(state.ActiveThreads, "thr_1") {
		t.Errorf("wip thread still in ActiveThreads: %v", state.ActiveThreads)
	}
	if !containsString(state.DormantThreads, "thr_1") {
		t.Errorf("wip thread not demoted to DormantThreads: %v", state.DormantThreads)
	}
	if !strings.Contains(readDayLog(t, paths), "retire.ack thr=thr_1 resolution=wip") {
		t.Errorf("expected retire.ack log line")
	}
}

// TestSurfaceClosure_DeferReArms — ClosureDefer writes no state and
// suppresses the re-prompt for decayTurns turns; once the grace
// expires the thread is offered again.
func TestSurfaceClosure_DeferReArms(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + decayTurns
	state.Curator = stubCurator{}

	prompts := 0
	state.ClosureResolver = func(_ context.Context, _ ClosureOffer) (ClosureResolution, error) {
		prompts++
		return ClosureResolution{Outcome: ClosureDefer}, nil
	}

	// Turn N: defer.
	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates (defer turn): %v", err)
	}
	rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if rec.State != memops.ThreadActive {
		t.Errorf("defer wrote state %q, want unchanged active", rec.State)
	}

	// Turn N+1: still inside the grace window → no re-prompt.
	state.TurnNumber++
	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates (suppressed turn): %v", err)
	}
	if prompts != 1 {
		t.Errorf("prompts within grace window = %d, want 1", prompts)
	}

	// Advance past the grace → the thread is offered again.
	state.TurnNumber += decayTurns
	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates (re-armed turn): %v", err)
	}
	if prompts != 2 {
		t.Errorf("prompts after grace expiry = %d, want 2", prompts)
	}

	if !strings.Contains(readDayLog(t, paths), "retire.defer thr=thr_1") {
		t.Errorf("expected retire.defer log line")
	}
}

// errCurator is a Curator whose DraftClosure always fails. Used to
// exercise the opportunistic-non-fatal contract: a curator error must
// be logged and swallowed, never abort the turn.
type errCurator struct{}

func (errCurator) DraftClosure(_ context.Context, _ memops.Thread) (curator.ClosureDraft, error) {
	return curator.ClosureDraft{}, errors.New("synthetic curator failure")
}

// TestSurfaceClosure_CrossProjectScanLeavesSiblingUntouched — the decay
// scan is active-project-scoped: an idle, decay-eligible active thread
// in a SIBLING project must not be touched (S1).
func TestSurfaceClosure_CrossProjectScanLeavesSiblingUntouched(t *testing.T) {
	paths, meta := newTestHome(t)
	// Seed a second project alongside the active one.
	other := memops.ProjectMeta{ID: "prj_2", Name: "beta", CurrentRootPath: paths.Home}
	if err := store.SaveProjectMeta(paths, other); err != nil {
		t.Fatalf("save prj_2 meta: %v", err)
	}
	// thr_1 is in the active project and decay-eligible; thr_2 is in the
	// sibling project and equally idle.
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")
	seedClosureThread(t, paths, other.ID, "thr_2", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + decayTurns
	state.Curator = stubCurator{}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}

	// thr_1 (active project) retired.
	rec1, found, err := state.Ops.FindThread(context.Background(), "thr_1")
	if err != nil || !found {
		t.Fatalf("FindThread thr_1: found=%v err=%v", found, err)
	}
	if rec1.State != memops.ThreadResolved {
		t.Errorf("thr_1 state = %q, want resolved", rec1.State)
	}
	// thr_2 (sibling project) untouched.
	rec2, found, err := state.Ops.FindThread(context.Background(), "thr_2")
	if err != nil || !found {
		t.Fatalf("FindThread thr_2: found=%v err=%v", found, err)
	}
	if rec2.State != memops.ThreadActive {
		t.Errorf("sibling-project thr_2 state = %q, want unchanged active", rec2.State)
	}
}

// TestClosureDeferPrunedOnEngage — a thread deferred for closure, then
// engaged on a later turn, must have its closureDeferUntil entry pruned
// so a stale grace cannot suppress a future legitimate re-prompt (S2).
func TestClosureDeferPrunedOnEngage(t *testing.T) {
	paths, meta := newTestHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))

	// Defer thr_1: applyClosureResolution with ClosureDefer arms the
	// suppression grace.
	if err := applyClosureResolution(context.Background(), state, "thr_1",
		curator.ClosureDraft{}, ClosureResolution{Outcome: ClosureDefer}); err != nil {
		t.Fatalf("applyClosureResolution defer: %v", err)
	}
	if _, deferred := state.closureDeferUntil["thr_1"]; !deferred {
		t.Fatalf("expected closureDeferUntil to contain thr_1 after defer")
	}

	// Engage thr_1 on a later turn — updateLayerLRU runs for every
	// engaged thread and must prune the stale defer entry.
	state.TurnNumber += 3
	updateLayerLRU(state, []string{"thr_1"})

	if _, deferred := state.closureDeferUntil["thr_1"]; deferred {
		t.Errorf("closureDeferUntil still contains thr_1 after engagement: %v", state.closureDeferUntil)
	}
}

// TestSurfaceClosure_CuratorErrorSwallowed — a curator that fails to
// draft a closure is logged under retire.curator-error and the scan
// completes without error (the opportunistic-non-fatal contract).
func TestSurfaceClosure_CuratorErrorSwallowed(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + decayTurns
	state.Curator = errCurator{}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates must swallow curator errors, got: %v", err)
	}

	// The thread is untouched — the curator failed before any verdict.
	rec, found, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if !found || rec.State != memops.ThreadActive {
		t.Errorf("thr_1 state = %q found=%v, want unchanged active", rec.State, found)
	}
	if !strings.Contains(readDayLog(t, paths), "retire.curator-error thr=thr_1") {
		t.Errorf("expected retire.curator-error log line\n%s", readDayLog(t, paths))
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
			if _, ok := decayEligible(rec, 1+decayTurns-1, sysRef); ok {
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
		if _, ok := decayEligible(recs[0], 1+decayTurns-1, sysRef); !ok {
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
		detail, ok := decayEligible(recs[0], 1+decayTurns, sysRef)
		if !ok {
			t.Fatalf("turn-count decay did not fire; want eligible")
		}
		if !strings.HasPrefix(detail, "turns=") {
			t.Errorf("detail = %q, want a turns= signal (turn-count branch)", detail)
		}
	})
}

// TestSurfaceClosure_VacationSurfacesNoPrompt is the end-to-end companion
// to TestDecayEligible_VacationSuppression: on resume after a whole-system
// absence, the decay scan must not surface a closure prompt for a thread
// idle only because the user was away (B5/PRT3-F3).
func TestSurfaceClosure_VacationSurfacesNoPrompt(t *testing.T) {
	paths, meta := newTestHome(t)
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	eightDaysAgo := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)

	// Two active threads, both last engaged ~8 days ago (the vacation).
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, eightDaysAgo)
	seedClosureThread(t, paths, meta.ID, "thr_2", memops.ThreadActive, 1, eightDaysAgo)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, now)
	// TurnNumber 1 mimics a fresh post-resume session: turn-count decay
	// cannot fire (1 < 1+decayTurns), so any prompt would be wall-clock.
	state.TurnNumber = 1
	state.Curator = stubCurator{}
	prompts := 0
	state.ClosureResolver = func(_ context.Context, _ ClosureOffer) (ClosureResolution, error) {
		prompts++
		return ClosureResolution{Outcome: ClosureDefer}, nil
	}

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}
	if prompts != 0 {
		t.Errorf("vacation resume surfaced %d closure prompts; want 0", prompts)
	}
	// A retire.prompt is logged for every eligible candidate before the
	// resolver runs, so any prompt would leave a retire.prompt line. Read
	// the logs dir directly (it may be empty — readDayLog requires exactly
	// one file).
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), "retire.prompt") {
			t.Errorf("vacation resume logged a retire.prompt\n%s", string(data))
		}
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

// TestApplyClosureResolution_EditedSummaryPropagates: a non-empty
// EditedSummary replaces the curator draft on the persisted record, and the
// §3.5 ack-quality retire.ack event records edited=yes (§2.8).
func TestApplyClosureResolution_EditedSummaryPropagates(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1

	draft := curator.ClosureDraft{Summary: "curator draft gist", Anchors: []string{"a"}}
	res := ClosureResolution{Outcome: ClosureResolved, EditedSummary: "human edited gist"}
	if err := applyClosureResolution(context.Background(), state, "thr_1", draft, res); err != nil {
		t.Fatalf("applyClosureResolution: %v", err)
	}

	rec, found, err := state.Ops.FindThread(context.Background(), "thr_1")
	if err != nil || !found {
		t.Fatalf("FindThread: found=%v err=%v", found, err)
	}
	if rec.Summary != "human edited gist" {
		t.Errorf("spine summary = %q, want the edited summary", rec.Summary)
	}
	log := readDayLog(t, paths)
	if !strings.Contains(log, "retire.ack thr=thr_1 resolution=resolved edited=yes") {
		t.Errorf("missing retire.ack edited=yes\n%s", log)
	}
	if !strings.Contains(log, "retire.complete thr=thr_1 resolution=resolved") {
		t.Errorf("missing retire.complete\n%s", log)
	}
}

// TestApplyClosureResolution_UneditedLogsAckNo: an accepted-as-drafted
// closure keeps the curator summary and logs retire.ack edited=no.
func TestApplyClosureResolution_UneditedLogsAckNo(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1

	draft := curator.ClosureDraft{Summary: "curator draft gist"}
	res := ClosureResolution{Outcome: ClosureDecided}
	if err := applyClosureResolution(context.Background(), state, "thr_1", draft, res); err != nil {
		t.Fatalf("applyClosureResolution: %v", err)
	}

	rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if rec.Summary != "curator draft gist" {
		t.Errorf("spine summary = %q, want the curator draft", rec.Summary)
	}
	if !strings.Contains(readDayLog(t, paths), "retire.ack thr=thr_1 resolution=decided edited=no") {
		t.Errorf("missing retire.ack edited=no\n%s", readDayLog(t, paths))
	}
}
