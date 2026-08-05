package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The read path, PLAIN backend: the contract every caller depends on off a
// terminal term owns — validation, the preamble/prompt split (R2-14), the
// Default and Keys semantics, the buffered end-of-input shapes, and the
// bookkeeping the ownership sensor reads.
//
// The EDITOR half is editor_test.go, which drives the same ReadLine over a
// scripted device. In W2 that was impossible — liner hardcoded the
// process's own stdin, so a test would have been typing into the terminal
// running the suite — and it is the coverage W3 buys.

// newReadFixture opens a plain-backend terminal over a scripted stdin.
func newReadFixture(t *testing.T, script string) *fixture {
	t.Helper()
	f := &fixture{}
	tm, err := Open(Options{Stdin: strings.NewReader(script), Stdout: &f.plain, Stderr: &f.diag})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	f.t = tm
	return f
}

// A malformed Question or an activity that cannot read a line is a CALLER
// error, reported rather than absorbed — and reported before anything is
// pushed or written, so a bad call leaves no residue on the stack or the
// screen.
func TestReadLine_CallerErrors(t *testing.T) {
	tests := []struct {
		name string
		act  Activity
		q    Question
	}{
		{"turn cannot read a line", ActivityTurn, Question{Prompt: "> "}},
		{"child cannot read a line", ActivityChild, Question{Prompt: "> "}},
		{"unknown activity", Activity("wat"), Question{Prompt: "> "}},
		{"keys and default are exclusive", ActivityAsk, Question{Prompt: "> ", Keys: "yn", Default: "d"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReadFixture(t, "y\n")
			_, err := f.t.ReadLine(context.Background(), tc.act, tc.q)
			if !errors.Is(err, ErrQuestion) {
				t.Fatalf("err = %v, want ErrQuestion", err)
			}
			// IsEndOrAbort must NOT match it: a resolver that treated "I
			// called this wrong" as "the user declined" would swallow its
			// own bug.
			if IsEndOrAbort(err) {
				t.Error("IsEndOrAbort matched a caller error")
			}
			if got := f.screen(); got != "" {
				t.Errorf("a rejected question still wrote %q", got)
			}
			if st := f.t.State(); len(st.Stack) != 0 || st.Violations != 0 {
				t.Errorf("stack = %v violations = %d, want empty and 0", st.Stack, st.Violations)
			}
		})
	}
}

// R2-14, the constraint the whole wave turns on: the preamble is COMMITTED
// and only the single-line prompt reaches the editor. Off a TTY that makes
// the bytes exactly what the seven sites used to print with Fprint,
// followed by the prompt the empty-prompt reader never echoed.
func TestReadLine_PreambleIsCommittedAndOnlyThePromptIsTheEditor(t *testing.T) {
	f := newReadFixture(t, "c\n")
	ans, err := f.t.ReadLine(context.Background(), ActivityAsk, Question{
		Preamble: []string{"No active project resolved.", "  [c] create", "  [s] switch"},
		Prompt:   "choice: ",
		Keys:     "cs",
	})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if ans.Text != "c" {
		t.Errorf("Text = %q, want %q", ans.Text, "c")
	}
	const want = "No active project resolved.\n  [c] create\n  [s] switch\nchoice: "
	if got := f.screen(); got != want {
		t.Errorf("emitted bytes:\n got %q\nwant %q", got, want)
	}
}

