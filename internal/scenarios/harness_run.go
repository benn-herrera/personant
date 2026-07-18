package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	pnlog "personant/internal/log"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
	"personant/internal/turn"
)

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

	// Run manifest: bind this rundata directory to the run that produces it
	// — mode, provenance, wall-clock span, git HEAD — BEFORE the first step,
	// so even a crashed/aborted run leaves a self-describing directory (End
	// stays empty on a run that never completed). Root cause served: a 74 s
	// mock rung's directory was indistinguishable from a live-inference
	// run's, mis-binding numbers to runs (burndown 2026-07b).
	manifestStart := clock.Profiling().UTC().Format(time.RFC3339)
	writeRunManifest(h, sc, manifestStart, "")

	// The mock holds a single current-response slot. runStep sets it from
	// each step's MockResponse before driving the turn; every consult
	// within that turn — including a §5.5 mid-turn re-prompt (a second,
	// legitimate consult triggered when a topic tag names a thread not in
	// Layer B) — re-serves that one response. The re-prompt re-issues the
	// same request; once the missing thread is fetched the topic tag
	// drains cleanly. No pre-queue, so an on-demand StepSource needs no
	// up-front step count.
	h.Mock = model.NewScriptedMock(nil, nil)
	if h.liveClient != nil {
		// Inference-in-loop (#98, Inc 3): drive turns against a real chat
		// endpoint instead of the scripted mock. The mock is still constructed
		// (some helpers reference it) but never installed on State; runStep
		// skips SetResponse and restartSession rebuilds against the live client.
		// State.Model names the real chat model so the request does not carry
		// the sentinel harness DefaultModel.
		h.State.Client = h.liveClient
		h.State.Model = h.liveModel
	} else {
		h.State.Client = h.Mock
	}

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
	// #94 R3: end-of-run loose-object level — with per-turn commits this is
	// the accrual since the last sleep-cycle gc, the gc-cadence watch metric.
	h.Metrics.Set(metricGitLooseObjectsFinal, float64(looseObjectCount(h.Paths.Home)))

	// Recovery-fetch gauge (design §6.3, AC1): the honest "recoverable,
	// measured" signal. Runs ONCE here — AFTER the measured run and AFTER the
	// final invariant sweep — because it mutates the substrate (it recovers
	// threads back onto the spine), so it must not perturb any measured series
	// or trip VerifyThreadAccounting. Drives RecoverThread under realistic
	// accumulated state; the adapter verifies the tree hash internally.
	runRecoveryGauge(t, h)

	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Errorf("scenario %s: metrics write: %v", sc.Name, err)
	}

	// Manifest completion stamp: rewrite with End filled. A manifest whose
	// End is empty is the durable signature of a run that never completed.
	writeRunManifest(h, sc, manifestStart, clock.Profiling().UTC().Format(time.RFC3339))

	if t.Failed() {
		// Re-emit the path on failure so it's adjacent to the verdict
		// line in a long test log.
		t.Logf("scenario %s FAILED — metrics blob: %s", sc.Name, h.MetricsPath)
	}

	return h
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

	// Sleep-cycle steps (#108) carry no turn: route them off the turn path
	// entirely. The harness owns h.Ops, so the substrate consolidation runs
	// here (the generator is a stateless StepSource that cannot reach Ops).
	// Returns a zero-but-indexed feedback so downstream bucketing treats it
	// as a real, no-recall-opportunity step (Index >= 0, RecallExpected 0).
	if step.SleepCycle {
		runSleepCycle(t, h, idx, label)
		return StepFeedback{Index: idx}
	}

	stepSetup(t, h, idx, label, step)
	preSpine, _ := store.ReadSpine(h.Paths.Spine)
	stepSetClock(h, idx, label, step)

	body, elapsed := stepExecTurn(t, h, idx, label, step)

	postSpine, _ := store.ReadSpine(h.Paths.Spine)
	stepUpdateEmbeddingIndex(t, h, idx, label, preSpine, postSpine)

	stepLines := stepScrapeEventLog(t, h, idx, label, preSpine, postSpine)
	// Forensic raw-response retention: on a live-inference rung a tag-miss
	// turn (topic.tag-missing / topic.tag-invalid in stepLines) has body ==
	// the raw model response — turn.Run strips the tag only when a valid one
	// parses, so nothing was stripped here. Persist it for post-run
	// decomposition of the tag-omission population. Live-only.
	stepCaptureTagMiss(h, idx, body, stepLines)
	livePost := spineIDSet(postSpine)
	rec := stepMeasureRecall(t, h, idx, label, step, stepLines, livePost)
	stepRecordMetrics(h, step, elapsed, preSpine, postSpine)

	runInvariants(t, h, perStepInvariants(h, step), label)
	stepCloseDays(h)

	return StepFeedback{
		Index:                  idx,
		RecallMatchFires:       len(rec.matchFires),
		RecallMatchFireIDs:     rec.matchFires,
		EmbedMatchFireIDs:      rec.embedFireIDs,
		IntraMatchFireIDs:      rec.intraFires,
		RecallExpected:         len(step.ExpectedRecallMatches),
		TargetRecoverable:      stepRecoverable(h, livePost),
		RecallExpectedForgiven: rec.expectedForgiven,
		// Burndown #8: expose the runtime's authoritative Layer-B set so a
		// shadow-keeping StepSource can cross-check. Copy it — ActiveThreads is
		// turn.State's live, mutable slice; the source must read a stable
		// snapshot, not alias state that the next turn rewrites.
		RuntimeLayerB: append([]string(nil), h.State.ActiveThreads...),
	}
}

// stepSetup runs the pre-turn phase: memory telemetry sampling, simulated
// clean restart, mock-response install, and per-step resolver install. It
// leaves h.State ready for the turn.
func stepSetup(t *testing.T, h *Harness, idx int, label string, step Step) {
	t.Helper()
	// Sample memory telemetry BEFORE the step executes so even a step
	// that crashes is preceded on disk by its memory-state record. Sample
	// at step 0 unconditionally (the run's baseline) and then every
	// memSampleEveryNSteps; for short scenarios (handful of steps) this
	// means only the baseline + a final sample at close, which is exactly
	// what a smoke run wants.
	if idx == 0 || idx%memSampleEveryNSteps == 0 {
		h.mem.sample(idx, h.pinnedClock)
	}

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
	// Under inference-in-loop (#98) the live client is installed instead, so
	// the canned response is neither served nor relevant — skip it.
	if h.liveClient == nil {
		h.Mock.SetResponse(step.MockResponse)
	}

	h.State.RecallResolver = recallResolverFor(t, idx, label, step.RecallAck)
	h.State.ClosureResolver = closureResolverFor(step.ClosureAck)
}

