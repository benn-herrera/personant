package term

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// The line editor. It replaces liner (SOLUTION.md §2: liner hardcodes
// os.Stdin, applies its mode at construction, installs its own SIGWINCH
// handler and holds three byte reservoirs) and it is deliberately small.
//
// # What it is not
//
// No completion, no reverse search, no kill ring beyond the three kills,
// no bracketed paste, no mouse. Personant's prompt is a place to type a
// sentence, and every feature added here is a feature two backends and one
// screen model have to agree about.
//
// # Where its state lives, and why it is split
//
// The BUFFER — what the user typed, where the cursor is in it, where the
// history walk has got to — is the editor's, under e.mu, and is touched by
// the pump goroutine (key dispatch, resize) plus the reading goroutine at
// construction and teardown.
//
// The BLOCK POSITION — whether a block is currently drawn and which of its
// rows the cursor sits on — is the TERMINAL's, under t.mu, next to the
// cursor column and the status slot. That is not tidiness: any committed
// write invalidates the block (it scrolled the editor's rows out from
// under it), and the write path must be able to say so without reaching
// into the editor and inverting the lock order.
//
// # Multi-line input, and the terminal reality behind its keys
//
// The buffer takes '\n' as an ordinary rune and [editor.layout] treats it
// as a hard row break — a SECOND cause of a row break beside width
// wrapping, over the same rendering machinery. Everything else follows
// from that: ←/→ cross a newline like any other rune, Backspace and Delete
// remove it as one rune, the row cap and the placeholder are unchanged,
// and submitting returns the whole buffer with its newlines intact.
//
// TWO keys insert one, and the reason there are two is not preference:
//
//   - Alt/Option+Enter, {KeyEnter, Alt: true}. The UNIVERSAL one. Every
//     terminal encodes it as ESC CR and the decoder has always told it
//     apart from a bare Enter.
//   - Shift+Enter, [KeyShiftEnter], which a terminal can only report under
//     an enhanced keyboard protocol — legacy input gives Enter and
//     Shift+Enter the same byte, 0x0D, and personant deliberately does not
//     request a protocol that would change that (see [KeyShiftEnter]). It
//     therefore works only where the user has configured their terminal to
//     send a distinguishable sequence; SPEC §4.3.1 carries the recipe.
//
// Bare Enter submits, always. That asymmetry is the whole point: the key
// that ends the read must be the one every terminal agrees about.

const (
	// ctrlCHint is the transient line a Ctrl-C at an EMPTY prompt draws
	// (user ruling, 2026-07-31 — arbitration item 3: hint, then a
	// deliberate second press; single-press exit is destructive on a typo).
	//
	// It is rendered as an extra row of the editor's OWN repaint block, so
	// it is genuinely transient: the next keystroke's repaint takes it back
	// with the same erase that redraws the line, and it can neither reach
	// scrollback nor contend with the ephemeral status slot for the live
	// line.
	ctrlCHint = "(press ctrl-c again to exit)"

	// inputOverflowFormat is the placeholder the row cap collapses excess
	// rows into (arbitration item 6). It says the buffer is INTACT because
	// that is the one thing the user cannot see and needs to know: the cap
	// bounds what is rendered, never what is returned.
	inputOverflowFormat = "… %d row(s) above — full line kept"
)

// editorResult is what one completed read produced. Exactly one is sent,
// on a buffered channel, so the pump goroutine never blocks on a reader
// that has already gone away down its ctx path.
type editorResult struct {
	text string
	key  rune // nonzero only on the Question.Keys fast path
	err  error
}

type editor struct {
	t       *Terminal
	prompt  string
	promptW int
	keys    string
	hist    *history
	rowCap  int
	result  chan editorResult

	mu sync.Mutex
	// reg is this read's stack entry, attached under mu by the reading
	// goroutine immediately after Push — so the pump, which must take mu to
	// deliver a key at all, cannot observe a half-attached editor. Cleared
	// from the offer chain by [editor.finish] the instant a line exists.
	reg *Registration
	// line and cur are runes and a RUNE index. Bytes would put the cursor
	// inside a multi-byte glyph the first time someone typed one.
	line  []rune
	cur   int
	first bool // no key has been offered yet — the Keys fast path's window
	hint  string
	ctrlC int // consecutive Ctrl-C presses at an empty line

	// The history walk. histIdx == len(entries) means "on the live line";
	// stash holds that line while the walk is above it.
	histIdx int
	stash   []rune

	finished bool
}

