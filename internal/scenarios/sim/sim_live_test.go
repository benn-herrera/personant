//go:build live_sim

// Live-mode simulation entry point (#98). Build-tag isolated (`live_sim`)
// and run via `make sim-live`; it is NOT compiled into `make test` /
// `make sim`, so the mock-deterministic acceptance gate cannot
// accidentally activate a live endpoint. It runs the SAME sim workload as
// TestSim and is the seam where the live toggles activate.
//
// As of Increment 1 (this file) the two live toggles are PARSED and
// LOGGED but wired as NO-OPS: when off (default) the existing
// mock-client / nil-embedder path runs unchanged; when on, Inc 1 only
// loads + validates the test/rundata config and logs that the live
// embedder/client swap is deferred to Inc 2/3. The config-load and
// usable-endpoint guard run regardless of which toggle is on, so live
// mode invoked with nothing reachable is a LOUD failure (design §2), not
// a skip — distinct from the integration tests' skipIfUnreachable.
//
// NEVER reads/writes/operates in ~/.personant; the apiKeyFile targets are
// resolved by store.LoadProviders (this file passes paths to the loader,
// it does not read key files itself).

package sim

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/log"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/scenarios"
	"personant/internal/store"
)

// Live-mode runtime toggles (design §3). Independent — either, both, or
// neither. Default OFF. In Inc 1 they are parsed + logged but wired as
// no-ops; the embedder/client swap lands in Inc 2/3.
var (
	liveEmbedding = flag.Bool("sim.live-embedding", false,
		"live-sim: install the real §3.4 embedder for embedding-in-loop recall (Inc 2; no-op in Inc 1)")
	liveInference = flag.Bool("sim.live-inference", false,
		"live-sim: install the real chat model for inference-in-loop (Inc 3; no-op in Inc 1)")
)

// rundataDir holds the USER-provided, gitignored (test/rundata*/) live
// config. NEVER ~/.personant. The two files are required for live mode.
var rundataDir = filepath.Join("..", "..", "..", "test", "rundata")

const (
	liveProvidersFile = "test.providers.toml"
	liveConfigFile    = "test.config.toml"
)

// liveConfigMissingMsg is the design §2 fail-with-suggestion message for
// the missing-files case. It is a constant so the test and any future
// caller share one wording (DRY).
const liveConfigMissingMsg = "test.providers.toml and test.config.toml are required in test/rundata/ for live-sim mode. " +
	"Suggestion: copy ~/.personant/{config,providers}.toml to test/rundata/test.{config,providers}.toml " +
	"(apiKeyFile paths may need to be absolute)."

// liveEndpoint pairs a resolved provider with the role and model it was
// selected for, for logging + the usable-endpoint probe. vectorLength is
// the [embedding] vectorLength config (Matryoshka truncation; 0 = native);
// meaningful only for the embedding role.
type liveEndpoint struct {
	role         string // "chat" or "embedding"
	provider     memops.Provider
	model        string
	vectorLength int
}

