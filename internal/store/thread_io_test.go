package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"personant/internal/memops"
)

func newThreadHome(t *testing.T) PersonantPaths {
	t.Helper()
	tmp := t.TempDir()
	paths := PathsForHome(tmp)
	if err := os.MkdirAll(paths.ThreadsDir, 0o755); err != nil {
		t.Fatalf("mkdir threads: %v", err)
	}
	return paths
}

func sampleThread() memops.Thread {
	return memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID:           "thr_42",
			Project:      "prj_3",
			Anchors:      []string{"trefoil", "unknot", "body-topology", "electron-shape"},
			Summary:      "topology investigation; trefoil vs unknot.",
			State:        memops.ThreadWIP,
			Created:      "2026-05-06T14:23:00-07:00",
			LastEngaged:  "2026-05-08T03:12:00-07:00",
			StateChanged: "2026-05-07T19:42:00-07:00",
			TurnCount:    24,
			RecallFires:  3,
			HistorySymbols: []memops.HistorySymbol{
				{Raw: "trefoil", Normalized: "trefoil", FirstSeenTurn: 142, Count: 17, Source: memops.SourceDeterministic},
				{Raw: "(3,2)-torus knot", Normalized: "3-2-torus-knot", FirstSeenTurn: 145, Count: 4, Source: memops.SourceModel},
				{Raw: "Faddeev-Skyrme", Normalized: "faddeev-skyrme", FirstSeenTurn: 148, Count: 2, Source: memops.SourceUser},
				{Raw: "Curator pick", Normalized: "curator-pick", FirstSeenTurn: 150, Count: 1, Source: memops.SourceCurator},
			},
		},
		Body: "# Body topology — trefoil vs unknot\n\nOperational notes go here.\n",
	}
}

func TestThreadRoundTrip(t *testing.T) {
	paths := newThreadHome(t)
	in := sampleThread()

	if err := SaveThread(paths, in); err != nil {
		t.Fatalf("SaveThread: %v", err)
	}
	got, err := LoadThread(paths, in.Frontmatter.ID)
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if !reflect.DeepEqual(got.Frontmatter, in.Frontmatter) {
		t.Fatalf("frontmatter mismatch:\n got: %#v\nwant: %#v", got.Frontmatter, in.Frontmatter)
	}
	if got.Body != in.Body {
		t.Fatalf("body mismatch:\n got: %q\nwant: %q", got.Body, in.Body)
	}
}

func TestLoadThreadMissingFile(t *testing.T) {
	paths := newThreadHome(t)
	_, err := LoadThread(paths, "thr_999")
	if !errors.Is(err, memops.ErrThreadFileNotFound) {
		t.Fatalf("expected memops.ErrThreadFileNotFound; got %v", err)
	}
}

