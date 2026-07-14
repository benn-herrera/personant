package sim

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"personant/internal/scenarios"
	"personant/internal/store"
)

// Within-session interleaving telemetry (SPEC §9.1 "too coherent" concern).
// These accumulators are pure post-emission bookkeeping folded from each
// session's emitted bufSteps (slotIdx / engagedIdx): they draw no rng and do
// not influence control flow, so they leave the canonical step stream a pure
// function of (Seed, Duration, Corpus). The test folds the per-session and
// per-dwell samples into the metrics blob post-run.

// beginInterleaveSession resets the per-session interleaving accumulators.
// Called once at the top of every runSession.
func (g *generator) beginInterleaveSession() {
	g.sessSlots = map[int]struct{}{}
	g.sessThreads = map[int]struct{}{}
	g.sessTurnCount = 0
	g.dwellPrev = 0
	g.dwellLen = 0
	g.dwellActive = false
}

// observeInterleave folds one just-emitted step into the per-session
// interleaving accumulators. Pure bookkeeping: no rng, no control flow.
func (g *generator) observeInterleave(bs bufStep) {
	g.sessSlots[bs.slotIdx] = struct{}{}
	g.sessThreads[bs.engagedIdx] = struct{}{}
	g.sessTurnCount++

	// Topic dwell: extend the current run while engagedIdx is unchanged;
	// otherwise flush the finished run and start a new one.
	if g.dwellActive && bs.engagedIdx == g.dwellPrev {
		g.dwellLen++
		return
	}
	if g.dwellActive {
		g.topicDwellRuns = append(g.topicDwellRuns, g.dwellLen)
	}
	g.dwellPrev = bs.engagedIdx
	g.dwellLen = 1
	g.dwellActive = true
}

// finalizeInterleaveSession pushes the per-session summary samples and
// flushes the final in-progress dwell run. Called once per non-empty
// session (a session that emitted no steps contributes no samples).
func (g *generator) finalizeInterleaveSession() {
	g.sessionDistinctSlots = append(g.sessionDistinctSlots, len(g.sessSlots))
	g.sessionDistinctThreads = append(g.sessionDistinctThreads, len(g.sessThreads))
	g.sessionTurns = append(g.sessionTurns, g.sessTurnCount)
	if g.dwellActive {
		g.topicDwellRuns = append(g.topicDwellRuns, g.dwellLen)
	}
}

