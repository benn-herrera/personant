// Package term is the sole owner of personant's terminal: the file
// descriptors, the terminal mode, the single reader, every emitted byte,
// and the ownership state that says who holds what at each instant.
//
// It exists because five bugs shipped in one week of dogfooding and they
// were one defect — multiple independent owners of one terminal (see
// mad-design/terminal-layer/problem-brief.md §2, SOLUTION.md §1). Fixing
// them individually produced better manners between peers; this package
// replaces manners with an arbiter. internal/chat keeps POLICY (what to
// ask, what a key means); internal/shell keeps process execution and
// signal DELIVERY. Everything else becomes a client that requests an
// effect and can never name the fd.
//
// Read mad-design/terminal-layer/SOLUTION.md first; the section
// references below are to that document.
//
// # Migration state
//
// The package lands in waves (SOLUTION.md §8, IMPLEMENTATION-PLAN.md §2).
// W1 landed the OUTPUT half plus the bookkeeping: backend selection, the
// four channels, the activity stack, the abort window, the cursor column.
// W2 landed the SINGLE READ PATH, [Terminal.ReadLine], with liner
// underneath it. W3 — this one — takes the fd: the terminal mode
// (device_unix.go), the single reader (pump.go), the decoder (decode.go)
// and a hand-written editor (editor.go). liner is gone, and with it the
// three byte reservoirs and the second owner of the mode.
//
// What is left for W4 is the SIGINT offer chain at [Handler.Interrupt] —
// which serves the [Terminal.Handoff] window and signals raised from
// outside the terminal, because everything typed while term holds the fd
// is now a decoded key.
//
// # The five bugs and the structural answer to each
//
//  1. progress emitted one line per frame — it tracked "cursor at
//     column 0" and "I own this line" in one flag. Answer: nobody but
//     term tracks either. The ephemeral status slot ([Status]) is the
//     only thing that redraws in place, term owns the cursor column as
//     a tri-state ([CursorColumn]), and no client has a flag to get
//     wrong.
//  2. long recalled input lines rendered mid-token. Answer: one
//     editor, inside term, behind [Terminal.ReadLine]; wrapping is
//     term's business and the terminal width is term's to know.
//  3. Esc was unobservable mid-turn because liner applies its mode for
//     the whole session and re-adopts whatever it finds. Answer: term
//     owns the mode, always, with ZERO prompt/turn transitions (§3).
//  4. Ctrl-C during a `$`/`#` child killed the session. Answer: SIGINT
//     is an offer chain down the activity stack, never a lookup (§4,
//     and [Handler.Interrupt]).
//  5. menu prompts erased by the first keystroke — seven sites print a
//     question and then call a reader that repaints from column 0.
//     Answer: there is exactly one read path and it takes the question
//     ([Question]). No API writes text and then separately reads a
//     line, so the shape that caused bug 5 cannot be spelled.
//
// # Verification posture
//
// Two layers, and neither is sufficient alone. The screen model
// (internal/term/screentest) reconstructs what the user would see from the
// bytes this package emitted and FAILS on any sequence it does not model,
// so writer and model share one finite alphabet and cannot drift. And
// plain-backend bookkeeping parity covers the BYTE-INVISIBLE defect class
// — bugs 3 and 4 emit no bytes, which is exactly why they shipped green —
// by having the non-TTY backend keep the same books as the TTY backend.
// [Terminal.State] is what those assertions read. It is first-class API,
// not a debug helper.
package term

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Sizing constants. Each is used by more than one caller (status
// truncation, the W3 row cap, the editor's wrap math), so each is named
// once here rather than spelled at each site.
const (
	// StatusColumnInset is how many columns the ephemeral status slot
	// leaves unwritten at the right edge. It is 1, and the reason is
	// mechanical, not aesthetic: writing the FINAL column puts
	// xterm-family terminals into pending-wrap, where the next glyph
	// scrolls the screen. A status line that scrolls is bug 1 again.
	StatusColumnInset = 1

	// DefaultCols and DefaultRows are the assumed geometry when the
	// window size cannot be read — a pipe, or an ioctl failure that
	// selected the plain backend (U12). Truncation and the row cap must
	// still have a number.
	DefaultCols = 80
	DefaultRows = 24

	// HistoryLimit is the §4.3.1 cap on recallable input lines, carried
	// over from liner.HistoryLimit so the existing on-disk history file
	// (newline-delimited text) needs no migration.
	HistoryLimit = 1000

	// DefaultInputRowCap is the ceiling half of [Options.InputRowCap]'s
	// default, min(DefaultInputRowCap, Rows-2). See that field for what
	// the cap is for and why an uncapped editor is a worse bug 2.
	DefaultInputRowCap = 10
)

// Options configures a session's terminal. Both output fds are named
// here because BOTH belong to the arbiter: stderr is not a side channel
// that bypasses serialization, it is [Terminal.Diag] and goes through
// the same point (§5). A design where errors reach the terminal by any
// other route reproduces the stderr/indicator collision W1 closes.
type Options struct {
	// Stdin is the input fd. It must be *os.File for the TTY backend to
	// be selectable; anything else selects the plain backend.
	Stdin io.Reader

	// Stdout carries [Terminal.Out], [Terminal.Reasoning] and
	// [Terminal.Status].
	Stdout io.Writer

	// Stderr carries [Terminal.Diag].
	Stderr io.Writer

	// HistoryFile is where [Terminal.ReadLine]'s history is loaded from
	// and flushed to at [Terminal.Close]. Empty disables history.
	//
	// The file is one file and it has one owner: the editor lives inside
	// term (editor.go, history.go), so the load-at-open and flush-at-Close
	// pair live here rather than in a second place that also opens it. The
	// on-disk format is newline-delimited text — liner's — and needs no
	// migration now that liner is gone.
	HistoryFile string

	// TermEnv is $TERM. A "dumb" or empty value means no ANSI: the
	// status slot degrades to one committed line per phase and
	// [Terminal.Reasoning] loses its dim attribute (it does not lose its
	// content — see the channel's doc).
	TermEnv string

	// InputRowCap bounds how many rows the editor RENDERS for a wrapped
	// input line (user ruling, 2026-07-31 — arbitration item 6 adopted).
	// Excess rows collapse to a placeholder; the full buffer is RETAINED
	// and returned in [Answer], so the cap is a rendering bound and never
	// a truncation of what the user typed or recalled.
	//
	// 0 selects the default, min([DefaultInputRowCap], Size().Rows-2) —
	// the two rows left over are the prompt's own line and the line the
	// terminal is about to scroll into.
	//
	// It is not a tuning knob. A 5000-rune line recalled with ↑ at width
	// 40 is 125 rows, repainted on EVERY keystroke, and §4.1 makes the
	// overflow unerasable: content taller than the terminal has scrolled
	// out of the region the editor may repaint, so an uncapped editor
	// cannot take back what it drew. Unbounded wrapping therefore turns
	// bug 2 (mid-token rendering of long recalled lines) into a strictly
	// worse bug rather than fixing it.
	//
	// The floor is 2: one row is the placeholder, so a cap of 1 would
	// render a line the user cannot see at all.
	InputRowCap int

	// Platform, when non-nil, overrides backend selection. This is the
	// S2 injection point (§7): a test supplies decoded events directly.
	// Backend selection is otherwise made ONCE, at Open, for the whole
	// session — U12: an ioctl failure selects the plain backend for the
	// session rather than per call, because a backend that changes
	// underneath a running turn is the same ownership ambiguity in a
	// smaller window.
	//
	// An injected platform is INTERACTIVE by definition: the plain
	// backend is what "not a terminal" means here, and a caller that
	// supplies its own platform is standing in for a real one. That is
	// the whole of [Terminal.Interactive]'s rule — see it.
	Platform Platform
}

// Terminal is the arbiter. Exactly one exists per session.
//
// Its methods are the whole of what a client may do to the terminal.
// There is deliberately no accessor for the fd, the mode, or the
// decoder: Go cannot express capability confinement across a struct
// field, so the confinement is enforced by there being nothing to reach
// for, backed by the no-direct-terminal-access gate in gate_test.go.
// liner is the standing proof that a dependency can mutate termios where
// no compiler rule of ours reaches.
type Terminal struct {
	plat  Platform
	diag  io.Writer
	stdin io.Reader

	// interactive and ansi are the session's two display determinations,
	// each made ONCE at Open. interactive is the ONE TTY predicate the
	// whole program shares; ansi is whether the terminal accepts the
	// erase and dim sequences.
	interactive bool
	ansi        bool

	// owns says term owns the terminal MODE, the READER and the EDITOR for
	// this session. It is the read half's determination and it is NARROWER
	// than interactive, deliberately.
	//
	// It starts as `interactive && ansi` — not as `interactive` — because
	// an in-place line editor on a terminal with no erase sequence cannot
	// exist: it could draw a line but never take it back, so every
	// backspace, every ↑ recall and every wrap would append. The honest
	// fallback there is the kernel's own line discipline, which is exactly
	// the plain read path, with no mode installed and no pump running. It
	// is cleared at Open if the mode cannot be installed, for the same
	// reason and by the same rule (U12: decided once, for the session).
	//
	// This is not a second projection of `ansi`. They answer different
	// questions — can this terminal erase, and is the read path ours — and
	// they coincide only because the first is a precondition of the second.
	owns     bool
	histFile string
	// rowCapOpt is [Options.InputRowCap] as given. Resolved against the
	// live geometry at each read, because the window can be resized between
	// two prompts.
	rowCapOpt int

	// The three channel writers are built once and handed out by value of
	// pointer, so Out()/Diag()/Reasoning() are allocation-free and two
	// callers of the same channel are literally the same object.
	outW, diagW, reasonW *channelWriter
	status               *Status

	// raiseTSTP suspends the process for an in-band Ctrl-Z. A field, not a
	// call, so the mode lifecycle around a suspend can be asserted without
	// a test stopping the test binary. See [Terminal.suspend].
	raiseTSTP func()

	// eofCh is CLOSED once, when input ends, releasing every blocked read
	// then and thereafter. End of input is permanent; a value would have to
	// be put back.
	eofCh   chan struct{}
	eofOnce sync.Once

	// wake nudges the pump when the activity stack gains an entry. It only
	// matters once the stream has ended and the pump has no read left to
	// return and notice with; see [Terminal.drainThenEOF].
	wake chan struct{}

	// sigCh carries SIGTERM/SIGHUP (U4). Written once at Open and cleared
	// by Close, both from the session's own goroutine.
	sigCh chan os.Signal

	// mu is THE serialization point. Every emitted byte and every piece
	// of bookkeeping below is under it, which is what makes an error
	// mid-stream unable to interleave into the status line — Diag lands
	// on a different fd but passes through the same lock.
	mu            sync.Mutex
	stack         []*Registration
	abortWindows  int
	modeInstalls  int
	mode          ModeIntent
	column        CursorColumn
	statusText    string
	statusLive    bool
	reasoningOpen bool
	violations    int
	closed        bool
	reading       bool

	// The pump's handle. stopPump joins on pumpDone, so on return nothing
	// in this process is reading stdin.
	pumpCancel context.CancelFunc
	pumpDone   chan struct{}

	// The read half. hist is the session's one recall store; ed is the
	// editor currently on the terminal, which the pump needs so a resize
	// can re-wrap it; edDrawn/edRow/edCols are where its block is, which is
	// TERMINAL state and belongs beside the cursor column (see editor.go).
	// edCols is the WIDTH the block was laid out at: after a resize it is
	// what lets the editor find the block's first row again instead of
	// abandoning it and committing a copy — see [editor.blockTop].
	// in is the plain path's one buffered reader.
	hist    *history
	ed      *editor
	edDrawn bool
	edRow   int
	edCols  int
	in      *bufio.Reader
}

// Open captures the terminal, selects a backend, installs the session
// mode, and starts the input pump.
//
// Mode installations for the whole session are: one here (capture the
// ENTRY mode, install the SESSION mode), one at [Terminal.Close]
// (restore entry), plus exactly one restore/re-install pair per
// [Terminal.Handoff]. There are ZERO transitions between prompt and turn
// — the load-bearing simplification of §3. The only thing that varied
// per-prompt before was VMIN/VTIME, a read-blocking strategy; owning the
// reader lets readiness be a call argument instead. A parameter cannot
// be observed, adopted, or left stale by anything, so bug 3's category
// stops existing rather than being disciplined.
//
// # A session that does NOT get a mode
//
// The arithmetic above holds for a session where [Terminal.owns] is true:
// a terminal on both ends that can also erase. A pipe, a redirected end,
// or TERM=dumb installs NOTHING and runs no pump —
// [StateSnapshot.ModeInstalls] stays 0 for the whole session — because
// cbreak without an editor to interpret the bytes would take the kernel's
// line discipline away and put nothing in its place. See the field.
//
// Signal handling arrives here with the mode, for one reason: a handler
// that restored a mode term never installed would be putting back somebody
// else's guess. SIGWINCH feeds [Device.Resized]; SIGTERM/SIGHUP restore
// the entry mode before the process dies (U4 — today they leave the
// terminal in no-echo mode, because only os.Interrupt is registered);
// SIGTSTP is raised, never caught.
//
// # Ctrl-C is not one of those signals while term holds the fd
//
// [ModeSession] clears ISIG, so Ctrl-C is decoded IN BAND as an ordinary
// key event and offered down the activity stack like any other key. The
// SIGINT dispatcher still exists, and still matters, but it serves a
// narrower population: the [Terminal.Handoff] window (where the entry
// mode — ISIG included — is restored for the child) and any SIGINT
// originating outside the terminal, such as `kill -INT`. See
// [ModeSession] for why in-band is the point rather than a side effect,
// and [Handler.Interrupt] for the division of labour.
//
// # Ctrl-Z is term's, and has no exported API
//
// Clearing ISIG also puts 0x1A in band (user ruling, 2026-07-31 —
// arbitration item 5 adopted), which is the only place suspend can be
// handled correctly: term restores the ENTRY mode, drops SIGTSTP to its
// default disposition, raises it, and re-installs the session mode when
// SIGCONT resumes the process. Today's mid-turn Ctrl-Z is a latent
// defect — it suspends with cbreak still installed, so the shell
// inherits a terminal personant modified and `fg` returns to a mode
// nobody re-established. Only the fd owner can fix that, and only by
// owning both halves of the transition.
//
// There is deliberately NO exported hook for it, and no [Activity] or
// [Handler] field: no client needs to observe a suspend, and none has
// standing to veto one — a suspend is between the user and the job
// control of their shell. Add a hook when a client turns up that needs
// one; an unused hook is a second opinion about terminal state, which is
// the shape this package exists to remove.
func Open(opts Options) (*Terminal, error) {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Stdin == nil {
		// An empty stream rather than nil: the read path must have
		// something to reach end-of-input on, and "no stdin" and "stdin
		// closed" are the same session to every caller.
		opts.Stdin = strings.NewReader("")
	}

	plat, interactive := selectBackend(opts)
	ansi := interactive && ansiCapable(opts.TermEnv)

	t := &Terminal{
		plat:        plat,
		diag:        opts.Stderr,
		stdin:       opts.Stdin,
		interactive: interactive,
		ansi:        ansi,
		owns:        ansi,
		histFile:    opts.HistoryFile,
		rowCapOpt:   opts.InputRowCap,
		raiseTSTP:   raiseTSTP,
		eofCh:       make(chan struct{}),
		wake:        make(chan struct{}, 1),
		// The cursor starts at column 0: nothing has been written yet.
		// ColumnUnknown is the ZERO value of CursorColumn and means "a
		// child owned the fd", so it must be assigned away here rather
		// than left to the zero value.
		column:  ColumnStart,
		mode:    ModeEntry,
		reading: true,
	}
	t.outW = &channelWriter{t: t, ch: chanOut}
	t.diagW = &channelWriter{t: t, ch: chanDiag}
	t.reasonW = &channelWriter{t: t, ch: chanReasoning}
	t.status = &Status{t: t}

	if !t.owns {
		return t, nil
	}
	if err := plat.InstallMode(ModeSession); err != nil {
		// U12's rule applied to the mode: the degradation is decided ONCE
		// and for the whole session. A retry per prompt would mean the read
		// path could change underneath a running turn.
		fmt.Fprintf(t.Diag(), "warn: %v; line editing disabled for this session\n", err)
		t.owns = false
		return t, nil
	}
	t.mode, t.modeInstalls = ModeSession, 1
	if t.histFile != "" {
		t.hist = loadHistory(t.histFile)
	}
	t.watchExitSignals()
	t.startPump()
	return t, nil
}

// selectBackend makes the ONE backend decision of a session (U12).
//
// The TTY backend requires BOTH ends to be character devices: the device
// reads and sizes through stdin, and every byte term emits goes to
// stdout. A run with either end redirected (`personant chat > out.txt`,
// a test's injected buffers) is not a terminal — line editing has
// nowhere to render and decoration would corrupt the captured stream.
// This is the whole of the old chat.interactiveTTY predicate, moved to
// the package that owns the answer.
//
// Every failure below falls through to the plain backend and is NOT
// retried later. That is U12: an ioctl failure selects the plain backend
// for the WHOLE session, because a backend that changes underneath a
// running turn is the same ownership ambiguity in a smaller window.
func selectBackend(opts Options) (Platform, bool) {
	if opts.Platform != nil {
		return opts.Platform, true
	}
	in, okIn := charDevice(opts.Stdin)
	out, okOut := charDevice(opts.Stdout)
	if okIn && okOut {
		if d, err := NewUnixDevice(in, out); err == nil {
			return NewUnixPlatform(d), true
		}
	}
	return NewPlainPlatform(opts.Stdin, opts.Stdout), false
}

// charDevice reports whether v is an *os.File backed by a character
// device — a terminal rather than a file, a pipe or an in-memory buffer.
// Stdlib-only; no dependency knows more than os.FileMode does here.
func charDevice(v any) (*os.File, bool) {
	f, ok := v.(*os.File)
	if !ok || f == nil {
		return nil, false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil, false
	}
	return f, true
}

// Close joins the pump, restores the entry mode, flushes history and
// removes the signal handlers. Idempotent, and safe from a forced-exit
// path where no deferred cleanup runs — which is why the decoration is
// retired first and unconditionally: a half-drawn status frame or an
// unterminated dim run must not be the last thing on the user's terminal.
//
// The ORDER is the contract. The pump is joined BEFORE the mode is put
// back, so no read can straddle a mode change; and the entry mode goes
// back before the device is released, because the device is where the
// captured entry termios lives.
//
// It must not be called from a [Handler] — see [Handler.Key]. The pump
// cannot join itself.
func (t *Terminal) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.retireStatusLocked()
	t.endReasoningLocked()
	hist := t.hist
	t.mu.Unlock()

	t.stopPump()
	t.stopExitSignals()
	t.installMode(ModeEntry)

	var err error
	if hist != nil {
		err = hist.flush()
	}
	if perr := t.plat.Close(); err == nil {
		err = perr
	}
	return err
}

