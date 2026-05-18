package store

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"personant/internal/memops"
)

func sampleSpine() []memops.SpineRecord {
	return []memops.SpineRecord{
		{
			ID: "thr_1", Project: "prj_1",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "first thread", State: memops.ThreadActive,
			Created: "2026-05-01T00:00:00Z", LastEngaged: "2026-05-01T00:00:00Z", StateChanged: "2026-05-01T00:00:00Z",
			TurnCount: 1, RecallFires: 0,
		},
		{
			ID: "thr_2", Project: "prj_1",
			Anchors: []string{"epsilon", "zeta", "eta", "theta"},
			Summary: "second thread", State: memops.ThreadWIP,
			Created: "2026-05-02T00:00:00Z", LastEngaged: "2026-05-02T00:00:00Z", StateChanged: "2026-05-02T00:00:00Z",
			TurnCount: 5, RecallFires: 2,
		},
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spine.jsonl")

	in := sampleSpine()
	if err := WriteSpine(path, in); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}

	got, err := ReadSpine(path)
	if err != nil {
		t.Fatalf("ReadSpine: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, in)
	}
}

func TestWriteSortsByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spine.jsonl")

	unsorted := []memops.SpineRecord{
		{ID: "thr_3", Project: "prj_1", Summary: "c"},
		{ID: "thr_1", Project: "prj_1", Summary: "a"},
		{ID: "thr_2", Project: "prj_1", Summary: "b"},
	}
	if err := WriteSpine(path, unsorted); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}
	got, err := ReadSpine(path)
	if err != nil {
		t.Fatalf("ReadSpine: %v", err)
	}
	want := []string{"thr_1", "thr_2", "thr_3"}
	for i, r := range got {
		if r.ID != want[i] {
			t.Fatalf("position %d: got %q, want %q (full order: %v)", i, r.ID, want[i], idsOf(got))
		}
	}
}

func idsOf(rs []memops.SpineRecord) []string {
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}

func TestReadEmptyAndMissing(t *testing.T) {
	dir := t.TempDir()
	// Missing canonical files are treated as empty by ReadSpine /
	// ReadSymbols — semantically equivalent to "no records yet."
	missing := filepath.Join(dir, "nope.jsonl")
	got, err := ReadSpine(missing)
	if err != nil {
		t.Fatalf("ReadSpine on missing path: want nil err, got %v", err)
	}
	if got != nil {
		t.Fatalf("ReadSpine on missing path: want nil slice, got %v", got)
	}
	// Generic ReadJSONL (non-canonical typed wrappers) is still strict —
	// missing file errors so other callers aren't surprised.
	if _, err := ReadJSONL[memops.SpineRecord](missing); err == nil {
		t.Fatal("ReadJSONL on missing path: expected error, got nil")
	}

	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatalf("create empty file: %v", err)
	}
	got, err = ReadSpine(empty)
	if err != nil {
		t.Fatalf("ReadSpine empty: %v", err)
	}
	if got != nil {
		t.Fatalf("ReadSpine empty: want nil slice, got %v", got)
	}

	// File containing only blank lines also returns nil.
	blanks := filepath.Join(dir, "blanks.jsonl")
	if err := os.WriteFile(blanks, []byte("\n\n   \n\n"), 0o644); err != nil {
		t.Fatalf("create blanks file: %v", err)
	}
	got, err = ReadSpine(blanks)
	if err != nil {
		t.Fatalf("ReadSpine blanks: %v", err)
	}
	if got != nil {
		t.Fatalf("ReadSpine blanks: want nil slice, got %v", got)
	}
}

func TestReadMalformedLine(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		content  string
		wantLine string
	}{
		{
			name:     "garbage on second line",
			content:  `{"id":"thr_1","project":"prj_1","anchors":[],"summary":"","state":"active","created":"","last_engaged":"","state_changed":"","turn_count":0,"recall_fires":0}` + "\n" + `not json`,
			wantLine: ":2:",
		},
		{
			name:     "trailing content after object",
			content:  `{"id":"thr_1","project":"prj_1","anchors":[],"summary":"","state":"active","created":"","last_engaged":"","state_changed":"","turn_count":0,"recall_fires":0} extra`,
			wantLine: ":1:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".jsonl")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("setup: %v", err)
			}
			_, err := ReadSpine(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantLine) {
				t.Fatalf("error %q does not contain line marker %q", err.Error(), tc.wantLine)
			}
		})
	}
}

func TestWriteDuplicateID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spine.jsonl")
	dup := []memops.SpineRecord{
		{ID: "thr_1", Project: "prj_1", Summary: "first"},
		{ID: "thr_1", Project: "prj_1", Summary: "second"},
	}
	err := WriteSpine(path, dup)
	if err == nil {
		t.Fatal("WriteSpine with duplicate id: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("error %q does not mention duplicate key", err.Error())
	}
	// No output file should exist.
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output file should not exist after duplicate-key rejection; stat err=%v", statErr)
	}
	// No leftover temp files in the directory either.
	leftovers := tempLeftovers(t, dir)
	if len(leftovers) != 0 {
		t.Fatalf("unexpected leftover temp files: %v", leftovers)
	}
}

func TestWriteEncodeFailureCleansUp(t *testing.T) {
	// json.Marshal cannot encode a chan; use a payload type that includes one
	// to force an encode error mid-write. This proves the temp-file cleanup
	// path runs.
	type bad struct {
		ID  string   `json:"id"`
		Bad chan int `json:"bad"`
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	recs := []bad{{ID: "k1", Bad: make(chan int)}}
	err := WriteJSONL(path, recs, func(b bad) string { return b.ID })
	if err == nil {
		t.Fatal("expected encode error, got nil")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output file should not exist after encode failure; stat err=%v", statErr)
	}
	leftovers := tempLeftovers(t, dir)
	if len(leftovers) != 0 {
		t.Fatalf("unexpected leftover temp files after encode failure: %v", leftovers)
	}
}

func TestWriteAtomicTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spine.jsonl")
	if err := WriteSpine(path, sampleSpine()); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("file does not end with newline: %q", string(data))
	}
	// Sanity: line count == record count.
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	n := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			n++
		}
	}
	if n != len(sampleSpine()) {
		t.Fatalf("line count: got %d, want %d", n, len(sampleSpine()))
	}
}

func tempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".jsonl-") && strings.HasSuffix(name, ".tmp") {
			out = append(out, name)
		}
	}
	return out
}