// stepSetClock slaves the pinned clock to the step's simulated instant.
// sim-time-single-clock.md §3.4 / burndown #3: there is exactly ONE
// clock-advance path — SET pinnedClock = Step.At. pinnedClock becomes a copy
// of the generator's one re-anchored clock, so the two cannot diverge by
// construction (there is no independent harness accumulator to drift).
//
// At is mandatory: every emitted step carries an absolute At. The sliceSource
// shim stamps it for handwritten scenarios (SimClockStart + Σ TimeDelta), and
// every sim step stamps its own turnInstant — EXCEPT the generator's
// execution-time-injected refinement step (workload.go buildRefinementStep),
// which the day-ahead generation buffer cannot stamp: the generator's simNow
// is the generation frontier, running AHEAD of execution, so it does NOT know
// the prior-EXECUTED turn's instant. The refinement's correct reference is the
// instant of the turn it follows — which is exactly h.pinnedClock at the
// moment this runs (the prior executed step set it from its own At). So when a
// step arrives At-less carrying only TimeDelta, NORMALIZE its At here from the
// authoritative executed instant (pinnedClock + TimeDelta) BEFORE the single
// set. This makes the refinement's advance TRANSIENT: the next buffered step
// carries its own absolute At (>= this one, mid-day, far from any day-close),
// re-anchoring the clock to the planned timeline. There is no rival integrate
// branch — relative steps are normalized to absolute, then the one set runs.
//
// Forensic monotonic assert (§4/m3): At must never land before the prior
// clock — a backward jump would mean a re-anchor landed before a prior turn
// (the original-bug shape the deleted `td < 0 → 0` clamp used to swallow).
// Non-fatal: route through the log, not t.Fatalf — it is a tripwire, not a
// gate.
func stepSetClock(h *Harness, idx int, label string, step Step) {
	at := step.At
	if at.IsZero() {
		// Execution-time refinement step: stamp At from the authoritative
		// executed instant (the prior turn's pinnedClock) + its relative gap.
		at = h.pinnedClock.Add(step.TimeDelta)
	}
	if at.Before(h.pinnedClock) {
		pnlog.Warn("scenario step %d (%s): non-monotonic clock: Step.At %s is before prior pinnedClock %s",
			idx+1, label, at.Format(time.RFC3339Nano), h.pinnedClock.Format(time.RFC3339Nano))
	}
	h.pinnedClock = at
}

// stepExecTurn drives the turn end-to-end and returns the streamed body and
// the measured wall time. A turn.Run error is fatal — the runtime hit a bug
// the scenario can't recover from.
//
// It drives turn.RunWithInfo (not RunWithDeltas) so the per-turn
// TurnInfo.PromptTokens — the provider's count for the FULLY-assembled request
// (system + history + userInput + tool-result deltas) — is observable. On a
// live-inference run that number is the only honest measurement of the
// assembled-request size and the source for the B1+X4 token-ceiling gate
// (X4-PROD FM3: a byte-budget-derived or composed-memory-only count is
// true-by-construction). One sample/turn is recorded into request_prompt_tokens
// — but ONLY when a live chat client is installed: the mock client reports a
// canned PromptTokens=8 (mockllm.go), so recording it on the mock path would
// both inject a vacuous series into the gate AND perturb the mock metrics blob.
// Gating the Record on h.liveClient keeps the mock run byte-identical (rung
// invariant 8) and the gate non-vacuous (rung invariant 4).
func stepExecTurn(t *testing.T, h *Harness, idx int, label string, step Step) (string, time.Duration) {
	t.Helper()
	start := clock.Profiling()
	body, info, err := turn.RunWithInfo(context.Background(), h.State, step.PreEvents, step.UserInput, io.Discard)
	elapsed := clock.Since(start)
	if err != nil {
		t.Fatalf("scenario step %d (%s): turn.Run: %v", idx+1, label, err)
	}
	if h.liveClient != nil {
		h.Metrics.Record(MetricRequestPromptTokens, float64(info.PromptTokens))
	}
	// Per-turn commit latency (#94 R3, the design's open cost question).
	// The runtime measures its own CommitTurn wall-clock (TurnInfo); one
	// sample per turn, mock and live alike — the commit is equally real on
	// both paths. Harness-local key: written here, read from the metrics
	// blob (no sim-side consumer yet), so per the registry discipline it
	// stays out of metric_keys.go until a cross-seam reader exists.
	h.Metrics.Record(metricTurnCommitMs, float64(info.CommitDuration.Microseconds())/1000.0)
	// The other half of the measured per-turn crash-stability overhead:
	// the two fsynced journal appends (prompt + response), summed by the
	// runtime into TurnInfo.JournalDuration. Same one-sample-per-turn
	// discipline as turn_commit_ms.
	h.Metrics.Record(metricTurnJournalMs, float64(info.JournalDuration.Microseconds())/1000.0)
	// The post-commit loose-object pressure gc (MaybeGC), measured by the
	// runtime OUTSIDE the CommitDuration window (TurnInfo.GCDuration) so it
	// no longer inflates turn_commit_ms. Zero on turns where the throttled
	// check does not fire; a spike marks a turn that carried a repack.
	h.Metrics.Record(metricTurnGCMs, float64(info.GCDuration.Microseconds())/1000.0)
	// Day-barrier gauges (#94 R3b §6.3): the PRE-TURN poll inside
	// turn.RunWithInfo is where a sim's day boundary usually seals (the
	// first step past midnight runs before the harness's day-close
	// catch-up), so the measurements surface via TurnInfo. Cross-seam
	// keys: the sim's rung summary and the 2d barrier rung read them.
	if b := info.Barrier; len(b.DaysSealed) > 0 {
		for range b.DaysSealed {
			h.Metrics.Counter(MetricBarrierCount, 1)
		}
		h.Metrics.Record(MetricBarrierDurationMs, float64(b.BarrierDuration.Microseconds())/1000.0)
		h.Metrics.Record(MetricDayCommitDurationMs, float64(b.DayCommitDuration.Microseconds())/1000.0)
		h.Metrics.Record(MetricMorningInitDurationMs, float64(b.MorningInitDuration.Microseconds())/1000.0)
		h.Metrics.Record(MetricDayCommitBytes, float64(b.DayCommitBytes))
		h.Metrics.Record(MetricDailyLooseObjects, float64(b.DailyLooseObjects))
	}
	return body, elapsed
}

