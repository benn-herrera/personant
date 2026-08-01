package chat

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"personant/internal/term"
	"personant/internal/term/screentest"
	"personant/internal/turn"
)

// W1's dogfood checklist (IMPLEMENTATION-PLAN.md §2, W1), automated.
//
// The checklist was the acceptance gate for W1 and nothing re-ran it. Each
// test below is one numbered item, driven through the SAME seams
// production uses — an injected S2 platform for the TTY determination, the
// injected ticker for the animation heartbeat, the injected elapsed clock
// for the reveal threshold — with every emitted byte replayed through
// internal/term/screentest to reconstruct what the user would have seen.
//
// Items 9 (`$`/`#` child output and the pager) and Ctrl-Z stay human-only:
// both are about a terminal personant has handed away, and a model of our
// own alphabet has by construction nothing to say about bytes a child
// wrote. Item 10's piped half is internal/chat's golden_test.go.
//
// The screen model FAILS on any sequence term does not currently emit, so
// every test here is also a standing check that W1's alphabet has not
// quietly grown.

// screen replays everything the session emitted and returns the resulting
// screen. Both fds feed one buffer (see newProgressFixture), which is what
// a real terminal is.
func (f *progressFixture) screen(t *testing.T) *screentest.Screen {
	t.Helper()
	s := screentest.New(f.size.Cols, f.size.Rows)
	if err := s.Feed(f.out.Bytes()); err != nil {
		t.Fatalf("%v\n\nemitted stream: %q", err, f.out.String())
	}
	return s
}

// Item 1 — the indicator redraws IN PLACE. One line, animating, through a
// phase change: not one line per frame, which is bug 1's rendering.
func TestW1Item1_IndicatorRedrawsInPlace(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseComposing)

	first := f.screen(t)
	if first.TotalRows() != 1 || !strings.Contains(first.Line(), string(turn.PhaseComposing)) {
		t.Fatalf("the announcement did not draw one line: rows=%d line=%q\n%s",
			first.TotalRows(), first.Line(), first)
	}
	opening := first.Line()

	for range 3 { // six frames: the glyph cycle is 4, so the frame must move
		f.tick(t)
	}
	animated := f.screen(t)
	if animated.Line() == opening {
		t.Errorf("six heartbeats did not animate the line: still %q", opening)
	}
	if got := animated.TotalRows(); got != 1 {
		t.Errorf("frames grew the screen to %d rows — one line per frame is bug 1\n%s", got, animated)
	}

	f.p.phase(turn.PhaseWaiting)
	f.p.phase(turn.PhaseClosing)
	final := f.screen(t)
	if got := final.TotalRows(); got != 1 {
		t.Errorf("phase changes grew the screen to %d rows\n%s", got, final)
	}
	if final.Wraps() != 0 || final.Scrolls() != 0 {
		t.Errorf("the indicator wrapped (%d) or scrolled (%d)\n%s", final.Wraps(), final.Scrolls(), final)
	}
	if !strings.Contains(final.Line(), string(turn.PhaseClosing)) {
		t.Errorf("the one line does not carry the current label: %q", final.Line())
	}
	f.p.stop()
}

