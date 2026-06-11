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
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/metrics"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
	"personant/internal/turn"
)

// threadIndexer is the narrow optional interface a Recaller may satisfy to
// receive incremental per-thread index updates as the run creates threads.
// measure.Service implements it (AddThread). The harness type-asserts the
// installed recaller against this interface once and, when satisfied, calls
// AddThread for every thread a step creates — keeping the embedding index
// current across a run that creates thousands of threads (the session-start
// Prepare alone cannot recall a thread born mid-run). A recaller that does
// not implement it (the nil-embedder default) is simply not driven, so the
// symbolic-only path is unaffected.
type threadIndexer interface {
	AddThread(ctx context.Context, threadID string) error
}

// cacheSweeper is the narrow optional interface a Recaller may satisfy to
// drop persisted recall-cache (.vec) files for archived/absent threads — the
// design §6.4 sleep-cycle hook. measure.Service implements it (SweepCache).
// The harness drives it from runSleepCycle, alongside the substrate gc the
// MemoryOps.Consolidate call performs: the recall cache is operational state
// the adapter's Consolidate cannot reach, so the sweep is invoked on the
// Service the harness already holds. A recaller that does not implement it
// (the nil-embedder default) is not driven.
type cacheSweeper interface {
	SweepCache(ctx context.Context) error
}

// treeRebuilder is the narrow optional interface a Recaller may satisfy to
// (re)build its per-thread within-thread summary trees (#111 / design
// within-thread-summary-hierarchy.md §9.3). measure.Service implements it
// (RebuildTrees). Like cacheSweeper it is operational state the adapter's
// Consolidate cannot reach, so the harness drives it from runSleepCycle —
// OFFLINE, in the sleep pass, never on the turn loop (W6). It runs AFTER the
// cache sweep so the trees summarize a settled leaf set (§5.3 ordering). The
// returned count is the recall_intra_tree_rebuild_calls trip-wire metric
// (§7.2 / fork F-B). A recaller that does not implement it (the nil-embedder
// default) is not driven.
type treeRebuilder interface {
	RebuildTrees(ctx context.Context) (int, error)
}

// intraDivergenceProbe is the narrow optional interface a Recaller may satisfy
// to expose the W1 recall-preservation differential (#111 / design §7.1):
// IntraThreadDivergence runs BOTH the summary-tree descent and the flat scan
// over the engaged thread's leaves and returns the size of their top-Kf leaf
// set difference plus, when that size is >0, the per-probe classification
// ("strict-miss" / "tie" / "tree-mismatch"; empty when 0). measure.Service
// implements it. The harness calls it on intra-probe steps (Step.W1Engaged
// set) on an embedding-live run and accumulates BOTH recall_intra_descent_
// divergence (a REPORTED approximation-drift measure, #119 — a nonzero means
// the approximate tree substituted a within-Kf leaf, not necessarily lost
// recall; exact recall is the #117 tier) AND the
// classification tally (recall_intra_w1_*), at the SAME call site and moment,
// so the two are structurally consistent. Accumulating the class here — not
// reading it from a Service atomic post-run — is what makes the tally survive
// the per-session Service instance churn a RestartSession step causes: each
// RestartSession installs a fresh Service whose W1 atomics start at 0, so a
// probe that classified a strict-miss on an earlier instance would be lost to
// a post-run read of the final instance's atomic (the #111 bug). A recaller
// without it (symbolic-only default) is not probed.
type intraDivergenceProbe interface {
	IntraThreadDivergence(ctx context.Context, queryText, engaged string) (int, string)
}

// cosineOpsReporter is the narrow optional interface a Recaller may satisfy to
// expose the MEASURED recall_query_cosine_ops instrument (#111 / design §7.2):
// CosineOps is the run-total cosine comparisons the embedding recall path
// performed, RecallQueries the number of queries that performed them. The
// harness reads them post-run and emits the per-query average — the empirical
// O(C_main)→O(log) perf bend, counted rather than modeled. measure.Service
// implements it; a recaller without it is simply not reported.
type cosineOpsReporter interface {
	CosineOps() int64
	RecallQueries() int64
}

