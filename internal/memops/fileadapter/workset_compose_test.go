// Tests for the substrate-fetch half of ComposeWorkingSet. The pure
// rendering half is covered in internal/workset/workset_test.go; these
// tests confirm the adapter loads each layer's substrate state
// correctly and that warnings route through the event log.

package fileadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

// TestComposeWorkingSet_LayerE_DirectivesAndConventions seeds the three
// directive files plus a conventions file and verifies Layer E content.
func TestComposeWorkingSet_LayerE_DirectivesAndConventions(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := a.CreateProject(ctx, meta); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// store.Init seeds defaults.md and user.md with template content;
	// rewrite them with marker bodies so the test asserts on known data.
	defaults := "---\nscope: defaults\n---\nDEFAULTS_BODY\n"
	user := "USER_BODY_NO_FRONTMATTER\n"
	if err := os.WriteFile(filepath.Join(a.paths.DirectivesDir, "defaults.md"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("write defaults.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.paths.DirectivesDir, "user.md"), []byte(user), 0o644); err != nil {
		t.Fatalf("write user.md: %v", err)
	}
	projectDir := filepath.Join(a.paths.DirectivesDir, "prj_1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "project.md"),
		[]byte("---\nscope: project\n---\nPROJECT_BODY\n"), 0o644); err != nil {
		t.Fatalf("write project.md: %v", err)
	}

	convDir := t.TempDir()
	convPath := filepath.Join(convDir, "AGENTS.md")
	if err := os.WriteFile(convPath, []byte("CONVENTIONS_BODY\n"), 0o644); err != nil {
		t.Fatalf("write conventions: %v", err)
	}
	meta.ConventionsPaths = []string{convPath}
	if err := a.SaveProject(ctx, meta); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{ActiveProject: meta})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	for _, want := range []string{"DEFAULTS_BODY", "USER_BODY_NO_FRONTMATTER", "PROJECT_BODY", "CONVENTIONS_BODY"} {
		if !strings.Contains(out.LayerE, want) {
			t.Errorf("LayerE missing %q\ngot: %s", want, out.LayerE)
		}
	}
	for _, header := range []string{"=== defaults ===", "=== user ===", "=== project: alpha ===", "=== conventions: " + convPath + " ==="} {
		if !strings.Contains(out.LayerE, header) {
			t.Errorf("LayerE missing header %q\ngot: %s", header, out.LayerE)
		}
	}
}

// TestComposeWorkingSet_LayerE_MissingConventionsLogsWarning verifies
// that an unreadable conventions path is emitted to the event log as a
// workset.warning entry.
func TestComposeWorkingSet_LayerE_MissingConventionsLogsWarning(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{
		ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now(),
		ConventionsPaths: []string{"/nonexistent/path/CONVENTIONS.md"},
	}
	if err := a.CreateProject(ctx, meta); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{ActiveProject: meta}); err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	assertEventLogContains(t, a.paths, "workset", "warning", "conventions")
}

// TestComposeWorkingSet_LayerA2_DigestsSortedByLastActive seeds two
// non-active projects with digests and verifies the A2 layer renders
// them sorted by LastActive desc.
func TestComposeWorkingSet_LayerA2_DigestsSortedByLastActive(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	active := memops.ProjectMeta{ID: "prj_1", Name: "active", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := a.CreateProject(ctx, active); err != nil {
		t.Fatalf("create active: %v", err)
	}
	older := memops.ProjectMeta{ID: "prj_2", Name: "older", Created: rfc3339Now(), LastActive: "2026-04-01T00:00:00Z"}
	if err := a.CreateProject(ctx, older); err != nil {
		t.Fatalf("create older: %v", err)
	}
	newer := memops.ProjectMeta{ID: "prj_3", Name: "newer", Created: rfc3339Now(), LastActive: "2026-05-01T00:00:00Z"}
	if err := a.CreateProject(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}
	writeDigest(t, a.paths, memops.ProjectDigest{
		Project: "prj_2", DisplayName: "older",
		ThreadCount: 2, RecentAnchors: []string{"a", "b", "c", "d", "e", "f"},
		OneLineSummary: "older summary",
	})
	writeDigest(t, a.paths, memops.ProjectDigest{
		Project: "prj_3", DisplayName: "newer",
		ThreadCount: 1, RecentAnchors: []string{"x", "y"},
		OneLineSummary: "newer summary",
	})

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{ActiveProject: active})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.LayerA2, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 A2 lines; got %d: %q", len(lines), out.LayerA2)
	}
	if !strings.HasPrefix(lines[0], "newer (prj_3)") {
		t.Errorf("first A2 line should be prj_3; got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "older (prj_2)") {
		t.Errorf("second A2 line should be prj_2; got %q", lines[1])
	}
	if strings.Contains(out.LayerA2, "prj_1") {
		t.Errorf("active project leaked into A2: %q", out.LayerA2)
	}
}

