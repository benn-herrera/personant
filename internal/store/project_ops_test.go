package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/memops"
)

// projectFixturePaths returns a PersonantPaths rooted at t.TempDir() with
// the projects/ subtree present.
func projectFixturePaths(t *testing.T) PersonantPaths {
	t.Helper()
	paths := PathsForHome(t.TempDir())
	if err := os.MkdirAll(paths.ProjectsDir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	return paths
}

func TestSaveLoadProjectMetaRoundTrip(t *testing.T) {
	paths := projectFixturePaths(t)

	in := memops.ProjectMeta{
		ID:               "prj_1",
		Name:             "alpha",
		CurrentRootPath:  "/tmp/alpha",
		Created:          "2026-05-01T00:00:00Z",
		LastActive:       "2026-05-08T00:00:00Z",
		ThreadCount:      0,
		ConventionsPaths: []string{"/tmp/alpha/CLAUDE.md"},
		IgnoreSymbols:    []string{},
	}
	if err := SaveProjectMeta(paths, in); err != nil {
		t.Fatalf("SaveProjectMeta: %v", err)
	}

	got, err := LoadProjectMeta(paths, "prj_1")
	if err != nil {
		t.Fatalf("LoadProjectMeta: %v", err)
	}
	if got.ID != in.ID || got.Name != in.Name {
		t.Errorf("identity mismatch: got %+v, want %+v", got, in)
	}
	if got.CurrentRootPath != in.CurrentRootPath {
		t.Errorf("path mismatch: got %q, want %q", got.CurrentRootPath, in.CurrentRootPath)
	}
}

func TestLoadProjectMetaMissing(t *testing.T) {
	paths := projectFixturePaths(t)
	_, err := LoadProjectMeta(paths, "prj_99")
	if !errors.Is(err, memops.ErrProjectNotFound) {
		t.Fatalf("expected memops.ErrProjectNotFound, got %v", err)
	}
}

func TestLoadProjectMetaDefaultSynthetic(t *testing.T) {
	paths := projectFixturePaths(t)
	meta, err := LoadProjectMeta(paths, DefaultProjectID)
	if err != nil {
		t.Fatalf("LoadProjectMeta default: %v", err)
	}
	if meta.ID != DefaultProjectID {
		t.Errorf("ID: got %q, want %q", meta.ID, DefaultProjectID)
	}
	if meta.Name != "default" {
		t.Errorf("Name: got %q, want %q", meta.Name, "default")
	}
	if meta.CurrentRootPath != "" {
		t.Errorf("expected empty CurrentRootPath, got %q", meta.CurrentRootPath)
	}
}

func TestSaveProjectMetaRejectsEmptyID(t *testing.T) {
	paths := projectFixturePaths(t)
	err := SaveProjectMeta(paths, memops.ProjectMeta{Name: "no-id"})
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestListProjectsSortedAndSkipsMissing(t *testing.T) {
	paths := projectFixturePaths(t)

	for _, m := range []memops.ProjectMeta{
		{ID: "prj_3", Name: "c"},
		{ID: "prj_1", Name: "a"},
		{ID: "prj_2", Name: "b"},
	} {
		if err := SaveProjectMeta(paths, m); err != nil {
			t.Fatalf("save %s: %v", m.ID, err)
		}
	}

	// Bare prj_default directory with no meta.json — must be skipped, per
	// §4.5.7's reservation.
	if err := os.MkdirAll(filepath.Join(paths.ProjectsDir, DefaultProjectID), 0o755); err != nil {
		t.Fatalf("mkdir prj_default: %v", err)
	}

	// Stray file (not a directory) and a bogus subdir — must be skipped.
	if err := os.WriteFile(filepath.Join(paths.ProjectsDir, "stray.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.ProjectsDir, "not-a-project"), 0o755); err != nil {
		t.Fatalf("mkdir bogus: %v", err)
	}

	got, err := ListProjects(paths)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("count: got %d, want 3 (records: %+v)", len(got), got)
	}
	want := []string{"prj_1", "prj_2", "prj_3"}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d: got %s, want %s", i, got[i].ID, id)
		}
	}
}

func TestListProjectsMissingDir(t *testing.T) {
	// No projects/ directory at all → empty result, no error.
	paths := PathsForHome(t.TempDir())
	got, err := ListProjects(paths)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestFindProjectByPath(t *testing.T) {
	paths := projectFixturePaths(t)

	if err := SaveProjectMeta(paths, memops.ProjectMeta{
		ID:              "prj_1",
		Name:            "alpha",
		CurrentRootPath: "/projects/alpha",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	cases := []struct {
		name   string
		query  string
		wantOK bool
		wantID string
	}{
		{name: "current", query: "/projects/alpha", wantOK: true, wantID: "prj_1"},
		{name: "trailing slash equivalent", query: "/projects/alpha/", wantOK: true, wantID: "prj_1"},
		{name: "miss", query: "/elsewhere", wantOK: false},
		{name: "empty", query: "", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := FindProjectByPath(paths, tc.query)
			if err != nil {
				t.Fatalf("FindProjectByPath: %v", err)
			}
			if ok != tc.wantOK {
				t.Errorf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && got.ID != tc.wantID {
				t.Errorf("id: got %s, want %s", got.ID, tc.wantID)
			}
		})
	}
}

func TestNextProjectID(t *testing.T) {
	cases := []struct {
		name  string
		metas []memops.ProjectMeta
		want  string
	}{
		{name: "empty", metas: nil, want: "prj_1"},
		{
			name: "ignores prj_default",
			metas: []memops.ProjectMeta{
				{ID: DefaultProjectID},
			},
			want: "prj_1",
		},
		{
			name: "with gaps",
			metas: []memops.ProjectMeta{
				{ID: "prj_1"}, {ID: "prj_5"}, {ID: "prj_3"},
			},
			want: "prj_6",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextProjectID(tc.metas); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