// Harness-local crash-stability cost keys (#94 R3). turn_commit_ms is the
// per-turn CommitTurn latency histogram and turn_journal_ms its journal
// counterpart (the turn's two fsynced appends); turn_gc_ms is the
// post-commit pressure-gc cost (MaybeGC), split out of turn_commit_ms so a
// repack no longer contaminates the commit gauge; git_loose_objects_{pre,post}_gc
// gauge loose-object accrual at each sleep cycle — the gc-interplay watch
// metric (per-turn commits make loose-object growth ~turns/day, and the
// sleep gc is the stated hard dependency that bounds it).
const (
	metricTurnCommitMs         = "turn_commit_ms"
	metricTurnJournalMs        = "turn_journal_ms"
	metricTurnGCMs             = "turn_gc_ms"
	metricGitLooseObjectsPre   = "git_loose_objects_pre_gc"
	metricGitLooseObjectsPost  = "git_loose_objects_post_gc"
	metricGitLooseObjectsFinal = "final_git_loose_objects"
	// R3-addendum item 4: packed-size + repack-duration growth gauges
	// beside the loose-object ones. Harness-local per the registry
	// discipline (written here, read from the metrics blob).
	metricGitPackedBytesPre  = "git_packed_bytes_pre_gc"
	metricGitPackedBytesPost = "git_packed_bytes_post_gc"
	metricGitGCRepackMs      = "gc_repack_ms"
)

// stepUpdateEmbeddingIndex keeps the embedding index current (#98): a thread
// created this turn must be embedded and added to the layer-2 index, or it is
// structurally unrecallable by embedding for the rest of the run (the
// session-start Prepare embedded only the threads that existed then). Drives
// the incremental AddThread seam for each spine ID that appeared this turn. A
// no-op for the symbolic-only default (h.indexer nil). An embed failure is
// fatal: a silently-missing index entry would understate embedding recall and
// corrupt the symbolic-vs-embedding head-to-head.
func stepUpdateEmbeddingIndex(t *testing.T, h *Harness, idx int, label string, preSpine, postSpine []memops.SpineRecord) {
	t.Helper()
	if h.indexer == nil || len(postSpine) <= len(preSpine) {
		return
	}
	for _, id := range newSpineIDs(preSpine, postSpine) {
		if err := h.indexer.AddThread(context.Background(), id); err != nil {
			t.Fatalf("scenario step %d (%s): embedding index add %s: %v", idx+1, label, id, err)
		}
	}
}

// stepScrapeEventLog performs one incremental poll capturing exactly this
// turn's appended log lines (the turn just completed and the next turn has
// not started), folds thread.created IDs into the cumulative created set, and
// refreshes the index-derived archived set whenever an archival ran. Returns
// the freshly-tailed lines for recall measurement. Invariant checks between
// turns do not write to logs/, so they cannot pollute the next step's window.
func stepScrapeEventLog(t *testing.T, h *Harness, idx int, label string, preSpine, postSpine []memops.SpineRecord) []string {
	t.Helper()
	stepLines, err := h.tailer.poll()
	if err != nil {
		t.Fatalf("scenario step %d (%s): tailer.poll: %v", idx+1, label, err)
	}
	h.foldEventLines(stepLines)
	// Spine-shrink is the second, log-independent refresh trigger (F4): if
	// the spine lost records this turn an archival drain ran, so resync the
	// index-derived archived set even if the forensic archive.archived line
	// was dropped or partially emitted. This is what makes the measurement
	// set independent of the event log's completeness.
	if len(postSpine) < len(preSpine) {
		h.refreshArchivedSet()
	}
	return stepLines
}

// stepRecall bundles the recall-measurement outputs of one step that
// runStep threads into the returned StepFeedback.
type stepRecall struct {
	matchFires       []string
	embedFireIDs     []string
	intraFires       []string
	expectedForgiven int
}

// stepMeasureRecall scores every recall layer for this step: the symbolic
// Jaccard layer (recall_fidelity_*, the gate), the embedding head-to-head
// (#98), the intra-thread fine tier (#109), and the W1 descent-vs-flat
// quality measure (#111). livePost is the in-scope live-spine ID set, passed
// to the recall-fidelity scorer to avoid a redundant ReadSpine (#11).
func stepMeasureRecall(t *testing.T, h *Harness, idx int, label string, step Step, stepLines []string, livePost map[string]struct{}) stepRecall {
	t.Helper()
	matchFires := matchFireSet(stepLines)
	keptExpected, expectedForgiven := recordRecallFidelity(t, h, idx, label, step.RecallMode, step.ExpectedRecallMatches, matchFires, livePost)

	// Embedding-in-loop head-to-head (#98): when an embedder is installed the
	// turn-close path logs spine.embed-match-fire alongside spine.match-fire,
	// so parse the embedding match set and score it against the SAME forgiven
	// expected set the symbolic path scored (keptExpected), into the parallel
	// embed_recall_fidelity_* series. This runs only when the embedding layer
	// is live (h.indexer != nil), keeping the symbolic-only default's metrics
	// blob unchanged. Symbolic recall is still recorded above on the same step
	// — apples-to-apples on one workload, one forgiven ground truth.
	embedFires := embedMatchFireSet(stepLines)
	if h.indexer != nil {
		recordEmbedRecallFidelity(h, keptExpected, embedFires, step.ExpectedRecallMatches != nil)
	}

	// Intra-thread (#109) fine-tier match set: which engaged threads the
	// runtime surfaced via spine.intra-match-fire this turn. Parsed
	// unconditionally (cheap); threaded back ONLY on an embedding-in-loop run
	// (h.indexer != nil), per the field contract ("non-nil only on an
	// embedding-in-loop run"). On the symbolic-only default the intra layer is
	// off and never fires, so a non-nil empty slice would falsely signal the
	// layer is live to a StepSource that gates its coherence tally on liveness.
	var intraFires, embedFireIDs []string
	if h.indexer != nil {
		intraFires = intraMatchFireSet(stepLines)
		embedFireIDs = embedFires
	}

	stepMeasureW1(h, step)

	return stepRecall{
		matchFires:       matchFires,
		embedFireIDs:     embedFireIDs,
		intraFires:       intraFires,
		expectedForgiven: expectedForgiven,
	}
}

