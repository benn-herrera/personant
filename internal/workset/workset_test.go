package workset

import (
	"fmt"
	"strings"
	"testing"

	"personant/internal/dedup"
	"personant/internal/memops"
)

// defaultBudget returns a baseline budget for tests that don't care.
func defaultBudget() memops.Budget { return memops.DefaultBudget() }

// activeMeta is the canonical "current project" fixture.
var activeMeta = memops.ProjectMeta{ID: "prj_1", Name: "alpha"}

func TestComposeRequiresProjectID(t *testing.T) {
	if _, err := Compose(Inputs{}, ComposeOptions{}); err == nil {
		t.Fatalf("expected error for empty ActiveProject.ID")
	}
}

func TestComposeEmptyInputs(t *testing.T) {
	in := Inputs{ActiveProject: activeMeta, Budget: defaultBudget()}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if params.LayerE != "" || params.LayerA1 != "" || params.LayerA2 != "" || params.LayerB != "" || params.LayerC != "" {
		t.Errorf("all layers should be empty when no inputs provided; got %+v", params)
	}
}

func TestComposeRendersA1DisplayLines(t *testing.T) {
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		ActiveProjectSpine: []memops.SpineRecord{
			{
				ID: "thr_1", Project: "prj_1",
				Anchors: []string{"alpha", "beta", "gamma", "delta"},
				Summary: "first thread",
				State:   memops.ThreadActive,
			},
			{
				ID: "thr_2", Project: "prj_1",
				Anchors: []string{"trefoil", "unknot", "body-topology", "electron-shape"},
				Summary: "topology conflict; awaiting resolution",
				State:   memops.ThreadWIP,
			},
		},
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	lines := strings.Split(params.LayerA1, "\n")
	if got, want := len(lines), 2; got != want {
		t.Fatalf("expected %d lines, got %d: %q", want, got, params.LayerA1)
	}
	if !strings.HasPrefix(lines[0], "thr_1 ") || !strings.Contains(lines[0], "[ACTIVE]") {
		t.Errorf("first line: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "thr_2 ") || !strings.Contains(lines[1], "[WIP]") {
		t.Errorf("second line: %q", lines[1])
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

func TestLayerERendersDirectivesAndConventions(t *testing.T) {
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		Directives: []DirectiveSection{
			{Header: "defaults", Body: "DEFAULTS_BODY\n"},
			{Header: "user", Body: "USER_BODY\n"},
			{Header: "project: alpha", Body: "PROJECT_BODY\n"},
		},
		Conventions: []ConventionFile{
			{Path: "/ws/CONVENTIONS.md", Content: "CONVENTIONS_BODY\n"},
		},
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, want := range []string{"DEFAULTS_BODY", "USER_BODY", "PROJECT_BODY", "CONVENTIONS_BODY"} {
		if !strings.Contains(params.LayerE, want) {
			t.Errorf("LayerE missing %q\ngot: %s", want, params.LayerE)
		}
	}
	for _, header := range []string{
		"=== defaults ===",
		"=== user ===",
		"=== project: alpha ===",
		"=== conventions: /ws/CONVENTIONS.md ===",
	} {
		if !strings.Contains(params.LayerE, header) {
			t.Errorf("LayerE missing header %q\ngot: %s", header, params.LayerE)
		}
	}
}

func TestLayerESkipsEmptyDirectives(t *testing.T) {
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		Directives: []DirectiveSection{
			{Header: "defaults", Body: ""},
			{Header: "user", Body: "real content\n"},
		},
	}
	params, _ := Compose(in, ComposeOptions{})
	if strings.Contains(params.LayerE, "=== defaults ===") {
		t.Errorf("empty defaults directive should not render: %q", params.LayerE)
	}
	if !strings.Contains(params.LayerE, "=== user ===") {
		t.Errorf("expected user directive header: %q", params.LayerE)
	}
}

func TestLayerA2OrdersByLastActiveDesc(t *testing.T) {
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		OtherProjects: []ProjectDigestEntry{
			{
				Meta:   memops.ProjectMeta{ID: "prj_2", Name: "older", LastActive: "2026-04-01T00:00:00Z"},
				Digest: memops.ProjectDigest{Project: "prj_2", OneLineSummary: "older summary", RecentAnchors: []string{"a", "b", "c", "d", "e", "f"}},
			},
			{
				Meta:   memops.ProjectMeta{ID: "prj_3", Name: "newer", LastActive: "2026-05-01T00:00:00Z"},
				Digest: memops.ProjectDigest{Project: "prj_3", OneLineSummary: "newer summary", RecentAnchors: []string{"x", "y"}},
			},
		},
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	lines := strings.Split(strings.TrimRight(params.LayerA2, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 A2 lines; got %d: %q", len(lines), params.LayerA2)
	}
	if !strings.HasPrefix(lines[0], "newer (prj_3)") {
		t.Errorf("newer (prj_3) should sort first; got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "older (prj_2)") {
		t.Errorf("older (prj_2) should sort second; got %q", lines[1])
	}
	// Top-5 anchor cap.
	if !strings.Contains(lines[1], "a, b, c, d, e") || strings.Contains(lines[1], ", f") {
		t.Errorf("expected older's anchors capped at 5: %q", lines[1])
	}
}

func TestLayerA2EmptyWhenNoOthers(t *testing.T) {
	in := Inputs{ActiveProject: activeMeta, Budget: defaultBudget()}
	params, _ := Compose(in, ComposeOptions{})
	if params.LayerA2 != "" {
		t.Errorf("expected empty A2; got %q", params.LayerA2)
	}
}

func TestLayerBRespectsBTopK(t *testing.T) {
	threads := make(map[string]ThreadData, 5)
	ids := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("thr_%d", i)
		ids = append(ids, id)
		threads[id] = ThreadData{
			Meta: memops.ThreadMeta{
				ID: id, Project: "prj_1",
				Anchors: []string{"alpha"},
				Summary: id + " summary",
				State:   memops.ThreadActive,
			},
			Body: fmt.Sprintf("thread %d body BBBBBBBBBB", i),
		}
	}
	in := Inputs{
		ActiveProject:    activeMeta,
		Budget:           defaultBudget(),
		ActiveThreads:    ids,
		ActiveThreadData: threads,
	}
	params, err := Compose(in, ComposeOptions{})
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

func TestLayerBOversizedThreadTruncates(t *testing.T) {
	huge := strings.Repeat("X", 100*1024)
	id := "thr_1"
	in := Inputs{
		ActiveProject: activeMeta,
		Budget: func() memops.Budget {
			b := defaultBudget()
			b.LayerB = 1024 // tight
			return b
		}(),
		ActiveThreads: []string{id},
		ActiveThreadData: map[string]ThreadData{
			id: {
				Meta: memops.ThreadMeta{ID: id, Anchors: []string{"alpha"}, Summary: "huge", State: memops.ThreadActive},
				Body: huge,
			},
		},
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(params.LayerB) > in.Budget.LayerB {
		t.Errorf("LayerB exceeded budget: %d > %d", len(params.LayerB), in.Budget.LayerB)
	}
	if !strings.Contains(params.LayerB, "truncated") {
		t.Errorf("expected truncation marker in LayerB; got %q", params.LayerB)
	}
}

func TestLayerBMissingThreadDataSilent(t *testing.T) {
	// A thread id with no entry in ActiveThreadData is treated as
	// substrate-missing — the adapter has already logged the warning;
	// workset just skips it.
	in := Inputs{
		ActiveProject:    activeMeta,
		Budget:           defaultBudget(),
		ActiveThreads:    []string{"thr_missing"},
		ActiveThreadData: map[string]ThreadData{},
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if params.LayerB != "" {
		t.Errorf("LayerB should be empty when only thread is missing; got %q", params.LayerB)
	}
}

func TestLayerBTrackedFilesSection(t *testing.T) {
	// Hand-build a window covering current + diff + identifier kinds so
	// the test is independent of dedup's anchor cadence.
	window := []dedup.WindowEntry{
		{Kind: "identifier", Version: 0, Text: "[chain anchor: see most-recent position]"},
		{Kind: "diff", Version: 4, Text: "@@ -1 +1 @@\n-old\n+new\n"},
		{Kind: "current", Version: 5, Text: "title\nrevision 5\nstable footer\n"},
	}
	id := "thr_1"
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		ActiveThreads: []string{id},
		ActiveThreadData: map[string]ThreadData{
			id: {
				Meta: memops.ThreadMeta{ID: id, Anchors: []string{"alpha"}, Summary: "with files", State: memops.ThreadActive},
				Body: "thread body text",
				TrackedFiles: []TrackedFile{{
					Path:   "src/main.go",
					Window: window,
				}},
			},
		},
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, want := range []string{
		"=== tracked files ===",
		"--- src/main.go ---",
		"revision 5",
		"[version 4 diff]",
		"see most-recent position",
	} {
		if !strings.Contains(params.LayerB, want) {
			t.Errorf("LayerB missing %q\ngot: %s", want, params.LayerB)
		}
	}
}

func TestLayerBNoTrackedFilesSection(t *testing.T) {
	id := "thr_1"
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		ActiveThreads: []string{id},
		ActiveThreadData: map[string]ThreadData{
			id: {
				Meta: memops.ThreadMeta{ID: id, Summary: "plain", State: memops.ThreadActive},
				Body: "plain thread body",
			},
		},
	}
	params, _ := Compose(in, ComposeOptions{})
	if strings.Contains(params.LayerB, "tracked files") {
		t.Errorf("LayerB should have no tracked-files section; got %q", params.LayerB)
	}
}

func TestLayerCRendersDormantSpineDisplays(t *testing.T) {
	dormant := make(map[string]memops.SpineRecord, 3)
	ids := []string{"thr_1", "thr_2", "thr_3"}
	for i, id := range ids {
		dormant[id] = memops.SpineRecord{
			ID: id, Project: "prj_1",
			Anchors: []string{"a", "b", "c", "d"},
			Summary: fmt.Sprintf("dormant %d", i+1),
			State:   memops.ThreadPaused,
		}
	}
	in := Inputs{
		ActiveProject:  activeMeta,
		Budget:         defaultBudget(),
		DormantThreads: ids,
		DormantSpine:   dormant,
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for i, id := range ids {
		want := fmt.Sprintf("%s [a, b, c, d] — dormant %d [PAUSED]", id, i+1)
		if !strings.Contains(params.LayerC, want) {
			t.Errorf("LayerC missing %q\ngot: %s", want, params.LayerC)
		}
	}
}

func TestLayerCBudgetTruncates(t *testing.T) {
	const N = 50
	dormant := make(map[string]memops.SpineRecord, N)
	ids := make([]string, 0, N)
	for i := 1; i <= N; i++ {
		id := fmt.Sprintf("thr_%d", i)
		ids = append(ids, id)
		dormant[id] = memops.SpineRecord{
			ID: id, Project: "prj_1",
			Anchors: []string{"a-very-long-anchor-name", "another-long-anchor", "third-anchor", "fourth-anchor"},
			Summary: strings.Repeat("dormant summary text ", 10),
			State:   memops.ThreadPaused,
		}
	}
	budget := defaultBudget()
	budget.LayerC = 256
	in := Inputs{
		ActiveProject:  activeMeta,
		Budget:         budget,
		DormantThreads: ids,
		DormantSpine:   dormant,
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(params.LayerC) > budget.LayerC {
		t.Errorf("LayerC exceeded budget: %d > %d", len(params.LayerC), budget.LayerC)
	}
}

func TestComposeFullIntegration(t *testing.T) {
	// All five layers populated.
	dormantIDs := []string{"thr_4", "thr_5"}
	dormant := map[string]memops.SpineRecord{}
	for _, id := range dormantIDs {
		dormant[id] = memops.SpineRecord{
			ID: id, Project: "prj_1",
			Anchors: []string{"alpha"},
			Summary: id + " dormant",
			State:   memops.ThreadPaused,
		}
	}
	active := map[string]ThreadData{}
	activeIDs := []string{"thr_3", "thr_2", "thr_1"}
	for _, id := range activeIDs {
		active[id] = ThreadData{
			Meta: memops.ThreadMeta{ID: id, Summary: id, State: memops.ThreadActive},
			Body: "body of " + id,
		}
	}
	spineRecs := make([]memops.SpineRecord, 0, 5)
	for i := 1; i <= 5; i++ {
		spineRecs = append(spineRecs, memops.SpineRecord{
			ID:      fmt.Sprintf("thr_%d", i),
			Project: "prj_1",
			Anchors: []string{"alpha"},
			Summary: fmt.Sprintf("thread %d summary", i),
			State:   memops.ThreadActive,
		})
	}
	in := Inputs{
		ActiveProject: activeMeta,
		Budget:        defaultBudget(),
		Directives: []DirectiveSection{
			{Header: "defaults", Body: "DEFAULTS\n"},
		},
		Conventions: []ConventionFile{
			{Path: "/ws/CONVENTIONS.md", Content: "WORKSPACE_AGENTS\n"},
		},
		ActiveProjectSpine: spineRecs,
		OtherProjects: []ProjectDigestEntry{{
			Meta:   memops.ProjectMeta{ID: "prj_2", Name: "other", LastActive: "2026-04-01T00:00:00Z"},
			Digest: memops.ProjectDigest{Project: "prj_2", OneLineSummary: "other-summary", RecentAnchors: []string{"alpha", "beta"}},
		}},
		ActiveThreads:    activeIDs,
		ActiveThreadData: active,
		DormantThreads:   dormantIDs,
		DormantSpine:     dormant,
	}
	params, err := Compose(in, ComposeOptions{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !strings.Contains(params.LayerE, "DEFAULTS") {
		t.Errorf("LayerE missing defaults: %q", params.LayerE)
	}
	if !strings.Contains(params.LayerE, "WORKSPACE_AGENTS") {
		t.Errorf("LayerE missing conventions: %q", params.LayerE)
	}
	for i := 1; i <= 5; i++ {
		if !strings.Contains(params.LayerA1, fmt.Sprintf("thr_%d ", i)) {
			t.Errorf("LayerA1 missing thr_%d", i)
		}
	}
	if !strings.Contains(params.LayerA2, "(prj_2)") {
		t.Errorf("LayerA2 missing prj_2: %q", params.LayerA2)
	}
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
	for _, want := range []string{"thr_4 ", "thr_5 "} {
		if !strings.Contains(params.LayerC, want) {
			t.Errorf("LayerC missing %s", want)
		}
	}
}

// TestComposeWarnsOnEmptyLayerDespiteInputs verifies the §3.1
// render-stage workset.warning seam: when a layer is handed input ids but
// the pre-fetched data for every one is missing, the layer renders empty
// and ComposeOptions.Logger fires exactly one warning naming that layer.
func TestComposeWarnsOnEmptyLayerDespiteInputs(t *testing.T) {
	cases := []struct {
		name      string
		in        Inputs
		wantLayer string // substring identifying which layer warned
		wantEmpty func(prompt string) bool
	}{
		{
			name: "layer B all threads missing data",
			in: Inputs{
				ActiveProject:    activeMeta,
				Budget:           defaultBudget(),
				ActiveThreads:    []string{"thr_1", "thr_2"},
				ActiveThreadData: nil, // no data pre-fetched for either id
			},
			wantLayer: "layer B",
		},
		{
			name: "layer C all dormant missing spine",
			in: Inputs{
				ActiveProject:  activeMeta,
				Budget:         defaultBudget(),
				DormantThreads: []string{"thr_9"},
				DormantSpine:   nil,
			},
			wantLayer: "layer C",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warnings []string
			logger := func(format string, args ...any) {
				warnings = append(warnings, fmt.Sprintf(format, args...))
			}
			if _, err := Compose(tc.in, ComposeOptions{Logger: logger}); err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
			}
			if !strings.Contains(warnings[0], tc.wantLayer) {
				t.Errorf("warning %q does not name %q", warnings[0], tc.wantLayer)
			}
		})
	}
}

// TestComposeNoWarnWhenLayerRenders confirms the warning does NOT fire on
// a healthy composition (data present) nor when a layer is legitimately
// empty because no inputs were supplied — only genuine render-stage loss
// warns.
func TestComposeNoWarnWhenLayerRenders(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
	}{
		{
			name: "no inputs at all",
			in:   Inputs{ActiveProject: activeMeta, Budget: defaultBudget()},
		},
		{
			name: "layer B renders with data present",
			in: Inputs{
				ActiveProject: activeMeta,
				Budget:        defaultBudget(),
				ActiveThreads: []string{"thr_1"},
				ActiveThreadData: map[string]ThreadData{"thr_1": {
					Meta: memops.ThreadMeta{ID: "thr_1", Summary: "s", State: memops.ThreadActive},
					Body: "body",
				}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warnings []string
			logger := func(format string, args ...any) {
				warnings = append(warnings, fmt.Sprintf(format, args...))
			}
			if _, err := Compose(tc.in, ComposeOptions{Logger: logger}); err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// TestPerThreadBudgetFloor pins the substrate-fetch contract: the
// adapter uses PerThreadBudget to bound store.ReadThreadBody reads to
// the same share workset will render at, so the floor enforcement here
// matches the render-time floor exactly.
func TestPerThreadBudgetFloor(t *testing.T) {
	if got := PerThreadBudget(100, 0); got != 100 {
		t.Errorf("n=0: got %d, want 100", got)
	}
	// Above the floor: 40960/4 = 10240 (> minPerThreadBudget).
	if got := PerThreadBudget(40960, 4); got != 10240 {
		t.Errorf("40960/4: got %d, want 10240", got)
	}
	// Below the floor: share floors to minPerThreadBudget (#127: 4096).
	if got := PerThreadBudget(2048, 4); got != minPerThreadBudget {
		t.Errorf("floor (small budget): got %d, want %d", got, minPerThreadBudget)
	}
	if got := PerThreadBudget(100, 10); got != minPerThreadBudget {
		t.Errorf("floor: got %d, want %d", got, minPerThreadBudget)
	}
}
