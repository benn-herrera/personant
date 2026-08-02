package term

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/term/screentest"
)

// The editor, driven end to end: scripted BYTES into the real device fake,
// through the real decoder, the real pump and the real editor, with every
// emitted byte replayed through the fail-closed screen model.
//
// This is the coverage W2 could not have. liner hardcoded the process's
// own stdin, so a test would have been typing into the terminal running
// the suite; nothing about the edited line was assertable, and bugs 2 and
// 5 were verifiable only by a human. Owning the fd is what buys this.
//
// Nothing here sleeps or polls. The synchronisation is that ReadLine
// returns only after the pump has dispatched every key ahead of the one
// that completed the line, and the final frame is emitted before the
// result is sent.

const editorRows = 24

// newEditorFixture opens a scripted terminal, optionally with a history
// file already populated.
func newEditorFixture(t *testing.T, size Size, hist []string, script []scriptStep) *fixture {
	t.Helper()
	opts := []func(*Options){}
	if hist != nil {
		path := filepath.Join(t.TempDir(), "history")
		body := ""
		for _, h := range hist {
			body += h + "\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("seed history: %v", err)
		}
		opts = append(opts, func(o *Options) { o.HistoryFile = path })
	}
	return newScripted(t, "xterm-256color", size, script, opts...)
}

// replay reconstructs what the user would have seen. It FAILS on any
// sequence the model does not interpret, so every test below is also a
// standing check that the editor's emitted alphabet has not grown without
// screentest growing with it.
func replay(t *testing.T, f *fixture, size Size, stream string) *screentest.Screen {
	t.Helper()
	s := screentest.New(size.Cols, size.Rows)
	if err := s.Feed([]byte(stream)); err != nil {
		t.Fatalf("%v\n\nemitted stream: %q", err, stream)
	}
	return s
}

// liveScreen is the screen as it stood while the user was still typing —
// the whole stream minus the single newline that COMMITS the line. That
// newline is always the editor's last byte, and stripping it is what lets
// the cursor cell be asserted at all.
func liveScreen(t *testing.T, f *fixture, size Size) *screentest.Screen {
	t.Helper()
	return replay(t, f, size, strings.TrimSuffix(f.screen(), "\n"))
}

func readOne(t *testing.T, f *fixture, q Question) (Answer, error) {
	t.Helper()
	return f.t.ReadLine(context.Background(), ActivityEditor, q)
}

// --- C5: the wrapped recalled line ------------------------------------

// SOLUTION.md §6's worked example, verbatim: a long line recalled with ↑
// at a narrow terminal is FULLY VISIBLE, wrapped across rows, with the
// cursor in the right cell and NO TRUNCATION MARKER.
//
// The marker is the specific thing: liner's single-line mode horizontally
// scrolled a long line to a window centred on the cursor and prefixed it
// with a literal "{", so recalling a long prompt showed the user the tail
// of their own sentence starting mid-token. That is bug 2, and "no `{`
// anywhere on the screen" is how it stays fixed.
func TestEditor_RecalledLineWrapsFullyVisibleWithNoTruncationMarker(t *testing.T) {
	const width = 40
	size := Size{Cols: width, Rows: editorRows}
	recalled := strings.Repeat("the quick brown fox jumps over it ", 3)[:95]

	f := newEditorFixture(t, size, []string{recalled}, []scriptStep{typed("\x1b[A\r")})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if ans.Text != recalled {
		t.Fatalf("recalled %q, want %q", ans.Text, recalled)
	}

	s := liveScreen(t, f, size)
	if strings.Contains(strings.Join(s.All(), ""), "{") {
		t.Errorf("a truncation marker reached the screen — bug 2's rendering\n%s", s)
	}
	// Every rune of the line is on screen, in order, across the rows. The
	// model reports a row without its trailing blanks, so each row is
	// compared to its own slice trimmed the same way.
	full := []rune("> " + recalled)
	for i := 0; i*width < len(full); i++ {
		want := strings.TrimRight(string(full[i*width:min((i+1)*width, len(full))]), " ")
		if got := s.Row(i); got != want {
			t.Errorf("row %d = %q, want %q — the recalled line is not fully visible\n%s",
				i, got, want, s)
		}
	}
	// The wrap math: prompt + line = 97 cells at width 40 is 3 rows, and
	// the cursor sits after the last rune of the third.
	wantRow, wantCol := (2+len(recalled))/width, (2+len(recalled))%width
	if row, col := s.Cursor(); row != wantRow || col != wantCol {
		t.Errorf("cursor at (%d,%d), want (%d,%d)\n%s", row, col, wantRow, wantCol, s)
	}
	if s.Scrolls() != 0 {
		t.Errorf("a 3-row line scrolled a 24-row screen %d time(s)\n%s", s.Scrolls(), s)
	}
}