// newEditor builds one read's editor. It does not touch the terminal;
// [editor.start] paints the first frame.
func newEditor(t *Terminal, q Question) *editor {
	e := &editor{
		t:       t,
		prompt:  q.Prompt,
		promptW: cellWidth(q.Prompt),
		keys:    q.Keys,
		hist:    t.history(),
		rowCap:  t.inputRowCap(),
		result:  make(chan editorResult, 1),
		first:   true,
	}
	// Default is a pre-filled, EDITABLE value with the cursor at the end
	// (today's PromptWithSuggestion). Rune-indexed, or a multi-byte default
	// would place the cursor past the end of its own line.
	e.line = []rune(q.Default)
	e.cur = len(e.line)
	if e.hist != nil {
		e.histIdx = len(e.hist.entries)
	}
	return e
}

func (e *editor) start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.render()
}

// key is the [Handler.Key] this editor registers. It CLAIMS every key: an
// editor is the input consumer, and a key falling past it to a turn's
// watcher would be the turn reading over the prompt's shoulder.
func (e *editor) key(k Key) Disposition {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return Declined
	}
	first := e.first
	e.first = false

	// The Question.Keys fast path (finally live — W2 could not have it,
	// because liner owned the whole line read and offered no hook to see
	// the first keystroke first). If the first key is in the set the read
	// returns immediately, WITHOUT Enter; if it is not, it becomes the
	// first character of a normally edited line, so a menu offering
	// "[a]ll / [n]one" still accepts "1 3 5".
	if first && e.keys != "" && k.Name == KeyRune && !k.Alt &&
		strings.ContainsRune(e.keys, k.Rune) {
		e.line = []rune{k.Rune}
		e.cur = 1
		e.finish(string(k.Rune), k.Rune, nil)
		return Claimed
	}

	// Any key that is not Ctrl-C spends the pending double-press and takes
	// the hint back down with it.
	if !(k.Name == KeyCtrl && k.Rune == 'c') {
		e.ctrlC, e.hint = 0, ""
	}

	switch k.Name {
	case KeyEnter:
		if !k.Alt {
			e.finish(string(e.line), 0, nil)
			return Claimed
		}
		e.insert('\n') // Alt+Enter: the universal line break
	case KeyShiftEnter:
		e.insert('\n')
	case KeyCtrl:
		return e.control(k.Rune)
	case KeyRune:
		e.insert(k.Rune)
	case KeyBackspace:
		if e.cur > 0 {
			e.line = slices.Delete(e.line, e.cur-1, e.cur)
			e.cur--
		}
	case KeyDelete:
		e.deleteForward()
	case KeyLeft:
		e.moveBy(-1)
	case KeyRight:
		e.moveBy(1)
	case KeyHome:
		e.cur = e.lineStart()
	case KeyEnd:
		e.cur = e.lineEnd()
	case KeyUp:
		e.walkHistory(-1)
	case KeyDown:
		e.walkHistory(1)
	default:
		// KeyTab (no completion) and KeyEsc. Esc at a prompt is NOT an
		// abort: retraction is a property of a turn in flight, and a line
		// being typed has nothing to retract.
		return Claimed
	}
	e.render()
	return Claimed
}