// The ephemeral status slot cannot survive a question: the preamble goes
// out on the content channel, and a committed write retires the slot. That
// is the §5 invariant, asserted where a menu meets a running indicator.
func TestReadLine_RetiresTheStatusSlotBeforeAsking(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	f.t.Status().Set("waiting (3s)")
	if f.t.State().Status == "" {
		t.Fatal("the slot did not take the line")
	}

	// Interactive but NOT a real terminal (injected platform), so the read
	// falls to the plain path and ends at EOF with nothing typed.
	_, err := f.t.ReadLine(context.Background(), ActivityAsk, Question{
		Preamble: []string{"thread thr_1 has gone idle — closure suggested."},
		Prompt:   "close as? ",
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if got := f.t.State().Status; got != "" {
		t.Errorf("the slot survived the question: %q", got)
	}
	if got := f.screen(); strings.Contains(got, "waiting (3s)\nthread") {
		t.Errorf("the question was committed on top of a live slot: %q", got)
	}
}

// The plain backend's Default is a printed hint that an empty line
// accepts — today's PromptWithSuggestion semantics off a TTY, and the
// bytes are part of the contract because the goldens pin them.
func TestReadLine_DefaultOffATTY(t *testing.T) {
	tests := []struct {
		name  string
		typed string
		want  string
	}{
		{"empty line accepts the default", "\n", "curator draft"},
		{"whitespace accepts it too", "   \n", "curator draft"},
		{"a typed line replaces it", "revised gist\n", "revised gist"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReadFixture(t, tc.typed)
			ans, err := f.t.ReadLine(context.Background(), ActivityAsk, Question{
				Prompt:  "edit summary: ",
				Default: "curator draft",
			})
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if ans.Text != tc.want {
				t.Errorf("Text = %q, want %q", ans.Text, tc.want)
			}
			if ans.Key != 0 {
				t.Errorf("Key = %q, want 0 — a submitted line is never a keystroke", ans.Key)
			}
			const wantEcho = "edit summary: [curator draft]\n> "
			if got := f.screen(); got != wantEcho {
				t.Errorf("echo = %q, want %q", got, wantEcho)
			}
		})
	}
}

// Keys, as W2 implements it: the first rune of the SUBMITTED line resolves
// the question when it is in the set, and everything else comes back
// whole so a menu that also takes "1 3 5" still gets it. Answer.Key stays
// 0 throughout — there is no keystroke path in this wave, and claiming one
// would misreport which path produced the answer (see readline.go).
func TestReadLine_KeysResolvesTheFirstRuneOfTheLine(t *testing.T) {
	tests := []struct {
		name  string
		keys  string
		typed string
		want  string
	}{
		{"a listed key", "rdawes", "r\n", "r"},
		{"a word starting with a listed key", "rdawes", "resolved\n", "r"},
		{"an unlisted answer comes back whole", "an", "1 3 5\n", "1 3 5"},
		{"an empty line is not a key", "an", "\n", ""},
		{"no keys means no folding", "", "resolved\n", "resolved"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReadFixture(t, tc.typed)
			ans, err := f.t.ReadLine(context.Background(), ActivityAsk,
				Question{Prompt: "pick: ", Keys: tc.keys})
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if ans.Text != tc.want {
				t.Errorf("Text = %q, want %q", ans.Text, tc.want)
			}
			if ans.Key != 0 {
				t.Errorf("Key = %q, want 0 until W3 owns the keystroke", ans.Key)
			}
		})
	}
}

// End of input and its shapes. A final line WITHOUT a trailing newline is
// returned before io.EOF — the buffered semantics the scripted sessions
// have always relied on.
func TestReadLine_EndOfInput(t *testing.T) {
	f := newReadFixture(t, "trailing line with no newline")
	ans, err := f.t.ReadLine(context.Background(), ActivityEditor, Question{Prompt: "> "})
	if err != nil {
		t.Fatalf("first ReadLine: %v", err)
	}
	if ans.Text != "trailing line with no newline" {
		t.Errorf("Text = %q", ans.Text)
	}

	_, err = f.t.ReadLine(context.Background(), ActivityEditor, Question{Prompt: "> "})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second ReadLine err = %v, want io.EOF", err)
	}
	if !IsEndOrAbort(err) {
		t.Error("IsEndOrAbort did not match io.EOF")
	}
}

// A read on an already-cancelled context does not touch the terminal and
// reports the same disposition the user's own interrupt does, so a
// resolver's single "stop asking" test covers both. W2 honours ctx at
// ENTRY only; W3's pump makes it reach a read in progress.
func TestReadLine_CancelledContext(t *testing.T) {
	f := newReadFixture(t, "y\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.t.ReadLine(ctx, ActivityAsk, Question{Prompt: "sure? "})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if !IsEndOrAbort(err) {
		t.Error("IsEndOrAbort did not match ErrAborted")
	}
	if got := f.screen(); got != "" {
		t.Errorf("a cancelled read still wrote %q", got)
	}
}

// The bookkeeping half: a read pushes its activity for the duration and
// pops it before returning, leaving no residue and no ownership violation
// on ANY exit path. This is the sensor the off-TTY suites read (§6 layer
// 2) — it emits no bytes, which is exactly why it is asserted.
func TestReadLine_PushesAndPopsItsActivity(t *testing.T) {
	f := newReadFixture(t, "one\n")
	for _, act := range []Activity{ActivityEditor, ActivityAsk} {
		if _, err := f.t.ReadLine(context.Background(), act, Question{Prompt: "> "}); err != nil &&
			!errors.Is(err, io.EOF) {
			t.Fatalf("ReadLine(%s): %v", act, err)
		}
		st := f.t.State()
		if len(st.Stack) != 0 {
			t.Errorf("after ReadLine(%s) the stack is %v, want empty", act, st.Stack)
		}
		if st.Violations != 0 {
			t.Errorf("after ReadLine(%s) violations = %d, want 0", act, st.Violations)
		}
		if st.ModeInstalls != 0 {
			t.Errorf("a read installed %d mode(s) on a terminal term does not own", st.ModeInstalls)
		}
	}
}

// AppendHistory off a real terminal is a no-op, and must not build an
// editor to discover that: constructing liner applies its terminal mode,
// and a bookkeeping call is not a moment to change the terminal.
func TestAppendHistory_InertOffARealTerminal(t *testing.T) {
	hp := filepath.Join(t.TempDir(), "history")
	f := &fixture{}
	tm, err := Open(Options{Stdin: strings.NewReader(""), Stdout: &f.plain, HistoryFile: hp})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tm.AppendHistory("a line nobody can recall")
	if err := tm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(hp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a non-terminal session wrote a history file (stat err = %v)", err)
	}
}

