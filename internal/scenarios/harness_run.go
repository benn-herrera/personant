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

	// Sleep-cycle steps (#108) carry no turn: route them off the turn path
	// entirely. The harness owns h.Ops, so the substrate consolidation runs
	// here (the generator is a stateless StepSource that cannot reach Ops).
	// Returns a zero-but-indexed feedback so downstream bucketing treats it
	// as a real, no-recall-opportunity step (Index >= 0, RecallExpected 0).
	if step.SleepCycle {
		runSleepCycle(t, h, idx, label)
		return StepFeedback{Index: idx}
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
	// Under inference-in-loop (#98) the live client is installed instead, so
	// the canned response is neither served nor relevant — skip it.
	if h.liveClient == nil {
		h.Mock.SetResponse(step.MockResponse)
	}

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

	// Keep the embedding index current (#98): a thread created this turn must
	// be embedded and added to the layer-2 index, or it is structurally
	// unrecallable by embedding for the rest of the run (the session-start
	// Prepare embedded only the threads that existed then). Drive the
	// incremental AddThread seam for each spine ID that appeared this turn.
	// A no-op for the symbolic-only default (h.indexer nil). An embed failure
	// is fatal: a silently-missing index entry would understate embedding
	// recall and corrupt the symbolic-vs-embedding head-to-head.
	if h.indexer != nil && len(postSpine) > len(preSpine) {
		for _, id := range newSpineIDs(preSpine, postSpine) {
			if err := h.indexer.AddThread(context.Background(), id); err != nil {
				t.Fatalf("scenario step %d (%s): embedding index add %s: %v", idx+1, label, id, err)
			}
		}
	}

	// One incremental poll captures exactly this turn's appended log
	// lines: the turn just completed and the next turn has not started.
	// From those lines extract this step's match-fire set and fold any
	// thread.created IDs into the cumulative created set. The archived set
	// is refreshed from the canonical index (not the log) whenever the
	// batch carried an archival/recovery line. Invariant checks between
	// turns do not write to logs/, so they cannot pollute the next step's
	// window.
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
	matchFires := matchFireSet(stepLines)
	keptExpected, expectedForgiven := recordRecallFidelity(t, h, idx, label, step.RecallMode, step.ExpectedRecallMatches, matchFires)

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

	// Per-step metrics.
	h.Metrics.Counter("turns", 1)
	h.Metrics.Record("turn_duration_ms", float64(elapsed.Milliseconds()))
	if len(postSpine) > len(preSpine) {
		h.Metrics.Counter("threads_created", int64(len(postSpine)-len(preSpine)))
	} else if len(postSpine) == len(preSpine) {
		// Existing-thread engagement (or no engagement at all). Don't
		// double-count: only mark engagement if the body itself implies
		// a tag was present and at least one record's turn_count moved.
		h.Metrics.Counter("engaged_existing_threads", 1)
	}

	// Response size + topic-tag presence accounting. Under mock inference these
	// read the canned step.MockResponse (the bytes the mock served, the tag the
	// plan emitted) — cheap cross-version drift signal. Under inference-in-loop
	// (#98) the canned response is NOT what the model produced, so these mock-
	// derived figures are meaningless: the REAL model's response bytes never
	// reach the harness (turn.Run streams + tag-strips the body), and its tag
	// discipline is measured authoritatively from the runtime's own
	// `topic.tag-missing` log line (counted by the live behavior-validation
	// summary), not from the canned plan. Skip them in live mode to avoid
	// recording the plan as if it were the model.
	if h.liveClient == nil {
		h.Metrics.Record("response_bytes", float64(len(step.MockResponse.Content)))
		if strings.Contains(step.MockResponse.Content, "*topic:") {
			h.Metrics.Counter("topic_tag_parsed", 1)
		} else {
			h.Metrics.Counter("topic_tag_missing", 1)
		}
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
		// loss" — a thread that is neither live nor recoverable-via-fetch, a
		// genuine integrity bug. This is a HARD ==0 gate in the sim (#100):
		// the oracle no longer manufactures false positives here. Previously a
		// long-sim refinement burst could name an expected thread the instant
		// before its spine record materialized (the day-ahead generation
		// buffer creates the thread in the shadow model before its creating
		// step executes), inflating this counter for a thread that was never
		// archived and ended up on-spine. The refinement oracle now excludes
		// unmaterialized threads (recallExpectedForMaterialized), so a nonzero
		// count is a real off-spine-not-archived loss, not an oracle artifact.
		h.Metrics.Counter("recall_unexplained_absence", 1)
		return true
	}

	return StepFeedback{
		Index:                  idx,
		RecallMatchFires:       len(matchFires),
		RecallMatchFireIDs:     matchFires,
		EmbedMatchFireIDs:      embedFireIDs,
		IntraMatchFireIDs:      intraFires,
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

// Sleep-cycle metric keys (#108). One source of truth for the harness
// emitter and any downstream summary/baseline comparison.
const (
	// metricSleepCycles counts how many sleep/consolidation passes the run
	// fired (one per day-off).
	metricSleepCycles = "sleep_cycles"
	// metricGitDirBytesPreGC / PostGC are histograms — one sample per sleep
	// cycle — of the substrate .git directory size immediately before and
	// after the gc. The post < pre delta is the reclamation proof.
	metricGitDirBytesPreGC  = "git_dir_bytes_pre_gc"
	metricGitDirBytesPostGC = "git_dir_bytes_post_gc"
	// metricGitDirBytesReclaimed accumulates total bytes reclaimed across all
	// sleep cycles (pre − post, clamped at 0 so a cycle that grew .git — e.g.
	// a fresh pack larger than the loose pile it replaced — does not subtract).
	metricGitDirBytesReclaimed = "git_dir_bytes_reclaimed"
	// metricRecallIntraTreeRebuildCalls counts within-thread summary-tree
	// (re)builds across the run — the design §7.2 / fork-F-B trip-wire (#111):
	// if it trends up with main-thread length on the long rungs, semantic
	// rebalancing is not staying bounded and escalates to the hybrid MAD.
	metricRecallIntraTreeRebuildCalls = "recall_intra_tree_rebuild_calls"
)

// runSleepCycle drives one ARCHITECTURE.md sleep cycle (#108): it measures
// the substrate .git footprint, calls h.Ops.Consolidate (substrate gc), and
// re-measures, recording the before/after sizes so the run proves gc
// reclamation. Consolidate is non-fatal by contract (a gc failure is logged
// and swallowed inside the adapter), so this never fails the test on gc
// trouble — it asserts only that the port call itself returns without error.
func runSleepCycle(t *testing.T, h *Harness, idx int, label string) {
	t.Helper()
	pre := gitDirBytes(h.Paths.Home)
	if err := h.Ops.Consolidate(context.Background(), "sleep-cycle: day-off"); err != nil {
		t.Fatalf("scenario step %d (%s): Consolidate: %v", idx+1, label, err)
	}
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
				h.Metrics.Counter(metricRecallIntraTreeRebuildCalls, int64(rebuilt))
			}
		}
	}

	post := gitDirBytes(h.Paths.Home)

	h.Metrics.Counter(metricSleepCycles, 1)
	h.Metrics.Record(metricGitDirBytesPreGC, float64(pre))
	h.Metrics.Record(metricGitDirBytesPostGC, float64(post))
	if pre > post {
		h.Metrics.Counter(metricGitDirBytesReclaimed, pre-post)
	}
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
