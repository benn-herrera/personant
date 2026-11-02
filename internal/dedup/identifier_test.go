package dedup

import (
	"strings"
	"testing"
)

func TestContentID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // git hash-object
	}{
		{"empty blob", "", "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{"hello newline", "hello\n", "ce013625030ba8dba906f756967f9e9ca394464a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContentID([]byte(tt.in)); got != tt.want {
				t.Errorf("ContentID(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestIdentifierLine(t *testing.T) {
	const id = "ce013625030ba8dba906f756967f9e9ca394464a"
	tests := []struct {
		name string
		kind FileEventKind
		path string
		want string
	}{
		{"read", FileRead, "src/a.go", "[file read #ce013625030b] src/a.go"},
		{"write", FileWrite, "src/a.go", "[file write #ce013625030b] src/a.go"},
		{"commit", FileCommit, "src/a.go", "[file commit @ce01362] src/a.go"},
		{"path with spaces and bracket", FileWrite, "dir x/a] b.go", "[file write #ce013625030b] dir x/a] b.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IdentifierLine(tt.kind, id, tt.path)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if _, rest, ok := strings.Cut(got, "] "); !ok || rest != tt.path {
				t.Errorf("remainder after first \"] \" = %q, want path %q", rest, tt.path)
			}
		})
	}
}
