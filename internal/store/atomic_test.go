package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomicUsesDeterministicTemp asserts the write routes through a
// "tmp-<base>" sibling of the target and leaves no temp behind on success.
func TestWriteFileAtomicUsesDeterministicTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "spine.jsonl")
	tmpPath := filepath.Join(dir, atomicWriteTempPrefix+"spine.jsonl")

	if err := WriteFileAtomic(target, []byte("payload")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("target contents = %q, want %q", got, "payload")
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp %s should not survive a successful write, stat err = %v", tmpPath, err)
	}
}

// TestWriteFileAtomicOverwritesStaleTemp asserts a leftover tmp-<base> from a
// prior crashed write is truncated and reused, not errored on.
func TestWriteFileAtomicOverwritesStaleTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "spine.jsonl")
	tmpPath := filepath.Join(dir, atomicWriteTempPrefix+"spine.jsonl")

	// Simulate a crashed write: a stale temp with longer, unrelated content.
	if err := os.WriteFile(tmpPath, []byte("stale-crash-residue"), 0o600); err != nil {
		t.Fatalf("seed stale temp: %v", err)
	}

	if err := WriteFileAtomic(target, []byte("new")); err != nil {
		t.Fatalf("WriteFileAtomic over stale temp: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("target contents = %q, want %q (stale temp not cleanly overwritten)", got, "new")
	}
}
