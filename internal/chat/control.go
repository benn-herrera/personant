package chat

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"personant/internal/memops"
	"personant/internal/shell"
	"personant/internal/term"
	"personant/internal/turn"
)

// forcedExitCode is the conventional 128+SIGINT status for a session the
// user force-quit.
const forcedExitCode = 130

// control is the session's terminal-control state: the shared interrupt
// count, the session cancel, and the per-turn Esc-to-abort window. One
// value per session, read by three goroutines — the REPL loop (which also
// runs the turn and its phase callbacks), the signal handler, and the Esc
// watcher.
//
// Two keys, two meanings, and the split is the point:
//
//   - Ctrl-C ENDS THE SESSION, at the prompt and mid-turn alike. At the
//     prompt it is exactly /exit. Mid-turn it cancels the turn first and
//     then takes the same clean-shutdown path. A second Ctrl-C during
//     shutdown force-quits.
//   - Esc ABORTS THE IN-FLIGHT TURN and returns to the prompt. It cancels
//     the turn's context only; the session is untouched.
type control struct {
	// cancel cancels the SESSION context. Ctrl-C only.
	cancel context.CancelFunc

	// t is the arbiter. Every byte this file emits goes through it —
	// warnings and the shutdown announcement on Diag, the retraction
	// notice on Out — so an interrupt arriving mid-stream cannot
	// interleave into the wait indicator's line.
	t  *term.Terminal
	lr lineReader
	pr *progress

	// escAvailable is whether Esc-to-abort is wired up at all: an
	// interactive TTY whose mode we can actually drive. Written only from
	// the REPL goroutine (in arm), read only there.
	escAvailable bool

	// origTerm is the terminal mode the session found at startup, BEFORE
	// liner took the terminal. nil on a non-terminal session, or where the
	// platform has no termios path. See handoffTerminal.
	//
	// Assigned once by Run, before the signal-handler goroutine starts, and
	// never written again.
	origTerm *termState

	// sh is the §4.4 shell runner, consulted by onSignal so a Ctrl-C during
	// a `$`/`#` command kills the command instead of the session. nil until
	// Run wires it, on the same write-once-before-the-goroutine basis as
	// origTerm.
	sh *shell.Runner

	interrupts atomic.Int32
	inTurn     atomic.Bool

	mu sync.Mutex
	// abortTurn cancels the IN-FLIGHT turn; nil whenever the Esc window is
	// closed. Called under mu — see disarm for why that ordering matters.
	abortTurn context.CancelFunc
	watcher   *escWatcher
	saved     *termState
	aborted   bool
	// lastPhase is the stage the turn had reached, kept so an abort record
	// can say WHERE the user gave up — the one piece of forensic context
	// the REPL has that the event log cannot reconstruct.
	lastPhase turn.Phase
}

func newControl(t *term.Terminal, lr lineReader, pr *progress, cancel context.CancelFunc) *control {
	return &control{
		cancel:       cancel,
		t:            t,
		lr:           lr,
		pr:           pr,
		escAvailable: t.Interactive(),
	}
}

// abortablePhase reports whether a turn in phase p is still inside its
// PRE-CANONICAL window — the span in which turn.RunWithInfo's deferred
// handler releases the #94 recovery scope on an error return, so an abort
// leaves nothing for the next launch to recover.
//
// It is an ALLOW-list on purpose. A phase added later is non-abortable by
// default, which fails safe: the cost of not honouring Esc for a moment
// is a slightly late abort, while the cost of honouring it one step too
// late is a half-written turn that greets the user with a recovery banner.
// The §6.1 tool-execution phases are the one FAMILY on the list rather
// than a constant: the label carries the tool name, so it cannot be
// matched by value. turn.IsToolPhase owns the membership test, keeping the
// family's definition in one place. Tool rounds run entirely between model
// streams and add no canonical write, so the whole family is
// pre-canonical — and admitting it is what makes Esc cancel a slow fetch
// instead of waiting it out.
func abortablePhase(p turn.Phase) bool {
	if turn.IsToolPhase(p) {
		return true
	}
	switch p {
	case turn.PhaseComposing, turn.PhaseWaiting, turn.PhaseReissuing:
		return true
	default:
		return false
	}
}