// control handles the emacs-style control keys. Ctrl-C is the interesting
// one and it is the whole reason ISIG had to go — see [editor.interrupt].
func (e *editor) control(r rune) Disposition {
	switch r {
	case 'c':
		return e.interrupt()
	case 'd':
		if len(e.line) == 0 {
			// End of input at an empty line. The REPL's clean exit.
			e.finish("", 0, io.EOF)
			return Claimed
		}
		e.deleteForward()
	case 'a':
		e.cur = e.lineStart()
	case 'e':
		e.cur = e.lineEnd()
	case 'b':
		e.moveBy(-1)
	case 'f':
		e.moveBy(1)
	case 'k':
		// The two kills stay BUFFER-scoped where Home/End are line-scoped,
		// and the asymmetry is deliberate: motion is about where you are
		// typing, but a kill is how a recalled multi-line entry gets cleared,
		// and a Ctrl-U that emptied one segment of six would make clearing a
		// block a repeated keystroke.
		e.line = e.line[:e.cur]
	case 'u':
		e.line = slices.Delete(e.line, 0, e.cur)
		e.cur = 0
	case 'w':
		e.killWord()
	default:
		return Claimed // unbound: swallowed, never echoed as a glyph
	}
	e.render()
	return Claimed
}

// interrupt is Ctrl-C, and its disposition is keyed on the BUFFER.
//
// That keying is the entire argument for clearing ISIG (arbitration item
// 2): an async signal handler cannot read the editor's buffer without
// racing the editor, so with ISIG on, "clear the line" — the reference
// behaviour — is not expressible at all. In band it is three lines.
//
//   - Non-empty line: clear it. The session survives, and the cleared text
//     does NOT enter history: it was never submitted.
//   - Empty line, first press: hint (item 3).
//   - Empty line, second consecutive press: abort the read. The REPL reads
//     that as /exit with a different key.
func (e *editor) interrupt() Disposition {
	if len(e.line) > 0 {
		e.line, e.cur, e.ctrlC, e.hint = e.line[:0], 0, 0, ""
		e.render()
		return Claimed
	}
	e.ctrlC++
	if e.ctrlC >= 2 {
		e.finish("", 0, ErrAborted)
		return Claimed
	}
	e.hint = ctrlCHint
	e.render()
	return Claimed
}

// insert puts one rune at the cursor. '\n' goes in like any other: the
// buffer is a flat rune slice and the row break is a rendering fact, not a
// data-structure one — which is what keeps every motion, kill and deletion
// below unaware that multi-line input exists.
func (e *editor) insert(r rune) {
	e.line = slices.Insert(e.line, e.cur, r)
	e.cur++
}

// moveBy is rune motion, so ←/→ cross an embedded newline exactly as they
// cross any other rune — the cursor passes from the end of one logical
// line to the start of the next in one press, with nothing to special-case.
func (e *editor) moveBy(d int) {
	if n := e.cur + d; n >= 0 && n <= len(e.line) {
		e.cur = n
	}
}

// lineStart and lineEnd bound the LOGICAL LINE the cursor is on: the run
// between two embedded newlines, or between one and an end of the buffer.
//
// That is the choice Home/End and Ctrl-A/Ctrl-E make, and it is the
// readline one: in a multi-line buffer "the line" is the segment you are
// typing on, not the whole draft. Buffer ends stay reachable — they are
// the segment ends of the first and last segments — and the kills are
// deliberately not scoped this way; see [editor.control].
func (e *editor) lineStart() int {
	for i := e.cur; i > 0; i-- {
		if e.line[i-1] == '\n' {
			return i
		}
	}
	return 0
}

func (e *editor) lineEnd() int {
	for i := e.cur; i < len(e.line); i++ {
		if e.line[i] == '\n' {
			return i
		}
	}
	return len(e.line)
}

func (e *editor) deleteForward() {
	if e.cur < len(e.line) {
		e.line = slices.Delete(e.line, e.cur, e.cur+1)
	}
}