// percentile returns the q-quantile (0..1) of xs using nearest-rank.
// Returns 0 for an empty series.
func percentile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	idx := int(q * float64(len(sorted)-1))
	return sorted[idx]
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// computeIntraThreadGauges is the PURE shadow-derived half of
// recordIntraThreadMetrics (#120): it returns the §4.3 perf-decay + intra-thread
// hop/depth gauge map from the generator's CURRENT shadow state plus the supplied
// (liveThreads, p50/p95/p99), WITHOUT touching h.Metrics. Because the generator
// accumulates its shadow counters step-by-step, calling this mid-run yields the
// run-to-date values; calling it at end-of-run yields the final values — and the
// last daily snapshot therefore equals the end-of-run summary for the same seed
// (the #120 correctness invariant). recordCosineOpsMeasured (which reads the live
// recaller, not the shadow) and the W1 classification counters are NOT included
// here — they are set/accumulated by their own call sites.
func computeIntraThreadGauges(gen *generator, liveThreads int, p50, p95, p99 float64) map[string]float64 {
	g := map[string]float64{}

	// §4.3 perf-decay series.
	g[metricRecallIndexCoarseSize] = float64(liveThreads)
	g[metricRecallIndexFineChunks] = float64(gen.totalFineChunks())
	g[metricRecallIndexFineChunksMain] = float64(gen.mainThreadChunkCount())
	g[metricRecallQueryLatencyP50] = p50
	g[metricRecallQueryLatencyP95] = p95
	g[metricRecallQueryLatencyP99] = p99
	// The §6.5 flush cost (recall_index_flush_calls/_chunks) is OBSERVED from
	// the runtime, not modeled here (#126) — the harness folds the actual
	// turn.State flush counters into run-total Metrics counters. It is therefore
	// NOT part of this pure shadow-derived map; recordIntraThreadMetrics and the
	// daily snapshot read it directly off h.Metrics, exactly as cosine_ops is
	// read off the live recaller.

	// Per-hop intra-thread recall + coherence.
	divergence := 0
	for hop, total := range gen.intraHopTotal {
		if total == 0 {
			continue
		}
		key := fmt.Sprintf("_h%d", hop)
		// Symbolic predicted-recoverability curve (H2): the fraction of probes
		// at this hop the oracle predicts recoverable. This is the #109 fidelity
		// curve "symbolic now" — a coherence signal, NOT validated user recall.
		g[metricRecallIntraHopRecall+key] = float64(gen.intraHopPredictHit[hop]) / float64(total)
		g[metricRecallIntraHopRecall+key+"_obs"] = float64(total)
		// Oracle/runtime coherence at this hop — meaningful only on an
		// embedding-live run (the mock run leaves the observed/coherence tallies
		// at 0, so this reads 0.0 there and is reported as "n/a — symbolic-only").
		g[metricRecallIntraHopRecall+key+"_coherence"] = float64(gen.intraHopCoherent[hop]) / float64(total)
		// Embedding-OBSERVED per-hop recall (#109/#111 Finding B head-to-head),
		// scored against the SAME forgiven ground truth as the symbolic curve
		// above and on the SAME denominator (intraHopTotal), so the two columns
		// are directly comparable. intraHopObservedHit is the runtime's
		// spine.intra-match-fire for the oracle-predicted leaf, recorded only on
		// an embedding-live run — 0 at every hop on the mock run (no intra
		// layer), exactly as metricWanderEmbedRecallByHops is on the mock run.
		// This is the recall users actually get; mirror of how wander derives
		// metricWanderEmbedRecallByHops from its embed-match-fire observations.
		g[metricRecallIntraEmbedHopRecall+key] = float64(gen.intraHopObservedHit[hop]) / float64(total)
		divergence += gen.intraHopDiverge[hop]
	}

	// Per-turn-depth intra-thread recall (#109 H2 quality curve, turn-depth
	// axis) — the SIBLING of the per-hop curve, on a log-scale turn-depth bucket
	// (powers of B=16). Same derivation as the hop pair: symbolic predicted curve
	// (recall_intra_recall_bydepth, every run) + an _obs companion, plus the
	// embedding-observed curve (recall_intra_embed_recall_bydepth, 0 at every
	// bucket on the mock run — no intra layer — exactly as the embed-hop column
	// is). REPORT-ONLY characterization: no gate, no floor.
	for bucket, total := range gen.intraDepthTotal {
		if total == 0 {
			continue
		}
		key := fmt.Sprintf("_d%d", bucket)
		g[metricRecallIntraRecallByDepth+key] = float64(gen.intraDepthPredictHit[bucket]) / float64(total)
		g[metricRecallIntraRecallByDepth+key+"_obs"] = float64(total)
		g[metricRecallIntraEmbedRecallByDepth+key] = float64(gen.intraDepthObservedHit[bucket]) / float64(total)
	}
	g[metricRecallIntraCoherenceDivergence] = float64(divergence)

	// B1 completeness-floor (flush-lag dead-zone) tally (§3.4 / #123): the
	// denominator (dead-zone probes observed) and the runtime's hits. The
	// completeness gate (completenessFloorAsserts) reads these directly:
	// non-vacuity requires _total > 0, and the floor holds iff _hit == _total.
	// Both 0 on the mock run (no intra layer); the headline on a live-embedding
	// run. Plain run-to-date counts (the daily snapshot reuses this pure compute,
	// so they must be cumulative shadow state — which they are).
	g[metricRecallCompletenessDeadZoneTotal] = float64(gen.intraDeadZoneTotal)
	g[metricRecallCompletenessDeadZoneHit] = float64(gen.intraDeadZoneObservedHit)

	return g
}

