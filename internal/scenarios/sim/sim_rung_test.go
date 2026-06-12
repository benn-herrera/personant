package sim

import (
	"fmt"
	"sort"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/scenarios"
	"testing"
)

// gatePolicy is the single source of truth for which §9.4 acceptance gates
// fire as hard t.Errorf asserts versus downgrade to logged observations on a
// given rung. It replaces the scatter of inline `oracleBlind` / `*liveEmbedding`
// branches that the gate-evaluation code used to re-test at each decision point
// (sim-harness burndown #6): every gate now consults this object once, by
// intent-named predicate, so the live-vs-mock policy is decided in one place and
// read everywhere.
//
// The two inputs:
//
//   - oracleBlind (true only under -sim.live-inference): a real chat model emits
//     different topic tags / symbols than the canned plan, so the runtime engages
//     and creates threads the deterministic recall oracle never named. Every gate
//     whose ground truth is that oracle (recall-fidelity / wander-coherence /
//     abandoned-topic / criterion (e) / the unexplained-absence canary / the
//     plan-driven closure-liveness check) is therefore meaningless and reports
//     instead of failing. The substrate-invariant gates that do NOT depend on the
//     oracle (R6 history cap, thread-id well-formedness, RunScenario's per-step
//     invariants) stay LIVE under blinding — a real model cannot legitimately
//     break a storage invariant.
//
//   - liveEmbedding (true under -sim.live-embedding): the intra-thread coherence
//     gate compares the SYMBOLIC shadow-chunk oracle against the runtime's intra
//     fine tier. That comparison is only valid when both sides are symbolic; on a
//     live-embedding run the intra tier fires real embeddings, which legitimately
//     diverge from the symbolic oracle (#96), so the gate is report-only there and
//     a hard ==0 gate on the mock run.
type gatePolicy struct {
	oracleBlind   bool
	liveEmbedding bool
}

// oracleGatesAssert reports whether the oracle-dependent gates (closures,
// wander coherence, criterion (e), the unexplained-absence canary) should fire
// as hard asserts. False under live inference, where the oracle's plan-derived
// ground truth no longer matches the runtime.
func (p gatePolicy) oracleGatesAssert() bool { return !p.oracleBlind }

// intraDivergenceAsserts reports whether the intra-thread coherence-divergence
// tripwire fires as a hard ==0 gate. It holds only on the symbolic MOCK run:
// not under live inference (oracle blind) and not under live embedding (symbolic
// oracle ≠ embedding recall by design, #96 — report-only there).
func (p gatePolicy) intraDivergenceAsserts() bool {
	return !p.oracleBlind && !p.liveEmbedding
}

// rungReport is the write-once-read-many owner of a rung's materialized
// telemetry (sim-harness burndown #6). runSimRung used to write→re-read→mutate→
// re-write→re-read the metrics blob three times, interleaving the folds with the
// scalar derivations; materializeRungMetrics now folds every series into
// h.Metrics in memory, writes the blob ONCE, reads it back ONCE, and derives the
// summary scalars from that single materialized read. logRungSummary and
// evalRungGates then only READ this — they never re-write or re-read the blob.
//
// The intermediate JSON round-trips were never needed: the folds mutate the live
// in-memory *metrics.Run, and the two intra-fold reads they appeared to require
// (the turn-latency percentiles for recordIntraThreadMetrics, and the adversarial
// precision for recordLifecycleMetrics) are served from h.Metrics' in-memory
// accessors (HistogramSnapshot), which carry the same data the blob would.
type rungReport struct {
	m               metricsBlob
	turns           int
	threadsCreated  int64
	closures        int
	finalPopulation int
	p50, p95, p99   float64
	meanMs          float64
	wall            time.Duration
}