// onPhase is the turn.State.OnPhase hook: it closes the Esc window at the
// pre-canonical boundary, then re-labels the indicator.
//
// Closing here does double duty. It is the correctness gate above, AND it
// is what hands the terminal back: the §3.4 recall and §3.5 closure
// resolvers PROMPT THE USER from inside turn close, and they run strictly
// after the first non-abortable phase. Retiring the watcher at that
// boundary is what guarantees nothing is holding stdin when liner wants
// it back mid-turn.
func (c *control) onPhase(p turn.Phase) {
	if !abortablePhase(p) {
		c.disarm()
	}
	c.mu.Lock()
	c.lastPhase = p
	c.mu.Unlock()
	c.pr.phase(p)
}

// abortPhase reports the stage the turn had reached when Esc fired.
func (c *control) abortPhase() turn.Phase {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPhase
}

// arm opens the Esc window for one turn. abortTurn must be the in-flight
// turn's cancel — never the session's.
//
// Called AFTER lineReader.prompt has returned, so liner is not holding
// the terminal; disarm restores liner's exact mode before the next prompt
// call. That ordering is load-bearing rather than merely tidy: liner
// captures whatever mode it finds at prompt entry and restores it at
// prompt exit, so leaving VMIN=0/VTIME=1 in place would make liner adopt
// it and read end-of-input from every 100 ms of user thinking time.
func (c *control) arm(abortTurn context.CancelFunc) {
	c.inTurn.Store(true)
	c.mu.Lock()
	c.aborted = false
	c.lastPhase = ""
	c.mu.Unlock()
	if !c.escAvailable {
		return
	}
	saved, err := enterCbreak()
	if err != nil {
		// Esc is simply unavailable for the rest of the session. The turn
		// still runs and Ctrl-C is unaffected; the indicator must not
		// advertise a key that does nothing, so latch it off.
		c.escAvailable = false
		fmt.Fprintf(c.t.Diag(), "warn: esc-to-abort unavailable: %v\n", err)
		return
	}
	c.mu.Lock()
	c.abortTurn = abortTurn
	c.saved = saved
	c.watcher = startEscWatcher(readStdin, c.onEsc)
	c.mu.Unlock()
	c.pr.setAbortHint(true)
}

// disarm closes the Esc window: it joins the watcher goroutine and
// restores the mode liner left, so nothing is reading the terminal — and
// nothing has changed under it — when the next prompt runs. Idempotent;
// it fires both at the pre-canonical phase boundary and at turn end.
func (c *control) disarm() {
	c.inTurn.Store(false)
	c.mu.Lock()
	w, saved := c.watcher, c.saved
	c.watcher, c.saved, c.abortTurn = nil, nil, nil
	c.mu.Unlock()
	if w == nil {
		return
	}
	c.pr.setAbortHint(false)
	w.close() // joined BEFORE the restore: no read may straddle a mode change
	if err := saved.restore(); err != nil {
		fmt.Fprintf(c.t.Diag(), "warn: %v\n", err)
	}
}

// onEsc fires on a bare Esc. The cancel is issued WHILE HOLDING mu, and
// that is the whole synchronisation argument for the pre-canonical
// guarantee:
//
//   - If disarm takes mu first, abortTurn is already nil and no cancel is
//     ever issued — the turn runs to completion untouched.
//   - If onEsc takes mu first, the cancel is issued before disarm returns,
//     hence before the phase callback returns, hence before the turn's
//     next substrate call. Every adapter op checks ctx.Err() on entry, so
//     that call fails inside the pre-canonical window and the turn's
//     deferred handler releases the recovery scope.
//
// Issuing the cancel after releasing mu would break the second case: the
// turn could already be past its last pre-canonical step.
func (c *control) onEsc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.abortTurn == nil {
		return
	}
	c.aborted = true
	c.abortTurn()
	c.abortTurn = nil
}

// tookAbort reports whether Esc fired during the turn just run. The REPL
// keys off this rather than inspecting the returned error, because a
// Ctrl-C-cancelled turn produces the same context.Canceled and must NOT
// be reported as an abort-and-continue.
func (c *control) tookAbort() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted
}

