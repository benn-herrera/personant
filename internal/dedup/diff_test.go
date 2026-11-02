package dedup

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single no newline", "abc", []string{"abc"}},
		{"single with newline", "abc\n", []string{"abc\n"}},
		{"multi trailing newline", "a\nb\n", []string{"a\n", "b\n"}},
		{"multi no trailing newline", "a\nb", []string{"a\n", "b"}},
		{"blank lines", "\n\n", []string{"\n", "\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitLines(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitLines(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
			// Round-trip: joining must reproduce the input exactly.
			if join := strings.Join(got, ""); join != tt.in {
				t.Fatalf("join(splitLines(%q)) = %q", tt.in, join)
			}
		})
	}
}

// TestDiffRoundTrip exercises makeDiff/applyDiff: applying the diff of
// (old -> new) to old must reproduce new byte-for-byte.
func TestDiffRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
	}{
		{"identical", "a\nb\nc\n", "a\nb\nc\n"},
		{"empty to content", "", "hello\nworld\n"},
		{"content to empty", "hello\nworld\n", ""},
		{"both empty", "", ""},
		{"append line", "a\nb\n", "a\nb\nc\n"},
		{"prepend line", "b\nc\n", "a\nb\nc\n"},
		{"delete middle", "a\nb\nc\n", "a\nc\n"},
		{"replace middle", "a\nb\nc\n", "a\nX\nc\n"},
		{"multi-region", "a\nb\nc\nd\ne\nf\n", "a\nB\nc\nd\nE\nf\nG\n"},
		{"add trailing newline", "abc", "abc\n"},
		{"remove trailing newline", "abc\n", "abc"},
		{"no newline both sides change", "abc\ndef", "abc\nxyz"},
		{"blank line insert", "a\nc\n", "a\n\nc\n"},
		{"full rewrite", "one\ntwo\nthree\n", "alpha\nbeta\ngamma\n"},
		{"single char", "x", "y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := makeDiff(tt.old, tt.new)
			got, err := applyDiff(tt.old, diff)
			if err != nil {
				t.Fatalf("applyDiff error: %v\ndiff:\n%s", err, diff)
			}
			if got != tt.new {
				t.Fatalf("round-trip mismatch\n got: %q\nwant: %q\ndiff:\n%s", got, tt.new, diff)
			}
		})
	}
}

func TestApplyDiffMismatch(t *testing.T) {
	diff := makeDiff("a\nb\n", "a\nc\n")
	if _, err := applyDiff("x\ny\n", diff); err == nil {
		t.Fatal("expected context-mismatch error applying diff to wrong source")
	}
	if _, err := applyDiff("a\nb\n", "garbage"); err == nil {
		t.Fatal("expected error on malformed diff (no hunk header)")
	}
}

func TestDiffKeptRoundTrip(t *testing.T) {
	var lines []string
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("line %02d %s\n", i, strings.Repeat("x", 32)))
	}
	old := strings.Join(lines, "")
	lines[10] = "changed\n"
	new := strings.Join(lines, "")
	d, kept := Diff(old, new)
	if !kept {
		t.Fatalf("small edit not kept: diff %d bytes, new %d bytes", len(d), len(new))
	}
	got, err := applyDiff(old, d)
	if err != nil {
		t.Fatalf("applyDiff: %v", err)
	}
	if got != new {
		t.Fatalf("round trip = %q, want %q", got, new)
	}
}

func TestDiffLineProductGuard(t *testing.T) {
	// 3000 × 3000 lines = 9,000,000 > lcsLineProductMax.
	old := strings.Repeat("x\n", 3000)
	new := old[:len(old)-2] + "y\n"
	if d, kept := Diff(old, new); kept || d != "" {
		t.Fatalf("Diff over the line-product guard = (%d bytes, %v), want (\"\", false)", len(d), kept)
	}
}

func TestDiffThresholdBoundary(t *testing.T) {
	// The tail line sits outside the hunk's context, so the diff is the
	// same 70 bytes whatever the tail's length; only len(new) moves.
	ctx := "cccccccccccc01\ncccccccccccc02\ncccccccccccc03\n"
	build := func(tailLen int) (string, string) {
		tail := strings.Repeat("t", tailLen-1) + "\n"
		return "a\n" + ctx + tail, "b\n" + ctx + tail
	}
	tests := []struct {
		name    string
		tailLen int
		newLen  int
		kept    bool
	}{
		{"diff at 0.7 of new", 53, 100, false},
		{"diff below 0.7 of new", 54, 101, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, new := build(tt.tailLen)
			if len(new) != tt.newLen || len(makeDiff(old, new)) != 70 {
				t.Fatalf("fixture: len(new)=%d, len(diff)=%d; want %d, 70", len(new), len(makeDiff(old, new)), tt.newLen)
			}
			if _, kept := Diff(old, new); kept != tt.kept {
				t.Errorf("kept = %v, want %v", kept, tt.kept)
			}
		})
	}
}