// stepMeasureW1 records the W1 recall-preservation QUALITY MEASURE (#111 /
// design §7.1; gate→measure in #119): on an intra-probe step (W1Engaged set)
// on an embedding-live run, ask the recaller for the descent-vs-flat top-Kf
// set difference for the engaged main thread and accumulate it. The sim
// summary REPORTS this as an approximation-drift canary (no longer a hard
// assertion); here we only tally the per-step differential. Gated on h.indexer
// (an embedding recaller is live) and the optional intraDivergenceProbe
// interface (measure.Service satisfies it) so the symbolic-only default is
// untouched. Counted unconditionally when a usable tree exists — divergence
// is 0 by construction otherwise (W8).
func stepMeasureW1(h *Harness, step Step) {
	if h.indexer == nil || step.W1Engaged == "" || h.State == nil || h.State.Recaller == nil {
		return
	}
	probe, ok := h.State.Recaller.(intraDivergenceProbe)
	if !ok {
		return
	}
	div, class := probe.IntraThreadDivergence(context.Background(), step.UserInput, step.W1Engaged)
	h.Metrics.Counter(MetricRecallIntraDescentDivergence, int64(div))
	h.Metrics.Counter(MetricRecallIntraDescentProbes, 1)
	// Accumulate the per-probe classification tally HERE, in lockstep with the
	// divergence counter, so a strict-miss classified on an earlier
	// per-session Service instance is not lost when a RestartSession swaps in a
	// fresh Service with zeroed W1 atomics (the #111 bug). div==0 returns the
	// empty class and bumps nothing.
	recordW1Class(h, class)
}

// stepRecordMetrics records the per-step turn/engagement/response counters.
func stepRecordMetrics(h *Harness, step Step, elapsed time.Duration, preSpine, postSpine []memops.SpineRecord) {
	h.Metrics.Counter(MetricTurns, 1)
	h.Metrics.Record(MetricTurnDurationMs, float64(elapsed.Milliseconds()))
	if len(postSpine) > len(preSpine) {
		h.Metrics.Counter(MetricThreadsCreated, int64(len(postSpine)-len(preSpine)))
	} else if len(postSpine) == len(preSpine) {
		// Existing-thread engagement (or no engagement at all). Don't
		// double-count: only mark engagement if the body itself implies
		// a tag was present and at least one record's turn_count moved.
		h.Metrics.Counter(MetricEngagedExistingThreads, 1)
	}

	// Response size + topic-tag presence accounting. Under mock inference these
	// read the canned step.MockResponse (the bytes the mock served, the tag the
	// plan emitted) — cheap cross-version drift signal. Under inference-in-loop
	// (#98) the canned response is NOT what the model produced, so these mock-
	// derived figures are meaningless: the REAL model's response bytes never
	// reach the harness (turn.Run streams + tag-strips the body), and its tag
	// discipline is measured authoritatively from the runtime's own
	// `topic.tag-missing` log line (counted by the live behavior-validation
	// summary), not from the canned plan. Honest-accounting caveat:
	// `topic.tag-missing` fires only for a turn's FINAL response — a
	// D6-recovered omission (re-prompt succeeded) leaves no tag-missing line,
	// so true model omissions = topic.tag-missing +
	// topic.re-prompt(cause=missing-tag), stream-level; a double-miss turn
	// emits BOTH lines, so the two series must not be naively summed into a
	// turn-denominated rate (see reportInferenceBehavior). Skip them in live
	// mode to avoid recording the plan as if it were the model.
	if h.liveClient == nil {
		h.Metrics.Record("response_bytes", float64(len(step.MockResponse.Content)))
		if strings.Contains(step.MockResponse.Content, "*topic:") {
			h.Metrics.Counter("topic_tag_parsed", 1)
		} else {
			h.Metrics.Counter("topic_tag_missing", 1)
		}
	}
}

// stepCloseDays runs the per-sim-day-close catch-up (§3.5/M1, #120). The step
// set pinnedClock = Step.At above, and the boundary's heavy invariants have
// now run on settled state. Close every sim-day the DERIVED day index crossed,
// dating each close with the day it REPRESENTS: when the index has advanced
// from lastClosedDay to cur, days lastClosedDay..cur-1 are now complete, so
// fire each day d's close stamped with its OWN grid-exact date
// simDayCloseDate(d) (NOT d+1, and NOT the jittered Step.At — §3.5/M2
// preserves sim_date 24h contiguity). This is the labeling fix: day d's
// just-completed work is dated the day d it represents, so the 0-turn weekly
// day-off (Sunday, day index 6) lands ON the Sunday record rather than the
// Monday after it. In normal operation §3.3's 8am mid-bucket anchor crosses
// exactly one boundary per step; the loop is correct by construction for a
// legitimate multi-day jump (the future vacation path) too, each intervening
// day carrying its own grid date. Gated on a non-zero cadence (heavyCadence>0):
// handwritten per-step scenarios never close days.
func stepCloseDays(h *Harness) {
	if h.heavyCadence <= 0 {
		return
	}
	cur := SimDayIndex(h.pinnedClock)
	for d := h.lastClosedDay; d < cur; d++ {
		if h.onSimDayClose != nil {
			h.simDay++
			h.onSimDayClose(h, h.simDay, simDayCloseDate(d))
		}
	}
	h.lastClosedDay = cur
}