// dailyFilename is the per-sim-day stats series the #120 day-close handler
// appends to, inside the scenario's rundata directory (h.RunHome). One JSON
// object per line, one line per sim-day, in day order.
const dailyFilename = "daily.jsonl"

// dailyRecord is one sim-day's stats line in daily.jsonl (#120). RunToDate is
// the cumulative state at the close of day Day; DayDelta is the difference from
// the prior day's close (so the series carries both the rung-point reads and a
// per-day trajectory for spike detection). The CORRECTNESS INVARIANT: the
// RunToDate of the LAST record equals the end-of-run summary for the same run
// (and day-N of a long run equals an Nd run), because RunToDate is computed from
// the same counters/percentiles/computeIntraThreadGauges the summary uses.
type dailyRecord struct {
	Day       int        `json:"day"`
	SimDate   string     `json:"sim_date"`
	RunToDate dailyStats `json:"run_to_date"`
	DayDelta  dailyDelta `json:"day_delta"`
}

// dailyStats is the cumulative run-to-date snapshot. The headline scalars are
// named; the full sim-derived intra-thread gauge set (the H2 depth buckets
// recall_intra_recall_bydepth_d<k>, the per-hop recall curve, divergence,
// flush, cosine-ops, etc.) rides in IntraGauges so the series carries the §9.2
// curve per day without a field per bucket.
type dailyStats struct {
	Turns          int64   `json:"turns"`
	PerTurnMsMean  float64 `json:"per_turn_ms_mean"`
	LatencyP50     float64 `json:"latency_p50"`
	LatencyP95     float64 `json:"latency_p95"`
	LatencyP99     float64 `json:"latency_p99"`
	ThreadsCreated int64   `json:"threads_created"`
	SpineSize      int     `json:"spine_size"`
	SleepCycles    int64   `json:"sleep_cycles"`

	RecallQueryCosineOps     float64 `json:"recall_query_cosine_ops"`
	RecallIndexFlushCalls    float64 `json:"recall_index_flush_calls"`
	RecallIntraDescentDiverg int64   `json:"recall_intra_descent_divergence"`
	RecallIntraW1StrictMiss  int64   `json:"recall_intra_w1_strict_miss"`
	RecallIntraW1Tie         int64   `json:"recall_intra_w1_tie"`
	RecallIntraW1TreeMism    int64   `json:"recall_intra_w1_tree_mismatch"`

	// IntraGauges carries the full shadow-derived intra-thread gauge set
	// (computeIntraThreadGauges) — H2 depth buckets, per-hop recall, divergence,
	// blindspot, fine-chunk counts. This is the run-to-date #109 curve.
	IntraGauges map[string]float64 `json:"intra_gauges"`
}

// dailyDelta is the per-day movement: counters diffed against the prior day's
// close, and turn-latency percentiles computed over ONLY this day's slice of
// the turn_duration_ms histogram (histogram[prevLen:currLen]).
type dailyDelta struct {
	TurnsThisDay         int64   `json:"turns_this_day"`
	PerTurnMsMeanThisDay float64 `json:"per_turn_ms_mean_this_day"`
	LatencyP50ThisDay    float64 `json:"latency_p50_this_day"`
	LatencyP95ThisDay    float64 `json:"latency_p95_this_day"`
	LatencyP99ThisDay    float64 `json:"latency_p99_this_day"`
}