// Interactive reports whether this session drives a real terminal. It is
// the ONE TTY predicate (axiom §4.2 requires exactly one, and it now
// lives here rather than in chat.interactiveTTY). Clients ask term rather
// than re-deriving it, which is why the tty-only channels can be inert
// without every caller branching.
//
// It is exactly "the plain backend was not selected", which is what makes
// it one fact rather than two that can disagree: an injected
// [Options.Platform] is interactive, a real terminal on both ends is
// interactive, and everything else is not.
//
// Deliberately NOT duplicated in [StateSnapshot]: two projections of one
// fact is how the flag in bug 1 came to disagree with itself.
func (t *Terminal) Interactive() bool { return t.interactive }

// Size reports the current window geometry, falling back to
// [DefaultCols] x [DefaultRows] when it cannot be read. Clients need it
// for the W3 row cap; term needs it for status truncation and wrapping.
//
// A read failure is a fallback, never a re-selection of the backend
// (U12) — see [Device.Size].
func (t *Terminal) Size() Size {
	if s, err := t.plat.Size(); err == nil && s.Cols > 0 && s.Rows > 0 {
		return s
	}
	return Size{Cols: DefaultCols, Rows: DefaultRows}
}

// --- Input ownership: the LIFO activity stack ------------------------

// Activity names a kind of input consumer. It is a label for
// bookkeeping and for [StateSnapshot], NOT a dispatch key — see
// [Handler.Interrupt] for why switching on it would reintroduce bug 4.
type Activity string