// The §4.3.1 persistent history file: append with consecutive dedup and a
// blank-line drop, flush on close, reload on the next open, newest last.
//
// The FORMAT is the point of this test surviving liner's removal: it is
// still newline-delimited text with the same cap, so the file a user has
// been accumulating for weeks is read by the replacement without a
// migration. A change that broke that would break here.
func TestHistoryFileRoundTrip(t *testing.T) {
	hp := filepath.Join(t.TempDir(), "history")

	h1 := loadHistory(hp)
	h1.add("first")
	h1.add("second")
	h1.add("second") // consecutive duplicate — dropped
	h1.add("   ")    // blank — dropped
	if err := h1.flush(); err != nil {
		t.Fatalf("flush h1: %v", err)
	}

	got := readFile(t, hp)
	if strings.Count(got, "second") != 1 {
		t.Errorf("consecutive dedup failed: %q", got)
	}
	if got != "first\nsecond\n" {
		t.Errorf("history file = %q, want the newline-delimited form liner wrote", got)
	}

	// Reload, append, and confirm the prior entries round-tripped.
	h2 := loadHistory(hp)
	h2.add("third")
	if err := h2.flush(); err != nil {
		t.Fatalf("flush h2: %v", err)
	}
	if got2 := readFile(t, hp); got2 != "first\nsecond\nthird\n" {
		t.Errorf("after round-trip = %q, want newest last", got2)
	}
}

// A MULTI-LINE entry round-trips without corrupting the file or the
// entries around it. The file is a positional list of lines, so an
// unescaped newline would not merely mangle the new entry — it would split
// it into two and shift every older entry's meaning.
//
// The content here is deliberately adversarial to the encoding: a newline,
// a literal backslash, and the two-character sequence backslash-'n' that
// the escaped newline is spelled with.
func TestHistoryFile_MultiLineEntriesRoundTrip(t *testing.T) {
	hp := filepath.Join(t.TempDir(), "history")
	entries := []string{
		"before",
		"summarise:\nalpha\nbeta",
		`a windows path C:\temp and a literal \n typed by hand`,
		"after",
	}

	h1 := loadHistory(hp)
	for _, e := range entries {
		h1.add(e)
	}
	if err := h1.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// One line of the FILE per entry: that is the format, and it is what an
	// embedded newline would break.
	if got, want := strings.Count(readFile(t, hp), "\n"), len(entries); got != want {
		t.Errorf("file has %d lines, want %d — an entry split itself\n%q", got, want, readFile(t, hp))
	}

	h2 := loadHistory(hp)
	if len(h2.entries) != len(entries) {
		t.Fatalf("reloaded %d entries, want %d: %q", len(h2.entries), len(entries), h2.entries)
	}
	for i, want := range entries {
		if h2.entries[i] != want {
			t.Errorf("entry %d = %q, want %q", i, h2.entries[i], want)
		}
	}

	// And the encoded form is STABLE: a second write of the same store
	// produces the same bytes, so backslashes cannot accumulate across
	// sessions.
	first := readFile(t, hp)
	if err := h2.flush(); err != nil {
		t.Fatalf("reflush: %v", err)
	}
	if second := readFile(t, hp); second != first {
		t.Errorf("re-encoding changed the file:\n first  %q\n second %q", first, second)
	}
}

// Dedup and the blank-line drop operate on the DECODED entry, which is the
// only form that means anything: two identical multi-line drafts are one
// entry, and two that differ only past their first newline are two.
func TestHistory_DedupOnDecodedMultiLineEntries(t *testing.T) {
	h := &history{}
	h.add("draft:\nalpha")
	h.add("draft:\nalpha") // consecutive duplicate — dropped
	h.add("draft:\nbeta")  // differs only after the newline — kept
	h.add("\n  \n")        // whitespace and newlines only — dropped
	want := []string{"draft:\nalpha", "draft:\nbeta"}
	if len(h.entries) != len(want) {
		t.Fatalf("entries = %q, want %q", h.entries, want)
	}
	for i := range want {
		if h.entries[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, h.entries[i], want[i])
		}
	}
}

// The §4.3.1 cap, asserted at its own boundary rather than by writing a
// thousand lines through an editor: the store drops the OLDEST, so what a
// user can still recall is the most recent HistoryLimit lines.
func TestHistory_CapDropsTheOldest(t *testing.T) {
	h := &history{}
	for i := range HistoryLimit + 5 {
		h.add(fmt.Sprintf("line %d", i))
	}
	if len(h.entries) != HistoryLimit {
		t.Fatalf("kept %d entries, want the %d cap", len(h.entries), HistoryLimit)
	}
	if want := fmt.Sprintf("line %d", HistoryLimit+4); h.entries[len(h.entries)-1] != want {
		t.Errorf("newest entry = %q, want %q", h.entries[len(h.entries)-1], want)
	}
	if want := "line 5"; h.entries[0] != want {
		t.Errorf("oldest surviving entry = %q, want %q", h.entries[0], want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