// dailySnapshotWriter appends one run-to-date stats record to daily.jsonl per
// sim-day (#120). The generator is the shadow-state source for
// computeIntraThreadGauges; prevTurns / prevHistLen carry the prior close's
// cumulative turns and turn_duration_ms histogram length so the per-day delta
// is a clean diff. day is the running 1-based ordinal it stamps.
//
// Firing model — interior + final, mirroring the heavy-invariant cadence
// ("always fires once at end-of-run regardless of cadence"):
//
//   - onDayClose fires at each interior cadence tick (days 1..N-1 of an N-day
//     run): the simulated clock crosses 24h, 48h, …, well inside the run.
//   - finalize fires ONCE from runSimRung after the end-of-run summary is
//     computed (the LAST tick, N×24h, does NOT fire on its own — the run ends
//     when simNow ≥ Duration, a few jittered steps past the tick, so the
//     trailing partial day would otherwise be uncaptured). finalize emits day N
//     with the EXACT end-of-run summary values, which is what makes the LAST
//     daily record equal the end-of-run summary (the #120 correctness
//     invariant) and "day-N of a long run == an Nd run" valid.
type dailySnapshotWriter struct {
	gen         *generator
	day         int
	prevTurns   int64
	prevHistLen int
}

func newDailySnapshotWriter(gen *generator) *dailySnapshotWriter {
	return &dailySnapshotWriter{gen: gen}
}

// onDayClose is the scenarios.Scenario.OnSimDayClose handler for an INTERIOR
// sim-day tick. It computes run-to-date stats from the LIVE harness state
// (cumulative counters + percentiles over the full turn_duration_ms histogram so
// far, current spine size for liveThreads) and the generator shadow, then appends
// one record. It reads h.Metrics through the read-only accessors and writes only
// the daily file, so it NEVER mutates the final gauges — the end-of-run summary
// is unchanged. simDate is the tick instant; the harness's `day` argument is
// ignored in favor of the writer's own ordinal so interior and final records
// share one monotonic counter.
func (w *dailySnapshotWriter) onDayClose(h *scenarios.Harness, _ int, simDate time.Time) {
	durations := h.Metrics.HistogramSnapshot(scenarios.MetricTurnDurationMs)
	spineSize := 0
	if recs, err := store.ReadSpine(h.Paths.Spine); err == nil {
		spineSize = len(recs)
	}
	p50 := percentile(durations, 0.50)
	p95 := percentile(durations, 0.95)
	p99 := percentile(durations, 0.99)
	w.appendRecord(h, simDate, durations, spineSize, p50, p95, p99)
}

// finalize emits the LAST daily record (day N) from runSimRung, AFTER the
// end-of-run summary is computed, using the EXACT summary inputs — the final
// histogram, the final spine population (liveThreads), and the summary's
// p50/p95/p99 — so the record's run_to_date equals the end-of-run summary by
// construction (#120 correctness invariant). simDate is the run's final pinned
// instant. It must be called exactly once, after the last onDayClose.
func (w *dailySnapshotWriter) finalize(h *scenarios.Harness, simDate time.Time, liveThreads int, p50, p95, p99 float64) {
	durations := h.Metrics.HistogramSnapshot(scenarios.MetricTurnDurationMs)
	w.appendRecord(h, simDate, durations, liveThreads, p50, p95, p99)
}

