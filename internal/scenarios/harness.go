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
	"regexp"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/curator"
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
	//                     §9.6 metrics blob. Whether a mismatch fails
	//                     the test depends on RecallMode.
	ExpectedRecallMatches []string

	// RecallMode selects how ExpectedRecallMatches is enforced. The
	// zero value (RecallStrict) fails the test on any deviation;
	// RecallMeasureOnly records the metrics without failing. Ignored
	// when ExpectedRecallMatches is nil.
	RecallMode RecallFidelityMode

	// RecallAck scripts how this step resolves a recall offer surfaced
	// at turn close (Part B). AcceptThreadIDs lists the thread IDs to
	// accept (pull into Layer B); any offered candidate not listed is
	// declined with Reason. A nil RecallAck means the step installs no
	// resolver — recall stays log-only (the pre-Part-B behavior).
	RecallAck *RecallAck

	// ClosureAck scripts how this step resolves a §3.5 closure offer
	// surfaced at turn close. Its Outcome is applied to every closure
	// offer the step surfaces. A nil ClosureAck means the step installs
	// no ClosureResolver — closure stays detection-disabled for the
	// step (mirrors a nil RecallAck yielding a nil recall resolver).
	ClosureAck *ClosureAck

	// TimeDelta, when non-zero, advances the harness's pinned clock by
	// that much before this step's turn. The harness pins a fixed clock
	// by default; a non-zero TimeDelta lets a scenario cross the §3.5
	// wall-clock decay threshold deterministically.
	TimeDelta time.Duration

	// RestartSession, when true, simulates an application shutdown+relaunch
	// before running this step: the in-memory turn.State is discarded and
	// rebuilt from the substrate (turn.LoadSession), exactly as a fresh
	// process launch would. The harness then asserts via AssertSessionRestored
	// that the should-survive subset (Layer B/C membership, active project)
	// was reconstructed correctly. The step's turn then runs against the
	// rebuilt State. This exercises a *clean* lifecycle, not crash recovery.
	RestartSession bool
}

// ClosureAck scripts how a step resolves a §3.5 closure offer surfaced
// at turn close. Outcome is applied to every closure offer the step
// surfaces. A nil ClosureAck means the step installs no resolver —
// closure stays detection-disabled for that step.
type ClosureAck struct {
	Outcome turn.ClosureOutcome
}

// RecallAck scripts how a step resolves a recall offer surfaced at
// turn close (Part B). AcceptThreadIDs lists the thread IDs to
// accept (pull into Layer B); any offered candidate not listed is
// declined with Reason. A nil RecallAck means the step installs no
// resolver — recall stays log-only (the pre-Part-B behavior).
type RecallAck struct {
	AcceptThreadIDs []string
	Reason          turn.DeclineReason
}

// RecallFidelityMode selects how a Step's ExpectedRecallMatches is
// enforced against the observed spine.match-fire set.
type RecallFidelityMode int

const (
	// RecallStrict treats any deviation from ExpectedRecallMatches as
	// a test failure (t.Errorf), and records precision/recall/F1 into
	// the clean recall_fidelity_* metric series. For ground-truth
	// scenarios where a mismatch is a real recall bug.
	RecallStrict RecallFidelityMode = iota

	// RecallMeasureOnly records precision/recall/F1 into the
	// recall_fidelity_adversarial_* series and never fails the test.
	// For C.3 adversarial probes (vocabulary drift, stop-word leak,
	// false friends) whose underperformance is the measurement, not a
	// defect — the §9.9 baseline comparison is where regressions in
	// these numbers surface.
	RecallMeasureOnly
)

// StepFeedback is the per-step outcome the harness reports back to a
// StepSource after each turn, so the next Next() call may (in a later
// task) branch the generated workload on runtime behavior. It is the
// seam between the runtime and an on-demand generator.
//
// As of the on-demand-workload refactor the feedback is defined and
// threaded through but NOT consumed by the generator — the workload
// stays purely a function of (Seed, Duration), preserving determinism.
// A later task that wants runtime-conditional workload (e.g. reacting
// to a recall miss) reads these fields in StepSource.Next.
//
// The zero value is the "no step has run yet" feedback passed into the
// very first Next() call.
type StepFeedback struct {
	// Index is the 0-based index of the step that just ran. -1 before
	// the first step.
	Index int

	// RecallMatchFires is the count of `spine.match-fire` events the
	// just-run turn emitted — the observed recall hits for the step.
	RecallMatchFires int

	// RecallExpected is the count of ground-truth matches the step
	// declared (len of Step.ExpectedRecallMatches); 0 for an unmeasured
	// step.
	RecallExpected int
}

