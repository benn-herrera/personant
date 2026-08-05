package chat

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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

// control is the session's terminal POLICY: what a key means, what an
// abort implies, and how a session shuts down. Since W3 it owns none of
// the mechanism — no termios, no reader, no key detection. term decodes;
// this decides.
//
// Two keys, two meanings, and the split is the point:
//
//   - Ctrl-C ENDS THE SESSION. At the prompt that is term's editor's
//     decision (an empty line hints, then a deliberate second press
//     aborts the read); mid-turn it arrives here as a decoded key and
//     takes the same clean-shutdown path. A second Ctrl-C during shutdown
//     force-quits.
//   - Esc ABORTS THE IN-FLIGHT TURN and returns to the prompt. It cancels
//     the turn's context only; the session is untouched. Esc is
//     RETRACTION (axiom §4.3): nothing from the turn enters memory.
//
// One value per session, read by three goroutines — the REPL loop (which
// also runs the turn and its phase callbacks), the signal-servicing
// goroutine, and term's pump, which is where the key handlers below run.
type control struct {
	// cancel cancels the SESSION context. Ctrl-C only.
	cancel context.CancelFunc

	// t is the arbiter. Every byte this file emits goes through it —
	// warnings and the shutdown announcement on Diag, the retraction
	// notice on Out — so an interrupt arriving mid-stream cannot
	// interleave into the wait indicator's line.
	t  *term.Terminal
	pr *progress

	// sh is the §4.4 shell runner, consulted by onSignal so a Ctrl-C during
	// a `$`/`#` command kills the command instead of the session. nil until
	// Run wires it, before the servicing goroutine starts.
	sh *shell.Runner

	// sigCh carries BOTH delivered SIGINTs and the in-band Ctrl-C the
	// editor's pump decodes mid-turn. One queue, because they are one
	// intent — and because a key handler must not do the shutdown work
	// itself: it runs on the pump goroutine, and shutting down joins the
	// pump (see term.Handler.Key). It is deliberately never closed, so the
	// pump's non-blocking send can never race a close and panic.
	sigCh chan os.Signal

	interrupts atomic.Int32
	inTurn     atomic.Bool

	mu sync.Mutex
	// abortTurn cancels the IN-FLIGHT turn; nil whenever the Esc window is
	// closed. Called under mu — see onEsc for why that ordering matters.
	abortTurn context.CancelFunc
	// reg is the turn's registration on term's activity stack: the thing
	// that puts Esc-to-abort and mid-turn Ctrl-C on the offer chain, and
	// the thing StateSnapshot.AbortWindow reports on.
	reg     *term.Registration
	aborted bool
	// lastPhase is the stage the turn had reached, kept so an abort record
	// can say WHERE the user gave up — the one piece of forensic context
	// the REPL has that the event log cannot reconstruct.
	lastPhase turn.Phase
}

func newControl(t *term.Terminal, pr *progress, cancel context.CancelFunc) *control {
	return &control{
		cancel: cancel,
		t:      t,
		pr:     pr,
		sigCh:  make(chan os.Signal, 2),
	}
}

// watchInterrupts installs the SIGINT handler and services the queue,
// returning the function that retires both.
//
// The servicing goroutine is what keeps shutdown OFF the pump goroutine.
// term's pump calls onKey in-line, and a handler that joined the pump
// would be joining itself; sending on sigCh hands the work to a goroutine
// that is free to do it.
//
// SIGINT reaching this handler at all is narrower than "Ctrl-C" now:
// term clears ISIG while it owns the fd, so what arrives here is a signal
// from OUTSIDE the terminal (`kill -INT`), a Ctrl-C inside a
// term.Handoff window where the entry mode is restored, or the in-band key
// this file forwards.
func (c *control) watchInterrupts() func() {
	signal.Notify(c.sigCh, os.Interrupt)
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			case <-c.sigCh:
				c.onSignal()
			}
		}
	}()
	return func() {
		signal.Stop(c.sigCh)
		close(quit)
		<-done
	}
}