// SimClockStart is THE anchor of the simulated clock: a Monday at
// 00:00 UTC (sim-time-single-clock.md §3.1). It is the single primitive
// the whole sim time model derives from — current date is the clock,
// elapsed is clock−SimClockStart, the day index is the floored quotient
// (SimDayIndex), and the day-off is the real calendar's Sunday. UTC has
// no DST, so every day is exactly SimDayLength wide and the quotient is
// exact. Both the harness (pinnedClock default) and the sim generator
// (its absolute simNow) anchor here, so the two cannot diverge in
// coordinate system.
//
// A Monday-midnight anchor puts day index 6 (the 7th day) on a Sunday,
// so the weekly day-off falls out of the calendar deterministically; an
// 8h work-start offset (SimWorkdayStart) keeps the workday mid-bucket,
// 8h from either midnight edge, so the ±15min start jitter never spills
// a day into an adjacent bucket.
var SimClockStart = time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)

// SimDayLength is the exact width of one simulated calendar day. The
// single home for the 24h day primitive shared by the harness's derived
// day-close tick, the generator's day-start grid, and the cadence tests.
const SimDayLength = 24 * time.Hour

// SimWorkdayStart is the offset into a midnight bucket at which the work
// day begins (08:00), per §3.3. dayStart(N) = SimClockStart +
// N·SimDayLength + SimWorkdayStart + jitter. RNG-free — added after the
// single jitter draw (§5/m4), so it does not perturb the determinism
// draw order.
const SimWorkdayStart = 8 * time.Hour

// SimDayIndex is the one derivation of "which simulated day": the floored
// number of whole SimDayLength buckets between SimClockStart and clock.
// Both the generator and the harness call THIS — same formula AND same
// value (the harness's pinnedClock is a slaved copy of the generator's
// emitted Step.At, §3.4) — so the day-close tick is unambiguous, not a
// reconstruction that can drift against a rival grid.
func SimDayIndex(clock time.Time) int {
	return int(clock.Sub(SimClockStart) / SimDayLength)
}