// runSimRung is the shared run-and-report logic for every rung of the
// six-month simulation rung walk. It generates a deterministic workload
// of simulated span d, drives it through the scenario harness, asserts
// clean completion (RunScenario does the per-step invariant + turn.Run
// error checks) plus thread-id well-formedness, and logs the summary
// the rung walk tracks. It is untagged so it compiles into every test
// build, and returns the post-run *Harness for any rung-specific
// follow-up assertions.
//
// runSimRung's recaller argument is the embedding-in-loop seam (#98): the
// mock acceptance path (TestSim with both toggles off) passes nil → the
// harness keeps the symbolic-only default recaller, and the run is
// byte-for-byte the pre-#98 mock rung. The live-embedding path (TestSim under
// -sim.live-embedding) passes a factory that builds
// measure.NewService(ops, embedder); the harness installs it, Prepares the
// index, and keeps it current via the per-thread-creation AddThread seam.
//
// oracleBlind is the inference-in-loop guard (#98, Inc 3). When true (set
// only by TestSim under -sim.live-inference), every gate whose ground truth is
// the deterministic generator's CANNED responses is downgraded from a t.Errorf
// failure to a logged observation; see gatePolicy for the full rationale. With
// oracleBlind false (every mock path) the run is unchanged.
//
// The three responsibilities — materialize the telemetry blob, log the rung
// summary, and evaluate the pass/fail gates — are separated into
// materializeRungMetrics, logRungSummary, and evalRungGates so the
// summary-logging never reaches a gate and the gates read one materialized
// blob through a single gatePolicy.
func runSimRung(t *testing.T, label string, d time.Duration, corpus []CorpusSlot,
	recaller func(ops memops.MemoryOps) measure.Recaller, oracleBlind bool,
	liveClient model.Client, liveModel string) *scenarios.Harness {
	t.Helper()

	sc := GenerateWorkload(WorkloadConfig{
		Seed:     simSeed,
		Duration: d,
		Corpus:   corpus,
	})
	sc.Recaller = recaller
	// Inference-in-loop (#98, Inc 3): when a live chat client is supplied the
	// harness drives turns against it instead of the scripted mock, and
	// oracleBlind is set in lockstep (every caller pairs them). nil keeps the
	// mock path.
	sc.LiveClient = liveClient
	sc.LiveModel = liveModel

	// Relax the heavy-invariant cadence to sim-daily. The sim's per-step
	// heavy-invariant sweeps were super-linear in turn count and ate the
	// wall budget on long rungs; the cheap subset still fires per step
	// and the heavy set still fires once at end-of-run, so a substrate
	// break is caught within ~one sim-day and the acceptance gate is
	// unchanged. Handwritten scenarios leave this zero and keep
	// per-step heavy firing.
	sc.HeavyInvariantCadence = simHeavyInvariantCadence

	// Arm the heap watchdog. A leaking sim previously got SIGKILL'd by
	// macOS memorystatus mid-run with no diagnostics; capping HeapInuse
	// at 8 GiB converts that into a Go panic plus a heap profile under
	// the rundata directory.
	sc.MemoryCapBytes = simMemoryCapBytes

	// Append a wall-clock timestamp suffix so each sim run gets its own
	// forensic-data directory. GenerateWorkload keys sc.Name on
	// (seed, duration) only, so two runs of the same duration would
	// otherwise reuse one test/rundata/<name>/ directory and the
	// harness's runDataHome RemoveAll would destroy the prior run's data.
	sc.Name = withRunTimestampSuffix(sc.Name)

	// The workload is generated on demand — the harness pulls one step
	// at a time from sc.StepSource — so the turn count is not knowable
	// before the run. It is read back from the `turns` counter the
	// metrics blob accumulated as the run proceeded.
	t.Logf("driving on-demand workload over %s simulated", d)

	// Hold the generator pointer so the post-run episode stats can be
	// pulled out. The generator is the only thing that knows about
	// refinement episodes — the harness sees them as ordinary steps.
	gen := sc.StepSource.(*generator)

	// Per-sim-day-close stats series (#120): at the close of every sim-day the
	// harness fires this handler, which appends one run-to-date stats record to
	// daily.jsonl in the scenario directory. The acceptance-ladder rung points
	// (15/30/60/120d) and every intermediate day are then just READ from the
	// series — one long run replaces the re-simulate-from-zero rung walk — and a
	// run terminated mid-flight has all stats to that point safely cached. The
	// CORRECTNESS INVARIANT is that the run-to-date record at the close of day N
	// equals the end-of-run summary of a standalone N-day run (same seed): the
	// shadow-derived stats come from the SAME computeIntraThreadGauges the
	// end-of-run summary uses, and counters/percentiles are read run-to-date.
	daily := newDailySnapshotWriter(gen)
	sc.OnSimDayClose = daily.onDayClose

	start := clock.Profiling()
	h := scenarios.RunScenario(t, sc)
	wall := clock.Since(start)

	// Materialize the telemetry blob: fold every generator-owned series into
	// h.Metrics, write the blob ONCE, read it back ONCE, and derive the summary
	// scalars from that single read (the write-once-read-many lifecycle, #6).
	rep := materializeRungMetrics(t, h, gen, wall)

	// Per-sim-day series final record (#120): emit the LAST daily.jsonl record
	// now that the end-of-run summary inputs (finalPopulation = liveThreads, the
	// turn-latency percentiles) are computed, using those EXACT values. The
	// interior ticks (days 1..N-1) already fired via sc.OnSimDayClose; this is
	// day N, carrying the trailing partial day the cadence tick at N×24h does not
	// catch (the run ends a few jittered steps past the tick). Because it uses the
	// summary's own inputs, this last record's run_to_date equals the end-of-run
	// summary by construction — the #120 correctness invariant that lets the
	// acceptance-ladder rungs be read from the series.
	// Date the finalize record grid-exact, consistent with the interior records:
	// the close stamp is the day-start instant of the final pinned day
	// (simDayCloseDate(SimDayIndex(pinned)) = SimClockStart + N·SimDayLength), NOT
	// the live ~21:30 instant. The two truncate to the SAME calendar date (the
	// final instant is within day N), so sim_date is unchanged — a consistency
	// tidy, not a behavior change. simDayCloseDate is unexported in scenarios; its
	// definition is SimClockStart + N·SimDayLength, computed inline here.
	finalDay := scenarios.SimDayIndex(h.PinnedClock())
	finalClose := scenarios.SimClockStart.Add(time.Duration(finalDay) * scenarios.SimDayLength)
	daily.finalize(h, finalClose, rep.finalPopulation, rep.p50, rep.p95, rep.p99)

	logRungSummary(t, label, rep, gen)
	evalRungGates(t, label, rep, gen, gatePolicy{oracleBlind: oracleBlind, liveEmbedding: *liveEmbedding})

	reportSleepCycles(t, rep.m, d)

	return h
}

