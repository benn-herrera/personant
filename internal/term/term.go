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
// This file is the W0 API surface: types, signatures and the rationale
// for each. There are no implementations — every body panics. Read
// mad-design/terminal-layer/SOLUTION.md first; the section references
// below are to that document.
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
// The screen model and the pty runner were dropped by user ruling
// (IMPLEMENTATION-PLAN.md §1). What remains as automated coverage of the
// BYTE-INVISIBLE defect class — bugs 3 and 4 emit no bytes, which is
// exactly why they shipped green — is plain-backend bookkeeping parity:
// the non-TTY backend keeps the SAME books as the TTY backend, so the
// existing sim and scenario suites become an ordering sensor for free.
// [Terminal.State] is what those assertions read. It is first-class API,
// not a debug helper.
package term

import (
	"context"
	"io"
)

// notImplemented is the W0 body. Every function in this package panics
// with it; W1 replaces the bodies, not the signatures.
const notImplemented = "term: not implemented (W1)"

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
	// W0 only names it; the cap is enforced by the W3 editor.
	InputRowCap int

	// Platform, when non-nil, overrides backend selection. This is the
	// S2 injection point (§7): a test supplies decoded events directly.
	// Backend selection is otherwise made ONCE, at Open, for the whole
	// session — U12: an ioctl failure selects the plain backend for the
	// session rather than per call, because a backend that changes
	// underneath a running turn is the same ownership ambiguity in a
	// smaller window.
	Platform Platform
}

// Terminal is the arbiter. Exactly one exists per session.
//
// Its methods are the whole of what a client may do to the terminal.
// There is deliberately no accessor for the fd, the mode, or the
// decoder: Go cannot express capability confinement across a struct
// field, so the confinement is enforced by there being nothing to reach
// for, backed by the no-direct-terminal-access grep gate. liner is the
// standing proof that a dependency can mutate termios where no compiler
// rule of ours reaches.
type Terminal struct{ _ struct{} }

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
// Open also installs the handlers for the signals that MUTATE TERMINAL
// STATE — SIGWINCH, SIGTERM/SIGHUP (U4: today these leave the terminal
// in no-echo mode), SIGTSTP/SIGCONT — and the single SIGINT dispatcher
// described at [Handler.Interrupt]. Signals that carry USER INTENT stay
// with policy; signals that change the terminal belong to whoever owns
// it (§4, layering rule).
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
func Open(opts Options) (*Terminal, error) { panic(notImplemented) }

// Close restores the entry mode, flushes history, joins the pump, and
// removes the signal handlers. Idempotent, and safe from a forced-exit
// path where no deferred cleanup runs.
func (t *Terminal) Close() error { panic(notImplemented) }

// Interactive reports whether this session drives a real terminal. It is
// the ONE TTY predicate (axiom §4.2 requires exactly one, and the
// current code's interactiveTTY is it). Clients ask term rather than
// re-deriving it, which is why the tty-only channels can be inert
// without every caller branching.
//
// Deliberately NOT duplicated in [StateSnapshot]: two projections of one
// fact is how the flag in bug 1 came to disagree with itself.
func (t *Terminal) Interactive() bool { panic(notImplemented) }

// Size reports the current window geometry, falling back to
// [DefaultCols] x [DefaultRows] when it cannot be read. Clients need it
// for the W3 row cap; term needs it for status truncation and wrapping.
func (t *Terminal) Size() Size { panic(notImplemented) }

// --- Output: the §5 2x2, with one cell that has no name -------------
//
//	                | scrollback (persists) | ephemeral (erasable)
//	----------------+-----------------------+---------------------
//	always (TTY+pipe)| Out(), Diag()        | FORBIDDEN
//	tty-only         | Reasoning()          | Status()
//
// The forbidden cell is (always, ephemeral) and it is forbidden for a
// mechanical reason: a pipe has no erasure. There is no method for it,
// no channel enum with a fourth member, and no way to compose one out of
// the three that exist — that is what "inexpressible" means here. Any
// client that believes it needs it wants Out().
//
// The missing (tty-only, scrollback) cell is precisely why thinking.go
// existed and why it ended up the writer for EVERYTHING: it was the only
// object that knew about the reasoning-to-content transition, so every
// other producer had to route through it to be serialized. Naming the
// cell dissolves the object. All four channels share one serialization
// point inside term, so the transition is handled once, invisibly.

