package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
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
	// thread.created / archive.archived IDs into the cumulative
	// sets. Invariant checks between turns do not write to logs/, so
	// they cannot pollute the next step's window.
	stepLines, err := h.tailer.poll()
	if err != nil {
		t.Fatalf("scenario step %d (%s): tailer.poll: %v", idx+1, label, err)
	}
	h.foldEventLines(stepLines)
	matchFires := matchFireSet(stepLines)
	expectedForgiven := recordRecallFidelity(t, h, idx, label, step.RecallMode, step.ExpectedRecallMatches, matchFires)

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

	// Archival-recoverability predicate for a StepSource that measures recall
	// against a target it did NOT declare as an expected match (the #96
	// abandoned-topic probe). Mirrors recordRecallFidelity's filter: a
	// target on the live spine is recoverable; one absent AND in the
	// archive.archived log is off the LIVE recall surface (excluded — the
	// thread is preserved + recoverable via explicit fetch, not lost); one
	// absent but NOT archived is an unexplained absence — a genuine integrity
	// bug, counted via recall_unexplained_absence (the recall-measurement
	// sibling of VerifyThreadAccounting) and conservatively treated as a real
	// miss. Built from postSpine (already read) so no extra spine read.
	livePost := make(map[string]struct{}, len(postSpine))
	for _, r := range postSpine {
		livePost[r.ID] = struct{}{}
	}
	recoverable := func(id string) bool {
		if _, onSpine := livePost[id]; onSpine {
			return true
		}
		if _, wasArchived := h.archivedThreadIDs[id]; wasArchived {
			return false
		}
		// Off the live spine and NOT archived: not a forgivable archival. The
		// recall-measurement sibling of VerifyThreadAccounting's "unexplained
		// loss" — a thread that is neither live nor recoverable-via-fetch. (In
		// long sims a tiny count also surfaces an oracle-side artifact: the
		// shadow generator's refinement burst can name an expected thread the
		// instant before its spine record materializes; such a thread is never
		// archived and ends up on-spine — see the §6 measurement-honesty notes.)
		h.Metrics.Counter("recall_unexplained_absence", 1)
		return true
	}

	return StepFeedback{
		Index:                  idx,
		RecallMatchFires:       len(matchFires),
		RecallMatchFireIDs:     matchFires,
		RecallExpected:         len(step.ExpectedRecallMatches),
		TargetRecoverable:      recoverable,
		RecallExpectedForgiven: expectedForgiven,
	}
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

// foldEventLines folds a batch of tailed log lines into the harness's
// cumulative created/archived thread-ID sets. Called by runStep after
// each turn's poll and by tests that seed log lines directly.
func (h *Harness) foldEventLines(lines []string) {
	for _, line := range lines {
		if id, ok := eventThreadID(line, "thread.created ", ""); ok {
			h.createdThreadIDs[id] = struct{}{}
		}
		if id, ok := eventThreadID(line, "archive.archived ", "thr="); ok {
			h.archivedThreadIDs[id] = struct{}{}
		}
	}
}

// recoveryGaugeSample is the maximum number of archived threads the
// recovery-fetch gauge attempts to recover. Sampling a bounded prefix keeps
// the gauge O(1) regardless of how much a long run archived; recovery is the
// same code path for every entry, so a sample is sufficient to exercise it.
const recoveryGaugeSample = 5

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
