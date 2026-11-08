package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
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

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func TestResolveActiveProject_PathMatch(t *testing.T) {
	paths := bootstrapPaths(t)
	dir := t.TempDir()
	if err := SaveProjectMeta(paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: dir}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := ResolveActiveProject(paths, memops.BootstrapHints{Dir: dir + "/"})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != memops.StepPathMatch || res.Project.ID != "prj_1" {
		t.Fatalf("got step=%v project=%s, want path-match prj_1", res.Step, res.Project.ID)
	}
	metas, err := ListProjects(paths)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(metas) != 1 {
		t.Errorf("path match created a project: %d projects", len(metas))
	}
}

// A directory not seen before is a new project, even when its base name
// matches a project rooted elsewhere (a moved or copied directory).
func TestResolveActiveProject_Create(t *testing.T) {
	paths := bootstrapPaths(t)
	parent := t.TempDir()
	old := mkdir(t, filepath.Join(parent, "old", "My App_1"))
	if err := SaveProjectMeta(paths, memops.ProjectMeta{ID: "prj_1", Name: "My App_1", CurrentRootPath: old}); err != nil {
		t.Fatalf("save: %v", err)
	}
	dir := mkdir(t, filepath.Join(parent, "new", "My App_1"))

	res, err := ResolveActiveProject(paths, memops.BootstrapHints{Dir: dir})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != memops.StepCreated {
		t.Fatalf("step: got %v, want created", res.Step)
	}
	if res.Project.ID != "prj_2" || res.Project.Name != "My App_1" || res.Project.CurrentRootPath != dir {
		t.Fatalf("project: %+v", res.Project)
	}
	stored, err := LoadProjectMeta(paths, "prj_2")
	if err != nil {
		t.Fatalf("LoadProjectMeta: %v", err)
	}
	if stored.CurrentRootPath != dir || stored.Created == "" || stored.LastActive == "" {
		t.Errorf("stored meta: %+v", stored)
	}

	again, err := ResolveActiveProject(paths, memops.BootstrapHints{Dir: dir})
	if err != nil {
		t.Fatalf("second ResolveActiveProject: %v", err)
	}
	if again.Step != memops.StepPathMatch || again.Project.ID != "prj_2" {
		t.Errorf("second resolve: step=%v project=%s, want path-match prj_2", again.Step, again.Project.ID)
	}
}

// The resolver reads nothing inside the directory: a git remote there
// does not identify the project.
func TestResolveActiveProject_IgnoresGitRemote(t *testing.T) {
	paths := bootstrapPaths(t)
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, ".git"))
	gitConfig := "[remote \"origin\"]\n\turl = git@github.com:foo/bar.git\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(gitConfig), 0o644); err != nil {
		t.Fatalf("write .git/config: %v", err)
	}

	res, err := ResolveActiveProject(paths, memops.BootstrapHints{Dir: dir})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != memops.StepCreated || res.Project.CurrentRootPath != dir {
		t.Fatalf("got step=%v project=%+v, want a new project rooted at %s", res.Step, res.Project, dir)
	}
	raw, err := os.ReadFile(filepath.Join(paths.ProjectsDir, res.Project.ID, ProjectMetaFileName))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	if strings.Contains(string(raw), "github.com") {
		t.Errorf("meta.json carries the directory's remote: %s", raw)
	}
}

func TestResolveActiveProject_NoDirIsDefault(t *testing.T) {
	paths := bootstrapPaths(t)
	res, err := ResolveActiveProject(paths, memops.BootstrapHints{})
	if err != nil {
		t.Fatalf("ResolveActiveProject: %v", err)
	}
	if res.Step != memops.StepDefault || res.Project.ID != DefaultProjectID {
		t.Fatalf("got step=%v project=%s, want default %s", res.Step, res.Project.ID, DefaultProjectID)
	}
}

func TestResolveActiveProject_RejectsRelativeDir(t *testing.T) {
	paths := bootstrapPaths(t)
	if _, err := ResolveActiveProject(paths, memops.BootstrapHints{Dir: "relative/dir"}); err == nil {
		t.Fatal("relative directory resolved; want an error")
	}
	metas, err := ListProjects(paths)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("rejected directory created a project: %+v", metas)
	}
}