// killWord deletes back to the start of the previous word: trailing
// whitespace first, then the run of non-whitespace.
func (e *editor) killWord() {
	i := e.cur
	for i > 0 && unicode.IsSpace(e.line[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.line[i-1]) {
		i--
	}
	e.line = slices.Delete(e.line, i, e.cur)
	e.cur = i
}

// walkHistory moves through the recall list, stashing the live line on the
// way up so ↓ can come back to it.
func (e *editor) walkHistory(d int) {
	if e.hist == nil {
		return
	}
	n := len(e.hist.entries)
	idx := e.histIdx + d
	if idx < 0 || idx > n {
		return
	}
	if e.histIdx == n {
		e.stash = slices.Clone(e.line)
	}
	e.histIdx = idx
	if idx == n {
		e.line = slices.Clone(e.stash)
	} else {
		e.line = []rune(e.hist.entries[idx])
	}
	e.cur = len(e.line)
}

// finish completes the read: one final frame with the cursor at the end
// and no hint, then the newline that COMMITS the line to scrollback.
// Callers hold e.mu.
func (e *editor) finish(text string, key rune, err error) {
	if e.finished {
		return
	}
	e.finished = true
	e.retire()
	e.hint = ""
	e.cur = len(e.line)
	e.render()
	e.t.closeEditorLine()
	e.result <- editorResult{text: text, key: key, err: err}
}

// retire takes this editor off the offer chain. Callers hold e.mu.
func (e *editor) retire() {
	if e.reg != nil {
		e.reg.retire()
	}
}

// abandon tears the editor down from the READING goroutine — a cancelled
// context, or end of input while the buffer still had text in it. The line
// is left on screen and the cursor moved off it, so whatever is written
// next starts clean.
func (e *editor) abandon() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return
	}
	e.finished = true
	e.retire()
	e.t.closeEditorLine()
}

// refresh repaints after a window resize.
//
// It deliberately does NOT invalidate the block. Invalidating means "paint
// a fresh one wherever the cursor now stands", and a fresh paint per
// SIGWINCH is a COMMITTED COPY of the input per SIGWINCH — a window drag on
// macOS delivers a burst of them, so a single drag left a stack of stale
// half-width copies in scrollback. The block is still the editor's after a
// resize; only its geometry moved, and [editor.blockTop] recomputes where
// its first row went.
func (e *editor) refresh() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return
	}
	e.render()
}

// --- rendering --------------------------------------------------------

// render repaints the whole block. Callers hold e.mu.
//
// The sequence is: return to the block's first row, erase from there to
// the end of the screen, write every row, then walk the cursor back to
// the cell it belongs in. Four sequences and no absolute positioning —
// personant is a scrollback CLI, and the editor may repaint exactly the
// rows it drew itself.
//
// That last clause is bug 5, structurally: the block start is reached by
// moving up by the number of rows THIS editor emitted, so a committed
// preamble one row above is unreachable by construction rather than by
// arithmetic that has to be got right.
func (e *editor) render() {
	t := e.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ansi {
		return
	}
	// The two transient occupants of the live line go first: an editor and
	// a status frame cannot share a row, and a reasoning run must be closed
	// before anything is drawn under it.
	t.retireStatusLocked()
	t.endReasoningLocked()

	cols := effectiveCols(t.sizeLocked().Cols)
	rows, curRow, curCol := e.layout(cols)

	var b strings.Builder
	if t.edDrawn {
		b.WriteString("\r")
		if up := e.blockTop(cols); up > 0 {
			fmt.Fprintf(&b, cursorUp, up)
		}
	} else if t.column != ColumnStart {
		// Committed content did not end its line (or a child owned the fd
		// and the column is unknown). Take a fresh row rather than draw
		// over it — the deterministic answer, not a guess.
		b.WriteString("\n")
	}
	b.WriteString(eraseDown)
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(r)
	}
	b.WriteString("\r")
	if up := len(rows) - 1 - curRow; up > 0 {
		fmt.Fprintf(&b, cursorUp, up)
	}
	if curCol > 0 {
		fmt.Fprintf(&b, cursorRight, curCol)
	}
	t.emitControlLocked(b.String())
	t.edDrawn, t.edRow, t.edCols = true, curRow, cols
	t.column = ColumnMid
}

