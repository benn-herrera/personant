package chat

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"personant/internal/clock"
	"personant/internal/turn"
)

const (
	// progressShowAfter is how long a wait must run before the indicator
	// draws anything at all. Short turns stay visually silent; only a wait
	// long enough to make the user wonder gets decorated.
	progressShowAfter = 2 * time.Second

	// progressFrameInterval is the animation period.
	progressFrameInterval = 120 * time.Millisecond

	// eraseLine returns the cursor to column 0 and clears to end of line —
	// the only ANSI sequence the indicator emits.
	eraseLine = "\r\x1b[K"
)

// progressFrames is the spinner cycle. Deliberately ASCII: it is one
// column wide on every terminal and font.
var progressFrames = [...]string{"|", "/", "-", "\\"}

// progress is the REPL's phase-labeled wait indicator. It renders one
// line — frame, the turn.Phase label, and elapsed seconds — while the
// runtime is busy, and clears it the moment content needs the terminal.
//
// It owns the single mutex guarding terminal writes. Three producers
// touch the terminal during a turn: the ticker goroutine (frames), the
// turn goroutine's phase callbacks, and the response body / offer
// prompts streaming through writer(). All three go through mu, so a
// frame can never interleave with content.
//
// A disabled progress (non-terminal stdout) writes ZERO bytes of its
// own — the piped and test paths must stay byte-for-byte undecorated.
type progress struct {
	out io.Writer

	// enabled is the session interactivity determination (interactiveTTY).
	// false → the indicator is inert and writer() is a pure pass-through.
	enabled bool
	// animate is false on a no-ANSI terminal (TERM=dumb or unset): the
	// indicator degrades to one static line per phase — no frames, no
	// cursor movement, no erase.
	animate bool

	// newTicker supplies the animation heartbeat; tests inject a
	// hand-driven channel so no assertion waits on the wall clock.
	newTicker func() (<-chan time.Time, func())
	// elapsed reports how long the current wait has run. Same signature as
	// clock.Since, which is the production value; tests inject a fake.
	elapsed func(clock.ProfilingTime) time.Duration

	mu        sync.Mutex
	running   bool
	label     turn.Phase
	drawn     bool // the current label has been rendered
	lineClean bool // the cursor sits at column 0
	// onLine says the indicator's OWN frame currently occupies the line the
	// cursor is on, so \r + erase-to-EOL reclaims it and no newline is
	// wanted. Distinct from lineClean: mid-line with onLine false means
	// FOREIGN content is on the line and must not be drawn over.
	onLine    bool
	frame     int
	startedAt clock.ProfilingTime
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// newProgress builds the indicator for a session. interactive comes from
// interactiveTTY — the one place the session decides it is driving a real
// terminal.
func newProgress(out io.Writer, interactive bool) *progress {
	return &progress{
		out:       out,
		enabled:   interactive,
		animate:   interactive && ansiCapable(os.Getenv("TERM")),
		newTicker: realTicker,
		elapsed:   clock.Since,
		lineClean: true,
	}
}

// ansiCapable reports whether TERM names a terminal that can handle the
// cursor-return + erase-to-end-of-line sequence. "dumb" and an unset TERM
// cannot; anything else is assumed to.
func ansiCapable(term string) bool { return term != "" && term != "dumb" }

func realTicker() (<-chan time.Time, func()) {
	t := time.NewTicker(progressFrameInterval)
	return t.C, t.Stop
}

// phase is the turn.State.OnPhase hook. It re-labels the indicator,
// starting it when this is the first phase of a wait (including a
// restart after content retired it — turn close re-announces its phase
// for exactly that reason).
func (p *progress) phase(ph turn.Phase) {
	if !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running && p.label == ph {
		return
	}
	p.label = ph
	p.drawn = false
	if !p.running {
		p.startLocked()
	}
	p.renderLocked()
}

// startLocked opens a wait: it resets the elapsed clock and, in animated
// mode, launches the ticker goroutine. The goroutine's only exit is
// stopCh, and stop joins it, so it can never outlive the turn.
func (p *progress) startLocked() {
	p.running = true
	p.frame = 0
	p.startedAt = clock.Profiling()
	if !p.animate {
		return // static mode has no heartbeat to run
	}
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
					p.renderLocked()
				}
				p.mu.Unlock()
			}
		}
	}()
}

// renderLocked draws the indicator. Nothing appears until the wait passes
// progressShowAfter.
func (p *progress) renderLocked() {
	if !p.running || p.elapsed(p.startedAt) < progressShowAfter {
		return
	}
	if !p.lineClean && !p.onLine {
		// Restarting under FOREIGN content that did not end in a newline:
		// take a fresh line rather than drawing over text the user is
		// reading. When the line is our own frame (onLine), the erase below
		// reclaims it in place — a newline there would scroll one line per
		// frame instead of animating.
		fmt.Fprintln(p.out)
		p.lineClean = true
	}
	if !p.animate {
		if !p.drawn {
			fmt.Fprintf(p.out, "%s...\n", p.label)
			p.drawn = true
		}
		return
	}
	fmt.Fprintf(p.out, "%s%s %s (%ds)",
		eraseLine, progressFrames[p.frame%len(progressFrames)], p.label,
		int(p.elapsed(p.startedAt).Seconds()))
	p.drawn = true
	p.lineClean = false
	p.onLine = true
}

// eraseLocked removes an animated frame from the terminal. The static
// (no-ANSI) rendering is a committed line and is deliberately left alone.
func (p *progress) eraseLocked() {
	if p.animate && p.drawn {
		io.WriteString(p.out, eraseLine)
		p.lineClean = true
	}
	p.drawn = false
	p.onLine = false // the line is given back; whatever lands next owns it
}

// stop retires the indicator and clears its line. Idempotent and safe
// from any goroutine — including the signal handler on the forced-exit
// path, where no deferred cleanup runs and a half-drawn frame would be
// the last thing left on the terminal. It joins the ticker goroutine, so
// no frame can land after it returns.
func (p *progress) stop() {
	if !p.enabled {
		return
	}
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	p.eraseLocked()
	p.label = ""
	stopCh, doneCh := p.stopCh, p.doneCh
	p.stopCh, p.doneCh = nil, nil
	p.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
		<-doneCh // the goroutine is gone before any caller writes content
	}
}

// atLineStart reports whether the cursor sits at column 0, i.e. whether
// the next prompt would land on a fresh line. The REPL asks the progress
// rather than inspecting the response body, because the indicator writes
// to the same line the body left the cursor on.
func (p *progress) atLineStart() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lineClean
}

// writer wraps w so that content emitted during a wait retires the
// indicator first. Every REPL producer that can fire mid-turn goes
// through it: the streamed response body (first byte hands the terminal
// back) and the §3.4 recall / §3.5 closure offer prompts.
func (p *progress) writer(w io.Writer) io.Writer { return &progressWriter{p: p, w: w} }

type progressWriter struct {
	p *progress
	w io.Writer
}

func (pw *progressWriter) Write(b []byte) (int, error) {
	// Idempotent: the first write of a wait clears the line and joins the
	// ticker goroutine; every write after that returns immediately.
	pw.p.stop()
	pw.p.mu.Lock()
	defer pw.p.mu.Unlock()
	n, err := pw.w.Write(b)
	if n > 0 {
		pw.p.lineClean = b[n-1] == '\n'
	}
	return n, err
}
