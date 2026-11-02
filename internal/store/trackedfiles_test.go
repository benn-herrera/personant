package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"personant/internal/crashpoint"
	"personant/internal/dedup"
	"personant/internal/memops"
)

const fileStoreThread = "thr_1"

var fileStoreZone = time.FixedZone("", -7*3600)

func fileStoreAt(turn int) time.Time {
	return time.Date(2026, 5, 9, 4, turn, 0, 0, fileStoreZone)
}

// numberedLines returns n lines "line <i>", with line changed replaced.
func numberedLines(n, changed int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i == changed {
			fmt.Fprintf(&b, "changed %d\n", i)
			continue
		}
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func applyEvents(t *testing.T, paths PersonantPaths, turn int, events ...memops.FileEvent) []string {
	t.Helper()
	written, err := ApplyFileEvents(paths, fileStoreThread, turn, fileStoreAt(turn), events)
	if err != nil {
		t.Fatalf("ApplyFileEvents turn %d: %v", turn, err)
	}
	return written
}

func writeEvent(path, content string) memops.FileEvent {
	return memops.FileEvent{Kind: memops.FileEventWrite, Path: path, Content: content}
}

func readIndex(t *testing.T, paths PersonantPaths) []dedup.FileEntry {
	t.Helper()
	entries, err := ReadTrackedIndex(paths, fileStoreThread)
	if err != nil {
		t.Fatalf("ReadTrackedIndex: %v", err)
	}
	return entries
}

// storeFiles lists the names under the thread's files/ directory.
func storeFiles(t *testing.T, paths PersonantPaths) []string {
	t.Helper()
	des, err := os.ReadDir(filepath.Join(ThreadDir(paths, fileStoreThread), trackedFilesDirName))
	if err != nil {
		t.Fatalf("read files dir: %v", err)
	}
	var names []string
	for _, d := range des {
		names = append(names, d.Name())
	}
	return names
}

func TestFileStoreFirstWriteCreatesEntry(t *testing.T) {
	paths := newThreadHome(t)
	content := "hello\n"
	id := dedup.ContentID([]byte(content))

	written := applyEvents(t, paths, 1, writeEvent("a.go", content))

	want := []string{TrackedBlobRel(fileStoreThread, id), TrackedIndexRel(fileStoreThread)}
	if !reflect.DeepEqual(written, want) {
		t.Errorf("written = %v, want %v", written, want)
	}
	if got := storeFiles(t, paths); !reflect.DeepEqual(got, []string{id}) {
		t.Errorf("files/ = %v, want [%s]", got, id)
	}
	raw, err := os.ReadFile(TrackedIndexPath(paths, fileStoreThread))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	wantLine := fmt.Sprintf(`{"path":"a.go","id":"%s","size":6,"turn":1,"at":"2026-05-09T04:01:00-07:00","changes":[]}`+"\n", id)
	if string(raw) != wantLine {
		t.Errorf("index =\n%s\nwant\n%s", raw, wantLine)
	}
	blob, err := ReadTrackedBlob(paths, fileStoreThread, id)
	if err != nil || string(blob) != content {
		t.Errorf("ReadTrackedBlob = %q, %v", blob, err)
	}
}

func TestFileStoreChangeAddsDiff(t *testing.T) {
	paths := newThreadHome(t)
	v1, v2 := numberedLines(40, 0), numberedLines(40, 20)
	id1, id2 := dedup.ContentID([]byte(v1)), dedup.ContentID([]byte(v2))
	applyEvents(t, paths, 1, writeEvent("a.go", v1))

	written := applyEvents(t, paths, 2, writeEvent("a.go", v2))

	want := []string{TrackedDiffRel(fileStoreThread, id1, id2), TrackedBlobRel(fileStoreThread, id2), TrackedIndexRel(fileStoreThread)}
	if !reflect.DeepEqual(written, want) {
		t.Errorf("written = %v, want %v", written, want)
	}
	diff, err := os.ReadFile(TrackedDiffPath(paths, fileStoreThread, id1, id2))
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	wantDiff, kept := dedup.Diff(v1, v2)
	if !kept || string(diff) != wantDiff {
		t.Errorf("diff file = %q (kept %v), want %q", diff, kept, wantDiff)
	}
	entries := readIndex(t, paths)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.ID != id2 || e.Size != len(v2) || e.Turn != 2 || !e.At.Equal(fileStoreAt(2)) {
		t.Errorf("entry = %+v", e)
	}
	wantChange := dedup.FileChange{Turn: 2, At: fileStoreAt(2), From: id1, To: id2, Diff: true, Size: len(diff)}
	if len(e.Changes) != 1 || !e.Changes[0].At.Equal(wantChange.At) {
		t.Fatalf("changes = %+v", e.Changes)
	}
	e.Changes[0].At = wantChange.At
	if e.Changes[0] != wantChange {
		t.Errorf("change = %+v, want %+v", e.Changes[0], wantChange)
	}
}

func TestFileStoreUnchangedReadMovesOnlyTurnAndAt(t *testing.T) {
	paths := newThreadHome(t)
	v1 := numberedLines(10, 0)
	id1 := dedup.ContentID([]byte(v1))
	applyEvents(t, paths, 1, writeEvent("a.go", v1))

	written := applyEvents(t, paths, 3, memops.FileEvent{Kind: memops.FileEventRead, Path: "a.go", Content: v1})

	if want := []string{TrackedIndexRel(fileStoreThread)}; !reflect.DeepEqual(written, want) {
		t.Errorf("written = %v, want %v", written, want)
	}
	e := readIndex(t, paths)[0]
	if e.ID != id1 || e.Size != len(v1) || e.Turn != 3 || !e.At.Equal(fileStoreAt(3)) || len(e.Changes) != 0 {
		t.Errorf("entry = %+v", e)
	}
}

func TestFileStoreChangesTrimToWindow(t *testing.T) {
	paths := newThreadHome(t)
	versions := dedup.LiveDiffWindow + 3
	var ids []string
	for i := 0; i < versions; i++ {
		v := numberedLines(40, i)
		ids = append(ids, dedup.ContentID([]byte(v)))
		applyEvents(t, paths, i+1, writeEvent("a.go", v))
	}
	changes := readIndex(t, paths)[0].Changes
	if len(changes) != dedup.LiveDiffWindow {
		t.Fatalf("changes = %d, want %d", len(changes), dedup.LiveDiffWindow)
	}
	for k, c := range changes {
		j := versions - dedup.LiveDiffWindow + k
		if c.From != ids[j-1] || c.To != ids[j] || c.Turn != j+1 {
			t.Errorf("change %d = %+v, want %s -> %s at turn %d", k, c, ids[j-1], ids[j], j+1)
		}
	}
}

func TestFileStoreSkippedDiffRecordsNoFile(t *testing.T) {
	paths := newThreadHome(t)
	v1, v2 := "alpha\nbeta\n", "gamma\ndelta\n"
	if _, kept := dedup.Diff(v1, v2); kept {
		t.Fatal("fixture diff is kept; the test needs a skipped one")
	}
	applyEvents(t, paths, 1, writeEvent("a.go", v1))

	written := applyEvents(t, paths, 2, writeEvent("a.go", v2))

	for _, w := range written {
		if strings.HasSuffix(w, ".diff") {
			t.Errorf("wrote diff %s", w)
		}
	}
	for _, name := range storeFiles(t, paths) {
		if strings.HasSuffix(name, ".diff") {
			t.Errorf("files/ holds diff %s", name)
		}
	}
	c := readIndex(t, paths)[0].Changes
	if len(c) != 1 || c[0].Diff || c[0].Size != 0 {
		t.Errorf("changes = %+v, want one with diff false, size 0", c)
	}
}

func TestFileStoreMissingIndexIsEmpty(t *testing.T) {
	paths := newThreadHome(t)
	entries, err := ReadTrackedIndex(paths, fileStoreThread)
	if err != nil || entries != nil {
		t.Fatalf("ReadTrackedIndex = %v, %v; want nil, nil", entries, err)
	}
}

func TestFileStoreIndexOrderEnforced(t *testing.T) {
	id := dedup.ContentID([]byte("x"))
	line := func(path string) string {
		return fmt.Sprintf(`{"path":%q,"id":"%s","size":1,"turn":1,"at":"2026-05-09T04:00:00Z","changes":[]}`+"\n", path, id)
	}
	tests := []struct {
		name, index, wantErr string
	}{
		{"unsorted", line("b") + line("a"), "sorts before"},
		{"duplicate", line("a") + line("a"), "duplicate path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			paths := newThreadHome(t)
			if err := os.MkdirAll(ThreadDir(paths, fileStoreThread), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(TrackedIndexPath(paths, fileStoreThread), []byte(tc.index), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ReadTrackedIndex(paths, fileStoreThread)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ReadTrackedIndex err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestFileStoreTamperedBlobFailsRead(t *testing.T) {
	paths := newThreadHome(t)
	id := dedup.ContentID([]byte("hello\n"))
	applyEvents(t, paths, 1, writeEvent("a.go", "hello\n"))
	if err := os.WriteFile(TrackedBlobPath(paths, fileStoreThread, id), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadTrackedBlob(paths, fileStoreThread, id)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadTrackedBlob err = %v, want a hash mismatch", err)
	}
}

// assertIndexedFilesPresent fails for each blob or kept diff the index
// names that is absent.
func assertIndexedFilesPresent(t *testing.T, paths PersonantPaths) {
	t.Helper()
	for _, e := range readIndex(t, paths) {
		if _, err := os.Stat(TrackedBlobPath(paths, fileStoreThread, e.ID)); err != nil {
			t.Errorf("blob of %s: %v", e.Path, err)
		}
		for _, c := range e.Changes {
			if !c.Diff {
				continue
			}
			if _, err := os.Stat(TrackedDiffPath(paths, fileStoreThread, c.From, c.To)); err != nil {
				t.Errorf("diff of %s: %v", e.Path, err)
			}
		}
	}
}

func indexPaths(t *testing.T, paths PersonantPaths) []string {
	t.Helper()
	var out []string
	for _, e := range readIndex(t, paths) {
		out = append(out, e.Path)
	}
	return out
}

func writeLegacySidecar(t *testing.T, paths PersonantPaths) string {
	t.Helper()
	legacy := filepath.Join(ThreadDir(paths, fileStoreThread), threadFilesFileName)
	if err := os.WriteFile(legacy, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func TestPruneTrackedFilesKeepsCurrentBlobAndWindowDiffs(t *testing.T) {
	paths := newThreadHome(t)
	edits := dedup.LiveDiffWindow + 4
	for i := 0; i < edits; i++ {
		applyEvents(t, paths, i+1, writeEvent("a.go", numberedLines(40, i)))
	}
	legacy := writeLegacySidecar(t, paths)
	indexBefore, err := os.ReadFile(TrackedIndexPath(paths, fileStoreThread))
	if err != nil {
		t.Fatal(err)
	}
	filesBefore := len(storeFiles(t, paths))

	got, err := PruneTrackedFiles(paths, fileStoreThread, edits)
	if err != nil {
		t.Fatalf("PruneTrackedFiles: %v", err)
	}

	e := readIndex(t, paths)[0]
	want := []string{e.ID}
	for _, c := range e.Changes {
		if !c.Diff {
			t.Fatalf("fixture change %+v kept no diff", c)
		}
		want = append(want, trackedDiffName(c.From, c.To))
	}
	slices.Sort(want)
	if files := storeFiles(t, paths); !reflect.DeepEqual(files, want) {
		t.Errorf("files/ = %v, want %v", files, want)
	}
	if indexAfter, _ := os.ReadFile(TrackedIndexPath(paths, fileStoreThread)); string(indexAfter) != string(indexBefore) {
		t.Errorf("index rewritten with no entry dropped")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("files.json after prune: stat err %v, want absent", err)
	}
	if wantFiles := filesBefore - len(want) + 1; got.Files != wantFiles || got.Bytes <= 0 || got.Skip != "" {
		t.Errorf("prune = %+v, want %d files removed", got, wantFiles)
	}
	if again, err := PruneTrackedFiles(paths, fileStoreThread, edits); err != nil || again != (TrackedPrune{}) {
		t.Errorf("second prune = %+v, %v; want nothing", again, err)
	}
}

// pastWindowFixture holds entries at turn 1 (old.go, and shared-old.go
// whose content shared-new.go also holds) and at turn 10, and returns a
// turn count that keeps only the turn-10 entries.
func pastWindowFixture(t *testing.T, paths PersonantPaths) int {
	t.Helper()
	applyEvents(t, paths, 1, writeEvent("old.go", "old\n"), writeEvent("shared-old.go", "shared\n"))
	applyEvents(t, paths, 10, writeEvent("new.go", "new\n"), writeEvent("shared-new.go", "shared\n"))
	return ThreadTurnWindow + 5
}

func TestPruneTrackedFilesDropsEntriesPastWindow(t *testing.T) {
	paths := newThreadHome(t)
	turnCount := pastWindowFixture(t, paths)

	got, err := PruneTrackedFiles(paths, fileStoreThread, turnCount)
	if err != nil {
		t.Fatalf("PruneTrackedFiles: %v", err)
	}

	if p := indexPaths(t, paths); !reflect.DeepEqual(p, []string{"new.go", "shared-new.go"}) {
		t.Errorf("index paths = %v, want [new.go shared-new.go]", p)
	}
	want := []string{dedup.ContentID([]byte("new\n")), dedup.ContentID([]byte("shared\n"))}
	slices.Sort(want)
	if files := storeFiles(t, paths); !reflect.DeepEqual(files, want) {
		t.Errorf("files/ = %v, want %v", files, want)
	}
	if got.Files != 1 || got.Bytes != int64(len("old\n")) {
		t.Errorf("prune = %+v, want old.go's blob removed", got)
	}
}

func TestPruneTrackedFilesUnreadableIndexDeletesNothing(t *testing.T) {
	paths := newThreadHome(t)
	turnCount := pastWindowFixture(t, paths)
	legacy := writeLegacySidecar(t, paths)
	index := TrackedIndexPath(paths, fileStoreThread)
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	broken := string(raw) + "not json\n"
	if err := os.WriteFile(index, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	filesBefore := storeFiles(t, paths)

	got, err := PruneTrackedFiles(paths, fileStoreThread, turnCount)
	if err != nil || got != (TrackedPrune{Skip: PruneUnreadable}) {
		t.Fatalf("PruneTrackedFiles = %+v, %v; want skip %q", got, err, PruneUnreadable)
	}
	if files := storeFiles(t, paths); !reflect.DeepEqual(files, filesBefore) {
		t.Errorf("files/ = %v, want untouched %v", files, filesBefore)
	}
	if after, _ := os.ReadFile(index); string(after) != broken {
		t.Errorf("unreadable index rewritten")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("files.json removed from a skipped thread: %v", err)
	}
}

func TestPruneTrackedFilesCrashAfterIndexConverges(t *testing.T) {
	paths := newThreadHome(t)
	turnCount := pastWindowFixture(t, paths)
	oldBlob := TrackedBlobPath(paths, fileStoreThread, dedup.ContentID([]byte("old\n")))

	disarm := crashpoint.Arm("barrier.prune.postIndex")
	recoverCrash(t, "barrier.prune.postIndex", func() {
		_, _ = PruneTrackedFiles(paths, fileStoreThread, turnCount)
	})
	disarm()

	if p := indexPaths(t, paths); !reflect.DeepEqual(p, []string{"new.go", "shared-new.go"}) {
		t.Errorf("index paths at the crash = %v, want the rewritten index", p)
	}
	assertIndexedFilesPresent(t, paths)
	if _, err := os.Stat(oldBlob); err != nil {
		t.Errorf("old.go's blob gone before the deletes: %v", err)
	}

	got, err := PruneTrackedFiles(paths, fileStoreThread, turnCount)
	if err != nil || got.Files != 1 {
		t.Fatalf("re-run prune = %+v, %v; want old.go's blob removed", got, err)
	}
	assertIndexedFilesPresent(t, paths)
	if _, err := os.Stat(oldBlob); !os.IsNotExist(err) {
		t.Errorf("old.go's blob after re-run: stat err %v, want absent", err)
	}
}
