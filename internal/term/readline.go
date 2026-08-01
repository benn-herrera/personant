package term

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/peterh/liner"
)

// The read half (SOLUTION.md §5, W2). One path in, two ways to acquire a
// line under it:
//
//   - the EDITOR path, liner, on a session that drives a real terminal.
//     This is scaffolding and it is the last wave it survives: liner
//     hardcodes os.Stdin, applies its mode at construction and holds three
//     byte reservoirs, all of which W3 removes with the pump and decoder.
//     It lives HERE rather than in internal/chat because there may be
//     exactly one of it — two liner.States over one fd would be the defect
//     this package exists to remove, wearing a migration badge.
//   - the PLAIN path, a buffered reader, everywhere else: pipes, tests,
//     and an injected [Options.Platform] (which has no decoder until W3).
//
// Which one a session gets is decided ONCE at [Open], by the same U12 rule
// the backend follows, and is narrower than [Terminal.Interactive]: an
// injected platform is interactive but is NOT a real terminal, and handing
// it to liner would mean liner reading the process's actual stdin behind a
// test's back.
//
// # What W2 does NOT land: the [Question.Keys] first-keystroke fast path
//
// Read this before assuming a single-key menu answers without Enter — it
// does not, yet, and the reason is mechanical.
//
// The fast path needs the FIRST KEYSTROKE before the editor owns the line.
// liner has no hook for it, so the only way to get one while liner still
// holds the fd is to read a byte off the terminal ourselves before calling
// Prompt. Verified against liner v1.2.2, that read is unsafe in three
// independent ways:
//
//  1. liner's bufio.Reader over os.Stdin PERSISTS BETWEEN PROMPTS
//     (input.go: `s.r = bufio.NewReader(os.Stdin)`, built once in
//     NewLiner). Type-ahead already pulled into that reservoir is
//     invisible to a raw read of the fd, so the pre-read would block for a
//     key the user has already pressed. This is precisely the reservoir
//     argument SOLUTION.md §3 used to reject "the lessee reads": a lease
//     can be released at an instant, the lookahead cannot.
//  2. ISIG is cleared only INSIDE liner's Prompt (startPrompt/stopPrompt),
//     so between prompts Ctrl-C is still a signal. A pre-read cannot see
//     it as a byte, which would turn "Ctrl-C declines this menu" into "the
//     session takes a SIGINT while a read stays blocked".
//  3. An ESC-prefixed sequence (an arrow key at a menu) would be split
//     across the two readers, leaving its tail to be typed into the line.
//
// So W2 takes the third option ruled acceptable for this wave: Keys
// questions are ENTER-TERMINATED here, and [Question.Keys] degrades to
// "the first rune of the submitted line, when it is in the set" — which is
// exactly today's menu behaviour, expressed once instead of at seven call
// sites. [Answer.Key] is therefore ALWAYS 0 in W2, because no keystroke
// path exists to set it; callers switch on [Answer.Text], which is what
// [Answer.Key]'s doc says lets the menus keep their shape. W3 owns the fd
// and the decoder and makes the fast path a first event rather than a
// second reader.

// plainDefaultPrompt is what the plain backend prints to collect a line
// for a question carrying a [Question.Default]. It is deliberately NOT
// [Question.Prompt]: the hint line already carried that text, and this is
// the literal the buffered reader has always used. Off a TTY there is
// nothing to pre-fill, so the two-line shape (hint, then a bare prompt) IS
// the semantics, and reproducing it byte for byte is what keeps the
// scenario goldens still.
const plainDefaultPrompt = "> "

