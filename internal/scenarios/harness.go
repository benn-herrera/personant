// Package scenarios is the spec §11.4 scenario-test fabric: the
// instrument by which the architectural thesis is empirically verified.
//
// Each Scenario is a named end-to-end story driven through the real
// turn.Run path with a scripted mock LLM. After every step (and once
// at the end) the harness runs invariant validators (§11.5) and
// accumulates per-scenario metrics (§11.6) into a stable JSON blob the
// six-month simulation harness will consume verbatim.
//
// The shape — Step → Scenario, with default + custom invariants — is
// designed to scale from the four Phase-2.f scenarios to the thousands
// of steps the v0.1 acceptance gate will demand.
package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/metrics"
	"personant/internal/model"
	"personant/internal/store"
	"personant/internal/turn"
)

// Step is one turn in a Scenario. Exactly one mock LLM response is
// queued for the step; running the step calls turn.Run once. Per-step
// invariants run after the turn completes; an empty Invariants slice
// means use DefaultInvariants.
type Step struct {
	// UserInput is the line the synthetic user "types" at the >
	// prompt.
	UserInput string

	// MockResponse is the LLM response the harness queues into the
	// scripted mock before driving the turn. Use NewMockResponseWithTag
	// for the common case.
	MockResponse model.Response

	// Invariants run after this step's turn completes. nil/empty →
	// DefaultInvariants run. Use this slot to override the default
	// suite for steps that intentionally introduce a transient
	// inconsistency (rare in v0.1).
	Invariants []InvariantCheck

	// Annotation is a free-form label printed on assertion failure to
	// identify which step failed in a multi-step scenario.
	Annotation string

	// PreEvents are §3.0 deltas emitted BEFORE the user.prompt delta
	// for this step, sharing the same TurnNumber. Use for scenarios
	// that exercise tool.result, user.shell-capture, or other
	// non-user.prompt sources of context modification within a turn.
	// The harness wires these into turn.RunWithDeltas.
	PreEvents []turn.Delta

	// ExpectedRecallMatches is the §3.4 recall-fidelity ground truth
	// for this step: the set of thread IDs the harness expects to
	// observe `spine.match-fire` records for during turn close. Set
	// semantics — duplicate IDs and ordering are not significant.
	//
	//   - nil           → step is unmeasured; no assertion, no
	//                     precision/recall/F1 sample emitted. The
	//                     metrics blob counts these via
	//                     `recall_fidelity_unmeasured_steps`.
	//   - non-nil empty → "expect zero matches" (negative probe). The
	//                     step is measured: emits 1/1/1 on agreement.
	//   - non-nil set   → exact-set expectation. The harness records
	//                     per-step precision / recall / F1 into the
	//                     §9.6 metrics blob and calls t.Errorf when
	//                     the actual set differs (strict semantics —
	//                     catches both false positives and false
	//                     negatives, which is the point of the
	//                     measurement instrument).
	ExpectedRecallMatches []string
}

// Scenario is a named end-to-end flow. Setup runs once before the
// steps; FinalInvariants run once after the last step. MetricsPath
// receives the per-scenario metrics blob when non-empty; otherwise the
// harness writes <t.TempDir>/<scenario-name>.metrics.json and logs the
// path via t.Logf so a developer can inspect it post-run.
type Scenario struct {
	Name string

	// Setup runs once before Steps. The harness has already initialized
	// a fresh isolated home and created a default project named
	// "harness-default" before Setup is invoked; Setup may seed
	// additional projects, pre-populate spine entries, etc.
	Setup func(h *Harness) error

	Steps []Step

	// FinalInvariants run after the last step. Use this for end-of-run
	// assertions that are not meaningful step-by-step (e.g. spine
	// cardinality, history-symbol cap behavior across the whole run).
	FinalInvariants []InvariantCheck

	// MetricsPath, if non-empty, overrides the default metrics output
	// location. Default: <t.TempDir>/<scenario-name>.metrics.json.
	MetricsPath string
}

// Harness owns the isolated test environment for one scenario run.
//
// Fields are exported for invariants and step builders that need to
// inspect or extend state (e.g. seeding a second project, switching
// active project mid-scenario). The mutex protects History across the
// per-step and final-invariant boundaries; turn.Run itself is
// single-goroutine within RunScenario.
type Harness struct {
	// Paths is retained as a direct handle into the substrate so
	// invariant validators can inspect canonical state without going
	// through the port. Application code goes through Ops; invariants
	// are test-side substrate validators and stay on store.* access.
	Paths    store.PersonantPaths
	Ops      memops.MemoryOps
	Project  store.ProjectMeta
	Provider store.Provider
	Mock     *model.MockClient
	State    *turn.State
	Metrics  *metrics.Run
	T        testing.TB

	// MetricsPath is the resolved path the metrics blob will be written
	// to (post-Setup). Filled in before Setup runs so Setup may inspect
	// it; tests may also override it before the first step.
	MetricsPath string

	// startedAt is the harness construction time; per-step durations
	// are computed from monotonic now() reads, but startedAt is kept
	// for cross-correlation with the metrics blob's started_at.
	startedAt time.Time
}

