package term

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// --- fixtures --------------------------------------------------------

// fakeDevice is the S1 byte-level stand-in (§7). It is what lets an
// INTERACTIVE session be driven with no terminal, and it plugs in through
// the real NewUnixPlatform, so these tests exercise the same S1→S2
// composition production uses — the real decoder, the real pump, the real
// editor — rather than a parallel one.
//
// A SCRIPT is a list of reads. Each entry is one Read's worth of bytes; an
// EMPTY entry is a read window that expired with nothing typed, which is
// the decoder's only proof that an ESC was bare. There is no clock here
// and no sleep anywhere: the timeout is data.
//
// Once the script is exhausted the stream ENDS. A fake that idled instead
// would either spin or have to be woken, and every test below wants the
// session to finish on its own.
type fakeDevice struct {
	mu     sync.Mutex
	script []scriptStep
	out    bytes.Buffer
	modes  []ModeIntent

	size    Size
	sizeErr error
	// sizeCalls counts ioctl-equivalent calls, so U12 can be asserted:
	// a failing size must not re-select the backend, however often it is
	// asked.
	sizeCalls int

	resized chan struct{}

	// hold, when non-nil, parks the read once the script is exhausted
	// instead of ending the stream — a terminal with nobody typing at it.
	// Closing it releases the read. It is what lets a test assert on a read
	// that is genuinely IN PROGRESS.
	hold chan struct{}

	// term and ready are the ORDERING BARRIER; see [fakeDevice.awaitConsumer]
	// for what it is for. ready nil means no barrier: this script has no
	// out-of-band step to order, so it keeps the read-ahead. When it is
	// non-nil it is CLOSED once term has been published, and term is read
	// only after that receive.
	term  *Terminal
	ready chan struct{}

	// stop releases a parked read at teardown, so a fixture whose consumer
	// never registers ends the test instead of hanging it. Closed by the
	// fixture's cleanup BEFORE Terminal.Close, which joins the pump.
	stop chan struct{}
}

// scriptStep is one read's worth of terminal.
type scriptStep struct {
	in     []byte
	resize *Size
}

// typed, idle and resized build script steps.
//
//   - typed delivers bytes.
//   - idle is a read window that expired with nothing typed — the ONLY
//     thing that resolves a pending ESC, and the reason no test here needs
//     a clock.
//   - resized changes the reported geometry and rings the out-of-band
//     channel, exactly as SIGWINCH does between two reads.
func typed(s string) scriptStep { return scriptStep{in: []byte(s)} }
func idle() scriptStep          { return scriptStep{} }
func resized(cols, rows int) scriptStep {
	return scriptStep{resize: &Size{Cols: cols, Rows: rows}}
}

// awaitConsumer is the ORDERING BARRIER, and it exists because the pump
// consumes some things ITSELF rather than offering them down the activity
// stack — see [pumpConsumed]. Both cases are correct: SIGWINCH is a signal
// and never a byte, so [Device.Resized] is the only seam that can carry a
// resize, and only the fd's owner can suspend correctly on Ctrl-Z.
//
// That correctness is also why a script interleaving such a step between
// typed() steps has, on its own, NO ordering guarantee. The pump READS
// AHEAD of the consumer: keys read before an editor registers wait in its
// reservoir, but a pump-consumed step is not held — it is acted on
// immediately. Under parallel-package load the pump wins that race, the
// geometry changes (or the block is invalidated) before the keystroke the
// step was scripted to follow is ever dispatched, and the frames come out
// wrong.
//
// The barrier is in the FIXTURE, not the pump. For such a script the fake
// serves no step until the terminal has a registered consumer. With no
// read-ahead the reservoir is empty by construction, and dispatch then runs
// synchronously ON the pump goroutine between one Read and the next — so
// the script's order IS the delivery order.
//
// It is a happens-before, not a sleep and not a poll: [Terminal.Push]
// publishes the stack entry under t.mu and then nudges t.wake, and this
// blocks on that nudge. The nudge is put BACK, because it is the pump's own
// signal (drainThenEOF waits on it) and peeking must not consume it.
//
// Scripts WITHOUT a resize keep the read-ahead on purpose: the reservoir is
// the subject of TestPump_EscSurvivesTypeAheadAcrossThePromptTurnBoundary
// and TestPump_TypeAheadBetweenTwoReadsLandsInTheNextLine, and serializing
// every fixture would make those tests vacuous rather than green.
func (d *fakeDevice) awaitConsumer() {
	if d.ready == nil {
		return
	}
	select {
	case <-d.ready:
	case <-d.stop:
		return
	}
	consumed := false
	for !d.term.hasConsumer() {
		select {
		case <-d.term.wake:
			consumed = true
		case <-d.stop:
			return
		}
	}
	if consumed {
		select {
		case d.term.wake <- struct{}{}:
		default:
		}
	}
}