// stepRecoverable builds the archival-recoverability predicate for a
// StepSource that measures recall against a target it did NOT declare as an
// expected match (the #96 abandoned-topic probe). It applies the shared
// classifyRecoverability rule (#7) so it can never drift from
// recordRecallFidelity's expected-set filter: a target on the live spine is
// recoverable; one absent AND archived is off the LIVE recall surface
// (excluded — preserved + recoverable via explicit fetch, not lost); one
// absent but NOT archived is an unexplained absence — a genuine integrity bug,
// counted via recall_unexplained_absence (the recall-measurement sibling of
// VerifyThreadAccounting) and conservatively treated as a real miss
// (returns true).
//
// recall_unexplained_absence is a HARD ==0 gate in the sim (#100): the oracle
// no longer manufactures false positives here. Previously a long-sim
// refinement burst could name an expected thread the instant before its spine
// record materialized (the day-ahead generation buffer creates the thread in
// the shadow model before its creating step executes), inflating this counter
// for a thread that was never archived and ended up on-spine. The refinement
// oracle now excludes unmaterialized threads (recallExpectedForMaterialized),
// so a nonzero count is a real off-spine-not-archived loss, not an oracle
// artifact. livePost is built from postSpine (already read) so no extra spine
// read.
func stepRecoverable(h *Harness, livePost map[string]struct{}) func(string) bool {
	return func(id string) bool {
		switch classifyRecoverability(id, livePost, h.archivedThreadIDs) {
		case recovOnSpine:
			return true
		case recovArchived:
			return false
		default: // recovUnexplained
			h.Metrics.Counter(MetricRecallUnexplainedAbsence, 1)
			return true
		}
	}
}

// linesShowTagMiss reports whether this turn's freshly-tailed log lines
// carry a topic.tag-missing or topic.tag-invalid event — the runtime's
// authoritative signal that the turn's FINAL response bound no valid
// §5.1.2 tag. tag-missing fires for every tag-less final response;
// tag-invalid is the additive near-miss forensic (chain.go). Either means
// turn.Run performed no tag-strip, so the returned body is the raw model
// response worth retaining.
func linesShowTagMiss(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "topic.tag-missing ") || strings.Contains(l, "topic.tag-invalid ") {
			return true
		}
	}
	return false
}

// stepCaptureTagMiss persists the raw model response body of a live-
// inference tag-miss turn to test/rundata/<scenario>/tagmiss/<turn>.txt
// (h.RunHome + tagmiss/, 1-based turn number), for post-run decomposition
// of the ~34% live tag-omission figure (design burndown-2026-07b).
//
// Live-only, mirroring the liveClient gating precedent (#98): on the mock
// path body is a canned plan response, not a real model emission, so
// capturing it would retain noise — a mock/symbolic rung never captures.
//
// Observability note: this captures the FINAL stream's fully-accumulated
// body (the value turn.Run returns). On a tag-miss turn that value is the
// RAW pre-filter response — the stream filter only strips the tag from the
// user-visible out, and turn.Run's own tag-strip is a no-op when Parse
// fails — so no runtime hook is needed. The one thing NOT observable here
// is a D6 re-prompt's aborted FIRST stream, which turn.Run closes and
// discards internally; but the turn's tag outcome is set by its final
// response, which is exactly what is retained.
func stepCaptureTagMiss(h *Harness, idx int, body string, stepLines []string) {
	if h.liveClient == nil {
		return
	}
	if !linesShowTagMiss(stepLines) {
		return
	}
	dir := filepath.Join(h.RunHome, "tagmiss")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.T.Errorf("stepCaptureTagMiss: mkdir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.txt", idx+1))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		h.T.Errorf("stepCaptureTagMiss: write %s: %v", path, err)
	}
}

// spineIDSet projects spine records to a set of their thread IDs.
func spineIDSet(recs []memops.SpineRecord) map[string]struct{} {
	out := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		out[r.ID] = struct{}{}
	}
	return out
}

// restartSession simulates a CLEAN application shutdown→relaunch ONLY —
// it is NOT a crash-recovery test. The restart fires at a clean step
// boundary, after the previous turn fully committed both its thread file
// and the spine; the substrate on disk is internally consistent. A
// mid-write crash (thread file written but spine not yet, or vice versa)
// is deliberately out of scope here and is tracked as queued
// crash-recovery work (sim-vs-reality MAD finding B9 / T0-3). When that
// work lands, its coverage belongs in a distinct scenario that injects a
// torn write, not in this clean-lifecycle path.
//
// Mechanics: it pointer-caches the pre-shutdown turn.State as a full
// snapshot (nothing mutates it after replacement), rebuilds a fresh State
// from the substrate via turn.LoadSession exactly as a real process
// launch would,
// re-installs the harness's scripted Curator (newHarness installs it on
// the original State; the rebuild needs the same), swaps it into the
// harness, and asserts the should-survive subset was reconstructed via
// the canonical AssertSessionRestored comparator. Mismatches are reported
// via t.Errorf so the run collects every problem.
func restartSession(t *testing.T, h *Harness, idx int, label string) {
	t.Helper()
	before := h.State

	// Rebuild against the client the run is driving: the live chat client under
	// inference-in-loop (#98), else the scripted mock. A relaunched real runtime
	// would re-resolve its live client too, so the live model name is re-applied
	// below — without it LoadSession's State would revert to the sentinel
	// harness DefaultModel.
	client := model.Client(h.Mock)
	if h.liveClient != nil {
		client = h.liveClient
	}
	rebuilt, err := turn.LoadSession(context.Background(), h.Ops, h.Project, h.Provider, client)
	if err != nil {
		t.Fatalf("scenario step %d (%s): simulated relaunch: turn.LoadSession: %v", idx+1, label, err)
	}
	if h.liveClient != nil {
		rebuilt.Model = h.liveModel
	}
	// Re-install the deterministic scripted curator — newHarness installs
	// it on the original State, and a relaunched runtime would re-install
	// its curator too. The per-step RecallResolver/ClosureResolver are
	// installed by runStep below, so they need no handling here.
	rebuilt.Curator = scriptedCurator{}

	// Re-install the scenario's custom recaller — turn.LoadSession built
	// rebuilt with turn.NewState's nil-embedder default, so without this an
	// embedding-in-loop run would silently revert to symbolic-only after the
	// first RestartSession step. installRecaller re-Prepares the index from
	// the current substrate, so embedding recall resumes against every thread
	// that exists at relaunch. A no-op when no factory is set.
	h.installRecaller(t, rebuilt)

	// Drain the pre-shutdown session's §6.5 flush cost into the run-total
	// counter BEFORE the State is discarded (#126): a relaunch rebuilds a fresh
	// State whose flush counters reset to 0, so the count this ended session
	// accrued would be lost otherwise. The new session resumes counting from 0;
	// the LIVE read (flushCostRunToDate) adds the current session's not-yet-
	// drained count on top of this counter, so the run-to-date total is always
	// correct without ever draining (and double-counting) the live session.
	foldEndedSessionFlushCost(h)

	h.State = rebuilt

	if err := AssertSessionRestored(before, rebuilt); err != nil {
		t.Errorf("scenario step %d (%s): simulated relaunch: %v", idx+1, label, err)
	}
}