// ReadLine is the ONLY read path in personant.
//
// There is deliberately no API that writes text and then separately
// reads a line. That shape is bug 5: seven sites print a question with
// Fprint and then call a reader with an empty prompt, and the reader —
// never told the question exists — repaints from column 0 over it. When
// the question is an argument to the read, the reader knows the width it
// must not repaint above, and the defect has nowhere to live.
//
// [Question.Preamble] is COMMITTED through [Terminal.Out] before the
// editor is entered and only [Question.Prompt] reaches it (constraint
// R2-14, and mandatory rather than tidy — see the field's own doc for the
// erase-math reason). That is also what makes the off-TTY bytes today's
// bytes minus the empty prompt.
//
// It pushes a for the duration of the call and pops it before returning.
// Exactly two activities are legal, and §3's ownership table names them
// separately for a reason:
//
//   - [ActivityEditor] — the read AT THE PROMPT, the outermost input
//     consumer of a session.
//   - [ActivityAsk] — a resolver prompting from INSIDE TURN CLOSE (the
//     §3.4 recall and §3.5 closure offers). It is pushed OVER
//     [ActivityTurn], which is the whole reason the stack is LIFO rather
//     than a single current-owner slot.
//
// [ActivityTurn] and [ActivityChild] return [ErrQuestion]. Neither reads
// a line: a turn watches for its abort key, and a child window has
// handed the fd away entirely.
//
// The activity is an EXPLICIT ARGUMENT rather than something term infers
// from stack state, and the rule is [Handler.Interrupt]'s rule. Inferring
// it — "a read while a turn is on the stack must be an ask" — would make
// the stack consult itself to decide what the caller meant, which §4
// forbids for dispositions and forbids here for the same mechanical
// reason: the windows NEST, so an inference drawn from stack position is
// wrong exactly in the slivers where the nesting is imperfect, and it is
// wrong silently. The caller never has to guess — it knows which of the
// two it is without looking at anything.
//
// ctx cancels the read. In W2 it is honoured AT ENTRY ONLY, and that
// limitation is structural rather than an omission: the read below blocks
// inside liner or inside a buffered Read, neither of which term can
// interrupt without being the one holding the fd. W3's pump makes the
// cancellation reach a read already in progress. An already-cancelled ctx
// returns [ErrAborted] — the same disposition the user's own interrupt
// produces, so a resolver's single "stop asking" test ([IsEndOrAbort])
// covers both without learning a third sentinel.
//
// The error contract is [io.EOF] for end of input (Ctrl-D) and
// [ErrAborted] for an interrupt at the prompt; [IsEndOrAbort] tests for
// either. A caller error — an illegal a, or a malformed q — is
// [ErrQuestion], which IsEndOrAbort deliberately does not match.
//
// AppendHistory is NOT called here. What deserves to be recalled with ↑
// is policy — a menu answer is not a prompt — and the caller decides.
func (t *Terminal) ReadLine(ctx context.Context, a Activity, q Question) (Answer, error) {
	if err := q.validate(a); err != nil {
		return Answer{}, err
	}
	if ctx.Err() != nil {
		return Answer{}, ErrAborted
	}

	reg := t.Push(a, Handler{})
	defer reg.Pop()

	t.commitPreamble(q.Preamble)

	line, err := t.readLine(q)
	if err != nil {
		return Answer{}, err
	}
	return q.answer(line), nil
}

// validate rejects the two caller errors the read boundary can detect
// before it touches the terminal: an activity that does not read a line,
// and the one [Question] field combination that contradicts itself.
//
// Both are wrapped [ErrQuestion] rather than bare, so errors.Is still
// matches while the message says which of the two happened. Nothing is
// pushed or written until this passes.
func (q Question) validate(a Activity) error {
	switch a {
	case ActivityEditor, ActivityAsk:
	default:
		return fmt.Errorf("%w: %q cannot read a line", ErrQuestion, a)
	}
	if q.Keys != "" && q.Default != "" {
		return fmt.Errorf("%w: Keys (%q) and Default (%q) are mutually exclusive",
			ErrQuestion, q.Keys, q.Default)
	}
	return nil
}

// answer projects a submitted line onto the [Answer] contract.
//
// The [Question.Keys] half is W2's degradation, in one place rather than
// at each menu: the first rune of the line resolves the question when it
// is in the set, so "resolved" answers a [r]esolved menu and "1 3 5" still
// falls through to the caller's own parsing. [Answer.Key] stays 0 — no
// keystroke path exists in this wave, and reporting one would be a lie
// about which path the answer came from. See the file header.
func (q Question) answer(line string) Answer {
	if q.Keys == "" {
		return Answer{Text: line}
	}
	for _, r := range line { // the first rune, or no iteration at all
		if strings.ContainsRune(q.Keys, r) {
			return Answer{Text: string(r)}
		}
		break
	}
	return Answer{Text: line}
}

// commitPreamble puts the question's multi-line body into scrollback
// through the content channel, as ONE write: the lines are one utterance,
// and a single write takes the serialization point once and retires the
// ephemeral status slot once.
//
// The write error is dropped for the reason every other decoration write
// drops it — there is no recovery for a terminal that stopped accepting
// bytes, and the payload that matters (the answer) reports its own.
func (t *Terminal) commitPreamble(lines []string) {
	if len(lines) == 0 {
		return
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	_, _ = io.WriteString(t.Out(), b.String())
}

// readLine acquires one line on whichever backend this session opened.
func (t *Terminal) readLine(q Question) (string, error) {
	if ls := t.ensureEditor(); ls != nil {
		return readViaEditor(ls, q)
	}
	return t.readPlain(q)
}

// readViaEditor is the liner scaffolding. The prompt is liner's to render
// — it is the single line the editor may repaint over — and the preamble
// above it is already committed, which is R2-14 satisfied by construction.
//
// term's cursor-column belief is deliberately NOT updated here. liner
// writes straight to the terminal and ends every completed read with a
// newline, so ColumnStart — the belief the committed preamble already left
// behind — is still true when this returns. W3 removes the question by
// making the editor's writes term's own.
func readViaEditor(ls *liner.State, q Question) (string, error) {
	var (
		s   string
		err error
	)
	if q.Default != "" {
		// The cursor sits at the END of the pre-filled value. Runes, not
		// bytes: liner takes a rune index, and a multi-byte default with a
		// byte length would place the cursor past the end of its own line.
		s, err = ls.PromptWithSuggestion(q.Prompt, q.Default, len([]rune(q.Default)))
	} else {
		s, err = ls.Prompt(q.Prompt)
	}
	if errors.Is(err, liner.ErrPromptAborted) {
		return "", ErrAborted
	}
	return s, err // io.EOF passes through unchanged
}

// readPlain is the non-interactive path: piped stdin, tests, and an
// injected platform. It preserves the buffered semantics exactly — a final
// line without a trailing newline is returned BEFORE io.EOF, and a
// [Question.Default] prints as a hint that an empty line accepts.
func (t *Terminal) readPlain(q Question) (string, error) {
	if q.Default == "" {
		return t.readPlainLine(q.Prompt)
	}
	fmt.Fprintf(t.Out(), "%s[%s]\n", q.Prompt, q.Default)
	line, err := t.readPlainLine(plainDefaultPrompt)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return q.Default, nil
	}
	return line, nil
}

