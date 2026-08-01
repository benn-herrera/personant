package term

import (
	"context"
	"io"
)

// The platform structure is TWO cuts, not one (§7). Each participant in
// the design debate had correctly identified one of them.
//
//	S1 — device seam:   bytes, termios, winsize.        [Device]
//	S2 — platform seam: decoded events, mode intent, size. [Platform]
//
// On unix, S2 = S1 + the decoder, and [NewUnixPlatform] composes exactly
// that. The two exist separately because neither alone is sufficient:
//
//   - A byte seam is not enough for a future Windows backend.
//     ENABLE_VIRTUAL_TERMINAL_INPUT makes a byte seam implementable, so
//     the objection is not that it is impossible — it is that
//     Read(p []byte) cannot receive an INPUT_RECORD, and key records
//     carry key-down/up, repeat count and left/right modifier
//     distinction with no faithful VT encoding. Forcing them through
//     bytes would mean encoding facts the decoder then has to invent
//     back.
//   - A decoded-event seam alone is not enough for testing. A
//     byte-level scripted fake is what keeps exactly-emitted output
//     assertable and lets the decoder itself be driven with real
//     terminal byte sequences.
//
// Neither seam mentions the output model. The WRITE side stays bytes at
// both cuts: term formats, the seam transports.
//
// There is no Windows implementation and W1 does not add one. The
// requirement is only that S2 admits one later (axiom §4.6).
//
// # Both seams carry TWO files, not one
//
// W0 gave the unix device a single *os.File. Implementation contradicted
// it: reads, termios and the winsize ioctl come from STDIN while every
// emitted byte goes to STDOUT, and [Options] names them separately
// because the process genuinely has two — a session with stdout
// redirected is not a terminal even when stdin is one. Writing bytes to
// fd 0 because it happens to be the same tty would work by coincidence
// on the common case and silently disagree with Options everywhere else.
// [NewPlainPlatform] already took a reader and a writer; the unix device
// now matches it.
//
// Diag/stderr is deliberately NOT at either seam. It is a third fd that
// only the arbiter knows about, held on Terminal and written under the
// same lock — see the invariant at the top of output.go. Putting it in
// the seam would let a backend decide where errors go, which is the
// stderr-escapes-the-arbiter defect W1 closes.

// ModeIntent names WHAT the terminal is being used for, never which
// flags are set. Termios lives strictly below [Device]; a Windows
// backend has no termios at all, so an intent is the only thing that can
// cross S2.
//
// There are exactly two intents, and that is the §3 simplification made
// into a type: the old code had a third de-facto mode (liner's, plus a
// per-turn cbreak layered over it) and its transitions are bug 3. With
// two intents and the transition points enumerated at [Open], the set of
// legal mode changes is small enough to count — which is what
// [StateSnapshot.ModeInstalls] counts.
type ModeIntent int

const (
	// ModeEntry is the mode personant FOUND at startup. It is what a
	// `$`/`#` child must run under, and what the terminal is left in at
	// exit (U4: today SIGTERM/SIGHUP leave it in no-echo mode, because
	// only os.Interrupt is registered).
	//
	// It is also the only mode W1 ever reports: term captures the entry
	// mode at Open and installs nothing until W3. See [Open].
	ModeEntry ModeIntent = iota

	// ModeSession is the one mode personant runs under, at the prompt
	// and mid-turn alike, for the whole session.
	//
	// It is cbreak, not raw (axiom §4.4): ICANON and ECHO off, OPOST
	// left ON — clearing it would make a bare "\n" stop implying a
	// carriage return and staircase every writer. VMIN/VTIME are NOT
	// part of it: readiness is a poll(2) argument to the read, because a
	// parameter cannot be observed, adopted, or left stale by anything.
	// That is what dissolves bug 3 rather than disciplining it.
	//
	// ISIG is CLEARED, along with IEXTEN and IXON (user ruling,
	// 2026-07-31 — arbitration item 2 adopted, amending axiom §4.4), and
	// is restored for exactly one mid-session window: [Terminal.Handoff].
	// The axiom kept ISIG on to avoid hand-rolling interrupt semantics —
	// but personant ALREADY hand-rolls them, precisely because liner
	// clears ISIG per prompt today. Keeping it on therefore buys no
	// simplification and forecloses a disposition keyed on the EDITOR'S
	// BUFFER (clear-the-line), which an async signal handler cannot read
	// without racing the editor. It is also free: Handoff already
	// restores the entry mode, so the restoration rides a transition that
	// exists anyway and [StateSnapshot.ModeInstalls] is unchanged.
	//
	// Two consequences, both documented where they bite:
	//
	//   - Whenever term owns the fd, Ctrl-C is decoded IN BAND as a key
	//     event ([KeyCtrl] with Rune 'c') and offered down the activity
	//     stack like any other key. That in-band delivery is the POINT:
	//     it is what lets a disposition depend on what is in the editor's
	//     buffer. Ctrl-Z arrives in band for the same reason, and term
	//     handles it itself — see [Open].
	//   - The SIGINT dispatcher at [Handler.Interrupt] consequently
	//     serves the Handoff window (entry mode restored, child in its
	//     own process group, kernel generating SIGINT for personant so
	//     shell.Runner.Interrupt() has something to forward) and any
	//     signal raised from outside the terminal — NOT in-band Ctrl-C.
	//
	// The INTENT does not change either way, which is the point of naming
	// intents instead of flags; what changes is what the pump sees.
	//
	// Nothing installs it before W3.
	ModeSession
)

