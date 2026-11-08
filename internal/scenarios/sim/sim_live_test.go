// Live-mode setup for the unified TestSim (#98). There is NO separate live
// test function: TestSim is the single entry point, and these helpers supply
// the live-vs-stand-in elements it installs per toggle. The two toggles
// (-sim.live-embedding / -sim.live-inference, both default false) are read by
// TestSim; with NEITHER set (bare `make test` / `make sim`) setupLiveElements
// returns zero values immediately — no endpoint touch — so the deterministic
// mock acceptance gate never reaches any live code here and never skips. When
// a toggle is on, the usable-endpoint guard runs against the `reaper` endpoint
// from internal/testsupport (base URL overridable via PERSONANT_REAPER_URL),
// so live mode invoked with nothing reachable is a LOUD failure (design §2),
// not a skip.
//
// NEVER reads/writes/operates in ~/.personant.

package sim

import (
	"context"
	"errors"
	"flag"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/log"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/scenarios"
	"personant/internal/testsupport"
)

// Live-mode runtime toggles (design §3), read by TestSim. Independent —
// either, both, or neither. Default OFF; both-off IS the mock acceptance gate.
var (
	liveEmbedding = flag.Bool("sim.live-embedding", false,
		"sim: install the real §3.4 embedder for embedding-in-loop recall (off = symbolic-only stand-in)")
	liveInference = flag.Bool("sim.live-inference", false,
		"sim: install the real chat model for inference-in-loop (off = scripted mock client stand-in)")
)

// setupLiveElements supplies TestSim with the live-vs-stand-in elements for
// the run: a recaller factory (live embedder, or nil = symbolic-only
// stand-in), a live chat client + model (or nil = scripted mock stand-in), and
// oracleBlind (true only under live inference). With NEITHER toggle set it
// returns all zero values WITHOUT touching any endpoint — that is the mock
// acceptance gate path, and it must stay a no-op here (no skip, no probe).
//
// When a toggle is on it runs the usable-endpoint guard against the testsupport
// `reaper` endpoint (FAIL-not-skip on an unreachable endpoint, design §2;
// override the URL with PERSONANT_REAPER_URL). For embedding it
// builds a recaller factory installing the real §3.4 embedder (the harness
// Prepares its index and keeps it current via the per-thread-creation
// AddThread seam); mock inference is unchanged, so the recall oracle stays
// valid and symbolic recall runs alongside (both layers fire) for the
// head-to-head. For inference it installs the real chat client on State.Client
// (replacing the scripted mock) — a SHORT behavior-validation mode, NOT a
// coherent recall workload: a real model emits different topic tags / symbols
// than the canned plan, so the runtime engages/creates different threads,
// which both invalidates the recall oracle AND breaks the generator's
// forward-planning coherence. oracleBlind is therefore set in lockstep with
// the client install, and the span is refused if it exceeds the short cap
// (liveInferenceMaxDuration).
func setupLiveElements(t *testing.T, d time.Duration) (
	recaller func(ops memops.MemoryOps) measure.Recaller,
	liveClient model.Client, liveModel string, oracleBlind bool,
) {
	t.Helper()
	if !*liveEmbedding && !*liveInference {
		// The mock acceptance gate (bare `make test` / `make sim`, or
		// `make sim LIVE_EMBEDDING=false LIVE_INFERENCE=false`). No live element to
		// install: return zero values and touch no endpoint. The gate
		// RUNS — this is not a skip.
		return nil, nil, "", false
	}

	// Usable-endpoint guard (design §2). Runs whenever EITHER toggle is on;
	// a failure here is fatal (fail-not-skip).
	url := testsupport.ReaperURL()
	probeLiveEndpoint(t, url)

	if *liveEmbedding {
		log.Info("sim-live: installing live §3.4 embedder — url=%s model=%s dims=native (embedding-in-loop, Inc 2)",
			url, testsupport.ReaperEmbeddingModel)
		recaller = func(ops memops.MemoryOps) measure.Recaller {
			return measure.NewService(ops,
				model.NewHTTPEmbedder(url, testsupport.ReaperAPIKey, testsupport.ReaperEmbeddingModel, 0))
		}
	}

	if *liveInference {
		if d > liveInferenceMaxDuration {
			t.Fatalf("sim-live: -sim.live-inference is a SHORT behavior-validation mode (a real model emits "+
				"different tags than the canned plan, so a multi-day workload is structurally incoherent). "+
				"Requested span %s exceeds the cap %s — re-run with -sim.duration=1d (or shorter).",
				d, liveInferenceMaxDuration)
		}
		liveClient = model.NewHTTPClient(url, testsupport.ReaperAPIKey)
		liveModel = testsupport.ReaperChatModel
		oracleBlind = true
		log.Info("sim-live: installing live chat model — url=%s model=%s (inference-in-loop, Inc 3; "+
			"oracle/coherence gates BLINDED, behavior-validation only)", url, liveModel)
	}

	return recaller, liveClient, liveModel, oracleBlind
}