// appendRecord builds one dailyRecord from the supplied run-to-date inputs and
// appends it to daily.jsonl. The shared body of onDayClose (interior, live
// state) and finalize (end-of-run, summary state) so the two emit byte-identical
// schema. durations is the run-to-date turn_duration_ms slice; liveThreads is the
// run-to-date spine size (the coarse-tier vector count); p50/p95/p99 are over
// durations. The per-day delta is over durations[prevHistLen:], the slice added
// since the prior record.
func (w *dailySnapshotWriter) appendRecord(h *scenarios.Harness, simDate time.Time, durations []float64, liveThreads int, p50, p95, p99 float64) {
	w.day++
	currHistLen := len(durations)
	// Guard the delta slice bounds: a record can in principle be emitted with no
	// new turns since the prior one (an empty day), so clamp prevHistLen.
	lo := w.prevHistLen
	if lo > currHistLen {
		lo = currHistLen
	}
	dayHist := durations[lo:currHistLen]

	// Run-to-date sim-derived gauges from the SAME pure function the end-of-run
	// summary uses — fed the run-to-date liveThreads + percentiles. At finalize
	// these are the exact end-of-run values (the #120 correctness invariant).
	intra := computeIntraThreadGauges(w.gen, liveThreads, p50, p95, p99)

	turns := h.Metrics.CounterValue(scenarios.MetricTurns)
	rec := dailyRecord{
		Day:     w.day,
		SimDate: simDate.Format("2006-01-02"),
		RunToDate: dailyStats{
			Turns:          turns,
			PerTurnMsMean:  mean(durations),
			LatencyP50:     p50,
			LatencyP95:     p95,
			LatencyP99:     p99,
			ThreadsCreated: h.Metrics.CounterValue(scenarios.MetricThreadsCreated),
			SpineSize:      liveThreads,
			SleepCycles:    h.Metrics.CounterValue(scenarios.MetricSleepCycles),
			// recall_query_cosine_ops is the LIVE-recaller gauge, NOT a shadow
			// value (computeIntraThreadGauges does not compute it). Read it
			// DIRECTLY off the recaller's run-to-date accessors (the SAME source
			// recordCosineOpsMeasured uses for the end-of-run gauge), NOT off
			// metricRecallQueryCosineOps — that gauge is only Set at end-of-run,
			// so reading it here would leave every INTERIOR day stuck at 0 even on
			// an embedding-live run (#120 interior-record bug). The recaller's
			// CosineOps()/RecallQueries() accumulate throughout the run, so this is
			// a true run-to-date value; 0 on the mock path, where the perf bend is
			// honestly unmeasurable. At finalize this equals the summary's gauge by
			// construction (one source) — the #120 last-record==summary invariant.
			RecallQueryCosineOps: liveCosineOpsPerQuery(h),
			// recall_index_flush_calls is the OBSERVED §6.5 flush cost (#126), read
			// run-to-date via h.FlushCostRunToDate (drained-counter + live session) —
			// NOT a shadow-modeled value (computeIntraThreadGauges no longer computes
			// it) and NOT off the end-of-run gauge (which is only Set at finalize, so
			// reading it here would leave interior days stuck at 0). Because the read
			// adds the LIVE session it tracks correctly on every interior day, not
			// just after a restart drain. At finalize this equals the summary's gauge
			// by construction (one FlushCostRunToDate source) — the #120
			// last-record==summary invariant, exactly as cosine_ops.
			RecallIndexFlushCalls:    flushCallsRunToDate(h),
			RecallIntraDescentDiverg: h.Metrics.CounterValue(scenarios.MetricRecallIntraDescentDivergence),
			RecallIntraW1StrictMiss:  h.Metrics.CounterValue(scenarios.MetricRecallIntraW1StrictMiss),
			RecallIntraW1Tie:         h.Metrics.CounterValue(scenarios.MetricRecallIntraW1Tie),
			RecallIntraW1TreeMism:    h.Metrics.CounterValue(scenarios.MetricRecallIntraW1TreeMismatch),
			IntraGauges:              intra,
		},
		DayDelta: dailyDelta{
			TurnsThisDay:         turns - w.prevTurns,
			PerTurnMsMeanThisDay: mean(dayHist),
			LatencyP50ThisDay:    percentile(dayHist, 0.50),
			LatencyP95ThisDay:    percentile(dayHist, 0.95),
			LatencyP99ThisDay:    percentile(dayHist, 0.99),
		},
	}

	line, err := json.Marshal(rec)
	if err != nil {
		h.T.Errorf("daily snapshot day %d: marshal: %v", w.day, err)
		return
	}
	path := filepath.Join(h.RunHome, dailyFilename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		h.T.Errorf("daily snapshot day %d: open %s: %v", w.day, path, err)
		return
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		h.T.Errorf("daily snapshot day %d: write: %v", w.day, err)
	}
	if err := f.Close(); err != nil {
		h.T.Errorf("daily snapshot day %d: close: %v", w.day, err)
	}

	w.prevTurns = turns
	w.prevHistLen = currHistLen
}