// ctrlZByte is Ctrl-Z in band. ISIG is cleared in [ModeSession], so it
// arrives as a byte and the pump suspends on it itself.
const ctrlZByte = 0x1a

// pumpConsumed reports whether a step carries something the PUMP consumes
// ITSELF instead of offering it down the activity stack. There are exactly
// two such things, both named in pump.go: a RESIZE ("never offered to a
// handler") and CTRL-Z ("consumed HERE and never delivered").
//
// They are the same hazard for the same reason, which is why one predicate
// covers both. An ordinary key read before a consumer exists is HELD in the
// pump's reservoir and delivered in order once one appears; a pump-consumed
// step is acted on immediately and cannot queue behind the reservoir. So a
// script that mixes the two has an unspecified delivery order, and it is
// exactly those scripts that need [fakeDevice.awaitConsumer].
func pumpConsumed(s scriptStep) bool {
	return s.resize != nil || bytes.IndexByte(s.in, ctrlZByte) >= 0
}

func (d *fakeDevice) Read(p []byte) (int, error) {
	// Outside the lock, and only while a step is left to serve: the barrier
	// waits on the terminal, whose render path writes to this device, and an
	// EXHAUSTED script must still reach its EOF (or its hold) after the last
	// consumer has retired.
	d.mu.Lock()
	pending := len(d.script) > 0
	d.mu.Unlock()
	if pending {
		d.awaitConsumer()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.script) == 0 {
		if d.hold != nil {
			d.mu.Unlock()
			<-d.hold
			d.mu.Lock()
		}
		return 0, io.EOF
	}
	step := d.script[0]
	d.script = d.script[1:]
	if step.resize != nil {
		d.size = *step.resize
		select {
		case d.resized <- struct{}{}:
		default:
		}
		return 0, nil
	}
	if len(step.in) == 0 {
		return 0, nil
	}
	return copy(p, step.in), nil
}

func (d *fakeDevice) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.out.Write(p)
}

func (d *fakeDevice) InstallMode(m ModeIntent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.modes = append(d.modes, m)
	return nil
}

func (d *fakeDevice) Size() (Size, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sizeCalls++
	return d.size, d.sizeErr
}

func (d *fakeDevice) Resized() <-chan struct{} { return d.resized }
func (d *fakeDevice) Close() error             { return nil }

func (d *fakeDevice) emitted() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.out.String()
}

func (d *fakeDevice) installed() []ModeIntent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ModeIntent(nil), d.modes...)
}

// fixture is a terminal plus the two byte sinks behind it. Nothing here
// touches a real fd, and nothing sleeps.
type fixture struct {
	t     *Terminal
	dev   *fakeDevice
	diag  bytes.Buffer
	plain bytes.Buffer
}

// newTTY opens an interactive terminal over the fake device with no script
// — the stream ends at once, which is what the OUTPUT assertions want.
func newTTY(t *testing.T, termEnv string, cols int) *fixture {
	t.Helper()
	return newScripted(t, termEnv, Size{Cols: cols, Rows: 24}, nil)
}