// foldEventLines folds a batch of tailed log lines into the harness's
// cumulative thread-ID sets. Called by runStep after each turn's poll and
// by tests that seed log lines directly.
//
// createdThreadIDs is built incrementally from `thread.created` lines (these
// only ever accrue; a partial-emit window for creation is harmless to
// accounting).
//
// archivedThreadIDs is NOT built from log lines. It is derived from the
// canonical archive index (store.LoadArchiveIndex), restricted to entries
// that are currently archived and NOT yet recovered (RecoveredAt == "").
// This single rule fixes F4 and F8 together:
//
//   - F4: a mid-loop eventlog.Log failure in ArchiveThreads could under-emit
//     `archive.archived` lines for durably-committed threads, so a log-line
//     fold under-counted the archived set → false recall_unexplained_absence.
//     The index has every committed entry regardless of log emission, so the
//     measurement set no longer depends on the forensic log's completeness.
//   - F8: a recovered thread's index entry is RETAINED with RecoveredAt
//     stamped (invariant 4). Filtering to RecoveredAt == "" automatically
//     drops a recovered thread from the archived set the instant it is
//     recovered, so VerifyThreadAccounting's "on-spine AND archived =
//     corruption" can never fire on a recovered thread — independent of
//     whether the recovery gauge runs before or after the invariant sweep.
//
// To avoid an O(index) read on every recall-fidelity step (which would hurt
// long-rung throughput), the index is reloaded ONLY when this batch carries
// an archival or recovery line — the markers that mean the index changed
// this turn. Drains/recoveries are infrequent and bounded, so the refresh is
// cheap. A caller that mutates the spine without an event line (none today)
// can force a refresh via refreshArchivedSet.
func (h *Harness) foldEventLines(lines []string) {
	archiveActivity := false
	for _, line := range lines {
		if id, ok := eventThreadID(line, "thread.created ", ""); ok {
			h.createdThreadIDs[id] = struct{}{}
		}
		if strings.Contains(line, "archive.archived ") ||
			strings.Contains(line, "archive.recovered ") ||
			strings.Contains(line, "archive.recovered-record ") {
			archiveActivity = true
		}
	}
	if archiveActivity {
		h.refreshArchivedSet()
	}
}

// refreshArchivedSet rebuilds h.archivedThreadIDs from the canonical archive
// index, keeping only entries that are currently archived and not yet
// recovered (RecoveredAt == ""). It is the single source of truth for "off
// the live recall surface but preserved + recoverable" — see foldEventLines
// for why it is index-derived rather than log-derived (F4/F8). Reassigns the
// map wholesale so a thread that left the archived set (recovered) is dropped,
// not just never re-added. A read failure leaves the prior set in place and
// reports via t.Errorf (the index is canonical; a read error is a real
// substrate fault worth surfacing, not silently swallowing).
func (h *Harness) refreshArchivedSet() {
	entries, err := store.LoadArchiveIndex(h.Paths)
	if err != nil {
		h.T.Errorf("refreshArchivedSet: load archive index: %v", err)
		return
	}
	set := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.RecoveredAt != "" {
			continue
		}
		set[e.ThrID] = struct{}{}
	}
	h.archivedThreadIDs = set
}

// recoveryGaugeSample is the maximum number of archived threads the
// recovery-fetch gauge attempts to recover. Sampling a bounded prefix keeps
// the gauge O(1) regardless of how much a long run archived; recovery is the
// same code path for every entry, so a sample is sufficient to exercise it.
const recoveryGaugeSample = 5

// recordW1Class bumps the harness's run-total W1 classification tally for one
// divergent probe (#111 §7.1). class is the per-probe verdict the recaller
// returned; the empty string (a non-divergent probe) bumps nothing. The
// recognized verdicts are the exported measure-package labels (measure.W1Class),
// referenced directly rather than mirrored as local literals (burndown BD-3) so
// a label rename in measure is a single-edit compile-checked change.
//
// An UNKNOWN non-empty class is a seam break — the recaller returned a verdict
// the harness has no bucket for — not benign noise: it means measure grew a
// class the harness silently drops, zeroing a divergence that really occurred.
// Log it loudly on the harness's *testing.T so it surfaces in the run output.
func recordW1Class(h *Harness, class string) {
	switch measure.W1Class(class) {
	case "":
		// Non-divergent probe: nothing to record.
	case measure.W1StrictMiss:
		h.Metrics.Counter(MetricRecallIntraW1StrictMiss, 1)
	case measure.W1Tie:
		h.Metrics.Counter(MetricRecallIntraW1Tie, 1)
	case measure.W1TreeMismatch:
		h.Metrics.Counter(MetricRecallIntraW1TreeMismatch, 1)
	default:
		h.T.Errorf("recordW1Class: unknown W1 divergence class %q from IntraThreadDivergence — "+
			"a measure-package class the harness has no bucket for (seam break; recall_intra_w1_* undercounts)", class)
	}
}

