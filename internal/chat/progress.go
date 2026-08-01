package chat

import (
	"fmt"
	"sync"
	"time"

	"personant/internal/clock"
	"personant/internal/term"
	"personant/internal/turn"
)

const (
	// progressShowAfter is how long a wait must run before the indicator
	// draws anything at all. Short turns stay visually silent; only a wait
	// long enough to make the user wonder gets decorated.
	progressShowAfter = 2 * time.Second

	// progressFrameInterval is the animation period.
	progressFrameInterval = 120 * time.Millisecond

	// abortHintSuffix advertises Esc-to-abort while the turn is still in
	// its abortable window. Deliberately short: the rendered line is
	// "<frame> <label> (Ns)<suffix>" and the longest label is 20 columns,
	// so the whole line stays well inside a normal width. term truncates
	// it to the real width regardless (term.StatusColumnInset), so an
	// over-long line can no longer wrap and turn the redraw into a scroll.
	abortHintSuffix = "  esc to abort"
)

// progressFrames is the spinner cycle. Deliberately ASCII: it is one
// column wide on every terminal and font.
var progressFrames = [...]string{"|", "/", "-", "\\"}

// progress is the REPL's phase-labeled wait indicator — and, since W1, a
// FORMATTER and nothing more. It decides WHAT the wait line says (frame,
// the turn.Phase label, elapsed seconds, the Esc hint) and hands the
// finished string to term's ephemeral slot. Whether that string reaches
// the terminal, how it is erased, whether the terminal can erase at all,
// and what the cursor column is afterwards are all term's, under term's
// one lock.
//
// That split is the structural end of bug 1. The old indicator tracked
// "cursor at column 0" and "I own this line" in flags of its own, and
// mistook its own previous frame for foreign content — one line per frame
// instead of a redraw. There is now exactly one owner of both facts and
// this object holds neither, so there is no flag left here to get wrong.
//
// Its mutex guards its OWN fields only (the label, the frame counter, the
// ticker handles). Terminal serialization is term.Terminal's mutex, one
// level down; this one exists because the ticker goroutine and the turn
// goroutine both re-label.
//
// A non-interactive session never starts a wait at all, and the slot is
// inert underneath regardless — the piped and test paths stay
// byte-for-byte undecorated by both belts.
type progress struct {
	t  *term.Terminal
	st *term.Status

	// interactive is term's ONE TTY predicate, read once. false → the
	// indicator does no work; the slot would swallow it anyway, but there
	// is no reason to run a ticker for output nobody can see.
	interactive bool

	// newTicker supplies the animation heartbeat; tests inject a
	// hand-driven channel so no assertion waits on the wall clock.
	newTicker func() (<-chan time.Time, func())
	// elapsed reports how long the current wait has run. Same signature as
	// clock.Since, which is the production value; tests inject a fake.
	elapsed func(clock.ProfilingTime) time.Duration

	mu        sync.Mutex
	running   bool
	label     turn.Phase
	frame     int
	startedAt clock.ProfilingTime
	// abortHint says the turn is in its Esc-abortable window, so the label
	// should advertise the key. Set by control.arm / cleared by
	// control.disarm — never inferred here, because a session with no
	// working cbreak must not advertise a key that does nothing.
	abortHint bool
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// newProgress builds the indicator over the session's terminal. The
// interactivity determination is READ from term rather than recomputed:
// axiom §4.2 requires exactly one TTY predicate and term owns it.
func newProgress(t *term.Terminal) *progress {
	return &progress{
		t:           t,
		st:          t.Status(),
		interactive: t.Interactive(),
		newTicker:   realTicker,
		elapsed:     clock.Since,
	}
}

func realTicker() (<-chan time.Time, func()) {
	t := time.NewTicker(progressFrameInterval)
	return t.C, t.Stop
}

// phase is the turn.State.OnPhase hook. It re-labels the indicator,
// starting it when this is the first phase of a wait (including a
// restart after content retired it — turn close re-announces its phase
// for exactly that reason).
func (p *progress) phase(ph turn.Phase) {
	if !p.interactive {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running && p.label == ph {
		return
	}
	if p.running {
		// A new label re-OCCUPIES the slot rather than appending to it.
		// Releasing first is what lets a terminal with no erase commit a
		// fresh line per label instead of one per animation tick.
		p.st.Clear()
	} else {
		p.startLocked()
	}
	p.label = ph
	p.renderLocked(true)
}

// startLocked opens a wait: it resets the elapsed clock and launches the
// ticker goroutine. The goroutine's only exit is stopCh, and stop joins
// it, so it can never outlive the turn.
//
// The heartbeat runs whatever kind of terminal this is. It used to be
// suppressed on a no-ANSI one, which meant this object had to know
// whether the terminal could erase — the knowledge the W1 split moved to
// its owner. A ticker whose frames term drops costs one wakeup every
// 120ms on a TERM=dumb session; a second opinion about the terminal's
// capabilities costs a class of bug.
func (p *progress) startLocked() {
	p.running = true
	p.frame = 0
	p.startedAt = clock.Profiling()
	ticks, stopTicker := p.newTicker()
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	p.stopCh, p.doneCh = stopCh, doneCh
	go func() {
		defer close(doneCh)
		defer stopTicker()
		for {
			select {
			case <-stopCh:
				return
			case <-ticks:
				p.mu.Lock()
				if p.running {
					p.frame++
					p.renderLocked(false)
				}
				p.mu.Unlock()
			}
		}
	}()
}

// renderLocked hands the current frame to the slot.
//
// reveal distinguishes an ANNOUNCEMENT — a new phase, or the abort hint
// appearing — from the animation HEARTBEAT. An announcement claims the
// slot unconditionally. A heartbeat may only redraw a frame that is still
// on screen: content taking the terminal retires the slot, and a
// heartbeat that took it back would stamp a spinner over the response the
// user is reading. Whether the frame is still on screen is term's own
// bookkeeping (State().Status), not a flag kept here — asking the owner
// is exactly what bug 1's indicator failed to do.
func (p *progress) renderLocked(reveal bool) {
	if !p.running || p.elapsed(p.startedAt) < progressShowAfter {
		return
	}
	if !reveal && p.t.State().Status == "" {
		return
	}
	p.st.Set(fmt.Sprintf("%s %s (%ds)%s",
		progressFrames[p.frame%len(progressFrames)], p.label,
		int(p.elapsed(p.startedAt).Seconds()), p.hintLocked()))
}

// hintLocked renders the Esc-to-abort advertisement, or nothing.
func (p *progress) hintLocked() string {
	if p.abortHint {
		return abortHintSuffix
	}
	return ""
}

// setAbortHint toggles the Esc-to-abort advertisement and repaints, so
// the hint appears and disappears with the abortable window rather than
// one frame late. Safe from any goroutine; inert on a non-terminal.
func (p *progress) setAbortHint(on bool) {
	if !p.interactive {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.abortHint == on {
		return
	}
	p.abortHint = on
	p.renderLocked(true)
}

// stop retires the indicator and gives the line back. Idempotent and safe
// from any goroutine — including the signal handler on the forced-exit
// path, where no deferred cleanup runs and a half-drawn frame would be
// the last thing left on the terminal. It joins the ticker goroutine, so
// no frame can land after it returns.
//
// The slot is released UNCONDITIONALLY, before the running check. Two
// reasons, and both matter: the call must be safe on a wait that never
// started, and term.Status.Clear is also what closes an open reasoning
// run — the turn that produced reasoning and no body ends here, and it
// must not leave the terminal dim.
func (p *progress) stop() {
	if !p.interactive {
		return
	}
	p.mu.Lock()
	p.st.Clear()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	p.label = ""
	stopCh, doneCh := p.stopCh, p.doneCh
	p.stopCh, p.doneCh = nil, nil
	p.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
		<-doneCh // the goroutine is gone before any caller writes content
	}
}
