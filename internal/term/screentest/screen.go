// Package screentest is a screen model: it consumes the byte stream
// internal/term emits and maintains what the user would actually see —
// a grid of cells, a cursor, and the lines that have scrolled off.
//
// It is SOLUTION.md §6 layer 1, revived in reduced form after W1
// (IMPLEMENTATION-PLAN.md §1 dropped it in favour of dogfooding, and W1
// then established that a wave's rendering had no standing automated
// gate at all). It is deliberately NOT a VT100 emulator. It models
// exactly the alphabet term emits — `\r`, `\n`, printable runes, `\x1b[K`,
// `\x1b[2m`, `\x1b[0m`, and (from W3, with the editor) `\x1b[J`,
// `\x1b[<n>A` and `\x1b[<n>C` — and it FAILS on anything else.
//
// # Fail-closed is the load-bearing property
//
// A model that skipped what it did not recognize would drift from the
// writer silently, and its green runs would mean nothing. Failing keeps
// the two sharing ONE finite alphabet: the commit that teaches term a new
// sequence is the commit that teaches this package to interpret it, or it
// does not go green. W1 predicted the editor would widen it and W3 did,
// by three sequences; nothing speculative is modelled beyond them.
//
// # Two modelling decisions worth stating
//
//   - `\n` moves to column 0 of the next row, not just down a row.
//     [term.ModeSession] leaves OPOST on precisely so that a bare "\n"
//     still implies a carriage return, and every writer in term relies on
//     it (see term.endReasoningLocked). Modelling it as a bare line feed
//     would make the model disagree with the terminal term is written
//     for.
//   - Writing the final column does NOT wrap; the wrap resolves when the
//     NEXT glyph arrives. That is xterm-family pending-wrap, and it is the
//     behaviour term.StatusColumnInset exists to stay clear of — a model
//     that wrapped eagerly could not tell a safe status line from one that
//     scrolls the screen.
package screentest

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The alphabet. These are term's constants restated at the reader's end
// of the seam, and that restatement is the point: they are unexported in
// term, and a model that imported them could not fail when term started
// emitting something it does not model.
const (
	esc       = 0x1b
	eraseLine = "\x1b[K" // erase from the cursor to end of line
	eraseDown = "\x1b[J" // erase from the cursor to the end of the screen
	dimOn     = "\x1b[2m"
	dimOff    = "\x1b[0m"

	// The parameterised pair. Both are RELATIVE, which is the property
	// worth restating at this end of the seam: term never positions
	// absolutely, so nothing it draws can reach a row it did not put there
	// — and a model that accepted absolute positioning would be a model
	// that could not notice if term started.
	csiPrefix    = "\x1b["
	finalUp      = 'A'
	finalForward = 'C'
)

// Cell is one screen position: the rune on it and whether it was written
// under SGR faint. A zero Cell is blank.
type Cell struct {
	R   rune
	Dim bool
}

// Screen is the model. Not safe for concurrent use — a test feeds it a
// captured buffer after the writers have quiesced.
type Screen struct {
	cols, rows int

	grid [][]Cell // rows x cols, grid[0] is the top of the screen
	row  int      // cursor row, 0..rows-1
	col  int      // cursor column, 0..cols (cols == pending wrap)
	dim  bool     // the SGR state the next glyph inherits

	scroll [][]Cell // lines that have scrolled off the top, oldest first

	maxRow  int // the lowest row the cursor has reached since the last scroll
	wraps   int
	scrolls int
}

// New returns a blank screen of the given geometry.
func New(cols, rows int) *Screen {
	if cols <= 0 || rows <= 0 {
		panic(fmt.Sprintf("screentest: nonsense geometry %dx%d", cols, rows))
	}
	s := &Screen{cols: cols, rows: rows, grid: make([][]Cell, rows)}
	for i := range s.grid {
		s.grid[i] = make([]Cell, cols)
	}
	return s
}

// UnmodeledError is what Feed returns for a byte sequence this package
// does not interpret. It is not a defect in the stream by itself — it is
// the model telling you the writer's alphabet grew and this package was
// not extended in the same commit.
type UnmodeledError struct {
	// Offset is the byte index within the buffer passed to Feed.
	Offset int
	// Seq is the offending bytes, quoted for a test failure message.
	Seq string
}

