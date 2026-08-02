package chat

import (
	"context"
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
	return replay(t, f.size, f.out)
}

// replay is the model over a recorded stream, in one place: it fails
// closed on any sequence term does not emit today, so every caller is also
// a standing check that the alphabet has not quietly grown.
func replay(t *testing.T, size term.Size, out *syncBuffer) *screentest.Screen {
	t.Helper()
	s := screentest.New(size.Cols, size.Rows)
	if err := s.Feed(out.Bytes()); err != nil {
		t.Fatalf("%v\n\nemitted stream: %q", err, out.String())
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
		f.beat()
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

// Item 1, the regression a real terminal found: the SINGLE-PHASE wait.
//
// A wait announces its phase at t≈0, inside the reveal delay, so the
// announcement paints nothing; every tick after it is a heartbeat, and a
// heartbeat may not claim an empty slot. Those two rules deadlocked, and
// the indicator never appeared at all unless a SECOND phase change
// happened to land past the threshold — which is why every test above
// passes while the user watches a blank line with /thinking off.
//
// The deferred announcement is therefore owed, not dropped: the first
// heartbeat past the threshold delivers it, and the ones after it are
// redraw-only again.
func TestW1Item1_SinglePhaseWaitRevealsAfterTheDelay(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(progressShowAfter - time.Millisecond)
	f.p.phase(turn.PhaseWaiting)
	if got := f.slot(); got != "" {
		t.Fatalf("drew inside the reveal delay: %q", got)
	}

	// Past the threshold, with NO second phase change: only the heartbeat.
	f.setElapsed(progressShowAfter + time.Second)
	f.beat()
	if got := f.slot(); !strings.Contains(got, string(turn.PhaseWaiting)) {
		t.Fatalf("the wait stayed invisible past the threshold: slot %q", got)
	}
	s := f.screen(t)
	if got := s.TotalRows(); got != 1 {
		t.Errorf("the revealed indicator took %d rows, want 1\n%s", got, s)
	}
	if !strings.Contains(s.Line(), string(turn.PhaseWaiting)) {
		t.Errorf("the revealed line does not carry the label: %q\n%s", s.Line(), s)
	}

	// …and the claim is spent. Content retires the frame, and the
	// heartbeats after it must go back to emitting nothing at all.
	if _, err := io.WriteString(f.tm.Out(), "Hi there."); err != nil {
		t.Fatalf("write: %v", err)
	}
	quiet := f.out.Len()
	f.beat()
	if got := f.out.Len(); got != quiet {
		t.Errorf("a heartbeat emitted %d bytes after content took the line: %q",
			got-quiet, f.out.String()[quiet:])
	}
	f.p.stop()
}

// The same delay, with the abort window opening inside it: the hint is an
// announcement too, so it is owed exactly as the phase is and must be on
// the line the moment the line appears — not one window later.
func TestW1Item8_AbortHintOpenedInsideTheDelayStillSurfaces(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(time.Second)
	f.p.phase(turn.PhaseWaiting)
	f.p.setAbortHint(true)
	if got := f.slot(); got != "" {
		t.Fatalf("drew inside the reveal delay: %q", got)
	}

	f.setElapsed(5 * time.Second)
	f.beat()
	if got := f.slot(); !strings.HasSuffix(got, abortHintSuffix) {
		t.Errorf("the hint did not surface with the indicator: %q", got)
	}
	if got := f.screen(t).Line(); !strings.Contains(got, string(turn.PhaseWaiting)) {
		t.Errorf("the revealed line lost its label: %q", got)
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
		f.beat()
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

// Item 6, failed-turn half — a turn that ERRORS after a partial body. The
// body is already on the user's line and the loop's `turn error:` goes to
// Diag, which term deliberately does not newline-align (in a pipe stdout
// and stderr are separate streams). Without runOneTurn's re-align the two
// jam together as `partial answer so fturn error: …`, which reads as
// model output.
//
// The re-align is called here the way runOneTurn calls it — after the
// indicator is retired and before the branch — so this test fails if the
// call moves back inside the success branch.
func TestW1Item6_TurnErrorAfterPartialBodyLandsOnItsOwnRow(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)

	const body = "partial answer so f"
	if _, err := io.WriteString(f.tm.Out(), body); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The turn returns an error here: retire the indicator, re-align, and
	// let the REPL loop report it.
	f.p.stop()
	realign(f.tm)
	const msg = "turn error: turn: stream: connection reset"
	fmt.Fprintf(f.tm.Diag(), "%s\n", msg)

	s := f.screen(t)
	if got := s.Row(0); got != body {
		t.Errorf("the body's row is not exactly the body: %q\n%s", got, s)
	}
	if got := s.Row(1); got != msg {
		t.Errorf("the error did not land on its own row: %q\n%s", got, s)
	}
	if got := s.TotalRows(); got != 3 {
		t.Errorf("body + error + the fresh line took %d rows, want 3\n%s", got, s)
	}
}

// The abort half of the same shape: an Esc retraction after partial
// tokens. reportRetraction re-aligns through the SAME helper, so the
// notice cannot be jammed onto the body either — and the notice reading as
// model output is the exact failure it exists to prevent.
func TestW1_AbortNoticeAfterPartialBodyLandsOnItsOwnRow(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)

	const body = "partial answer so f"
	if _, err := io.WriteString(f.tm.Out(), body); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.p.stop()
	realign(f.tm)
	// reportRetraction's own re-align must be a no-op here, not a blank row.
	realign(f.tm)
	fmt.Fprintln(f.tm.Out(), abortNotice)

	s := f.screen(t)
	if got := s.Row(0); got != body {
		t.Errorf("the body's row is not exactly the body: %q\n%s", got, s)
	}
	if got := s.Row(1); got != abortNotice {
		t.Errorf("the abort notice did not land on its own row: %q\n%s", got, s)
	}
	if got := s.TotalRows(); got != 3 {
		t.Errorf("a second re-align inserted a blank row: %d rows, want 3\n%s", got, s)
	}
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
		f.beat()
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

	f.p.stop()
}

// Item 8's STATE half, which W1.5 could only pin as ABSENT.
//
// Until W3 the Esc window was three independent facts — control.arm, a
// private escwatch goroutine, and progress.setAbortHint — and term's
// StateSnapshot.AbortWindow was inert because nothing in chat ever
// registered a Handler{Abort}. The pinned test asserted that inertia and
// said it should fail the day the wiring landed. It has, and this is the
// correlation it was holding a place for:
//
//	the hint is visible ⇔ the abort window is open ⇔ StateSnapshot.AbortWindow
//
// All three are now projections of ONE registration, which is why they
// cannot drift — and the snapshot half is the one an off-TTY suite can
// see, since none of it emits a byte.
func TestW3Item8_AbortHintAndTermsAbortWindowAreOneFact(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	ctl := newControl(f.tm, f.p, func() {})
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseWaiting)

	hinted := func() bool { return strings.HasSuffix(f.slot(), abortHintSuffix) }
	windowed := func() bool { return f.tm.State().AbortWindow }

	if hinted() || windowed() {
		t.Fatalf("hint=%v window=%v before the turn armed", hinted(), windowed())
	}

	ctl.arm(func() {})
	if !hinted() || !windowed() {
		t.Errorf("after arm: hint=%v window=%v, want both open\n%s", hinted(), windowed(), f.screen(t))
	}

	// The pre-canonical boundary closes BOTH, through one call.
	ctl.onPhase(turn.PhaseClosing)
	if hinted() || windowed() {
		t.Errorf("after the boundary: hint=%v window=%v, want both closed\n%s",
			hinted(), windowed(), f.screen(t))
	}
	if !strings.Contains(f.slot(), string(turn.PhaseClosing)) {
		t.Errorf("closing the window destroyed the label: %q", f.slot())
	}
	// And no residue on the line the hint used to occupy — the half only
	// the screen can see.
	if s := f.screen(t); strings.Contains(s.Line(), "esc") {
		t.Errorf("the hint left residue on the line: %q\n%s", s.Line(), s)
	}

	ctl.disarm()
	if st := f.tm.State(); len(st.Stack) != 0 || st.Violations != 0 {
		t.Errorf("stack=%v violations=%d after the turn", st.Stack, st.Violations)
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
	f.beat()
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

// --- W2/W3: the menus (bug 5) ----------------------------------------
//
// R2-14, and now the whole of it: the preamble is COMMITTED to scrollback
// above the editor and only the single-line prompt is left for the editor
// to repaint. A question that lives in scrollback is a question no line
// editor is allowed to draw over.
//
// W2 could assert only the split, because liner wrote straight to the tty
// and the screen model — which reconstructs from what TERM emitted — could
// not see the line being edited at all. W3 owns those bytes, so the
// keystroke half is here too: the answer the user typed is on screen,
// under a question that survived it.

// newQuestionFixture opens an interactive terminal whose keystrokes are
// the given script, delivered through the real device fake, decoder, pump
// and editor.
func newQuestionFixture(t *testing.T, script string) (*term.Terminal, *syncBuffer) {
	t.Helper()
	out := &syncBuffer{}
	tm, err := term.Open(term.Options{
		Stdout:  out,
		Stderr:  out,
		TermEnv: "xterm-256color",
		Platform: term.NewUnixPlatform(&fakeDevice{
			out:    out,
			size:   fixtureSize,
			script: [][]byte{[]byte(script)},
		}),
	})
	if err != nil {
		t.Fatalf("term.Open: %v", err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	return tm, out
}

// A menu question with a multi-line preamble: every preamble line is
// COMMITTED on its own row, and only the prompt is the editor's — which is
// what makes the question survive the first keystroke.
//
// The preamble is a real §4.5.7 menu rather than a synthetic one, so a
// change to the menu's shape that broke the split would fail here. The
// answer is a SINGLE KEYSTROKE with no Enter: the Question.Keys fast path,
// which W2 documented as not live and W3 lands.
func TestW3_MenuPreambleSurvivesTheKeystrokeThatAnswersIt(t *testing.T) {
	tm, out := newQuestionFixture(t, "c")

	preamble := []string{
		"No active project resolved.",
		"  [c] create a new project rooted at this directory",
		"  [s] switch to a known project",
		"  [n] no project (use prj_default)",
	}
	ans, err := tm.ReadLine(context.Background(), term.ActivityAsk, term.Question{
		Preamble: preamble,
		Prompt:   "choice: ",
		Keys:     "csn",
	})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if ans.Text != "c" || ans.Key != 'c' {
		t.Errorf("Answer{Text:%q, Key:%q}, want {\"c\", 'c'} — the fast path is live in W3",
			ans.Text, ans.Key)
	}

	s := replay(t, fixtureSize, out)
	for i, want := range preamble {
		if got := s.Row(i); got != want {
			t.Errorf("preamble row %d = %q, want %q — bug 5's exact shape\n%s", i, got, want, s)
		}
	}
	if got, want := s.Row(len(preamble)), "choice: c"; got != want {
		t.Errorf("the answered question row = %q, want %q\n%s", got, want, s)
	}
	if s.Wraps() != 0 {
		t.Errorf("the question wrapped %d time(s)\n%s", s.Wraps(), s)
	}
	// One row per preamble line, the prompt's row, and the fresh row the
	// committed answer left the cursor on.
	if got, want := s.TotalRows(), len(preamble)+2; got != want {
		t.Errorf("the question took %d rows, want %d\n%s", got, want, s)
	}
}

// A question retires the ephemeral slot BEFORE it renders, and the slot
// stays suspended for the duration. The indicator and a menu contend for
// the same physical line — turn close prompts while the phase indicator is
// still up — and a frame drawn over a line being typed is a frame the
// editor's next repaint cannot account for.
func TestW3_QuestionTakesTheLineFromTheStatusSlotAndKeepsIt(t *testing.T) {
	tm, out := newQuestionFixture(t, "s")
	tm.Status().Set("closing (4s)")
	if tm.State().Status == "" {
		t.Fatal("the slot did not take the line")
	}

	if _, err := tm.ReadLine(context.Background(), term.ActivityAsk, term.Question{
		Preamble: []string{"thread thr_1 has gone idle — closure suggested."},
		Prompt:   "close as? [r]esolved / [s]kip: ",
		Keys:     "rs",
	}); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}

	if got := tm.State().Status; got != "" {
		t.Errorf("the slot survived into the question: %q", got)
	}
	s := replay(t, fixtureSize, out)
	if s.Contains("closing (4s)") {
		t.Errorf("an indicator frame is still on screen under the question\n%s", s)
	}
	if got := s.Row(0); got != "thread thr_1 has gone idle — closure suggested." {
		t.Errorf("the question did not start on a clean row: %q\n%s", got, s)
	}
	if got := s.Row(1); got != "close as? [r]esolved / [s]kip: s" {
		t.Errorf("the answered question row = %q\n%s", got, s)
	}
}
