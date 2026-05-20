package workset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

func newHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	for _, dir := range []string{paths.ThreadsDir, paths.ProjectsDir, paths.LogsDir, paths.DirectivesDir} {
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

func defaultCompose(state State) State {
	if state.Budget.Total == 0 {
		state.Budget = memops.DefaultBudget()
	}
	return state
}

func TestComposeEmptySpine(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	params, err := Compose(defaultCompose(State{Paths: paths, ActiveProject: meta}), ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if params.LayerA1 != "" {
		t.Errorf("LayerA1 should be empty; got %q", params.LayerA1)
	}
	if params.LayerA2 != "" || params.LayerB != "" || params.LayerC != "" {
		t.Errorf("non-A1 layers should be empty without seeded inputs; got %+v", params)
	}
	// Layer E may or may not be empty depending on whether seed
	// directives are present — for this test the home has none.
	if params.LayerE != "" {
		t.Errorf("LayerE should be empty without seeded directives; got %q", params.LayerE)
	}
}

func TestComposeRendersDisplayLines(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	otherMeta := memops.ProjectMeta{ID: "prj_2", Name: "beta"}
	if err := store.SaveProjectMeta(paths, otherMeta); err != nil {
		t.Fatalf("save other meta: %v", err)
	}

	recs := []memops.SpineRecord{
		{
			ID: "thr_2", Project: "prj_1",
			Anchors: []string{"trefoil", "unknot", "body-topology", "electron-shape"},
			Summary: "topology conflict; awaiting resolution",
			State:   memops.ThreadWIP,
		},
		{
			ID: "thr_1", Project: "prj_1",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "first thread",
			State:   memops.ThreadActive,
		},
		{
			// In a different project; must be filtered out.
			ID: "thr_99", Project: "prj_2",
			Anchors: []string{"x", "y", "z", "w"},
			Summary: "other project",
			State:   memops.ThreadActive,
		},
	}
	for _, r := range recs {
		if err := store.AppendSpineRecord(paths, r); err != nil {
			t.Fatalf("append %s: %v", r.ID, err)
		}
	}

	params, err := Compose(defaultCompose(State{Paths: paths, ActiveProject: meta}), ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	lines := strings.Split(params.LayerA1, "\n")
	if got, want := len(lines), 2; got != want {
		t.Fatalf("expected %d lines, got %d: %q", want, got, params.LayerA1)
	}
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
	if _, err := Compose(State{Paths: paths, ActiveProject: memops.ProjectMeta{}}, ComposeOptions{}); err == nil {
		t.Fatalf("expected error for empty ActiveProject.ID")
	}
}

func TestRenderSpineDisplay(t *testing.T) {
	rec := memops.SpineRecord{
		ID:      "thr_88",
		Anchors: []string{"trefoil", "unknot", "body-topology", "electron-shape"},
		Summary: "electron body-topology conflict",
		State:   memops.ThreadWIP,
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

// TestStripFrontmatter exercises the YAML-frontmatter trimming used by
// Layer E directive loading. Behaves like splitFrontmatter (in
// store/thread_io.go) but tolerant: unmatched/missing frontmatter
// returns the input unchanged.
func TestStripFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no frontmatter",
			in:   "# heading\nbody\n",
			want: "# heading\nbody\n",
		},
		{
			name: "frontmatter stripped",
			in:   "---\nfoo: bar\n---\nbody\n",
			want: "body\n",
		},
		{
			name: "frontmatter with blank line after close",
			in:   "---\nfoo: bar\n---\n\nbody\n",
			want: "body\n",
		},
		{
			name: "leading delimiter without close",
			in:   "---\nfoo: bar\nbody",
			want: "---\nfoo: bar\nbody",
		},
		{
			name: "embedded triple-dash in body left alone",
			in:   "---\nfoo: bar\n---\nbody\n```\n---\n```\n",
			want: "body\n```\n---\n```\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripFrontmatter(tc.in)
			if got != tc.want {
				t.Errorf("stripFrontmatter:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestLayerEReadsAllSources seeds defaults.md, user.md, project
// directive, and a ConventionsPaths file; verifies all four contribute.
func TestLayerEReadsAllSources(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{
		ID:               "prj_1",
		Name:             "alpha",
		ConventionsPaths: nil,
	}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	// Seed directives.
	defaults := "---\nscope: defaults\n---\nDEFAULTS_BODY\n"
	user := "USER_BODY_NO_FRONTMATTER\n"
	projectDir := filepath.Join(paths.DirectivesDir, "prj_1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}
	projectMD := "---\nscope: project\n---\nPROJECT_BODY\n"
	if err := os.WriteFile(filepath.Join(paths.DirectivesDir, "defaults.md"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("write defaults.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.DirectivesDir, "user.md"), []byte(user), 0o644); err != nil {
		t.Fatalf("write user.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "project.md"), []byte(projectMD), 0o644); err != nil {
		t.Fatalf("write project.md: %v", err)
	}

	// Seed a conventions file outside the home (simulates a workspace
	// AGENTS.md being pulled into Layer E).
	conventionsDir := t.TempDir()
	convPath := filepath.Join(conventionsDir, "AGENTS.md")
	if err := os.WriteFile(convPath, []byte("CONVENTIONS_BODY\n"), 0o644); err != nil {
		t.Fatalf("write conventions: %v", err)
	}
	meta.ConventionsPaths = []string{convPath}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta with conventions: %v", err)
	}

	params, err := Compose(defaultCompose(State{Paths: paths, ActiveProject: meta}), ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, want := range []string{"DEFAULTS_BODY", "USER_BODY_NO_FRONTMATTER", "PROJECT_BODY", "CONVENTIONS_BODY"} {
		if !strings.Contains(params.LayerE, want) {
			t.Errorf("LayerE missing %q\ngot: %s", want, params.LayerE)
		}
	}
	for _, header := range []string{"=== defaults ===", "=== user ===", "=== project: alpha ===", "=== conventions: " + convPath + " ==="} {
		if !strings.Contains(params.LayerE, header) {
			t.Errorf("LayerE missing header %q\ngot: %s", header, params.LayerE)
		}
	}
}

func TestLayerEMissingConventionsFileLogged(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{
		ID:               "prj_1",
		Name:             "alpha",
		ConventionsPaths: []string{"/nonexistent/path/CONVENTIONS.md"},
	}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	var logs []string
	logf := func(format string, args ...any) {
		logs = append(logs, format)
	}
	if _, err := Compose(defaultCompose(State{Paths: paths, ActiveProject: meta}), ComposeOptions{Logger: logf}); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	found := false
	for _, line := range logs {
		if strings.Contains(line, "conventions") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected conventions warning in logs; got %v", logs)
	}
}

// TestLayerA2RendersOtherProjects seeds two other projects with
// digests; LayerA2 must list them sorted by LastActive desc.
func TestLayerA2RendersOtherProjects(t *testing.T) {
	paths := newHome(t)
	active := memops.ProjectMeta{ID: "prj_1", Name: "active"}
	if err := store.SaveProjectMeta(paths, active); err != nil {
		t.Fatalf("save active: %v", err)
	}
	older := memops.ProjectMeta{ID: "prj_2", Name: "older", LastActive: "2026-04-01T00:00:00Z"}
	if err := store.SaveProjectMeta(paths, older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	newer := memops.ProjectMeta{ID: "prj_3", Name: "newer", LastActive: "2026-05-01T00:00:00Z"}
	if err := store.SaveProjectMeta(paths, newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	writeDigest(t, paths, memops.ProjectDigest{
		Project: "prj_2", DisplayName: "older",
		ThreadCount: 2, RecentAnchors: []string{"a", "b", "c", "d", "e", "f"},
		OneLineSummary: "older summary",
	})
	writeDigest(t, paths, memops.ProjectDigest{
		Project: "prj_3", DisplayName: "newer",
		ThreadCount: 1, RecentAnchors: []string{"x", "y"},
		OneLineSummary: "newer summary",
	})

	params, err := Compose(defaultCompose(State{Paths: paths, ActiveProject: active}), ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	lines := strings.Split(strings.TrimRight(params.LayerA2, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 A2 lines; got %d: %q", len(lines), params.LayerA2)
	}
	// Newer project sorts first.
	if !strings.HasPrefix(lines[0], "newer (prj_3)") {
		t.Errorf("first A2 line should be prj_3; got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "older (prj_2)") {
		t.Errorf("second A2 line should be prj_2; got %q", lines[1])
	}
	// Active project must NOT appear.
	if strings.Contains(params.LayerA2, "prj_1") {
		t.Errorf("active project leaked into A2: %q", params.LayerA2)
	}
	// Top-5 anchor cap.
	if !strings.Contains(lines[0], "x, y") {
		t.Errorf("expected newer's anchors in line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "a, b, c, d, e") || strings.Contains(lines[1], ", f") {
		t.Errorf("expected older's anchors capped at 5: %q", lines[1])
	}
}

func TestLayerA2EmptyWhenNoOthers(t *testing.T) {
	paths := newHome(t)
	active := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, active); err != nil {
		t.Fatalf("save active: %v", err)
	}
	params, err := Compose(defaultCompose(State{Paths: paths, ActiveProject: active}), ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if params.LayerA2 != "" {
		t.Errorf("expected empty A2; got %q", params.LayerA2)
	}
}

// TestLayerBRespectsBTopK: 5 active threads with BTopK=3 → only the
// first 3 are rendered.
func TestLayerBRespectsBTopK(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	for i := 1; i <= 5; i++ {
		seedThread(t, paths, fmt.Sprintf("thr_%d", i), meta.ID, fmt.Sprintf("thread %d body BBBBBBBBBB", i))
	}
	budget := memops.DefaultBudget()
	state := State{
		Paths:         paths,
		ActiveProject: meta,
		ActiveThreads: []string{"thr_1", "thr_2", "thr_3", "thr_4", "thr_5"},
		Budget:        budget,
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, want := range []string{"(thr_1)", "(thr_2)", "(thr_3)"} {
		if !strings.Contains(params.LayerB, want) {
			t.Errorf("LayerB missing %s", want)
		}
	}
	for _, no := range []string{"(thr_4)", "(thr_5)"} {
		if strings.Contains(params.LayerB, no) {
			t.Errorf("LayerB leaked thread past BTopK: %s", no)
		}
	}
}

// TestLayerBOversizedThreadTruncates: a single thread with body longer
// than its per-thread share is truncated and tagged with the marker.
func TestLayerBOversizedThreadTruncates(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	huge := strings.Repeat("X", 100*1024)
	seedThread(t, paths, "thr_1", meta.ID, huge)
	budget := memops.DefaultBudget()
	budget.LayerB = 1024 // tight
	state := State{
		Paths:         paths,
		ActiveProject: meta,
		ActiveThreads: []string{"thr_1"},
		Budget:        budget,
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(params.LayerB) > budget.LayerB {
		t.Errorf("LayerB exceeded budget: %d > %d", len(params.LayerB), budget.LayerB)
	}
	if !strings.Contains(params.LayerB, "truncated") {
		t.Errorf("expected truncation marker in LayerB; got %q", params.LayerB)
	}
}

func TestLayerBMissingThreadFileWarns(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, format) }
	state := State{
		Paths:         paths,
		ActiveProject: meta,
		ActiveThreads: []string{"thr_does_not_exist"},
		Budget:        memops.DefaultBudget(),
	}
	params, err := Compose(state, ComposeOptions{Logger: logf})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if params.LayerB != "" {
		t.Errorf("LayerB should be empty when only thread is missing; got %q", params.LayerB)
	}
	if len(logs) == 0 {
		t.Errorf("expected a warning log for missing thread")
	}
}

func TestLayerCRendersDormantSpineDisplays(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("thr_%d", i)
		if err := store.AppendSpineRecord(paths, memops.SpineRecord{
			ID: id, Project: meta.ID,
			Anchors: []string{"a", "b", "c", "d"},
			Summary: fmt.Sprintf("dormant %d", i),
			State:   memops.ThreadPaused,
		}); err != nil {
			t.Fatalf("append spine: %v", err)
		}
	}
	state := State{
		Paths:          paths,
		ActiveProject:  meta,
		DormantThreads: []string{"thr_1", "thr_2", "thr_3"},
		Budget:         memops.DefaultBudget(),
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("thr_%d [a, b, c, d] — dormant %d [PAUSED]", i, i)
		if !strings.Contains(params.LayerC, want) {
			t.Errorf("LayerC missing %q\ngot: %s", want, params.LayerC)
		}
	}
}

func TestLayerCBudgetTruncates(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	const N = 50
	dormant := make([]string, 0, N)
	for i := 1; i <= N; i++ {
		id := fmt.Sprintf("thr_%d", i)
		dormant = append(dormant, id)
		if err := store.AppendSpineRecord(paths, memops.SpineRecord{
			ID: id, Project: meta.ID,
			Anchors: []string{"a-very-long-anchor-name", "another-long-anchor", "third-anchor", "fourth-anchor"},
			Summary: strings.Repeat("dormant summary text ", 10),
			State:   memops.ThreadPaused,
		}); err != nil {
			t.Fatalf("append spine: %v", err)
		}
	}
	budget := memops.DefaultBudget()
	budget.LayerC = 256
	state := State{
		Paths:          paths,
		ActiveProject:  meta,
		DormantThreads: dormant,
		Budget:         budget,
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(params.LayerC) > budget.LayerC {
		t.Errorf("LayerC exceeded budget: %d > %d", len(params.LayerC), budget.LayerC)
	}
}

// TestComposeFullIntegration: 2 projects (active + 1 other), 5 threads
// (3 active in B, 2 dormant in C), directives, conventions. Verifies
// every layer is populated.
func TestComposeFullIntegration(t *testing.T) {
	paths := newHome(t)
	active := memops.ProjectMeta{ID: "prj_1", Name: "active"}
	if err := store.SaveProjectMeta(paths, active); err != nil {
		t.Fatalf("save active: %v", err)
	}
	other := memops.ProjectMeta{
		ID: "prj_2", Name: "other",
		LastActive: "2026-04-01T00:00:00Z",
	}
	if err := store.SaveProjectMeta(paths, other); err != nil {
		t.Fatalf("save other: %v", err)
	}
	writeDigest(t, paths, memops.ProjectDigest{
		Project: "prj_2", DisplayName: "other",
		ThreadCount: 1, RecentAnchors: []string{"alpha", "beta"},
		OneLineSummary: "other-summary",
	})

	// Directives.
	if err := os.WriteFile(filepath.Join(paths.DirectivesDir, "defaults.md"),
		[]byte("---\nscope: defaults\n---\nDEFAULTS\n"), 0o644); err != nil {
		t.Fatalf("write defaults.md: %v", err)
	}

	// Conventions file.
	convDir := t.TempDir()
	convPath := filepath.Join(convDir, "AGENTS.md")
	if err := os.WriteFile(convPath, []byte("WORKSPACE_AGENTS\n"), 0o644); err != nil {
		t.Fatalf("write conv: %v", err)
	}
	active.ConventionsPaths = []string{convPath}
	if err := store.SaveProjectMeta(paths, active); err != nil {
		t.Fatalf("save active+conv: %v", err)
	}

	// Threads: thr_1..thr_5 in active project. Spine + thread file.
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("thr_%d", i)
		if err := store.AppendSpineRecord(paths, memops.SpineRecord{
			ID: id, Project: active.ID,
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: fmt.Sprintf("thread %d summary", i),
			State:   memops.ThreadActive,
		}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		seedThread(t, paths, id, active.ID, fmt.Sprintf("body of thread %d", i))
	}

	state := State{
		Paths:          paths,
		ActiveProject:  active,
		ActiveThreads:  []string{"thr_3", "thr_2", "thr_1"},
		DormantThreads: []string{"thr_4", "thr_5"},
		Budget:         memops.DefaultBudget(),
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	// Layer E.
	if !strings.Contains(params.LayerE, "DEFAULTS") {
		t.Errorf("LayerE missing defaults: %q", params.LayerE)
	}
	if !strings.Contains(params.LayerE, "WORKSPACE_AGENTS") {
		t.Errorf("LayerE missing conventions: %q", params.LayerE)
	}

	// Layer A1: 5 spine records.
	for i := 1; i <= 5; i++ {
		if !strings.Contains(params.LayerA1, fmt.Sprintf("thr_%d ", i)) {
			t.Errorf("LayerA1 missing thr_%d", i)
		}
	}

	// Layer A2: prj_2 line.
	if !strings.Contains(params.LayerA2, "(prj_2)") {
		t.Errorf("LayerA2 missing prj_2: %q", params.LayerA2)
	}

	// Layer B: thr_3, thr_2, thr_1 (BTopK=3 ≥ 3 so all get rendered).
	for _, want := range []string{"(thr_1)", "(thr_2)", "(thr_3)"} {
		if !strings.Contains(params.LayerB, want) {
			t.Errorf("LayerB missing %s", want)
		}
	}
	for _, no := range []string{"(thr_4)", "(thr_5)"} {
		if strings.Contains(params.LayerB, no) {
			t.Errorf("LayerB leaked dormant: %s", no)
		}
	}

	// Layer C: thr_4, thr_5 spine displays.
	for _, want := range []string{"thr_4 ", "thr_5 "} {
		if !strings.Contains(params.LayerC, want) {
			t.Errorf("LayerC missing %s", want)
		}
	}
}

// TestLayerBTrackedFilesSection seeds a .files.json sidecar for an
// active thread and asserts renderThreadBody appends the §3.9.2
// tracked-files section: a path header, the current literal, and older
// versions as diffs / content-addressed identifiers.
func TestLayerBTrackedFilesSection(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	seedThread(t, paths, "thr_1", meta.ID, "thread body text")

	// Build a tracked file with enough versions to produce both a diff
	// and an identifier entry under the default 3-diff window.
	tf := store.ThreadFiles{ThreadID: "thr_1", Files: map[string]*store.FileEntry{}}
	for i := 0; i < 6; i++ {
		body := fmt.Sprintf("title\nrevision %d\nstable footer\n", i)
		tf.RecordWrite("src/main.go", body)
	}
	if err := store.SaveThreadFiles(paths, tf); err != nil {
		t.Fatalf("save thread files: %v", err)
	}

	state := State{
		Paths:         paths,
		ActiveProject: meta,
		ActiveThreads: []string{"thr_1"},
		Budget:        memops.DefaultBudget(),
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, want := range []string{
		"=== tracked files ===",
		"--- src/main.go ---",
		"revision 5",                // current literal
		"[version 4 diff]",          // a recent-diff entry
		"see most-recent position]", // an older-version identifier
	} {
		if !strings.Contains(params.LayerB, want) {
			t.Errorf("LayerB missing %q\ngot: %s", want, params.LayerB)
		}
	}
}

// TestLayerBNoTrackedFilesSidecar confirms a thread with no .files.json
// sidecar renders no tracked-files section at all.
func TestLayerBNoTrackedFilesSidecar(t *testing.T) {
	paths := newHome(t)
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha"}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	seedThread(t, paths, "thr_1", meta.ID, "plain thread body")

	state := State{
		Paths:         paths,
		ActiveProject: meta,
		ActiveThreads: []string{"thr_1"},
		Budget:        memops.DefaultBudget(),
	}
	params, err := Compose(state, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if strings.Contains(params.LayerB, "tracked files") {
		t.Errorf("LayerB should have no tracked-files section; got %q", params.LayerB)
	}
}

// ---- helpers ----

func writeDigest(t *testing.T, paths store.PersonantPaths, d memops.ProjectDigest) {
	t.Helper()
	dir := filepath.Join(paths.ProjectsDir, d.Project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir digest dir: %v", err)
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "digest.json"), data, 0o644); err != nil {
		t.Fatalf("write digest: %v", err)
	}
}

func seedThread(t *testing.T, paths store.PersonantPaths, id, projectID, body string) {
	t.Helper()
	thr := memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID:      id,
			Project: projectID,
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: id + " summary",
			State:   memops.ThreadActive,
		},
		Body: body,
	}
	if err := store.SeedThread(paths, thr); err != nil {
		t.Fatalf("save thread %s: %v", id, err)
	}
}