// liveInferenceMaxDuration is the T2-1 short-run cap on inference-in-loop. A
// real chat model diverges from the canned plan immediately, so the workload
// is only coherent for a behavior-validation sniff, not a multi-day recall
// run; one sim day is plenty to observe tag discipline, extraction, streaming,
// and the §5.5 re-prompt. A longer request is refused (TestSimLive fails with
// guidance) rather than silently producing meaningless "recall" over an
// incoherent workload.
const liveInferenceMaxDuration = 24 * time.Hour

// reportInferenceBehavior logs the inference-in-loop behavior-validation
// metrics (#98, Inc 3) from the run's event log. The recall oracle is blind
// under live inference, so these — NOT any recall number — are what the mode
// measures:
//
//   - tag-emission discipline: the runtime logs `topic.tag-missing` whenever
//     the FINAL response of a turn carried no parseable §5.1 topic tag. The
//     parseable rate = (turns - tag-missing) / turns is the headline. A high
//     miss rate is a real model regression on the tag contract the whole
//     substrate rests on — the signal the mock can never produce.
//     HONEST ACCOUNTING: a D6-recovered omission (first stream tag-less →
//     re-prompt → second stream tagged) never logs `topic.tag-missing`, so
//     that line alone UNDERCOUNTS model omissions. True omission events =
//     topic.tag-missing + topic.re-prompt(cause=missing-tag) — a stream-level
//     count: a double-miss turn emits BOTH lines (one omission each for its
//     two streams), so the sum is not a turn count and the two series must
//     not be naively added to the turn-denominated rate. Both are reported
//     below.
//   - real symbol extraction (observability): threads_created + match-fires
//     count what the real model's tags/anchors drove through the runtime.
//   - streaming: turn.Run drives ConsultStream; the run reaching this point
//     with no turn.Run error (RunScenario t.Fatalf's otherwise) confirms the
//     harness drained a real streaming endpoint cleanly.
//   - mid-turn re-prompts: `topic.re-prompt` is split by cause= —
//     missing-thread (§5.5 dormant-resumption fetches) and missing-tag (D6
//     protocol reminders) — and each series is reported separately; both
//     confirm the runtime handled the extra stream without desync.
func reportInferenceBehavior(t *testing.T, h *scenarios.Harness, d time.Duration, corpus []CorpusSlot) {
	t.Helper()

	// A2 tag-fidelity grader (live-inference only). It regenerates the canonical
	// plan for the SAME cfg the run used (Seed/Duration/Corpus + the live-inference
	// large-input profile runSimRung installs), grades the model's tag discipline
	// against plan intent, and re-writes the metrics blob with the three
	// tag-fidelity series. Gated inside recordTagFidelityMetrics (liveInference
	// true here) — the mock path never reaches it, keeping the mock blob clean.
	cfg := WorkloadConfig{Seed: simSeed, Duration: d, Corpus: corpus}
	cfg.LargeInputEveryN = largeInputRungEveryN
	cfg.LargeInputBytes = largeInputBytesFor(memops.DefaultBudget())
	recordTagFidelityMetrics(t, h, cfg, true)

	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("reportInferenceBehavior: read metrics blob: %v", err)
	}
	turns := m.Counters[scenarios.MetricTurns]
	tagMissing := logEventCount(t, h, "topic.tag-missing")
	tagParsed := int(turns) - tagMissing
	parsedRate := 0.0
	if turns > 0 {
		parsedRate = float64(tagParsed) / float64(turns)
	}
	// Split the shared topic.re-prompt act by cause=: the two series measure
	// different failure modes (§5.5 dormant-resumption fetch vs D6 tag
	// omission) and folding them together misreports both.
	fetchReprompts := logEventCount(t, h, "topic.re-prompt cause=missing-thread")
	tagReprompts := logEventCount(t, h, "topic.re-prompt cause=missing-tag")
	// Stream-level model omissions (see the doc comment above): recovered
	// re-prompts leave no topic.tag-missing line, and a double-miss turn
	// emits one line of EACH — the sum counts omitted streams, not turns.
	omissionEvents := tagMissing + tagReprompts

	t.Logf("=== inference-in-loop behavior validation (#98, oracle BLIND — no recall claim) ===")
	t.Logf("rundata:                  %s (manifest.json binds mode/seed/duration/git-head)", h.RunHome)
	t.Logf("turns:                    %d (live chat model, streaming via ConsultStream)", turns)
	t.Logf("topic-tag discipline:     %d/%d parseable (%.3f); %d missing (runtime `topic.tag-missing`, FINAL responses only)",
		tagParsed, int(turns), parsedRate, tagMissing)
	t.Logf("model tag omissions:      %d stream-level events (= %d tag-missing + %d missing-tag re-prompts; "+
		"a double-miss turn emits both — not a turn count)",
		omissionEvents, tagMissing, tagReprompts)
	t.Logf("threads created:          %d (real-model tags/anchors that drove §3.0.4 creation)",
		m.Counters[scenarios.MetricThreadsCreated])
	t.Logf("engaged existing threads: %d (real-model tags naming a live thr_<n>)",
		m.Counters[scenarios.MetricEngagedExistingThreads])
	t.Logf("topic.warning lines:      %d (malformed-tag drift the runtime tolerated)",
		logEventCount(t, h, "topic.warning"))
	t.Logf("§5.5 mid-turn re-prompts: %d (cause=missing-thread dormant-resumption fetches, no queue desync)",
		fetchReprompts)
	t.Logf("D6 tag re-prompts:        %d (cause=missing-tag protocol reminders; recovered omissions log no tag-missing)",
		tagReprompts)
	t.Logf("spine.match-fire:         %d (symbolic recall fires off real-model symbols — observability only)",
		logEventCount(t, h, "spine.match-fire "))
	if *liveEmbedding {
		t.Logf("spine.embed-match-fire:   %d (embedding recall fires; fullest live mode, still oracle-blind)",
			logEventCount(t, h, "spine.embed-match-fire "))
	}
}