// newScripted opens an interactive terminal whose keystrokes are the given
// script. This is the harness for everything the pump, the decoder and the
// editor do; opt tunes the Options a particular test is about.
func newScripted(t *testing.T, termEnv string, size Size, script []scriptStep, opt ...func(*Options)) *fixture {
	t.Helper()
	f := &fixture{dev: &fakeDevice{
		size:    size,
		script:  script,
		resized: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}}
	// The barrier is armed by the SCRIPT, not by the test. See
	// [fakeDevice.awaitConsumer].
	if slices.ContainsFunc(script, pumpConsumed) {
		f.dev.ready = make(chan struct{})
	}
	opts := Options{
		Platform: NewUnixPlatform(f.dev),
		Stderr:   &f.diag,
		TermEnv:  termEnv,
	}
	for _, o := range opt {
		o(&opts)
	}
	tm, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Nothing may raise SIGTSTP under `go test`: it would stop the test
	// binary, which no assertion can come back from. The lifecycle AROUND
	// the suspend is what is worth asserting, and this is what makes it
	// assertable. Under the lock, because the pump reads it.
	tm.mu.Lock()
	tm.raiseTSTP = func() {}
	tm.mu.Unlock()
	t.Cleanup(func() {
		// stop first: Close joins the pump, and a pump parked at the barrier
		// would be joining a read nobody is going to release.
		close(f.dev.stop)
		_ = tm.Close()
	})
	f.t = tm
	// Published LAST, so nothing the pump can already be holding — the mode,
	// raiseTSTP, the cleanup — is still being written when the barrier lifts.
	if f.dev.ready != nil {
		f.dev.term = tm
		close(f.dev.ready)
	}
	return f
}

// newPipe opens a non-interactive terminal over buffers — the plain
// backend, which is the path every test, the sim and every piped
// invocation take.
func newPipe(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	tm, err := Open(Options{Stdin: strings.NewReader(""), Stdout: &f.plain, Stderr: &f.diag})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	f.t = tm
	return f
}

// screen is every byte that reached stdout, whichever backend carried it.
func (f *fixture) screen() string {
	if f.dev != nil {
		return f.dev.emitted()
	}
	return f.plain.String()
}

// --- backend selection (U12) -----------------------------------------

// U12: the backend is chosen ONCE, at Open, for the session. The cases
// below are every input shape Open can be handed.
func TestOpen_BackendSelectedOnce(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	t.Cleanup(func() { _ = regular.Close() })

	tests := []struct {
		name string
		opts Options
		want bool
	}{
		{"buffers", Options{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}, false},
		{"regular file on both ends", Options{Stdin: regular, Stdout: regular}, false},
		{"nil streams", Options{}, false},
		{"injected platform", Options{Platform: NewUnixPlatform(&fakeDevice{})}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm, err := Open(tc.opts)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = tm.Close() }()
			if got := tm.Interactive(); got != tc.want {
				t.Errorf("Interactive() = %v, want %v", got, tc.want)
			}
		})
	}
}

// An ioctl failure is a FALLBACK, never a re-selection: the session keeps
// the backend it opened with, however many times the failing call is
// made. A backend that changed underneath a running turn would be the
// same ownership ambiguity in a smaller window.
func TestSize_IoctlFailureDoesNotDegradeTheBackend(t *testing.T) {
	f := newTTY(t, "xterm", 100)
	f.dev.sizeErr = errors.New("ioctl: ENOTTY")

	for i := 0; i < 3; i++ {
		if got := f.t.Size(); got != (Size{Cols: DefaultCols, Rows: DefaultRows}) {
			t.Fatalf("Size() = %v, want the %dx%d fallback", got, DefaultCols, DefaultRows)
		}
		if !f.t.Interactive() {
			t.Fatal("a failing size call demoted the session to the plain backend")
		}
	}
	if f.dev.sizeCalls < 3 {
		t.Errorf("size was asked %d times, want at least 3 (the fallback must not cache a demotion)", f.dev.sizeCalls)
	}
}

// The plain backend has no geometry, and the callers that need a number
// still get one.
func TestSize_PlainBackendFallsBack(t *testing.T) {
	f := newPipe(t)
	if got := f.t.Size(); got != (Size{Cols: DefaultCols, Rows: DefaultRows}) {
		t.Errorf("Size() = %v, want the default geometry", got)
	}
}

// --- channel routing --------------------------------------------------

// Out lands on stdout, Diag on stderr, and both are committed. The two
// fds differ; the serialization point does not.
func TestChannels_RouteToTheirOwnFds(t *testing.T) {
	f := newPipe(t)
	fmt.Fprint(f.t.Out(), "body")
	fmt.Fprint(f.t.Diag(), "warn")
	if got := f.plain.String(); got != "body" {
		t.Errorf("stdout = %q, want %q", got, "body")
	}
	if got := f.diag.String(); got != "warn" {
		t.Errorf("stderr = %q, want %q", got, "warn")
	}
}

