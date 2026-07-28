package chat

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/turn"
)

// progressFixture drives a progress with an injected tick source and an
// injected elapsed clock, so no assertion here depends on real time
// passing (neither the 2s reveal threshold nor the frame interval).
type progressFixture struct {
	p   *progress
	out *bytes.Buffer

	mu        sync.Mutex
	ticks     chan time.Time // most recently created ticker channel
	stopCount int            // ticker Stop() calls, i.e. goroutine exits

	elapsedNS atomic.Int64
}

func newProgressFixture(enabled, animate bool) *progressFixture {
	f := &progressFixture{out: &bytes.Buffer{}}
	f.p = &progress{
		out:       f.out,
		enabled:   enabled,
		animate:   animate,
		lineClean: true,
		newTicker: f.makeTicker,
		elapsed: func(clock.ProfilingTime) time.Duration {
			return time.Duration(f.elapsedNS.Load())
		},
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

// rendered reads the captured output under the progress mutex — the
// ticker goroutine writes to the same buffer.
func (f *progressFixture) rendered() string {
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	return f.out.String()
}

func (f *progressFixture) stops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCount
}

// tick delivers one heartbeat to the running ticker goroutine.
func (f *progressFixture) tick(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	ch := f.ticks
	f.mu.Unlock()
	select {
	case ch <- time.Time{}:
	case <-time.After(2 * time.Second):
		t.Fatal("ticker goroutine is not consuming its channel")
	}
}

// waitFor polls cond to a deadline. It synchronizes with the ticker
// goroutine; it never asserts a duration.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The regression that protects the sim harness, the scenario tests, and
// every piped invocation: a non-terminal session must not emit a single
// byte of decoration, while still passing content through untouched.
func TestProgress_DisabledWritesZeroBytes(t *testing.T) {
	f := newProgressFixture(false, false)
	f.setElapsed(time.Hour)
	f.p.phase(turn.PhaseComposing)
	f.p.phase(turn.PhaseWaiting)
	f.p.phase(turn.PhaseClosing)
	f.p.stop()
	if got := f.rendered(); got != "" {
		t.Fatalf("disabled progress wrote %q, want no output", got)
	}
	if _, err := io.WriteString(f.p.writer(f.out), "body\n"); err != nil {
		t.Fatalf("writer: %v", err)
	}
	if got := f.rendered(); got != "body\n" {
		t.Errorf("content not passed through verbatim: %q", got)
	}
	if f.stops() != 0 {
		t.Errorf("disabled progress started a ticker")
	}
}

// Short turns stay visually quiet: nothing is drawn until the wait
// exceeds progressShowAfter.
func TestProgress_QuietBelowThreshold(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(progressShowAfter - time.Millisecond)
	f.p.phase(turn.PhaseWaiting)
	if got := f.rendered(); got != "" {
		t.Fatalf("drew before the reveal threshold: %q", got)
	}
	f.setElapsed(progressShowAfter)
	f.p.phase(turn.PhaseClosing)
	if got := f.rendered(); !strings.Contains(got, string(turn.PhaseClosing)) {
		t.Errorf("nothing drawn past the threshold: %q", got)
	}
	f.p.stop()
}

func TestProgress_PhaseTransitionsUpdateLabel(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	for _, ph := range []turn.Phase{turn.PhaseComposing, turn.PhaseWaiting, turn.PhaseReissuing} {
		f.p.phase(ph)
		if got := f.rendered(); !strings.HasSuffix(got, string(ph)+" (5s)") {
			t.Errorf("phase %q not the current label; rendered %q", ph, got)
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
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	before := len(f.rendered())
	f.tick(t)
	waitFor(t, "an animated frame", func() bool { return len(f.rendered()) > before })

	f.p.stop()
	if f.stops() != 1 {
		t.Errorf("ticker goroutine did not exit exactly once: %d", f.stops())
	}
	if got := f.rendered(); !strings.HasSuffix(got, eraseLine) {
		t.Errorf("stop did not clear the line: %q", got)
	}
	if !f.p.atLineStart() {
		t.Error("cursor not at column 0 after stop")
	}
	f.p.stop() // idempotent
	if f.stops() != 1 {
		t.Errorf("second stop restarted or re-stopped the ticker: %d", f.stops())
	}
}

// The reported dogfooding bug: every frame landed on its own new line and
// scrolled the terminal. An animated wait must be ONE line redrawn in
// place — so across a run of frames the indicator emits zero newlines,
// and each frame is introduced by the erase sequence that reclaims the
// previous one.
func TestProgress_AnimatedFramesRedrawInPlace(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)

	const ticks = 4
	for i := 0; i < ticks; i++ {
		before := len(f.rendered())
		f.tick(t)
		waitFor(t, "an animated frame", func() bool { return len(f.rendered()) > before })
	}
	out := f.rendered()
	f.p.stop()

	if strings.Contains(out, "\n") {
		t.Errorf("animated frames emitted a newline — one line per frame instead of an in-place redraw: %q", out)
	}
	// Split on the erase sequence: a leading empty element (the output
	// opens with an erase) plus exactly one body per render.
	parts := strings.Split(out, eraseLine)
	if len(parts) != ticks+2 {
		t.Fatalf("want %d in-place redraws separated by %q, got %d: %q", ticks+1, eraseLine, len(parts)-1, out)
	}
	if parts[0] != "" {
		t.Errorf("first frame not preceded by the erase sequence: %q", out)
	}
	for _, body := range parts[1:] {
		if !strings.Contains(body, string(turn.PhaseWaiting)) {
			t.Errorf("frame %q is not a labeled indicator line; full output %q", body, out)
		}
	}
}

// A phase change mid-wait overwrites the existing indicator line rather
// than pushing it down: the label is not "content", it is the same line
// relabeled.
func TestProgress_LabelChangeOverwritesInPlace(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseComposing)
	f.p.phase(turn.PhaseWaiting)
	out := f.rendered()
	f.p.stop()

	if strings.Contains(out, "\n") {
		t.Errorf("label change emitted a newline instead of overwriting in place: %q", out)
	}
	if n := strings.Count(out, eraseLine); n != 2 {
		t.Fatalf("want 2 in-place redraws (one per label), got %d: %q", n, out)
	}
	tail := out[strings.LastIndex(out, eraseLine)+len(eraseLine):]
	if !strings.Contains(tail, string(turn.PhaseWaiting)) || strings.Contains(tail, string(turn.PhaseComposing)) {
		t.Errorf("the surviving line is not the new label: %q", tail)
	}
}

// The counterpart to TestProgress_RestartsForClosingPhase: content that
// already ended the line leaves the cursor at column 0, so the restarted
// indicator must NOT open a second, blank line.
func TestProgress_RestartAfterNewlineTerminatedContent(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if _, err := io.WriteString(f.p.writer(f.out), "body text\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.p.phase(turn.PhaseClosing)
	out := f.rendered()
	f.p.stop()

	after := out[strings.Index(out, "body text\n")+len("body text\n"):]
	if !strings.HasPrefix(after, eraseLine) {
		t.Errorf("restart on an already-clean line did not draw immediately: %q", after)
	}
}

// First body byte hands the terminal back, and the body text arrives
// intact and contiguous — no frame interleaved into it.
func TestProgress_FirstBodyByteRetiresIndicator(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if !strings.Contains(f.rendered(), string(turn.PhaseWaiting)) {
		t.Fatal("indicator did not draw")
	}
	w := f.p.writer(f.out)
	for _, chunk := range []string{"Hello, ", "world", "."} {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	out := f.rendered()
	if !strings.Contains(out, "Hello, world.") {
		t.Errorf("body corrupted by the indicator: %q", out)
	}
	if f.stops() != 1 {
		t.Errorf("body write did not stop the ticker: %d", f.stops())
	}
	tail := out[strings.Index(out, "Hello, "):]
	if strings.Contains(tail, string(turn.PhaseWaiting)) {
		t.Errorf("a frame landed after the body started: %q", tail)
	}
}

// After the stream ends the closing phase restarts the indicator — on a
// clean line, never drawn over the response the user is reading.
func TestProgress_RestartsForClosingPhase(t *testing.T) {
	f := newProgressFixture(true, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if _, err := io.WriteString(f.p.writer(f.out), "body text"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.p.phase(turn.PhaseClosing)

	out := f.rendered()
	if !strings.Contains(out, string(turn.PhaseClosing)) {
		t.Fatalf("closing phase did not restart the indicator: %q", out)
	}
	after := out[strings.Index(out, "body text")+len("body text"):]
	if !strings.HasPrefix(after, "\n") {
		t.Errorf("restarted indicator drew onto the body's line: %q", after)
	}
	f.p.stop()
	if f.stops() != 2 {
		t.Errorf("want 2 ticker lifecycles (wait + closing), got %d", f.stops())
	}
}

// TERM=dumb / no ANSI: one static line per phase, no frames, no cursor
// games, and no heartbeat goroutine at all.
func TestProgress_DumbTerminalDegradesToStaticLines(t *testing.T) {
	f := newProgressFixture(true, false)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	f.p.phase(turn.PhaseWaiting) // repeat must not re-print
	f.p.phase(turn.PhaseClosing)
	f.p.stop()

	want := string(turn.PhaseWaiting) + "...\n" + string(turn.PhaseClosing) + "...\n"
	got := f.rendered()
	if got != want {
		t.Errorf("static rendering = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("no-ANSI rendering emitted control sequences: %q", got)
	}
	if f.stops() != 0 {
		t.Errorf("static mode started %d tickers, want none", f.stops())
	}
}

func TestAnsiCapable(t *testing.T) {
	for _, tc := range []struct {
		term string
		want bool
	}{
		{"xterm-256color", true},
		{"screen", true},
		{"dumb", false},
		{"", false},
	} {
		if got := ansiCapable(tc.term); got != tc.want {
			t.Errorf("ansiCapable(%q) = %v, want %v", tc.term, got, tc.want)
		}
	}
}

func TestIsCharDevice(t *testing.T) {
	if isCharDevice(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer is not a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer f.Close()
	if isCharDevice(f) {
		t.Error("a regular file is not a terminal")
	}
}