// simDayCloseDate is the grid-exact, jitter-free instant at which sim-day
// N closes: SimClockStart + N·SimDayLength (§3.5/M2). The day-close STAMP
// must be this canonical instant, NOT the jittered Step.At that triggered
// the crossing — under the midnight anchor a jittered trigger would
// truncate to different calendar dates either side of midnight and break
// sim_date 24h contiguity. Consecutive grid instants are exactly 24h
// apart by construction, so contiguity holds.
func simDayCloseDate(n int) time.Time {
	return SimClockStart.Add(time.Duration(n) * SimDayLength)
}

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

	// At is the absolute simulated instant of this step's turn — the
	// turn's position on the one simulated clock (sim-time-single-clock.md
	// §3.4). It is the AUTHORITATIVE wire field: runStep SETS
	// pinnedClock = At (a slaved copy of the generator's re-anchored
	// instant), it does NOT integrate TimeDelta. Stamping the absolute
	// instant rather than accumulating a delta is the keystone that makes
	// the single-clock claim mechanically true — there is no independent
	// harness-side accumulator to drift against the generator's clock.
	//
	// The sim generator always carries At. A handwritten scenario that
	// advances relatively need only set TimeDelta: the sliceSource shim
	// stamps At from a running base (SimClockStart + Σ TimeDelta) so the
	// harness drive loop sees At on every step, keeping one authoritative
	// field on the wire. A zero At (no shim, no generator — e.g. a test
	// constructing a Step literal directly and driving runStep) leaves the
	// pinned clock untouched, the pre-At-field behavior.
	At time.Time

	// TimeDelta is the legacy relative advance for handwritten scenarios:
	// the simulated span between this step's turn and the prior one. The
	// sliceSource shim integrates it onto a running base to derive Step.At
	// (the authoritative field, above), so a handwritten scenario's call
	// sites are unchanged. The sim path ignores TimeDelta for clock
	// advancement (it sets pinnedClock from At) but still stamps it for the
	// cadence tests that measure intra-day per-turn spans.
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

	// W1Engaged names the engaged thread whose intra-thread recall-preservation
	// QUALITY MEASURE (#111 / design §7.1; gate→measure in #119) the harness
	// should evaluate on this step. When non-empty AND an embedding recaller is
	// live, the harness calls the recaller's IntraThreadDivergence(ctx,
	// UserInput, W1Engaged) and accumulates recall_intra_descent_divergence — the
	// REPORTED approximation-drift measure of how far the descent's top-Kf leaves
	// drift from the flat scan's (not a hard gate). The sim sets it to the
	// Candidate-A main thread on intra-probe steps. Empty on every other step;
	// the symbolic-only run never evaluates it (no usable tree, divergence is 0
	// by construction). It is observability/gate wiring only — it does not touch
	// the turn, the canonical step stream, or determinism.
	W1Engaged string

	// SleepCycle marks a deterministic ARCHITECTURE.md "sleep cycle" step
	// (task #108): the sim's day-off generator emits one such step per
	// day-off idle window. It carries no turn — UserInput is empty and no
	// LLM consult runs. Instead the harness intercepts it before any turn
	// machinery and drives h.Ops.Consolidate (substrate gc), measuring the
	// .git footprint before and after to prove reclamation. A SleepCycle
	// step is the ONLY step type the harness routes off the turn path; it
	// is seed-independent (fires every day-off) so it does not perturb the
	// workload's (Seed, Duration) determinism contract.
	SleepCycle bool
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

	// RecallMatchFireIDs is the set of thread IDs that fired
	// `spine.match-fire` on the just-run turn (de-duplicated, sorted —
	// matchFireSet semantics). RecallMatchFires == len(RecallMatchFireIDs).
	// A StepSource needs the IDs (not just the count) to score
	// per-candidate coherence: the within-thread wander hop-graded probe
	// (#96) asks "did the runtime surface THIS specific abandoned-topic
	// thread?", which the count alone cannot answer when a collision could
	// fire a different thread. nil on the zero-feedback drainSteps path.
	RecallMatchFireIDs []string

	// EmbedMatchFireIDs is the set of thread IDs that fired
	// `spine.embed-match-fire` on the just-run turn (de-duplicated, sorted —
	// embedMatchFireSet semantics). Non-nil only on an embedding-in-loop run
	// (#98); nil on the symbolic-only default and the zero-feedback drainSteps
	// path. A StepSource that measures per-candidate recall (the #96
	// abandoned-topic wander probe) reads it to record whether the EMBEDDING
	// layer surfaced the specific probed thread, parallel to RecallMatchFireIDs
	// for the symbolic layer — the per-hop head-to-head gap-closure view.
	EmbedMatchFireIDs []string

	// IntraMatchFireIDs is the set of thread IDs that fired
	// `spine.intra-match-fire` on the just-run turn (de-duplicated, sorted —
	// intraMatchFireSet semantics). It is the §7 intra-thread (fine-tier)
	// analogue of EmbedMatchFireIDs: non-empty only when an embedder is
	// installed AND the engaged thread's scrolled-out early content matched
	// the query (the #109 case); nil on the symbolic-only default and the
	// zero-feedback drainSteps path. A StepSource that measures intra-thread
	// recall (the #109 intra-thread probe) reads it to record whether the
	// runtime surfaced the engaged long thread for a probe of its early
	// scrolled-out slot, scored per hop against the shadow-chunk oracle.
	IntraMatchFireIDs []string

	// RecallExpected is the count of ground-truth matches the step
	// declared (len of Step.ExpectedRecallMatches); 0 for an unmeasured
	// step.
	RecallExpected int

	// TargetRecoverable reports whether a thread ID is still on the LIVE
	// recall surface as of this step — i.e. it is present on the live spine,
	// OR it is absent but NOT in the archive.archived log (an unexplained
	// absence, conservatively treated as a real miss). It returns false ONLY
	// for a thread the runtime has archived off the spine, which is off the
	// live recall surface (preserved + recoverable via explicit fetch) and
	// can never produce a `spine.match-fire` again. It is the same
	// archival-recoverability predicate recordRecallFidelity applies to a
	// declared expected set, exposed to a StepSource that measures recall
	// against a target WITHOUT declaring it as an expected match (the #96
	// abandoned-topic probe, which must stay out of the
	// recall_fidelity_adversarial_* series). A StepSource MUST forgive an
	// observation whose target is not recoverable, or it counts archival as
	// an oracle/runtime divergence. nil on the zero-feedback drainSteps path
	// (the source must nil-guard; drainSteps measures nothing).
	TargetRecoverable func(id string) bool

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
//
// It is also the §3.4 shim that stamps the authoritative Step.At for
// handwritten scenarios: a handwritten step sets only the relative
// TimeDelta, so sliceSource integrates a running base (SimClockStart + Σ
// TimeDelta) and stamps At, so the harness drive loop sees At on every
// step and never has to integrate a delta itself. A step that already
// carries At (none today on the slice path, but harmless) is left as-is.
type sliceSource struct {
	steps []Step
	pos   int
	base  time.Time // running At base; zero until the first step seeds it
}