// The (tty-only, scrollback) cell: reasoning writes ZERO bytes off a
// terminal, whatever the /thinking setting says, so piped, sim and test
// output are unaffected. It still reports the bytes consumed — a short
// write is an error, and there is no error here.
func TestReasoning_WritesNothingOffATerminal(t *testing.T) {
	f := newPipe(t)
	n, err := io.WriteString(f.t.Reasoning(), "model scratch")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len("model scratch") {
		t.Errorf("reported %d bytes written, want %d — a short write is an error", n, len("model scratch"))
	}
	if got := f.screen() + f.diag.String(); got != "" {
		t.Errorf("reasoning reached a non-terminal session: %q", got)
	}
}

// The forbidden cell of the §5 2x2 is (always, ephemeral) — a pipe has no
// erasure. It is enforced by the ephemeral slot being tty-only: off a
// terminal it emits nothing at all, so no caller can produce erasable
// output on a stream that cannot erase.
func TestStatus_InertOffATerminal(t *testing.T) {
	f := newPipe(t)
	f.t.Status().Set("waiting (5s)")
	f.t.Status().Set("closing (7s)")
	f.t.Status().Clear()
	if got := f.screen() + f.diag.String(); got != "" {
		t.Errorf("the ephemeral slot wrote %q on a stream that cannot erase", got)
	}
	if got := f.t.State().Status; got != "" {
		t.Errorf("State().Status = %q on a non-terminal session, want empty", got)
	}
}

// --- the serialization point ------------------------------------------

// The whole of W1's rendering contract, in one sequence: content retires
// the ephemeral slot AND closes an open reasoning run before it lands, so
// the answer can never arrive dim or jammed onto the tail of a scratch
// line.
func TestSerialization_ContentRetiresDecorationFirst(t *testing.T) {
	f := newTTY(t, "xterm-256color", 80)
	f.t.Status().Set("waiting (5s)")
	io.WriteString(f.t.Reasoning(), "first ")
	io.WriteString(f.t.Reasoning(), "second")
	io.WriteString(f.t.Out(), "the answer")

	want := eraseLine + "waiting (5s)" + // the slot draws
		eraseLine + // reasoning retires it
		dimOn + "first " + "second" + // one run, opened once
		dimOff + "\n" + // closed by the content write
		"the answer"
	if got := f.screen(); got != want {
		t.Fatalf("emitted\n  %q\nwant\n  %q", got, want)
	}
	if n := strings.Count(f.screen(), dimOn); n != 1 {
		t.Errorf("dim opened %d times across one run, want 1", n)
	}
	if got := f.t.State().Column; got != ColumnMid {
		t.Errorf("column after unterminated content = %v, want ColumnMid", got)
	}
}

// The stderr/indicator collision W1 closes: Diag lands on a different fd
// but passes through the same point, so an error arriving mid-stream
// retires the indicator instead of interleaving into its line.
func TestSerialization_DiagRetiresTheStatusLine(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	f.t.Status().Set("waiting (5s)")
	fmt.Fprintln(f.t.Diag(), "warn: something")

	if got := f.screen(); got != eraseLine+"waiting (5s)"+eraseLine {
		t.Errorf("stdout = %q; the error did not retire the slot in place", got)
	}
	if got := f.diag.String(); got != "warn: something\n" {
		t.Errorf("stderr = %q", got)
	}
	if got := f.t.State().Status; got != "" {
		t.Errorf("the slot survived a diagnostic: %q", got)
	}
}

// A turn that produced reasoning and no body — the D6 empty-response
// shape. Releasing the slot is what closes the run, so there is no
// end-the-run call for a caller to forget.
func TestSerialization_StatusClearClosesAnOpenReasoningRun(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	io.WriteString(f.t.Reasoning(), "thought hard, said nothing")
	f.t.Status().Clear()
	if !strings.HasSuffix(f.screen(), dimOff+"\n") {
		t.Errorf("reasoning-only turn left the terminal at %q, want a dim reset and a newline", f.screen())
	}
	if got := f.t.State().Column; got != ColumnStart {
		t.Errorf("column = %v, want ColumnStart after the run closed", got)
	}
}

// Close is the last line of defence on a forced exit, where no deferred
// cleanup runs: neither a half-drawn frame nor an unterminated dim run
// may be the last thing on the user's terminal. Idempotent.
func TestClose_RetiresDecoration(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	io.WriteString(f.t.Reasoning(), "scratch")
	if err := f.t.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.HasSuffix(f.screen(), dimOff+"\n") {
		t.Errorf("Close left the terminal at %q", f.screen())
	}
	before := f.screen()
	if err := f.t.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if f.screen() != before {
		t.Errorf("Close is not idempotent: %q then %q", before, f.screen())
	}
}