// TestComposeWorkingSet_LayerB_TrackedFiles seeds a thread with a
// tracked-file sidecar and verifies the tracked-files section is
// rendered with the current literal + history.
func TestComposeWorkingSet_LayerB_TrackedFiles(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := a.CreateProject(ctx, meta); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	rec := validSpine("thr_1", "prj_1")
	if err := a.CreateThread(ctx, memops.ThreadWrite{
		Spine:       rec,
		Meta: validFrontmatter(rec),
		TurnExcerpt: "## Turn 1\n\nthread body text\n",
	}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	// Six writes ensure both diff- and identifier-kind entries fall in
	// the live window under the default 3-diff cap.
	for i := 0; i < 6; i++ {
		body := fmt.Sprintf("title\nrevision %d\nstable footer\n", i)
		if err := a.RecordFileWrite(ctx, "thr_1", "src/main.go", body); err != nil {
			t.Fatalf("RecordFileWrite[%d]: %v", i, err)
		}
	}

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{
		ActiveProject: meta,
		ActiveThreads: []string{"thr_1"},
	})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	for _, want := range []string{
		"=== tracked files ===",
		"--- src/main.go ---",
		"revision 5",
		"[version 4 diff]",
		"see most-recent position",
	} {
		if !strings.Contains(out.LayerB, want) {
			t.Errorf("LayerB missing %q\ngot: %s", want, out.LayerB)
		}
	}
}

// TestComposeWorkingSet_LayerB_MissingThreadLogsWarning verifies that
// an active thread id with no spine record / no thread file emits a
// workset.warning event-log line.
func TestComposeWorkingSet_LayerB_MissingThreadLogsWarning(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := a.CreateProject(ctx, meta); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{
		ActiveProject: meta,
		ActiveThreads: []string{"thr_does_not_exist"},
	})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	if out.LayerB != "" {
		t.Errorf("LayerB should be empty when only thread is missing; got %q", out.LayerB)
	}
	assertEventLogContains(t, a.paths, "workset", "warning", "thr_does_not_exist")
}

// TestStripFrontmatter exercises the YAML-frontmatter trimming used by
// directive loading. The function lives in the adapter alongside its
// only caller; the table mirrors the substrate behavior contract.
func TestStripFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "no frontmatter", in: "# heading\nbody\n", want: "# heading\nbody\n"},
		{name: "frontmatter stripped", in: "---\nfoo: bar\n---\nbody\n", want: "body\n"},
		{name: "frontmatter with blank line after close", in: "---\nfoo: bar\n---\n\nbody\n", want: "body\n"},
		{name: "leading delimiter without close", in: "---\nfoo: bar\nbody", want: "---\nfoo: bar\nbody"},
		{name: "embedded triple-dash in body left alone", in: "---\nfoo: bar\n---\nbody\n```\n---\n```\n", want: "body\n```\n---\n```\n"},
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

// assertEventLogContains scans every *.log file under the LogsDir and
// fails if no line matches all three substring needles. Used to verify
// adapter-side warnings hit the canonical destination.
func assertEventLogContains(t *testing.T, paths store.PersonantPaths, needles ...string) {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("event log dir missing — no warning emitted")
		}
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			matched := true
			for _, n := range needles {
				if !strings.Contains(line, n) {
					matched = false
					break
				}
			}
			if matched {
				return
			}
		}
	}
	t.Errorf("expected event-log line containing %v; not found", needles)
}