func (t *Terminal) readPlainLine(prompt string) (string, error) {
	if prompt != "" {
		fmt.Fprint(t.Out(), prompt)
	}
	s, err := t.reader().ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			if s == "" {
				return "", io.EOF
			}
			return strings.TrimRight(s, "\r\n"), nil
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// reader is the plain backend's ONE buffered reader over the session's
// stdin, built on first use. One is the whole point: a second reservoir
// over the same stream is what strands bytes at a handover.
func (t *Terminal) reader() *bufio.Reader {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.in == nil {
		t.in = bufio.NewReader(t.stdin)
	}
	return t.in
}

// AppendHistory records an accepted line for ↑/↓ recall. Blank lines and
// consecutive duplicates are dropped, and the store is capped at
// [HistoryLimit] entries; the file is flushed at [Terminal.Close].
//
// All three of those are liner's own behaviour in W2 (liner.HistoryLimit
// is 1000, the same number [HistoryLimit] names, which is why the on-disk
// file needs no migration when W3 replaces the editor). Off a real
// terminal there is no history at all and this is a no-op, exactly as the
// buffered reader always was.
//
// It deliberately does not BUILD the editor: constructing liner applies
// its terminal mode, and doing that from a bookkeeping call rather than
// from a read would install a mode at a moment when nothing is prompting.
func (t *Terminal) AppendHistory(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	t.mu.Lock()
	ls := t.editor
	t.mu.Unlock()
	if ls != nil {
		ls.AppendHistory(line)
	}
}

// ensureEditor returns the liner scaffolding, building it on first use,
// and nil on every session that is not a real terminal.
//
// Construction is LAZY rather than at [Open], and the reason is bug 3's:
// liner applies ICANON/ECHO-off at construction and holds it for the whole
// session, so building it early would change the terminal mode before the
// caller had captured the mode a `$`/`#` child must be handed. First use
// is the first prompt, which is no earlier than the old chat-side
// construction ever was.
func (t *Terminal) ensureEditor() *liner.State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.realTTY || t.closed {
		return nil
	}
	if t.editor == nil {
		t.editor = newEditor(t.histFile)
	}
	return t.editor
}

// newEditor builds the liner state: line editing plus persistent,
// deduplicated, capped history (§4.3.1).
func newEditor(historyFile string) *liner.State {
	ls := liner.NewLiner()
	ls.SetCtrlCAborts(true)
	// liner defaults to single-line mode, where a line wider than the
	// terminal is HORIZONTALLY SCROLLED to a window centred on the cursor
	// (line.go refreshSingleLine) and prefixed with a literal "{"
	// truncation marker. Recalling a long prompt with ↑ leaves the cursor
	// at end-of-line, so the user sees the tail of their own prompt
	// starting mid-token. Multi-line mode wraps across terminal rows
	// instead, keeping the whole line visible.
	ls.SetMultiLineMode(true)
	if historyFile != "" {
		if f, err := os.Open(historyFile); err == nil {
			_, _ = ls.ReadHistory(f) // newest last; a malformed tail is tolerated
			_ = f.Close()
		}
	}
	return ls
}

// closeEditor flushes history and hands the terminal mode back. Called
// once, from [Terminal.Close], with the editor already detached from the
// Terminal so no later read can revive it.
//
// A free function rather than a method: it needs the editor and the path
// and nothing else, which is also what lets the history round-trip be
// asserted without a terminal to build a session over.
func closeEditor(ls *liner.State, historyFile string) error {
	if ls == nil {
		return nil
	}
	if historyFile != "" {
		// WriteHistory is goroutine-safe (liner docs), so flushing here is
		// safe even from the force-quit signal path.
		if f, err := os.Create(historyFile); err == nil {
			_, _ = ls.WriteHistory(f)
			_ = f.Close()
		}
	}
	return ls.Close()
}