// --- the ephemeral slot ------------------------------------------------

// Bug 1, structurally. A run of frames is ONE line redrawn in place: no
// newline is ever emitted, and each frame is introduced by the erase that
// reclaims the previous one. There is no client flag involved — the slot
// knows whether it is on the line because it is the only thing that puts
// it there.
func TestStatus_RedrawsInPlace(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	for _, frame := range []string{"| waiting (5s)", "/ waiting (5s)", "- waiting (6s)"} {
		f.t.Status().Set(frame)
	}
	got := f.screen()
	if strings.Contains(got, "\n") {
		t.Errorf("the slot emitted a newline — one line per frame instead of a redraw: %q", got)
	}
	if n := strings.Count(got, eraseLine); n != 3 {
		t.Errorf("want 3 in-place redraws, got %d: %q", n, got)
	}
	if got := f.t.State().Status; got != "- waiting (6s)" {
		t.Errorf("State().Status = %q, want the last frame", got)
	}
}

// The slot must never draw OVER committed content. A body that did not
// end its line leaves the cursor mid-line, so the frame takes a fresh one
// — and ColumnUnknown (a child owned the fd) takes the same deterministic
// newline rather than a guess.
func TestStatus_NeverDrawsOverCommittedContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool // a newline is taken before the frame
	}{
		{"content left the cursor mid-line", "body text", true},
		{"content ended its own line", "body text\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTTY(t, "xterm", 80)
			io.WriteString(f.t.Out(), tc.body)
			f.t.Status().Set("| waiting (5s)")
			after := f.screen()[len(tc.body):]
			if got := strings.HasPrefix(after, "\n"); got != tc.want {
				t.Errorf("newline before the frame = %v, want %v (emitted %q)", got, tc.want, after)
			}
		})
	}
}

// Truncation is to width-StatusColumnInset. Writing the FINAL column puts
// xterm-family terminals into pending-wrap, where the next glyph scrolls
// the screen — and a status line that scrolls is bug 1 again.
func TestStatus_TruncatesShortOfTheFinalColumn(t *testing.T) {
	tests := []struct {
		name string
		cols int
		text string
		want string
	}{
		{"fits", 20, "waiting (5s)", "waiting (5s)"},
		{"exactly the writable width", 13, "waiting (5s)", "waiting (5s)"},
		{"one column too long", 12, "waiting (5s)", "waiting (5s"},
		{"narrow", 5, "waiting (5s)", "wait"},
		{"multi-byte glyphs are cut on a rune boundary", 5, "αβγδεζ", "αβγδ"},
		{"no room at all", 1, "waiting", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newTTY(t, "xterm", tc.cols)
			f.t.Status().Set(tc.text)
			if got := f.t.State().Status; got != tc.want {
				t.Errorf("slot = %q, want %q at %d columns", got, tc.want, tc.cols)
			}
		})
	}
}

// TERM=dumb: the slot cannot be erased, so it commits at most ONE line
// per occupancy and drops the frames in between. No escape sequence
// reaches a terminal that would print it literally.
func TestStatus_NoANSICommitsOneLinePerOccupancy(t *testing.T) {
	f := newTTY(t, "dumb", 80)
	f.t.Status().Set("waiting (5s)")
	f.t.Status().Set("waiting (6s)")
	f.t.Status().Set("waiting (7s)")
	f.t.Status().Clear()
	f.t.Status().Set("closing (8s)")

	got := f.screen()
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("no-ANSI rendering emitted control sequences: %q", got)
	}
	if want := "waiting (5s)\nclosing (8s)\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnsiCapable(t *testing.T) {
	for _, tc := range []struct {
		termEnv string
		want    bool
	}{
		{"xterm-256color", true},
		{"screen", true},
		{"dumb", false},
		{"", false},
	} {
		if got := ansiCapable(tc.termEnv); got != tc.want {
			t.Errorf("ansiCapable(%q) = %v, want %v", tc.termEnv, got, tc.want)
		}
	}
}

// --- the ownership state machine --------------------------------------