// Arbitration item 6: the rendered rows are CAPPED and the excess collapses
// to one placeholder — while the buffer is returned whole. Without the cap,
// 5000 runes at width 40 is 125 rows repainted on every keystroke, and §4.1
// makes the overflow unerasable, so unbounded wrapping turns bug 2 into a
// strictly worse bug rather than fixing it.
func TestEditor_InputRowCapCollapsesTheExcessAndKeepsTheBuffer(t *testing.T) {
	const width = 40
	size := Size{Cols: width, Rows: 8} // cap = min(10, Rows-2) = 6
	long := strings.Repeat("abcdefghij", 30)

	f := newEditorFixture(t, size, []string{long}, []scriptStep{typed("\x1b[A\r")})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if ans.Text != long {
		t.Fatalf("the cap truncated the ANSWER: got %d runes, want %d", len(ans.Text), len(long))
	}

	s := liveScreen(t, f, size)
	rows := 0
	for _, r := range s.Rows() {
		if r != "" {
			rows++
		}
	}
	if want := f.t.inputRowCap(); rows > want {
		t.Errorf("rendered %d rows, want at most the cap of %d\n%s", rows, want, s)
	}
	if !strings.Contains(strings.Join(s.All(), "\n"), "row(s) above") {
		t.Errorf("the collapsed rows left no placeholder\n%s", s)
	}
	// The TAIL is what stays visible, because that is where the cursor is.
	if !strings.Contains(strings.Join(s.Rows(), ""), long[len(long)-40:]) {
		t.Errorf("the cursor's own rows were the ones collapsed\n%s", s)
	}
	if s.Scrolls() != 0 {
		t.Errorf("a capped block still scrolled %d time(s) — the cap exists to stop exactly that\n%s",
			s.Scrolls(), s)
	}
}

// --- editing ----------------------------------------------------------

// The editing keys, as bytes off a terminal. Table-driven because each row
// is one independently verifiable claim about the buffer, and the buffer is
// the thing a user would notice being wrong.
func TestEditor_EditingKeys(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{"plain typing", "hello\r", "hello"},
		{"multi-byte runes are inserted whole", "héllo\r", "héllo"},
		{"backspace", "hello\x7f\r", "hell"},
		{"backspace at the start of the line is a no-op", "\x7f\x7fab\r", "ab"},
		{"left arrow then insert", "abc\x1b[D\x1b[Dx\r", "axbc"},
		{"right arrow past the end is a no-op", "ab\x1b[C\x1b[Cx\r", "abx"},
		{"delete forward", "abc\x1b[D\x1b[3~\r", "ab"},
		{"home", "abc\x1b[Hx\r", "xabc"},
		{"end", "abc\x1b[Hx\x1b[Fy\r", "xabcy"},
		{"ctrl-a and ctrl-e", "abc\x01x\x05y\r", "xabcy"},
		{"ctrl-b and ctrl-f", "abc\x02\x02x\x06y\r", "axbyc"},
		{"ctrl-k kills to the end", "abcdef\x01\x06\x06\x0b\r", "ab"},
		{"ctrl-u kills to the start", "abcdef\x1b[D\x15\r", "f"},
		{"ctrl-w kills the previous word", "one two three\x17\r", "one two "},
		{"ctrl-w skips trailing spaces first", "one two   \x17\r", "one "},
		{"ctrl-d with text deletes forward", "abc\x01\x04\r", "bc"},
		{"tab does nothing — there is no completion", "ab\tc\r", "abc"},
		{"esc at a prompt is not an abort and not a glyph", "ab\x1b\r", "ab"},
		{"an unbound control key is swallowed", "ab\x0ec\r", "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			size := Size{Cols: 80, Rows: editorRows}
			f := newEditorFixture(t, size, nil, []scriptStep{typed(tc.script), idle()})
			ans, err := readOne(t, f, Question{Prompt: "> "})
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if ans.Text != tc.want {
				t.Errorf("line = %q, want %q", ans.Text, tc.want)
			}
			if ans.Key != 0 {
				t.Errorf("Key = %q, want 0 — a submitted line is never a keystroke", ans.Key)
			}
			// The model reports a row without its trailing blanks, so a
			// kill that left a trailing space is compared trimmed.
			s := liveScreen(t, f, size)
			if got, want := s.Row(0), strings.TrimRight("> "+tc.want, " "); got != want {
				t.Errorf("the line on screen = %q, want %q\n%s", got, want, s)
			}
		})
	}
}

