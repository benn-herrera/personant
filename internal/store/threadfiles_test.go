package store

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"
)

// TestThreadFilesIsGitFree is the M1 acceptance A5 structural guard: the
// §3.9 aging-policy decision in threadfiles.go must stay free of any
// workspace-git dependency. The reachability gate lives only in the adapter
// + gitworkspace.go; threadfiles.go computes candidates purely in memory.
func TestThreadFilesIsGitFree(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "threadfiles.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse threadfiles.go: %v", err)
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "os/exec" || strings.HasSuffix(path, "gitworkspace") {
			t.Errorf("threadfiles.go imports %q — workspace-git dependency must live in the adapter + gitworkspace.go, not here", path)
		}
	}
}

func TestThreadFilesPath(t *testing.T) {
	paths := PathsForHome("/home/x")
	got := ThreadFilesPath(paths, "thr_7")
	want := "/home/x/threads/thr_7/files.json"
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
	if err := os.MkdirAll(ThreadDir(paths, "thr_2"), 0o755); err != nil {
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
	if err := tf.RecordCommit("src/a.go", "abc123", "2026-05-18T12:00:00Z", 7); err != nil {
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
	if err := tf.RecordCommit("f.txt", "h1", "2026-05-18T00:00:00Z", 3); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}

	tf.RecordWrite("f.txt", "v1")
	e, _ := tf.Entry("f.txt")
	if e.LastCommit != "" || e.CommittedAt != "" || e.CommittedTurn != 0 {
		t.Errorf("commit pointer not cleared: (%q,%q,%d)", e.LastCommit, e.CommittedAt, e.CommittedTurn)
	}
}

func TestRecordWriteUnchangedContentIsNoOp(t *testing.T) {
	tf := ThreadFiles{ThreadID: "thr_7", Files: map[string]*FileEntry{}}
	tf.RecordWrite("f.txt", "v0")
	if err := tf.RecordCommit("f.txt", "h1", "2026-05-18T00:00:00Z", 3); err != nil {
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

	if err := tf.RecordCommit("untracked.txt", "h", "2026-05-18T00:00:00Z", 1); err == nil {
		t.Error("RecordCommit on untracked path: want error, got nil")
	}
	if err := tf.RecordCommit("tracked.txt", "", "2026-05-18T00:00:00Z", 1); err == nil {
		t.Error("RecordCommit with empty hash: want error, got nil")
	}
	if err := tf.RecordCommit("tracked.txt", "h", "", 1); err == nil {
		t.Error("RecordCommit with empty committedAt: want error, got nil")
	}
	if err := tf.RecordCommit("tracked.txt", "h", "2026-05-18T00:00:00Z", 1); err != nil {
		t.Errorf("RecordCommit on tracked path: unexpected error %v", err)
	}
}

// committedAt is a fixed reference timestamp used by the AgeOut tests.
var ageOutCommitTime = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

// newCommittedEntry builds a ThreadFiles with one committed file whose
// chain has the given current literal, committed at ageOutCommitTime on
// committedTurn.
func newCommittedEntry(t *testing.T, content string, committedTurn int) ThreadFiles {
	t.Helper()
	tf := ThreadFiles{ThreadID: "thr_age", Files: map[string]*FileEntry{}}
	tf.RecordWrite("f.txt", content)
	if err := tf.RecordCommit("f.txt", "hash-abc", ageOutCommitTime.Format(time.RFC3339), committedTurn); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}
	return tf
}

func TestAgeOutTurnThreshold(t *testing.T) {
	tf := newCommittedEntry(t, "committed-content", 5)
	// currentTurn - committedTurn = FileChainRetentionTurns exactly →
	// candidate. AgeOut is decision-only and must NOT mutate.
	now := ageOutCommitTime // same instant, so the day threshold cannot trip
	cands := tf.AgeOut(5+FileChainRetentionTurns, now)
	if len(cands) != 1 || cands[0] != "f.txt" {
		t.Fatalf("candidates = %v, want [f.txt]", cands)
	}
	e, _ := tf.Entry("f.txt")
	if e.Chain.Len() != 1 {
		t.Errorf("AgeOut mutated chain (must be decision-only): Len = %d, want 1", e.Chain.Len())
	}
	// The effect is applied by DropChain.
	freed, ok := tf.DropChain("f.txt")
	if !ok {
		t.Fatal("DropChain reported path not tracked")
	}
	if freed != len("committed-content") {
		t.Errorf("bytesFreed = %d, want %d", freed, len("committed-content"))
	}
	if e.Chain.Len() != 0 {
		t.Errorf("chain not dropped: Len = %d, want 0", e.Chain.Len())
	}
}

func TestAgeOutDayThreshold(t *testing.T) {
	tf := newCommittedEntry(t, "content", 5)
	// Turn delta is below the turn threshold; only the day threshold trips.
	now := ageOutCommitTime.AddDate(0, 0, FileChainRetentionDays)
	cands := tf.AgeOut(6, now)
	if len(cands) != 1 || cands[0] != "f.txt" {
		t.Fatalf("candidates = %v, want [f.txt]", cands)
	}
	freed, ok := tf.DropChain("f.txt")
	if !ok || freed != len("content") {
		t.Errorf("DropChain = (%d, %v), want (%d, true)", freed, ok, len("content"))
	}
	e, _ := tf.Entry("f.txt")
	if e.Chain.Len() != 0 {
		t.Errorf("chain not dropped: Len = %d, want 0", e.Chain.Len())
	}
}

func TestAgeOutUnderBothThresholds(t *testing.T) {
	tf := newCommittedEntry(t, "content", 5)
	// Just under the turn threshold and just under the day threshold.
	now := ageOutCommitTime.AddDate(0, 0, FileChainRetentionDays).Add(-time.Second)
	cands := tf.AgeOut(5+FileChainRetentionTurns-1, now)
	if len(cands) != 0 {
		t.Errorf("candidates = %v, want none (under both thresholds)", cands)
	}
	e, _ := tf.Entry("f.txt")
	if e.Chain.Len() != 1 {
		t.Errorf("chain dropped while under thresholds: Len = %d, want 1", e.Chain.Len())
	}
}

func TestAgeOutUncommittedNeverAges(t *testing.T) {
	tf := ThreadFiles{ThreadID: "thr_age", Files: map[string]*FileEntry{}}
	tf.RecordWrite("f.txt", "uncommitted")
	// Far past both thresholds — but the entry was never committed.
	now := ageOutCommitTime.AddDate(1, 0, 0)
	cands := tf.AgeOut(100000, now)
	if len(cands) != 0 {
		t.Errorf("uncommitted entry is a candidate: %v", cands)
	}
	e, _ := tf.Entry("f.txt")
	if e.Chain.Len() != 1 {
		t.Errorf("uncommitted chain dropped: Len = %d, want 1", e.Chain.Len())
	}
}

func TestAgeOutCorruptCommittedAtSkipped(t *testing.T) {
	tf := newCommittedEntry(t, "content", 5)
	e, _ := tf.Entry("f.txt")
	e.CommittedAt = "not-a-timestamp"
	// The day threshold cannot be evaluated; the turn delta is below
	// threshold, so the corrupt entry must be skipped, not a candidate.
	now := ageOutCommitTime.AddDate(0, 0, 365)
	cands := tf.AgeOut(6, now)
	if len(cands) != 0 {
		t.Errorf("corrupt-timestamp entry is a candidate via day path: %v", cands)
	}
	if e.Chain.Len() != 1 {
		t.Errorf("corrupt-timestamp chain dropped: Len = %d, want 1", e.Chain.Len())
	}
	// The turn threshold still works independently of the timestamp.
	cands = tf.AgeOut(5+FileChainRetentionTurns, now)
	if len(cands) != 1 {
		t.Errorf("turn-threshold candidate blocked by corrupt timestamp: %v", cands)
	}
}

func TestDropChainKeepsHash(t *testing.T) {
	tf := newCommittedEntry(t, "content", 5)
	cands := tf.AgeOut(5+FileChainRetentionTurns, ageOutCommitTime)
	if len(cands) != 1 {
		t.Fatalf("entry not a candidate: %v", cands)
	}
	if _, ok := tf.DropChain("f.txt"); !ok {
		t.Fatal("DropChain reported path not tracked")
	}
	e, _ := tf.Entry("f.txt")
	if e.Chain.Len() != 0 {
		t.Errorf("chain not dropped: Len = %d, want 0", e.Chain.Len())
	}
	if e.LastCommit != "hash-abc" {
		t.Errorf("LastCommit = %q, want hash-abc (recovery pointer must survive)", e.LastCommit)
	}
	if e.CommittedAt != ageOutCommitTime.Format(time.RFC3339) {
		t.Errorf("CommittedAt = %q, want preserved", e.CommittedAt)
	}
	if e.CommittedTurn != 5 {
		t.Errorf("CommittedTurn = %d, want 5 (preserved)", e.CommittedTurn)
	}
	if e.Path != "f.txt" {
		t.Errorf("Path = %q, want f.txt (preserved)", e.Path)
	}
}

func TestDropChainUntrackedPath(t *testing.T) {
	tf := newCommittedEntry(t, "content", 5)
	freed, ok := tf.DropChain("absent.txt")
	if ok || freed != 0 {
		t.Errorf("DropChain on untracked path = (%d, %v), want (0, false)", freed, ok)
	}
}