const (
	// ActivityEditor is a line being edited at the REPL prompt. It is
	// what the REPL passes [Terminal.ReadLine] for its prompt read.
	ActivityEditor Activity = "editor"
	// ActivityTurn is a turn in flight, watching for its abort key. It is
	// pushed with [Terminal.Push], never by a read: it is not an input
	// consumer that reads a line, which is why [Terminal.ReadLine]
	// rejects it.
	ActivityTurn Activity = "turn"
	// ActivityAsk is a resolver prompting from INSIDE turn close (the
	// §3.4 recall and §3.5 closure offers). [Terminal.ReadLine] pushes it
	// OVER the turn, which is the whole reason the stack is LIFO rather
	// than a single current-owner slot: turn close legitimately prompts
	// while a turn is still the outer owner.
	ActivityAsk Activity = "ask"
	// ActivityChild is a `$`/`#` child running. Inside its window NOBODY
	// in-process reads stdin — see [Terminal.Handoff].
	ActivityChild Activity = "child"
)

// Disposition is what a handler did with an offer.
type Disposition int

const (
	// Declined means the handler did not take it and the offer FALLS
	// THROUGH to the next handler down the stack. Declining is normal,
	// not exceptional: the `$`/`#` child handler declines whenever no
	// command is in flight.
	Declined Disposition = iota
	// Claimed means the handler took it and the offer stops here.
	Claimed
)

