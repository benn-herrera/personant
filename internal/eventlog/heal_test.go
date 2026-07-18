package eventlog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHealTailAppendsNewlineOnTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.log")
	if err := os.WriteFile(path, []byte("complete line\ntorn line no newline"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := HealTail(path); err != nil {
		t.Fatalf("HealTail: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "complete line\ntorn line no newline\n"
	if string(got) != want {
		t.Fatalf("HealTail result = %q, want %q", got, want)
	}
}

func TestHealTailIdempotentAndNoMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.log")
	// A torn final line (no trailing newline).
	if err := os.WriteFile(path, []byte("first\nsecond-torn"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Heal once.
	if err := HealTail(path); err != nil {
		t.Fatalf("HealTail 1: %v", err)
	}
	afterFirst, _ := os.ReadFile(path)

	// Heal again: idempotent — already terminated, no second newline added.
	if err := HealTail(path); err != nil {
		t.Fatalf("HealTail 2: %v", err)
	}
	afterSecond, _ := os.ReadFile(path)
	if string(afterFirst) != string(afterSecond) {
		t.Fatalf("HealTail not idempotent: %q then %q", afterFirst, afterSecond)
	}

	// No-merge: an append after healing lands on its own line, so the torn
	// fragment and the new record scan as two distinct lines.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString("third\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	lines, torn, err := ReadLinesTolerant(path)
	if err != nil {
		t.Fatalf("ReadLinesTolerant: %v", err)
	}
	if torn != 0 {
		t.Errorf("torn=%d after heal+append, want 0", torn)
	}
	want := []string{"first", "second-torn", "third"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(lines), lines, len(want), want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (torn fragment merged with the append?)", i, lines[i], want[i])
		}
	}
}

func TestHealTailNoOpCases(t *testing.T) {
	dir := t.TempDir()

	// Absent file: no-op, no error.
	absent := filepath.Join(dir, "absent.log")
	if err := HealTail(absent); err != nil {
		t.Fatalf("HealTail absent: %v", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("HealTail created an absent file")
	}

	// Empty file: no-op.
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatalf("seed empty: %v", err)
	}
	if err := HealTail(empty); err != nil {
		t.Fatalf("HealTail empty: %v", err)
	}
	if info, _ := os.Stat(empty); info.Size() != 0 {
		t.Fatalf("HealTail grew an empty file to %d bytes", info.Size())
	}

	// Already-terminated file: no second newline.
	term := filepath.Join(dir, "term.log")
	if err := os.WriteFile(term, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("seed term: %v", err)
	}
	if err := HealTail(term); err != nil {
		t.Fatalf("HealTail terminated: %v", err)
	}
	got, _ := os.ReadFile(term)
	if string(got) != "line\n" {
		t.Fatalf("HealTail mutated a terminated file: %q", got)
	}
}

func TestReadLinesTolerantSkipsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.log")
	if err := os.WriteFile(path, []byte("a\nb\nctorn"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	lines, torn, err := ReadLinesTolerant(path)
	if err != nil {
		t.Fatalf("ReadLinesTolerant: %v", err)
	}
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("lines = %q, want [a b]", lines)
	}
	if torn != len("ctorn") {
		t.Fatalf("tornBytes = %d, want %d", torn, len("ctorn"))
	}
}

func TestReadLinesTolerantAbsentAndClean(t *testing.T) {
	dir := t.TempDir()
	lines, torn, err := ReadLinesTolerant(filepath.Join(dir, "absent.log"))
	if err != nil || lines != nil || torn != 0 {
		t.Fatalf("absent = (%q, %d, %v), want (nil, 0, nil)", lines, torn, err)
	}

	clean := filepath.Join(dir, "clean.log")
	if err := os.WriteFile(clean, []byte("x\ny\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	lines, torn, err = ReadLinesTolerant(clean)
	if err != nil {
		t.Fatalf("ReadLinesTolerant clean: %v", err)
	}
	if torn != 0 || len(lines) != 2 {
		t.Fatalf("clean read = (%q, torn=%d), want 2 lines torn=0", lines, torn)
	}
}