// materializeRungMetrics folds every generator-owned telemetry series into
// h.Metrics, writes the metrics blob ONCE, reads it back ONCE, and returns the
// materialized blob plus the derived summary scalars in a rungReport (the
// write-once-read-many owner, #6). RunScenario already wrote the blob with the
// counters/histograms it recorded; this records the remaining generator-derived
// samples in memory, then performs the single write/read.
//
// The two reads the folds appear to need are served in-memory rather than via an
// intermediate blob round-trip: the turn-latency percentiles (for
// recordIntraThreadMetrics' latency gauges and the daily finalize) come from
// h.Metrics.HistogramSnapshot("turn_duration_ms"); recordLifecycleMetrics' own
// adversarial-precision read is likewise an in-memory snapshot. Both carry the
// same data the blob would, so the single end read is byte-identical to the
// prior three-round-trip flow.
func materializeRungMetrics(t *testing.T, h *scenarios.Harness, gen *generator, wall time.Duration) rungReport {
	t.Helper()

	// Fold the generator's miss → refinement episode stats into the metrics
	// blob (in memory; RunScenario already wrote the base blob). queries-to-hit
	// is a histogram (one sample per HIT episode, n in {1,2,3}); unresolved is a
	// counter (episodes that failed to hit within 3 attempts or were superseded).
	for _, n := range gen.episodeQueriesToHit {
		h.Metrics.Record("recall_episode_queries_to_hit", float64(n))
	}
	if gen.episodeUnresolved > 0 {
		h.Metrics.Counter("recall_episode_unresolved", int64(gen.episodeUnresolved))
	}
	if gen.userDictatedCount > 0 {
		h.Metrics.Counter("workload_user_dictated_turns", int64(gen.userDictatedCount))
	}

	// Fold the anchor-lifecycle (Inc 5) metrics, the within-session interleaving
	// telemetry, the within-thread wander telemetry, and the synthesis-thread
	// telemetry. All generator-owned, metrics-package-free at the seam; these set
	// counters/histograms/gauges on the live in-memory *metrics.Run.
	recordLifecycleMetrics(t, h, gen)
	recordInterleaveMetrics(h, gen)
	recordWanderMetrics(h, gen)
	recordSynthesisMetrics(h, gen)

	// Turn-latency percentiles from the live in-memory histogram (no blob read
	// needed): the same turn_duration_ms series the blob would carry. They feed
	// recordIntraThreadMetrics' latency gauges below and the daily finalize.
	durations := h.Metrics.HistogramSnapshot("turn_duration_ms")
	p50 := percentile(durations, 0.50)
	p95 := percentile(durations, 0.95)
	p99 := percentile(durations, 0.99)
	meanMs := mean(durations)

	closures := closureCount(t, h)

	// Thread-id well-formedness: every spine thread ID must be a well-formed
	// thr_<n> and unique. Gaps are LEGAL — archival deletes retired thread
	// records, leaving holes; the runtime never reuses an id, so the
	// creation-order → thr_{N+1} mapping the generator relies on stays valid even
	// with gaps. This is a substrate invariant (oracle-independent), so it stays a
	// hard assert regardless of gate policy.
	assertThreadIDsWellFormed(t, h)
	finalPopulation := finalThreadPopulation(t, h)

	// Fold the intra-thread recall telemetry (§9.2, #109): the §4.3 perf-decay
	// series and the intra-thread oracle's per-hop coherence curve. Needs the
	// final spine size T and the turn-latency percentiles, both now known.
	recordIntraThreadMetrics(h, gen, finalPopulation, p50, p95, p99)

	// Single write, single read: every series is now in h.Metrics, so write the
	// blob once and read it back once. The summary + gates read only this m.
	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Fatalf("write metrics blob: %v", err)
	}
	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}

	return rungReport{
		m:               m,
		turns:           int(m.Counters["turns"]),
		threadsCreated:  m.Counters["threads_created"],
		closures:        closures,
		finalPopulation: finalPopulation,
		p50:             p50,
		p95:             p95,
		p99:             p99,
		meanMs:          meanMs,
		wall:            wall,
	}
}

