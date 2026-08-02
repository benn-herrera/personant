package chat

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/term"
	"personant/internal/turn"
)

// fakeDevice is the S1 byte-level stand-in (SOLUTION.md §7): it makes a
// session INTERACTIVE without a terminal, so the indicator's formatter and
// the line editor can both be driven off a TTY. Composed through the real
// term.NewUnixPlatform, so the test exercises the same S1→S2 composition
// production uses — the real decoder, the real pump — rather than a
// parallel one.
//
// script is the keystrokes, one entry per read(2). It is nil for the
// rendering fixtures, whose sessions have nothing to type: the stream then
// ends immediately, which is what a scripted piped session does too.
type fakeDevice struct {
	out  io.Writer
	size term.Size

	mu     sync.Mutex
	script [][]byte
}

// syncBuffer is the fixture's byte sink.
//
// term serializes its own writers under one lock, but that lock does not
// extend to a TEST's reads, and the animation goroutine is still writing
// while an assertion inspects the stream. So the sink carries its own —
// and, because every escape sequence term emits leaves it in a single
// Write, a read taken under this lock can never land inside one. That is
// the precondition screentest.Feed states.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.b.Bytes())
}

func (s *syncBuffer) String() string { return string(s.Bytes()) }

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

func (d *fakeDevice) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.script) == 0 {
		return 0, io.EOF
	}
	chunk := d.script[0]
	d.script = d.script[1:]
	if len(chunk) == 0 {
		return 0, nil // an expired read window: how a bare Esc is proven
	}
	return copy(p, chunk), nil
}

func (d *fakeDevice) Write(p []byte) (int, error) { return d.out.Write(p) }

// InstallMode succeeds. From W3 term owns the terminal mode, so a session
// over this device installs one at Open and restores it at Close — the
// arithmetic is asserted in internal/term, and refusing it here would
// only stop these fixtures opening at all.
func (d *fakeDevice) InstallMode(term.ModeIntent) error { return nil }

func (d *fakeDevice) Size() (term.Size, error) { return d.size, nil }
func (d *fakeDevice) Resized() <-chan struct{} { return nil }
func (d *fakeDevice) Close() error             { return nil }

// progressFixture drives a progress with an injected tick source and an
// injected elapsed clock, so no assertion here depends on real time
// passing (neither the 2s reveal threshold nor the frame interval) — and,
// per the terminal-test sleep ban, no assertion polls one either.
type progressFixture struct {
	p   *progress
	tm  *term.Terminal
	out *syncBuffer
	// size is the geometry the injected device reports, kept so the screen
	// model in render_test.go can be built with the same one.
	size term.Size

	mu        sync.Mutex
	ticks     chan time.Time // most recently created ticker channel
	stopCount int            // ticker Stop() calls, i.e. goroutine exits

	elapsedNS atomic.Int64
}

// fixtureSize is the geometry a fixture gets unless the test is ABOUT the
// geometry (the narrow-terminal checklist item passes its own).
var fixtureSize = term.Size{Cols: 80, Rows: 24}

// newProgressFixture builds an indicator over a terminal that is
// interactive (an injected platform) or not (buffers, the plain backend).
// ansi selects whether that terminal accepts the erase sequence.
//
// Stdout and Stderr are ONE buffer on purpose. On a real terminal both fds
// are the same tty, and term serializes them under one lock precisely so
// that an error mid-stream cannot interleave into the indicator's line —
// splitting them here would throw away the ordering that claim is about.
func newProgressFixture(t *testing.T, interactive, ansi bool, size term.Size) *progressFixture {
	t.Helper()
	f := &progressFixture{out: &syncBuffer{}, size: size}
	opts := term.Options{Stdout: f.out, Stderr: f.out}
	if interactive {
		opts.Platform = term.NewUnixPlatform(&fakeDevice{out: f.out, size: size})
		if ansi {
			opts.TermEnv = "xterm-256color"
		}
	}
	tm, err := term.Open(opts)
	if err != nil {
		t.Fatalf("term.Open: %v", err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	f.tm = tm
	f.p = newProgress(tm)
	f.p.newTicker = f.makeTicker
	f.p.elapsed = func(clock.ProfilingTime) time.Duration {
		return time.Duration(f.elapsedNS.Load())
	}
	return f
}

func (f *progressFixture) makeTicker() (<-chan time.Time, func()) {
	ch := make(chan time.Time)
	f.mu.Lock()
	f.ticks = ch
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		f.stopCount++
		f.mu.Unlock()
	}
}

func (f *progressFixture) setElapsed(d time.Duration) { f.elapsedNS.Store(int64(d)) }

// slot is what term believes occupies the ephemeral slot — the formatter's
// actual output, read from its one owner rather than reconstructed from
// bytes.
func (f *progressFixture) slot() string { return f.tm.State().Status }

func (f *progressFixture) rendered() string { return f.out.String() }

func (f *progressFixture) stops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCount
}

