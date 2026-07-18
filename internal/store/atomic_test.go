package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/crashpoint"
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

// TestWriteFileAtomicUnarmedNoCrash: with no crashpoint armed (the
// production state), the registered kill points are inert.
func TestWriteFileAtomicUnarmedNoCrash(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "spine.jsonl")
	if err := WriteFileAtomic(target, []byte("payload")); err != nil {
		t.Fatalf("WriteFileAtomic with nothing armed: %v", err)
	}
}

// TestWriteFileAtomicPreRenameCrash: a crash at the pre-rename point leaves
// the target at its PRIOR contents — the atomic commit (rename) never ran.
func TestWriteFileAtomicPreRenameCrash(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "spine.jsonl")
	if err := WriteFileAtomic(target, []byte("original")); err != nil {
		t.Fatalf("seed original: %v", err)
	}

	disarm := crashpoint.Arm(cpAtomicPreRename)
	defer disarm()

	recoverCrash(t, cpAtomicPreRename, func() {
		_ = WriteFileAtomic(target, []byte("replacement"))
	})

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("target = %q, want %q — pre-rename crash must not commit", got, "original")
	}
}

// TestWriteFileAtomicTornWriteCrash: a crash mid-write leaves a partial
// tmp- residue on disk (deliberately not cleaned up) and does NOT create
// or replace the target — the residue is what recovery must sweep.
func TestWriteFileAtomicTornWriteCrash(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "spine.jsonl")
	tmpPath := filepath.Join(dir, atomicWriteTempPrefix+"spine.jsonl")

	disarm := crashpoint.Arm(cpAtomicTornWrite)
	defer disarm()

	recoverCrash(t, cpAtomicTornWrite, func() {
		_ = WriteFileAtomic(target, []byte("0123456789"))
	})

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target should not exist after a torn write, stat err = %v", err)
	}
	residue, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("torn-write residue temp missing (recovery would have nothing to sweep): %v", err)
	}
	if len(residue) == 0 || len(residue) >= len("0123456789") {
		t.Fatalf("residue = %q, want a partial prefix of the data", residue)
	}
}

// recoverCrash runs fn, asserting it panics with a *crashpoint.Crash whose
// Point is want.
func recoverCrash(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a crashpoint panic at %q, got none", want)
		}
		err, ok := r.(error)
		var crash *crashpoint.Crash
		if !ok || !errors.As(err, &crash) {
			t.Fatalf("panic value %v (%T) is not a *crashpoint.Crash", r, r)
		}
		if crash.Point != want {
			t.Fatalf("crashed at %q, want %q", crash.Point, want)
		}
	}()
	fn()
}