// [Question.Default] is a pre-filled, EDITABLE value with the cursor at the
// end — today's PromptWithSuggestion, and the §3.5 summary edit's whole
// user interface.
func TestEditor_DefaultIsPreFilledAndEditable(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{"accepted unedited", "\r", "curator draft"},
		{"edited at the end", "\x7f\x7f\x7f\x7f\x7fgist\r", "curator gist"},
		{"cleared and replaced", "\x15something else\r", "something else"},
		{"edited at the start", "\x01revised \r", "revised curator draft"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEditorFixture(t, size, nil, []scriptStep{typed(tc.script), idle()})
			ans, err := readOne(t, f, Question{Prompt: "edit: ", Default: "curator draft"})
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if ans.Text != tc.want {
				t.Errorf("line = %q, want %q", ans.Text, tc.want)
			}
		})
	}
}

// ↑/↓ walk the recall list and come back to the line the user was typing.
// The stash is the property worth pinning: walking up and back down must
// return the half-written line, not an empty one.
func TestEditor_HistoryWalk(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	hist := []string{"first prompt", "second prompt"}
	// Three reads off one script, which also exercises type-ahead surviving
	// between them: everything after the first Enter waits in the pump's
	// reservoir for the next consumer.
	f := newEditorFixture(t, size, hist, []scriptStep{
		typed("\x1b[A\r"),               // ↑ once: the newest entry
		typed("\x1b[A\x1b[A\r"),         // ↑ twice: the oldest
		typed("half\x1b[A\x1b[Bdone\r"), // ↑ then ↓ returns the stashed line
	})
	for _, want := range []string{"second prompt", "first prompt", "halfdone"} {
		ans, err := readOne(t, f, Question{Prompt: "> "})
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if ans.Text != want {
			t.Errorf("line = %q, want %q", ans.Text, want)
		}
	}
}

// ↑ past the oldest entry and ↓ past the live line are no-ops, not wraps
// and not empties.
func TestEditor_HistoryWalkStopsAtBothEnds(t *testing.T) {
	f := newEditorFixture(t, Size{Cols: 80, Rows: editorRows}, []string{"only entry"},
		[]scriptStep{typed("\x1b[A\x1b[A\x1b[A\x1b[B\x1b[B\x1b[B\r"), idle()})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if ans.Text != "" {
		t.Errorf("line = %q, want the empty live line back", ans.Text)
	}
}

// --- Question.Keys ----------------------------------------------------

// The fast path, finally live. The FIRST keystroke in the set resolves the
// question with no Enter; anything else becomes the first character of a
// normally edited line, so a menu offering "[a]ll / [n]one" still accepts
// "1 3 5". Both halves are required — the menus in chat genuinely have both
// shapes, and a design that forces one loses answers.
func TestEditor_KeysFastPathAndItsFallback(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	tests := []struct {
		name     string
		script   string
		wantText string
		wantKey  rune
	}{
		{"a listed key answers without Enter", "a", "a", 'a'},
		{"an unlisted first key SEEDS the line instead", "1 3 5\r", "1 3 5", 0},
		{"a listed key later in the line is not the fast path", "xa\r", "xa", 0},
		{"Enter first is an ordinary empty line", "\r", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEditorFixture(t, size, nil, []scriptStep{typed(tc.script), idle()})
			ans, err := f.t.ReadLine(context.Background(), ActivityAsk,
				Question{Prompt: "accept? ", Keys: "an"})
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if ans.Text != tc.wantText || ans.Key != tc.wantKey {
				t.Errorf("Answer{Text:%q, Key:%q}, want {%q, %q}",
					ans.Text, ans.Key, tc.wantText, tc.wantKey)
			}
		})
	}
}