// Device is the S1 device seam: raw bytes, mode installation, window
// size. It knows nothing about keys, escape sequences, activities, or
// the output model.
//
// Two implementations plug in here: the unix one ([NewUnixDevice]) and a
// byte-level scripted fake in tests.
type Device interface {
	// Read is one read(2). It may legally return (0, nil) — a read
	// window that expired with nothing typed. That honest zero must NOT
	// be mapped to io.EOF, which is precisely the bug os.File.Read has
	// for this use and why the current code calls unix.Read directly.
	Read(p []byte) (int, error)

	// Write emits bytes. The write side is bytes at both seams.
	Write(p []byte) (int, error)

	// InstallMode installs the named mode. The device captured the entry
	// mode when it was opened, so [ModeEntry] is "put back exactly what
	// we found", byte for byte. All termios detail lives below this
	// call.
	InstallMode(ModeIntent) error

	// Size reports the window geometry. An error means the caller uses
	// [DefaultCols] x [DefaultRows]; it does not mean re-selecting a
	// backend mid-session (U12).
	Size() (Size, error)

	// Resized fires whenever the window size changed. It is out of band
	// because SIGWINCH is a signal, never a byte — a byte seam could
	// never have carried it, on unix any more than on Windows.
	Resized() <-chan struct{}

	// Close releases the device. It does not restore the mode; that is
	// [Terminal.Close]'s explicit final install.
	Close() error
}

// Platform is the S2 platform seam: decoded events, mode intent, size.
// A future Windows ReadConsoleInput backend plugs in HERE, and it is the
// injection point for [Options.Platform].
type Platform interface {
	// NextEvent blocks until one event is decoded, ctx cancels, or the
	// stream ends.
	//
	// It is called by the PUMP GOROUTINE AND BY NOTHING ELSE. One
	// physical reader is the whole of §3's stdin answer: consumers
	// register on the LIFO activity stack and receive events, so no
	// second party can strand a partially-read escape sequence in a
	// private buffer. liner holding three byte reservoirs while
	// escwatch.go read the same fd is the defect this forecloses.
	NextEvent(ctx context.Context) (Event, error)

	// Write emits bytes. Formatting happened above this line.
	Write(p []byte) (int, error)

	// InstallMode installs the named mode; see [ModeIntent].
	InstallMode(ModeIntent) error

	// Size reports the current geometry. Changes also arrive as
	// [EventResize] on NextEvent; this is for the caller that needs the
	// value now rather than the notification.
	Size() (Size, error)

	// Close releases the platform.
	Close() error
}

// NewUnixPlatform composes S2 out of S1 plus the decoder: on unix, that
// composition IS the platform seam. It is also where a byte-level
// scripted fake becomes a source of decoded events, so the decoder is
// testable with real terminal byte sequences and no terminal.
//
// The decoder is W3's; W1 composes the halves that do not need it.
func NewUnixPlatform(d Device) Platform { return &unixPlatform{d: d} }

type unixPlatform struct{ d Device }

// NextEvent is the decoder, and the decoder is the whole of W3's input
// half — ESC disambiguation held across a timeout inside the pump, which
// is why no consumer may read bytes for itself (§3).
func (p *unixPlatform) NextEvent(context.Context) (Event, error) { panic(notImplementedW3) }

func (p *unixPlatform) Write(b []byte) (int, error)    { return p.d.Write(b) }
func (p *unixPlatform) InstallMode(m ModeIntent) error { return p.d.InstallMode(m) }
func (p *unixPlatform) Size() (Size, error)            { return p.d.Size() }
func (p *unixPlatform) Close() error                   { return p.d.Close() }

// NewPlainPlatform is the non-TTY backend: a buffered reader, no mode,
// no decoration, and byte-identical output to today's piped path.
//
// It is not a degraded stub. It keeps the SAME BOOKS as the TTY backend
// — activity stack, mode-install count, abort window, cursor column —
// which is the load-bearing half of the verification posture: those
// facts emit no bytes, so the off-TTY suites become an ordering sensor
// for the exact defect class (bugs 3 and 4) that shipped through a green
// suite.
//
// The books themselves live on [Terminal], deliberately: a backend that
// kept its own copy would be a second opinion about ownership, and two
// opinions is the defect. What this constructor supplies is the half
// that genuinely differs — where bytes go, and the fact that there is no
// mode and no geometry to report.
func NewPlainPlatform(r io.Reader, w io.Writer) Platform {
	return &plainPlatform{r: r, w: w}
}

type plainPlatform struct {
	// r is held UNWRAPPED on purpose, and nothing here reads it. The plain
	// backend's ONE buffered reader lives on Terminal, built by the read
	// path in readline.go; a second reservoir over the same stream is what
	// strands bytes at a handover, which is liner's defect and not one to
	// reproduce in our own code. W3 makes the pump the reader and this the
	// place it reads from.
	r io.Reader
	w io.Writer
}

// NextEvent on the plain backend is the buffered reader feeding the same
// event stream. It lands with the pump in W3; nothing consumes events
// before then, which is why r is still unbuffered above.
func (p *plainPlatform) NextEvent(context.Context) (Event, error) { panic(notImplementedW3) }

func (p *plainPlatform) Write(b []byte) (int, error) {
	if p.w == nil {
		return len(b), nil
	}
	return p.w.Write(b)
}

// InstallMode has no meaning off a terminal, and saying so by panicking
// rather than by returning nil is deliberate: nothing may install a mode
// before W3 on EITHER backend, and a silent success here would make the
// plain backend the one place a premature install went unnoticed.
func (p *plainPlatform) InstallMode(ModeIntent) error { panic(notImplementedW3) }

// Size has no geometry to report; [Terminal.Size] supplies the
// [DefaultCols] x [DefaultRows] fallback, which the W3 row cap and
// status truncation still need a number for.
func (p *plainPlatform) Size() (Size, error) { return Size{}, errNotTerminal }

func (p *plainPlatform) Close() error { return nil }
