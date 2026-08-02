package chat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/term"
	"personant/internal/turn"
)

// newTestControl builds a control over in-memory streams. The streams are
// buffers rather than character devices, so term selects the PLAIN backend
// and Interactive() is false — the same path the tests and piped stdin
// take in production, and the one that keeps the same books as the TTY
// backend (the parity half of the verification posture).
func newTestControl(t *testing.T) (*control, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	tm := openTestTerm(t, strings.NewReader(""), &stdout, &stderr)
	return newControl(tm, newProgress(tm), func() {}), &stdout, &stderr
}

// openTestTerm opens the arbiter over injected streams and closes it with
// the test. It is the one place chat's tests construct a terminal, so a
// change to term's Options reaches every fixture at once.
func openTestTerm(t *testing.T, in io.Reader, out, errw io.Writer) *term.Terminal {
	t.Helper()
	tm, err := term.Open(term.Options{Stdin: in, Stdout: out, Stderr: errw})
	if err != nil {
		t.Fatalf("term.Open: %v", err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	return tm
}

// TestAbortablePhase pins the allow-list. The turn's PRE-CANONICAL window
// — the span where an error return releases the #94 recovery scope — ends
// at PhaseClosing, and every phase from there on must be non-abortable.
// An unknown/future phase must default to non-abortable.
func TestAbortablePhase(t *testing.T) {
	tests := []struct {
		phase turn.Phase
		want  bool
	}{
		{turn.PhaseComposing, true},
		{turn.PhaseWaiting, true},
		{turn.PhaseReissuing, true},
		{turn.PhaseRecall, false},
		{turn.PhaseClosing, false},
		{turn.Phase("some phase added later"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			if got := abortablePhase(tt.phase); got != tt.want {
				t.Errorf("abortablePhase(%q) = %v, want %v", tt.phase, got, tt.want)
			}
		})
	}
}

// TestControl_ArmIsBookkeepingOnly is the zero-change guarantee for piped
// stdin and every test in this package: arming a turn touches no terminal
// mode, starts no goroutine, and writes no bytes. Since W3 it is a
// REGISTRATION and nothing else — the cbreak/watcher pair it used to be is
// what term absorbed.
func TestControl_ArmIsBookkeepingOnly(t *testing.T) {
	ctl, stdout, stderr := newTestControl(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctl.arm(cancel)
	st := ctl.t.State()
	if len(st.Stack) != 1 || st.Stack[0] != term.ActivityTurn {
		t.Errorf("stack = %v, want [turn]", st.Stack)
	}
	if st.ModeInstalls != 0 {
		t.Errorf("arming installed %d terminal mode(s)", st.ModeInstalls)
	}
	ctl.disarm()

	if ctx.Err() != nil {
		t.Error("arming cancelled the turn context")
	}
	if ctl.tookAbort() {
		t.Error("tookAbort reported an abort that could not have happened")
	}
	if st := ctl.t.State(); len(st.Stack) != 0 || st.Violations != 0 {
		t.Errorf("stack = %v violations = %d after disarm, want empty and 0", st.Stack, st.Violations)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("non-terminal session wrote bytes: stdout=%q stderr=%q", stdout, stderr)
	}
}

// The abort window is term's, opened by the registration and closed by
// C12 form (i)'s single unconditional call. This is the correlation the
// W1.5 pinned test was holding a place for: hint, window and snapshot are
// now ONE fact, and the snapshot is what an off-TTY suite can see.
func TestControl_AbortWindowTracksTheTurnRegistration(t *testing.T) {
	ctl, _, _ := newTestControl(t)
	if ctl.t.State().AbortWindow {
		t.Fatal("an abort window was open before a turn armed")
	}

	ctl.arm(func() {})
	if !ctl.t.State().AbortWindow {
		t.Fatal("arming a turn did not open term's abort window")
	}

	// The pre-canonical boundary. Idempotent, and the entry STAYS on the
	// stack — turn close prompts over it.
	ctl.onPhase(turn.PhaseClosing)
	ctl.onPhase(turn.PhaseClosing)
	st := ctl.t.State()
	if st.AbortWindow {
		t.Error("the window survived the pre-canonical boundary — RevokeAbort's omission is exactly what this senses")
	}
	if len(st.Stack) != 1 || st.Stack[0] != term.ActivityTurn {
		t.Errorf("stack = %v after the boundary, want the turn still registered", st.Stack)
	}

	ctl.disarm()
	ctl.disarm() // idempotent: a second release must not be a violation
	if st := ctl.t.State(); len(st.Stack) != 0 || st.Violations != 0 {
		t.Errorf("stack = %v violations = %d, want empty and 0", st.Stack, st.Violations)
	}
}

// A turn that never reaches the pre-canonical boundary still gives the
// window back at turn end — the backstop runOneTurn defers.
func TestControl_DisarmClosesAWindowThatNeverReachedTheBoundary(t *testing.T) {
	ctl, _, _ := newTestControl(t)
	ctl.arm(func() {})
	ctl.disarm()
	if ctl.t.State().AbortWindow {
		t.Error("the abort window outlived the turn")
	}
}

// Mid-turn Ctrl-C is SESSION EXIT (arbitration item 4), and it arrives as
// a decoded KEY because ModeSession clears ISIG. The handler must not do
// the shutdown itself — it runs on term's pump goroutine, and shutting
// down joins that pump — so it queues the interrupt and returns.
func TestControl_MidTurnCtrlCQueuesAnInterrupt(t *testing.T) {
	ctl, _, _ := newTestControl(t)
	cancelled := false
	ctl.cancel = func() { cancelled = true }
	ctl.arm(func() {})

	if got := ctl.onKey(term.Key{Name: term.KeyCtrl, Rune: 'c'}); got != term.Claimed {
		t.Errorf("Ctrl-C disposition = %v, want Claimed", got)
	}
	if cancelled {
		t.Error("the key handler shut the session down in line; it must queue and return")
	}
	select {
	case <-ctl.sigCh:
	default:
		t.Fatal("Ctrl-C queued no interrupt")
	}

	// Everything else falls through: keys typed while the model works are
	// discarded, as they always were.
	for _, k := range []term.Key{
		{Name: term.KeyRune, Rune: 'x'},
		{Name: term.KeyUp},
		{Name: term.KeyCtrl, Rune: 'd'},
	} {
		if got := ctl.onKey(k); got != term.Declined {
			t.Errorf("onKey(%v) = %v, want Declined", k, got)
		}
	}
	ctl.disarm()
}

// TestControl_EscCancelsTurnOnce checks the armed core: Esc cancels the
// TURN context (never the session's) and records the abort so the REPL can
// tell it apart from a Ctrl-C cancel.
func TestControl_EscCancelsTurnOnce(t *testing.T) {
	ctl, _, _ := newTestControl(t)
	sessionCancelled := false
	ctl.cancel = func() { sessionCancelled = true }

	turnCtx, turnCancel := context.WithCancel(context.Background())
	defer turnCancel()
	ctl.abortTurn = turnCancel // arm the core directly; no terminal to drive

	ctl.onEsc()
	if turnCtx.Err() == nil {
		t.Error("Esc did not cancel the turn context")
	}
	if !ctl.tookAbort() {
		t.Error("Esc did not record the abort")
	}
	if sessionCancelled {
		t.Error("Esc cancelled the SESSION; it must only abandon the turn")
	}
}

// TestControl_DisarmBeatsEsc is the ordering guarantee that keeps an
// aborted turn out of the #94 recovery path. Once the pre-canonical
// boundary has closed the window, a racing Esc must NOT cancel — a cancel
// landing after the first canonical write would leave the turn scope open
// and make the next launch print a recovery banner.
func TestControl_DisarmBeatsEsc(t *testing.T) {
	ctl, _, _ := newTestControl(t)
	turnCtx, turnCancel := context.WithCancel(context.Background())
	defer turnCancel()
	ctl.abortTurn = turnCancel

	ctl.onPhase(turn.PhaseClosing) // the pre-canonical boundary
	ctl.onEsc()

	if turnCtx.Err() != nil {
		t.Error("Esc cancelled the turn after the pre-canonical boundary")
	}
	if ctl.tookAbort() {
		t.Error("an abort was recorded after the window closed")
	}
}

// TestPromptLine_ReOffersRetractedInput pins the §4.3.3 recovery path for
// a retracted prompt: it comes back as an EDITABLE DEFAULT, and an
// ordinary prompt is untouched.
//
// W2 rewrote it. The previous version counted calls on the lineReader
// seam, and that seam is gone — term.ReadLine is the only read path now,
// so "which method did it call" has no answer and would be a checksum of
// the call rather than a test of the recovery. What is asserted instead is
// what the user experiences off a TTY: the retracted text is offered as a
// value a bare Enter ACCEPTS and a typed line REPLACES, which is the same
// contract the editor's pre-filled line carries on a terminal.
func TestPromptLine_ReOffersRetractedInput(t *testing.T) {
	tests := []struct {
		name      string
		retracted string
		typed     string
		want      string
		wantEcho  string
	}{
		{"ordinary prompt", "", "typed anew\n", "typed anew", "> "},
		{"retraction accepted unedited", "the mistaken input", "\n", "the mistaken input",
			"> [the mistaken input]\n> "},
		{"retraction replaced", "the mistaken input", "something else\n", "something else",
			"> [the mistaken input]\n> "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			tm := openTestTerm(t, strings.NewReader(tt.typed), &out, &errw)

			line, err := promptLine(context.Background(), tm, tt.retracted)
			if err != nil {
				t.Fatalf("promptLine: %v", err)
			}
			if line != tt.want {
				t.Errorf("line = %q, want %q", line, tt.want)
			}
			if got := out.String(); got != tt.wantEcho {
				t.Errorf("prompt echo = %q, want %q", got, tt.wantEcho)
			}
		})
	}
}

// TestReportRetraction covers both halves of closing out an abort: the
// on-screen marker that keeps the transcript honest about partial
// response text, and the single §2.8 forensic line — which must carry the
// turn id, phase and byte count and must NOT carry the text.
func TestReportRetraction(t *testing.T) {
	paths := scaffoldHome(t)
	ops := newOps(paths)
	ctx := context.Background()
	if err := ops.Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("init: %v", err)
	}

	const secret = "a prompt the user retracted and does not want remembered"

	// The marker goes out on term's content channel — there is no
	// separate screen writer to hand in any more.
	ctl, screen, _ := newTestControl(t)
	ctl.lastPhase = turn.PhaseWaiting
	if err := ctl.reportRetraction(ctx, ops, "t7-123456789", secret); err != nil {
		t.Fatalf("reportRetraction: %v", err)
	}

	if !strings.Contains(screen.String(), abortNotice) {
		t.Errorf("abort marker missing from the transcript: %q", screen.String())
	}
	if strings.Contains(screen.String(), secret) {
		t.Errorf("the marker echoed the retracted text back: %q", screen.String())
	}

	logs := readSessionLog(t, paths)
	line := ""
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, "system.turn-aborted") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no system.turn-aborted event was recorded; log was:\n%s", logs)
	}
	for _, want := range []string{
		"turn=t7-123456789",
		`phase="` + string(turn.PhaseWaiting) + `"`,
		fmt.Sprintf("bytes=%d", len(secret)),
	} {
		if !strings.Contains(line, want) {
			t.Errorf("abort event %q missing %q", line, want)
		}
	}
	if strings.Contains(logs, secret) {
		t.Error("the retracted text reached the event log — it must have no durable home")
	}
}