// StepSource yields scenario steps on demand instead of materialising
// the whole list up front. RunScenario drives a non-nil
// Scenario.StepSource by looping Next(feedback) until it returns false.
//
// Next receives the StepFeedback for the step that just completed (the
// zero value, with Index -1, before the first step) and returns the
// next Step plus an ok flag — ok false signals the run is complete.
//
// A fixed []Step is adapted to this interface by sliceSource so the
// harness has exactly one internal drive loop.
type StepSource interface {
	Next(feedback StepFeedback) (Step, bool)
}

// sliceSource adapts a fixed []Step to the StepSource interface so a
// pre-built scenario and an on-demand generator share RunScenario's one
// drive loop. It ignores the feedback — a fixed list never branches.
type sliceSource struct {
	steps []Step
	pos   int
}

func (s *sliceSource) Next(_ StepFeedback) (Step, bool) {
	if s.pos >= len(s.steps) {
		return Step{}, false
	}
	step := s.steps[s.pos]
	s.pos++
	return step, true
}

// Scenario is a named end-to-end flow. Setup runs once before the
// steps; FinalInvariants run once after the last step. MetricsPath
// receives the per-scenario metrics blob when non-empty; otherwise the
// harness writes <home>/<scenario-name>.metrics.json — where <home> is
// the persistent test/rundata/<name>/ run home — and logs the path via
// t.Logf so a developer can inspect it post-run.
//
// A scenario supplies its steps in exactly one of two ways: a fixed
// Steps slice (the ~80 hand-written scenarios) or a StepSource that
// yields them on demand (the six-month simulation, whose step count is
// far too large to hold in memory). When StepSource is non-nil it
// takes precedence and Steps is ignored.
type Scenario struct {
	Name string

	// Setup runs once before Steps. The harness has already initialized
	// a fresh isolated home and created a default project named
	// "harness-default" before Setup is invoked; Setup may seed
	// additional projects, pre-populate spine entries, etc.
	Setup func(h *Harness) error

	Steps []Step

	// StepSource, when non-nil, drives the scenario on demand instead of
	// Steps: RunScenario pulls one Step per turn rather than iterating a
	// pre-built slice. Used by the six-month simulation, whose ~59 000+
	// steps must not all live in memory at once. When set, Steps is
	// ignored.
	StepSource StepSource

	// FinalInvariants run after the last step. Use this for end-of-run
	// assertions that are not meaningful step-by-step (e.g. spine
	// cardinality, history-symbol cap behavior across the whole run).
	FinalInvariants []InvariantCheck

	// MetricsPath, if non-empty, overrides the default metrics output
	// location. Default: <home>/<scenario-name>.metrics.json, inside the
	// persistent test/rundata/<name>/ run home.
	MetricsPath string

	// HeavyInvariantCadence relaxes per-step invariant firing for long
	// simulations: when > 0, the heavy invariants (substrate-scale checks —
	// VerifySpineIntegrity, VerifyIndexFresh, VerifyProjectReferences,
	// VerifyThreadFrontmatterMatchesSpine, VerifyThreadAccounting) fire
	// only when the harness's simulated clock has advanced past the
	// next-due tick. Cheap invariants (strictly O(1) or
	// O(touched-this-step)) still fire every step. The heavy set always
	// fires once at end-of-run regardless of cadence, so the
	// acceptance-gate behavior is preserved.
	//
	// Zero value = fire heavy invariants every step (the legacy behavior,
	// what handwritten scenarios still get). Only opt-in scenarios — the
	// six-month sim — set this.
	//
	// Per-step Step.Invariants overrides bypass this cadence: when a step
	// supplies an explicit invariant list, that exact list runs.
	HeavyInvariantCadence time.Duration

	// MemoryCapBytes, when > 0, arms the harness's heap watchdog: a
	// goroutine that polls runtime.MemStats.HeapInuse every
	// memWatchdogCheckInterval and, on cap exceed, captures a heap profile
	// + goroutine dump under the rundata directory, flushes mem.jsonl, and
	// panics. The panic produces a Go stack trace via the test harness —
	// infinitely more useful for diagnosing a leak than a macOS SIGKILL.
	//
	// Zero value (the default) disables the watchdog, preserving the
	// behavior unit tests have always seen. The six-month sim sets it to a
	// value comfortably under the macOS jetsam threshold (~30 GB
	// compressed on a 38 GB machine) so a runaway leak trips the cap
	// before the OS pager kills the process.
	//
	// The mem.jsonl per-step sample stream (memTelemetry) writes
	// regardless of this field — the file is the load-bearing forensic
	// artifact; the watchdog is the trip-wire that converts a future
	// SIGKILL into a stack trace.
	MemoryCapBytes int64
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
	Project  memops.ProjectMeta
	Provider memops.Provider
	Mock     *model.MockClient
	State    *turn.State
	Metrics  *metrics.Run
	T        testing.TB

	// MetricsPath is the resolved path the metrics blob will be written
	// to (post-Setup). Filled in before Setup runs so Setup may inspect
	// it; tests may also override it before the first step.
	MetricsPath string

	// RunHome is the per-scenario rundata directory
	// (<repo-root>/test/rundata/<scenario-name>/) that holds the metrics
	// blob, the mem.jsonl telemetry, and any watchdog pprof drops. Stored
	// on the harness so post-run inspection code does not have to
	// reconstruct it from MetricsPath topology.
	RunHome string

	// mem is the per-step memory-telemetry writer. Opened in newHarness,
	// sampled before each step in runStep, and closed via t.Cleanup so
	// the file is released even on a test failure / panic. nil only on a
	// telemetry open failure (which logs but does not fail the run — the
	// run itself is the load-bearing artifact).
	mem *memTelemetry

	// watchdog, when non-nil, polls HeapInuse against Scenario.MemoryCapBytes
	// on a separate goroutine. Owned by the harness; stopped via t.Cleanup.
	watchdog *memWatchdog

	// startedAt is the harness construction time; per-step durations
	// are computed from monotonic now() reads, but startedAt is kept
	// for cross-correlation with the metrics blob's started_at.
	startedAt clock.ProfilingTime

	// pinnedClock is the deterministic time the turn.State clock
	// returns. It defaults to a fixed instant; a Step.TimeDelta advances
	// it before that step's turn so §3.5 wall-clock decay can be
	// exercised. runStep is single-goroutine, so no synchronization is
	// needed.
	pinnedClock time.Time

	// tailer is the incremental event-log reader. runStep calls
	// tailer.poll() exactly once after each turn to obtain that turn's
	// newly-appended log lines, instead of re-reading all of run-so-far.
	// This is what keeps a long simulation O(N) rather than O(N²).
	tailer *logTailer

	// createdThreadIDs and archivedThreadIDs are the cumulative sets
	// folded incrementally from each step's tailed log lines:
	// `thread.created` IDs and `archive.simulated-delete` IDs
	// respectively. They replace the per-call full log walks the
	// VerifyThreadAccounting invariant and the recall-fidelity
	// archival-forgiveness filter used to perform. Both sets only ever
	// grow, so they are insensitive to which poll window an event lands
	// in — only the per-step match-fire set is window-sensitive.
	createdThreadIDs  map[string]struct{}
	archivedThreadIDs map[string]struct{}

	// heavyCadence is the Scenario.HeavyInvariantCadence the run was
	// started with. Zero = fire heavy invariants every step (legacy
	// behavior).
	heavyCadence time.Duration

	// nextHeavyAt is the simulated time at which the next heavy-invariant
	// firing is due, when heavyCadence > 0. runStep fires the heavy
	// invariants iff h.pinnedClock >= h.nextHeavyAt, then advances
	// h.nextHeavyAt by h.heavyCadence. Unused (and meaningless) when
	// heavyCadence == 0.
	nextHeavyAt time.Time
}