// The books the plain backend keeps are the SAME books — that is the
// load-bearing half of the verification posture, since none of this emits
// a byte. Asserted on the plain backend deliberately.
func TestStack_LIFODisciplineIsClean(t *testing.T) {
	f := newPipe(t)
	turnReg := f.t.Push(ActivityTurn, Handler{})
	askReg := f.t.Push(ActivityAsk, Handler{})

	if got, want := f.t.State().Stack, []Activity{ActivityTurn, ActivityAsk}; !sameStack(got, want) {
		t.Fatalf("stack = %v, want %v (bottom first)", got, want)
	}
	askReg.Pop()
	if got, want := f.t.State().Stack, []Activity{ActivityTurn}; !sameStack(got, want) {
		t.Errorf("stack after popping the top = %v, want %v", got, want)
	}
	turnReg.Pop()
	st := f.t.State()
	if len(st.Stack) != 0 {
		t.Errorf("stack = %v, want empty", st.Stack)
	}
	if st.Violations != 0 {
		t.Errorf("violations = %d on a clean LIFO sequence, want 0", st.Violations)
	}
	if f.diag.String() != "" {
		t.Errorf("a clean sequence reported %q on Diag", f.diag.String())
	}
}

// U8: the ownership sensor sits at RELEASE, not acquire — acquisition is
// unambiguous and release ordering is where the interleavings go wrong.
// An out-of-order release still releases; refusing would leave an entry
// nobody can remove, which is worse than the violation it reports.
func TestStack_OutOfOrderPopIsAViolation(t *testing.T) {
	f := newPipe(t)
	turnReg := f.t.Push(ActivityTurn, Handler{})
	askReg := f.t.Push(ActivityAsk, Handler{})

	turnReg.Pop() // the ask is still above it
	st := f.t.State()
	if st.Violations != 1 {
		t.Errorf("violations = %d, want 1", st.Violations)
	}
	if got, want := st.Stack, []Activity{ActivityAsk}; !sameStack(got, want) {
		t.Errorf("stack = %v, want %v — the entry must still be released", got, want)
	}
	if !strings.Contains(f.diag.String(), string(ActivityTurn)) {
		t.Errorf("the violation was not reported on Diag: %q", f.diag.String())
	}
	askReg.Pop()
	if got := f.t.State().Violations; got != 1 {
		t.Errorf("violations = %d after a legal pop, want 1", got)
	}
}

// A second Pop of the same registration means two owners believe they
// hold one entry. It is a violation, not a tolerated no-op, and it must
// not corrupt the stack underneath it.
func TestStack_DoublePopIsAViolation(t *testing.T) {
	f := newPipe(t)
	outer := f.t.Push(ActivityTurn, Handler{})
	inner := f.t.Push(ActivityAsk, Handler{})
	inner.Pop()
	inner.Pop()

	st := f.t.State()
	if st.Violations != 1 {
		t.Errorf("violations = %d, want 1", st.Violations)
	}
	if got, want := st.Stack, []Activity{ActivityTurn}; !sameStack(got, want) {
		t.Errorf("a double pop corrupted the stack: %v, want %v", got, want)
	}
	outer.Pop()
}

// The abort window, C12 form (i): opened by a non-nil Handler.Abort,
// closed by a single unconditional argument-free idempotent call whose
// OMISSION is detectable in the snapshot. Popping closes it too — a
// released registration cannot hold a window.
func TestAbortWindow_OpensWithTheHandlerAndRevokesIdempotently(t *testing.T) {
	f := newPipe(t)
	if f.t.State().AbortWindow {
		t.Fatal("an abort window was open before anything registered")
	}

	reg := f.t.Push(ActivityTurn, Handler{Abort: func() {}})
	if !f.t.State().AbortWindow {
		t.Fatal("a non-nil Abort handler did not open the window")
	}
	reg.RevokeAbort()
	if f.t.State().AbortWindow {
		t.Error("RevokeAbort did not close the window")
	}
	reg.RevokeAbort() // idempotent: the boundary is crossed once, the call also fires at turn end
	if f.t.State().AbortWindow {
		t.Error("a second RevokeAbort re-opened the window")
	}
	reg.Pop()

	// A turn that never reached the pre-canonical boundary: the window is
	// open right up to the release, which is the shape the snapshot makes
	// detectable.
	reg2 := f.t.Push(ActivityTurn, Handler{Abort: func() {}})
	if !f.t.State().AbortWindow {
		t.Fatal("window not open")
	}
	reg2.Pop()
	if f.t.State().AbortWindow {
		t.Error("a popped registration is still holding an abort window")
	}
	if got := f.t.State().Violations; got != 0 {
		t.Errorf("violations = %d, want 0", got)
	}
}