// logRungSummary writes the rung walk's summary lines (the §9.4 instrument
// readout) — turns, thread population, latency, the anchor-lifecycle /
// interleaving / wander / intra-thread metric blocks, the per-query cosine-op
// cost, and the extrapolated six-month floor. It is pure reporting: it makes NO
// pass/fail assertion (those live in evalRungGates), so a summary edit can never
// silently change a gate. All values come from the materialized rep.m.
func logRungSummary(t *testing.T, label string, rep rungReport, gen *generator) {
	t.Helper()
	m := rep.m

	t.Logf("=== %s summary ===", label)
	t.Logf("turns:            %d", rep.turns)
	t.Logf("threads created:  %d", rep.threadsCreated)
	t.Logf("closures:         %d", rep.closures)
	t.Logf("final thread pop: %d (final spine size)", rep.finalPopulation)
	t.Logf("turn latency:     P50=%.1fms P95=%.1fms mean=%.2fms", rep.p50, rep.p95, rep.meanMs)
	t.Logf("user-dictated turns: %d (Realism C variant — user prompt carries the literal line being appended)",
		gen.userDictatedCount)

	// Recall fidelity is measured (RecallMeasureOnly), never pass/fail at this
	// rung. Truth-in-labeling (sim-vs-reality MAD T0-1): this figure measures
	// ONLY the symbolic Jaccard layer; the acceptance run uses a nil embedder, so
	// it is labeled "symbolic-only recall" until embedding recall is measured.
	if steps := m.Counters["recall_fidelity_adversarial_steps"]; steps > 0 {
		t.Logf("symbolic-only recall (Jaccard):  measured over %d steps, mean symbolic-only recall=%.3f mean F1=%.3f",
			steps,
			mean(m.Histograms["recall_fidelity_adversarial_recall"]),
			mean(m.Histograms["recall_fidelity_adversarial_f1"]))
	} else {
		t.Logf("symbolic-only recall (Jaccard):  no measured steps this run")
	}

	// Embedding-vs-symbolic head-to-head (#98). Present only on an
	// embedding-in-loop run (embed_recall_fidelity_steps > 0); the mock
	// acceptance run never installs an embedder, so the series is absent and this
	// block is skipped — keeping the mock summary unchanged.
	if esteps := m.Counters["embed_recall_fidelity_steps"]; esteps > 0 {
		t.Logf("=== embedding-vs-symbolic recall head-to-head (#98) ===")
		t.Logf("embedding recall: measured over %d steps, mean recall=%.3f mean precision=%.3f mean F1=%.3f",
			esteps,
			mean(m.Histograms["embed_recall_fidelity_recall"]),
			mean(m.Histograms["embed_recall_fidelity_precision"]),
			mean(m.Histograms["embed_recall_fidelity_f1"]))
		t.Logf("(symbolic adversarial mean recall=%.3f over %d steps — compare; embedding closes the #96 gap iff it recovers what symbolic misses)",
			mean(m.Histograms["recall_fidelity_adversarial_recall"]),
			m.Counters["recall_fidelity_adversarial_steps"])
	}

	// Miss → refinement episode summary. queries-to-hit is a per-episode sample
	// (n attempts to first hit, n ∈ {1,2,3}); unresolved is a counter of episodes
	// that exhausted 3 attempts without a hit or were superseded before closure.
	qth := m.Histograms["recall_episode_queries_to_hit"]
	unresolved := m.Counters["recall_episode_unresolved"]
	if len(qth) > 0 || unresolved > 0 {
		t.Logf("recall episodes:  mean queries-to-hit %.2f (%d episodes, %d unresolved)",
			mean(qth), len(qth), unresolved)
	} else {
		t.Logf("recall episodes:  no closed episodes this run")
	}

	// Anchor-lifecycle (Inc 5) metric summary — the redesign's instrument.
	t.Logf("=== anchor-lifecycle metrics ===")
	t.Logf("drift_recall_origin:      %.3f (%d obs) — abandoned origin premise still matchable",
		m.Gauges["drift_recall_origin"], int(m.Gauges["drift_recall_origin_obs"]))
	t.Logf("drift_recall_dest:        %.3f (%d obs) — active (destination) projection recall",
		m.Gauges["drift_recall_dest"], int(m.Gauges["drift_recall_dest_obs"]))
	t.Logf("abandoned_premise_recall: %.3f (%d obs) — inverted-premise thread surfaces on original query",
		m.Gauges["abandoned_premise_recall"], int(m.Gauges["abandoned_premise_recall_obs"]))
	t.Logf("superseded_precision:     %.3f — recall matches dominated by intended (not abandoned) threads",
		m.Gauges["superseded_precision"])
	t.Logf("ever_central_count:       total=%d mean/thread=%.2f max/thread=%d (steady-state: flat across rung length)",
		int(m.Gauges["ever_central_count"]), m.Gauges["ever_central_mean"], int(m.Gauges["ever_central_max"]))
	t.Logf("history_len:              total=%d mean/thread=%.2f max/thread=%d (cap=%d; flat = steady-state proof)",
		int(m.Gauges["history_len_total"]), m.Gauges["history_len_mean"], int(m.Gauges["history_len_max"]), historyCapForReport)
	t.Logf("superseded symbols:       total=%d (retained-not-evicted, ever-central protected)",
		int(m.Gauges["superseded_count"]))
	t.Logf("projection_churn:         %.3f (mean AnchorsProjectedAtTurn / TurnCount; idempotent-write guard keeps it bounded)",
		m.Gauges["projection_churn"])
	t.Logf("vague-new became-matchable turn: mean %.2f over %d campaigns (unmatchable before accretion)",
		m.Gauges["vague_match_turn_mean"], int(m.Gauges["vague_match_campaigns"]))

	// Within-session interleaving (SPEC §9.1). Low mean dwell + high
	// distinct-slots ⇒ highly interleaved; high dwell + low distinct ⇒
	// coherent/clustered. Measures the concern instead of asserting it.
	slots := m.Histograms[metricSessionDistinctSlots]
	threadsPer := m.Histograms[metricSessionDistinctThreads]
	turnsPer := m.Histograms[metricSessionTurns]
	dwell := m.Histograms[metricTopicDwellRunlen]
	t.Logf("=== workload interleaving ===")
	t.Logf("distinct slots/session:   mean %.2f max %d (within-session topic diversity)",
		mean(slots), int(maxOf(slots)))
	t.Logf("distinct threads/session: mean %.2f max %d",
		mean(threadsPer), int(maxOf(threadsPer)))
	t.Logf("turns/session:            mean %.2f", mean(turnsPer))
	t.Logf("topic dwell run-length:   mean %.2f (consecutive turns on one thread before a cross-topic move)",
		mean(dwell))

	// Within-thread wander (#96 increment 2). Criterion (a): trajectory shape;
	// criterion (b): current-topic recall holds while abandoned-topic recall
	// decays with hop distance (the decay is the deliverable, not a failure).
	topicsDistinct := m.Histograms[metricThreadTopicsDistinct]
	wanderHopsHist := m.Histograms[metricThreadWanderHops]
	t.Logf("=== within-thread wander (#96) ===")
	t.Logf("thread topics distinct:   mean %.3f max %d (criterion a: >1.0 with tail to %d = non-monotonic)",
		mean(topicsDistinct), int(maxOf(topicsDistinct)), wanderMaxHops)
	t.Logf("thread wander hops:       mean %.3f max %d (trajectory length distribution)",
		mean(wanderHopsHist), int(maxOf(wanderHopsHist)))
	t.Logf("wander_current_recall:    %.3f (%d obs) — current topic must surface (>=~0.95)",
		m.Gauges[metricWanderCurrentRecall], int(m.Gauges[metricWanderCurrentRecall+"_obs"]))
	// Per-hop decay + coherence curve (hop 0 = current topic). embedHeadToHead
	// gates the per-hop embedding column so the mock summary stays unchanged.
	embedHeadToHead := m.Counters["embed_recall_fidelity_steps"] > 0
	for hop := 0; hop < wanderMaxHops; hop++ {
		key := fmt.Sprintf("_h%d", hop)
		obs := int(m.Gauges[metricWanderOriginRecallByHops+key+"_obs"])
		if obs == 0 {
			continue
		}
		if embedHeadToHead {
			// Gap-closure view (#98): symbolic vs embedding per-hop recall on one
			// denominator. Embedding "closes the gap" iff embed_recall stays high
			// at hops where symbolic origin_recall has decayed.
			t.Logf("  hop %d: symbolic_recall=%.3f embed_recall=%.3f coherence=%.3f (%d obs)",
				hop,
				m.Gauges[metricWanderOriginRecallByHops+key],
				m.Gauges[metricWanderEmbedRecallByHops+key],
				m.Gauges[metricWanderCoherenceByHops+key],
				obs)
			continue
		}
		t.Logf("  hop %d: origin_recall=%.3f coherence=%.3f (%d obs)",
			hop,
			m.Gauges[metricWanderOriginRecallByHops+key],
			m.Gauges[metricWanderCoherenceByHops+key],
			obs)
	}
	divergence := int(m.Gauges[metricWanderCoherenceDivergence])
	t.Logf("wander coherence divergence: %d (criterion b PASS = 0; decay is expected, divergence is the failure)",
		divergence)
	t.Logf("layerb shadow divergence:   %d (burndown #8 PASS = 0 when runtime follows the plan; shadow LRU vs runtime ActiveThreads)",
		int(m.Gauges[metricLayerBShadowDivergence]))

	// Intra-thread recall (#109, §9.2). The §4.3 perf-decay series (the I3/I4
	// gates) and the intra-thread oracle's per-hop coherence curve. H2 HONESTY:
	// the hop-recall curve is the SYMBOLIC shadow-chunk oracle's predicted
	// recoverability — a COHERENCE signal, not validated user recall.
	embeddingRun := gen.embeddingRun
	t.Logf("=== intra-thread recall (#109) ===")
	t.Logf("recall_index_coarse_size: %d (== final spine size T; flat per-thread-length, the I4 check)",
		int(m.Gauges[metricRecallIndexCoarseSize]))
	t.Logf("recall_index_fine_chunks: %d total, %d main-thread durable/kept (the I3 length axis; PRNG keep/toss trim active — convo=%d%% tool=%d%% transient)",
		int(m.Gauges[metricRecallIndexFineChunks]), int(m.Gauges[metricRecallIndexFineChunksMain]),
		convoTransientPct, toolTransientPct)
	t.Logf("recall_query_cosine_ops:  %.1f per-query MEASURED (§7.2; counted at the scoring call sites — coarse + engaged flat/descent; 0 on the symbolic mock run where no embedding recall ran)",
		m.Gauges[metricRecallQueryCosineOps])
	t.Logf("recall_query_latency:     P50=%.1fms P95=%.1fms P99=%.1fms (I3 gate: P99 flat across the rung ladder)",
		m.Gauges[metricRecallQueryLatencyP50], m.Gauges[metricRecallQueryLatencyP95], m.Gauges[metricRecallQueryLatencyP99])
	t.Logf("recall_index_flush:       %d calls / %d chunks (OBSERVED §6.5 debt-cap + dormancy flush rate — the cost N pays; folded from the runtime's turn.State counters, not modeled)",
		int(m.Gauges[metricRecallIndexFlushCalls]), int(m.Gauges[metricRecallIndexFlushChunks]))

	intraProbeObs := 0
	for _, total := range gen.intraHopTotal {
		intraProbeObs += total
	}
	if intraProbeObs == 0 {
		t.Logf("intra-thread probes:      none this run (rung too short to scroll the main thread past the assembly window + debt cap)")
	} else if embeddingRun {
		// Symbolic-vs-embedding intra-thread head-to-head (#109/#111 Finding B):
		// the SYMBOLIC column is the oracle's predicted/coherence curve (H2), the
		// EMBEDDING column is the recall users ACTUALLY get. Both scored against the
		// identical predicted-leaf ground truth on one denominator.
		t.Logf("intra-thread hop-recall head-to-head (symbolic predicted/coherence curve, H2 — vs embedding-observed = actual user recall):")
		maxHop := 0
		for hop := range gen.intraHopTotal {
			if hop > maxHop {
				maxHop = hop
			}
		}
		for hop := 1; hop <= maxHop; hop++ {
			key := fmt.Sprintf("_h%d", hop)
			obs := int(m.Gauges[metricRecallIntraHopRecall+key+"_obs"])
			if obs == 0 {
				continue
			}
			t.Logf("  hop %d: symbolic_recall=%.3f embed_recall=%.3f coherence=%.3f (%d obs)",
				hop, m.Gauges[metricRecallIntraHopRecall+key],
				m.Gauges[metricRecallIntraEmbedHopRecall+key],
				m.Gauges[metricRecallIntraHopRecall+key+"_coherence"], obs)
		}
	} else {
		// Symbolic-only (mock) run: the intra layer never fires (no embedder), so
		// only the oracle's PREDICTED recoverability curve is meaningful; observed
		// recall and coherence are by-construction 0.
		t.Logf("intra-thread hop-recall (oracle PREDICTED recoverability — symbolic-only run, intra layer off; observed n/a):")
		maxHop := 0
		for hop := range gen.intraHopTotal {
			if hop > maxHop {
				maxHop = hop
			}
		}
		for hop := 1; hop <= maxHop; hop++ {
			key := fmt.Sprintf("_h%d", hop)
			obs := int(m.Gauges[metricRecallIntraHopRecall+key+"_obs"])
			if obs == 0 {
				continue
			}
			t.Logf("  hop %d: predicted_recall=%.3f (%d obs)",
				hop, m.Gauges[metricRecallIntraHopRecall+key], obs)
		}
	}

	// Intra-thread recall by TURN-DEPTH (#109 H2 quality curve, turn-depth axis)
	// — the SIBLING of the hop curve, bucketed by how many turns back the
	// oracle-predicted recall target scrolled out (log-scale, powers of B=16).
	// REPORT-ONLY characterization (no gate, no floor).
	if intraProbeObs > 0 {
		depthBuckets := make([]int, 0, len(gen.intraDepthTotal))
		for b := range gen.intraDepthTotal {
			depthBuckets = append(depthBuckets, b)
		}
		sort.Ints(depthBuckets)
		if len(depthBuckets) > 0 {
			if embeddingRun {
				t.Logf("intra-thread recall by turn-depth (symbolic predicted vs embedding-observed = actual user recall; report-only):")
			} else {
				t.Logf("intra-thread recall by turn-depth (oracle PREDICTED recoverability — symbolic-only run, observed n/a; report-only):")
			}
		}
		for _, b := range depthBuckets {
			key := fmt.Sprintf("_d%d", b)
			obs := int(m.Gauges[metricRecallIntraRecallByDepth+key+"_obs"])
			if obs == 0 {
				continue
			}
			predicted := m.Gauges[metricRecallIntraRecallByDepth+key]
			embed := m.Gauges[metricRecallIntraEmbedRecallByDepth+key]
			// Range-validate to [0,1] as a sanity log only — report-only, never
			// fail the test on these values.
			if predicted < 0 || predicted > 1 || embed < 0 || embed > 1 {
				t.Logf("  WARN depth %s: ratio out of [0,1] (predicted=%.3f embed=%.3f)",
					intraDepthBucketLabel(b), predicted, embed)
			}
			if embeddingRun {
				t.Logf("  depth %s: predicted=%.3f embed=%.3f (%d obs)",
					intraDepthBucketLabel(b), predicted, embed, obs)
			} else {
				t.Logf("  depth %s: predicted=%.3f (%d obs)",
					intraDepthBucketLabel(b), predicted, obs)
			}
		}
	}

	// Intra-thread coherence divergence — the #109 tripwire (its GATE is in
	// evalRungGates; this is the report line). 0 on a clean mock run.
	intraDivergence := int(m.Gauges[metricRecallIntraCoherenceDivergence])
	t.Logf("intra-thread coherence divergence: %d (#109 mock PASS = 0; the decay curve is the deliverable, divergence is the failure)",
		intraDivergence)

	// W1 recall-preservation QUALITY MEASURE (#111 / design §7.1, the
	// approximation-drift canary). REPORTED, NOT GATED (#119): a nonzero
	// divergence means the heuristic substituted a within-top-Kf leaf, NOT
	// necessarily lost recall. On the symbolic mock run no W1 probe runs.
	descentDivergence := int(m.Counters[scenarios.MetricRecallIntraDescentDivergence])
	descentProbes := int(m.Counters[scenarios.MetricRecallIntraDescentProbes])
	if descentProbes == 0 {
		t.Logf("recall_intra_descent_divergence: n/a — no W1 probe ran this rung (symbolic mock run, or main thread never grew a usable summary tree)")
	} else {
		t.Logf("recall_intra_descent_divergence (quality measure): %d over %d W1 probes — descent-vs-flat top-Kf divergence; approximation-drift canary. Nonzero = the heuristic substituted a within-Kf leaf, NOT necessarily lost recall — see the §7.1 classification below and the exact tiers (#117) for guaranteed-exhaustive recall.",
			descentDivergence, descentProbes)
		// W1 divergence classification (#111 §7.1 DIAGNOSTIC): strict-miss / tie /
		// tree-mismatch breakdown — settles whether a divergence is key-quality or
		// structural. Pure observation; per-probe detail is in logs/.
		t.Logf("recall_intra_w1 classification: strict_miss=%d tie=%d tree_mismatch=%d (#111 §7.1 diagnostic — strict_miss=real loss/fix keys; tie=equal-cosine/fix tie-break; tree_mismatch=staleness/build edge)",
			int(m.Counters[scenarios.MetricRecallIntraW1StrictMiss]),
			int(m.Counters[scenarios.MetricRecallIntraW1Tie]),
			int(m.Counters[scenarios.MetricRecallIntraW1TreeMismatch]))
	}

	// F-B rebuild trip-wire (#111 / design §7.2, fork F-B). A WATCH metric, NOT a
	// hard gate: if it trends up with main-thread length on the long rungs,
	// semantic rebalancing is not staying bounded.
	t.Logf("recall_intra_tree_rebuild_calls: %d (F-B trip-wire — semantic-rebalance churn; watch, no gate; escalates to the hybrid MAD if it trends with main-thread length)",
		int(m.Counters[scenarios.MetricRecallIntraTreeRebuildCalls]))

	t.Logf("wall-clock runtime: %s", rep.wall.Round(time.Millisecond))

	// Headline output: a LOWER BOUND on the six-month simulation runtime. 62400
	// is the approximate turn count of the six-month acceptance run (spec §9.1).
	// Scaling the rung's mean per-turn wall cost by it is only a floor: per-turn
	// cost grows with spine size, and at short rungs the population is small.
	const sixMonthTurns = 62400
	perTurnMs := rep.meanMs
	floor := time.Duration(sixMonthTurns*perTurnMs) * time.Millisecond
	t.Logf("per-turn cost:    %.2fms (mean wall)", perTurnMs)
	t.Logf("extrapolated 6 m runtime ≥ %s (floor; real per-turn cost grows "+
		"with spine size, so the actual run will exceed this)", floor.Round(time.Second))
}