// foldEventLines folds a batch of tailed log lines into the harness's
// cumulative created/archived thread-ID sets. Called by runStep after
// each turn's poll and by tests that seed log lines directly.
func (h *Harness) foldEventLines(lines []string) {
	for _, line := range lines {
		if id, ok := eventThreadID(line, "thread.created ", ""); ok {
			h.createdThreadIDs[id] = struct{}{}
		}
		if id, ok := eventThreadID(line, "archive.simulated-delete ", "thr="); ok {
			h.archivedThreadIDs[id] = struct{}{}
		}
	}
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
//
// Returns the *Harness it constructed so callers can inspect post-run
// state — final spine, metrics, the LogsDir event log — without
// reconstructing paths from TempDir topology. Existing callers that
// ignore the return value continue to compile unchanged.
func RunScenario(t *testing.T, sc Scenario) *Harness {
	t.Helper()
	h := newHarness(t, sc)

	// The mock holds a single current-response slot. runStep sets it from
	// each step's MockResponse before driving the turn; every consult
	// within that turn — including a §5.5 mid-turn re-prompt (a second,
	// legitimate consult triggered when a topic tag names a thread not in
	// Layer B) — re-serves that one response. The re-prompt re-issues the
	// same request; once the missing thread is fetched the topic tag
	// drains cleanly. No pre-queue, so an on-demand StepSource needs no
	// up-front step count.
	h.Mock = model.NewScriptedMock(nil, nil)
	h.State.Client = h.Mock

	if sc.Setup != nil {
		if err := sc.Setup(h); err != nil {
			t.Fatalf("scenario %s: Setup: %v", sc.Name, err)
		}
	}

	t.Logf("scenario %s: metrics path: %s", sc.Name, h.MetricsPath)

	// One drive loop for both modes: a fixed Steps slice is adapted to a
	// StepSource by sliceSource. The feedback from each completed step is
	// passed into the next Next() call (the zero value, Index -1, seeds
	// the first call). The on-demand generator does not yet branch on it.
	src := sc.StepSource
	if src == nil {
		src = &sliceSource{steps: sc.Steps}
	}
	fb := StepFeedback{Index: -1}
	for i := 0; ; i++ {
		step, ok := src.Next(fb)
		if !ok {
			break
		}
		fb = runStep(t, h, i, step)
	}

	// Final poll before the final invariant run: each runStep already
	// polls after its turn, so this normally returns nothing, but it
	// guarantees the cumulative sets reflect every event the run
	// emitted before VerifyThreadAccounting reads them.
	if lines, err := h.tailer.poll(); err != nil {
		t.Fatalf("scenario %s: final tailer.poll: %v", sc.Name, err)
	} else {
		h.foldEventLines(lines)
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

	return h
}

// runTimestampSuffixRE matches a trailing `.<12 digits>` run-timestamp
// suffix — Go layout `060102150405` (yymmddhhMMss). Compiled once at
// package scope, consistent with the userTagRE-style pattern elsewhere.
var runTimestampSuffixRE = regexp.MustCompile(`\.\d{12}$`)

// runDataHome resolves and prepares the persistent per-scenario run
// home: <repo-root>/test/rundata/<scenario-name>/. Run data is forensic
// data — it deliberately does NOT live under t.TempDir() (which Go
// auto-deletes) so a developer can inspect spine/threads/logs/metrics
// after an interesting or failing run. The directory is cleared and
// recreated at the start of each run so it always holds the latest run
// of that scenario; no cleanup is registered, so it persists after the
// test exits. test/rundata/ is gitignored.
//
// The `<name>.<12-digit-suffix>` shape applies ONLY to simulation-run
// directories — the ones named `sim-workload-...` (produced by
// GenerateWorkload). runSimRung stamps a real wall-clock timestamp suffix
// so each sim run gets a unique directory; a sim-workload run that
// arrives un-suffixed (e.g. TestSim_DormantResumptionDrivesMidTurnFetch,
// which calls RunScenario directly) is given the all-zeros sentinel
// `.000000000000`. The sentinel is deliberate: it marks a sim directory
// whose run was NOT stamped with a real timestamp, and because it is
// constant the directory name is stable across runs, so that scenario
// overwrites in place (no accumulation). Non-sim scenario directories
// (decay-triggered-closure, recall-madlibs-*, project-switching, etc.)
// get NO suffix at all — bare names. Sim scenario names never
// legitimately end in `.<12 digits>`, so detecting an existing suffix is
// safe and avoids double-suffixing.
func runDataHome(t *testing.T, name string) string {
	t.Helper()
	if strings.HasPrefix(name, "sim-workload-") && !runTimestampSuffixRE.MatchString(name) {
		name += ".000000000000"
	}
	home := filepath.Join(repoRoot(t), "test", "rundata", name)
	if err := os.RemoveAll(home); err != nil {
		t.Fatalf("scenario %s: clear run home %s: %v", name, home, err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("scenario %s: create run home %s: %v", name, home, err)
	}
	return home
}

// measurementBlobPath resolves a persistent path for a standalone
// measurement's metrics blob (tests that write a metrics blob directly
// rather than driving the scenario harness). The blob is forensic data:
// it lives under <repo-root>/test/rundata/ — gitignored, never
// auto-deleted — alongside the per-scenario run homes. The parent
// directory is created if absent.
func measurementBlobPath(t *testing.T, filename string) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "test", "rundata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create rundata dir %s: %v", dir, err)
	}
	return filepath.Join(dir, filename)
}

