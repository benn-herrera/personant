package chat

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/peterh/liner"

	"personant/internal/term"
)

// errInputAborted signals the user pressed Ctrl-C at an interactive prompt.
// liner consumes Ctrl-C in raw mode and returns liner.ErrPromptAborted; the
// liner-backed reader maps it to this sentinel so the rest of the chat
// package need not import liner. The REPL loop treats it as a session
// interrupt; the interactive resolvers treat it like io.EOF (decline /
// defer).
var errInputAborted = errors.New("chat: input aborted")

// lineReader is the REPL input seam. Two implementations: a liner-backed
// reader with line editing + persistent history for a real terminal
// (§4.3.1), and a plain buffered reader for piped stdin and tests.
type lineReader interface {
	// prompt writes p (the liner path renders it as an editable prompt),
	// reads one line, and returns it stripped of the trailing newline.
	// io.EOF signals end of input (Ctrl-D / stream end); errInputAborted
	// signals Ctrl-C.
	prompt(p string) (string, error)
	// promptWithDefault reads a line pre-filled with def so the user can
	// edit it in place (the liner path); the buffered path prints def as a
	// hint and returns it unchanged when the user submits an empty line.
	promptWithDefault(p, def string) (string, error)
	// appendHistory records an accepted line for ↑/↓ recall (no-op on the
	// buffered path).
	appendHistory(line string)
	// close restores terminal state and flushes history to disk (no-op on
	// the buffered path).
	close() error
}

// isEndOrAbort reports whether err ends interactive input — end-of-stream
// (Ctrl-D) or an abort (Ctrl-C). Both mean "stop asking".
//
// term.IsEndOrAbort is the same test over term's own sentinels; the two
// coexist only while liner does, and this one goes with it in W3.
func isEndOrAbort(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, errInputAborted)
}

// newLineReader picks the input implementation: liner-backed editing on
// an interactive terminal, a plain buffered reader everywhere else.
//
// The interactivity determination comes from term — axiom §4.2 requires
// exactly ONE TTY predicate, and it now lives in the package that owns
// the terminal rather than being re-derived here from os.Stdin and a
// stat. The non-interactive reader writes its prompt through term's
// content channel, so even the prompt echo is inside the arbiter and the
// cursor-column belief stays honest. The liner path still writes
// straight to the terminal and still reads os.Stdin: that is the
// scaffolding W3 removes, and it is why internal/chat/input.go carries an
// entry on the no-direct-terminal-access allowlist.
func newLineReader(t *term.Terminal, in *bufio.Reader, historyFile string) lineReader {
	if t.Interactive() {
		return newLinerReader(historyFile)
	}
	return &bufLineReader{in: in, out: t.Out()}
}

// bufLineReader is the non-interactive reader: piped stdin and tests. It
// preserves the prior bufio.ReadString semantics — a final line without a
// trailing newline is returned before io.EOF.
type bufLineReader struct {
	in  *bufio.Reader
	out io.Writer
}

func (b *bufLineReader) prompt(p string) (string, error) {
	if p != "" {
		fmt.Fprint(b.out, p)
	}
	s, err := b.in.ReadString('\n')
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

func (b *bufLineReader) promptWithDefault(p, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(b.out, "%s[%s]\n", p, def)
	}
	line, err := b.prompt("> ")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return def, nil
	}
	return line, nil
}

func (b *bufLineReader) appendHistory(string) {}
func (b *bufLineReader) close() error         { return nil }

// linerLineReader wraps a *liner.State for interactive line editing and
// persistent, deduplicated, capped history (§4.3.1). liner enforces the
// spec's default cap natively (liner.HistoryLimit == 1000) and drops
// consecutive duplicates in AppendHistory.
type linerLineReader struct {
	ls          *liner.State
	historyFile string
}

func newLinerReader(historyFile string) *linerLineReader {
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
	return &linerLineReader{ls: ls, historyFile: historyFile}
}

func (l *linerLineReader) prompt(p string) (string, error) {
	s, err := l.ls.Prompt(p)
	if errors.Is(err, liner.ErrPromptAborted) {
		return "", errInputAborted
	}
	return s, err // io.EOF passes through unchanged
}

func (l *linerLineReader) promptWithDefault(p, def string) (string, error) {
	s, err := l.ls.PromptWithSuggestion(p, def, len(def))
	if errors.Is(err, liner.ErrPromptAborted) {
		return "", errInputAborted
	}
	return s, err
}

func (l *linerLineReader) appendHistory(line string) {
	if strings.TrimSpace(line) != "" {
		l.ls.AppendHistory(line)
	}
}

func (l *linerLineReader) close() error {
	if l.historyFile != "" {
		// WriteHistory is goroutine-safe (liner docs), so flushing here is
		// safe even from the force-quit signal path.
		if f, err := os.Create(l.historyFile); err == nil {
			_, _ = l.ls.WriteHistory(f)
			_ = f.Close()
		}
	}
	return l.ls.Close()
}