// requestInterrupt queues an in-band Ctrl-C as if the kernel had delivered
// it. Non-blocking: two queued interrupts already mean "shut down, and
// force it if that wedges", and a third has nothing left to say.
func (c *control) requestInterrupt() {
	select {
	case c.sigCh <- os.Interrupt:
	default:
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
// This is C12 form (i)'s call site. revokeAbort is unconditional,
// idempotent and argument-free, and its OMISSION is detectable: the abort
// window would still be open in term's StateSnapshot past the boundary.
//
// The registration itself is NOT popped here. The §3.4 recall and §3.5
// closure resolvers prompt the user from inside turn close, and they push
// term.ActivityAsk OVER this entry — which is the whole reason the stack
// is LIFO. Popping at the boundary would take the floor out from under
// them and make every resolver read an out-of-order release.
func (c *control) onPhase(p turn.Phase) {
	if !abortablePhase(p) {
		c.revokeAbort()
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

// arm opens the Esc window for one turn by REGISTERING it. abortTurn must
// be the in-flight turn's cancel — never the session's.
//
// There is no terminal mode to change and no watcher goroutine to start:
// term installs ONE mode for the whole session and one pump reads the fd
// from Open to Close. The window is now purely a registration, which is
// why the indicator's hint and term's StateSnapshot.AbortWindow are the
// same fact rather than two that can disagree — the defect the W1.5 pinned
// test was holding open.
func (c *control) arm(abortTurn context.CancelFunc) {
	c.inTurn.Store(true)
	c.mu.Lock()
	c.aborted = false
	c.lastPhase = ""
	c.abortTurn = abortTurn
	c.reg = c.t.Push(term.ActivityTurn, term.Handler{Key: c.onKey, Abort: c.onEsc})
	c.mu.Unlock()
	c.pr.setAbortHint(true)
}

// revokeAbort closes the Esc window without releasing the registration.
// Idempotent; it fires at the pre-canonical phase boundary and again at
// turn end.
func (c *control) revokeAbort() {
	c.mu.Lock()
	reg := c.reg
	c.abortTurn = nil
	c.mu.Unlock()
	if reg == nil {
		return
	}
	reg.RevokeAbort()
	c.pr.setAbortHint(false)
}

// disarm ends the turn's registration: the Esc window closes and the entry
// leaves the stack, so the next prompt is the outermost input consumer
// again. Idempotent, and safe from any goroutine.
func (c *control) disarm() {
	c.inTurn.Store(false)
	c.revokeAbort()
	c.mu.Lock()
	reg := c.reg
	c.reg = nil
	c.mu.Unlock()
	if reg != nil {
		reg.Pop()
	}
}

// onKey is the turn's term.Handler.Key. It claims exactly one key.
//
// Ctrl-C mid-turn is SESSION EXIT (user ruling, 2026-07-31 — arbitration
// item 4), consistent with the second press at the prompt: the reference
// guidance genuinely runs out here, because Esc already carries retraction
// semantics the reference model has no analogue for, and a third
// context-dependent meaning for Ctrl-C would earn nothing.
//
// Everything else is DECLINED, which — with nothing below a turn on the
// stack — means keys typed while the model is working are discarded, as
// they always were. They are discarded by one reader now instead of racing
// three, which is the part that changed.
func (c *control) onKey(k term.Key) term.Disposition {
	if k.Name == term.KeyCtrl && k.Rune == 'c' {
		c.requestInterrupt()
		return term.Claimed
	}
	return term.Declined
}

// onEsc fires on a bare Esc while the window is open. The cancel is issued
// WHILE HOLDING mu, and that is the whole synchronisation argument for the
// pre-canonical guarantee:
//
//   - If revokeAbort takes mu first, abortTurn is already nil and no cancel
//     is ever issued — the turn runs to completion untouched.
//   - If onEsc takes mu first, the cancel is issued before revokeAbort
//     returns, hence before the phase callback returns, hence before the
//     turn's next substrate call. Every adapter op checks ctx.Err() on
//     entry, so that call fails inside the pre-canonical window and the
//     turn's deferred handler releases the recovery scope.
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
	// The notice takes its own row: a mid-stream abort leaves a partial
	// body on the current one. Idempotent with runOneTurn's re-align, and
	// kept here so the marker does not depend on the caller's ordering.
	realign(c.t)
	fmt.Fprintln(c.t.Out(), abortNotice)
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
// It runs on the servicing goroutine or on the REPL's, never on term's
// pump: the forced path calls term.Close, which joins the pump.
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
		c.disarm() // release the stack entry before the stack stops being ours
		// One Close: term owns the read path, the terminal mode and the
		// pump, so retiring the decoration, joining the reader, restoring
		// the entry mode and flushing history are all behind this call. No
		// deferred cleanup runs after it.
		_ = c.t.Close()
		os.Exit(forcedExitCode)
	}
	c.cancel()
	if announce {
		fmt.Fprintln(c.t.Diag(), "\ninterrupt received — abandoning the turn and shutting down (Ctrl-C again to force quit)")
	}
}

// onSignal handles one interrupt, delivered or in-band.
//
// A `$`/`#` child running is the one case where Ctrl-C does NOT mean "end
// the session": standard shell semantics are that it kills the running
// command and returns you to the prompt, and a shell escape that quit
// personant on Ctrl-C would be a trap. That window is also the only place
// a Ctrl-C is still a real SIGINT — term restores the entry mode, ISIG
// included, precisely so the kernel generates one for personant while the
// child runs in its own process group. Runner.Interrupt forwards it and
// reports that it consumed the interrupt; the session's interrupt COUNT is
// deliberately left untouched, so a later Ctrl-C at the prompt is still
// the first one.
//
// THE TWO-STEP CONSULT IS THE FINAL FORM. W4 was to generalise it into a
// LIFO offer chain over term's activity stack; that wave was DISSOLVED
// (2026-08-04), and no SIGINT hook was left behind on term.Handler. The
// ISIG ruling (2026-07-31, arbitration item 2) made
// Ctrl-C a decoded KEY wherever term owns the fd, which left a real SIGINT
// only two sources: the Handoff window — whose sole plausible claimant is
// the child runner, consulted first below — and a kill from outside the
// terminal, which nothing but the session can claim. Two fixed handlers in
// a fixed order are LIFO by construction, and a registration stack over a
// set of size two is a mechanism with no second case to serve.
//
// The step below ASKS the runner rather than inferring from state anyone
// else can see, and that is term.Disposition's rule rather than a local
// choice: the child's terminal window strictly CONTAINS the runner's claim
// window, so a consult that read ownership state would drop a Ctrl-C
// arriving in that sliver — which is bug 4.
//
// The wording is honest by construction: it announces only when a turn was
// actually in flight.
func (c *control) onSignal() {
	if c.sh != nil && c.sh.Interrupt() {
		return
	}
	c.exit(c.inTurn.Load())
}