// repoRoot walks up from the test's working directory until it finds a
// go.mod file, returning that directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root (containing go.mod) not found from %s", dir)
		}
		dir = parent
	}
}

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
		Summary: "scripted closure summary for " + thread.Frontmatter.ID,
		Anchors: append([]string(nil), thread.Frontmatter.Anchors...),
	}, nil
}

// runStep drives one Step end-to-end: turn.Run, metrics record,
// invariants. Step indices in messages are 1-based for readability.
//
// It returns the step's StepFeedback — the recall outcome the harness
// observed — which RunScenario passes into the next StepSource.Next
// call. The on-demand generator does not yet branch on it; the return
// value is the seam a later runtime-conditional-workload task consumes.
func runStep(t *testing.T, h *Harness, idx int, step Step) StepFeedback {
	t.Helper()
	label := step.Annotation
	if label == "" {
		label = fmt.Sprintf("step %d", idx+1)
	}

	// Sample memory telemetry BEFORE the step executes so even a step
	// that crashes is preceded on disk by its memory-state record. Sample
	// at step 0 unconditionally (the run's baseline) and then every
	// memSampleEveryNSteps; for short scenarios (handful of steps) this
	// means only the baseline + a final sample at close, which is exactly
	// what a smoke run wants.
	if idx == 0 || idx%memSampleEveryNSteps == 0 {
		h.mem.sample(idx, h.pinnedClock)
	}

	preSpine, _ := store.ReadSpine(h.Paths.Spine)

	// Simulated clean shutdown→relaunch. BEFORE running this step's turn,
	// discard the in-memory turn.State and rebuild it from the substrate
	// exactly as a fresh process launch would (turn.LoadSession), then
	// assert the should-survive subset was reconstructed. The step's turn
	// then runs against the rebuilt State.
	if step.RestartSession {
		restartSession(t, h, idx, label)
	}

	// Install this step's mock response. Every consult during the turn —
	// including a §5.5 mid-turn re-prompt — re-serves this one response.
	h.Mock.SetResponse(step.MockResponse)

	h.State.RecallResolver = recallResolverFor(t, idx, label, step.RecallAck)
	h.State.ClosureResolver = closureResolverFor(step.ClosureAck)

	// Advance the pinned clock before the turn so the step can cross
	// the §3.5 wall-clock decay threshold deterministically.
	if step.TimeDelta != 0 {
		h.pinnedClock = h.pinnedClock.Add(step.TimeDelta)
	}

	start := clock.Profiling()
	body, err := turn.RunWithDeltas(context.Background(), h.State, step.PreEvents, step.UserInput, io.Discard)
	elapsed := clock.Since(start)
	if err != nil {
		t.Fatalf("scenario step %d (%s): turn.Run: %v", idx+1, label, err)
	}

	postSpine, _ := store.ReadSpine(h.Paths.Spine)

	// One incremental poll captures exactly this turn's appended log
	// lines: the turn just completed and the next turn has not started.
	// From those lines extract this step's match-fire set and fold any
	// thread.created / archive.simulated-delete IDs into the cumulative
	// sets. Invariant checks between turns do not write to logs/, so
	// they cannot pollute the next step's window.
	stepLines, err := h.tailer.poll()
	if err != nil {
		t.Fatalf("scenario step %d (%s): tailer.poll: %v", idx+1, label, err)
	}
	h.foldEventLines(stepLines)
	matchFires := matchFireSet(stepLines)
	recordRecallFidelity(t, h, idx, label, step.RecallMode, step.ExpectedRecallMatches, matchFires)

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

	runInvariants(t, h, perStepInvariants(h, step), label)

	return StepFeedback{
		Index:            idx,
		RecallMatchFires: len(matchFires),
		RecallExpected:   len(step.ExpectedRecallMatches),
	}
}