// Handler is what an activity supplies when it registers. Every field is
// optional; a nil field declines by construction.
//
// Key and Abort are dispatched; Interrupt is recorded and waits for W4's
// offer chain. What was always kept, from W1, is the BOOKKEEPING they
// imply — stack order and the abort window — because that is the half
// that emits no bytes and is therefore the half that shipped green (see
// [StateSnapshot]).
type Handler struct {
	// Key is offered each decoded keystroke while this registration is
	// the TOP of the stack. A declined key falls to the next handler
	// down. Consumers receive decoded keys and never bytes, and never
	// the fd: ESC-vs-escape-sequence disambiguation needs bytes held
	// read-but-not-yet-interpreted ACROSS a timeout, and a lookahead
	// that straddles a handover is lost. That is why "the lessee reads"
	// was rejected (§3) and why private per-consumer buffers — liner's
	// defect, reimplemented in our own code — are not an option.
	//
	// Window resize is not offered here: it is term's own business (it
	// repaints the status slot and re-wraps the editor). Ctrl-Z is not
	// offered either — term consumes it (see [Open]).
	//
	// TWO RULES, both consequences of dispatch running ON the pump
	// goroutine (pump.go says why it does):
	//
	//   - A handler MUST NOT BLOCK. While it runs nothing is reading the
	//     terminal, so a handler that waits on a turn is a handler that
	//     has stopped the next keystroke from being decoded.
	//   - A handler MUST NOT call [Terminal.Close] or [Terminal.Handoff].
	//     Both JOIN the pump, and the pump cannot join itself. A handler
	//     that wants the session to end says so to its own owner and
	//     returns — internal/chat routes an in-band Ctrl-C onto the same
	//     channel a delivered SIGINT arrives on, which is both
	//     deadlock-free and the honest description of what the key means.
	Key func(Key) Disposition

	// Interrupt is offered SIGINT. Read this before implementing it.
	//
	// SIGINT enters ONE dispatcher, which offers it down the
	// registration stack in LIFO ORDER; each handler returns claimed or
	// declined; a decline falls through (§4). Nothing consults the stack
	// to decide the OUTCOME.
	//
	// WHAT REACHES IT is narrower than "Ctrl-C". [ModeSession] clears
	// ISIG, so whenever term owns the fd a Ctrl-C is a decoded KEY and
	// arrives at [Handler.Key] instead — that is the point of clearing
	// it, because the dispositions this design must keep available are
	// keyed on the EDITOR'S BUFFER (reference behaviour O2, clear the
	// line), and an async signal handler cannot read that buffer without
	// racing the editor.
	//
	// What is left for this hook is exactly two populations: the
	// [Terminal.Handoff] window, where the entry mode is restored, the
	// child runs in its OWN process group, and the kernel must generate
	// SIGINT for personant for shell.Runner.Interrupt() to have anything
	// to forward; and any SIGINT raised from outside the terminal, such
	// as `kill -INT`. That is not a demotion — the first of the two is
	// the whole of bug 4's territory.
	//
	// Handler SELECTION may be a function of stack position. The
	// DISPOSITION may not, and this is not a style preference — it is
	// bug 4. The child's terminal window strictly CONTAINS
	// shell.Runner's claim window, so a literal "disposition =
	// f(stack top)" routes a Ctrl-C arriving in that sliver to a handler
	// with nothing to claim, and silently drops it. The `child` handler
	// is shell.Runner.Interrupt() verbatim, deciding under its own
	// mutex, and it declines when it has nothing in flight.
	//
	// The signature is argument-free on purpose: a handler cannot be
	// told its own position, cannot see the stack, and has nothing to
	// branch on except its own state. [StateSnapshot] exposes the stack
	// as []Activity — VALUES, not handlers — so even a client that reads
	// it cannot get from there to a disposition without writing a switch
	// on Activity, which is visibly the wrong thing next to this
	// comment.
	//
	// Dispatched from W4.
	Interrupt func() Disposition

	// Abort, when non-nil, OPENS the abort window for this registration:
	// a bare Esc calls it. Esc is retraction, not stop-generating
	// (axiom §4.3) — the turn's input is declared a mistake and nothing
	// from it enters memory.
	//
	// While the window is open a BARE Esc is claimed here, at this level
	// of the offer chain, before Key sees it — which is what makes "Esc
	// aborts the turn" a property of the registration rather than of every
	// consumer's switch statement. Alt-Esc is not Esc.
	// [Registration.RevokeAbort] closes the window.
	//
	// It runs on the pump goroutine and [Handler.Key]'s two rules apply to
	// it unchanged.
	Abort func()
}

