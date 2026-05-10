package workset

import (
	"os"
	"strings"
	"testing"

	"personant/internal/store"
)

func newHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	for _, dir := range []string{paths.ThreadsDir, paths.ProjectsDir, paths.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	// Tests need an empty spine on disk; ReadJSONL errors on a missing file.
	if err := store.WriteSpine(paths.Spine, nil); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	return paths
}

func TestComposeEmptySpine(t *testing.T) {
	paths := newHome(t)
	meta := store.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	params, err := Compose(State{Paths: paths, ActiveProject: meta})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if params.LayerA1 != "" {
		t.Errorf("LayerA1 should be empty; got %q", params.LayerA1)
	}
	if params.LayerE != "" || params.LayerA2 != "" || params.LayerB != "" || params.LayerC != "" {
		t.Errorf("non-A1 layers should be empty in v0.1 MVP; got %+v", params)
	}
}

func TestComposeRendersDisplayLines(t *testing.T) {
	paths := newHome(t)
	meta := store.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	otherMeta := store.ProjectMeta{ID: "prj_2", Name: "beta"}
	if err := store.SaveProjectMeta(paths, otherMeta); err != nil {
		t.Fatalf("save other meta: %v", err)
	}

	recs := []store.SpineRecord{
		{
			ID: "thr_2", Project: "prj_1",
			Anchors: []string{"trefoil", "unknot", "body-topology", "electron-shape"},
			Summary: "topology conflict; awaiting resolution",
			State:   store.ThreadWIP,
		},
		{
			ID: "thr_1", Project: "prj_1",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "first thread",
			State:   store.ThreadActive,
		},
		{
			// In a different project; must be filtered out.
			ID: "thr_99", Project: "prj_2",
			Anchors: []string{"x", "y", "z", "w"},
			Summary: "other project",
			State:   store.ThreadActive,
		},
	}
	for _, r := range recs {
		if err := store.AppendSpineRecord(paths, r); err != nil {
			t.Fatalf("append %s: %v", r.ID, err)
		}
	}

	params, err := Compose(State{Paths: paths, ActiveProject: meta})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	lines := strings.Split(params.LayerA1, "\n")
	if got, want := len(lines), 2; got != want {
		t.Fatalf("expected %d lines, got %d: %q", want, got, params.LayerA1)
	}
	// Spine is sorted by ID ascending; thr_1 comes before thr_2.
	if !strings.HasPrefix(lines[0], "thr_1 ") {
		t.Errorf("first line: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "thr_2 ") {
		t.Errorf("second line: %q", lines[1])
	}
	if !strings.Contains(lines[0], "[ACTIVE]") {
		t.Errorf("active state marker: %q", lines[0])
	}
	if !strings.Contains(lines[1], "[WIP]") {
		t.Errorf("wip state marker: %q", lines[1])
	}
	if strings.Contains(params.LayerA1, "thr_99") {
		t.Errorf("other-project record leaked: %q", params.LayerA1)
	}
}

func TestComposeRequiresProjectID(t *testing.T) {
	paths := newHome(t)
	if _, err := Compose(State{Paths: paths, ActiveProject: store.ProjectMeta{}}); err == nil {
		t.Fatalf("expected error for empty ActiveProject.ID")
	}
}

func TestRenderSpineDisplay(t *testing.T) {
	rec := store.SpineRecord{
		ID:      "thr_88",
		Anchors: []string{"trefoil", "unknot", "body-topology", "electron-shape"},
		Summary: "electron body-topology conflict",
		State:   store.ThreadWIP,
	}
	got := RenderSpineDisplay(rec)
	want := "thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict [WIP]"
	if got != want {
		t.Errorf("display:\n got: %q\nwant: %q", got, want)
	}

	rec.State = ""
	got = RenderSpineDisplay(rec)
	want = "thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict"
	if got != want {
		t.Errorf("no-state display:\n got: %q\nwant: %q", got, want)
	}
}