// restartSession simulates a clean application shutdown→relaunch: it
// pointer-caches the pre-shutdown turn.State as a full snapshot (nothing
// mutates it after replacement), rebuilds a fresh State from the
// substrate via turn.LoadSession exactly as a real process launch would,
// re-installs the harness's scripted Curator (newHarness installs it on
// the original State; the rebuild needs the same), swaps it into the
// harness, and asserts the should-survive subset was reconstructed via
// the canonical AssertSessionRestored comparator. Mismatches are reported
// via t.Errorf so the run collects every problem.
func restartSession(t *testing.T, h *Harness, idx int, label string) {
	t.Helper()
	before := h.State

	rebuilt, err := turn.LoadSession(context.Background(), h.Ops, h.Project, h.Provider, h.Mock)
	if err != nil {
		t.Fatalf("scenario step %d (%s): simulated relaunch: turn.LoadSession: %v", idx+1, label, err)
	}
	// Re-install the deterministic scripted curator — newHarness installs
	// it on the original State, and a relaunched runtime would re-install
	// its curator too. The per-step RecallResolver/ClosureResolver are
	// installed by runStep below, so they need no handling here.
	rebuilt.Curator = scriptedCurator{}

	h.State = rebuilt

	if err := AssertSessionRestored(before, rebuilt); err != nil {
		t.Errorf("scenario step %d (%s): simulated relaunch: %v", idx+1, label, err)
	}
}