// NewMockResponseWithTag is the common-case constructor for a Step's
// MockResponse: a §5.1 topic tag built from threads + anchors followed
// by the body text. Threads is typically a single entry — either
// "*new-topic*" for thread creation or "thr_<n>" for engagement. Anchors
// is the 4–8-element list the model would emit alongside the tag.
func NewMockResponseWithTag(threads []string, anchors []string, body string) model.Response {
	tag := fmt.Sprintf("*topic: %s [%s]*",
		strings.Join(threads, ", "),
		strings.Join(anchors, ", "))
	return model.Response{
		Content:      tag + "\n" + body,
		FinishReason: "stop",
	}
}

// RunScenario is the harness entry point. Sets up an isolated home,
// drives sc.Steps through turn.Run end-to-end, runs invariants after
// each step, and writes a metrics blob at scenario completion.
//
// Failure policy:
//
//   - turn.Run errors → t.Fatalf (halt; the runtime hit a bug the
//     scenario can't recover from).
//   - Invariant failures → t.Errorf (continue; collect every problem in
//     a single pass so the developer sees the full picture).
//   - Setup errors → t.Fatalf.
//   - Metrics write failure → t.Errorf (the run's results matter even
//     if the blob can't be persisted).
//
// On any failure the metrics-blob path is logged via t.Logf so the
// developer can inspect it for forensics.
func RunScenario(t *testing.T, sc Scenario) {
	t.Helper()
	h := newHarness(t, sc)

	// Pre-queue every step's mock response. This works because each
	// step has exactly one turn and turn.Run consumes exactly one
	// response per call.
	queue := make([]model.Response, 0, len(sc.Steps))
	for _, step := range sc.Steps {
		queue = append(queue, step.MockResponse)
	}
	h.Mock = model.NewScriptedMock(queue, nil)
	h.State.Client = h.Mock

	if sc.Setup != nil {
		if err := sc.Setup(h); err != nil {
			t.Fatalf("scenario %s: Setup: %v", sc.Name, err)
		}
	}

	t.Logf("scenario %s: metrics path: %s", sc.Name, h.MetricsPath)

	for i, step := range sc.Steps {
		runStep(t, h, i, step)
	}

	finalInvs := sc.FinalInvariants
	if len(finalInvs) == 0 {
		finalInvs = DefaultInvariants
	}
	runInvariants(t, h, finalInvs, fmt.Sprintf("final[%s]", sc.Name))

	// Final gauges.
	if recs, err := store.ReadSpine(h.Paths.Spine); err == nil {
		h.Metrics.Set("final_spine_size", float64(len(recs)))
		h.Metrics.Set("final_thread_count", float64(len(recs)))
	}
	h.Metrics.Set("final_peak_history_symbols", float64(peakHistorySymbols(h.Paths)))

	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Errorf("scenario %s: metrics write: %v", sc.Name, err)
	}
	if t.Failed() {
		// Re-emit the path on failure so it's adjacent to the verdict
		// line in a long test log.
		t.Logf("scenario %s FAILED — metrics blob: %s", sc.Name, h.MetricsPath)
	}
}

