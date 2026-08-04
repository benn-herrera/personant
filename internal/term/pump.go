package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// The pump: ONE goroutine that calls [Platform.NextEvent] and nothing else
// ever reads the fd (§3).
//
// # Why a single reader is the whole answer
//
// Disambiguating a bare ESC from the ESC that introduces an arrow key
// needs bytes held read-but-not-yet-interpreted across a boundary in time.
// A lease on the fd can be released at an instant; the lookahead cannot,
// so "the lessee reads" makes every release lossy exactly when a sequence
// straddles it, and private per-consumer buffers reproduce liner's defect
// in our own code. Consumers therefore register on the LIFO activity stack
// and receive decoded events. They cannot name the fd; there is nothing to
// name it with.
//
// # Dispatch runs ON the pump goroutine
//
// A key is offered by calling the handler, not by handing it to a channel
// the consumer polls. Two consequences, both wanted: a disposition keyed on
// the editor's buffer (Ctrl-C clears the line) cannot race the editor,
// because the same goroutine owns both; and there is no per-consumer queue
// to strand anything in, which is the property the whole design turns on.
//
// The cost is stated at [Handler.Key]: a handler must not block, and must
// not call [Terminal.Close].
//
// # The reservoir
//
// Between one consumer retiring and the next registering — the prompt→turn
// boundary, where liner swallowed type-ahead — there is nobody to offer a
// key to. Those keys are HELD, and offered when a consumer appears. That is
// the whole of "one reservoir, in the pump": bytes are read once, decoded
// once, and wait in one place.
//
// The window is closed at BOTH ends, which is the part liner could not do.
// A completed editor RETIRES its handler the instant it has a line, before
// the reading goroutine has got as far as popping it — so a key typed in
// that sliver falls into the reservoir instead of being eaten by a consumer
// that has stopped consuming.
//
// A key a LIVE consumer declines is DROPPED, not held. That is deliberate
// and it is what keeps Esc reachable: a turn declines ordinary typing, and
// a pump that held those keys instead would stop reading the moment the
// user leaned on the keyboard — with the Esc they meant to press still in
// the terminal's buffer.

const (
	// reservoirMax bounds the held events. A burst longer than this is a
	// cat on the keyboard, not type-ahead; the excess is dropped from the
	// NEW end so the first keys — the ones with intent behind them, an Esc
	// among them — are the ones that survive.
	reservoirMax = 256

	// exitSignalBase is the conventional 128+signal exit status.
	exitSignalBase = 128
)

// startPump launches the reader. Callers must have installed the session
// mode first: a pump reading a terminal in canonical mode would block for
// a newline that the editor is supposed to be interpreting itself.
func (t *Terminal) startPump() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.mu.Lock()
	// reading is set on BOTH backends: it reports that term holds the read
	// path, which off a terminal is the buffered line reader rather than a
	// goroutine. Keeping one flag for one fact is what makes the off-TTY
	// suite an ordering sensor for the on-TTY one.
	t.reading = true
	if !t.owns {
		t.mu.Unlock()
		cancel() // no goroutine to own it; releasing here keeps vet honest
		return
	}
	t.pumpCancel, t.pumpDone = cancel, done
	t.mu.Unlock()
	go t.runPump(ctx, done)
}

