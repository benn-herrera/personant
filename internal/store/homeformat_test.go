package store

import (
	"os"
	"testing"
)

func TestReadHomeFormatMissingIsNotAnError(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	found, format, err := ReadHomeFormat(paths)
	if err != nil {
		t.Fatalf("ReadHomeFormat: %v", err)
	}
	if found {
		t.Errorf("found = true for a home with no version.toml (format=%d)", format)
	}
	if format != 0 {
		t.Errorf("format = %d, want 0 when not found", format)
	}
}

func TestWriteReadHomeFormatRoundTrip(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := WriteHomeFormat(paths, 3); err != nil {
		t.Fatalf("WriteHomeFormat: %v", err)
	}

	data, err := os.ReadFile(paths.HomeVersion)
	if err != nil {
		t.Fatalf("read version.toml: %v", err)
	}
	if string(data) != "format = 3\n" {
		t.Errorf("version.toml = %q, want %q", data, "format = 3\n")
	}

	found, format, err := ReadHomeFormat(paths)
	if err != nil {
		t.Fatalf("ReadHomeFormat: %v", err)
	}
	if !found || format != 3 {
		t.Errorf("ReadHomeFormat = (%v, %d), want (true, 3)", found, format)
	}

	// Rewriting replaces the stamp rather than appending to it.
	if err := WriteHomeFormat(paths, 4); err != nil {
		t.Fatalf("WriteHomeFormat rewrite: %v", err)
	}
	found, format, err = ReadHomeFormat(paths)
	if err != nil {
		t.Fatalf("ReadHomeFormat after rewrite: %v", err)
	}
	if !found || format != 4 {
		t.Errorf("ReadHomeFormat after rewrite = (%v, %d), want (true, 4)", found, format)
	}
}

// TestReadHomeFormatMalformedIsAnError: a present-but-broken stamp must
// never be reported as "unversioned" — that would hand the adopt-forward
// path a corrupt home and let it overwrite the evidence.
func TestReadHomeFormatMalformedIsAnError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "not toml", content: "format = = 1\n"},
		{name: "wrong type", content: "format = \"one\"\n"},
		{name: "empty file", content: ""},
		{name: "no format key", content: "revision = 2\n"},
		{name: "zero format", content: "format = 0\n"},
		{name: "negative format", content: "format = -1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			paths := PathsForHome(t.TempDir())
			if err := os.WriteFile(paths.HomeVersion, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			found, format, err := ReadHomeFormat(paths)
			if err == nil {
				t.Fatalf("ReadHomeFormat = (%v, %d), want an error", found, format)
			}
			if found {
				t.Errorf("found = true alongside an error")
			}
		})
	}
}

func TestWriteHomeFormatRejectsNonPositive(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	for _, format := range []int{0, -1} {
		if err := WriteHomeFormat(paths, format); err == nil {
			t.Errorf("WriteHomeFormat(%d) = nil, want an error", format)
		}
	}
	if _, err := os.Stat(paths.HomeVersion); !os.IsNotExist(err) {
		t.Errorf("refused write still created %s", paths.HomeVersion)
	}
}
