package chat

import (
	"bufio"
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
	"personant/internal/turn"
)

// newTestControl builds a control over in-memory streams. Options.Stdin is
// deliberately NOT os.Stdin, so interactiveTTY is false and the session
// takes the non-terminal path — the same path the tests and piped stdin
// take in production.
func newTestControl(t *testing.T) (*control, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts := Options{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	lr := &bufLineReader{in: bufio.NewReader(opts.Stdin), out: &stdout}
	pr := newProgress(&stdout, interactiveTTY(opts))
	return newControl(opts, lr, pr, func() {}), &stdout, &stderr
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

// TestControl_NonTTYIsInert is the zero-change guarantee for piped stdin
// and every test in this package: no terminal mode is touched, no watcher
// goroutine starts, nothing is written, and Esc has no effect.
func TestControl_NonTTYIsInert(t *testing.T) {
	ctl, stdout, stderr := newTestControl(t)
	if ctl.escAvailable {
		t.Fatal("esc abort armed on a non-terminal session")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctl.arm(cancel)
	if ctl.watcher != nil {
		t.Error("arm started a watcher goroutine on a non-terminal session")
	}
	ctl.onEsc() // whatever fires it, there is nothing armed to abort
	ctl.disarm()

	if ctx.Err() != nil {
		t.Error("the turn context was cancelled on a non-terminal session")
	}
	if ctl.tookAbort() {
		t.Error("tookAbort reported an abort that could not have happened")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("non-terminal session wrote bytes: stdout=%q stderr=%q", stdout, stderr)
	}
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

// recordingReader records which lineReader method the REPL used and with
// what default, so the re-offer routing can be asserted without a
// terminal.
type recordingReader struct {
	bufLineReader
	calls    []string
	defaults []string
}

func (r *recordingReader) prompt(p string) (string, error) {
	r.calls = append(r.calls, "prompt")
	r.defaults = append(r.defaults, "")
	return "", io.EOF
}

func (r *recordingReader) promptWithDefault(p, def string) (string, error) {
	r.calls = append(r.calls, "promptWithDefault")
	r.defaults = append(r.defaults, def)
	return def, nil
}

// TestPromptLine_ReOffersRetractedInput pins the §4.3.3 recovery path for
// a retracted prompt: it comes back as an EDITABLE DEFAULT, through the
// lineReader seam that already exists for exactly this, and an ordinary
// prompt is untouched.
func TestPromptLine_ReOffersRetractedInput(t *testing.T) {
	tests := []struct {
		name      string
		retracted string
		wantCall  string
	}{
		{"ordinary prompt", "", "prompt"},
		{"after a retraction", "the mistaken input", "promptWithDefault"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recordingReader{}
			line, _ := promptLine(r, tt.retracted)
			if len(r.calls) != 1 || r.calls[0] != tt.wantCall {
				t.Fatalf("calls = %v, want [%s]", r.calls, tt.wantCall)
			}
			if r.defaults[0] != tt.retracted {
				t.Errorf("offered default = %q, want %q", r.defaults[0], tt.retracted)
			}
			if tt.retracted != "" && line != tt.retracted {
				t.Errorf("re-offered line = %q, want %q", line, tt.retracted)
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

	ctl, _, _ := newTestControl(t)
	ctl.lastPhase = turn.PhaseWaiting
	const secret = "a prompt the user retracted and does not want remembered"

	var screen bytes.Buffer
	if err := ctl.reportRetraction(ctx, ops, &screen, "t7-123456789", secret); err != nil {
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