// The fast path ECHOES the key it took, so the user sees which menu answer
// was registered rather than a question that vanished.
func TestEditor_KeysFastPathEchoesTheAnswer(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	f := newEditorFixture(t, size, nil, []scriptStep{typed("r"), idle()})
	if _, err := f.t.ReadLine(context.Background(), ActivityAsk, Question{
		Preamble: []string{"thread thr_1 has gone idle — closure suggested."},
		Prompt:   "close as? ",
		Keys:     "rdaw",
	}); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	s := replay(t, f, size, f.screen())
	if got := s.Row(0); got != "thread thr_1 has gone idle — closure suggested." {
		t.Errorf("the committed preamble is not row 0: %q\n%s", got, s)
	}
	if got := s.Row(1); got != "close as? r" {
		t.Errorf("the answered question row = %q, want the prompt with the key echoed\n%s", got, s)
	}
}

// --- Ctrl-C (arbitration items 2 and 3) --------------------------------

// A non-empty buffer: Ctrl-C CLEARS THE LINE. The session survives, the
// cleared text is not submitted, and the next line is read normally.
//
// This disposition is the entire reason ISIG had to go: it is keyed on the
// editor's BUFFER, and an async signal handler cannot read that buffer
// without racing the editor. In band it is three lines.
func TestEditor_CtrlCWithTextClearsTheLine(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	f := newEditorFixture(t, size, nil, []scriptStep{typed("a mistaken thought\x03replacement\r"), idle()})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v — a Ctrl-C with text in the buffer must not end the read", err)
	}
	if ans.Text != "replacement" {
		t.Errorf("line = %q, want %q", ans.Text, "replacement")
	}
	s := liveScreen(t, f, size)
	if strings.Contains(strings.Join(s.All(), ""), "a mistaken thought") {
		t.Errorf("the cleared text is still on screen\n%s", s)
	}
}

// An empty buffer: hint, then a deliberate SECOND press (arbitration item
// 3 — single-press exit is destructive on a typo). The first press must be
// visible and must not survive the read; that pair needs both verification
// layers, which is the §6 argument in miniature.
func TestEditor_CtrlCAtAnEmptyLineHintsThenExitsOnTheSecond(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	f := newEditorFixture(t, size, nil, []scriptStep{typed("\x03\x03"), idle()})
	if _, err := readOne(t, f, Question{Prompt: "> "}); !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted from the second press", err)
	}
	if !strings.Contains(f.screen(), ctrlCHint) {
		t.Errorf("the first press printed no hint:\n%q", f.screen())
	}
	s := replay(t, f, size, f.screen())
	if strings.Contains(strings.Join(s.All(), ""), ctrlCHint) {
		t.Errorf("the hint was not transient — it is still on screen\n%s", s)
	}
}

// The press count is CONSECUTIVE. Anything typed in between spends it, so
// a Ctrl-C now and another one a minute later never adds up to an exit.
func TestEditor_CtrlCPressCountIsConsecutive(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	f := newEditorFixture(t, size, nil, []scriptStep{typed("\x03x\x7f\x03still here\r"), idle()})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v — two non-consecutive presses ended the read", err)
	}
	if ans.Text != "still here" {
		t.Errorf("line = %q, want %q", ans.Text, "still here")
	}
}

// Ctrl-C on a line that was CLEARED by a previous Ctrl-C starts the count
// again, so clear-then-exit takes three presses and never two.
func TestEditor_CtrlCAfterClearingStartsTheCountAgain(t *testing.T) {
	size := Size{Cols: 80, Rows: editorRows}
	f := newEditorFixture(t, size, nil, []scriptStep{typed("text\x03\x03\x03"), idle()})
	if _, err := readOne(t, f, Question{Prompt: "> "}); !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	f2 := newEditorFixture(t, size, nil, []scriptStep{typed("text\x03\x03ok\r"), idle()})
	ans, err := readOne(t, f2, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v — clear-then-one-press must not exit", err)
	}
	if ans.Text != "ok" {
		t.Errorf("line = %q, want %q", ans.Text, "ok")
	}
}

