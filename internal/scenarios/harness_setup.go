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
// harnessMockModel is the sentinel model name a mock-driven scenario
// issues requests under. It is installed on State (not on the provider:
// a pool entry names no models), and a live-inference run replaces it
// with Scenario.LiveModel.
const harnessMockModel = "harness-mock"

// turn.State for the scenario. Mock is left as nil and is populated by
// RunScenario after Steps are known so the queue can be sized exactly.
func newHarness(t *testing.T, sc Scenario) *Harness {
	t.Helper()

	// Fail loud at run start on contradictory / no-op-inducing config (#9)
	// before building anything, so a misconfigured scenario stops here with a
	// precise message instead of producing a misleading green run.
	if err := sc.validate(); err != nil {
		t.Fatalf("scenario %s: invalid configuration: %v", sc.Name, err)
	}

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
type         = "inference"
api          = "openai"
`
	if err := os.WriteFile(paths.Providers, []byte(providersTOML), 0o644); err != nil {
		t.Fatalf("scenario %s: write providers.toml: %v", sc.Name, err)
	}

	provider := memops.Provider{
		Name:    "test",
		BaseURL: "http://harness.invalid",
		APIKey:  "harness-key",
		Type:    memops.ProviderTypeInference,
		API:     memops.ProviderAPIOpenAI,
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
	// The model name rides on State, never on the provider: a pool entry
	// names no models. A live-inference run overwrites this with
	// Scenario.LiveModel in harness_run.
	state.Model = harnessMockModel

	mPath := sc.MetricsPath
	if mPath == "" {
		mPath = filepath.Join(tmp, sc.Name+".metrics.json")
	}

	run := metrics.New(map[string]string{"scenario": sc.Name})

	// Pin the simulated clock to the Monday-midnight anchor (§3.1). It is
	// the one clock primitive; a Step.At (sim path) or the sliceSource
	// shim (handwritten path) advances it, and SimDayIndex(pinnedClock)
	// drives the derived day-close tick. The prior 2026-05-01 12:00 anchor
	// (a Friday at noon) put grid lines at noon and landed the day-off on
	// an arbitrary weekday; the Monday-midnight anchor makes day index 6 a
	// real Sunday and the day-off fall out of the calendar.
	pinned := SimClockStart
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
		onSimDayClose:     sc.OnSimDayClose,
		recallerFactory:   sc.Recaller,
		liveClient:        sc.LiveClient,
		liveModel:         sc.LiveModel,
		// lastClosedDay starts at the pinned clock's day index — day 0
		// under the Monday-midnight anchor. The first close fires when the
		// derived day index first ticks to 1, well into the run rather than
		// at step 1 when nothing has happened. End-of-run always fires heavy
		// regardless.
		lastClosedDay: SimDayIndex(pinned),
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

	// §3.5 closure + §3.4 recall ack policy — see installAckPolicy. A
	// simulated relaunch (restartSession) re-applies it to the rebuilt State.
	installAckPolicy(state)

	// Install the scenario's custom recaller (embedding-in-loop seam, #98)
	// and prime its index. A no-op when the scenario kept the default
	// symbolic-only recaller turn.NewState installed.
	h.installRecaller(t, state)

	return h
}

// installAckPolicy applies the harness's ACK configuration — §3.5 closure
// and §3.4 recall — to a State, at construction AND after a simulated
// relaunch, since turn.LoadSession builds a stock State that carries
// neither. One helper for both because they have one reason to exist: a
// scripted ack IS a human ack, so the harness runs the interactive
// regime.
//
// The curator is a deterministic script, so closure scenarios never need
// a live model. The closure scan only runs when both a curator and a
// ClosureResolver are installed; the resolver is installed per-step from
// Step.ClosureAck.
//
// The §2.6.1 closure ack mode is pinned to `always` — the pre-2026-08-04
// per-decay interactive flow. A Step.ClosureAck IS a scripted HUMAN ack, so
// `always` is what the scenarios actually encode, and pinning it keeps every
// scripted outcome (wip / defer / abandoned) reachable. Under the front
// end's `auto` default a scripted non-resolved outcome would be
// unreachable for routine threads, and every anchor-rich thread would
// queue for a boundary drain the harness does not run — leaving the sim's
// BD-4 runtime-mirror (which models decay closure as an unconditional
// eviction) diverging from a runtime that no longer retires. That is not
// hypothetical: the first run that missed this on the relaunch path failed
// TestDayOffThroughHarness with exactly that divergence. Auto-accept is
// covered by internal/turn's unit tests.
//
// The §2.6.1 RECALL ack mode is pinned to `always` for the same reason
// (AMENDED 2026-08-05): a Step.RecallAck is a scripted human verdict on
// every surfaced candidate, and the scripted resolver ERRORS when an id it
// accepts was not offered — under the front end's `banded` default a
// high-confidence candidate would be auto-accepted before the resolver saw
// it and the scenario would fail on its own success. COVERAGE CONSEQUENCE,
// stated rather than hidden: the banded default's AUTO path is unit-covered
// (internal/turn) and NOT sim-covered. It joins the already-named
// banded/auto-mode sim rung — one rung now owing coverage for both the
// closure and the recall auto paths.
func installAckPolicy(state *turn.State) {
	state.Curator = scriptedCurator{}
	state.ClosureAckMode = turn.AckModeAlways
	state.RecallAckMode = turn.RecallAckAlways
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