// reportRetraction closes out an Esc-aborted turn: it marks the screen
// and writes the one durable trace.
//
// The screen marker exists because a mid-stream abort leaves partial
// response tokens the terminal cannot take back — they may have scrolled
// and may span many rows. Without a marker the transcript would assert
// something false: a response the user can read that is not, and never
// was, in memory.
//
// The event line carries the turn id, the phase the user gave up in, and
// the input's size — NEVER the text. §2.8 lines are single-line free-form
// and a multi-line prompt would break the format, and more to the point
// the retraction rule says the text gets no durable home. It is not lost
// — it is in ↑ history and is re-offered as an editable default at the
// next prompt — but it is not in MEMORY, and re-submitting it is the only
// way it ever will be.
func (c *control) reportRetraction(ctx context.Context, ops memops.MemoryOps, turnID, input string) error {
	out := c.t.Out()
	// term owns the cursor column, as a tri-state: ColumnUnknown (a child
	// wrote whatever it liked) takes the newline too, which is the
	// deterministic answer rather than a guess.
	if c.t.State().Column != term.ColumnStart {
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, abortNotice)
	return ops.Log(ctx, memops.LogCategorySystem, "turn-aborted",
		fmt.Sprintf("turn=%s phase=%q bytes=%d", turnID, c.abortPhase(), len(input)))
}

// exit begins session shutdown, or forces it.
//
// The first call cancels the session context so an in-flight turn unwinds
// and the REPL leaves through the clean-shutdown path — recaller close,
// the §3.11 session-close checkpoint, the session-end log — all of which
// deliberately run on the UNCANCELLED parent context, which is why a
// Ctrl-C exit loses no state. A second call forces an immediate exit: the
// safety valve for a shutdown that itself wedges.
//
// announce is true only when something was actually interrupted. An
// ordinary quit says nothing at all: "shutting down (Ctrl-C again to
// force quit)" is a warning, and there is nothing to warn about when the
// user simply asked to leave — least of all at the prompt, where the
// session is already over by the time the line would be printed.
func (c *control) exit(announce bool) {
	// Clear any in-flight indicator FIRST. On the forced path below no
	// deferred cleanup runs, so a half-drawn frame would be the last thing
	// on the terminal; on the clean path it keeps the notice from being
	// drawn over.
	c.pr.stop()
	if c.interrupts.Add(1) >= 2 {
		c.disarm()       // hand the terminal back before it stops being ours
		_ = c.lr.close() // restore the pre-session mode and flush history
		_ = c.t.Close()  // retire any decoration; no deferred cleanup runs here
		os.Exit(forcedExitCode)
	}
	c.cancel()
	if announce {
		fmt.Fprintln(c.t.Diag(), "\ninterrupt received — abandoning the turn and shutting down (Ctrl-C again to force quit)")
	}
}

// handoffTerminal hands the terminal to a child process and returns the
// function that takes it back. Both halves are no-ops on a session that
// does not drive a terminal, so the caller never branches.
//
// This is necessary, not defensive. liner.NewLiner applies its mode ONCE
// for the whole session — ICANON and ECHO are off from the first prompt
// until Close, not just while Prompt is blocked — so a child spawned
// mid-session inherits a terminal with no echo and no line discipline.
// Anything that reads input, or merely expects typed characters to
// appear, misbehaves. Restoring the mode personant found at STARTUP is
// what gives the child a normal terminal.
//
// The mode in force at handoff time is captured and put back afterwards,
// rather than assuming it was liner's: that way an Esc-window cbreak mode
// left in place by some future caller is also restored faithfully.
func (c *control) handoffTerminal() func() {
	if c.origTerm == nil {
		return func() {}
	}
	current, err := captureTerm()
	if err != nil {
		fmt.Fprintf(c.t.Diag(), "warn: %v\n", err)
		return func() {}
	}
	if err := c.origTerm.restore(); err != nil {
		fmt.Fprintf(c.t.Diag(), "warn: %v\n", err)
		return func() {}
	}
	return func() {
		if err := current.restore(); err != nil {
			fmt.Fprintf(c.t.Diag(), "warn: %v\n", err)
		}
	}
}

// onSignal handles one delivered SIGINT.
//
// A `$`/`#` child running is the one case where Ctrl-C does NOT mean "end
// the session": standard shell semantics are that it kills the running
// command and returns you to the prompt, and a shell escape that quit
// personant on Ctrl-C would be a trap. Runner.Interrupt forwards the
// signal to the child's process group and reports that it consumed the
// interrupt; the session's interrupt COUNT is deliberately left untouched,
// so a later Ctrl-C at the prompt is still the first one and behaves
// exactly as it would have.
//
// Otherwise the wording is honest by construction: it announces only when
// a turn was actually in flight.
func (c *control) onSignal() {
	if c.sh != nil && c.sh.Interrupt() {
		return
	}
	c.exit(c.inTurn.Load())
}