// recallResolverFor builds a turn.RecallResolver from a step's scripted
// RecallAck. A nil ack yields a nil resolver — recall stays log-only.
// Otherwise the resolver accepts every offered candidate whose ThreadID
// is in ack.AcceptThreadIDs and fails the test if a wanted ID never
// appeared in the offer (a stale or mis-specified scenario).
func recallResolverFor(t *testing.T, idx int, label string, ack *RecallAck) turn.RecallResolver {
	if ack == nil {
		return nil
	}
	return func(_ context.Context, offer turn.RecallOffer) (turn.RecallResolution, error) {
		want := make(map[string]bool, len(ack.AcceptThreadIDs))
		for _, id := range ack.AcceptThreadIDs {
			want[id] = false // false = not yet seen in the offer
		}
		var accept []int
		for i, c := range offer.Candidates {
			if _, ok := want[c.ThreadID]; ok {
				accept = append(accept, i)
				want[c.ThreadID] = true
			}
		}
		for id, seen := range want {
			if !seen {
				t.Errorf("scenario step %d (%s): RecallAck accepts %s but it was not in the recall offer",
					idx+1, label, id)
			}
		}
		reason := ack.Reason
		if reason == "" {
			reason = turn.DeclineNotRelevant
		}
		return turn.RecallResolution{Accept: accept, Reason: reason}, nil
	}
}

// closureResolverFor builds a turn.ClosureResolver from a step's
// scripted ClosureAck. A nil ack yields a nil resolver — closure stays
// detection-disabled for the step (mirrors recallResolverFor). A
// non-nil ack applies its Outcome to every closure offer the step
// surfaces.
func closureResolverFor(ack *ClosureAck) turn.ClosureResolver {
	if ack == nil {
		return nil
	}
	return func(_ context.Context, _ turn.ClosureOffer) (turn.ClosureResolution, error) {
		return turn.ClosureResolution{Outcome: ack.Outcome}, nil
	}
}

// perStepInvariants resolves the invariant list to run after a step,
// applying the heavy-cadence policy when the scenario opted into it.
//
//   - A step with an explicit Step.Invariants list bypasses the policy
//     entirely: that exact list runs (the step is expressing a precise
//     intent, e.g. "run only VerifyClosedThreadConsistency here").
//   - Otherwise, when Scenario.HeavyInvariantCadence is zero, the full
//     DefaultInvariants suite fires (legacy behavior — handwritten
//     scenarios are short and want the per-step assurance).
//   - Otherwise the cheap subset fires every step; the heavy subset
//     fires only when the simulated clock has advanced past the
//     next-due tick. nextHeavyAt advances by exactly one cadence per
//     firing (not "snap to now") so a long gap between turns does not
//     suppress heavy firings — the catch-up effectively folds into the
//     subsequent firings.
func perStepInvariants(h *Harness, step Step) []InvariantCheck {
	if len(step.Invariants) > 0 {
		return step.Invariants
	}
	if h.heavyCadence == 0 {
		return DefaultInvariants
	}
	if !h.pinnedClock.Before(h.nextHeavyAt) {
		h.nextHeavyAt = h.nextHeavyAt.Add(h.heavyCadence)
		return DefaultInvariants
	}
	return cheapDefaultInvariants
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
		fm, err := store.LoadThreadFrontmatter(paths, r.ID)
		if err != nil {
			continue
		}
		if n := len(fm.HistorySymbols); n > peak {
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

func indexCheck(paths store.PersonantPaths) (memops.CheckResult, error) {
	return index.Check(paths, index.Options{Quiet: true})
}