func (e *UnmodeledError) Error() string {
	return fmt.Sprintf("screentest: unmodeled sequence %s at byte %d — "+
		"internal/term emitted something this screen model does not interpret.\n"+
		"Extend the model IN THE SAME COMMIT that adds the sequence; widening it to skip\n"+
		"the bytes would let the writer and the model drift, which is the one property\n"+
		"this package has.", e.Seq, e.Offset)
}

// Feed consumes a captured stream. The buffer must contain whole
// sequences: term emits each escape with a single write under its own
// lock, so a buffer that ends mid-escape means the capture was taken
// while a writer was inside term — which is itself the finding.
func (s *Screen) Feed(b []byte) error {
	for i := 0; i < len(b); {
		switch c := b[i]; {
		case c == esc:
			n, err := s.escapeAt(b, i)
			if err != nil {
				return err
			}
			i += n
		case c == '\n':
			s.lineFeed()
			i++
		case c == '\r':
			s.col = 0
			i++
		case c < 0x20 || c == 0x7f:
			return &UnmodeledError{Offset: i, Seq: fmt.Sprintf("%q", string(rune(c)))}
		default:
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size <= 1 {
				return &UnmodeledError{Offset: i, Seq: fmt.Sprintf("%#x", b[i])}
			}
			s.put(r)
			i += size
		}
	}
	return nil
}

// escapeAt interprets one escape sequence and reports its length.
func (s *Screen) escapeAt(b []byte, i int) (int, error) {
	rest := string(b[i:])
	switch {
	case strings.HasPrefix(rest, eraseLine):
		s.eraseToEndOfRow(s.row, s.col)
		return len(eraseLine), nil
	case strings.HasPrefix(rest, eraseDown):
		s.eraseToEndOfRow(s.row, s.col)
		for r := s.row + 1; r < s.rows; r++ {
			s.eraseToEndOfRow(r, 0)
		}
		return len(eraseDown), nil
	case strings.HasPrefix(rest, dimOn):
		s.dim = true
		return len(dimOn), nil
	case strings.HasPrefix(rest, dimOff):
		s.dim = false
		return len(dimOff), nil
	}
	if n, ok := s.relativeMove(rest); ok {
		return n, nil
	}
	// Quote enough to identify it without dumping the rest of the stream.
	end := min(len(rest), 8)
	return 0, &UnmodeledError{Offset: i, Seq: fmt.Sprintf("%q", rest[:end])}
}

// relativeMove interprets "ESC [ <n> A" (up) and "ESC [ <n> C" (forward),
// the editor's two cursor motions. A count is REQUIRED and must be
// positive: term always writes one, and accepting the defaulted form would
// let a "\x1b[A" typo through as a legal move.
//
// Both CLAMP at the screen edge rather than scrolling, which is what a
// real terminal does — and it matters, because the editor's row-cap exists
// precisely so that it never asks for a row that is not there.
func (s *Screen) relativeMove(rest string) (int, bool) {
	if !strings.HasPrefix(rest, csiPrefix) {
		return 0, false
	}
	j := len(csiPrefix)
	n := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		n = n*10 + int(rest[j]-'0')
		j++
	}
	if j == len(csiPrefix) || j >= len(rest) || n <= 0 {
		return 0, false
	}
	switch rest[j] {
	case finalUp:
		s.row = max(s.row-n, 0)
	case finalForward:
		s.col = min(s.col+n, s.cols)
	default:
		return 0, false
	}
	return j + 1, true
}

func (s *Screen) eraseToEndOfRow(row, from int) {
	for c := from; c < s.cols; c++ {
		s.grid[row][c] = Cell{}
	}
}

// put writes one glyph, resolving a pending wrap first.
func (s *Screen) put(r rune) {
	if s.col >= s.cols {
		s.wraps++
		s.lineFeed()
	}
	s.grid[s.row][s.col] = Cell{R: r, Dim: s.dim}
	s.col++
}

