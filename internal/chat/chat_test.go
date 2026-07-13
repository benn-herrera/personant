package chat

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// newOps constructs the substrate adapter for chat tests rooted at the
// scaffolded home. Equivalent to what cmd/chat.go does for the CLI.
func newOps(paths store.PersonantPaths) memops.MemoryOps {
	return fileadapter.NewFileAdapter(paths)
}

// scaffoldHome stands up enough of the personant home for the chat
// REPL to bootstrap: directory tree, empty spine, providers.toml with
// a "local" entry pointing at an unused base URL (chat tests use a
// MockClient injected via Options.Client, so the base URL is never
// dialed).
func scaffoldHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	for _, dir := range []string{paths.ThreadsDir, paths.ProjectsDir, paths.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := store.WriteSpine(paths.Spine, nil); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	if err := os.WriteFile(paths.Providers, []byte(`
[local]
baseUrl = "http://127.0.0.1:0/v1"
defaultModel = "test-model"
`), 0o644); err != nil {
		t.Fatalf("write providers: %v", err)
	}
	return paths
}

func writeMeta(t *testing.T, paths store.PersonantPaths, m memops.ProjectMeta) {
	t.Helper()
	if err := store.SaveProjectMeta(paths, m); err != nil {
		t.Fatalf("save meta %s: %v", m.ID, err)
	}
}

func TestRunExplicitProjectThenQuit(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	mock := model.NewScriptedMock(nil, nil) // no turns issued
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("/quit\n")

	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "personant —") {
		t.Errorf("missing banner: %q", out)
	}
	if !strings.Contains(out, "active: alpha (prj_1)") {
		t.Errorf("missing active line: %q", out)
	}
}

func TestRunFallbackChoosesDefault(t *testing.T) {
	paths := scaffoldHome(t)
	// No projects on disk → bootstrap hits StepNeedsFallback.
	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("n\n/quit\n")

	if err := Run(Options{
		Ops:    newOps(paths),
		Stdin:  in,
		Stdout: &stdout,
		Stderr: &stderr,
		Client: mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "No active project resolved") {
		t.Errorf("missing fallback prompt: %q", out)
	}
	if !strings.Contains(out, "(prj_default)") {
		t.Errorf("default not active: %q", out)
	}
	last, err := store.ReadLastActive(paths)
	if err != nil {
		t.Fatalf("read last-active: %v", err)
	}
	if last != store.DefaultProjectID {
		t.Errorf("last-active: got %q want %q", last, store.DefaultProjectID)
	}
}

func TestRunConfirmationYResumes(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{
		ID: "prj_7", Name: "previous-work",
		CurrentRootPath: filepath.Join(paths.Home, "..", "elsewhere"),
		LastActive:      "2026-05-01T00:00:00Z",
	})
	if err := store.WriteLastActive(paths, "prj_7"); err != nil {
		t.Fatalf("write last-active: %v", err)
	}

	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("y\n/quit\n")

	if err := Run(Options{
		Ops:    newOps(paths),
		Stdin:  in,
		Stdout: &stdout,
		Stderr: &stderr,
		Client: mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "Resume work on 'previous-work'") {
		t.Errorf("missing confirmation prompt: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "active: previous-work (prj_7)") {
		t.Errorf("not resumed: %q", stdout.String())
	}
}

func TestRunUnknownSlashContinues(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("/bogus\n/quit\n")
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "unknown command: /bogus") {
		t.Errorf("missing unknown-command message: %q", stderr.String())
	}
}

func TestRunShellEscapeStubbed(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("$ ls\n# pwd\n/quit\n")

	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := strings.Count(stderr.String(), "shell escape ($/#) is not yet implemented"); c != 2 {
		t.Errorf("expected 2 shell-escape stub messages; got %d in %q", c, stderr.String())
	}
}

func TestRunEmptyInputBenign(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	// This test asserts a clean (empty) stderr, so the startup /models
	// probe must succeed: report the provider's default model so the
	// chat-model resolution finds it and emits no warning.
	mock := model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}})
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("\n   \n/quit\n")

	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No turn ran; no errors should have surfaced.
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunOneTurnPrintsBody(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHi there."},
	}, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("hello\n/quit\n")

	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "Hi there.") {
		t.Errorf("response body not printed: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "*topic:") {
		t.Errorf("topic tag leaked into stdout: %q", stdout.String())
	}
	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 spine record; got %d", len(records))
	}
}

func TestRunUnknownExplicitProviderIsFatal(t *testing.T) {
	paths := scaffoldHome(t) // pool: "local"
	var stdout, stderr bytes.Buffer
	// The error fires at provider resolution, before any stdin read.
	in := strings.NewReader("")
	err := Run(Options{
		Ops:          newOps(paths),
		ProviderName: "nonesuch",
		Stdin:        in,
		Stdout:       &stdout,
		Stderr:       &stderr,
		Client:       model.NewScriptedMock(nil, nil),
	})
	if err == nil {
		t.Fatalf("expected a hard error for an unknown explicit --provider")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"nonesuch"`) {
		t.Errorf("error must name the unknown provider; got %q", msg)
	}
	if !strings.Contains(msg, "local") {
		t.Errorf("error must list the available providers; got %q", msg)
	}
	// Case (c): the unknown-provider error is distinct from the empty-pool
	// "no providers configured" message.
	if strings.Contains(msg, "no provider") {
		t.Errorf("unknown-provider error must not read as an empty pool; got %q", msg)
	}
}

func TestRunAbsentProviderFallsBackToDefault(t *testing.T) {
	paths := scaffoldHome(t)
	// Pool WITHOUT the conventional "local" default; no --provider given and
	// no config.toml [chat] pin. The documented default-selection (alphabetical
	// fallback) must still resolve — an absent default is not a hard error.
	if err := os.WriteFile(paths.Providers, []byte(`
[zephyr]
baseUrl = "http://127.0.0.1:0/v1"
defaultModel = "test-model"
`), 0o644); err != nil {
		t.Fatalf("write providers: %v", err)
	}
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("/quit\n")
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run should fall back to the sole provider, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "active: alpha (prj_1)") {
		t.Errorf("session did not start on the default path: %q", stdout.String())
	}
}

func TestRunNoProvidersIsFatal(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	for _, dir := range []string{paths.ThreadsDir, paths.ProjectsDir, paths.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := store.WriteSpine(paths.Spine, nil); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	// providers.toml absent → LoadProviders returns empty → Run errors.
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("")
	err := Run(Options{
		Ops:    newOps(paths),
		Stdin:  in,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err == nil {
		t.Fatalf("expected error for missing providers")
	}
	if !strings.Contains(err.Error(), "no providers configured") {
		t.Errorf("error message: %q", err.Error())
	}
}