func (s *sliceSource) Next(_ StepFeedback) (Step, bool) {
	if s.pos >= len(s.steps) {
		return Step{}, false
	}
	step := s.steps[s.pos]
	s.pos++
	if step.At.IsZero() {
		// Integrate the running base from SimClockStart by each step's
		// relative TimeDelta, exactly as the pre-At runStep did (it added
		// each step's TimeDelta onto the pinned clock, the Monday anchor,
		// before the turn). The first step lands at SimClockStart +
		// TimeDelta — usually SimClockStart itself, since handwritten steps
		// rarely set a delta on step 0.
		if s.base.IsZero() {
			s.base = SimClockStart
		}
		s.base = s.base.Add(step.TimeDelta)
		step.At = s.base
	}
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

	// OnSimDayClose, when non-nil, is invoked once each time the DERIVED
	// sim-day index (SimDayIndex of the slaved pinnedClock) ticks past a
	// day boundary — i.e. at the close of every sim-day for a sim whose
	// HeavyInvariantCadence is 24h. The harness it receives is the run's
	// live harness (h.Metrics holds the run-to-date counters/histograms,
	// h.Paths the substrate); day is the 1-based sim-day ordinal (1 for the
	// first close, 2 for the second, …) and simDate is the GRID-EXACT close
	// instant simDayCloseDate(N) = SimClockStart + N·SimDayLength — NOT the
	// jittered Step.At that triggered the crossing, so consecutive simDates
	// are exactly 24h apart (sim_date contiguity, §3.5/M2). It fires in
	// lockstep with that day's heavy-invariant firing (one fire per crossed
	// day; the catch-up loop fires each intervening day with its own grid
	// date), AFTER the step's turn has committed and the heavy invariants
	// for that boundary have run, so the run-to-date state the handler reads
	// is settled.
	//
	// The generic harness owns the trigger; the sim supplies the handler
	// (a daily run-to-date stats snapshot, #120). It never fires when
	// HeavyInvariantCadence is 0 (legacy per-step harness users — every
	// handwritten scenario — are unaffected) or when the field is nil.
	OnSimDayClose func(h *Harness, day int, simDate time.Time)

	// Recaller, when non-nil, overrides the default symbolic-only recaller
	// the harness installs on turn.State. It is a factory (not a built
	// instance) because the harness builds State during newHarness AND
	// rebuilds it on a RestartSession step (turn.LoadSession constructs a
	// fresh State with the nil-embedder default) — both install points call
	// this with the run's ops so the embedding recaller survives a simulated
	// relaunch. The harness calls Prepare on the returned recaller after
	// install. When nil the State keeps turn.NewState's symbolic-only default,
	// so every existing scenario is unaffected.
	//
	// This is the embedding-in-loop seam (#98): the live-sim path passes a
	// factory that builds measure.NewService(ops, embedder). A recaller that
	// also satisfies the threadIndexer interface (measure.Service does) gets
	// per-thread-creation index upkeep wired automatically — see runStep.
	Recaller func(ops memops.MemoryOps) measure.Recaller

	// LiveClient, when non-nil, is installed on turn.State.Client INSTEAD of
	// the scripted mock — the inference-in-loop seam (#98, Inc 3). With it set,
	// every turn's ConsultStream hits a real chat endpoint, so the per-step
	// MockResponse is NOT served (runStep skips SetResponse) and the workload's
	// canned bodies are ignored by the runtime. This is a BEHAVIOR-VALIDATION
	// mode ONLY: a real model emits different topic tags / symbols than the
	// canned plan, which both invalidates the recall oracle AND breaks the
	// generator's forward-planning coherence (it plans the next step's thread
	// engagement from canned tags). The live-sim caller therefore runs it short
	// and SKIPS every oracle/coherence-dependent gate. When nil the harness
	// keeps the scripted mock, so every existing scenario is unaffected.
	LiveClient model.Client

	// LiveModel is the model name set on turn.State.Model when LiveClient is
	// installed, overriding the sentinel harness provider's DefaultModel so the
	// live request names the real chat model. Ignored when LiveClient is nil.
	LiveModel string

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
	// `thread.created` IDs and `archive.archived` IDs
	// respectively. They replace the per-call full log walks the
	// VerifyThreadAccounting invariant and the recall-fidelity
	// archival-forgiveness filter used to perform. Both sets only ever
	// grow, so they are insensitive to which poll window an event lands
	// in — only the per-step match-fire set is window-sensitive.
	createdThreadIDs  map[string]struct{}
	archivedThreadIDs map[string]struct{}

	// heavyCadence is the Scenario.HeavyInvariantCadence the run was
	// started with. Zero = fire heavy invariants every step (legacy
	// behavior). When > 0 the harness folds BOTH the heavy-invariant
	// firing AND the day-close onto the DERIVED day tick (SimDayIndex of
	// the slaved pinnedClock), not a rival nextHeavyAt grid (§3.5/M1).
	heavyCadence time.Duration

	// lastClosedDay is the SimDayIndex of the last sim-day the harness has
	// closed (fired the heavy invariants + onSimDayClose for). After a
	// step sets pinnedClock = Step.At, runStep computes cur :=
	// SimDayIndex(pinnedClock) and closes every day in (lastClosedDay,
	// cur] — one heavy-invariant firing + one onSimDayClose per crossed
	// day, each stamped with its own grid-exact date simDayCloseDate(k).
	// In normal operation §3.3's 8am mid-bucket anchor means at most one
	// crossing per step; the catch-up loop is correct by construction for
	// the legitimate multi-day jump (a future vacation path) too. Starts
	// at 0 — day 0 is the run's first day and never "closes" before any
	// turn runs. Meaningless when heavyCadence == 0.
	lastClosedDay int

	// onSimDayClose is Scenario.OnSimDayClose — the per-sim-day-close handler
	// (#120). Fired once per crossed derived-day tick, in lockstep with the
	// day's heavy-invariant firing. simDay is the running 1-based count of
	// closes fired so far; it is the `day` argument passed to the handler.
	// nil/zero-cadence => never fires.
	onSimDayClose func(h *Harness, day int, simDate time.Time)
	simDay        int

	// recallerFactory is Scenario.Recaller — retained so restartSession can
	// re-install the same custom recaller after turn.LoadSession rebuilds a
	// nil-embedder State. nil when the scenario kept the default recaller.
	recallerFactory func(ops memops.MemoryOps) measure.Recaller

	// indexer is the installed recaller's incremental-index seam, set iff the
	// recaller satisfies threadIndexer (the embedding measure.Service does).
	// nil for the symbolic-only default. runStep drives it per thread created.
	indexer threadIndexer

	// liveClient is Scenario.LiveClient — the inference-in-loop chat client
	// (#98). When non-nil it is installed on State.Client instead of the
	// scripted mock; runStep then skips the per-step SetResponse and
	// restartSession rebuilds the session against it (not the mock). liveModel
	// rides along onto State.Model. nil on every mock scenario.
	liveClient model.Client
	liveModel  string
}