// Out is the committed content channel: the streamed response body, menu
// and question text, banners, ordinary notices. Present on a TTY and in
// a pipe alike, and byte-identical between them — the sim and scenario
// goldens are the gate on that.
//
// A write to Out first retires whatever ephemeral decoration is on
// screen (the status slot) and closes any open reasoning run, so content
// can never arrive dim or jammed onto the tail of a scratch line.
//
// Body text passes through UNWRAPPED (user ruling, 2026-07-31): term
// does not re-flow the streamed response, and the terminal's own soft
// wrap does the work. In-app wrapping would insert real newlines that
// survive copy-paste, `tee` and every downstream reader, which is
// exactly what the §4.1 scrollback-fidelity bar forbids — what is on
// screen must be what the user gets when they take it away. This is a
// starting position, not a closed question: if a case for wrapping here
// appears, it is a ruling to revisit, not a rule of the architecture.
//
// It says nothing about the other two places term does width math.
// [Status] truncation and the editor's wrap are term's business by
// construction: both write to a region term must be able to erase or
// repaint, so both need the width and neither commits a newline to
// scrollback.
func (t *Terminal) Out() io.Writer { panic(notImplemented) }

// Diag is the committed diagnostic channel — errors and warnings. It
// lands on stderr, but it is INSIDE the arbiter (§5, U2): it passes
// through the same serialization point as Out, which is what stops an
// error mid-stream from interleaving into the status line. stderr
// escaping the arbiter is the collision W1 closes.
//
// Diag is (always, scrollback), not ephemeral: an error the user did not
// see is worse than an error that stayed on screen.
func (t *Terminal) Diag() io.Writer { panic(notImplemented) }

// Reasoning is the live model-reasoning channel: dimmed, tty-only, and
// PERSISTENT in scrollback (user ruling, 2026-07-31 — arbitration item 1;
// the brief's "or scrollback" phrasing was an error). It is a channel,
// NOT a bounded rewriteable region: dimmed reasoning stays in scrollback
// exactly as it does today and survives scrolling back.
//
// tty-only means it writes ZERO bytes when [Terminal.Interactive] is
// false, so piped, sim and test output are unaffected regardless of the
// /thinking setting. Axiom §4.5 — reasoning is model scratch and never
// enters memory — is unaffected by this package: there is no path from
// here to storage, and none may be created.
//
// Interleaving with Out is term's problem, not the caller's: an open
// reasoning run is closed (dim reset, newline) by the first byte written
// to Out or Diag.
func (t *Terminal) Reasoning() io.Writer { panic(notImplemented) }

// Status returns the session's single ephemeral slot — the phase-labeled
// progress indicator's home.
//
// It is TOKEN-LESS by design (§5, retired contention C13): every caller
// gets the same slot and there is no handle proving ownership, so two
// producers cannot each believe they own a line. That belief was bug 1.
// The slot holds one line, replaced whole; it is not an io.Writer,
// because append semantics on an erasable region is the thing that made
// the old indicator track its own previous frame.
//
// On a non-interactive session the slot is inert and writes nothing.
func (t *Terminal) Status() *Status { panic(notImplemented) }

// Status is the ephemeral single-line slot. See [Terminal.Status].
type Status struct{ _ struct{} }

// Set replaces the slot's contents and redraws it in place. The text is
// truncated to Size().Cols-[StatusColumnInset]; see that constant for
// why the final column is left alone.
func (s *Status) Set(text string) { panic(notImplemented) }

