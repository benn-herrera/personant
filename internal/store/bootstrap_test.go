package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// bootstrapPaths returns a PersonantPaths rooted at t.TempDir() with the
// projects/ subtree present.
func bootstrapPaths(t *testing.T) PersonantPaths {
	t.Helper()
	paths := PathsForHome(t.TempDir())
	if err := os.MkdirAll(paths.ProjectsDir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	return paths
}

// pathsEqual returns true if a and b refer to the same file after
// filepath.Clean and EvalSymlinks (macOS $TMPDIR is symlinked).
func pathsEqual(t *testing.T, a, b string) bool {
	t.Helper()
	ea, err := filepath.EvalSymlinks(filepath.Clean(a))
	if err != nil {
		ea = filepath.Clean(a)
	}
	eb, err := filepath.EvalSymlinks(filepath.Clean(b))
	if err != nil {
		eb = filepath.Clean(b)
	}
	return ea == eb
}

func TestResolveActiveProject_ExplicitByID(t *testing.T) {
	paths := bootstrapPaths(t)
	if err := SaveProjectMeta(paths, ProjectMeta{ID: "prj_1", Name: "alpha"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, BootstrapOptions{ExplicitProject: "prj_1"})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepExplicit {
		t.Errorf("step: got %v, want StepExplicit", res.Step)
	}
	if res.Resolved == nil || res.Resolved.ID != "prj_1" {
		t.Fatalf("Resolved: %+v", res.Resolved)
	}
	got, err := ReadLastActive(paths)
	if err != nil {
		t.Fatalf("ReadLastActive: %v", err)
	}
	if got != "prj_1" {
		t.Errorf("last-active: got %q, want %q", got, "prj_1")
	}
}

func TestResolveActiveProject_ExplicitByName(t *testing.T) {
	paths := bootstrapPaths(t)
	if err := SaveProjectMeta(paths, ProjectMeta{ID: "prj_7", Name: "gamma"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, BootstrapOptions{ExplicitProject: "gamma"})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepExplicit {
		t.Errorf("step: got %v, want StepExplicit", res.Step)
	}
	if res.Resolved == nil || res.Resolved.ID != "prj_7" {
		t.Fatalf("Resolved: %+v", res.Resolved)
	}
}

func TestResolveActiveProject_ExplicitUnknown(t *testing.T) {
	paths := bootstrapPaths(t)
	_, err := ResolveActiveProject(paths, BootstrapOptions{ExplicitProject: "ghost"})
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("expected ErrProjectNotFound, got %v", err)
	}
	// last-active must remain unwritten on a failed explicit resolution.
	if _, statErr := os.Stat(paths.LastActive); statErr == nil {
		t.Error("last-active should not have been written on failed explicit resolution")
	}
}

func TestResolveActiveProject_RemoteMatch(t *testing.T) {
	hasGit(t)
	paths := bootstrapPaths(t)

	repo := t.TempDir()
	gitInit(t, repo)
	gitRun(t, repo, "remote", "add", "origin", "git@github.com:foo/bar.git")

	// ProjectMeta with the canonical (https) form of the same remote, and a
	// stored CurrentRootPath that differs from the test repo to verify the
	// drift-update.
	staleRoot := filepath.Join(t.TempDir(), "stale-root")
	if err := os.MkdirAll(staleRoot, 0o755); err != nil {
		t.Fatalf("mkdir stale: %v", err)
	}
	if err := SaveProjectMeta(paths, ProjectMeta{
		ID:              "prj_1",
		Name:            "alpha",
		CurrentRootPath: staleRoot,
		RemoteURLs:      []string{"https://github.com/foo/bar"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: repo})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepRemoteMatch {
		t.Errorf("step: got %v, want StepRemoteMatch", res.Step)
	}
	if res.Resolved == nil || res.Resolved.ID != "prj_1" {
		t.Fatalf("Resolved: %+v", res.Resolved)
	}

	// Drift-update verification: stored meta should now point at repo,
	// and staleRoot should be in historical_root_paths.
	stored, err := LoadProjectMeta(paths, "prj_1")
	if err != nil {
		t.Fatalf("LoadProjectMeta: %v", err)
	}
	if !pathsEqual(t, stored.CurrentRootPath, repo) {
		t.Errorf("CurrentRootPath: got %q, want %q", stored.CurrentRootPath, repo)
	}
	if len(stored.HistoricalRootPaths) != 1 || !pathsEqual(t, stored.HistoricalRootPaths[0], staleRoot) {
		t.Errorf("HistoricalRootPaths: got %v, want [%q]", stored.HistoricalRootPaths, staleRoot)
	}

	// last-active must be written.
	last, err := ReadLastActive(paths)
	if err != nil {
		t.Fatalf("ReadLastActive: %v", err)
	}
	if last != "prj_1" {
		t.Errorf("last-active: got %q, want %q", last, "prj_1")
	}
}

func TestResolveActiveProject_RemoteMatch_Historical(t *testing.T) {
	hasGit(t)
	paths := bootstrapPaths(t)

	repo := t.TempDir()
	gitInit(t, repo)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/foo/bar.git")

	if err := SaveProjectMeta(paths, ProjectMeta{
		ID:                   "prj_1",
		Name:                 "alpha",
		CurrentRootPath:      repo,
		RemoteURLs:           []string{"https://github.com/foo/renamed"},
		HistoricalRemoteURLs: []string{"https://github.com/foo/bar"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: repo})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepRemoteMatch {
		t.Errorf("step: got %v, want StepRemoteMatch", res.Step)
	}
	if res.Resolved == nil || res.Resolved.ID != "prj_1" {
		t.Fatalf("Resolved: %+v", res.Resolved)
	}
}

func TestResolveActiveProject_PathMatch_Current(t *testing.T) {
	paths := bootstrapPaths(t)

	cwd := t.TempDir()
	if err := SaveProjectMeta(paths, ProjectMeta{
		ID:              "prj_1",
		Name:            "alpha",
		CurrentRootPath: cwd,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: cwd})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepPathMatch {
		t.Errorf("step: got %v, want StepPathMatch", res.Step)
	}
	if res.Resolved == nil || res.Resolved.ID != "prj_1" {
		t.Fatalf("Resolved: %+v", res.Resolved)
	}
}

func TestResolveActiveProject_PathMatch_Historical_DriftUpdate(t *testing.T) {
	paths := bootstrapPaths(t)

	oldCWD := t.TempDir() // simulates the historical path on disk
	newCWD := t.TempDir() // simulates "today" — user moved the project here
	if err := SaveProjectMeta(paths, ProjectMeta{
		ID:                  "prj_1",
		Name:                "alpha",
		CurrentRootPath:     oldCWD,
		HistoricalRootPaths: []string{newCWD},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: newCWD})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepPathMatch {
		t.Errorf("step: got %v, want StepPathMatch", res.Step)
	}

	stored, err := LoadProjectMeta(paths, "prj_1")
	if err != nil {
		t.Fatalf("LoadProjectMeta: %v", err)
	}
	if !pathsEqual(t, stored.CurrentRootPath, newCWD) {
		t.Errorf("CurrentRootPath: got %q, want %q", stored.CurrentRootPath, newCWD)
	}
	// oldCWD must now appear somewhere in HistoricalRootPaths (the older
	// historical entry, the previous current, or both).
	foundOld := false
	for _, p := range stored.HistoricalRootPaths {
		if pathsEqual(t, p, oldCWD) {
			foundOld = true
			break
		}
	}
	if !foundOld {
		t.Errorf("expected oldCWD %q to appear in HistoricalRootPaths %v", oldCWD, stored.HistoricalRootPaths)
	}
}

func TestResolveActiveProject_LastActiveConfirmation(t *testing.T) {
	paths := bootstrapPaths(t)

	if err := SaveProjectMeta(paths, ProjectMeta{ID: "prj_1", Name: "alpha"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := WriteLastActive(paths, "prj_1"); err != nil {
		t.Fatalf("WriteLastActive: %v", err)
	}

	// CWD that does not match any project.
	bareCWD := t.TempDir()
	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: bareCWD})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	// On machines whose $TMPDIR ancestors carry .git, FindGitRoot may walk
	// up and the test could match a project we did not configure with that
	// remote. We guard by ensuring there is no ProjectMeta with the
	// developer's project remote — but the cleanest assertion here is that
	// either NeedsConfirmation fired (the documented branch) or the
	// remote-match silently passed without any configured remote (which
	// shouldn't happen in this fixture).
	if res.Step != StepNeedsConfirmation {
		t.Fatalf("step: got %v, want StepNeedsConfirmation; resolved=%+v", res.Step, res.Resolved)
	}
	if res.Candidate == nil || res.Candidate.ID != "prj_1" {
		t.Errorf("Candidate: %+v", res.Candidate)
	}
}

func TestResolveActiveProject_LastActiveStaleFallsThrough(t *testing.T) {
	paths := bootstrapPaths(t)

	// Write a last-active pointing at a project whose meta.json does not
	// exist — drag-and-drop'd home, hand-edit, etc.
	if err := WriteLastActive(paths, "prj_99"); err != nil {
		t.Fatalf("WriteLastActive: %v", err)
	}

	bareCWD := t.TempDir()
	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: bareCWD})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepNeedsFallback {
		t.Errorf("step: got %v, want StepNeedsFallback", res.Step)
	}
}

func TestResolveActiveProject_FreshHomeNeedsFallback(t *testing.T) {
	paths := bootstrapPaths(t)
	bareCWD := t.TempDir()
	res, err := ResolveActiveProject(paths, BootstrapOptions{CWD: bareCWD})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepNeedsFallback {
		t.Errorf("step: got %v, want StepNeedsFallback", res.Step)
	}
	if res.Resolved != nil || res.Candidate != nil {
		t.Errorf("expected no resolved/candidate, got resolved=%+v candidate=%+v", res.Resolved, res.Candidate)
	}
}

func TestResolveActiveProject_ExplicitDoesNotDriftPath(t *testing.T) {
	paths := bootstrapPaths(t)
	staleRoot := "/some/stored/path"
	if err := SaveProjectMeta(paths, ProjectMeta{
		ID:              "prj_1",
		Name:            "alpha",
		CurrentRootPath: staleRoot,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	someCWD := t.TempDir()
	res, err := ResolveActiveProject(paths, BootstrapOptions{
		ExplicitProject: "prj_1",
		CWD:             someCWD, // should be ignored on the explicit path
	})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != StepExplicit {
		t.Fatalf("step: got %v, want StepExplicit", res.Step)
	}

	// stored meta must not have been rewritten — the explicit path
	// deliberately skips drift-update.
	stored, err := LoadProjectMeta(paths, "prj_1")
	if err != nil {
		t.Fatalf("LoadProjectMeta: %v", err)
	}
	if stored.CurrentRootPath != staleRoot {
		t.Errorf("CurrentRootPath was modified by explicit resolution: got %q, want %q", stored.CurrentRootPath, staleRoot)
	}
	if len(stored.HistoricalRootPaths) != 0 {
		t.Errorf("HistoricalRootPaths was modified by explicit resolution: %v", stored.HistoricalRootPaths)
	}
}

func TestResolveActiveProject_DefaultProjectIDExplicit(t *testing.T) {
	paths := bootstrapPaths(t)
	res, err := ResolveActiveProject(paths, BootstrapOptions{ExplicitProject: DefaultProjectID})
	if err != nil {
		t.Fatalf("ResolveActiveProject default: %v", err)
	}
	if res.Step != StepExplicit {
		t.Errorf("step: got %v, want StepExplicit", res.Step)
	}
	if res.Resolved == nil || res.Resolved.ID != DefaultProjectID {
		t.Fatalf("Resolved: %+v", res.Resolved)
	}
}
