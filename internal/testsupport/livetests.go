// Package testsupport holds shared helpers for live-endpoint tests that
// span more than one package (currently internal/model and internal/turn).
// It is a NON-test-only package on purpose: the helpers are imported by
// test files in different packages, which a `_test.go`-local helper cannot
// serve, and a shared *_test.go file cannot cross a package boundary.
//
// The live-test convention these helpers enforce:
//
//   - Tests ALWAYS COMPILE (no //go:build tag). They are part of the
//     normal `go vet ./...` / `make test` compile, so a refactor that
//     breaks them is caught immediately instead of rotting silently.
//   - EXECUTION is gated at runtime on an explicit opt-in (LiveTestsEnv).
//     Not opted in → the test t.Skips: bare `make test` compiles and skips
//     it, hitting no endpoint and staying fast.
//   - Opted in but the endpoint is unreachable / misconfigured → FAIL,
//     not skip. You explicitly asked for the live path; nothing live is a
//     real failure, not a reason to pass quietly.
//
// This package imports only memops + stdlib. It deliberately does NOT
// import internal/model: the model package's own *_test.go files are in
// `package model` and import this package, so a model import here would be
// an import cycle. The model-presence check therefore takes a small
// locally-defined lister interface instead of a concrete model client.
package testsupport

import (
	"context"
	"os"
	"testing"

	"personant/internal/memops"
)

// LiveTestsEnv is the opt-in environment variable for live-endpoint tests.
// Set it to any non-empty value (the Makefile entry points set it to "1")
// to RUN the live tests; leave it unset to SKIP them. One constant so the
// helpers, the Makefile, and any future caller share one name (DRY).
const LiveTestsEnv = "PERSONANT_LIVE_TESTS"

// CorpusTestsEnv is the opt-in environment variable for the slow,
// artifact-dependent recall-corpus measurement tests. Set it to any
// non-empty value (the Makefile's recall-corpus-test target sets it) to RUN
// them; leave it unset to SKIP them. Distinct from LiveTestsEnv: these
// tests are gated on cost + derived-artifact presence, not a live endpoint.
const CorpusTestsEnv = "PERSONANT_CORPUS_TESTS"

// ReaperEmbeddingModel is the embedding model id served by the local,
// non-metered `reaper` provider.
const ReaperEmbeddingModel = "nomicai-embed"

// reaperDefaultURL is the dev-machine reaper endpoint, overridable via the
// PERSONANT_REAPER_URL environment variable.
const reaperDefaultURL = "http://reaper.local:4000/v1"

// ReaperProvider builds the `reaper` provider used by the live tests. The
// base URL is overridable via PERSONANT_REAPER_URL; the API key is the
// local non-metered placeholder `dummy` (no secret involved).
func ReaperProvider() memops.Provider {
	url := os.Getenv("PERSONANT_REAPER_URL")
	if url == "" {
		url = reaperDefaultURL
	}
	return memops.Provider{
		Name:    "reaper",
		BaseURL: url,
		APIKey:  "dummy",
	}
}

// RequireLive skips the calling test unless the live opt-in is set. Call it
// first in every live-endpoint test: with the opt-in unset, the test
// compiles (caught by `make test`) and skips without touching the network.
func RequireLive(t *testing.T) {
	t.Helper()
	if os.Getenv(LiveTestsEnv) == "" {
		t.Skipf("live test skipped — set %s=1 (or run `make integration-test`) to exercise the live endpoint", LiveTestsEnv)
	}
}

// RequireCorpus skips the calling test unless the corpus opt-in is set.
// Call it first in every recall-corpus measurement test: with the opt-in
// unset, the test compiles (caught by `make test`) and skips without doing
// the slow corpus work. When opted in but the required derived artifact is
// absent, the test still skips with a regeneration hint (the artifact
// loaders handle that) — the corpus artifacts are .gitignore'd, so absence
// is an expected "regenerate first" state, not a defect.
func RequireCorpus(t *testing.T) {
	t.Helper()
	if os.Getenv(CorpusTestsEnv) == "" {
		t.Skipf("corpus measurement skipped — run `make recall-corpus-test` (sets %s) to exercise it", CorpusTestsEnv)
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
// (Inc 3 hit a 404 at turn 1 from a model ref the server no longer served):
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