// installRecaller builds the scenario's custom recaller (if any), installs
// it on the given State, captures its incremental-index seam, and Prepares
// it. Shared by newHarness's initial install and restartSession's
// post-relaunch re-install so the embedding recaller — and a primed index —
// survives a simulated shutdown→relaunch. A no-op when no factory is set
// (the symbolic-only default State.Recaller stays in place). A Prepare
// failure is fatal: an embedding run with an unbuilt index would silently
// measure zero embedding recall, masquerading as a gap-closure failure.
func (h *Harness) installRecaller(t testing.TB, state *turn.State) {
	t.Helper()
	if h.recallerFactory == nil {
		return
	}
	// Close any prior recaller so a restart does not leak its indexer
	// goroutine; the default symbolic-only Close is a no-op.
	if state.Recaller != nil {
		_ = state.Recaller.Close()
	}
	r := h.recallerFactory(h.Ops)
	state.Recaller = r
	if ix, ok := r.(threadIndexer); ok {
		h.indexer = ix
	}
	if err := r.Prepare(context.Background()); err != nil {
		t.Fatalf("install recaller: Prepare: %v", err)
	}
	// Stop the indexer goroutine at test end (the final live recaller; a
	// restart already closed the prior one above). h.State is the source of
	// truth — restartSession swaps it — so read it at cleanup time.
	t.Cleanup(func() {
		if h.State != nil && h.State.Recaller != nil {
			_ = h.State.Recaller.Close()
		}
	})
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

// PinnedClock returns the harness's current pinned simulated time — the instant
// turn.State's clock returns, advanced by each Step.TimeDelta. Post-run it is the
// run's final simulated instant. Read-only; used by the #120 daily-snapshot
// finalize to stamp the last record's sim_date. Safe to call post-run (runStep is
// single-goroutine within RunScenario, which has returned by then).
func (h *Harness) PinnedClock() time.Time {
	return h.pinnedClock
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