// recordTagFidelityMetrics is the A2 tag-fidelity live-inference entry point. It
// builds the plan (ground-truth intent) / observed (event-log tag outcomes) /
// live (post-run spine) inputs, grades the re-engagement series, computes the
// anchor-emission overlap from the post-run frontmatter, and writes all three
// registry series (plus diagnostic companions) into the metrics blob. It NO-OPS
// when liveInference is false — the mock-run vacuity guard: a scripted mock's
// tags are true-by-construction, so the series would be vacuous (the
// request_prompt_tokens liveClient-gating precedent).
//
// It re-writes the metrics blob after Setting the series because runSimRung
// already wrote it once; h.Metrics still holds every prior series, so the
// re-write is the full blob plus the tag-fidelity additions. It never fails the
// test on an embedder problem — an unreachable/unconfigured embedder degrades the
// residue to unadjudicated (logged, counted); only a blob-rewrite fault is fatal.
func recordTagFidelityMetrics(t *testing.T, h *scenarios.Harness, cfg WorkloadConfig, liveInference bool) {
	t.Helper()
	if !liveInference {
		return // vacuity guard — no series on the mock path
	}
	ctx := context.Background()

	plan := buildPlanTurns(cfg)
	observed, joinDegraded := buildObservedTurns(h.Paths.LogsDir)
	live := buildLiveThreads(h.Paths)

	// Observed turns that join no plan turn (execution-time refinement/
	// injected turns carry no canonical Step.At; count-mismatched instants
	// contribute their surplus too) — a forensic count; a large value means
	// the plan↔observed join is degrading.
	planCount := make(map[string]int, len(plan))
	for _, p := range plan {
		planCount[p.instant]++
	}
	unmatched := 0
	for inst, turns := range observed {
		if extra := len(turns) - planCount[inst]; extra > 0 {
			unmatched += extra
		}
	}

	emb := resolveGraderEmbedder(ctx) // nil when unreachable/unconfigured
	res := gradeTagFidelity(ctx, emb, plan, observed, live, tagFidelityRankK, tagFidelityBorderlineEps)
	res.unmatchedGroups = unmatched

	overlap, overlapObs, detSymbols := anchorEmissionOverlap(h.Paths)
	overlapMeasured := overlapObs > 0 && detSymbols > 0

	// Miss rate over DECIDED turns (hits+misses); borderline/unadjudicated held
	// out. 0 when nothing was decided (honest "unmeasured", not a false 0 rate).
	decided := res.hits + res.misses
	missRate := 0.0
	if decided > 0 {
		missRate = float64(res.misses) / float64(decided)
	}
	spuriousRate := 0.0
	if res.reengageIntent > 0 {
		spuriousRate = float64(res.spuriousNewTopic) / float64(res.reengageIntent)
	}

	h.Metrics.Set(scenarios.MetricTagFidelityReengageMissRate, missRate)
	h.Metrics.Set(scenarios.MetricTagFidelitySpuriousNewTopicRate, spuriousRate)
	// The anchor-overlap gauge is set ONLY when measurable (≥1 model-anchor
	// thread AND ≥1 persisted deterministic symbol anywhere in the run). With
	// zero deterministic symbols the Jaccard is 0-against-empty-set by
	// construction — an absent key is the honest "unmeasured", distinguishable
	// from a measured 0.0 (the Jul-16 "0.000/151 threads" false alarm).
	if overlapMeasured {
		h.Metrics.Set(scenarios.MetricTagFidelityAnchorOverlap, overlap)
	}

	h.Metrics.Set(metricTagFidelityReengageIntent, float64(res.reengageIntent))
	h.Metrics.Set(metricTagFidelityHits, float64(res.hits))
	h.Metrics.Set(metricTagFidelityMisses, float64(res.misses))
	h.Metrics.Set(metricTagFidelitySpuriousCount, float64(res.spuriousNewTopic))
	h.Metrics.Set(metricTagFidelityTagMissing, float64(res.tagMissing))
	h.Metrics.Set(metricTagFidelityBorderline, float64(res.borderline))
	h.Metrics.Set(metricTagFidelityUnadjudicated, float64(res.unadjudicated))
	h.Metrics.Set(metricTagFidelityAnchorObs, float64(overlapObs))
	h.Metrics.Set(metricTagFidelityJoinAmbiguous, float64(res.joinAmbiguous))
	joinDegradedVal := 0.0
	if joinDegraded {
		joinDegradedVal = 1.0
	}
	h.Metrics.Set(metricTagFidelityJoinDegraded, joinDegradedVal)

	embState := "reachable"
	if emb == nil {
		embState = "none (residue unadjudicated)"
	}
	joinState := "per-turn (context.modified source=user.prompt boundary)"
	if joinDegraded {
		joinState = "DEGRADED instant-merged (no per-turn boundary lines — same-second turns smear)"
	}
	log.Info("=== tag-fidelity (A2, oracle=plan-intent, embedder=%s) ===", embState)
	log.Info("rundata: %s", h.RunHome)
	log.Info("join: %s; join-ambiguous reengage turns: %d", joinState, res.joinAmbiguous)
	log.Info("re-engagement intent turns: %d (decided=%d)", res.reengageIntent, decided)
	log.Info("  miss rate:        %.3f (misses=%d)", missRate, res.misses)
	log.Info("  spurious *new-topic* rate: %.3f (count=%d)", spuriousRate, res.spuriousNewTopic)
	log.Info("  tag-missing (plan-joined classification, not the runtime stream count): %d  borderline:%d  unadjudicated:%d",
		res.tagMissing, res.borderline, res.unadjudicated)
	if overlapMeasured {
		log.Info("  anchor overlap:   %.3f (obs=%d threads, deterministic symbols=%d)", overlap, overlapObs, detSymbols)
	} else {
		log.Info("  anchor overlap:   UNMEASURED (no deterministic symbols — %d model-anchor threads, %d persisted source=deterministic history symbols; 0-against-empty Jaccard would be vacuous)",
			overlapObs, detSymbols)
	}
	log.Info("  unmatched observed turns (refinement/injected): %d", unmatched)

	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Fatalf("tag-fidelity: rewrite metrics blob: %v", err)
	}
}

