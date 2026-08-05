package chat

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/store"
	"personant/internal/term"
	"personant/internal/turn"
)

// modelFixture drives cmdModel directly: the command's whole job is
// resolve → verify → swap, and driving a full REPL session to reach it
// would only add bootstrap noise around the three things under test.
type modelFixture struct {
	sel   *modelSelector
	tm    *term.Terminal
	out   *bytes.Buffer
	diag  *bytes.Buffer
	ops   memops.MemoryOps
	paths store.PersonantPaths
	state *turn.State

	// altClient is what newClient returns for a provider switch, so a
	// test can assert the session's client actually moved.
	altClient model.Client
}

// modelTestPool is the pool every /model test resolves against: two
// inference providers and one search entry (the kind that must never
// become a chat endpoint).
func modelTestPool() memops.Providers {
	return memops.Providers{
		"local": {Name: "local", BaseURL: "http://127.0.0.1:0/v1", Type: memops.ProviderTypeInference, API: memops.ProviderAPIOpenAI},
		"alt":   {Name: "alt", BaseURL: "http://127.0.0.1:0/v2", Type: memops.ProviderTypeInference, API: memops.ProviderAPIOpenAI},
		"exa":   {Name: "exa", BaseURL: "http://127.0.0.1:0/search", Type: memops.ProviderTypeSearch, API: memops.ProviderAPIExa},
	}
}

// newModelFixture builds a session sitting on local/test-model, with
// `current` serving the current provider's /models and `alt` serving the
// switched-to provider's.
func newModelFixture(t *testing.T, current, alt model.Client) *modelFixture {
	t.Helper()
	paths := scaffoldHome(t)
	ops := newOps(paths)
	pool := modelTestPool()
	state := turn.NewState(ops, memops.ProjectMeta{ID: "prj_1", Name: "alpha"}, pool["local"], current)
	state.Model = "test-model"
	t.Cleanup(func() { _ = state.Recaller.Close() })

	var out, diag bytes.Buffer
	f := &modelFixture{
		out: &out, diag: &diag, ops: ops, paths: paths, state: state, altClient: alt,
		tm: openTestTerm(t, strings.NewReader(""), &out, &diag),
	}
	f.sel = &modelSelector{
		pool:      pool,
		inference: pool.OfKind(memops.ProviderTypeInference),
		provider:  "local",
		state:     state,
		newClient: func(memops.Provider) model.Client { return alt },
	}
	return f
}

func (f *modelFixture) run(t *testing.T, arg string) error {
	t.Helper()
	return cmdModel(context.Background(), f.tm, f.ops, f.sel, arg)
}

// offering returns a mock whose /models lists exactly the given ids.
func offering(ids ...string) *model.MockClient {
	infos := make([]model.ModelInfo, len(ids))
	for i, id := range ids {
		infos[i] = model.ModelInfo{ID: id}
	}
	return model.NewScriptedMock(nil, infos)
}

// unreachableClient stands in for a provider whose /models cannot be
// reached. MockClient's ListModels cannot fail, and "the endpoint is
// down" is precisely the path that must still switch.
type unreachableClient struct{ *model.MockClient }

func (unreachableClient) ListModels(context.Context) ([]model.ModelInfo, error) {
	return nil, errors.New("dial tcp 127.0.0.1:0: connect: connection refused")
}

// TestModelBareReports: the bare form prints the active provider/model
// plus the usage hint, and mutates nothing (the /thinking convention).
func TestModelBareReports(t *testing.T) {
	f := newModelFixture(t, offering("test-model", "other-model"), offering())

	if err := f.run(t, ""); err != nil {
		t.Fatalf("cmdModel: %v", err)
	}
	if got, want := f.out.String(), "model: local/test-model\n"+modelUsage+"\n"; got != want {
		t.Errorf("bare /model output =\n%q\nwant\n%q", got, want)
	}
	if f.state.Model != "test-model" || f.sel.provider != "local" {
		t.Errorf("bare /model mutated the session: %s", f.sel.current())
	}
	if logs := readAllLogs(t, f.paths); strings.Contains(logs, "model.switched") {
		t.Errorf("bare /model logged a switch:\n%s", logs)
	}
}

// TestModelSwitchSameProvider: a model verified against the provider's
// /models is adopted, the client is kept (same endpoint), the §3.5
// curator follows the new model, and the confirmation names the
// session-only contract.
func TestModelSwitchSameProvider(t *testing.T) {
	current := offering("test-model", "other-model")
	f := newModelFixture(t, current, offering())
	beforeCurator := f.state.Curator

	if err := f.run(t, "other-model"); err != nil {
		t.Fatalf("cmdModel: %v", err)
	}
	want := "model: local/test-model → local/other-model (session only — config.toml defaultModel unchanged)\n"
	if got := f.out.String(); got != want {
		t.Errorf("confirmation =\n%q\nwant\n%q", got, want)
	}
	if f.state.Model != "other-model" {
		t.Errorf("state.Model = %q, want other-model", f.state.Model)
	}
	if f.state.Client != model.Client(current) {
		t.Error("same-provider switch replaced the client; the endpoint did not change")
	}
	if f.state.Curator == beforeCurator {
		t.Error("curator still pinned to the old model — a closure draft would use it")
	}
	if logs := readAllLogs(t, f.paths); !strings.Contains(logs, "model.switched from=local/test-model to=local/other-model") {
		t.Errorf("missing model.switched event:\n%s", logs)
	}
}