// TestSimLive is the live-mode sibling of TestSim. It loads + validates
// the test/rundata live config and runs the usable-endpoint guard
// (FAIL-not-skip on missing files or an unreachable endpoint), then runs
// the SAME workload TestSim runs. In Inc 1 the toggles are no-ops: the run
// is the ordinary mock/nil path regardless of the flags; only the config
// load + endpoint probe are exercised live, with the actual embedder/
// client swap deferred to Inc 2/3.
func TestSimLive(t *testing.T) {
	d, err := parseSimDuration(*simDuration)
	if err != nil {
		t.Fatalf("invalid -sim.duration %q: %v (use 1d|1w|1m|2m|6m, or `<N>d` calendar days like 30d, or a Go duration like 168h)",
			*simDuration, err)
	}

	if !*liveEmbedding && !*liveInference {
		// Neither toggle set: live mode invoked with nothing live. The
		// design makes this a loud failure — `make sim-live` always passes
		// both flags, so reaching here means a hand-run forgot them.
		t.Fatalf("TestSimLive requires -sim.live-embedding and/or -sim.live-inference; " +
			"neither is set. Run via `make sim-live`, or pass a toggle explicitly.")
	}

	// Config load + usable-endpoint guard (design §2). Runs whenever EITHER
	// toggle is on; a failure here is fatal (fail-not-skip).
	endpoints := loadLiveEndpoints(t)
	probeLiveEndpoints(t, endpoints)

	// Inc 2: embedding-in-loop. When -sim.live-embedding is on, build a
	// recaller factory that installs the real §3.4 embedder; the harness
	// Prepares its index and keeps it current via the per-thread-creation
	// AddThread seam (see runSimRung's recaller arg + harness installRecaller).
	// Mock inference is UNCHANGED — the canned thread bodies stay deterministic
	// so the recall oracle's ExpectedRecallMatches stays valid, and symbolic
	// recall runs alongside (both layers fire) for the head-to-head.
	var recaller func(ops memops.MemoryOps) measure.Recaller
	if *liveEmbedding {
		ep := embeddingEndpoint(t, endpoints)
		log.Info("sim-live: installing live §3.4 embedder — provider=%s model=%s dims=%d (embedding-in-loop, Inc 2)",
			ep.provider.Name, ep.model, ep.vectorLength)
		recaller = func(ops memops.MemoryOps) measure.Recaller {
			return measure.NewService(ops, model.NewHTTPEmbedder(ep.provider, ep.model, ep.vectorLength))
		}
	}

	// Inc 3: inference-in-loop. When -sim.live-inference is on, install the real
	// chat client on State.Client (replacing the scripted mock). This is a SHORT
	// BEHAVIOR-VALIDATION mode, NOT a coherent recall workload: a real model
	// emits different topic tags / symbols than the canned plan, so the runtime
	// engages/creates different threads, which both invalidates the recall
	// oracle AND breaks the generator's forward-planning coherence (it plans the
	// next step's engagement from canned tags). oracleBlind is therefore set in
	// lockstep with the client install, downgrading every oracle/coherence gate
	// to an observation; the run is capped short (liveInferenceMaxDuration).
	var liveClient model.Client
	var liveModel string
	oracleBlind := false
	if *liveInference {
		if d > liveInferenceMaxDuration {
			t.Fatalf("sim-live: -sim.live-inference is a SHORT behavior-validation mode (a real model emits "+
				"different tags than the canned plan, so a multi-day workload is structurally incoherent). "+
				"Requested span %s exceeds the cap %s — re-run with -sim.duration=1d (or shorter).",
				d, liveInferenceMaxDuration)
		}
		ep := chatEndpoint(t, endpoints)
		client := model.NewHTTPClient(ep.provider)
		liveClient, liveModel = client, ep.model
		oracleBlind = true
		log.Info("sim-live: installing live chat model — provider=%s model=%s (inference-in-loop, Inc 3; "+
			"oracle/coherence gates BLINDED, behavior-validation only)", ep.provider.Name, ep.model)
	}

	corpus := loadCorpusSlots(t)
	h := runSimRung(t, "sim-live-"+*simDuration, d, corpus, recaller, oracleBlind, liveClient, liveModel)

	// Inference-in-loop behavior-validation summary (#98, Inc 3). These are the
	// real-model behaviors the mock structurally cannot exercise. They are
	// derived from the runtime's OWN event log (the runtime's authoritative
	// view of what the model emitted), never from the canned plan — so they are
	// honest about the live model even though the recall oracle is blind.
	if *liveInference {
		reportInferenceBehavior(t, h)
	}
}

// liveInferenceMaxDuration is the T2-1 short-run cap on inference-in-loop. A
// real chat model diverges from the canned plan immediately, so the workload
// is only coherent for a behavior-validation sniff, not a multi-day recall
// run; one sim day is plenty to observe tag discipline, extraction, streaming,
// and the §5.5 re-prompt. A longer request is refused (TestSimLive fails with
// guidance) rather than silently producing meaningless "recall" over an
// incoherent workload.
const liveInferenceMaxDuration = 24 * time.Hour

// chatEndpoint returns the resolved chat endpoint from the loaded set,
// failing if it is absent (it must be present when -sim.live-inference is on,
// since loadLiveEndpoints resolved it under the same flag). Mirrors
// embeddingEndpoint.
func chatEndpoint(t *testing.T, endpoints []liveEndpoint) liveEndpoint {
	t.Helper()
	for _, ep := range endpoints {
		if ep.role == "chat" {
			return ep
		}
	}
	t.Fatalf("sim-live: -sim.live-inference set but no chat endpoint resolved (internal inconsistency)")
	return liveEndpoint{}
}

