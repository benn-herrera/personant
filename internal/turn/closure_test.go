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
// The seeded shape is deliberately ROUTINE under the §3.5 auto-accept
// criteria (4 anchors, 1 engaged turn — both under the exception
// thresholds); seedClosureThreadShape is the variant for exception cases.
func seedClosureThread(t *testing.T, paths store.PersonantPaths, project, thrID string, state memops.ThreadState, lastEngagedTurn int, lastEngaged string) {
	t.Helper()
	seedClosureThreadShape(t, paths, project, thrID, state, lastEngagedTurn, lastEngaged,
		[]string{"alpha", "beta", "gamma", "delta"}, 1)
}

// seedClosureThreadShape is seedClosureThread with the two §3.5
// classification inputs — the anchor set and the engaged-turn count —
// under the test's control.
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
	state.TurnNumber = 1 + DecayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.DormantThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "the thread's gist", anchors: []string{"x", "y", "z", "w"}}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)
	// Pinned to the pre-2026-08-04 interactive flow: this test is about
	// what the RESOLVER's verdict does. Auto-accept has its own tests.
	state.ClosureAckMode = AckModeAlways

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
	state.TurnNumber = 1 + DecayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = stubCurator{}
	state.ClosureResolver = fixedOutcomeResolver(ClosureWIP)
	// Pinned to the pre-2026-08-04 interactive flow: this test is about
	// what the RESOLVER's verdict does. Auto-accept has its own tests.
	state.ClosureAckMode = AckModeAlways

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
// suppresses the re-prompt for DecayTurns turns; once the grace
// expires the thread is offered again.
func TestSurfaceClosure_DeferReArms(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + DecayTurns
	state.Curator = stubCurator{}

	prompts := 0
	state.ClosureResolver = func(_ context.Context, _ ClosureOffer) (ClosureResolution, error) {
		prompts++
		return ClosureResolution{Outcome: ClosureDefer}, nil
	}
	// Defer is a verdict only a human gives, so this is the always-mode
	// flow. The grace it arms is honored by BOTH modes (decayCandidates).
	state.ClosureAckMode = AckModeAlways

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
	state.TurnNumber += DecayTurns
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
	state.TurnNumber = 1 + DecayTurns
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
		curator.ClosureDraft{}, ClosureResolution{Outcome: ClosureDefer}, ackHuman); err != nil {
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
	state.TurnNumber = 1 + DecayTurns
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
	// cannot fire (1 < 1+DecayTurns), so any prompt would be wall-clock.
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
	if err := applyClosureResolution(context.Background(), state, "thr_1", draft, res, ackHuman); err != nil {
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
	if err := applyClosureResolution(context.Background(), state, "thr_1", draft, res, ackHuman); err != nil {
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

// ---------------------------------------------------------------------
// §3.5 routine/exception split (user ruling 2026-08-04)
// ---------------------------------------------------------------------

// failingResolver fails the test if the closure resolver is consulted at
// all — the auto-accept path must never reach the user.
func failingResolver(t *testing.T) ClosureResolver {
	t.Helper()
	return func(_ context.Context, offer ClosureOffer) (ClosureResolution, error) {
		t.Errorf("closure resolver was consulted for %s; auto mode must not prompt", offer.ThreadID)
		return ClosureResolution{Outcome: ClosureDefer}, nil
	}
}

// TestClosureException covers the classification predicate directly: the
// two exception signals at and either side of their thresholds.
func TestClosureException(t *testing.T) {
	anchors := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "a"
		}
		return out
	}
	tests := []struct {
		name      string
		anchors   int
		turnCount int
		want      bool
	}{
		{"thin thread is routine", 3, 2, false},
		{"one anchor short of the bar is routine", exceptionAnchors - 1, 1, false},
		{"anchor-rich is an exception", exceptionAnchors, 1, true},
		{"one turn short of the bar is routine", 1, exceptionTurns - 1, false},
		{"long-engaged is an exception", 1, exceptionTurns, true},
		{"zero-anchor vague thread is routine", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := closureException(memops.SpineRecord{
				Anchors:   anchors(tt.anchors),
				TurnCount: tt.turnCount,
			})
			if got != tt.want {
				t.Errorf("closureException() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseClosureAckMode: the directive value maps to a mode, and an
// unrecognized value is refused rather than guessed at.
func TestParseClosureAckMode(t *testing.T) {
	tests := []struct {
		in   string
		want ClosureAckMode
		ok   bool
	}{
		{"auto", AckModeAuto, true},
		{"always", AckModeAlways, true},
		{" ALWAYS ", AckModeAlways, true},
		{"", "", false},
		{"never", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseClosureAckMode(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseClosureAckMode(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// TestSurfaceClosure_AutoAcceptsRoutine — the default mode retires a
// routine decayed thread with the curator's summary, without consulting
// the resolver, logs ack=auto, and reports it through OnAutoClosed.
func TestSurfaceClosure_AutoAcceptsRoutine(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + DecayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.DormantThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "the thread's gist"}
	state.ClosureResolver = failingResolver(t)

	var notices []ClosureNotice
	state.OnAutoClosed = func(n ClosureNotice) { notices = append(notices, n) }

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
		t.Errorf("spine summary = %q, want the curator draft", rec.Summary)
	}
	if containsString(state.ActiveThreads, "thr_1") || containsString(state.DormantThreads, "thr_1") {
		t.Errorf("auto-closed thread still in the working set: B=%v C=%v",
			state.ActiveThreads, state.DormantThreads)
	}
	if len(notices) != 1 || notices[0].ThreadID != "thr_1" || notices[0].Summary != "the thread's gist" {
		t.Errorf("OnAutoClosed notices = %+v, want one for thr_1 carrying the summary", notices)
	}

	log := readDayLog(t, paths)
	for _, want := range []string{
		"retire.ack thr=thr_1 resolution=resolved edited=no ack=auto",
		"retire.complete thr=thr_1 resolution=resolved",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n%s", want, log)
		}
	}
}

// TestSurfaceClosure_QueuesExceptions — an anchor-rich or long-engaged
// decayed thread is left untouched and queued: no curator call, no
// prompt, one retire.pending line, and it shows up in PendingClosures.
func TestSurfaceClosure_QueuesExceptions(t *testing.T) {
	tests := []struct {
		name       string
		anchors    []string
		turnCount  int
		wantReason string
	}{
		{"anchor-rich", []string{"a", "b", "c", "d", "e", "f"}, 1, "reason=anchors=6"},
		{"long-engaged", []string{"a", "b"}, exceptionTurns, "reason=turns=15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, meta := newTestHome(t)
			seedClosureThreadShape(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "",
				tt.anchors, tt.turnCount)

			state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
			pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
			state.TurnNumber = 1 + DecayTurns
			state.ActiveThreads = []string{"thr_1"}
			state.Curator = stubCurator{}
			state.ClosureResolver = failingResolver(t)
			state.OnAutoClosed = func(n ClosureNotice) {
				t.Errorf("queued closure was auto-accepted: %+v", n)
			}

			if err := surfaceClosureCandidates(context.Background(), state); err != nil {
				t.Fatalf("surfaceClosureCandidates: %v", err)
			}

			rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
			if rec.State != memops.ThreadActive {
				t.Errorf("queued thread state = %q, want unchanged active", rec.State)
			}
			if !containsString(state.ActiveThreads, "thr_1") {
				t.Errorf("queued thread was evicted from Layer B: %v", state.ActiveThreads)
			}
			// A second scan while the thread is still queued must NOT
			// re-announce it: retire.pending is a decision event, not a
			// per-turn heartbeat.
			state.TurnNumber++
			if err := surfaceClosureCandidates(context.Background(), state); err != nil {
				t.Fatalf("surfaceClosureCandidates (second scan): %v", err)
			}

			log := readDayLog(t, paths)
			if !strings.Contains(log, tt.wantReason) {
				t.Errorf("log missing retire.pending with %s\n%s", tt.wantReason, log)
			}
			if n := strings.Count(log, "retire.pending thr=thr_1"); n != 1 {
				t.Errorf("retire.pending logged %d times over two scans, want 1\n%s", n, log)
			}
			if strings.Contains(log, "retire.ack") {
				t.Errorf("queued thread was acked\n%s", log)
			}

			pending, err := PendingClosures(context.Background(), state)
			if err != nil {
				t.Fatalf("PendingClosures: %v", err)
			}
			if len(pending) != 1 || pending[0] != "thr_1" {
				t.Errorf("PendingClosures = %v, want [thr_1]", pending)
			}
		})
	}
}

// TestPendingClosures_EmptyUnderAlwaysMode — always mode offers every
// decayed thread in-session, so nothing can be queued for a drain.
func TestPendingClosures_EmptyUnderAlwaysMode(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThreadShape(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "",
		[]string{"a", "b", "c", "d", "e", "f"}, 1)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + DecayTurns
	state.Curator = stubCurator{}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)
	state.ClosureAckMode = AckModeAlways

	pending, err := PendingClosures(context.Background(), state)
	if err != nil {
		t.Fatalf("PendingClosures: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingClosures under always mode = %v, want none", pending)
	}
}

// TestDrainClosures_ResolvesQueueInteractively — the boundary drain puts
// each queued thread to the resolver and applies the verdict as a HUMAN
// ack (ack=human), leaving the queue empty.
func TestDrainClosures_ResolvesQueueInteractively(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThreadShape(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "",
		[]string{"a", "b", "c", "d", "e", "f"}, 1)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + DecayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "queued gist"}
	state.ClosureResolver = fixedOutcomeResolver(ClosureDecided)

	n, err := DrainClosures(context.Background(), state)
	if err != nil {
		t.Fatalf("DrainClosures: %v", err)
	}
	if n != 1 {
		t.Errorf("DrainClosures closed %d, want 1", n)
	}

	rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if rec.State != memops.ThreadDecided {
		t.Errorf("drained thread state = %q, want decided", rec.State)
	}
	if containsString(state.ActiveThreads, "thr_1") {
		t.Errorf("drained thread still in Layer B: %v", state.ActiveThreads)
	}
	log := readDayLog(t, paths)
	for _, want := range []string{
		"retire.prompt thr=thr_1 trigger=queued",
		"retire.ack thr=thr_1 resolution=decided edited=no ack=human",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n%s", want, log)
		}
	}

	pending, err := PendingClosures(context.Background(), state)
	if err != nil {
		t.Fatalf("PendingClosures after drain: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("queue not empty after drain: %v", pending)
	}
}

// TestAutoAccept_EmptyDraftIsNotAccepted — an empty curator summary is
// the one case a human must see: nothing is written, and the thread stays
// decay-eligible for the next scan.
func TestAutoAccept_EmptyDraftIsNotAccepted(t *testing.T) {
	paths, meta := newTestHome(t)
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state.TurnNumber = 1 + DecayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = emptyDraftCurator{}
	state.ClosureResolver = failingResolver(t)

	if err := surfaceClosureCandidates(context.Background(), state); err != nil {
		t.Fatalf("surfaceClosureCandidates: %v", err)
	}

	rec, _, _ := state.Ops.FindThread(context.Background(), "thr_1")
	if rec.State != memops.ThreadActive {
		t.Errorf("thread state = %q, want unchanged active", rec.State)
	}
	log := readDayLog(t, paths)
	if !strings.Contains(log, "retire.curator-error thr=thr_1") {
		t.Errorf("log missing retire.curator-error\n%s", log)
	}
	if strings.Contains(log, "retire.ack") {
		t.Errorf("empty draft was acked\n%s", log)
	}
}

// emptyDraftCurator drafts a blank summary — the model returned nothing
// usable, without erroring.
type emptyDraftCurator struct{}

func (emptyDraftCurator) DraftClosure(_ context.Context, _ memops.Thread) (curator.ClosureDraft, error) {
	return curator.ClosureDraft{Summary: "   "}, nil
}