// TestModelSwitchRefusedKeepsSession: a model absent from a non-empty
// /models list REFUSES the switch — naming what the provider offers —
// and leaves model, provider and client exactly as they were.
func TestModelSwitchRefusedKeepsSession(t *testing.T) {
	current := offering("test-model", "other-model")
	f := newModelFixture(t, current, offering("whatever"))

	err := f.run(t, "alt/nonesuch")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{`provider "alt" does not offer model "nonesuch"`, "it offers: whatever", "keeping local/test-model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if f.state.Model != "test-model" || f.sel.provider != "local" {
		t.Errorf("refused switch moved the session to %s", f.sel.current())
	}
	if f.state.Client != model.Client(current) {
		t.Error("refused switch swapped the client")
	}
	if f.state.Provider.BaseURL != f.sel.pool["local"].BaseURL {
		t.Errorf("refused switch moved state.Provider to %+v", f.state.Provider)
	}
	if f.out.Len() != 0 {
		t.Errorf("refused switch printed a confirmation: %q", f.out)
	}
	if logs := readAllLogs(t, f.paths); strings.Contains(logs, "model.switched") {
		t.Errorf("refused switch logged an event:\n%s", logs)
	}
}

// TestModelSwitchProvider: the provider/model form moves the client to
// the new provider's, along with provider and model.
func TestModelSwitchProvider(t *testing.T) {
	alt := offering("alt-model")
	f := newModelFixture(t, offering("test-model"), alt)

	if err := f.run(t, "alt/alt-model"); err != nil {
		t.Fatalf("cmdModel: %v", err)
	}
	if f.state.Client != model.Client(alt) {
		t.Error("provider switch did not install the new provider's client")
	}
	if f.sel.provider != "alt" || f.state.Model != "alt-model" {
		t.Errorf("session = %s, want alt/alt-model", f.sel.current())
	}
	if f.state.Provider.BaseURL != f.sel.pool["alt"].BaseURL {
		t.Errorf("state.Provider = %+v, want the alt entry", f.state.Provider)
	}
	if logs := readAllLogs(t, f.paths); !strings.Contains(logs, "model.switched from=local/test-model to=alt/alt-model") {
		t.Errorf("missing model.switched event:\n%s", logs)
	}
}

// TestModelRefAmbiguity pins the ambiguity rule: the first path segment
// is a provider ONLY when the pool declares it. Everything else is a
// model id — including a HuggingFace-style org/name — on the current
// provider.
func TestModelRefAmbiguity(t *testing.T) {
	f := newModelFixture(t, offering(), offering())
	tests := []struct {
		arg          string
		wantProvider string
		wantModel    string
	}{
		{"gemma-4-26B-A4B", "local", "gemma-4-26B-A4B"},
		{"alt/alt-model", "alt", "alt-model"},
		{"org/name", "local", "org/name"},
		{"alt/google/gemma-4-31b-it", "alt", "google/gemma-4-31b-it"},
		{"google/gemma/4", "local", "google/gemma/4"},
		{"/leading-slash", "local", "/leading-slash"},
		{"alt/", "local", "alt/"},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			p, m := f.sel.resolveRef(tt.arg)
			if p != tt.wantProvider || m != tt.wantModel {
				t.Errorf("resolveRef(%q) = (%q, %q), want (%q, %q)", tt.arg, p, m, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

// TestModelSwitchUnverifiedWhenUnreachable: an unreachable /models warns
// exactly as session open does and switches anyway — a momentarily-down
// provider must not be able to pin the session to its old model.
func TestModelSwitchUnverifiedWhenUnreachable(t *testing.T) {
	f := newModelFixture(t, offering("test-model"), unreachableClient{offering()})

	if err := f.run(t, "alt/anything-at-all"); err != nil {
		t.Fatalf("cmdModel: %v", err)
	}
	if !strings.Contains(f.diag.String(), `warn: could not verify model against provider "alt" /models`) {
		t.Errorf("missing the unverified warning, got diag: %q", f.diag)
	}
	if f.sel.current() != "alt/anything-at-all" {
		t.Errorf("session = %s, want alt/anything-at-all", f.sel.current())
	}
}

// TestModelSwitchNonInferenceProviderRefused: a search entry is not a
// chat endpoint, and saying "unknown provider" would send the user
// hunting a typo that isn't there (the bootstrap convention).
func TestModelSwitchNonInferenceProviderRefused(t *testing.T) {
	f := newModelFixture(t, offering("test-model"), offering("anything"))

	err := f.run(t, "exa/anything")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), `provider "exa" is a "search" provider, not an inference endpoint`) {
		t.Errorf("error = %q, want the kind-mismatch message", err)
	}
	if !strings.Contains(err.Error(), "inference providers: alt, local") {
		t.Errorf("error = %q, want the inference provider list", err)
	}
	if f.sel.current() != "local/test-model" {
		t.Errorf("refused switch moved the session to %s", f.sel.current())
	}
}

// TestSlashModelInSession drives /model through the REPL: the command is
// reachable from dispatch, the switch survives to the next command, and
// /help advertises it out of the stubbed section.
func TestSlashModelInSession(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}, {ID: "second-model"}}),
		"prj_1", "/model second-model\n/model\n/quit\n")

	if !strings.Contains(out, "model: local/test-model → local/second-model (session only") {
		t.Errorf("missing switch confirmation:\n%s", out)
	}
	if !strings.Contains(out, "model: local/second-model\n") {
		t.Errorf("bare /model did not report the switched model:\n%s", out)
	}
	if strings.Contains(helpText(), "/model <id>                  switch active model") {
		t.Error("/help still lists /model as stubbed")
	}
	if !strings.Contains(helpText(), "/model [id|provider/id]") {
		t.Error("/help does not advertise /model")
	}
}