// blockTop reports how many rows above the cursor the drawn block's first
// row sits. Callers hold e.mu and t.mu.
//
// While the width is unchanged that is just t.edRow — the row the previous
// frame left the cursor on, recorded rather than derived. After a RESIZE it
// is not: the terminal re-wrapped the rows underneath us.
//
// # The arithmetic follows what render EMITS, not what it lays out
//
// [editor.render] writes a '\n' BETWEEN rows, and [ModeSession] leaves
// OPOST on, so each of those is a CR/LF and every row of the block is its
// own logical line as far as the terminal is concerned. A reflowing
// terminal therefore re-wraps each row INDEPENDENTLY and never rejoins two
// of them: the block's new height is the SUM of the per-row heights, not
// one line's worth of wrap arithmetic over prompt+cursor. Widening cannot
// change the count at all (every row was already at most one screen row
// wide, and hard-separated rows do not merge); only narrowing does.
//
// # Two things it relies on, both stated rather than assumed
//
// The editor's buffer cannot have moved since the frame drawn at t.edCols:
// every mutation ends in a render, and the key path and the resize path are
// the same (pump) goroutine. Laying the buffer out again at the OLD width
// therefore reproduces the rows that are on the screen.
//
// And the terminal reflows. Every terminal personant targets on darwin and
// linux does (Terminal.app, iTerm2, kitty, ghostty, wezterm, Zed). One that
// does NOT — tmux is the notable case — leaves the rows where they were, so
// on a NARROWING resize this walks up too far and erases a row above the
// block. That is the trade taken deliberately: the alternative is the stale
// copy per SIGWINCH that this replaces, which every terminal got.
func (e *editor) blockTop(cols int) int {
	t := e.t
	if t.edCols == cols {
		return t.edRow
	}
	rows, curRow, curCol := e.layout(t.edCols)
	up := curCol / cols
	for _, r := range rows[:curRow] {
		up += wrapRows(cellWidth(r), cols)
	}
	return up
}

// wrapRows is how many screen rows a logical line of n cells occupies at
// width w. n == w is ONE row, not two: writing the final column leaves the
// terminal in pending wrap, where the wrap has not happened yet — the same
// edge [editor.layout]'s trailing row exists for.
func wrapRows(n, w int) int {
	if n <= w {
		return 1
	}
	return (n + w - 1) / w
}

// effectiveCols is the width to lay out at — the ONE place the missing
// geometry falls back, so a block's recorded width and the width it was
// wrapped at cannot disagree.
func effectiveCols(cols int) int {
	if cols < 1 {
		return DefaultCols
	}
	return cols
}

// layout wraps prompt+line into rendered rows and reports which cell of
// which row the cursor occupies.
//
// # Two causes of a row break, one row list
//
// A row ends either because the text ran out of columns (a soft wrap) or
// because the buffer holds a '\n' (a hard break). They produce the same
// thing — one more entry in rows — because [editor.render] emits a '\n'
// BETWEEN every pair of rows regardless, so the terminal already treats
// each rendered row as its own logical line. That is also why
// [editor.blockTop]'s re-wrap arithmetic needs no change: it sums per-row
// heights, and a hard-broken row was already a row that cannot rejoin its
// neighbour.
//
// # The trailing row, and where it is NOT
//
// The LAST logical line lays out to len/cols + 1 rows, ALWAYS: a line that
// exactly fills its final row leaves the terminal in pending wrap, where
// the cursor has no addressable cell, so the trailing (often empty) row is
// what gives it somewhere to be. Every other line editor that gets this
// right does the same thing.
//
// A logical line with a hard break after it gets NO such row: the break
// itself moves the cursor off the filled row, and an extra row there would
// draw a blank line the user never typed. The one consequence is at the
// pending-wrap edge — a cursor sitting at the end of a hard-broken line
// that exactly fills the width renders at column 0 of the row below, which
// is where a terminal would put the next glyph anyway.
func (e *editor) layout(cols int) (rows []string, curRow, curCol int) {
	cols = effectiveCols(cols)
	lines, curLine, curCell := e.logicalLines()

	rows = make([]string, 0, len(lines)+1)
	for i, l := range lines {
		n := wrapRows(len(l), cols)
		if i == len(lines)-1 {
			n = len(l)/cols + 1 // the trailing row, on the last line only
		}
		if i == curLine {
			curRow, curCol = len(rows)+curCell/cols, curCell%cols
		}
		for r := range n {
			lo := min(r*cols, len(l))
			rows = append(rows, string(l[lo:min(lo+cols, len(l))]))
		}
	}

	// The row cap (arbitration item 6). Without it a 5000-rune line
	// recalled with ↑ at width 40 is 125 rows repainted on EVERY keystroke,
	// and §4.1 makes the overflow unerasable — content taller than the
	// terminal has scrolled out of the region an editor may repaint. The
	// excess collapses to ONE placeholder row and the buffer is untouched.
	if len(rows) > e.rowCap {
		visible := e.rowCap - 1
		start := min(len(rows)-visible, curRow)
		kept := make([]string, 0, visible+1)
		kept = append(kept, truncateCells(fmt.Sprintf(inputOverflowFormat, start), cols-StatusColumnInset))
		kept = append(kept, rows[start:start+visible]...)
		rows, curRow = kept, curRow-start+1
	}
	if e.hint != "" {
		rows = append(rows, truncateCells(e.hint, cols-StatusColumnInset))
	}
	return rows, curRow, curCol
}