// Items 2 and 3 — the first body token retires the indicator cleanly, and
// no frame appears at all while the body streams.
//
// Item 3 is renderLocked's reveal rule and it is asserted the sharp way:
// a heartbeat delivered mid-body must emit ZERO BYTES. Byte equality, not
// a screen comparison, because a frame drawn and erased within one tick
// would leave the screen identical and still be the regression.
func TestW1Items2And3_BodyRetiresTheSlotAndNoFramePaintsOverIt(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if f.screen(t).TotalRows() != 1 {
		t.Fatal("the indicator did not draw")
	}

	if _, err := io.WriteString(f.tm.Out(), "Hi there."); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := f.screen(t)
	if got := s.Line(); got != "Hi there." {
		t.Errorf("a spinner fragment survived on the body's line: %q\n%s", got, s)
	}
	if got := s.TotalRows(); got != 1 {
		t.Errorf("retiring the slot inserted a blank line: %d rows\n%s", got, s)
	}
	if got := f.slot(); got != "" {
		t.Errorf("the slot was not retired: %q", got)
	}

	// Item 3: heartbeats between body writes must not paint.
	for _, chunk := range []string{" The answer", " is", " forty-two."} {
		quiet := f.out.Len()
		f.tick(t)
		if got := f.out.Len(); got != quiet {
			t.Fatalf("a heartbeat emitted %d bytes while the body was streaming: %q",
				got-quiet, f.out.String()[quiet:])
		}
		if _, err := io.WriteString(f.tm.Out(), chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s = f.screen(t)
	if got, want := s.Line(), "Hi there. The answer is forty-two."; got != want {
		t.Errorf("the body is not intact on one line:\n got %q\nwant %q\n%s", got, want, s)
	}
	if got := s.TotalRows(); got != 1 {
		t.Errorf("the streamed body occupies %d rows, want 1\n%s", got, s)
	}

	// An ANNOUNCEMENT still may take the slot back — that is how turn close
	// restarts the indicator after the stream.
	f.p.phase(turn.PhaseClosing)
	if !strings.Contains(f.screen(t).Line(), string(turn.PhaseClosing)) {
		t.Errorf("the closing announcement did not reclaim the line\n%s", f.screen(t))
	}
	f.p.stop()
}

// Item 4 — /thinking on: reasoning dimmed, then the answer UNDIMMED on its
// own line, and the dimmed reasoning still present after it scrolls
// (arbitration item 1: reasoning is persistent in scrollback, not a
// bounded region that gets taken back).
func TestW1Item4_ReasoningIsDimThenTheAnswerIsNot(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	th := newThinking(f.tm, true)

	th.reasoning("weighing the options")
	live := f.screen(t)
	if !live.LineDim() || live.Line() != "weighing the options" {
		t.Fatalf("reasoning is not dim on the live line: dim=%v line=%q\n%s",
			live.LineDim(), live.Line(), live)
	}

	if _, err := io.WriteString(f.tm.Out(), "Here is the answer.\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := f.screen(t)
	if s.Row(0) != "weighing the options" || !s.RowDim(0) {
		t.Errorf("the reasoning run did not stay put and dim: %q dim=%v\n%s", s.Row(0), s.RowDim(0), s)
	}
	if s.Row(1) != "Here is the answer." {
		t.Errorf("the answer did not start on its own line: %q\n%s", s.Row(1), s)
	}
	if got := s.DimLines(); len(got) != 1 || got[0] != "weighing the options" {
		t.Errorf("dim leaked past the reasoning run: %q\n%s", got, s)
	}
}

// Item 4, persistence half — the same run, pushed off a short screen. What
// the user scrolls back to must still be there, and must still be dim.
func TestW1Item4_DimReasoningSurvivesIntoScrollback(t *testing.T) {
	f := newProgressFixture(t, true, true, term.Size{Cols: 40, Rows: 2})
	th := newThinking(f.tm, true)

	th.reasoning("scratch to scroll back to")
	for _, line := range []string{"answer line one\n", "answer line two\n", "answer line three\n"} {
		if _, err := io.WriteString(f.tm.Out(), line); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	s := f.screen(t)
	if s.Scrolls() == 0 {
		t.Fatalf("nothing scrolled; the test is not exercising scrollback\n%s", s)
	}
	if !s.Contains("scratch to scroll back to") {
		t.Errorf("the reasoning was lost when it scrolled off\n%s", s)
	}
	dim := s.DimLines()
	if len(dim) == 0 || !strings.Contains(strings.Join(dim, "\n"), "scratch to scroll back") {
		t.Errorf("the reasoning lost its dim attribute in scrollback: %q\n%s", dim, s)
	}
	for _, l := range dim {
		if strings.Contains(l, "answer line") {
			t.Errorf("committed content went to scrollback dim: %q\n%s", l, s)
		}
	}
}

// Item 5 — the reasoning-only turn. An empty response after reasoning ends
// at progress.stop, whose unconditional Status.Clear is the ONLY thing
// that closes the run. If it did not, the next prompt would come back dim.
func TestW1Item5_ReasoningOnlyTurnLeavesTheTerminalUndimmed(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	th := newThinking(f.tm, true)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)

	th.reasoning("a thought that produced no answer")
	f.p.stop() // the turn ends here; no body was ever written

	if _, err := io.WriteString(f.tm.Out(), "> "); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := f.screen(t)
	if s.LineDim() {
		t.Errorf("the next prompt came back dim: %q\n%s", s.Line(), s)
	}
	if s.Line() != "> " && s.Line() != ">" {
		t.Errorf("the prompt did not land on a clean line: %q\n%s", s.Line(), s)
	}
	if got := s.DimLines(); len(got) != 1 || !strings.Contains(got[0], "a thought that produced no answer") {
		t.Errorf("the dim run did not close where it should have: %q\n%s", got, s)
	}
}

// Item 6 — an error mid-stream lands on its OWN line, not inside the
// indicator's. Diag is a different fd but the same serialization point,
// which is the whole of what W1 closed here.
func TestW1Item6_DiagLandsOnItsOwnLineNotInsideTheIndicator(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if !strings.Contains(f.screen(t).Line(), string(turn.PhaseWaiting)) {
		t.Fatal("the indicator did not draw")
	}

	const msg = "turn error: provider closed the connection"
	fmt.Fprintln(f.tm.Diag(), msg)

	s := f.screen(t)
	if got := s.Row(0); got != msg {
		t.Errorf("the error did not replace the indicator's line: %q\n%s", got, s)
	}
	if strings.Contains(s.Row(0), string(turn.PhaseWaiting)) || strings.ContainsAny(s.Row(0), "|/-\\") {
		t.Errorf("an indicator fragment survived on the error's line: %q\n%s", s.Row(0), s)
	}
	if got := s.TotalRows(); got != 2 {
		t.Errorf("the error line did not commit exactly one row: %d\n%s", got, s)
	}
	if got := f.slot(); got != "" {
		t.Errorf("the slot survived a Diag write: %q", got)
	}
	f.p.stop()
}

// Item 7 — a 30-column terminal. The status truncates to Cols minus
// term.StatusColumnInset and does NOT wrap or scroll: writing the final
// column puts xterm-family terminals into pending wrap, where the next
// glyph scrolls the screen, and a status line that scrolls is bug 1 again.
//
// The label is the real longest phase with the abort hint on, so the
// untruncated line is ~41 columns and the inset is genuinely load-bearing.
func TestW1Item7_NarrowTerminalTruncatesAndNeverWraps(t *testing.T) {
	const cols = 30
	f := newProgressFixture(t, true, true, term.Size{Cols: cols, Rows: 24})
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	f.p.setAbortHint(true)

	if untruncated := len(fmt.Sprintf("%s %s (%ds)%s",
		progressFrames[0], turn.PhaseWaiting, 5, abortHintSuffix)); untruncated <= cols {
		t.Fatalf("the fixture's label is only %d columns wide — it cannot exercise truncation", untruncated)
	}

	for range 3 {
		f.tick(t)
	}
	s := f.screen(t)
	if want := cols - term.StatusColumnInset; len([]rune(s.Line())) > want {
		t.Errorf("status is %d columns, want at most %d: %q\n%s", len([]rune(s.Line())), want, s.Line(), s)
	}
	if s.Wraps() != 0 {
		t.Errorf("the status wrapped %d time(s)\n%s", s.Wraps(), s)
	}
	if s.Scrolls() != 0 {
		t.Errorf("the status scrolled the screen %d time(s)\n%s", s.Scrolls(), s)
	}
	if got := s.TotalRows(); got != 1 {
		t.Errorf("a truncated status still took %d rows\n%s", got, s)
	}
	f.p.stop()
}

// Item 8 — the Esc hint appears and disappears WITH the abortable window,
// not a frame late and not lingering.
//
// The lingering half is the one a state assertion cannot make: the hint is
// 14 columns, so a redraw that did not erase first would leave its tail
// behind on a line that is otherwise correct. Only the screen sees that.
func TestW1Item8_AbortHintAppearsAndLeavesNoResidue(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)
	if strings.Contains(f.screen(t).Line(), abortHintSuffix) {
		t.Fatalf("the hint was advertised before the window opened: %q", f.screen(t).Line())
	}

	f.p.setAbortHint(true)
	opened := f.screen(t)
	if !strings.HasSuffix(opened.Line(), abortHintSuffix) {
		t.Errorf("the hint did not render with the window: %q\n%s", opened.Line(), opened)
	}
	if opened.TotalRows() != 1 {
		t.Errorf("the hint took a new row\n%s", opened)
	}

	f.p.setAbortHint(false)
	closed := f.screen(t)
	if strings.Contains(closed.Line(), "esc") {
		t.Errorf("the hint left residue on the line: %q\n%s", closed.Line(), closed)
	}
	if !strings.Contains(closed.Line(), string(turn.PhaseWaiting)) {
		t.Errorf("closing the window destroyed the label: %q\n%s", closed.Line(), closed)
	}

	// The STATE half of item 8 is not chat's to report in W1, and pinning
	// that here is the point rather than an omission. term's abort window
	// (StateSnapshot.AbortWindow, opened by Handler.Abort and closed by
	// Registration.RevokeAbort) is asserted in internal/term; nothing in
	// this package registers one, because chat still runs its own Esc
	// window through control.arm/disarm and its own escwatch until W3
	// takes the mode and the decoder. So the indicator's hint and term's
	// window are two facts today, and this asserts they are still
	// unconnected. When W3 wires the hint to a real registration, this
	// fails and gets replaced by the correlation it should have been.
	if f.tm.State().AbortWindow {
		t.Error("term now reports an abort window from chat — wire item 8's state half " +
			"to the hint and replace this assertion with the correlation")
	}
	f.p.stop()
}

// Item 10, TERM=dumb half — zero escape bytes. The two deliberate
// degradations recorded in the plan (the ticker keeps running, the spinner
// glyph appears in the static line) are visible here and are not defects.
//
// The piped half of item 10 is golden_test.go.
func TestW1Item10_DumbTerminalEmitsNoEscapeBytes(t *testing.T) {
	f := newProgressFixture(t, true, false, fixtureSize)
	th := newThinking(f.tm, true)
	f.setElapsed(5 * time.Second)

	f.p.phase(turn.PhaseWaiting)
	f.tick(t)
	th.reasoning("scratch on a terminal that cannot dim it")
	if _, err := io.WriteString(f.tm.Out(), "the answer\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	fmt.Fprintln(f.tm.Diag(), "warn: something went sideways")
	f.p.phase(turn.PhaseClosing)
	f.p.stop()

	stream := f.out.String()
	if i := strings.IndexByte(stream, 0x1b); i >= 0 {
		t.Fatalf("TERM=dumb emitted an escape byte at %d: %q", i, stream)
	}
	if i := strings.IndexByte(stream, '\r'); i >= 0 {
		t.Errorf("TERM=dumb emitted a carriage return at %d: %q", i, stream)
	}

	// The model over a stream with no escapes sees an EMPTY ANSI alphabet:
	// nothing dim, nothing erased, one committed line per occupancy.
	s := f.screen(t)
	if got := s.DimLines(); len(got) != 0 {
		t.Errorf("a terminal that cannot dim produced dim cells: %q\n%s", got, s)
	}
	if !s.Contains(reasoningNoANSIOpenMark) {
		t.Errorf("reasoning lost its words fallback, so it reads as the answer\n%s", s)
	}
	for _, want := range []string{string(turn.PhaseWaiting), string(turn.PhaseClosing), "the answer", "warn: something went sideways"} {
		if !s.Contains(want) {
			t.Errorf("%q missing from the static rendering\n%s", want, s)
		}
	}
}

// reasoningNoANSIOpenMark is term's no-ANSI reasoning bracket, restated
// here because it is unexported there. Its absence is the one failure the
// reasoning channel must not have: scratch that reads as the answer.
const reasoningNoANSIOpenMark = "[thinking]"