// stopPump cancels the reader and JOINS it, so on return nothing in this
// process is reading stdin. Idempotent.
//
// It must never be called from a handler — that is the pump's own
// goroutine, and it would be joining itself. See [Handler.Key].
func (t *Terminal) stopPump() {
	t.mu.Lock()
	cancel, done := t.pumpCancel, t.pumpDone
	t.pumpCancel, t.pumpDone, t.reading = nil, nil, false
	t.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (t *Terminal) runPump(ctx context.Context, done chan struct{}) {
	defer close(done)
	var held []Event
	for {
		// Held events go first, and only once somebody can take them.
		if len(held) > 0 && t.hasConsumer() {
			ev := held[0]
			held = held[1:]
			t.offerKey(ev.Key)
			continue
		}
		ev, err := t.plat.NextEvent(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.drainThenEOF(ctx, held)
			}
			return
		}
		switch ev.Kind {
		case EventResize:
			// Never offered to a handler: a resize is term's own business
			// — it repaints the status slot and re-wraps the editor — and a
			// consumer that could see it would be a consumer with an
			// opinion about rendering.
			t.onResize(ev.Size)
			continue
		case EventEOF:
			t.drainThenEOF(ctx, held)
			return
		}
		if ev.Key.Name == KeyCtrl && ev.Key.Rune == 'z' && !ev.Key.Alt {
			// Ctrl-Z is consumed HERE and never delivered (see [KeyCtrl]).
			// Only the fd's owner can suspend correctly, and no client has
			// standing to observe or veto one.
			t.suspend()
			continue
		}
		// Nothing may OVERTAKE the reservoir. A consumer that registered
		// while this read was in flight makes hasConsumer true, and
		// offering the new key directly would deliver it ahead of the ones
		// already held — which is keystrokes arriving out of order, the
		// most confusing failure an editor can have.
		if len(held) > 0 || !t.hasConsumer() {
			if len(held) < reservoirMax {
				held = append(held, ev)
			}
			continue
		}
		t.offerKey(ev.Key)
	}
}

// drainThenEOF offers everything still held to the consumers that turn up,
// and only then reports end of input.
//
// The order matters and it is the type-ahead rule at the one boundary
// where it is easiest to get wrong: input ending is not a reason to throw
// away input already read. A scripted session whose whole stream arrives
// before the first prompt is exactly this case — and so is a user who
// typed the next line while the last turn was still finishing.
func (t *Terminal) drainThenEOF(ctx context.Context, held []Event) {
	for len(held) > 0 {
		if t.hasConsumer() {
			t.offerKey(held[0].Key)
			held = held[1:]
			continue
		}
		select {
		case <-ctx.Done():
			t.signalEOF()
			return
		case <-t.wake:
		}
	}
	t.signalEOF()
}

// hasConsumer reports whether any registration can still take a key.
//
// "Can still take" rather than "is on the stack": a retired editor is
// still an entry (its reading goroutine has not popped it yet) but has
// stopped consuming, and treating it as a consumer is what would let a key
// vanish into it. See [Registration.retire].
func (t *Terminal) hasConsumer() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.stack {
		if r.handler.Key != nil || (r.abort && r.handler.Abort != nil) {
			return true
		}
	}
	return false
}

// offerKey walks the activity stack in LIFO order, offering the key to
// each registration until one claims it.
//
// The abort window is checked FIRST at each level and consumes a bare Esc
// before [Handler.Key] sees it, which is what makes "Esc aborts the turn"
// a property of the registration rather than of every consumer's switch
// statement. Alt-Esc is not Esc.
//
// The stack is SNAPSHOT under the lock and the handlers are called outside
// it: a handler renders, and rendering takes the same lock.
func (t *Terminal) offerKey(k Key) {
	type offer struct {
		h     Handler
		abort bool
	}
	t.mu.Lock()
	offers := make([]offer, len(t.stack))
	for i, r := range t.stack {
		offers[i] = offer{h: r.handler, abort: r.abort}
	}
	t.mu.Unlock()

	bare := k.Name == KeyEsc && !k.Alt
	for i := len(offers) - 1; i >= 0; i-- {
		o := offers[i]
		if bare && o.abort && o.h.Abort != nil {
			o.h.Abort()
			return
		}
		if o.h.Key != nil && o.h.Key(k) == Claimed {
			return
		}
	}
}

// signalEOF releases every blocked read. The channel is CLOSED rather than
// written, because end of input is permanent: a second read must see it
// too, without the first having to put it back.
func (t *Terminal) signalEOF() {
	t.eofOnce.Do(func() { close(t.eofCh) })
}