// readSessionLog concatenates every event-log file in the home.
func readSessionLog(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var all strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		all.Write(body)
	}
	return all.String()
}

// TestControl_ExitWording covers the Ctrl-C messaging rule: an ordinary
// quit is silent (it is /exit with a different key), and only a genuinely
// interrupted turn earns the shutdown warning.
func TestControl_ExitWording(t *testing.T) {
	tests := []struct {
		name     string
		announce bool
		wantMsg  bool
	}{
		{"ordinary quit at the prompt", false, false},
		{"interrupted mid-turn", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl, stdout, stderr := newTestControl(t)
			cancelled := false
			ctl.cancel = func() { cancelled = true }

			ctl.exit(tt.announce)

			if !cancelled {
				t.Error("exit did not cancel the session context")
			}
			if got := stderr.Len() > 0; got != tt.wantMsg {
				t.Errorf("stderr written = %v, want %v (got %q)", got, tt.wantMsg, stderr)
			}
			if tt.wantMsg && !strings.Contains(stderr.String(), "force quit") {
				t.Errorf("interrupt notice does not mention the force-quit valve: %q", stderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("exit wrote to stdout: %q", stdout)
			}
			if got := ctl.interrupts.Load(); got != 1 {
				t.Errorf("interrupt count = %d, want 1 (a second Ctrl-C must force)", got)
			}
		})
	}
}

// TestControl_InTurnDrivesSignalWording ties the honest wording to the
// actual state: a signal at the prompt announces nothing, a signal during
// a turn does.
func TestControl_InTurnDrivesSignalWording(t *testing.T) {
	ctl, _, stderr := newTestControl(t)
	ctl.cancel = func() {}

	ctl.onSignal() // at the prompt: inTurn is false
	if stderr.Len() != 0 {
		t.Errorf("SIGINT at the prompt printed a shutdown warning: %q", stderr)
	}

	ctl2, _, stderr2 := newTestControl(t)
	ctl2.cancel = func() {}
	ctl2.arm(func() {}) // marks the turn in flight (inert on a non-terminal)
	ctl2.onSignal()
	if stderr2.Len() == 0 {
		t.Error("SIGINT during a turn printed nothing; the abandoned turn must be reported")
	}
}