// A registration with no Abort handler declines by construction, so it
// never opens a window to leak.
func TestAbortWindow_NilHandlerNeverOpensOne(t *testing.T) {
	f := newPipe(t)
	reg := f.t.Push(ActivityTurn, Handler{})
	if f.t.State().AbortWindow {
		t.Error("a nil Abort handler opened an abort window")
	}
	reg.RevokeAbort()
	reg.Pop()
	if got := f.t.State().Violations; got != 0 {
		t.Errorf("violations = %d, want 0", got)
	}
}

// The mode-install arithmetic, which is §3's load-bearing simplification
// made into a number: ONE session mode, installed once, with ZERO
// transitions between prompt and turn. The only legal changes are the ones
// enumerated at StateSnapshot.ModeInstalls, and every one of them is
// exercised here in sequence.
//
// It replaces W1's TestMode_UntouchedThroughW1, which pinned the opposite
// fact for the waves where liner owned the mode.
func TestMode_InstallArithmeticOnAnOwnedTerminal(t *testing.T) {
	f := newScripted(t, "xterm", Size{Cols: 80, Rows: 24}, nil)

	st := f.t.State()
	if st.ModeInstalls != 1 || st.Mode != ModeSession {
		t.Fatalf("after Open: installs=%d mode=%v, want 1 and ModeSession", st.ModeInstalls, st.Mode)
	}
	if !st.PumpReading {
		t.Error("term does not hold the read path after Open")
	}

	// A prompt and a turn: reads, writes, registrations. NONE of it may
	// touch the mode — that is the whole of bug 3's category ceasing to
	// exist rather than being disciplined.
	f.t.Status().Set("waiting (5s)")
	io.WriteString(f.t.Out(), "body\n")
	reg := f.t.Push(ActivityTurn, Handler{Abort: func() {}})
	reg.RevokeAbort()
	reg.Pop()
	if _, err := f.t.ReadLine(context.Background(), ActivityEditor, Question{Prompt: "> "}); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadLine err = %v, want io.EOF (the script is empty)", err)
	}
	if got := f.t.State().ModeInstalls; got != 1 {
		t.Errorf("a prompt/turn cycle installed %d modes, want 1 — there are no prompt/turn transitions", got)
	}

	// A child window: restore entry, run, re-install. +2, and the pump is
	// not reading for the duration.
	insideReading, insideMode := true, ModeSession
	if err := f.t.Handoff(func() error {
		s := f.t.State()
		insideReading, insideMode = s.PumpReading, s.Mode
		return nil
	}); err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	if insideReading {
		t.Error("something was still reading stdin while a child owned the terminal")
	}
	if insideMode != ModeEntry {
		t.Errorf("mode inside the child window = %v, want ModeEntry (ISIG restored for the child)", insideMode)
	}
	st = f.t.State()
	if st.ModeInstalls != 3 || st.Mode != ModeSession {
		t.Fatalf("after Handoff: installs=%d mode=%v, want 3 and ModeSession", st.ModeInstalls, st.Mode)
	}
	if !st.PumpReading {
		t.Error("the read path was not taken back after the child")
	}

	// An in-band Ctrl-Z: the same restore/re-install pair, for the same
	// reason — the shell must inherit the terminal personant found.
	f.t.suspend()
	if got := f.t.State().ModeInstalls; got != 5 {
		t.Errorf("after a suspend: installs=%d, want 5", got)
	}

	if err := f.t.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st = f.t.State()
	if st.ModeInstalls != 6 || st.Mode != ModeEntry {
		t.Errorf("after Close: installs=%d mode=%v, want 6 and ModeEntry", st.ModeInstalls, st.Mode)
	}
	if st.PumpReading {
		t.Error("term still claims the read path after Close")
	}
	// The device's own log, which is the fact the count is a projection of.
	want := []ModeIntent{ModeSession, ModeEntry, ModeSession, ModeEntry, ModeSession, ModeEntry}
	if got := f.dev.installed(); !slices.Equal(got, want) {
		t.Errorf("installed %v, want %v", got, want)
	}
}