// Push registers a consumer on top of the LIFO activity stack. The
// returned registration must be popped; see [Registration.Pop] for what
// happens when the stack discipline is broken.
func (t *Terminal) Push(a Activity, h Handler) *Registration {
	r := &Registration{t: t, activity: a, handler: h, abort: h.Abort != nil}
	t.mu.Lock()
	if r.abort {
		t.abortWindows++
	}
	t.stack = append(t.stack, r)
	t.mu.Unlock()
	// Wake the pump: it may be holding type-ahead for the first consumer
	// to appear, with no read left to return and tell it so.
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return r
}

// Registration is one entry on the activity stack. Holding it is the
// only way to release it, so a client cannot pop an entry it did not
// push.
type Registration struct {
	t        *Terminal
	activity Activity
	handler  Handler

	// abort and popped are guarded by t.mu, not by the registration:
	// they are facts about the STACK, and the stack has one lock.
	abort  bool
	popped bool
}

// Pop releases the registration.
//
// Popping an entry that is not the top of the stack is a
// TRANSITION-LEGALITY VIOLATION (U8: the ownership sensor was
// deliberately relocated from acquire to release, because acquisition is
// unambiguous and release ordering is where the interleavings actually
// go wrong). A violation increments [StateSnapshot.Violations] and is
// reported on [Terminal.Diag].
//
// The entry is released either way. Refusing an out-of-order Pop would
// leave a registration nobody can remove — a leak on top of a violation
// — and the sensor's job is to REPORT the interleaving, not to make the
// program worse for having one.
//
// A second Pop of the same registration is a violation too, not a
// tolerated no-op: it means two owners believe they hold one entry,
// which is the belief this package exists to make impossible.
//
// It deliberately does NOT return an error. Pop is the archetypal
// deferred call; an error return that every call site drops into a
// `defer` is a sensor reporting to nobody. Routing it to observable
// state instead is what makes the plain-backend parity assertions — the
// only automated coverage this defect class has — able to see it.
func (r *Registration) Pop() {
	t := r.t
	t.mu.Lock()
	violation := ""
	switch {
	case r.popped:
		t.violations++
		violation = fmt.Sprintf("term: %s popped twice", r.activity)
	default:
		i := t.indexOfLocked(r)
		if i != len(t.stack)-1 {
			t.violations++
			violation = fmt.Sprintf("term: %s released out of order (%d entr%s above it)",
				r.activity, len(t.stack)-1-i, plural(len(t.stack)-1-i))
		}
		t.stack = append(t.stack[:i], t.stack[i+1:]...)
		r.popped = true
		if r.abort {
			r.abort = false
			t.abortWindows--
		}
	}
	t.mu.Unlock()
	// Reported OUTSIDE the lock: Diag's write path takes the same mutex,
	// so reporting under it would deadlock the sensor on its own report.
	if violation != "" {
		fmt.Fprintln(t.Diag(), violation)
	}
}

