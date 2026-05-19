package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

func sampleFrontmatter() memops.ThreadFrontmatter {
	return memops.ThreadFrontmatter{
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
	}
}

// turnExcerpt renders a plausible turn-excerpt block for turn n.
func turnExcerpt(n int) string {
	return fmt.Sprintf("## Turn %d\n\n**user:** prompt %d\n\n**agent:** reply %d\n", n, n, n)
}

func TestSaveLoadFrontmatterRoundTrip(t *testing.T) {
	paths := newThreadHome(t)
	in := sampleFrontmatter()

	if err := SaveThreadFrontmatter(paths, in.ID, in); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	got, err := LoadThreadFrontmatter(paths, in.ID)
	if err != nil {
		t.Fatalf("LoadThreadFrontmatter: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("frontmatter mismatch:\n got: %#v\nwant: %#v", got, in)
	}
}

func TestSaveThreadFrontmatterWritesTitle(t *testing.T) {
	paths := newThreadHome(t)
	in := sampleFrontmatter()
	if err := SaveThreadFrontmatter(paths, in.ID, in); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	data, err := os.ReadFile(ThreadMetaPath(paths, in.ID))
	if err != nil {
		t.Fatalf("read thread.md: %v", err)
	}
	if !strings.Contains(string(data), "# "+in.Summary) {
		t.Errorf("thread.md missing title line derived from summary:\n%s", data)
	}
}

func TestSaveThreadFrontmatterTitleFallsBackToID(t *testing.T) {
	paths := newThreadHome(t)
	in := sampleFrontmatter()
	in.Summary = ""
	if err := SaveThreadFrontmatter(paths, in.ID, in); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	data, err := os.ReadFile(ThreadMetaPath(paths, in.ID))
	if err != nil {
		t.Fatalf("read thread.md: %v", err)
	}
	if !strings.Contains(string(data), "# "+in.ID) {
		t.Errorf("thread.md missing ID-fallback title:\n%s", data)
	}
}

func TestLoadThreadFrontmatterMissing(t *testing.T) {
	paths := newThreadHome(t)
	_, err := LoadThreadFrontmatter(paths, "thr_999")
	if !errors.Is(err, memops.ErrThreadFileNotFound) {
		t.Fatalf("expected memops.ErrThreadFileNotFound; got %v", err)
	}
}

func TestLoadThreadFrontmatterMissingClosingDelimiter(t *testing.T) {
	paths := newThreadHome(t)
	dir := ThreadDir(paths, "thr_1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ThreadMetaPath(paths, "thr_1"), []byte("---\nid: thr_1\nproject: prj_1\nno-close\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThreadFrontmatter(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "closing delimiter") {
		t.Fatalf("expected closing-delimiter error; got %v", err)
	}
}

func TestLoadThreadFrontmatterMissingID(t *testing.T) {
	paths := newThreadHome(t)
	dir := ThreadDir(paths, "thr_1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ThreadMetaPath(paths, "thr_1"), []byte("---\nproject: prj_1\n---\n# t\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThreadFrontmatter(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("expected missing-id error; got %v", err)
	}
}

func TestLoadThreadFrontmatterMissingProject(t *testing.T) {
	paths := newThreadHome(t)
	dir := ThreadDir(paths, "thr_1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ThreadMetaPath(paths, "thr_1"), []byte("---\nid: thr_1\n---\n# t\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadThreadFrontmatter(paths, "thr_1")
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected missing-project error; got %v", err)
	}
}

// TestAppendThreadTurnAndReadBody exercises the core append + assemble
// round-trip: excerpts are written one per file and ReadThreadBody
// reassembles them in chronological order.
func TestAppendThreadTurnAndReadBody(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}

	for n := 1; n <= 3; n++ {
		if err := AppendThreadTurn(paths, fm.ID, n, turnExcerpt(n)); err != nil {
			t.Fatalf("AppendThreadTurn %d: %v", n, err)
		}
	}
	body, err := ReadThreadBody(paths, fm.ID, 0)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	// Chronological order: turn 1 before 2 before 3.
	i1, i2, i3 := strings.Index(body, "Turn 1"), strings.Index(body, "Turn 2"), strings.Index(body, "Turn 3")
	if !(i1 >= 0 && i1 < i2 && i2 < i3) {
		t.Errorf("body not chronological:\n%s", body)
	}
}

func TestReadThreadBodyEmptyWhenNoTurns(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	body, err := ReadThreadBody(paths, fm.ID, 0)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if body != "" {
		t.Errorf("expected empty body; got %q", body)
	}
}

// TestReadThreadBodyBudgetStopsEarly: with a small budget, ReadThreadBody
// reads newest-first and stops, so only the most recent excerpt(s)
// appear.
func TestReadThreadBodyBudgetStopsEarly(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	for n := 1; n <= 10; n++ {
		if err := AppendThreadTurn(paths, fm.ID, n, turnExcerpt(n)); err != nil {
			t.Fatalf("AppendThreadTurn %d: %v", n, err)
		}
	}
	// Budget large enough for only the single newest excerpt.
	budget := len(turnExcerpt(10)) - 1
	body, err := ReadThreadBody(paths, fm.ID, budget)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if !strings.Contains(body, "Turn 10") {
		t.Errorf("budgeted body missing newest turn:\n%s", body)
	}
	if strings.Contains(body, "Turn 1\n") {
		t.Errorf("budgeted body should not reach turn 1:\n%s", body)
	}
}

// TestAppendThreadTurnFIFOEviction: appending past ThreadTurnWindow
// evicts the lowest-numbered excerpts.
func TestAppendThreadTurnFIFOEviction(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	total := ThreadTurnWindow + 5
	for n := 1; n <= total; n++ {
		if err := AppendThreadTurn(paths, fm.ID, n, turnExcerpt(n)); err != nil {
			t.Fatalf("AppendThreadTurn %d: %v", n, err)
		}
	}
	nums, err := turnFileNumbers(ThreadTurnsDir(paths, fm.ID))
	if err != nil {
		t.Fatalf("turnFileNumbers: %v", err)
	}
	if len(nums) != ThreadTurnWindow {
		t.Fatalf("retained %d turn files, want %d", len(nums), ThreadTurnWindow)
	}
	// The oldest 5 must be gone; the lowest retained is turn 6.
	if nums[0] != total-ThreadTurnWindow+1 {
		t.Errorf("lowest retained turn = %d, want %d", nums[0], total-ThreadTurnWindow+1)
	}
	if nums[len(nums)-1] != total {
		t.Errorf("highest retained turn = %d, want %d", nums[len(nums)-1], total)
	}
}

func TestAppendThreadTurnEmptyExcerptIsNoOp(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	if err := AppendThreadTurn(paths, fm.ID, 1, ""); err != nil {
		t.Fatalf("AppendThreadTurn empty: %v", err)
	}
	body, err := ReadThreadBody(paths, fm.ID, 0)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if body != "" {
		t.Errorf("empty-excerpt append wrote a turn file; body=%q", body)
	}
}

func TestLoadThreadAssemblesBody(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	if err := AppendThreadTurn(paths, fm.ID, 1, turnExcerpt(1)); err != nil {
		t.Fatalf("AppendThreadTurn: %v", err)
	}
	thr, err := LoadThread(paths, fm.ID)
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if thr.Frontmatter.ID != fm.ID {
		t.Errorf("frontmatter ID = %q, want %q", thr.Frontmatter.ID, fm.ID)
	}
	if !strings.Contains(thr.Body, "Turn 1") {
		t.Errorf("assembled body missing turn 1:\n%s", thr.Body)
	}
}

func TestSeedThreadSplitsBodyIntoTurns(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	fm.ID = "thr_1"
	thr := memops.Thread{
		Frontmatter: fm,
		Body:        "# title\n\n" + turnExcerpt(7) + "\n" + turnExcerpt(8),
	}
	if err := SeedThread(paths, thr); err != nil {
		t.Fatalf("SeedThread: %v", err)
	}
	nums, err := turnFileNumbers(ThreadTurnsDir(paths, fm.ID))
	if err != nil {
		t.Fatalf("turnFileNumbers: %v", err)
	}
	if !reflect.DeepEqual(nums, []int{7, 8}) {
		t.Errorf("seeded turn numbers = %v, want [7 8]", nums)
	}
	body, err := ReadThreadBody(paths, fm.ID, 0)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if strings.Contains(body, "# title") {
		t.Errorf("title leaked into turn body:\n%s", body)
	}
}

func TestSaveThreadFrontmatterStableFieldOrder(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("save #1: %v", err)
	}
	first, err := os.ReadFile(ThreadMetaPath(paths, fm.ID))
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("save #2: %v", err)
	}
	second, err := os.ReadFile(ThreadMetaPath(paths, fm.ID))
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("byte-identical re-save expected; diff:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	got := string(first)
	idx := func(s string) int { return strings.Index(got, s) }
	if !(idx("id:") < idx("project:") &&
		idx("project:") < idx("anchors:") &&
		idx("anchors:") < idx("history_symbols:")) {
		t.Errorf("field order is not struct-declaration order:\n%s", got)
	}
}