// cosineOpsReporter is the recaller surface the MEASURED recall_query_cosine_ops
// metric reads (#111 / design §7.2). measure.Service satisfies it; the
// symbolic-only default does not (no embedding recall ran), so the metric is 0
// on the mock acceptance path — honestly, the perf bend is measurable only on
// an embedding-live run. Mirrors the harness's optional-interface discipline.
type cosineOpsReporter interface {
	CosineOps() int64
	RecallQueries() int64
}

// liveCosineOpsPerQuery reads the LIVE recaller's run-to-date MEASURED per-query
// cosine-op count (§7.2): the recaller's run-total cosine comparisons divided by
// the queries that performed them. The recaller's CosineOps()/RecallQueries()
// accumulate throughout the run (atomic, read concurrent-safe), so this is a
// valid run-to-date read at ANY day-close — not just end-of-run. Returns 0 when
// no embedding recaller is installed or no embedding query ran (the symbolic mock
// path), where the perf bend is honestly unmeasurable. Single source of truth for
// both the end-of-run summary (recordCosineOpsMeasured) and the per-day snapshot
// (appendRecord), so the LAST daily record equals the summary by construction.
func liveCosineOpsPerQuery(h *scenarios.Harness) float64 {
	if h.State != nil && h.State.Recaller != nil {
		if rep, ok := h.State.Recaller.(cosineOpsReporter); ok {
			if q := rep.RecallQueries(); q > 0 {
				return float64(rep.CosineOps()) / float64(q)
			}
		}
	}
	return 0
}

// flushCallsRunToDate reads the OBSERVED §6.5 flush-CALL count run-to-date
// (#126) for the daily record's named field, via h.FlushCostRunToDate (drained
// counter + live session). Single source of truth shared with the end-of-run
// gauge (recordFlushCostObserved), so the LAST daily record's
// recall_index_flush_calls equals the summary gauge by construction (the #120
// invariant). Returns 0 before any flush fired (e.g. a rung too short to scroll
// the main thread past the assembly window).
func flushCallsRunToDate(h *scenarios.Harness) float64 {
	calls, _ := h.FlushCostRunToDate()
	return float64(calls)
}

// recordCosineOpsMeasured emits the MEASURED per-query cosine-op count (§7.2)
// into the end-of-run gauge. Reads the live recaller off the harness's State via
// liveCosineOpsPerQuery; emits 0 when no embedding recaller is installed or no
// embedding query ran (the symbolic mock path). Counted at the scoring call sites
// (scoring.CosineCounter), so the O(C_main)→O(log n) bend is empirical, not
// modeled.
func recordCosineOpsMeasured(h *scenarios.Harness) {
	h.Metrics.Set(metricRecallQueryCosineOps, liveCosineOpsPerQuery(h))
}

// recordFlushCostObserved surfaces the OBSERVED §6.5 fine-tier flush cost
// (#126) as the end-of-run gauges. h.FlushCostRunToDate sums the run-total
// counter (sessions drained at each RestartSession) with the live session's
// not-yet-drained count, giving the whole-run flush cost the §6.2 debt-cap +
// dormancy policy actually paid — the real number, not the old
// scrolled-out/turn.EmbeddingDebtCap estimate. Reading from the SAME
// FlushCostRunToDate the daily snapshot reads makes the last daily record equal
// the summary by construction (the #120 invariant), exactly as cosine_ops does.
func recordFlushCostObserved(h *scenarios.Harness) {
	calls, chunks := h.FlushCostRunToDate()
	h.Metrics.Set(metricRecallIndexFlushCalls, float64(calls))
	h.Metrics.Set(metricRecallIndexFlushChunks, float64(chunks))
}