// resolveGraderEmbedder builds the grader's OWN embedder against the testsupport
// `reaper` endpoint — the same endpoint the sim-live install path uses —
// WITHOUT touching the runtime Recaller (oracle independence). It is non-fatal:
// an unreachable endpoint returns nil so the residue falls back to
// unadjudicated; a nil handle is the honest "no adjudication available" signal.
func resolveGraderEmbedder(ctx context.Context) graderEmbedder {
	url := testsupport.ReaperURL()
	if err := pingLiveEndpoint(ctx, url); err != nil {
		log.Warn("tag-fidelity: grader embedder %s at %s unreachable (%v) — residue unadjudicated",
			testsupport.ReaperEmbeddingModel, url, err)
		return nil
	}
	log.Info("tag-fidelity: grader embedder = %s at %s dims=native (measurement-side, independent of runtime recaller)",
		testsupport.ReaperEmbeddingModel, url)
	return model.NewHTTPEmbedder(url, testsupport.ReaperAPIKey, testsupport.ReaperEmbeddingModel, 0)
}

// liveProbeTimeout bounds the reachability probe. A live model server
// answers /v1/models in well under a second on a warm machine; a few
// seconds covers a cold start without hanging the guard.
const liveProbeTimeout = 10 * time.Second

// pingLiveEndpoint reports whether the endpoint at url answers a cheap
// GET <url>/models within liveProbeTimeout.
func pingLiveEndpoint(ctx context.Context, url string) error {
	client, ok := model.NewHTTPClient(url, testsupport.ReaperAPIKey).(*model.HTTPClient)
	if !ok {
		return errors.New("NewHTTPClient did not return *HTTPClient (cannot probe)")
	}
	pctx, cancel := context.WithTimeout(ctx, liveProbeTimeout)
	defer cancel()
	_, err := client.ListModels(pctx)
	return err
}

// probeLiveEndpoint runs the usable-endpoint check (design §2): the endpoint
// must be REACHABLE. A probe failure is FATAL (fail-not-skip), naming the URL
// and its override so the operator knows which endpoint is down. No key
// material appears in any log line.
func probeLiveEndpoint(t *testing.T, url string) {
	t.Helper()
	if err := pingLiveEndpoint(context.Background(), url); err != nil {
		t.Fatalf("sim-live: endpoint %s unreachable (GET /models failed: %v). "+
			"Bring the endpoint up or point PERSONANT_REAPER_URL at a reachable one.", url, err)
	}
	log.Info("sim-live: at=%s — endpoint %s usable; installing the selected live element(s)",
		clock.Profiling().Format(time.RFC3339), url)
}