// runSleepCycle drives one ARCHITECTURE.md sleep cycle (#108): it measures
// the substrate .git footprint, calls h.Ops.Consolidate (substrate gc), and
// re-measures, recording the before/after sizes so the run proves gc
// reclamation. Consolidate is non-fatal by contract (a gc failure is logged
// and swallowed inside the adapter), so this never fails the test on gc
// trouble — it asserts only that the port call itself returns without error.
func runSleepCycle(t *testing.T, h *Harness, idx int, label string) {
	t.Helper()
	pre := gitDirBytes(h.Paths.Home)
	h.Metrics.Record(metricGitLooseObjectsPre, float64(looseObjectCount(h.Paths.Home)))
	h.Metrics.Record(metricGitPackedBytesPre, float64(packedBytes(h.Paths.Home)))
	gcStart := clock.Profiling()
	if err := h.Ops.Consolidate(context.Background(), "sleep-cycle: day-off"); err != nil {
		t.Fatalf("scenario step %d (%s): Consolidate: %v", idx+1, label, err)
	}
	// Repack duration (R3-addendum item 4): the wall-clock cost of the
	// Consolidate pass (repack + prune + pack-refs) — the growth gauge's
	// companion, so a rung shows what the reclaim COSTS as loose-object
	// accrual scales with per-turn commits.
	h.Metrics.Record(metricGitGCRepackMs, float64(clock.Since(gcStart).Microseconds())/1000.0)
	// §6.4 recall-cache sweep: drop persisted .vec files for archived/absent
	// threads, alongside the substrate gc. The cache is operational state the
	// adapter's Consolidate cannot reach, so the seam is on the Service the
	// harness already holds (measure.Service.SweepCache). A recaller that does
	// not implement it (the symbolic-only default) is simply not swept.
	if h.State != nil && h.State.Recaller != nil {
		if sw, ok := h.State.Recaller.(cacheSweeper); ok {
			if err := sw.SweepCache(context.Background()); err != nil {
				t.Fatalf("scenario step %d (%s): SweepCache: %v", idx+1, label, err)
			}
		}
	}
	// §5.3 within-thread summary-tree build, AFTER the cache sweep so the
	// trees summarize a settled leaf set (ordering: leaf reconcile → sweep →
	// tree rebuild). Offline, in the sleep pass — never the turn loop (W6).
	// The rebuild count is the recall_intra_tree_rebuild_calls trip-wire
	// (§7.2 / F-B). A recaller without trees (symbolic-only) is not driven.
	if h.State != nil && h.State.Recaller != nil {
		if tb, ok := h.State.Recaller.(treeRebuilder); ok {
			rebuilt, err := tb.RebuildTrees(context.Background())
			if err != nil {
				t.Fatalf("scenario step %d (%s): RebuildTrees: %v", idx+1, label, err)
			}
			if rebuilt > 0 {
				h.Metrics.Counter(MetricRecallIntraTreeRebuildCalls, int64(rebuilt))
			}
		}
	}

	post := gitDirBytes(h.Paths.Home)
	h.Metrics.Record(metricGitLooseObjectsPost, float64(looseObjectCount(h.Paths.Home)))
	h.Metrics.Record(metricGitPackedBytesPost, float64(packedBytes(h.Paths.Home)))

	h.Metrics.Counter(MetricSleepCycles, 1)
	h.Metrics.Record(MetricGitDirBytesPreGC, float64(pre))
	h.Metrics.Record(MetricGitDirBytesPostGC, float64(post))
	if pre > post {
		h.Metrics.Counter(MetricGitDirBytesReclaimed, pre-post)
	}
}

// looseObjectCount counts loose objects in home's .git/objects — files
// under the two-hex-digit fan-out directories (pack/ and info/ excluded).
// Per-turn commits (#94 R3) accrue ~2-4 loose objects per turn between
// sleep-cycle gcs; this gauge is the growth-vs-reclaim forensic. Walk
// errors degrade to the count so far (forensic, not load-bearing).
func looseObjectCount(home string) int64 {
	var n int64
	objects := filepath.Join(home, ".git", "objects")
	entries, err := os.ReadDir(objects)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() || len(e.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(objects, e.Name()))
		if err != nil {
			continue
		}
		n += int64(len(files))
	}
	return n
}

// packedBytes sums the sizes of .git/objects/pack/* — the packed-store
// footprint the sleep gc folds loose objects into. Grows step-wise at
// each repack; paired with the loose-object gauges it separates "space
// moved into packs" from "space reclaimed". Errors degrade to 0
// (forensic, not load-bearing).
func packedBytes(home string) int64 {
	var n int64
	packDir := filepath.Join(home, ".git", "objects", "pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			n += info.Size()
		}
	}
	return n
}

// foldEndedSessionFlushCost drains a NOW-ENDING session's OBSERVED §6.5 flush
// cost (turn.State.FlushCalls/FlushChunks) into the run-total counters (#126).
// It is called at every RestartSession, just before the pre-shutdown State is
// discarded, so the ended session's count is preserved across the per-session
// State churn a relaunch causes. The live (current) session is NOT drained
// here — FlushCostRunToDate adds its not-yet-drained count to these counters at
// read time, so the run-to-date total is correct at any point without
// double-counting. A no-op when there is no live State (defensive). The counted
// cost is the §6.2 debt-cap + dormancy policy's flush rate — the real cost N
// pays, replacing the prior model that divided scrolled-out chunks by a mirror
// of turn.EmbeddingDebtCap.
func foldEndedSessionFlushCost(h *Harness) {
	if h.State == nil {
		return
	}
	if calls := h.State.FlushCalls(); calls > 0 {
		h.Metrics.Counter(MetricRecallIndexFlushCalls, int64(calls))
	}
	if chunks := h.State.FlushChunks(); chunks > 0 {
		h.Metrics.Counter(MetricRecallIndexFlushChunks, int64(chunks))
	}
}

// FlushCostRunToDate returns the OBSERVED §6.5 flush cost run-to-date (#126):
// the run-total counters (drained from sessions that have already ended at a
// RestartSession) plus the live current session's not-yet-drained
// FlushCalls/FlushChunks. This is the true run-to-date at ANY point — interior
// day-close or end-of-run — mirroring how the cosine-ops gauge reads the live
// recaller's accumulating accessor rather than a stale drained value. Returns
// (calls, chunks).
func (h *Harness) FlushCostRunToDate() (calls, chunks int64) {
	calls = h.Metrics.CounterValue(MetricRecallIndexFlushCalls)
	chunks = h.Metrics.CounterValue(MetricRecallIndexFlushChunks)
	if h.State != nil {
		calls += int64(h.State.FlushCalls())
		chunks += int64(h.State.FlushChunks())
	}
	return calls, chunks
}

