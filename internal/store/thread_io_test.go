package store

import (
	"encoding/json"
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

func sampleFrontmatter() memops.ThreadMeta {
	return memops.ThreadMeta{
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

// TestNewLifecycleFieldsRoundTrip — anchor-lifecycle Inc 1 acceptance
// gate. The new HistorySymbol fields (Lifecycle/EverCentral/
// LastActiveTurn) survive the YAML frontmatter round-trip with non-zero
// values, and the new SpineRecord.AnchorsProjectedAtTurn survives the
// JSONL round-trip. The fields are inert this increment — the test only
// proves they serialize and decode, not that anything consumes them.
func TestNewLifecycleFieldsRoundTrip(t *testing.T) {
	paths := newThreadHome(t)
	in := sampleFrontmatter()
	in.HistorySymbols = []memops.HistorySymbol{
		{Raw: "active sym", Normalized: "active-sym", FirstSeenTurn: 10, Count: 5, Source: memops.SourceModel, Lifecycle: memops.LifecycleActive, EverCentral: true, LastActiveTurn: 24},
		{Raw: "abandoned sym", Normalized: "abandoned-sym", FirstSeenTurn: 3, Count: 9, Source: memops.SourceUser, Lifecycle: memops.LifecycleSuperseded, EverCentral: true, LastActiveTurn: 12},
	}

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

	// SpineRecord JSONL round-trip of the new watermark field.
	spineIn := memops.SpineRecord{ID: "thr_42", Project: "prj_3", Anchors: []string{"a"}, State: memops.ThreadActive, AnchorsProjectedAtTurn: 151}
	b, err := json.Marshal(spineIn)
	if err != nil {
		t.Fatalf("marshal spine: %v", err)
	}
	var spineOut memops.SpineRecord
	if err := json.Unmarshal(b, &spineOut); err != nil {
		t.Fatalf("unmarshal spine: %v", err)
	}
	if spineOut.AnchorsProjectedAtTurn != 151 {
		t.Errorf("AnchorsProjectedAtTurn: got %d want 151", spineOut.AnchorsProjectedAtTurn)
	}
}

// TestNewLifecycleFieldsZeroValueSemantics — an old-shape record (no
// lifecycle fields written) decodes with the zero-value semantics the
// design relies on: Lifecycle "" ≡ active, EverCentral false,
// LastActiveTurn 0, AnchorsProjectedAtTurn 0. This is the
// greenfield-posture guarantee (SOLUTION §8): no migration shim needed.
func TestNewLifecycleFieldsZeroValueSemantics(t *testing.T) {
	// A frontmatter whose history symbols carry only the legacy fields.
	const legacy = `id: thr_7
project: prj_1
anchors: []
summary: ""
state: active
description: ""
created: ""
last_engaged: ""
state_changed: ""
turn_count: 0
recall_fires: 0
last_engaged_turn: 0
history_symbols:
  - raw: legacy
    normalized: legacy
    first_seen_turn: 1
    count: 2
    source: model
`
	paths := newThreadHome(t)
	dir := filepath.Dir(ThreadMetaPath(paths, "thr_7"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := thrFrontmatterDelimiter + "\n" + legacy + thrFrontmatterDelimiter + "\n# thr_7\n"
	if err := os.WriteFile(ThreadMetaPath(paths, "thr_7"), []byte(body), 0o644); err != nil {
		t.Fatalf("write thread.md: %v", err)
	}

	fm, err := LoadThreadFrontmatter(paths, "thr_7")
	if err != nil {
		t.Fatalf("LoadThreadFrontmatter: %v", err)
	}
	if len(fm.HistorySymbols) != 1 {
		t.Fatalf("history symbols: got %d want 1", len(fm.HistorySymbols))
	}
	s := fm.HistorySymbols[0]
	if s.Lifecycle != "" {
		t.Errorf("Lifecycle: got %q want \"\" (≡ active)", s.Lifecycle)
	}
	if s.Lifecycle != "" && s.Lifecycle != memops.LifecycleActive {
		t.Errorf("zero-value lifecycle must be active-equivalent, got %q", s.Lifecycle)
	}
	if s.EverCentral {
		t.Errorf("EverCentral: got true want false")
	}
	if s.LastActiveTurn != 0 {
		t.Errorf("LastActiveTurn: got %d want 0", s.LastActiveTurn)
	}
}

// TestDerivedFromRoundTrip — §2.7.3 origin provenance. A history symbol
// carrying DerivedFrom survives the YAML frontmatter round-trip AND the
// HistorySymbol JSON round-trip with its (sorted) origin set intact; a
// symbol with no DerivedFrom decodes to nil (omitted in YAML/JSON via
// omitempty). The field is honest provenance metadata — this proves it
// serializes, nothing more.
func TestDerivedFromRoundTrip(t *testing.T) {
	paths := newThreadHome(t)
	in := sampleFrontmatter()
	in.HistorySymbols = []memops.HistorySymbol{
		{Raw: "carried", Normalized: "carried", FirstSeenTurn: 5, Count: 3, Source: memops.SourceModel, DerivedFrom: []string{"thr_1", "thr_9"}},
		{Raw: "organic", Normalized: "organic", FirstSeenTurn: 6, Count: 1, Source: memops.SourceUser},
	}

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
	// The organic symbol decodes to nil (omitted), not an empty non-nil slice.
	if got.HistorySymbols[1].DerivedFrom != nil {
		t.Errorf("organic symbol DerivedFrom: got %#v want nil", got.HistorySymbols[1].DerivedFrom)
	}

	// HistorySymbol JSON round-trip of the same field.
	b, err := json.Marshal(in.HistorySymbols[0])
	if err != nil {
		t.Fatalf("marshal symbol: %v", err)
	}
	var symOut memops.HistorySymbol
	if err := json.Unmarshal(b, &symOut); err != nil {
		t.Fatalf("unmarshal symbol: %v", err)
	}
	if !reflect.DeepEqual(symOut.DerivedFrom, []string{"thr_1", "thr_9"}) {
		t.Errorf("JSON DerivedFrom: got %#v want [thr_1 thr_9]", symOut.DerivedFrom)
	}
	// An old record without derived_from decodes to nil.
	if !strings.Contains(string(b), "derived_from") {
		t.Errorf("expected derived_from key in JSON: %s", b)
	}
	var noField memops.HistorySymbol
	if err := json.Unmarshal([]byte(`{"raw":"x","normalized":"x","count":1,"source":"model"}`), &noField); err != nil {
		t.Fatalf("unmarshal legacy symbol: %v", err)
	}
	if noField.DerivedFrom != nil {
		t.Errorf("legacy symbol DerivedFrom: got %#v want nil", noField.DerivedFrom)
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

// TestAppendThreadTurnRetainsPastWindow: appending past ThreadTurnWindow
// RETAINS every excerpt on disk — retention is decoupled from the
// assembly window per SPEC §2.3. The window now bounds only what
// ReadThreadBody assembles into context (see TestReadThreadBodyWindowsOnRead);
// the FIFO no longer deletes, so the retained set is the durable source
// the §3.4 chunk index embeds for intra-thread recall of early content.
func TestAppendThreadTurnRetainsPastWindow(t *testing.T) {
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
	// Every excerpt is retained — nothing evicted.
	if len(nums) != total {
		t.Fatalf("retained %d turn files, want %d (all retained, no FIFO delete)", len(nums), total)
	}
	if nums[0] != 1 {
		t.Errorf("lowest retained turn = %d, want 1 (earliest excerpt kept past window)", nums[0])
	}
	if nums[len(nums)-1] != total {
		t.Errorf("highest retained turn = %d, want %d", nums[len(nums)-1], total)
	}
}

// TestReadThreadBodyWindowsOnRead: with more excerpts retained than the
// assembly window holds, ReadThreadBody (no byte budget) reads only the
// most-recent ThreadTurnWindow excerpts into the body — assembly is
// windowed on read while the older excerpts stay retained on disk
// (SPEC §2.3). This is the behavior the old FIFO-delete produced for the
// assembled context, now achieved by read-windowing instead of deletion.
func TestReadThreadBodyWindowsOnRead(t *testing.T) {
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
	body, err := ReadThreadBody(paths, fm.ID, 0)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	// turnExcerpt(n) renders a unique "prompt <n>" / "reply <n>" pair; use
	// "reply <n>\n" as the per-turn presence marker (the trailing newline
	// disambiguates "reply 6" from "reply 60").
	marker := func(n int) string { return fmt.Sprintf("reply %d\n", n) }
	// The newest excerpt is in the assembled body; the lowest in-window
	// excerpt is turn total-ThreadTurnWindow+1 (== 6 here).
	if !strings.Contains(body, marker(total)) {
		t.Errorf("assembled body missing newest turn %d", total)
	}
	lowestInWindow := total - ThreadTurnWindow + 1
	if !strings.Contains(body, marker(lowestInWindow)) {
		t.Errorf("assembled body missing lowest in-window turn %d", lowestInWindow)
	}
	// Excerpts older than the window are retained on disk but NOT assembled.
	for _, scrolled := range []int{1, lowestInWindow - 1} {
		if strings.Contains(body, marker(scrolled)) {
			t.Errorf("scrolled-out turn %d should not be in assembled body", scrolled)
		}
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
	if thr.Meta.ID != fm.ID {
		t.Errorf("frontmatter ID = %q, want %q", thr.Meta.ID, fm.ID)
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
		Meta: fm,
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
	if err := SaveThreadFrontmatter(paths, "", memops.ThreadMeta{}); err == nil {
		t.Errorf("expected error for empty id")
	}
	if err := SaveThreadFrontmatter(paths, "thr_1", memops.ThreadMeta{ID: "thr_1"}); err == nil {
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