func TestSaveThreadFrontmatterAtomicNoTempLeftBehind(t *testing.T) {
	paths := newThreadHome(t)
	fm := sampleFrontmatter()
	if err := SaveThreadFrontmatter(paths, fm.ID, fm); err != nil {
		t.Fatalf("SaveThreadFrontmatter: %v", err)
	}
	entries, err := os.ReadDir(ThreadDir(paths, fm.ID))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".thread-meta-") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestSaveThreadFrontmatterRequiresIDAndProject(t *testing.T) {
	paths := newThreadHome(t)
	if err := SaveThreadFrontmatter(paths, "", memops.ThreadFrontmatter{}); err == nil {
		t.Errorf("expected error for empty id")
	}
	if err := SaveThreadFrontmatter(paths, "thr_1", memops.ThreadFrontmatter{ID: "thr_1"}); err == nil {
		t.Errorf("expected error for missing project")
	}
}

func TestThreadPathFormats(t *testing.T) {
	paths := PathsForHome("/tmp/p")
	if got, want := ThreadDir(paths, "thr_42"), filepath.Join("/tmp/p", "threads", "thr_42"); got != want {
		t.Errorf("ThreadDir: got %q want %q", got, want)
	}
	if got, want := ThreadMetaPath(paths, "thr_42"), filepath.Join("/tmp/p", "threads", "thr_42", "thread.md"); got != want {
		t.Errorf("ThreadMetaPath: got %q want %q", got, want)
	}
	if got, want := ThreadTurnsDir(paths, "thr_42"), filepath.Join("/tmp/p", "threads", "thr_42", "turns"); got != want {
		t.Errorf("ThreadTurnsDir: got %q want %q", got, want)
	}
}

func TestListThreadIDsIgnoresLooseFiles(t *testing.T) {
	paths := newThreadHome(t)
	for _, id := range []string{"thr_2", "thr_1"} {
		if err := os.MkdirAll(ThreadDir(paths, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(paths.ThreadsDir, "loose.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ListThreadIDs(paths)
	if err != nil {
		t.Fatalf("ListThreadIDs: %v", err)
	}
	if !sort.StringsAreSorted(got) || !reflect.DeepEqual(got, []string{"thr_1", "thr_2"}) {
		t.Errorf("ListThreadIDs = %v, want [thr_1 thr_2]", got)
	}
}
