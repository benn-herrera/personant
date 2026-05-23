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
//
// File layout within the package:
//
//   - harness.go (this file): core types (Step, Scenario, Harness,
//     StepSource, RecallAck, ClosureAck, RecallFidelityMode,
//     StepFeedback), the NewMockResponseWithTag constructor, the
//     SwitchProject API, and thin index wrappers.
//   - harness_paths.go: rundata-directory resolution (runDataHome,
//     measurementBlobPath, repoRoot).
//   - harness_setup.go: newHarness + scriptedCurator (per-scenario
//     isolation, clock pinning, telemetry wiring).
//   - harness_run.go: RunScenario, runStep, restartSession, and the
//     final-gauge helpers (foldEventLines, peakHistorySymbols).
//   - harness_resolvers.go: per-step resolver builders
//     (recallResolverFor, closureResolverFor) and the invariant-cadence
//     policy (perStepInvariants, runInvariants).
package scenarios

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/index"
	"personant/internal/memops"
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
	//                     §9.4 metrics blob. Whether a mismatch fails
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
	// rebuilt State. This exercises a CLEAN shutdown/resume lifecycle ONLY,
	// NOT crash recovery: the restart fires at a clean step boundary, after
	// the prior turn fully committed (thread file AND spine both written). A
	// mid-write crash — e.g. the thread file landing ahead of the spine —
	// is NOT covered here and is tracked separately as queued crash-recovery
	// work (sim-vs-reality MAD finding B9 / T0-3).
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
	// defect — the §9.4 baseline comparison is where regressions in
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

	// RecallExpectedForgiven is the count of expected matches that remain
	// recoverable after removing archived / absent-from-spine threads —
	// the same forgiveness the F1/precision path applies in
	// recordRecallFidelity. The recall-episode hit/miss counter MUST use
	// THIS, not raw RecallExpected: an expected sibling the runtime has
	// since archived out of the spine can never produce a
	// `spine.match-fire`, so counting it as a miss penalizes the oracle
	// for naming an archived thread the runtime cannot surface — which
	// makes the unresolved-episode rate track archival rather than real
	// recall loss, and disagrees with how the F1 path already scores the
	// very same step. 0 (the zero-value default) on an unmeasured step
	// and on the zero-feedback drainSteps path, keeping the canonical
	// step stream a pure function of (Seed, Duration, Corpus).
	RecallExpectedForgiven int
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
	// VerifyThreadMetaMatchesSpine, VerifyThreadAccounting) fire
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