// reportInferenceBehavior logs the inference-in-loop behavior-validation
// metrics (#98, Inc 3) from the run's event log. The recall oracle is blind
// under live inference, so these — NOT any recall number — are what the mode
// measures:
//
//   - tag-emission discipline: the runtime logs `topic.tag-missing` whenever
//     the model's response carried no parseable §5.1 topic tag. The parseable
//     rate = (turns - tag-missing) / turns is the headline. A high miss rate
//     is a real model regression on the tag contract the whole substrate rests
//     on — the signal the mock can never produce.
//   - real symbol extraction (observability): threads_created + match-fires
//     count what the real model's tags/anchors drove through the runtime.
//   - streaming: turn.Run drives ConsultStream; the run reaching this point
//     with no turn.Run error (RunScenario t.Fatalf's otherwise) confirms the
//     harness drained a real streaming endpoint cleanly.
//   - §5.5 mid-turn re-prompt: `topic.re-prompt` counts dormant-resumption
//     re-prompts the real model drove and the runtime handled without desync.
func reportInferenceBehavior(t *testing.T, h *scenarios.Harness) {
	t.Helper()
	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("reportInferenceBehavior: read metrics blob: %v", err)
	}
	turns := m.Counters["turns"]
	tagMissing := logEventCount(t, h, "topic.tag-missing")
	tagParsed := int(turns) - tagMissing
	parsedRate := 0.0
	if turns > 0 {
		parsedRate = float64(tagParsed) / float64(turns)
	}

	t.Logf("=== inference-in-loop behavior validation (#98, oracle BLIND — no recall claim) ===")
	t.Logf("turns:                    %d (live chat model, streaming via ConsultStream)", turns)
	t.Logf("topic-tag discipline:     %d/%d parseable (%.3f); %d missing (runtime `topic.tag-missing`)",
		tagParsed, int(turns), parsedRate, tagMissing)
	t.Logf("threads created:          %d (real-model tags/anchors that drove §3.0.4 creation)",
		m.Counters["threads_created"])
	t.Logf("engaged existing threads: %d (real-model tags naming a live thr_<n>)",
		m.Counters["engaged_existing_threads"])
	t.Logf("topic.warning lines:      %d (malformed-tag drift the runtime tolerated)",
		logEventCount(t, h, "topic.warning"))
	t.Logf("§5.5 mid-turn re-prompts: %d (dormant-resumption re-prompts handled with no queue desync)",
		logEventCount(t, h, "topic.re-prompt"))
	t.Logf("spine.match-fire:         %d (symbolic recall fires off real-model symbols — observability only)",
		logEventCount(t, h, "spine.match-fire "))
	if *liveEmbedding {
		t.Logf("spine.embed-match-fire:   %d (embedding recall fires; fullest live mode, still oracle-blind)",
			logEventCount(t, h, "spine.embed-match-fire "))
	}
}

// embeddingEndpoint returns the resolved embedding endpoint from the
// loaded set, failing if it is absent (it must be present when
// -sim.live-embedding is on, since loadLiveEndpoints resolved it under the
// same flag). Separated so TestSimLive reads as the toggle wiring it is.
func embeddingEndpoint(t *testing.T, endpoints []liveEndpoint) liveEndpoint {
	t.Helper()
	for _, ep := range endpoints {
		if ep.role == "embedding" {
			return ep
		}
	}
	t.Fatalf("sim-live: -sim.live-embedding set but no embedding endpoint resolved (internal inconsistency)")
	return liveEndpoint{}
}