func TestLoadThreadMissingClosingDelimiter(t *testing.T) {
	paths := newThreadHome(t)
	path := ThreadPath(paths, "thr_1")
	if err := os.WriteFile(path, []byte("---\nid: thr_1\nproject: prj_1\nbody-without-close\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThread(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "closing delimiter") {
		t.Fatalf("expected closing-delimiter error; got %v", err)
	}
}

func TestLoadThreadGarbageYAML(t *testing.T) {
	paths := newThreadHome(t)
	path := ThreadPath(paths, "thr_1")
	if err := os.WriteFile(path, []byte("---\nthis is: not: valid: yaml\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThread(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "parse yaml") {
		t.Fatalf("expected yaml parse error; got %v", err)
	}
}

func TestLoadThreadMissingID(t *testing.T) {
	paths := newThreadHome(t)
	path := ThreadPath(paths, "thr_1")
	if err := os.WriteFile(path, []byte("---\nproject: prj_1\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThread(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("expected missing-id error; got %v", err)
	}
}

func TestLoadThreadMissingProject(t *testing.T) {
	paths := newThreadHome(t)
	path := ThreadPath(paths, "thr_1")
	if err := os.WriteFile(path, []byte("---\nid: thr_1\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThread(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected missing-project error; got %v", err)
	}
}

func TestLoadThreadEmptyBody(t *testing.T) {
	paths := newThreadHome(t)
	path := ThreadPath(paths, "thr_1")
	if err := os.WriteFile(path, []byte("---\nid: thr_1\nproject: prj_1\n---\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	thr, err := LoadThread(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Body != "" {
		t.Errorf("empty body expected; got %q", thr.Body)
	}
}

func TestLoadThreadNoOpeningDelimiter(t *testing.T) {
	paths := newThreadHome(t)
	path := ThreadPath(paths, "thr_1")
	if err := os.WriteFile(path, []byte("id: thr_1\nproject: prj_1\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThread(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "opening delimiter") {
		t.Fatalf("expected opening-delimiter error; got %v", err)
	}
}

func TestSaveThreadBodyWithEmbeddedDelimiters(t *testing.T) {
	paths := newThreadHome(t)
	thr := memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID:      "thr_5",
			Project: "prj_1",
			Anchors: []string{"a", "b", "c", "d"},
			Summary: "test",
			State:   memops.ThreadActive,
		},
		Body: "# title\n\nbefore\n\n```\n---\nembedded yaml in code block\n---\n```\n\nafter\n",
	}
	if err := SaveThread(paths, thr); err != nil {
		t.Fatalf("SaveThread: %v", err)
	}
	got, err := LoadThread(paths, thr.Frontmatter.ID)
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if got.Body != thr.Body {
		t.Errorf("body with embedded delimiters lost in round-trip:\n got: %q\nwant: %q", got.Body, thr.Body)
	}
}

func TestSaveThreadAtomicNoTempLeftBehind(t *testing.T) {
	paths := newThreadHome(t)
	thr := sampleThread()
	if err := SaveThread(paths, thr); err != nil {
		t.Fatalf("SaveThread: %v", err)
	}
	// Confirm no .thread-*.tmp files remain.
	entries, err := os.ReadDir(paths.ThreadsDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".thread-") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestSaveThreadStableFieldOrder(t *testing.T) {
	paths := newThreadHome(t)
	thr := sampleThread()
	if err := SaveThread(paths, thr); err != nil {
		t.Fatalf("SaveThread #1: %v", err)
	}
	first, err := os.ReadFile(ThreadPath(paths, thr.Frontmatter.ID))
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if err := SaveThread(paths, thr); err != nil {
		t.Fatalf("SaveThread #2: %v", err)
	}
	second, err := os.ReadFile(ThreadPath(paths, thr.Frontmatter.ID))
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("byte-identical re-save expected; diff:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// Field-order: id should appear before project, before anchors, before history_symbols.
	got := string(first)
	idx := func(s string) int { return strings.Index(got, s) }
	if !(idx("id:") < idx("project:") &&
		idx("project:") < idx("anchors:") &&
		idx("anchors:") < idx("history_symbols:")) {
		t.Errorf("field order is not struct-declaration order:\n%s", got)
	}
}

func TestSaveThreadIdempotentBodyTrailingNewline(t *testing.T) {
	paths := newThreadHome(t)
	// Body without trailing newline; SaveThread should add exactly one.
	thr := memops.Thread{
		Frontmatter: memops.ThreadFrontmatter{
			ID: "thr_7", Project: "prj_1",
			Anchors: []string{"a", "b", "c", "d"}, Summary: "x", State: memops.ThreadActive,
		},
		Body: "# title\n\nno trailing newline",
	}
	if err := SaveThread(paths, thr); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	loaded1, err := LoadThread(paths, thr.Frontmatter.ID)
	if err != nil {
		t.Fatalf("load 1: %v", err)
	}
	if err := SaveThread(paths, loaded1); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	first, _ := os.ReadFile(ThreadPath(paths, thr.Frontmatter.ID))
	loaded2, _ := LoadThread(paths, thr.Frontmatter.ID)
	if err := SaveThread(paths, loaded2); err != nil {
		t.Fatalf("save 3: %v", err)
	}
	second, _ := os.ReadFile(ThreadPath(paths, thr.Frontmatter.ID))
	if string(first) != string(second) {
		t.Errorf("repeated save-load-save should be byte-identical; diff:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestSaveThreadRequiresIDAndProject(t *testing.T) {
	paths := newThreadHome(t)
	if err := SaveThread(paths, memops.Thread{}); err == nil {
		t.Errorf("expected error for empty frontmatter")
	}
	if err := SaveThread(paths, memops.Thread{Frontmatter: memops.ThreadFrontmatter{ID: "thr_1"}}); err == nil {
		t.Errorf("expected error for missing project")
	}
}

func TestThreadPathFormat(t *testing.T) {
	paths := PathsForHome("/tmp/p")
	got := ThreadPath(paths, "thr_42")
	want := filepath.Join("/tmp/p", "threads", "thr_42.md")
	if got != want {
		t.Errorf("ThreadPath: got %q want %q", got, want)
	}
}