// tick delivers heartbeats THROUGH THE TICKER GOROUTINE and returns once
// the FIRST of them has been fully rendered. The channel is unbuffered, so
// the second send is accepted only after the goroutine has looped back to
// its select — which it does after renderLocked returns. That is a
// happens-before, not a duration: nothing here waits on an idle machine,
// which is exactly how two of the five §2 bugs were declared fixed.
//
// Note what it CANNOT prove, and use beat instead when that matters: the
// second tick's own render is still in flight when this returns, because
// the only proof a render finished is the NEXT send being accepted, and
// that send starts another render. There is always exactly one outstanding.
// So a byte count taken after tick can be overtaken by a frame — which is
// what it does under -race, at random.
//
// Use tick to assert that the GOROUTINE is alive and consuming.
func (f *progressFixture) tick(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	ch := f.ticks
	f.mu.Unlock()
	for i := 0; i < 2; i++ {
		select {
		case ch <- time.Time{}:
		case <-time.After(5 * time.Second):
			t.Fatal("ticker goroutine is not consuming its channel")
		}
	}
}

// beat delivers ONE heartbeat synchronously — the same two statements the
// ticker goroutine runs, on the test's own goroutine, under the same lock.
// On return the frame has either been emitted or deliberately not, with
// nothing left in flight.
//
// It is what every BYTE-LEVEL assertion uses. The animation heartbeat's
// contract is renderLocked(false), not "a value arrived on a channel", and
// driving it directly is the difference between asserting the rule and
// asserting the fixture's scheduling.
func (f *progressFixture) beat() {
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	if !f.p.running {
		return
	}
	f.p.frame++
	f.p.renderLocked(false)
}

// The regression that protects the sim harness, the scenario tests, and
// every piped invocation: a non-terminal session must not emit a single
// byte of decoration, while still passing content through untouched.
func TestProgress_DisabledWritesZeroBytes(t *testing.T) {
	f := newProgressFixture(t, false, false, fixtureSize)
	f.setElapsed(time.Hour)
	f.p.phase(turn.PhaseComposing)
	f.p.phase(turn.PhaseWaiting)
	f.p.phase(turn.PhaseClosing)
	f.p.stop()
	if got := f.rendered(); got != "" {
		t.Fatalf("disabled progress wrote %q, want no output", got)
	}
	if _, err := io.WriteString(f.tm.Out(), "body\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := f.rendered(); got != "body\n" {
		t.Errorf("content not passed through verbatim: %q", got)
	}
	if f.stops() != 0 {
		t.Errorf("disabled progress started a ticker")
	}
}

// Short turns stay visually quiet: nothing is handed to the slot until the
// wait exceeds progressShowAfter.
func TestProgress_QuietBelowThreshold(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(progressShowAfter - time.Millisecond)
	f.p.phase(turn.PhaseWaiting)
	if got := f.slot(); got != "" {
		t.Fatalf("drew before the reveal threshold: %q", got)
	}
	f.setElapsed(progressShowAfter)
	f.p.phase(turn.PhaseClosing)
	if got := f.slot(); !strings.Contains(got, string(turn.PhaseClosing)) {
		t.Errorf("nothing drawn past the threshold: %q", got)
	}
	f.p.stop()
}

// Each phase re-labels the ONE slot; a run of phase changes inside one
// wait is one ticker lifecycle, not one per label.
func TestProgress_PhaseTransitionsUpdateLabel(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	for _, ph := range []turn.Phase{turn.PhaseComposing, turn.PhaseWaiting, turn.PhaseReissuing} {
		f.p.phase(ph)
		if got := f.slot(); !strings.HasSuffix(got, string(ph)+" (5s)") {
			t.Errorf("phase %q not the current label; slot %q", ph, got)
		}
	}
	f.p.stop()
	if f.stops() != 1 {
		t.Errorf("phase changes within one wait started %d tickers, want 1", f.stops())
	}
}

