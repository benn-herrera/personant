// Package testsupport holds shared helpers for live-endpoint tests that
// span more than one package (currently internal/model and internal/turn).
// It is a NON-test-only package on purpose: the helpers are imported by
// test files in different packages, which a `_test.go`-local helper cannot
// serve, and a shared *_test.go file cannot cross a package boundary.
//
// The live-test convention these helpers enforce:
//
//   - Tests ALWAYS COMPILE (no //go:build tag). They are part of the
//     normal `go vet ./...` / `just test` compile, so a refactor that
//     breaks them is caught immediately instead of rotting silently.
//   - EXECUTION is gated at runtime on an explicit opt-in (LiveTestsEnv).
//     Not opted in → the test t.Skips: bare `just test` compiles and skips
//     it, hitting no endpoint and staying fast.
//   - Opted in but the endpoint is unreachable / misconfigured → FAIL,
//     not skip. You explicitly asked for the live path; nothing live is a
//     real failure, not a reason to pass quietly.
//
// This package imports only stdlib. It deliberately does NOT import
// internal/model: the model package's own *_test.go files are in
// `package model` and import this package, so a model import here would be
// an import cycle. The model-presence check therefore takes a small
// locally-defined lister interface instead of a concrete model client.
package testsupport

import (
	"context"
	"os"
	"testing"
)

// LiveTestsEnv is the opt-in environment variable for live-endpoint tests.
// Set it to any non-empty value (the justfile recipes set it to "1")
// to RUN the live tests; leave it unset to SKIP them. One constant so the
// helpers, the justfile, and any future caller share one name (DRY).
const LiveTestsEnv = "PERSONANT_LIVE_TESTS"

// CorpusTestsEnv is the opt-in environment variable for the slow,
// artifact-dependent recall-corpus measurement tests. Set it to any
// non-empty value (the justfile's recall-corpus-test recipe sets it) to RUN
// them; leave it unset to SKIP them. Distinct from LiveTestsEnv: these
// tests are gated on cost + derived-artifact presence, not a live endpoint.
const CorpusTestsEnv = "PERSONANT_CORPUS_TESTS"

// SlowSimTestsEnv is the opt-in environment variable for the slow, in-suite
// sim mock rungs whose wall-clock would blow the `just test` per-package
// timeout budget. Set it to any non-empty value (the justfile's
// sim-shadow-slow-test recipe sets it) to RUN them; leave it unset to SKIP
// them. Distinct from the other two: these are gated purely on wall-clock
// cost within the normal (non-live, non-corpus) mock suite — the fast arm of
// the same test still runs in the default `just test`.
const SlowSimTestsEnv = "PERSONANT_SLOW_SIM_TESTS"

// ReaperEmbeddingModel is the embedding model id served by the local,
// non-metered `reaper` endpoint.
const ReaperEmbeddingModel = "Qwen3-Embedding-8B"

// ReaperChatModel is the chat model id served by the `reaper` endpoint.
const ReaperChatModel = "gemma-4-main"

// ReaperAPIKey is the `reaper` endpoint's local non-metered placeholder
// key (no secret involved).
const ReaperAPIKey = "dummy"

// reaperDefaultURL is the dev-machine reaper endpoint, overridable via the
// PERSONANT_REAPER_URL environment variable.
const reaperDefaultURL = "http://reaper.local:4000/v1"

// ReaperURL returns the base URL of the `reaper` endpoint used by the live
// tests: PERSONANT_REAPER_URL when set, else the dev-machine default.
func ReaperURL() string {
	if url := os.Getenv("PERSONANT_REAPER_URL"); url != "" {
		return url
	}
	return reaperDefaultURL
}

// RequireLive skips the calling test unless the live opt-in is set. Call it
// first in every live-endpoint test: with the opt-in unset, the test
// compiles (caught by `just test`) and skips without touching the network.
func RequireLive(t *testing.T) {
	t.Helper()
	if os.Getenv(LiveTestsEnv) == "" {
		t.Skipf("live test skipped — set %s=1 (or run `just integration-test`) to exercise the live endpoint", LiveTestsEnv)
	}
}

// RequireCorpus skips the calling test unless the corpus opt-in is set.
// Call it first in every recall-corpus measurement test: with the opt-in
// unset, the test compiles (caught by `just test`) and skips without doing
// the slow corpus work. When opted in but the required derived artifact is
// absent, the test still skips with a regeneration hint (the artifact
// loaders handle that) — the corpus artifacts are .gitignore'd, so absence
// is an expected "regenerate first" state, not a defect.
func RequireCorpus(t *testing.T) {
	t.Helper()
	if os.Getenv(CorpusTestsEnv) == "" {
		t.Skipf("corpus measurement skipped — run `just recall-corpus-test` (sets %s) to exercise it", CorpusTestsEnv)
	}
}

// RequireSlowSim skips the calling test (or subtest) unless the slow-sim
// opt-in is set. Call it first in the slow arm of an in-suite sim rung: with
// the opt-in unset, the arm compiles (caught by `just test`) and skips, so the
// default suite stays within its per-package timeout budget; opted in, it runs.
func RequireSlowSim(t *testing.T) {
	t.Helper()
	if os.Getenv(SlowSimTestsEnv) == "" {
		t.Skipf("slow sim rung skipped — run `just sim-shadow-slow-test` (sets %s) to exercise it", SlowSimTestsEnv)
	}
}

// FailOnErr fails the test if err is non-nil. It is the post-opt-in error
// policy: once RequireLive has let the test run, the live endpoint was
// explicitly requested, so ANY failure (including an unreachable endpoint)
// is a real failure — never a skip. This replaces the older
// skip-on-unreachable behavior, which masked a down endpoint as a pass.
func FailOnErr(t *testing.T, context string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v (live opt-in %s is set, so an unreachable or "+
			"misconfigured endpoint is a failure, not a skip)", context, err, LiveTestsEnv)
	}
}

// ModelLister returns the model ids a provider serves. It is the minimal
// slice of a model client this package needs to verify a configured model
// exists; the caller adapts its concrete client (e.g. *model.HTTPClient's
// ListModels) to this shape. Defining it here — rather than importing
// internal/model — keeps the dependency one-directional (see package doc).
type ModelLister func(ctx context.Context) (ids []string, err error)

// RequireModelPresent fails the test if the named model is not in the
// provider's served model list. It guards the stale-model-name failure mode
// (a 404 at turn 1 from a model ref the server no longer serves):
// catch it up front with a clear message instead of deep in the run. A
// ListModels error is itself a failure (the endpoint is opted-in but not
// answering).
func RequireModelPresent(t *testing.T, lister ModelLister, model string) {
	t.Helper()
	ids, err := lister(context.Background())
	FailOnErr(t, "list models", err)
	for _, id := range ids {
		if id == model {
			return
		}
	}
	t.Fatalf("configured model %q is not served by the endpoint (available: %v); "+
		"fix the model ref in the config", model, ids)
}
