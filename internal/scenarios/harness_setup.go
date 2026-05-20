package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/metrics"
	"personant/internal/store"
	"personant/internal/turn"
)

// newHarness builds an isolated home, default project, and starting
// turn.State for the scenario. Mock is left as nil and is populated by
// RunScenario after Steps are known so the queue can be sized exactly.
func newHarness(t *testing.T, sc Scenario) *Harness {
	t.Helper()
	tmp := runDataHome(t, sc.Name)
	paths := store.PathsForHome(tmp)

	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("scenario %s: store.Init: %v", sc.Name, err)
	}

	// Write a minimal providers.toml so any future code path that
	// resolves a provider has a deterministic answer. The URL is a
	// sentinel; the mock client is the only consumer of "test" in this
	// package.
	providersTOML := `[test]
baseUrl      = "http://harness.invalid"
apiKeyUnsafe = "harness-key"
defaultModel = "harness-mock"
`
	if err := os.WriteFile(paths.Providers, []byte(providersTOML), 0o644); err != nil {
		t.Fatalf("scenario %s: write providers.toml: %v", sc.Name, err)
	}

	provider := memops.Provider{
		Name:         "test",
		BaseURL:      "http://harness.invalid",
		APIKey:       "harness-key",
		DefaultModel: "harness-mock",
	}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	project := memops.ProjectMeta{
		ID:               "prj_1",
		Name:             "harness-default",
		CurrentRootPath:  tmp,
		Created:          now,
		LastActive:       now,
		ConventionsPaths: []string{},
		SymbolPatterns:   []memops.ProjectPattern{},
		IgnoreSymbols:    []string{},
	}
	if err := store.SaveProjectMeta(paths, project); err != nil {
		t.Fatalf("scenario %s: SaveProjectMeta: %v", sc.Name, err)
	}
	if err := store.WriteLastActive(paths, project.ID); err != nil {
		t.Fatalf("scenario %s: WriteLastActive: %v", sc.Name, err)
	}

	ops := fileadapter.NewFileAdapter(paths)
	state := turn.NewState(ops, project, provider, nil)

	mPath := sc.MetricsPath
	if mPath == "" {
		mPath = filepath.Join(tmp, sc.Name+".metrics.json")
	}

	run := metrics.New(map[string]string{"scenario": sc.Name})

	pinned := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	h := &Harness{
		Paths:             paths,
		Ops:               ops,
		Project:           project,
		Provider:          provider,
		State:             state,
		Metrics:           run,
		T:                 t,
		MetricsPath:       mPath,
		RunHome:           tmp,
		startedAt:         clock.Profiling(),
		pinnedClock:       pinned,
		tailer:            newLogTailer(paths.LogsDir),
		createdThreadIDs:  map[string]struct{}{},
		archivedThreadIDs: map[string]struct{}{},
		heavyCadence:      sc.HeavyInvariantCadence,
		// Schedule the first heavy firing one cadence-tick after the
		// start: this puts the first firing well into the run rather than
		// at step 1 when nothing has happened yet. End-of-run always
		// fires heavy regardless.
		nextHeavyAt: pinned.Add(sc.HeavyInvariantCadence),
	}

	// Open the per-step memory telemetry. Every scenario gets a
	// mem.jsonl — the file is the load-bearing diagnostic the harness
	// was instrumented to produce, and a unit-test scenario's tiny file
	// (a few KB) is harmless. A nil mem (open failure) degrades to
	// no-op sampling, never to a halted run.
	mem, err := newMemTelemetry(tmp)
	if err != nil {
		t.Logf("scenario %s: memtel open: %v (continuing without mem.jsonl)", sc.Name, err)
	} else {
		h.mem = mem
	}
	t.Cleanup(func() {
		// Write a final sample so a clean run's mem.jsonl includes the
		// endpoint of the curve. The watchdog (if any) is stopped first
		// so its flush()-then-panic path cannot race the close.
		if h.watchdog != nil {
			h.watchdog.stopAndWait()
		}
		if h.mem != nil {
			h.mem.sample(-1, h.pinnedClock)
			h.mem.close()
		}
	})

	// Arm the heap watchdog when the scenario opted in. cap > 0 is the
	// opt-in: unit tests leave it zero and see no goroutine.
	if sc.MemoryCapBytes > 0 {
		h.watchdog = newMemWatchdog(uint64(sc.MemoryCapBytes), h.mem)
		h.watchdog.start()
	}

	// Pin the clock so timestamps are deterministic — invariant checks
	// (created ≤ last_engaged) are timestamp-sensitive, and a
	// scenario's metrics blob is more readable with stable timestamps.
	// A Step.TimeDelta advances h.pinnedClock; the closure reads it
	// live so the advance takes effect on the next turn. The override is
	// process-global, so restore it at test end to isolate runs.
	restore := clock.SetTimeline(func() time.Time { return h.pinnedClock })
	t.Cleanup(restore)

	// Install a deterministic scripted curator so closure scenarios
	// never need a live model. The closure scan only runs when both a
	// curator and a ClosureResolver are installed; the resolver is
	// installed per-step from Step.ClosureAck.
	state.Curator = scriptedCurator{}

	return h
}

// scriptedCurator is the harness's deterministic Curator: it returns a
// fixed summary and reuses the thread's existing anchors, so closure
// scenarios run without a live model.
type scriptedCurator struct{}

func (scriptedCurator) DraftClosure(_ context.Context, thread memops.Thread) (curator.ClosureDraft, error) {
	return curator.ClosureDraft{
		Summary: "scripted closure summary for " + thread.Meta.ID,
		Anchors: append([]string(nil), thread.Meta.Anchors...),
	}, nil
}