// Clear erases the slot and gives the line back. Idempotent, and safe
// from any goroutine including a signal path, where a half-drawn frame
// would otherwise be the last thing on the terminal.
func (s *Status) Clear() { panic(notImplemented) }

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
	// repaints the status slot and re-wraps the editor).
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
	Interrupt func() Disposition

	// Abort, when non-nil, OPENS the abort window for this registration:
	// a bare Esc calls it. Esc is retraction, not stop-generating
	// (axiom §4.3) — the turn's input is declared a mistake and nothing
	// from it enters memory.
	//
	// While the window is open the abort key is claimed here and is not
	// offered to Key. [Registration.RevokeAbort] closes it.
	Abort func()
}

// Push registers a consumer on top of the LIFO activity stack. The
// returned registration must be popped; see [Registration.Pop] for what
// happens when the stack discipline is broken.
func (t *Terminal) Push(a Activity, h Handler) *Registration { panic(notImplemented) }

// Registration is one entry on the activity stack. Holding it is the
// only way to release it, so a client cannot pop an entry it did not
// push.
type Registration struct{ _ struct{} }

// Pop releases the registration.
//
// Popping an entry that is not the top of the stack is a
// TRANSITION-LEGALITY VIOLATION (U8: the ownership sensor was
// deliberately relocated from acquire to release, because acquisition is
// unambiguous and release ordering is where the interleavings actually
// go wrong). A violation increments [StateSnapshot.Violations] and is
// reported on [Terminal.Diag].
//
// It deliberately does NOT return an error. Pop is the archetypal
// deferred call; an error return that every call site drops into a
// `defer` is a sensor reporting to nobody. Routing it to observable
// state instead is what makes the plain-backend parity assertions — the
// only automated coverage this defect class has — able to see it.
func (r *Registration) Pop() { panic(notImplemented) }

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
func (r *Registration) RevokeAbort() { panic(notImplemented) }

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
func (t *Terminal) Handoff(fn func() error) error { panic(notImplemented) }

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
// off-TTY suite asserts against this for free.
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
	// is a hard number, not a range: 1 at Open, +2 per Handoff, +1 at
	// Close. Any other value means someone transitioned the mode, which
	// is bug 3.
	ModeInstalls int

	// AbortWindow reports whether an abort window is open. Its being
	// open past the pre-canonical boundary is the detectable omission of
	// [Registration.RevokeAbort].
	AbortWindow bool

	// PumpReading reports whether the input pump is running. It is false
	// exactly inside a [Terminal.Handoff] window and at no other time.
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
func (t *Terminal) State() StateSnapshot { panic(notImplemented) }

// --- Reading ---------------------------------------------------------

// ReadLine is the ONLY read path in personant.
//
// There is deliberately no API that writes text and then separately
// reads a line. That shape is bug 5: seven sites print a question with
// Fprint and then call a reader with an empty prompt, and the reader —
// never told the question exists — repaints from column 0 over it. When
// the question is an argument to the read, the reader knows the width it
// must not repaint above, and the defect has nowhere to live.
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
// ctx cancels the read: a cancelled session or turn unblocks it rather
// than leaving a goroutine parked on a keypress that will never come.
// The error contract is [io.EOF] for end of input (Ctrl-D) and
// [ErrAborted] for an interrupt at the prompt; [IsEndOrAbort] tests for
// either. A caller error — an illegal a, or a malformed q — is
// [ErrQuestion], which IsEndOrAbort deliberately does not match.
//
// AppendHistory is NOT called here. What deserves to be recalled with ↑
// is policy — a menu answer is not a prompt — and the caller decides.
func (t *Terminal) ReadLine(ctx context.Context, a Activity, q Question) (Answer, error) {
	panic(notImplemented)
}

// AppendHistory records an accepted line for ↑/↓ recall. Blank lines and
// consecutive duplicates are dropped, and the store is capped at
// [HistoryLimit] entries; the file is flushed at [Terminal.Close].
func (t *Terminal) AppendHistory(line string) { panic(notImplemented) }