// The other side of the same rule: a session term does NOT own installs
// nothing at all, for the whole session, on either kind of stream. A pipe
// has no line discipline to change, and a terminal that cannot erase is
// one where the kernel's own line discipline is doing the editing — taking
// it away and putting no editor in its place is the bug, not the fix.
func TestMode_UntouchedWhereTermDoesNotOwnTheTerminal(t *testing.T) {
	fixtures := map[string]*fixture{
		"pipe":                  newPipe(t),
		"tty that cannot erase": newTTY(t, "dumb", 80),
	}
	for name, f := range fixtures {
		t.Run(name, func(t *testing.T) {
			io.WriteString(f.t.Out(), "body\n")
			if err := f.t.Handoff(func() error { return nil }); err != nil {
				t.Fatalf("Handoff: %v", err)
			}
			st := f.t.State()
			if st.ModeInstalls != 0 {
				t.Errorf("ModeInstalls = %d, want 0", st.ModeInstalls)
			}
			if st.Mode != ModeEntry {
				t.Errorf("Mode = %v, want ModeEntry", st.Mode)
			}
			if !st.PumpReading {
				t.Error("term does not hold the read path — the books must match the TTY backend's")
			}
		})
	}
}

// PumpReading is false EXACTLY inside a child window, on both backends.
// That is the parity claim in its sharpest form: the plain backend has no
// pump goroutine at all, and still keeps the same book, so the off-TTY
// suite can sense an unbalanced Handoff on the TTY backend's behalf.
func TestHandoff_SuspendsTheReadPathOnBothBackends(t *testing.T) {
	for name, f := range map[string]*fixture{"tty": newTTY(t, "xterm", 80), "pipe": newPipe(t)} {
		t.Run(name, func(t *testing.T) {
			inside := true
			if err := f.t.Handoff(func() error {
				s := f.t.State()
				inside = s.PumpReading
				if len(s.Stack) != 1 || s.Stack[0] != ActivityChild {
					t.Errorf("stack inside the window = %v, want [child]", s.Stack)
				}
				return nil
			}); err != nil {
				t.Fatalf("Handoff: %v", err)
			}
			if inside {
				t.Error("the read path was still held while a child ran")
			}
			st := f.t.State()
			if !st.PumpReading {
				t.Error("the read path was not taken back")
			}
			if len(st.Stack) != 0 || st.Violations != 0 {
				t.Errorf("stack=%v violations=%d after the window, want empty and 0", st.Stack, st.Violations)
			}
		})
	}
}

// An error from fn passes through unchanged, and the window still closes
// around it — a child that failed is still a child that ran.
func TestHandoff_ErrorPassesThroughAndTheWindowStillCloses(t *testing.T) {
	f := newTTY(t, "xterm", 80)
	sentinel := errors.New("child exploded")
	if err := f.t.Handoff(func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the child's own error", err)
	}
	st := f.t.State()
	if st.ModeInstalls != 3 || st.Mode != ModeSession {
		t.Errorf("installs=%d mode=%v, want 3 and ModeSession", st.ModeInstalls, st.Mode)
	}
	if !st.PumpReading || len(st.Stack) != 0 {
		t.Errorf("PumpReading=%v stack=%v after a failed child", st.PumpReading, st.Stack)
	}
}

// The two backends agree on the cursor column, which is the point of
// plain-backend parity: the belief emits no bytes, so the off-TTY suite
// is the only place it can be observed at all.
func TestColumn_TrackedIdenticallyOnBothBackends(t *testing.T) {
	tests := []struct {
		name string
		body string
		want CursorColumn
	}{
		{"fresh session", "", ColumnStart},
		{"unterminated content", "body", ColumnMid},
		{"content ending its line", "body\n", ColumnStart},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tty, pipe := newTTY(t, "xterm", 80), newPipe(t)
			for _, f := range []*fixture{tty, pipe} {
				if tc.body != "" {
					io.WriteString(f.t.Out(), tc.body)
				}
			}
			if got := tty.t.State().Column; got != tc.want {
				t.Errorf("tty column = %v, want %v", got, tc.want)
			}
			if got := pipe.t.State().Column; got != tc.want {
				t.Errorf("plain column = %v, want %v", got, tc.want)
			}
		})
	}
}

func sameStack(got, want []Activity) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
