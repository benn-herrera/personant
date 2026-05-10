package store

import (
	"os"
	"strings"
	"testing"
)

func lastActivePaths(t *testing.T) PersonantPaths {
	t.Helper()
	home := t.TempDir()
	return PathsForHome(home)
}

func TestWriteReadLastActiveRoundTrip(t *testing.T) {
	paths := lastActivePaths(t)
	if err := WriteLastActive(paths, "prj_42"); err != nil {
		t.Fatalf("WriteLastActive: %v", err)
	}
	got, err := ReadLastActive(paths)
	if err != nil {
		t.Fatalf("ReadLastActive: %v", err)
	}
	if got != "prj_42" {
		t.Errorf("got %q, want %q", got, "prj_42")
	}
}

func TestWriteReadLastActiveDefault(t *testing.T) {
	paths := lastActivePaths(t)
	if err := WriteLastActive(paths, DefaultProjectID); err != nil {
		t.Fatalf("WriteLastActive default: %v", err)
	}
	got, err := ReadLastActive(paths)
	if err != nil {
		t.Fatalf("ReadLastActive: %v", err)
	}
	if got != DefaultProjectID {
		t.Errorf("got %q, want %q", got, DefaultProjectID)
	}
}

func TestReadLastActiveMissing(t *testing.T) {
	paths := lastActivePaths(t)
	got, err := ReadLastActive(paths)
	if err != nil {
		t.Fatalf("ReadLastActive: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

func TestReadLastActiveMalformed(t *testing.T) {
	paths := lastActivePaths(t)
	if err := os.WriteFile(paths.LastActive, []byte("not-a-project-id\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadLastActive(paths)
	if err == nil {
		t.Fatalf("expected error for malformed id, got nil (read %q)", got)
	}
	if got != "" {
		t.Errorf("expected empty result on malformed read, got %q", got)
	}
}

func TestReadLastActiveBlank(t *testing.T) {
	paths := lastActivePaths(t)
	// File present but only whitespace — treated as "no value".
	if err := os.WriteFile(paths.LastActive, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadLastActive(paths)
	if err != nil {
		t.Fatalf("ReadLastActive blank: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for blank file, got %q", got)
	}
}

func TestWriteLastActiveRejectsInvalidID(t *testing.T) {
	paths := lastActivePaths(t)
	cases := []string{"", "not-an-id", "prj_", "PRJ_1", "thr_1"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			if err := WriteLastActive(paths, id); err == nil {
				t.Fatalf("expected error for invalid id %q", id)
			}
			// File must not exist after a rejected write.
			if _, statErr := os.Stat(paths.LastActive); statErr == nil {
				t.Fatalf("file should not exist after rejected write of %q", id)
			}
		})
	}
}

func TestWriteLastActiveTrailingNewline(t *testing.T) {
	paths := lastActivePaths(t)
	if err := WriteLastActive(paths, "prj_1"); err != nil {
		t.Fatalf("WriteLastActive: %v", err)
	}
	data, err := os.ReadFile(paths.LastActive)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("expected trailing newline, got %q", string(data))
	}
}
