package chat

import (
	"io"
	"strings"

	"personant/internal/term"
)

// thinking is the /thinking POLICY — and, since W1, nothing else.
//
// It used to be BOTH the reasoning sink and the content writer every
// other REPL producer went through, deliberately one object because the
// handoff between them was the whole problem: a reasoning run stays open
// across many chunks, and the moment anything else wants the terminal the
// run has to be closed or the user's answer arrives dim and jammed onto
// the tail of a scratch line.
//
// That object existed because the (tty-only, scrollback) cell of the §5
// output model had no name, so thinking.go was the only thing that knew
// about the reasoning-to-content transition and every other producer had
// to route through it to be serialized. term.Reasoning names the cell,
// and the transition is now handled once inside term's serialization
// point — an open run is closed by the first byte on Out or Diag and by
// either Status operation. Naming the cell dissolved the object.
//
// What is left is the two decisions that are genuinely chat's: whether
// the user asked for reasoning to be shown (/thinking, config), and
// reporting that state. Whether it can be shown at all, whether it is
// dimmed or bracketed with words on a dumb terminal, and when the run
// closes are term's.
type thinking struct {
	// w is term's reasoning channel. It writes ZERO bytes when the
	// session is not interactive, so the piped/test/sim paths are
	// byte-for-byte unaffected regardless of the config setting.
	w io.Writer

	// interactive is term's ONE TTY predicate; on is the user's choice.
	// Both halves must hold for anything to be shown, and the terminal
	// half is fixed for the session while the choice half moves with
	// /thinking.
	interactive bool
	on          bool
}

// newThinking builds the renderer over the session's terminal. on is the
// config default (memops.ChatConfig.ShowThinking).
func newThinking(t *term.Terminal, on bool) *thinking {
	return &thinking{w: t.Reasoning(), interactive: t.Interactive(), on: on}
}

// showing reports whether reasoning is currently being displayed. Both
// halves must hold: a non-terminal session shows nothing whatever the
// setting says.
func (t *thinking) showing() bool { return t.interactive && t.on }

// setOn applies the /thinking override for the rest of the session.
//
// It does not close an open reasoning run, because it cannot leave one
// dangling: the command's own reply goes out on term's content channel,
// which closes any open run before it lands. There is deliberately no
// end-the-run call anywhere in this package — that was the second
// mechanism that had to be kept in sync, and term removed the need for
// it.
func (t *thinking) setOn(on bool) { t.on = on }

// reasoning renders one thinking-mode delta. It is the
// turn.State.OnReasoning hook. Deltas arrive with no line structure of
// their own; term brackets the run rather than prefixing per line, since
// a per-line prefix would need lookahead the stream does not offer.
func (t *thinking) reasoning(s string) {
	if !t.showing() || s == "" {
		return
	}
	_, _ = io.WriteString(t.w, s)
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
