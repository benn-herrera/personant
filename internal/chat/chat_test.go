package chat

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"personant/internal/autogit"
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
type = "inference"
api = "openai"
`), 0o644); err != nil {
		t.Fatalf("write providers: %v", err)
	}
	// A pool entry names no models, so the chat model comes from
	// config.toml (or --model). Without one, Run refuses to open.
	if err := os.WriteFile(paths.Config, []byte(`
[chat]
defaultModel = "local/test-model"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
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

// The §4.4 escapes run for real through the whole REPL, with no turn
// driven — the model client is never consulted. Deeper behavior lives in
// shellescape_test.go; this pins the dispatch path itself.
func TestRunShellEscapeDispatch(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	mock := model.NewScriptedMock(nil, nil) // no turns issued
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("$echo fire-and-forget\n#echo captured\n/quit\n")

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
	for _, want := range []string{"fire-and-forget", "captured"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing shell output %q in %q", want, stdout.String())
		}
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
type = "inference"
api = "openai"
`), 0o644); err != nil {
		t.Fatalf("write providers: %v", err)
	}
	// Drop scaffoldHome's [chat] pin: this test is about provider
	// selection with NO pin at all. The model then comes from --model.
	if err := os.WriteFile(paths.Config, nil, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("/quit\n")
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Model:           "test-model",
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
	if !strings.Contains(err.Error(), "no inference providers configured") {
		t.Errorf("error message: %q", err.Error())
	}
}

// ---------- crash-stability wiring (#94): Init → Reconcile → LoadSession ----------

// gitBaseline promotes a scaffolded home to a git-backed substrate with
// everything committed — the state a real home is in between sessions.
func gitBaseline(t *testing.T, paths store.PersonantPaths) {
	t.Helper()
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, autogit.Daily, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.Daily, "test baseline", 0, 0); err != nil && !errors.Is(err, git.ErrEmptyCommit) {
		t.Fatalf("Commit: %v", err)
	}
}

// TestRunRecoveryBannerAfterTornTurn: a home crashed mid-turn opens
// with the recovery banner — the unclean-shutdown line and the
// preserved-content announcement — and the session proceeds normally.
func TestRunRecoveryBannerAfterTornTurn(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	gitBaseline(t, paths)

	// Torn turn t2: prompt journaled (the non-empty journal is the
	// in-flight-turn signal), a tracked canonical file dirtied, killed
	// before the per-turn commit.
	if err := store.AppendJournal(paths, "t2", store.JournalPrompt, []byte("lost prompt")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	userMD := filepath.Join(paths.DirectivesDir, "user.md")
	if err := os.WriteFile(userMD, []byte("TORN MID-TURN\n"), 0o644); err != nil {
		t.Fatalf("dirty user.md: %v", err)
	}

	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           strings.NewReader("/quit\n"),
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "recovery: unclean shutdown detected (in-flight turn t2)") {
		t.Errorf("missing unclean-shutdown banner line:\n%s", out)
	}
	if !strings.Contains(out, "recovery: in-flight content for turn t2 preserved at ") ||
		!strings.Contains(out, "not replayed") {
		t.Errorf("missing preserved-content banner line:\n%s", out)
	}
	if !strings.Contains(out, "active: alpha (prj_1)") {
		t.Errorf("session did not proceed after recovery:\n%s", out)
	}
}

// TestRunQuietRecoveryPrintsNoBanner: a routine open (fresh home,
// derived refresh only) must not nag.
func TestRunQuietRecoveryPrintsNoBanner(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           strings.NewReader("/quit\n"),
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stdout.String(), "recovery:") {
		t.Errorf("quiet open printed a recovery banner:\n%s", stdout.String())
	}
}

// TestRunReconcileFailureRefusesToOpen: an unreconcilable substrate
// (here: a corrupt op marker) must refuse the session outright — no
// prompt, no LoadSession, error surfaced.
func TestRunReconcileFailureRefusesToOpen(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	if err := os.WriteFile(paths.OpMarker, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}

	mock := model.NewScriptedMock(nil, nil)
	var stdout, stderr bytes.Buffer
	err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           strings.NewReader("/quit\n"),
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	})
	if err == nil {
		t.Fatal("Run opened a session on an unreconcilable substrate")
	}
	if !strings.Contains(err.Error(), "refusing to open") {
		t.Errorf("error = %v, want refuse-to-open wording", err)
	}
	if strings.Contains(stdout.String(), "active:") {
		t.Errorf("session banner printed despite refusal:\n%s", stdout.String())
	}
}

// failCommitOps wraps the real adapter, failing every CommitTurn — the
// post-canonical marker-retained failure shape (R3 review F1/F4).
type failCommitOps struct {
	memops.MemoryOps
}

func (f failCommitOps) CommitTurn(ctx context.Context, turnID, reason string) error {
	return errors.New("injected commit failure")
}

// TestRunMarkerRetainedWedgeThenQuitRecovers is the end-to-end quit path
// of a marker-retained turn failure (R3 review F1 + F4):
//
//   - the failed turn's error carries the restart-to-recover advisory
//     (retry cannot work; the marker is retained);
//   - a prompt typed in the wedged window is refused before it is
//     journaled — the advisory says it is NOT captured;
//   - /quit: the session-close Checkpoint is REFUSED by the marker guard
//     (committing the torn prefix trailer-less would reconcile as cell 6
//     and make it permanent) and chat reports the state as preserved;
//   - reopen: Reconcile fires the cell-4 rollback on the preserved shape.
func TestRunMarkerRetainedWedgeThenQuitRecovers(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	gitBaseline(t, paths)

	headBefore, err := autogit.HeadHash(context.Background(), paths, autogit.Daily)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}

	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHi there."},
	}, nil)
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("hello\nwedged prompt\n/quit\n")
	if err := Run(Options{
		Ops:             failCommitOps{MemoryOps: newOps(paths)},
		ExplicitProject: "prj_1",
		Stdin:           in,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// F4: both the commit failure and the wedged-window journal refusal
	// carry the restart advisory.
	if got := strings.Count(stderr.String(), "restart personant to recover"); got != 2 {
		t.Errorf("restart advisory printed %d time(s), want 2; stderr:\n%s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "conflicting in-flight marker") {
		t.Errorf("wedged prompt was not refused on the retained scope; stderr:\n%s", stderr.String())
	}

	// F1: the session-close checkpoint was refused, reported as preserved,
	// and nothing was committed — HEAD still at the pre-session baseline.
	if !strings.Contains(stdout.String(), "unclean turn state preserved for recovery") {
		t.Errorf("missing preserved-for-recovery close message; stdout:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "warn: session-close checkpoint") {
		t.Errorf("marker refusal surfaced as a checkpoint fault; stderr:\n%s", stderr.String())
	}
	headAfter, err := autogit.HeadHash(context.Background(), paths, autogit.Daily)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if headAfter != headBefore {
		t.Fatalf("session-close committed a torn prefix: %s → %s", headBefore, headAfter)
	}
	if owner, inFlight, jerr := store.JournalOwner(paths); jerr != nil || !inFlight || owner == "" {
		t.Fatalf("turn scope not preserved across quit: owner=%q inFlight=%v err=%v", owner, inFlight, jerr)
	}

	// Reopen: the cell-4 rollback fires on the preserved torn-turn shape.
	rep, err := newOps(paths).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !rep.ResetPerformed {
		t.Errorf("Reconcile did not roll the torn turn back; cells=%v", rep.CellsHit)
	}
	if !strings.Contains(strings.Join(rep.CellsHit, ","), "cell-4-torn-turn") {
		t.Errorf("cells = %v, want cell-4-torn-turn", rep.CellsHit)
	}
	if _, inFlight, _ := store.JournalOwner(paths); inFlight {
		t.Error("turn scope survived recovery")
	}
}