// Ctrl-D at an empty line is end of input — the REPL's clean exit.
func TestEditor_CtrlDAtAnEmptyLineIsEndOfInput(t *testing.T) {
	f := newEditorFixture(t, Size{Cols: 80, Rows: editorRows}, nil, []scriptStep{typed("\x04"), idle()})
	if _, err := readOne(t, f, Question{Prompt: "> "}); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

// --- type-ahead across the prompt→turn boundary ------------------------

// The defect liner made structural, gone: an Esc typed while the model is
// working — behind ordinary type-ahead, and across the boundary where the
// prompt's consumer retired and the turn's had not registered yet — still
// reaches the turn's abort handler.
//
// Under liner those bytes went into one of three private reservoirs and
// were invisible to the watcher reading the same fd. Here they are read
// once, decoded once, and HELD in the pump until somebody can take them.
func TestPump_EscSurvivesTypeAheadAcrossThePromptTurnBoundary(t *testing.T) {
	f := newEditorFixture(t, Size{Cols: 80, Rows: editorRows}, nil, []scriptStep{
		// One burst: the submitted line, then more typing, then the Esc.
		typed("hello\rnoise\x1b"),
	})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if ans.Text != "hello" {
		t.Fatalf("line = %q, want %q", ans.Text, "hello")
	}

	// The turn registers AFTER the Esc was already typed and read.
	aborted := make(chan struct{})
	reg := f.t.Push(ActivityTurn, Handler{
		Key:   func(Key) Disposition { return Declined }, // ordinary typing is discarded, as always
		Abort: func() { close(aborted) },
	})
	defer reg.Pop()
	if !f.t.State().AbortWindow {
		t.Fatal("the abort window did not open with the handler")
	}
	<-aborted // a happens-before, not a duration: the test times out if it never fires
}

// The same reservoir at the other boundary: keys typed between two reads
// are not lost, they are delivered into the NEXT line.
func TestPump_TypeAheadBetweenTwoReadsLandsInTheNextLine(t *testing.T) {
	f := newEditorFixture(t, Size{Cols: 80, Rows: editorRows}, nil,
		[]scriptStep{typed("first\rsecond\r"), idle()})
	for _, want := range []string{"first", "second"} {
		ans, err := readOne(t, f, Question{Prompt: "> "})
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if ans.Text != want {
			t.Errorf("line = %q, want %q", ans.Text, want)
		}
	}
}

// --- ctx cancellation --------------------------------------------------

// W2 honoured ctx AT ENTRY ONLY, and said so: the read blocked inside
// liner, which term could not interrupt without being the one holding the
// fd. It is the one holding the fd now, and readiness is a poll(2)
// argument, so no read blocks longer than one window and a cancellation
// reaches a read ALREADY IN PROGRESS.
//
// The device parks instead of ending the stream, so the read under test is
// genuinely pending rather than racing an EOF.
func TestReadLine_ContextCancellationReachesAReadInProgress(t *testing.T) {
	hold := make(chan struct{})
	f := newEditorFixture(t, Size{Cols: 80, Rows: editorRows}, nil, []scriptStep{typed("half a thought")})
	f.dev.mu.Lock()
	f.dev.hold = hold
	f.dev.mu.Unlock()
	t.Cleanup(func() { close(hold) })

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		ans Answer
		err error
	}
	done := make(chan result, 1)
	go func() {
		ans, err := f.t.ReadLine(ctx, ActivityEditor, Question{Prompt: "> "})
		done <- result{ans, err}
	}()

	cancel()
	got := <-done
	if !errors.Is(got.err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted — the cancellation did not reach the read", got.err)
	}
	if !IsEndOrAbort(got.err) {
		t.Error("IsEndOrAbort did not match the cancellation")
	}
	// The line is left on screen and the cursor moved off it, so whatever
	// the caller writes next starts clean.
	if got := f.t.State().Column; got != ColumnStart {
		t.Errorf("column after an abandoned read = %v, want ColumnStart", got)
	}
	if st := f.t.State(); len(st.Stack) != 0 || st.Violations != 0 {
		t.Errorf("stack=%v violations=%d after an abandoned read", st.Stack, st.Violations)
	}
}

// --- resize ------------------------------------------------------------