// indexOfLocked finds r on the stack, or returns the top index when it is
// not there (the double-pop case, which the caller has already
// classified). Callers hold t.mu.
func (t *Terminal) indexOfLocked(r *Registration) int {
	for i := len(t.stack) - 1; i >= 0; i-- {
		if t.stack[i] == r {
			return i
		}
	}
	return len(t.stack) - 1
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// retire stops this registration consuming keys WITHOUT releasing it.
//
// It exists for one instant: an editor that has just completed a line is
// still on the stack — its reading goroutine has not returned, let alone
// popped — and a key arriving in that sliver must fall past it into the
// pump's reservoir, to be offered to whoever registers next. A consumer
// that has stopped consuming but still claims is exactly how type-ahead
// disappears at a handover, which is the defect being removed.
//
// Unexported: it is an internal step of one read, not a thing a client may
// do to its own registration halfway through.
func (r *Registration) retire() {
	r.t.mu.Lock()
	defer r.t.mu.Unlock()
	r.handler.Key = nil
}

// RevokeAbort closes the abort window opened by [Handler.Abort].
//
// This is C12 form (i): a SINGLE, UNCONDITIONAL, IDEMPOTENT,
// ARGUMENT-FREE call, made at the pre-canonical phase boundary. Each
// property is load-bearing. Unconditional and argument-free, because
// every conditional variant of "should I close the window now?"
// evaluated at the call site is a place to get it wrong. Idempotent,
// because the boundary is crossed once but the call also fires at turn
// end. And single, because its OMISSION is then detectable: the abort
// window is still open in [StateSnapshot] past the boundary, which the
// parity assertions catch even though nothing about it emits a byte.
//
// The boundary matters: past it, a turn has begun writing canonical
// state, and honouring an abort there leaves a half-written turn that
// greets the user with a §4.5.8 recovery banner.
func (r *Registration) RevokeAbort() {
	t := r.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if !r.abort {
		return
	}
	r.abort = false
	t.abortWindows--
}

// --- Handoff ---------------------------------------------------------

// Handoff runs fn with the ENTRY mode restored and NOBODY in-process
// reading stdin. The pump is joined BEFORE fn starts and restarted after
// it returns, so neither personant nor a pager the child spawns is
// competing for bytes (§3).
//
// It is a callback rather than a Begin/End pair so the window cannot be
// leaked: there is no state a caller can forget to unwind, and child
// spawn plus terminal handoff compose in exactly one function (U9).
// Restoring the entry mode is not a nicety — the session mode has
// ICANON and ECHO off, so a naively spawned child inherits a terminal
// where typed characters do not appear.
//
// Handoff pushes [ActivityChild] for its duration, which is what puts
// the child's interrupt handler on the offer chain. Errors from fn pass
// through unchanged; a failure to restore or reinstall the mode is
// reported on [Terminal.Diag] and does not mask fn's result.
//
// # ISIG comes back, and that is the point
//
// Restoring the ENTRY mode restores ISIG with it, because the entry mode
// is whatever the user's shell had and shells leave ISIG on. That is not
// incidental — it is the ruled §4.4 amendment's other half. The child runs
// in its OWN process group, so the kernel must still generate SIGINT for
// personant for shell.Runner.Interrupt() to have anything to forward. This
// is therefore the one mid-session window where a Ctrl-C is a real signal
// rather than a decoded key, and it is exactly bug 4's territory.
//
// # The cursor column is NOT invalidated here
//
// [ColumnUnknown] is documented as "after a child owned the fd". A child
// spawned through this window does not own the fd for OUTPUT: its stdout
// is a pipe back into [Terminal.Out], so every byte it writes goes through
// the same serialization point as everything else and term's belief stays
// current. What it owns is the terminal MODE and the right to read stdin.
// Forcing ColumnUnknown here would have term assert it does not know
// something it does. A caller that hands a child the real fd is outside
// this contract and must say so.
func (t *Terminal) Handoff(fn func() error) error {
	reg := t.Push(ActivityChild, Handler{})
	defer reg.Pop()

	// Joined BEFORE the child starts: nothing in personant may read while
	// a child — or a pager it spawns — is competing for the same bytes.
	t.stopPump()
	t.installMode(ModeEntry)
	err := fn()
	t.installMode(ModeSession)
	t.startPump()
	return err
}

// --- Observable state ------------------------------------------------

// CursorColumn is where term believes the cursor is. It is a TRI-STATE,
// and the third state is the point (§5).
type CursorColumn int

const (
	// ColumnUnknown means term does not know — the state after a child
	// owned the fd and wrote whatever it liked. The correct response is
	// a deterministic newline, not a guess; a CPR query is an
	// optimization on top of that guarantee, never a substitute for it.
	ColumnUnknown CursorColumn = iota
	// ColumnStart means the cursor sits at column 0 and the next write
	// lands on a fresh line.
	ColumnStart
	// ColumnMid means content occupies the current line.
	ColumnMid
)

// StateSnapshot is what the arbiter BELIEVES about itself: who holds
// input, what mode is installed, whether the abort window is open.
//
// This is the second of the three verification layers in SOLUTION.md §6
// and, under the dogfood-first posture, the ONLY automated coverage of
// the byte-invisible defect class. Terminal mode and input ownership
// emit no bytes; bugs 3 and 4 look identical to a correct run right up
// until they bite, which is exactly why they shipped green. The plain
// backend keeps the SAME books as the TTY backend, so the existing
// off-TTY suites assert against this for free.
//
// Read it for assertions and diagnostics. NEVER consult it to decide the
// disposition of a signal — see [Handler.Interrupt].
type StateSnapshot struct {
	// Stack is the activity stack, bottom first; the last element is the
	// top. Values, not handlers, deliberately.
	Stack []Activity

	// Mode is the mode currently installed.
	Mode ModeIntent

	// ModeInstalls counts installations since Open. The expected value
	// is a hard number, not a range: 1 at Open, +2 per [Terminal.Handoff],
	// +2 per in-band Ctrl-Z, +1 at [Terminal.Close]. Any other value means
	// someone transitioned the mode, which is bug 3.
	//
	// On a session where term does NOT own the terminal — a pipe, a
	// redirected end, TERM=dumb — the expected value is 0 for the whole
	// session, because there is no mode to install and the kernel's line
	// discipline is doing the editing. See [Open].
	ModeInstalls int

	// AbortWindow reports whether an abort window is open. Its being
	// open past the pre-canonical boundary is the detectable omission of
	// [Registration.RevokeAbort].
	AbortWindow bool

	// PumpReading reports whether TERM HOLDS THE READ PATH. It is false
	// exactly inside a [Terminal.Handoff] window, and after
	// [Terminal.Close], and at no other time.
	//
	// "The read path" rather than "the pump goroutine" is deliberate, and
	// it is the parity rule: on a terminal term owns, the read path IS the
	// pump; off one it is the buffered line reader, which is equally term's
	// and equally suspended while a child runs. One flag for one fact means
	// the off-TTY suite can sense an unbalanced Handoff on behalf of a
	// backend it cannot run.
	PumpReading bool

	// Column is term's cursor-column belief. See [CursorColumn].
	Column CursorColumn

	// Status is the text currently occupying the ephemeral slot, or "".
	Status string

	// Violations counts ownership and transition-legality violations
	// since Open. The assertion is == 0; a nonzero value names a
	// released-out-of-order registration, not a tolerable warning.
	Violations int
}

// State returns a snapshot. Cheap enough to assert on in a loop and safe
// from any goroutine.
func (t *Terminal) State() StateSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	stack := make([]Activity, len(t.stack))
	for i, r := range t.stack {
		stack[i] = r.activity
	}
	return StateSnapshot{
		Stack:        stack,
		Mode:         t.mode,
		ModeInstalls: t.modeInstalls,
		AbortWindow:  t.abortWindows > 0,
		PumpReading:  t.reading,
		Column:       t.column,
		Status:       t.statusText,
		Violations:   t.violations,
	}
}

// --- Reading ---------------------------------------------------------
//
// The read half is readline.go: [Terminal.ReadLine] and
// [Terminal.AppendHistory], plus the editor scaffolding they run on.

// errNotTerminal is what a device or platform returns when it has no
// window geometry to report. It is a fallback trigger for
// [Terminal.Size], never a session-level failure (U12).
var errNotTerminal = errors.New("term: not a terminal")

// errNoEventStream is what the plain backend's [Platform.NextEvent]
// returns. Off a terminal there are lines, not keystrokes, and the read
// path is the buffered line reader — so a pump started there stops at once
// instead of quietly consuming the stream that reader owns.
var errNoEventStream = errors.New("term: backend produces no key events")