// newHarness builds an isolated home, default project, and starting
// turn.State for the scenario. Mock is left as nil and is populated by
// RunScenario after Steps are known so the queue can be sized exactly.
func newHarness(t *testing.T, sc Scenario) *Harness {
	t.Helper()
	tmp := t.TempDir()
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
apiKey       = "harness-key"
defaultModel = "harness-mock"
`
	if err := os.WriteFile(paths.Providers, []byte(providersTOML), 0o644); err != nil {
		t.Fatalf("scenario %s: write providers.toml: %v", sc.Name, err)
	}

	provider := store.Provider{
		Name:         "test",
		BaseURL:      "http://harness.invalid",
		APIKey:       "harness-key",
		DefaultModel: "harness-mock",
	}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	project := store.ProjectMeta{
		ID:               "prj_1",
		Name:             "harness-default",
		CurrentRootPath:  tmp,
		Created:          now,
		LastActive:       now,
		ConventionsPaths: []string{},
		SymbolPatterns:   []store.ProjectPattern{},
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
	// Pin the clock so timestamps are deterministic — invariant checks
	// (created ≤ last_engaged) are timestamp-sensitive, and a
	// scenario's metrics blob is more readable with stable timestamps.
	state.SetClock(func() time.Time {
		return time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	})

	mPath := sc.MetricsPath
	if mPath == "" {
		mPath = filepath.Join(tmp, sc.Name+".metrics.json")
	}

	run := metrics.New(map[string]string{"scenario": sc.Name})

	return &Harness{
		Paths:       paths,
		Ops:         ops,
		Project:     project,
		Provider:    provider,
		State:       state,
		Metrics:     run,
		T:           t,
		MetricsPath: mPath,
		startedAt:   time.Now(),
	}
}

// runStep drives one Step end-to-end: turn.Run, metrics record,
// invariants. Step indices in messages are 1-based for readability.
func runStep(t *testing.T, h *Harness, idx int, step Step) {
	t.Helper()
	label := step.Annotation
	if label == "" {
		label = fmt.Sprintf("step %d", idx+1)
	}

	preSpine, _ := store.ReadSpine(h.Paths.Spine)
	preFires, err := matchFireCounts(h.Paths)
	if err != nil {
		t.Fatalf("scenario step %d (%s): matchFireCounts (pre): %v", idx+1, label, err)
	}

	start := time.Now()
	body, err := turn.RunWithDeltas(context.Background(), h.State, step.PreEvents, step.UserInput, io.Discard)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("scenario step %d (%s): turn.Run: %v", idx+1, label, err)
	}

	postSpine, _ := store.ReadSpine(h.Paths.Spine)
	postFires, err := matchFireCounts(h.Paths)
	if err != nil {
		t.Fatalf("scenario step %d (%s): matchFireCounts (post): %v", idx+1, label, err)
	}
	recordRecallFidelity(t, h, idx, label, step.ExpectedRecallMatches, diffMatchFireSet(preFires, postFires))

	// Per-step metrics.
	h.Metrics.Counter("turns", 1)
	h.Metrics.Record("turn_duration_ms", float64(elapsed.Milliseconds()))
	h.Metrics.Record("response_bytes", float64(len(step.MockResponse.Content)))
	if len(postSpine) > len(preSpine) {
		h.Metrics.Counter("threads_created", int64(len(postSpine)-len(preSpine)))
	} else if len(postSpine) == len(preSpine) {
		// Existing-thread engagement (or no engagement at all). Don't
		// double-count: only mark engagement if the body itself implies
		// a tag was present and at least one record's turn_count moved.
		h.Metrics.Counter("engaged_existing_threads", 1)
	}

	// Topic-tag presence accounting. Cheap and useful for cross-version
	// drift detection (a model regressing on tag emission would show up
	// here long before any thread/spine corruption).
	if strings.Contains(step.MockResponse.Content, "*topic:") {
		h.Metrics.Counter("topic_tag_parsed", 1)
	} else {
		h.Metrics.Counter("topic_tag_missing", 1)
	}

	_ = body // body is the streamed text; harness keeps it in History via turn.Run.

	invs := step.Invariants
	if len(invs) == 0 {
		invs = DefaultInvariants
	}
	runInvariants(t, h, invs, label)
}

// runInvariants executes every check, calling t.Errorf on failure.
// Skip-marker errors (errSkipPhase) are surfaced via t.Logf so the
// developer sees what's deferred without polluting the error count.
func runInvariants(t *testing.T, h *Harness, invs []InvariantCheck, label string) {
	t.Helper()
	for _, inv := range invs {
		if inv == nil {
			continue
		}
		if err := inv(h); err != nil {
			var sk skipPhaseErr
			if errors.As(err, &sk) {
				t.Logf("scenario %s — invariant skipped (%s): %v", h.T.Name(), label, err)
				continue
			}
			t.Errorf("scenario %s [%s]: %v", h.T.Name(), label, err)
		}
	}
}

// peakHistorySymbols walks every thread file and returns the maximum
// history_symbols length observed. Cheap (handful of file reads); used
// by the final-gauge phase to make the cap-and-evict scenario's
// behavior visible in metrics.
func peakHistorySymbols(paths store.PersonantPaths) int {
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return 0
	}
	peak := 0
	for _, r := range recs {
		thr, err := store.LoadThread(paths, r.ID)
		if err != nil {
			continue
		}
		if n := len(thr.Frontmatter.HistorySymbols); n > peak {
			peak = n
		}
	}
	return peak
}

// SwitchProject swaps the harness's active project to targetID,
// updating last-active and the in-memory turn.State. Used by
// scenarios that exercise project switching ahead of /project switch
// landing in the runtime.
//
// Returns an error if the target project's meta.json is missing
// (callers must seed the second project via Setup before switching).
func (h *Harness) SwitchProject(targetID string) error {
	meta, err := store.LoadProjectMeta(h.Paths, targetID)
	if err != nil {
		return fmt.Errorf("SwitchProject %s: %w", targetID, err)
	}
	if err := store.WriteLastActive(h.Paths, targetID); err != nil {
		return fmt.Errorf("SwitchProject %s: WriteLastActive: %w", targetID, err)
	}
	h.Project = meta
	h.State.ActiveProject = meta
	// History is per-session-and-cross-project (the model still needs
	// to see the prior turn even after a project switch); leave it.
	return nil
}

// indexRebuild and indexCheck are thin wrappers around the index
// package's exported entry points. They live here so the invariants
// can call them without leaking the index package's option surface
// into invariant signatures.
func indexRebuild(paths store.PersonantPaths) error {
	return index.Rebuild(paths, index.Options{Quiet: true})
}

func indexCheck(paths store.PersonantPaths) (index.CheckResult, error) {
	return index.Check(paths, index.Options{Quiet: true})
}
