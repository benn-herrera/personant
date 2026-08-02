package term

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The read half (SOLUTION.md §5). One path in, two ways to acquire a line
// under it:
//
//   - the EDITOR path (editor.go), on a session where term owns the mode
//     and the reader. Keys arrive decoded, from the one pump, and the
//     editor renders every byte of the line itself.
//   - the PLAIN path, a buffered line reader, everywhere else: pipes,
//     tests, and a terminal that cannot erase.
//
// Which one a session gets is decided ONCE at [Open] — see
// [Terminal.owns], and read the reason there before assuming "interactive"
// is the predicate. It is not: a terminal with no erase sequence cannot
// have an in-place editor at all, and the kernel's own line discipline is
// the honest fallback rather than a half-drawn one of ours.

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
// R2-14). The property survived liner's removal by construction: the
// editor returns to its block by moving up over the rows IT drew, so a
// committed line above is unreachable rather than merely un-drawn-over.
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
// wrong silently.
//
// ctx cancels the read, and from W3 it reaches a read ALREADY IN PROGRESS:
// the pump's readiness is a poll(2) argument, so no read blocks longer
// than one window and the editor can be torn down under a cancelled
// context. An already-cancelled ctx returns [ErrAborted] without touching
// the terminal — the same disposition the user's own interrupt produces,
// so a resolver's single "stop asking" test ([IsEndOrAbort]) covers both.
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

	// Committed BEFORE the activity is pushed, so a key typed while the
	// menu text is going out lands in the pump's reservoir and is offered
	// to the editor the moment it registers. Pushing first would put those
	// keys in front of an editor that has not painted yet.
	t.commitPreamble(q.Preamble)

	if !t.owns {
		reg := t.Push(a, Handler{})
		defer reg.Pop()
		line, err := t.readPlain(q)
		if err != nil {
			return Answer{}, err
		}
		return q.answer(line), nil
	}
	return t.readEdited(ctx, a, q)
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
// It is the SUBMITTED-LINE half only. The [Question.Keys] fast path
// resolves inside the editor, on the first keystroke, and never reaches
// here — which is what makes [Answer.Key] a statement about which path
// produced the answer rather than about its value. The set is still
// matched against the first rune of a typed line, because a menu that
// takes "resolved" as well as "r" is a menu that has both shapes, and off
// a TTY the fast path does not exist at all.
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

// readEdited runs one line through the editor.
//
// The ORDER here is the contract: build, paint, publish, push. The editor
// must be on the terminal before it is on the stack, because the instant
// it is on the stack the pump may offer it a key — including a key the
// user typed while the preamble was still being written.
func (t *Terminal) readEdited(ctx context.Context, a Activity, q Question) (Answer, error) {
	ed := newEditor(t, q)
	ed.start()
	t.mu.Lock()
	t.ed = ed
	t.mu.Unlock()

	// Push and attach under the editor's own lock: the pump must take that
	// lock to deliver a key, so it cannot reach an editor that does not yet
	// know which registration to retire when it completes.
	ed.mu.Lock()
	reg := t.Push(a, Handler{Key: ed.key})
	ed.reg = reg
	ed.mu.Unlock()

	defer func() {
		reg.Pop()
		t.mu.Lock()
		t.ed = nil
		t.mu.Unlock()
	}()

	select {
	case res := <-ed.result:
		return editedAnswer(q, res)
	case <-ctx.Done():
	case <-t.eofCh:
	}
	// Both non-result exits race a result the pump may have produced a
	// moment earlier, and a completed line must never be thrown away for a
	// cancellation that arrived after it.
	select {
	case res := <-ed.result:
		return editedAnswer(q, res)
	default:
	}
	ed.abandon()
	if ctx.Err() != nil {
		return Answer{}, ErrAborted
	}
	return Answer{}, io.EOF
}

func editedAnswer(q Question, res editorResult) (Answer, error) {
	if res.err != nil {
		return Answer{}, res.err
	}
	if res.key != 0 {
		// The fast path: Text is string(Key), so a caller that only cares
		// about the value can switch on Text and never look at Key.
		return Answer{Text: res.text, Key: res.key}, nil
	}
	return q.answer(res.text), nil
}

// readPlain is the non-interactive path: piped stdin, tests, and a
// terminal term does not own. It preserves the buffered semantics exactly
// — a final line without a trailing newline is returned BEFORE io.EOF, and
// a [Question.Default] prints as a hint that an empty line accepts.
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
// A session with no editor has no history at all and this is a no-op,
// exactly as the buffered reader always was — which is also why a piped
// run never writes the file.
func (t *Terminal) AppendHistory(line string) {
	if h := t.history(); h != nil {
		h.add(line)
	}
}