// loadLiveEndpoints loads the test/rundata providers + config, resolves
// the selected chat and embedding providers, and returns them. It FAILS
// (not skips) on a missing config file or an unresolvable selection
// (design §2): live mode explicitly invoked with nothing usable is a loud
// failure. It does NOT read apiKeyFile targets itself — store.LoadProviders
// resolves those.
func loadLiveEndpoints(t *testing.T) []liveEndpoint {
	t.Helper()
	providersPath := filepath.Join(rundataDir, liveProvidersFile)
	configPath := filepath.Join(rundataDir, liveConfigFile)

	// Missing-files guard. store.LoadProviders / LoadConfig treat a
	// nonexistent file as an EMPTY (not error) result — a fresh-home
	// convenience — so an os.Stat is the explicit presence check live mode
	// needs to produce the design's fail-with-suggestion message.
	for _, p := range []string{providersPath, configPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s (looked for %s: %v)", liveConfigMissingMsg, p, err)
		}
	}

	providers, faults, err := store.LoadProviders(providersPath)
	if err != nil {
		t.Fatalf("sim-live: load %s: %v", providersPath, err)
	}
	for _, f := range faults {
		// A fault means a declared provider's apiKeyFile was unreadable;
		// log it (path/IO detail only, never key content) — it is fatal only
		// if it is the provider a toggle selected, which the resolve below
		// catches.
		log.Warn("sim-live: provider %s omitted from pool: %s", f.Name, f.Reason)
	}

	cfg, err := store.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("sim-live: load %s: %v", configPath, err)
	}

	var endpoints []liveEndpoint
	// Resolve the chat selection when inference is requested.
	if *liveInference {
		endpoints = append(endpoints, resolveEndpoint(t, "chat", cfg.Chat.DefaultModel, providers))
	}
	// Resolve the embedding selection when embedding is requested. The
	// [embedding] vectorLength rides along (Matryoshka truncation), so the
	// embedder is built with the same dimensionality production would use.
	if *liveEmbedding {
		ep := resolveEndpoint(t, "embedding", cfg.Embedding.Model, providers)
		ep.vectorLength = cfg.Embedding.VectorLength
		endpoints = append(endpoints, ep)
	}
	return endpoints
}

// resolveEndpoint parses a "<provider>/<model>" config ref and looks the
// provider up in the loaded pool. It FAILS on an empty/malformed ref or an
// unknown provider — a clear unconfigured-endpoint message naming the role
// and ref (design §2).
func resolveEndpoint(t *testing.T, role, ref string, providers memops.Providers) liveEndpoint {
	t.Helper()
	if ref == "" {
		t.Fatalf("sim-live: %s endpoint unconfigured — %s has no [%s] model ref in %s",
			role, liveConfigFile, role, filepath.Join(rundataDir, liveConfigFile))
	}
	providerName, modelName, ok := memops.ParseModelRef(ref)
	if !ok {
		t.Fatalf("sim-live: %s endpoint ref %q is not a \"provider/model\" reference", role, ref)
	}
	provider, ok := providers.Get(providerName)
	if !ok {
		t.Fatalf("sim-live: %s endpoint references provider %q, which is not in the %s pool",
			role, providerName, liveProvidersFile)
	}
	if provider.BaseURL == "" {
		t.Fatalf("sim-live: %s provider %q has no baseUrl in %s", role, providerName, liveProvidersFile)
	}
	return liveEndpoint{role: role, provider: provider, model: modelName}
}

// liveProbeTimeout bounds the per-endpoint reachability probe. A live
// model server answers /v1/models in well under a second on a warm
// machine; a few seconds covers a cold start without hanging the guard.
const liveProbeTimeout = 10 * time.Second

// probeLiveEndpoints runs the usable-endpoint check (design §2): each
// resolved provider must be REACHABLE, proven by a cheap GET
// <baseUrl>/models. A probe failure is FATAL (fail-not-skip), naming the
// role / provider / URL so the operator knows which endpoint is down —
// distinct from the integration tests, which skip on the same condition.
func probeLiveEndpoints(t *testing.T, endpoints []liveEndpoint) {
	t.Helper()
	for _, ep := range endpoints {
		client, ok := model.NewHTTPClient(ep.provider).(*model.HTTPClient)
		if !ok {
			t.Fatalf("sim-live: %s: NewHTTPClient did not return *HTTPClient (cannot probe)", ep.role)
		}
		ctx, cancel := context.WithTimeout(context.Background(), liveProbeTimeout)
		_, err := client.ListModels(ctx)
		cancel()
		if err != nil {
			t.Fatalf("sim-live: %s endpoint unreachable — provider %q at %s is configured but not reachable "+
				"(GET /models failed: %v). Bring the endpoint up or fix the baseUrl in %s.",
				ep.role, ep.provider.Name, ep.provider.BaseURL, err, liveProvidersFile)
		}
		// Resolved AND reachable: a usable endpoint. Log the selection (no key
		// material — the resolved key never appears in a log line).
		log.Info("sim-live: %s endpoint usable — provider=%s baseUrl=%s model=%s (reachable)",
			ep.role, ep.provider.Name, ep.provider.BaseURL, ep.model)
	}
	log.Info("sim-live: at=%s — all %d requested endpoint(s) usable; live wiring deferred to Inc 2/3, running mock path",
		clock.Profiling().Format(time.RFC3339), len(endpoints))
}