// evalRungGates evaluates every pass/fail gate for a rung against the
// materialized rep.m, consulting the single gatePolicy to decide which
// oracle-dependent gates fire as hard asserts versus log-only observations. It
// makes NO summary log of its own beyond the gate-context lines — the readable
// rung summary is logRungSummary's job — so a gate and its summary line cannot
// silently drift apart.
//
// The gates: closure-flow liveness (§3.5); wander coherence-divergence band
// (criterion b, §4.3); intra-thread coherence-divergence tripwire (#109,
// mock-only); criterion (e) no-eviction precondition (§4.3); the R6 history-cap
// design signal (substrate-invariant, always hard); and the
// recall_unexplained_absence genuine-loss canary (#100).
func evalRungGates(t *testing.T, label string, rep rungReport, gen *generator, policy gatePolicy) {
	t.Helper()
	m := rep.m

	// Closure: counted from retire.complete log lines. A multi-turn workload
	// spans enough idle turns to decay-close some threads, so a non-zero count is
	// the expected, healthy signal that the §3.5 closure flow is live.
	if rep.closures == 0 {
		// Closure depends on which threads the runtime engaged/decayed, which is
		// plan-driven; under inference-in-loop the real model engages different
		// threads than the canned plan, so a zero is an observation, not a defect.
		if policy.oracleGatesAssert() {
			t.Errorf("closures: got 0; the §3.5 closure flow is dead — expected >0 over the %s workload", label)
		} else {
			t.Logf("closures: got 0 (live-inference: plan-driven closure not asserted)")
		}
	}

	// Criterion (b) pass condition: oracle and runtime must AGREE per hop. Real
	// divergence beyond a tiny band means the §4.3 eviction-free coherence
	// assumption broke — STOP. A small band absorbs borderline-Jaccard rounding;
	// a structural break shows up as divergence scaling with obs.
	divergence := int(m.Gauges[metricWanderCoherenceDivergence])
	totalProbeObs := 0
	for hop := 0; hop < wanderMaxHops; hop++ {
		key := fmt.Sprintf("_h%d", hop)
		totalProbeObs += int(m.Gauges[metricWanderOriginRecallByHops+key+"_obs"])
	}
	if totalProbeObs > 0 && policy.oracleGatesAssert() {
		divergenceBand := totalProbeObs / 20 // 5% rounding band
		if divergence > divergenceBand {
			t.Errorf("criterion (b) FAILURE: wander coherence divergence %d exceeds band %d over %d probe obs — "+
				"oracle and runtime DISAGREE on abandoned-topic recall; the §4.3 eviction-free coherence "+
				"assumption broke (check criterion e / history_len_max). Do NOT trust the decay curve until resolved.",
				divergence, divergenceBand, totalProbeObs)
		}
	} else if !policy.oracleGatesAssert() && totalProbeObs > 0 {
		// The abandoned-topic recall oracle compares the runtime's match-fires
		// against the canned plan; under live inference the runtime fired on a
		// different thread set, so divergence here measures plan-vs-model drift,
		// not a coherence break. Report, do not fail.
		t.Logf("wander coherence divergence %d over %d probe obs (live-inference: oracle blind — not asserted)",
			divergence, totalProbeObs)
	}

	// Layer-B shadow cross-check (burndown #8): the generator's shadow LRU vs
	// the runtime's authoritative ActiveThreads. The comparison is purely
	// structural (LRU membership), independent of the embedder — it holds on
	// the mock and the live-EMBEDDING run (both follow the canned plan), so it
	// is hard ==0 whenever the runtime follows the plan (oracleGatesAssert).
	// Under live INFERENCE the real model engages a different thread set than
	// the plan, so the shadow legitimately diverges → report only.
	layerBDivergence := int(m.Gauges[metricLayerBShadowDivergence])
	if policy.oracleGatesAssert() {
		if layerBDivergence != 0 {
			t.Errorf("burndown #8 FAILURE: Layer-B shadow divergence %d != 0 — the generator's shadow LRU and the "+
				"runtime's ActiveThreads disagree (a runtime-resident thread the shadow did not predict). The recall "+
				"oracle's expected-sets are computed off the shadow, so a divergence silently corrupts them. Root-cause "+
				"the shadow engage()/eviction model against the runtime LRU (see the forensic log for the offending ids).",
				layerBDivergence)
		}
	} else {
		t.Logf("Layer-B shadow divergence %d (live-inference: real model engages off-plan — shadow legitimately diverges, not asserted)",
			layerBDivergence)
	}

	// Intra-thread coherence divergence — the #109 tripwire. SCOPED MOCK-ONLY
	// (#109/#111 Finding B): the comparison (symbolic shadow-chunk oracle vs the
	// runtime's intra fine-tier match-fire) is valid only when both sides are
	// symbolic. On a live-embedding run the intra tier fires real embeddings,
	// which legitimately diverge from a symbolic oracle (#96), so it is report-only
	// there; under live inference the oracle is blind. Hard ==0 gate on the mock.
	embeddingRun := gen.embeddingRun
	intraProbeObs := 0
	for _, total := range gen.intraHopTotal {
		intraProbeObs += total
	}
	intraDivergence := int(m.Gauges[metricRecallIntraCoherenceDivergence])
	if policy.liveEmbedding {
		// Live-embedding run: report only — see the scoping rationale above (#96).
		t.Logf("intra-thread coherence divergence %d over %d probe obs (live-embedding: symbolic oracle vs embedding runtime — "+
			"NOT asserted; the head-to-head curve is the live deliverable, not coherence==0)",
			intraDivergence, intraProbeObs)
	} else if embeddingRun && policy.intraDivergenceAsserts() && intraDivergence != 0 {
		t.Errorf("#109 FAILURE: intra-thread coherence divergence %d != 0 over %d probe obs — the shadow-chunk oracle and "+
			"the runtime fine tier DISAGREE on early-content recall outside the debt-window blind spot. Root-cause the "+
			"oracle/runtime coherence (do NOT widen forgiveness — that disables the canary).",
			intraDivergence, intraProbeObs)
	}

	// Criterion (e): max per-thread retained symbol count must stay under the
	// eviction cap — the no-eviction precondition the oracle's coherence rests on
	// (§4.3). The generator's own shadow retained set is the authoritative
	// per-thread emitted-symbol union; assert its max is below the cap.
	maxRetained := 0
	for i := range gen.threads {
		if gen.isCarrier(i) || gen.isMainThread(i) {
			// Dedicated measurement carriers absorb the abandoned-topic-probe and
			// campaign-recall query pollution by design (#96 inc 2); they are
			// excluded from the recall oracle on both sides, so their bloated shadow
			// set is not a no-eviction-coherence hazard and is not counted against
			// the cap. The Candidate-A main thread (#109) is likewise excluded: its
			// retained set is UNBOUNDED by design — its multi-year trajectory is the
			// intra-thread fine tier's reason to exist, scored by the intra-thread
			// probe, not by symbolic whole-thread Jaccard.
			continue
		}
		if n := len(gen.shadowRetainedSet(i)); n > maxRetained {
			maxRetained = n
		}
	}
	t.Logf("max per-thread retained symbols: %d (criterion e: must stay < eviction cap %d)",
		maxRetained, historyCapForReport)
	if maxRetained >= historyCapForReport && policy.oracleGatesAssert() {
		// This is the generator's shadow-set no-eviction precondition for the
		// abandoned-topic oracle. Under live inference that oracle is off, so the
		// precondition it guards is moot. Report only.
		t.Errorf("criterion (e) FAILURE: max per-thread retained symbols %d >= eviction cap %d — "+
			"the oracle's no-eviction assumption (§4.3) breaks; eviction modeling now required",
			maxRetained, historyCapForReport)
	}

	// R6 (Build-Plan §4): history_len / ever_central must not blow past the hard
	// cap. The cap is a storage invariant the runtime enforces; a max-per-thread
	// above it would be an unbounded-growth design signal. This is
	// substrate-invariant (oracle-independent), so it is ALWAYS a hard assert.
	if maxHist := int(m.Gauges["history_len_max"]); maxHist > historyCapForReport {
		t.Errorf("R6 design signal: max history_len/thread %d exceeds hard cap %d — "+
			"history is growing unbounded; investigate before tuning away", maxHist, historyCapForReport)
	}

	// Genuine-loss canary (#100): recall_unexplained_absence counts every recall
	// expectation whose thread is OFF the live spine AND NOT in the
	// archive.archived log — a thread that is neither recall-live nor
	// recoverable-via-fetch. The oracle no longer manufactures these, so any
	// nonzero value is a real off-spine-not-archived data loss. Gate hard on ==0.
	if absent := m.Counters["recall_unexplained_absence"]; absent != 0 {
		// The canary keys off the recall oracle's expected set (built from canned
		// tags). Under inference-in-loop the runtime engages/creates threads the
		// plan never named, manufacturing false absences. Report, do not fail.
		if policy.oracleGatesAssert() {
			t.Errorf("recall_unexplained_absence = %d, want 0 — a recall expectation names a thread that is "+
				"OFF the live spine AND NOT archived (genuine integrity loss, or an uncovered oracle/execution-timing "+
				"artifact). This is the genuine-loss canary; do NOT relax it without root-causing every count.", absent)
		} else {
			t.Logf("recall_unexplained_absence = %d (live-inference: oracle expected-set is plan-derived, "+
				"runtime diverged — canary not asserted)", absent)
		}
	}
}