// A resize mid-edit re-wraps the line at the new width. term consumes the
// event itself — it is never offered to a handler — and the block is
// invalidated first, because the terminal re-wrapped every row underneath
// us and a repaint that moved up by the recorded count would erase
// somebody else's output.
func TestEditor_ResizeMidEditReWraps(t *testing.T) {
	const narrow = 20
	f := newEditorFixture(t, Size{Cols: 80, Rows: editorRows}, nil, []scriptStep{
		typed("0123456789012345678901234567890123456789"), // 40 runes: one row at 80
		resized(narrow, editorRows),
		typed("\r"),
	})
	ans, err := readOne(t, f, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if len(ans.Text) != 40 {
		t.Fatalf("the resize changed the buffer: %q", ans.Text)
	}
	// Replay at the NEW geometry: the line must be wrapped across rows and
	// still complete.
	s := liveScreen(t, f, Size{Cols: narrow, Rows: editorRows})
	if !strings.Contains(strings.Join(s.All(), ""), "> "+ans.Text) {
		t.Errorf("the line did not re-wrap intact at %d columns\n%s", narrow, s)
	}
	if row, _ := s.Cursor(); row < 2 {
		t.Errorf("42 cells at %d columns should occupy 3 rows; cursor is on row %d\n%s", narrow, row, s)
	}
}

// --- the block boundary (bug 5) ----------------------------------------

// The editor may repaint exactly the rows it drew. A committed preamble
// one row above is unreachable BY CONSTRUCTION — the block start is
// reached by moving up over the editor's own rows — so no amount of
// editing can take the question back.
func TestEditor_NeverRepaintsOverTheCommittedPreamble(t *testing.T) {
	size := Size{Cols: 40, Rows: editorRows}
	preamble := []string{
		"No active project resolved.",
		"  [c] create a new project rooted at this directory",
		"  [s] switch to a known project",
	}
	f := newEditorFixture(t, size, nil, []scriptStep{typed("some long typed answer that wraps\r"), idle()})
	if _, err := f.t.ReadLine(context.Background(), ActivityAsk, Question{
		Preamble: preamble,
		Prompt:   "choice: ",
	}); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	all := strings.Join(replay(t, f, size, f.screen()).All(), "\n")
	for _, want := range preamble {
		// The second line is 51 cells at width 40, so it wraps; assert the
		// part that fits, which is what a repaint would have eaten first.
		head := want
		if len(head) > size.Cols {
			head = head[:size.Cols]
		}
		if !strings.Contains(all, head) {
			t.Errorf("the editor repainted over a committed preamble line %q\n%s", head, all)
		}
	}
}

// A committed write while the editor holds the line does not corrupt the
// repaint: the block is invalidated, so the next frame starts BELOW the
// content rather than moving up over it.
func TestEditor_CommittedWriteInvalidatesTheBlock(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	f.t.mu.Lock()
	f.t.edDrawn, f.t.edRow = true, 3
	f.t.mu.Unlock()

	io.WriteString(f.t.Out(), "a line of body text\n")

	f.t.mu.Lock()
	drawn, row := f.t.edDrawn, f.t.edRow
	f.t.mu.Unlock()
	if drawn || row != 0 {
		t.Errorf("edDrawn=%v edRow=%d after a committed write, want false and 0", drawn, row)
	}
}

// The ephemeral slot is SUSPENDED while a read is in flight. Two owners of
// one line is the defect this package exists to remove, and a status frame
// drawn over a line being typed is exactly that — the editor's next
// repaint would have to account for a newline it did not emit.
// It is asserted at the invariant rather than through a choreography: the
// contended instant is "an editor is on the terminal", which is one field,
// and driving a whole turn just to reach it would be testing the fixture.
func TestEditor_StatusSlotIsSuspendedWhileTheEditorHoldsTheLine(t *testing.T) {
	f := newTTY(t, "xterm", 80)

	f.t.mu.Lock()
	f.t.ed = &editor{t: f.t}
	f.t.mu.Unlock()
	f.t.Status().Set("closing (4s)")
	if got := f.t.State().Status; got != "" {
		t.Errorf("the slot took the line from the editor: %q", got)
	}
	if got := f.screen(); got != "" {
		t.Errorf("the slot emitted %q over a line being typed", got)
	}

	// …and it is available again the moment the read completes.
	f.t.mu.Lock()
	f.t.ed = nil
	f.t.mu.Unlock()
	f.t.Status().Set("closing (5s)")
	if got := f.t.State().Status; got != "closing (5s)" {
		t.Errorf("the slot stayed suspended after the read: %q", got)
	}
}