// gitDirBytes returns the total on-disk size of home's .git directory in
// bytes via a filepath.Walk over regular files. A walk error (e.g. a file
// vanishing mid-walk during a concurrent op — not expected in the
// single-threaded sim) yields the bytes counted so far; the gauge is
// forensic, not load-bearing, so a partial count beats failing the run.
func gitDirBytes(home string) int64 {
	var total int64
	root := filepath.Join(home, ".git")
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries; keep counting
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// runRecoveryGauge is the §6.3 / AC1 "recoverable, measured" gauge. It reads
// the archive index and, for up to recoveryGaugeSample entries, drives
// h.Ops.RecoverThread under the realistic accumulated state the run produced,
// asserting each comes back as a wip spine record. It records:
//
//   - archive_recovered_verified       (+1 per successful recovery)
//   - archive_recover_integrity_fail   (+1 per ErrArchiveIntegrity — a NONZERO
//     here is an acceptance FAILURE: the stored tree hash did not match the
//     restored bytes)
//
// It runs AFTER measurement and the final invariant sweep (RunScenario calls
// it there), so the spine mutation it causes cannot affect a measured series
// or trip thread-accounting. A run that archived nothing records both counters
// as 0 and returns cleanly.
func runRecoveryGauge(t *testing.T, h *Harness) {
	t.Helper()
	entries, err := store.LoadArchiveIndex(h.Paths)
	if err != nil {
		t.Errorf("recovery gauge: load archive index: %v", err)
		return
	}
	// Record 0 explicitly so the metric is always present in the blob, even
	// when nothing was archived — a missing key and a measured zero are
	// otherwise indistinguishable to the §9.4 baseline comparison.
	h.Metrics.Counter("archive_recovered_verified", 0)
	h.Metrics.Counter("archive_recover_integrity_fail", 0)
	if len(entries) == 0 {
		return
	}

	n := len(entries)
	if n > recoveryGaugeSample {
		n = recoveryGaugeSample
	}
	for _, e := range entries[:n] {
		rec, err := h.Ops.RecoverThread(context.Background(), e.ThrID)
		if err != nil {
			if errors.Is(err, memops.ErrArchiveIntegrity) {
				h.Metrics.Counter("archive_recover_integrity_fail", 1)
				t.Errorf("recovery gauge: %s: archive integrity check failed: %v", e.ThrID, err)
				continue
			}
			t.Errorf("recovery gauge: %s: RecoverThread: %v", e.ThrID, err)
			continue
		}
		if rec.State != memops.ThreadWIP {
			t.Errorf("recovery gauge: %s: recovered state %q, want %q", e.ThrID, rec.State, memops.ThreadWIP)
			continue
		}
		h.Metrics.Counter("archive_recovered_verified", 1)
	}
}

// RunManifestFilename is the self-description file every rundata scenario
// directory receives (writeRunManifest): the run's provenance, bound to the
// directory so its numbers can never again be attributed to the wrong kind
// of run (the burndown-2026-07b false-alarm root cause — a short mock dir
// indistinguishable from a live one).
const RunManifestFilename = "manifest.json"

// RunManifest is the manifest.json schema. Mode is derived by the harness
// from what is actually installed ("mock", "live-inference",
// "live-embedding", or "live-inference+live-embedding"); Rung/Seed/Duration
// come from the optional Scenario provenance fields (sim rungs populate
// them; handwritten scenarios omit them). Start/End are real wall-clock
// RFC3339 (clock.Profiling — the pinned sim Timeline never leaks in); an
// empty End means the run did not complete. GitHead is the repo HEAD at run
// time ("unknown" when git is unavailable).
type RunManifest struct {
	Scenario string `json:"scenario"`
	Rung     string `json:"rung,omitempty"`
	Mode     string `json:"mode"`
	Seed     int64  `json:"seed,omitempty"`
	Duration string `json:"duration,omitempty"`
	Start    string `json:"start"`
	End      string `json:"end,omitempty"`
	GitHead  string `json:"git_head"`
}

// writeRunManifest writes/rewrites manifest.json in the scenario's rundata
// directory. Called twice per run: at run start (end == "", so a crashed
// run still leaves a manifest) and at completion (end filled). A write
// failure is reported via t.Errorf — the manifest is forensic binding, not
// a reason to halt a measured run.
func writeRunManifest(h *Harness, sc Scenario, start, end string) {
	man := RunManifest{
		Scenario: sc.Name,
		Rung:     sc.RungLabel,
		Mode:     runMode(h),
		Seed:     sc.RungSeed,
		Start:    start,
		End:      end,
		// NB: resolve HEAD from the SOURCE repo root, never from h.RunHome —
		// the run home contains the substrate's OWN autogit .git, whose HEAD
		// is the memory tree's, not the code under test.
		GitHead: gitHead(repoRoot(h.T)),
	}
	if sc.RungSpan > 0 {
		man.Duration = sc.RungSpan.String()
	}
	body, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		h.T.Errorf("run manifest: marshal: %v", err)
		return
	}
	path := filepath.Join(h.RunHome, RunManifestFilename)
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		h.T.Errorf("run manifest: write %s: %v", path, err)
	}
}

// runMode derives the manifest's mode string from what the harness actually
// installed — never from a caller-supplied label, so it cannot lie:
// a live chat client (Scenario.LiveClient) is inference-in-loop; a custom
// recaller factory (Scenario.Recaller) is embedding-in-loop; neither is the
// mock acceptance path.
func runMode(h *Harness) string {
	switch {
	case h.liveClient != nil && h.recallerFactory != nil:
		return "live-inference+live-embedding"
	case h.liveClient != nil:
		return "live-inference"
	case h.recallerFactory != nil:
		return "live-embedding"
	default:
		return "mock"
	}
}

// gitHead returns the repository HEAD commit hash for the tree containing
// dir, or "unknown" when git/the repo is unavailable. Forensic only — an
// error is not worth failing a run over.
func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// newSpineIDs returns the thread IDs present in post but not in pre — the
// threads a step created. Used to drive the incremental embedding-index
// upkeep (#98). O(pre+post); the sets are small per step.
func newSpineIDs(pre, post []memops.SpineRecord) []string {
	had := make(map[string]struct{}, len(pre))
	for _, r := range pre {
		had[r.ID] = struct{}{}
	}
	var out []string
	for _, r := range post {
		if _, ok := had[r.ID]; !ok {
			out = append(out, r.ID)
		}
	}
	return out
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