// onResize repaints whatever occupies the live line at the new geometry.
func (t *Terminal) onResize(Size) {
	t.mu.Lock()
	ed, live, text := t.ed, t.statusLive, t.statusText
	t.mu.Unlock()
	if ed != nil {
		ed.refresh()
		return
	}
	if live {
		// Re-Set the same text: the slot re-truncates to the new width and
		// redraws in place. A frame sized for the old width is the shape
		// that turns a redraw into a scroll.
		t.status.Set(text)
	}
}

// suspend is Ctrl-Z, handled where it can be handled correctly (user
// ruling, 2026-07-31 — arbitration item 5).
//
// Today's mid-turn Ctrl-Z is a latent defect: it suspends with cbreak
// installed, so the shell inherits a terminal personant modified and `fg`
// returns to a mode nobody re-established. The fix is only available to
// whoever owns both halves of the transition — restore the ENTRY mode,
// raise the signal, and re-install the session mode when SIGCONT resumes
// the process.
//
// raiseTSTP is a field rather than a call so the lifecycle can be asserted
// without a test suspending the test binary.
func (t *Terminal) suspend() {
	t.mu.Lock()
	raise := t.raiseTSTP
	t.mu.Unlock()
	t.installMode(ModeEntry)
	raise()
	t.installMode(ModeSession)
	// The shell drew its own prompt over ours and wrote whatever it liked.
	// This is the case a resize is NOT: the block's rows are genuinely gone
	// rather than re-wrapped, nothing above the cursor is known to be ours,
	// and a fresh paint below is the only honest answer. See
	// [Terminal.invalidateEditorBlock].
	t.mu.Lock()
	t.column = ColumnUnknown
	ed := t.ed
	t.mu.Unlock()
	t.invalidateEditorBlock()
	if ed != nil {
		ed.refresh()
	}
}

// --- mode ownership ---------------------------------------------------

// installMode is the ONE place a terminal mode changes, which is what
// makes [StateSnapshot.ModeInstalls] a number worth asserting.
//
// A failure is reported and the count is NOT advanced: the books must say
// what is actually installed, or the sensor reports on itself.
func (t *Terminal) installMode(m ModeIntent) {
	if !t.owns {
		return
	}
	if err := t.plat.InstallMode(m); err != nil {
		fmt.Fprintf(t.Diag(), "warn: %v\n", err)
		return
	}
	t.mu.Lock()
	t.mode = m
	t.modeInstalls++
	t.mu.Unlock()
}

// --- exit signals (U4) -------------------------------------------------

// watchExitSignals installs the SIGTERM/SIGHUP handlers.
//
// U4: today the terminal is left in no-echo mode when either arrives,
// because signal.Notify registers os.Interrupt and nothing else. Mode
// ownership arrives with the pump, so exit-restore does too — and it has
// to be here rather than in a client, because a handler that restored a
// mode term never installed would be putting back somebody else's guess.
//
// The process still dies: catching these and returning to the REPL would
// turn `kill` into a no-op, which is a worse bug than the one being fixed.
// It dies with the conventional 128+signal status, AFTER Close has put the
// terminal back and flushed history.
func (t *Terminal) watchExitSignals() {
	// The watcher closes over a LOCAL, never over t.sigCh: Close clears the
	// field, and a goroutine still reading it would be reading a field
	// somebody else is writing.
	ch := make(chan os.Signal, 1)
	t.sigCh = ch
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		s, ok := <-ch
		if !ok {
			return // Close stopped delivery; nothing arrived
		}
		_ = t.Close()
		code := exitSignalBase
		if sig, isNum := s.(syscall.Signal); isNum {
			code += int(sig)
		}
		os.Exit(code)
	}()
}

// stopExitSignals stops delivery and lets the watcher retire. It
// deliberately does NOT join: the watcher may be inside Close at this very
// moment (that is what it does), and joining it from there would be Close
// waiting on itself.
func (t *Terminal) stopExitSignals() {
	ch := t.sigCh
	if ch == nil {
		return
	}
	t.sigCh = nil
	signal.Stop(ch)
	close(ch)
}
