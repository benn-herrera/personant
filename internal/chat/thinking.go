package chat

import (
	"io"
	"strings"
)

const (
	// dimOn / dimOff bracket reasoning output in ANSI SGR faint. Faint
	// (2) rather than a colour: it degrades sanely on terminals that
	// render it as normal text, and it makes no assumption about the
	// user's palette. Reset (0) rather than 22 for the same reason —
	// 22 is the exact undo, but 0 is the sequence every terminal
	// implements.
	dimOn  = "\x1b[2m"
	dimOff = "\x1b[0m"

	// thinkingOpen / thinkingClose are the no-ANSI fallback. On a dumb
	// terminal dimming is unavailable, so the distinction has to be
	// carried by words: reasoning that could be mistaken for the answer
	// is the one failure this feature must not have.
	thinkingOpen  = "[thinking] "
	thinkingClose = " [/thinking]"
)

// thinking renders a thinking model's live reasoning stream, dimmed and
// visually separated from the response body.
//
// It is BOTH the reasoning sink (reasoning, wired to turn.State.OnReasoning)
// and the content writer every other REPL producer goes through (Write) —
// deliberately one object, because the whole problem is the handoff
// between them. A reasoning run stays open across many chunks; the moment
// anything else wants the terminal, the run has to be closed first or the
// user's answer arrives dim and jammed onto the tail of a scratch line.
// Making content writes flow through the same object is what guarantees
// that, without a second mechanism to keep in sync.
//
// It wraps progress.writer rather than stdout: every byte it emits still
// retires the wait indicator on its first write and keeps the indicator's
// cursor-column bookkeeping honest. It writes ZERO bytes of its own when
// disabled, so the piped/test/sim paths are byte-for-byte unaffected
// regardless of the config setting.
//
// Not safe for concurrent use, and does not need to be: reasoning() and
// Write() are both called from the turn goroutine, and the toggle runs on
// the REPL loop between turns. Its own terminal writes are unchecked, as
// in progress.go — this is decoration around a stream whose real payload
// reports its own errors, and there is no per-byte recovery for a
// terminal that stopped accepting them.
type thinking struct {
	w io.Writer

	// enabled is the terminal determination AND the user's choice. The
	// terminal half is fixed for the session (interactiveTTY, via the
	// progress indicator); the choice half moves with /thinking.
	interactive bool
	on          bool
	// ansi is the progress indicator's own ANSI determination, shared so
	// the two can never disagree about what the terminal accepts.
	ansi bool

	// active says a reasoning run is open on the terminal — dim is on (or
	// the [thinking] marker has been printed) and nothing else may write
	// until it is closed.
	active bool
}

// newThinking builds the renderer over the progress indicator's writer
// discipline. on is the config default (memops.ChatConfig.ShowThinking).
// The interactivity and ANSI determinations are READ FROM pr rather than
// recomputed, so there is exactly one of each per session.
func newThinking(pr *progress, out io.Writer, on bool) *thinking {
	return &thinking{
		w:           pr.writer(out),
		interactive: pr.enabled,
		on:          on,
		ansi:        pr.animate,
	}
}

// showing reports whether reasoning is currently being displayed. Both
// halves must hold: a non-terminal session shows nothing whatever the
// setting says.
func (t *thinking) showing() bool { return t.interactive && t.on }

// setOn applies the /thinking override for the rest of the session. An
// open run is closed on the way to off so the terminal is not left dim.
func (t *thinking) setOn(on bool) {
	if !on {
		t.end()
	}
	t.on = on
}

// reasoning renders one thinking-mode delta. It is the
// turn.State.OnReasoning hook. Deltas arrive with no line structure of
// their own, so the run is bracketed rather than prefixed per line — a
// per-line prefix would need lookahead the stream does not offer.
func (t *thinking) reasoning(s string) {
	if !t.showing() || s == "" {
		return
	}
	if !t.active {
		t.active = true
		if t.ansi {
			io.WriteString(t.w, dimOn)
		} else {
			io.WriteString(t.w, thinkingOpen)
		}
	}
	io.WriteString(t.w, s)
}

// end closes an open reasoning run and returns the terminal to a clean
// line. Idempotent, and a no-op when no run is open, so callers can fire
// it defensively at any handoff.
func (t *thinking) end() {
	if !t.active {
		return
	}
	t.active = false
	if t.ansi {
		io.WriteString(t.w, dimOff)
	} else {
		io.WriteString(t.w, thinkingClose)
	}
	// Always a newline: the answer must start on its own line, never on
	// the tail of the scratch. The dim reset alone would leave the two
	// sharing a row.
	io.WriteString(t.w, "\n")
}

// Write is the content path — the response body, the §3.4 recall offer,
// the §3.5 closure prompt, the abort notice. Closing any open reasoning
// run first is the entire point: this is the reasoning→content
// transition, and it is handled once, here, for every producer.
func (t *thinking) Write(b []byte) (int, error) {
	if len(b) > 0 {
		t.end()
	}
	return t.w.Write(b)
}

// thinkingStateLabel renders the current setting for /thinking's report.
func thinkingStateLabel(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// parseThinkingArg maps the /thinking argument to a state. ok=false means
// the argument was not recognized (the caller reports usage); the bare
// form (empty arg) reports rather than sets, so it is handled by the
// caller and never reaches here.
func parseThinkingArg(arg string) (on, ok bool) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on":
		return true, true
	case "off":
		return false, true
	default:
		return false, false
	}
}