// lineFeed moves to column 0 of the next row, scrolling if the cursor is
// already on the last one.
func (s *Screen) lineFeed() {
	s.col = 0
	if s.row < s.rows-1 {
		s.row++
		s.maxRow = max(s.maxRow, s.row)
		return
	}
	s.scrolls++
	s.scroll = append(s.scroll, s.grid[0])
	copy(s.grid, s.grid[1:]) // rotate the row headers up
	s.grid[s.rows-1] = make([]Cell, s.cols)
}

// --- assertion surface ------------------------------------------------

// Cursor reports the cursor position. col == Cols means pending wrap.
func (s *Screen) Cursor() (row, col int) { return s.row, s.col }

// TotalRows counts every row the cursor has ever occupied, scrolled-off
// rows included. It is MONOTONE, which is what makes "the indicator
// redraws in place" assertable: take it before a run of frames and after,
// and a spinner that emitted one line per frame moved it.
//
// It is the DEEPEST row reached plus the rows scrolled past it, not a
// count of line feeds. Those coincided for W1's append-only writers, and
// W3's editor separates them: a repaint moves the cursor up and writes the
// same rows again, which must not read as new screen.
func (s *Screen) TotalRows() int { return s.scrolls + s.maxRow + 1 }

// Wraps counts soft wraps — a glyph that arrived with the cursor past the
// last column. Nonzero means something term wrote was wider than the
// terminal.
func (s *Screen) Wraps() int { return s.wraps }

// Scrolls counts rows pushed off the top of the screen.
func (s *Screen) Scrolls() int { return s.scrolls }

// Line is the text of the row the cursor is on, trailing blanks trimmed.
func (s *Screen) Line() string { return rowText(s.grid[s.row]) }

// LineDim reports whether any cell of the cursor's row carries the dim
// attribute.
func (s *Screen) LineDim() bool { return rowDim(s.grid[s.row]) }

// Row is the text of screen row i (0 is the top of the visible screen).
func (s *Screen) Row(i int) string { return rowText(s.grid[i]) }

// RowDim reports whether any cell of screen row i is dim.
func (s *Screen) RowDim(i int) bool { return rowDim(s.grid[i]) }

// Rows is the visible screen, top to bottom.
func (s *Screen) Rows() []string { return texts(s.grid) }

// Scrollback is what has scrolled off the top, oldest first.
func (s *Screen) Scrollback() []string { return texts(s.scroll) }

// All is the whole session as the user could scroll it: scrollback then
// the visible screen. This is what the persistence ruling (arbitration
// item 1 — reasoning is persistent) is asserted against.
func (s *Screen) All() []string { return append(s.Scrollback(), s.Rows()...) }

// Contains reports whether any line — scrolled off or on screen — holds
// sub.
func (s *Screen) Contains(sub string) bool {
	for _, l := range s.All() {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// DimLines is every line with at least one dim cell, scrollback included.
// The dim attribute travels with the line into scrollback, so this is how
// "scroll back: the dimmed reasoning must still be there" is checked.
func (s *Screen) DimLines() []string {
	var out []string
	for _, rows := range [][][]Cell{s.scroll, s.grid} {
		for _, r := range rows {
			if rowDim(r) {
				out = append(out, rowText(r))
			}
		}
	}
	return out
}

// String renders the model for a failure message: scrollback above a
// ruled screen, with dim rows marked.
func (s *Screen) String() string {
	var b strings.Builder
	for _, r := range s.scroll {
		fmt.Fprintf(&b, "  %s|%s\n", mark(rowDim(r)), rowText(r))
	}
	fmt.Fprintf(&b, "  --- screen %dx%d, cursor (%d,%d) ---\n", s.cols, s.rows, s.row, s.col)
	for _, r := range s.grid {
		fmt.Fprintf(&b, "  %s|%s\n", mark(rowDim(r)), rowText(r))
	}
	return b.String()
}

func mark(dim bool) string {
	if dim {
		return "~"
	}
	return " "
}

func rowText(r []Cell) string {
	var b strings.Builder
	for _, c := range r {
		if c.R == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(c.R)
	}
	return strings.TrimRight(b.String(), " ")
}

func rowDim(r []Cell) bool {
	for _, c := range r {
		if c.Dim && c.R != 0 {
			return true
		}
	}
	return false
}

func texts(rows [][]Cell) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = rowText(r)
	}
	return out
}