// logicalLines splits prompt+buffer at every embedded newline, and reports
// which of those lines the cursor is on and how many cells into it.
//
// The prompt belongs to the FIRST line and to no other: it is drawn once,
// and a continuation row of a multi-line entry starts at column 0. That is
// the one asymmetry the cursor arithmetic above has to carry.
func (e *editor) logicalLines() (lines [][]rune, curLine, curCell int) {
	first := make([]rune, 0, e.promptW+len(e.line))
	lines = [][]rune{append(first, []rune(e.prompt)...)}
	curLine, curCell = 0, len(lines[0])
	for i, r := range e.line {
		last := len(lines) - 1
		if i == e.cur {
			curLine, curCell = last, len(lines[last])
		}
		if r == '\n' {
			lines = append(lines, nil)
			continue
		}
		lines[last] = append(lines[last], r)
	}
	if e.cur >= len(e.line) {
		curLine, curCell = len(lines)-1, len(lines[len(lines)-1])
	}
	return lines, curLine, curCell
}

// --- the Terminal half of the editor's state ---------------------------

// closeEditorLine commits the edited line: the cursor leaves the block on
// a fresh row, and the block stops being repaintable. Everything written
// afterwards is below it.
func (t *Terminal) closeEditorLine() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ansi {
		t.emitControlLocked("\n")
	}
	t.column = ColumnStart
	t.forgetEditorBlockLocked()
}

// invalidateEditorBlock forgets where the block is, so the next render
// starts a new one wherever the cursor now stands. Two callers, and both
// mean the rows the editor drew are genuinely no longer the rows above the
// cursor: every COMMITTED write (see [Terminal.write]), which scrolled them
// away, and a return from suspend, after a shell owned the screen.
//
// A RESIZE is deliberately NOT one of them. The rows moved, but they are
// still the editor's and it can still find them — see [editor.blockTop].
// Invalidating there committed a stale copy of the input on every SIGWINCH.
func (t *Terminal) invalidateEditorBlock() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.forgetEditorBlockLocked()
}

// forgetEditorBlockLocked drops the block's position. Callers hold t.mu.
func (t *Terminal) forgetEditorBlockLocked() {
	t.edDrawn, t.edRow, t.edCols = false, 0, 0
}

// inputRowCap resolves [Options.InputRowCap] against the current geometry.
// The floor of 2 is structural: one row is the placeholder, so a cap of 1
// would render a line the user cannot see at all.
func (t *Terminal) inputRowCap() int {
	n := t.rowCapOpt
	if n <= 0 {
		n = min(DefaultInputRowCap, t.Size().Rows-2)
	}
	return max(n, 2)
}

// history returns the session's recall store, or nil when there is none.
func (t *Terminal) history() *history {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hist
}

// cellWidth is how many terminal columns s occupies.
//
// It counts RUNES. There is no wcwidth in the standard library and no
// dependency is worth one here: personant's prompts are ASCII, and the
// failure mode for a double-width glyph in the LINE is a cursor one column
// out until the next repaint, not corruption. Stated rather than hidden.
func cellWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
