package store

import (
	"os"
	"testing"
)

func TestThreadFilesPath(t *testing.T) {
	paths := PathsForHome("/home/x")
	got := ThreadFilesPath(paths, "thr_7")
	want := "/home/x/threads/thr_7.files.json"
	if got != want {
		t.Fatalf("ThreadFilesPath = %q, want %q", got, want)
	}
}

func TestLoadThreadFilesMissingSidecar(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	tf, err := LoadThreadFiles(paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThreadFiles on missing sidecar: %v", err)
	}
	if tf.ThreadID != "thr_1" {
		t.Errorf("ThreadID = %q, want thr_1", tf.ThreadID)
	}
	if tf.Files == nil {
		t.Error("Files map is nil; want empty non-nil map")
	}
	if len(tf.Files) != 0 {
		t.Errorf("Files has %d entries, want 0", len(tf.Files))
	}
}

func TestLoadThreadFilesCorruptSidecar(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := os.MkdirAll(paths.ThreadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ThreadFilesPath(paths, "thr_2"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadThreadFiles(paths, "thr_2"); err == nil {
		t.Fatal("LoadThreadFiles on corrupt sidecar: want error, got nil")
	}
}

func TestThreadFilesRoundTrip(t *testing.T) {
	paths := PathsForHome(t.TempDir())

	tf := ThreadFiles{ThreadID: "thr_3", Files: map[string]*FileEntry{}}
	tf.RecordWrite("src/a.go", "v0")
	tf.RecordWrite("src/a.go", "v1")
	if err := tf.RecordCommit("src/a.go", "abc123", "2026-05-18T12:00:00Z"); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}

	if err := SaveThreadFiles(paths, tf); err != nil {
		t.Fatalf("SaveThreadFiles: %v", err)
	}

	got, err := LoadThreadFiles(paths, "thr_3")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if got.ThreadID != "thr_3" {
		t.Errorf("ThreadID = %q, want thr_3", got.ThreadID)
	}
	e, ok := got.Entry("src/a.go")
	if !ok {
		t.Fatal("Entry src/a.go missing after round-trip")
	}
	if e.Path != "src/a.go" {
		t.Errorf("Path = %q, want src/a.go", e.Path)
	}
	if e.LastCommit != "abc123" || e.CommittedAt != "2026-05-18T12:00:00Z" {
		t.Errorf("commit pointer = (%q,%q), want (abc123, 2026-05-18T12:00:00Z)", e.LastCommit, e.CommittedAt)
	}
	if e.Chain.Len() != 2 {
		t.Fatalf("Chain.Len = %d, want 2", e.Chain.Len())
	}
	if e.Chain.Current() != "v1" {
		t.Errorf("Chain.Current = %q, want v1", e.Chain.Current())
	}
	first, err := e.Chain.Reconstruct(0)
	if err != nil {
		t.Fatalf("Reconstruct(0): %v", err)
	}
	if first != "v0" {
		t.Errorf("Reconstruct(0) = %q, want v0", first)
	}
}

func TestRecordWriteSeedsThenAppends(t *testing.T) {
	tf := ThreadFiles{ThreadID: "thr_4", Files: map[string]*FileEntry{}}

	tf.RecordWrite("f.txt", "first")
	e, ok := tf.Entry("f.txt")
	if !ok {
		t.Fatal("entry not seeded on first write")
	}
	if e.Chain.Len() != 1 {
		t.Fatalf("after seed Chain.Len = %d, want 1", e.Chain.Len())
	}

	tf.RecordWrite("f.txt", "second")
	tf.RecordWrite("f.txt", "third")
	if e.Chain.Len() != 3 {
		t.Fatalf("after 3 writes Chain.Len = %d, want 3", e.Chain.Len())
	}
	if e.Chain.Current() != "third" {
		t.Errorf("Current = %q, want third", e.Chain.Current())
	}
	v0, err := e.Chain.Reconstruct(0)
	if err != nil {
		t.Fatalf("Reconstruct(0): %v", err)
	}
	if v0 != "first" {
		t.Errorf("Reconstruct(0) = %q, want first", v0)
	}
}

func TestRecordWriteClearsCommitPointer(t *testing.T) {
	tf := ThreadFiles{ThreadID: "thr_5", Files: map[string]*FileEntry{}}
	tf.RecordWrite("f.txt", "v0")
	if err := tf.RecordCommit("f.txt", "h1", "2026-05-18T00:00:00Z"); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}

	tf.RecordWrite("f.txt", "v1")
	e, _ := tf.Entry("f.txt")
	if e.LastCommit != "" || e.CommittedAt != "" {
		t.Errorf("commit pointer not cleared: (%q,%q)", e.LastCommit, e.CommittedAt)
	}
}

func TestRecordWriteUnchangedContentIsNoOp(t *testing.T) {
	tf := ThreadFiles{ThreadID: "thr_7", Files: map[string]*FileEntry{}}
	tf.RecordWrite("f.txt", "v0")
	if err := tf.RecordCommit("f.txt", "h1", "2026-05-18T00:00:00Z"); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}

	// Re-write with byte-identical content: must not grow the chain and
	// must not clear the commit pointer.
	tf.RecordWrite("f.txt", "v0")
	e, _ := tf.Entry("f.txt")
	if e.Chain.Len() != 1 {
		t.Errorf("after unchanged re-write Chain.Len = %d, want 1", e.Chain.Len())
	}
	if e.LastCommit != "h1" || e.CommittedAt != "2026-05-18T00:00:00Z" {
		t.Errorf("commit pointer cleared by unchanged re-write: (%q,%q)", e.LastCommit, e.CommittedAt)
	}

	// A genuine change still appends and still clears the commit pointer.
	tf.RecordWrite("f.txt", "v1")
	if e.Chain.Len() != 2 {
		t.Errorf("after changed write Chain.Len = %d, want 2", e.Chain.Len())
	}
	if e.LastCommit != "" || e.CommittedAt != "" {
		t.Errorf("commit pointer not cleared by changed write: (%q,%q)", e.LastCommit, e.CommittedAt)
	}
}

func TestRecordCommitErrors(t *testing.T) {
	tf := ThreadFiles{ThreadID: "thr_6", Files: map[string]*FileEntry{}}
	tf.RecordWrite("tracked.txt", "x")

	if err := tf.RecordCommit("untracked.txt", "h", "2026-05-18T00:00:00Z"); err == nil {
		t.Error("RecordCommit on untracked path: want error, got nil")
	}
	if err := tf.RecordCommit("tracked.txt", "", "2026-05-18T00:00:00Z"); err == nil {
		t.Error("RecordCommit with empty hash: want error, got nil")
	}
	if err := tf.RecordCommit("tracked.txt", "h", ""); err == nil {
		t.Error("RecordCommit with empty committedAt: want error, got nil")
	}
	if err := tf.RecordCommit("tracked.txt", "h", "2026-05-18T00:00:00Z"); err != nil {
		t.Errorf("RecordCommit on tracked path: unexpected error %v", err)
	}
}
