package screentest

import (
	"errors"
	"strings"
	"testing"
)

// feed is the assertion-free driver: a sequence this package claims to
// model must not error.
func feed(t *testing.T, s *Screen, stream string) {
	t.Helper()
	if err := s.Feed([]byte(stream)); err != nil {
		t.Fatalf("Feed(%q): %v", stream, err)
	}
}

// The property the whole package exists for. A model that skipped what it
// did not recognize would go green while the writer drifted away from it.
func TestFeed_FailsClosedOnAnythingUnmodeled(t *testing.T) {
	tests := []struct {
		name   string
		stream string
	}{
		{"cursor up (W3 territory)", "abc\x1b[A"},
		{"absolute positioning", "\x1b[2;5H"},
		{"alternate screen", "\x1b[?1049h"},
		{"colour", "\x1b[31mred"},
		{"SGR 22, the exact dim undo term deliberately does not use", "\x1b[2mx\x1b[22m"},
		{"bare escape", "a\x1b"},
		{"tab", "a\tb"},
		{"backspace", "ab\b"},
		{"bell", "\a"},
		{"invalid utf-8", "ok\xff"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ue *UnmodeledError
			err := New(20, 5).Feed([]byte(tc.stream))
			if !errors.As(err, &ue) {
				t.Fatalf("Feed(%q) = %v, want an *UnmodeledError", tc.stream, err)
			}
			if !strings.Contains(ue.Error(), "IN THE SAME COMMIT") {
				t.Errorf("the error must say how to fix it; got %q", ue.Error())
			}
		})
	}
}

// The whole of term's current alphabet, and nothing about it errors.
func TestFeed_AcceptsExactlyTermsAlphabet(t *testing.T) {
	s := New(40, 5)
	feed(t, s, "\r\x1b[K| waiting (2s)"+"\r\x1b[K"+"\x1b[2mreasoning\x1b[0m\n"+"body\n")
	if got, want := s.TotalRows(), 3; got != want {
		t.Errorf("TotalRows = %d, want %d\n%s", got, want, s)
	}
}

// \n is CR+LF, because OPOST is left on precisely so that it is (see the
// package doc). A model that treated it as a bare line feed would put
// every second line at the wrong column.
func TestLineFeed_ReturnsToColumnZero(t *testing.T) {
	s := New(20, 5)
	feed(t, s, "abc\nd")
	if got := s.Row(1); got != "d" {
		t.Errorf("row 1 = %q, want %q — \\n did not imply a carriage return\n%s", got, "d", s)
	}
}

// eraseLine clears from the cursor to the end of the row and no further,
// which is what "no trailing artifacts after a region is retired" means.
func TestEraseLine_ClearsFromTheCursorRightward(t *testing.T) {
	s := New(20, 3)
	feed(t, s, "keep me | frame")
	feed(t, s, "\r\x1b[K")
	if got := s.Line(); got != "" {
		t.Errorf("erase from column 0 left %q\n%s", got, s)
	}

	s = New(20, 3)
	feed(t, s, "keepXXXX")
	feed(t, s, "\x1b[K") // cursor is mid-line: only the right of it goes
	if got := s.Line(); got != "keepXXXX" {
		t.Errorf("erase at end of text destroyed it: %q", got)
	}
}

// Pending wrap: writing the final column does not move to the next row.
// It is the behaviour term.StatusColumnInset stays clear of, so the model
// has to have it or a status line one column too long would look safe.
func TestWrap_IsPendingUntilTheNextGlyph(t *testing.T) {
	s := New(5, 3)
	feed(t, s, "abcde") // exactly the width
	if s.Wraps() != 0 || s.TotalRows() != 1 {
		t.Fatalf("filling the last column already wrapped: wraps=%d rows=%d\n%s", s.Wraps(), s.TotalRows(), s)
	}
	if _, col := s.Cursor(); col != 5 {
		t.Errorf("cursor col = %d, want 5 (pending wrap)", col)
	}
	feed(t, s, "f")
	if s.Wraps() != 1 || s.Row(1) != "f" {
		t.Errorf("the next glyph did not resolve the wrap: wraps=%d\n%s", s.Wraps(), s)
	}
}

// Scrollback: a row pushed off the top keeps its text AND its dim
// attribute, which is what makes the persistence ruling (arbitration
// item 1 — reasoning stays in scrollback) assertable at all.
func TestScrollback_KeepsTextAndDimness(t *testing.T) {
	s := New(20, 2)
	feed(t, s, "\x1b[2mreasoning\x1b[0m\n")
	feed(t, s, "answer\n")
	feed(t, s, "next prompt")

	if s.Scrolls() != 1 {
		t.Fatalf("scrolls = %d, want 1\n%s", s.Scrolls(), s)
	}
	if got := s.Scrollback(); len(got) != 1 || got[0] != "reasoning" {
		t.Fatalf("scrollback = %v, want [reasoning]\n%s", got, s)
	}
	if got := s.DimLines(); len(got) != 1 || got[0] != "reasoning" {
		t.Errorf("dim lines = %v, want [reasoning] — dimness did not survive the scroll\n%s", got, s)
	}
	if !s.Contains("answer") || !s.Contains("reasoning") {
		t.Errorf("Contains does not span scrollback + screen\n%s", s)
	}
	if s.LineDim() {
		t.Errorf("the current line inherited dimness from a scrolled-off row\n%s", s)
	}
}

// TotalRows is monotone and counts rows begun, not rows visible: it is
// the counter the "redraws in place" assertions read.
func TestTotalRows_CountsRowsBegunAcrossAScroll(t *testing.T) {
	s := New(10, 2)
	feed(t, s, "one\ntwo\nthree\nfour")
	if got, want := s.TotalRows(), 4; got != want {
		t.Errorf("TotalRows = %d, want %d\n%s", got, want, s)
	}
	before := s.TotalRows()
	feed(t, s, "\r\x1b[Kredraw\r\x1b[Kagain")
	if got := s.TotalRows(); got != before {
		t.Errorf("in-place redraws grew the row count: %d → %d\n%s", before, got, s)
	}
}