// The ticker goroutine animates while the wait runs and is GONE once stop
// returns — stop joins it, so a leak would hang here rather than pass.
func TestProgress_TickerAnimatesThenExits(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	before := f.slot()
	f.tick(t)
	if got := f.slot(); got == before {
		t.Errorf("a heartbeat did not advance the frame: still %q", got)
	}

	f.p.stop()
	if f.stops() != 1 {
		t.Errorf("ticker goroutine did not exit exactly once: %d", f.stops())
	}
	if got := f.slot(); got != "" {
		t.Errorf("stop did not give the line back: slot still %q", got)
	}
	f.p.stop() // idempotent
	if f.stops() != 1 {
		t.Errorf("second stop restarted or re-stopped the ticker: %d", f.stops())
	}
}

// Bug 1, at the formatter's end of the seam. Content taking the terminal
// retires the slot; a HEARTBEAT must not take it back, or a spinner lands
// on top of the response the user is reading. An ANNOUNCEMENT (a new
// phase) still may — that is how turn close restarts the indicator.
//
// The indicator asks term whether its frame is still on screen instead of
// keeping a flag, which is precisely the flag bug 1 got wrong.
func TestProgress_HeartbeatDoesNotRetakeTheLineFromContent(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if f.slot() == "" {
		t.Fatal("indicator did not draw")
	}

	if _, err := io.WriteString(f.tm.Out(), "Hello, world."); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := f.slot(); got != "" {
		t.Fatalf("content did not retire the slot: %q", got)
	}
	f.beat()
	if got := f.slot(); got != "" {
		t.Errorf("a heartbeat took the line back from content: %q", got)
	}
	if got := f.rendered(); !strings.HasSuffix(got, "Hello, world.") {
		t.Errorf("a frame landed after the body started: %q", got)
	}

	f.p.phase(turn.PhaseClosing)
	if got := f.slot(); !strings.Contains(got, string(turn.PhaseClosing)) {
		t.Errorf("the closing phase did not restart the indicator: %q", got)
	}
	f.p.stop()
}

// The Esc hint appears and disappears with the abortable window rather
// than one frame late, and it is never inferred — a session with no
// working cbreak must not advertise a key that does nothing.
func TestProgress_AbortHintTracksTheWindow(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if strings.Contains(f.slot(), abortHintSuffix) {
		t.Errorf("hint advertised before the window opened: %q", f.slot())
	}
	f.p.setAbortHint(true)
	if !strings.Contains(f.slot(), abortHintSuffix) {
		t.Errorf("hint missing while the window is open: %q", f.slot())
	}
	f.p.setAbortHint(false)
	if strings.Contains(f.slot(), abortHintSuffix) {
		t.Errorf("hint outlived the window: %q", f.slot())
	}
	f.p.stop()
}

// TERM=dumb / no ANSI: the slot cannot be erased, so it commits one line
// per occupancy instead of animating over itself. The frames still tick;
// term drops them. No cursor games and no escape codes reach a terminal
// that cannot render them.
func TestProgress_DumbTerminalDegradesToStaticLines(t *testing.T) {
	f := newProgressFixture(t, true, false, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	f.p.phase(turn.PhaseWaiting) // repeat must not re-print
	f.beat()                     // nor may a heartbeat
	f.p.phase(turn.PhaseClosing)
	f.p.stop()

	got := f.rendered()
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("no-ANSI rendering emitted control sequences: %q", got)
	}
	lines := strings.Count(got, "\n")
	if lines != 2 {
		t.Errorf("want one committed line per label (2), got %d: %q", lines, got)
	}
	for _, want := range []string{string(turn.PhaseWaiting), string(turn.PhaseClosing)} {
		if !strings.Contains(got, want) {
			t.Errorf("label %q missing from the static rendering: %q", want, got)
		}
	}
}

// The indicator holds no registration of its own; this pins that driving
// it leaves term's ownership books untouched and unviolated, which is the
// parity assertion the off-TTY suite exists to make.
func TestProgress_LeavesOwnershipBooksClean(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	f.beat()
	f.p.stop()
	st := f.tm.State()
	if st.Violations != 0 {
		t.Errorf("violations = %d, want 0", st.Violations)
	}
	if len(st.Stack) != 0 {
		t.Errorf("activity stack = %v, want empty", st.Stack)
	}
	// ONE install, from Open, and driving the indicator adds none: §3's
	// "zero prompt/turn transitions" seen from the one object that used to
	// hold flags about the terminal.
	if st.ModeInstalls != 1 {
		t.Errorf("mode installs = %d, want the session's single install", st.ModeInstalls)
	}
}
