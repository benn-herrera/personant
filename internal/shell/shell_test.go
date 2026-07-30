package shell

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestRunner pins the shell to /bin/sh so a developer's $SHELL cannot
// change what the tests measure.
func newTestRunner(t *testing.T, dir string) *Runner {
	t.Helper()
	r := NewRunner(dir)
	r.shell = "/bin/sh"
	return r
}

// realPath normalizes a path the way the OS does, so an assertion is not
// defeated by /var → /private/var on macOS.
func realPath(t *testing.T, p string) string {
	t.Helper()
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return rp
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		mode    Mode
		command string
		ok      bool
	}{
		{"fire", "$ls -l", ModeFire, "ls -l", true},
		{"fire spaced", "$  ls -l  ", ModeFire, "ls -l", true},
		{"capture", "#git status", ModeCapture, "git status", true},
		{"bare fire", "$", ModeFire, "", true},
		{"bare capture", "#", ModeCapture, "", true},
		{"not an escape", "hello", "", "", false},
		{"slash command", "/help", "", "", false},
		{"empty", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, cmd, ok := Parse(tt.line)
			if ok != tt.ok || mode != tt.mode || cmd != tt.command {
				t.Errorf("Parse(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.line, mode, cmd, ok, tt.mode, tt.command, tt.ok)
			}
		})
	}
}

// A `#` command captures its output; a `$` command captures nothing while
// both stream to the terminal writer.
func TestRunCaptureVsFireAndForget(t *testing.T) {
	r := newTestRunner(t, t.TempDir())
	ctx := context.Background()

	var term bytes.Buffer
	res, err := r.Run(ctx, ModeCapture, "echo hello", &term)
	if err != nil {
		t.Fatalf("Run capture: %v", err)
	}
	if res.Capture != "hello\n" {
		t.Errorf("capture: got %q want %q", res.Capture, "hello\n")
	}
	if term.String() != "hello\n" {
		t.Errorf("terminal stream: got %q", term.String())
	}
	if res.ExitCode != 0 {
		t.Errorf("exit: got %d want 0", res.ExitCode)
	}

	term.Reset()
	res, err = r.Run(ctx, ModeFire, "echo hello", &term)
	if err != nil {
		t.Fatalf("Run fire: %v", err)
	}
	if res.Capture != "" {
		t.Errorf("$ must capture nothing, got %q", res.Capture)
	}
	if term.String() != "hello\n" {
		t.Errorf("$ must still stream to the terminal, got %q", term.String())
	}
}

// A non-zero exit is reported in the Result, not returned as an error —
// a failing command is an ordinary outcome, exactly as in a shell.
func TestRunExitStatus(t *testing.T) {
	r := newTestRunner(t, t.TempDir())
	res, err := r.Run(context.Background(), ModeCapture, "exit 42", nil)
	if err != nil {
		t.Fatalf("a non-zero exit must not be an error: %v", err)
	}
	if res.ExitCode != 42 {
		t.Errorf("exit: got %d want 42", res.ExitCode)
	}
}

// stdout and stderr share one descriptor, so they interleave in true
// emission order rather than by copier-goroutine scheduling.
func TestRunStdoutStderrOrdering(t *testing.T) {
	r := newTestRunner(t, t.TempDir())
	var term bytes.Buffer
	res, err := r.Run(context.Background(), ModeCapture,
		"echo out1; echo err1 >&2; echo out2; echo err2 >&2", &term)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	const want = "out1\nerr1\nout2\nerr2\n"
	if res.Capture != want {
		t.Errorf("capture order: got %q want %q", res.Capture, want)
	}
	if term.String() != want {
		t.Errorf("terminal order: got %q want %q", term.String(), want)
	}
}

// The cwd epilogue makes the shell cwd persist across invocations, for
// every way a shell can change directory — not just a bare `cd`.
func TestRunCwdAdoption(t *testing.T) {
	start := t.TempDir()
	sub := filepath.Join(start, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	r := newTestRunner(t, start)
	ctx := context.Background()

	// A compound `cd /x && ls` — the case naive cd-parsing misses.
	res, err := r.Run(ctx, ModeCapture, "cd "+sub+" && ls", nil)
	if err != nil {
		t.Fatalf("Run cd&&ls: %v", err)
	}
	if !strings.Contains(res.Capture, "marker.txt") {
		t.Errorf("ls ran in the wrong directory: %q", res.Capture)
	}
	if got, want := realPath(t, r.Dir()), realPath(t, sub); got != want {
		t.Fatalf("cwd not adopted: got %q want %q", got, want)
	}

	// The NEXT invocation starts there — the persistence claim.
	res, err = r.Run(ctx, ModeCapture, "ls", nil)
	if err != nil {
		t.Fatalf("Run ls: %v", err)
	}
	if !strings.Contains(res.Capture, "marker.txt") {
		t.Errorf("cwd did not persist to the next command: %q", res.Capture)
	}

	// A FAILED cd leaves the tracked cwd alone and reports non-zero.
	res, err = r.Run(ctx, ModeCapture, "cd /no/such/directory", nil)
	if err != nil {
		t.Fatalf("Run failed-cd: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("a failed cd must report non-zero")
	}
	if got, want := realPath(t, r.Dir()), realPath(t, sub); got != want {
		t.Errorf("failed cd moved the tracked cwd: got %q want %q", got, want)
	}

	// A cwd that no longer exists is refused: adopting it would make the
	// NEXT command fail at chdir instead of just running somewhere sane.
	gone := filepath.Join(start, "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatalf("mkdir gone: %v", err)
	}
	if _, err := r.Run(ctx, ModeFire, "cd "+gone+" && rmdir "+gone, nil); err != nil {
		t.Fatalf("Run rmdir: %v", err)
	}
	if got, want := realPath(t, r.Dir()), realPath(t, sub); got != want {
		t.Errorf("vanished cwd was adopted: got %q want %q", got, want)
	}
}

// The in-memory capture is clipped at captureMax while still reporting
// the true byte count, so a runaway command cannot balloon the process
// before the §6.5 context cap is ever consulted.
func TestRunCaptureBufferBound(t *testing.T) {
	r := newTestRunner(t, t.TempDir())
	r.captureMax = 1024

	res, err := r.Run(context.Background(), ModeCapture,
		"i=0; while [ $i -lt 200 ]; do echo 0123456789012345678901234567890123456789; i=$((i+1)); done", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Capture) != r.captureMax {
		t.Errorf("capture not clipped to the bound: got %d want %d", len(res.Capture), r.captureMax)
	}
	if !res.Truncated {
		t.Errorf("Truncated must be set when the bound clipped output")
	}
	if res.OutputBytes <= r.captureMax {
		t.Errorf("OutputBytes must report the TRUE size: got %d", res.OutputBytes)
	}
}

// §4.4.3: the child's stdin is the null device, so a program that reads
// input gets EOF and exits instead of wedging the REPL. The test's own
// deadline is the assertion — a regression here hangs.
func TestRunStdinIsNullDevice(t *testing.T) {
	r := newTestRunner(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := make(chan Result, 1)
	go func() {
		res, err := r.Run(ctx, ModeCapture, "cat; echo \"eof=$?\"", nil)
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		if res.Capture != "eof=0\n" {
			t.Errorf("stdin did not read EOF immediately: %q", res.Capture)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a command reading stdin hung — stdin is not connected to the null device")
	}
}

// Interrupt kills the running command's process group and leaves the
// Runner ready for the next one. This is the Ctrl-C-during-a-command path
// with the terminal taken out of it.
func TestInterruptKillsRunningCommand(t *testing.T) {
	if !groupInterruptSupported {
		t.Skip("process-group interrupt unsupported on this platform")
	}
	r := newTestRunner(t, t.TempDir())

	done := make(chan Result, 1)
	go func() {
		res, err := r.Run(context.Background(), ModeCapture, "sleep 30", nil)
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		done <- res
	}()

	// Wait for the child to actually be running before signalling it.
	deadline := time.Now().Add(10 * time.Second)
	for !r.Interrupt() {
		if time.Now().After(deadline) {
			t.Fatal("child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case res := <-done:
		if res.ExitCode >= 0 {
			t.Errorf("a signalled shell must report a negative exit code, got %d", res.ExitCode)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("SIGINT did not reach the child's process group")
	}

	// No child running → the interrupt is declined, so the REPL's own
	// Ctrl-C handling still applies at the prompt.
	if r.Interrupt() {
		t.Errorf("Interrupt must decline when no command is running")
	}
}

// An unrunnable shell is a real error, distinct from a command that ran
// and failed.
func TestRunUnrunnableShellErrors(t *testing.T) {
	r := newTestRunner(t, t.TempDir())
	r.shell = filepath.Join(t.TempDir(), "no-such-shell")
	if _, err := r.Run(context.Background(), ModeCapture, "echo hi", nil); err == nil {
		t.Fatal("expected an error when the shell binary does not exist")
	}
	if _, err := r.Run(context.Background(), ModeCapture, "   ", nil); err == nil {
		t.Fatal("expected an error for an empty command")
	}
}

func TestNewRunnerShellResolution(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := NewRunner(".").Shell(); got != FallbackShell {
		t.Errorf("unset $SHELL: got %q want %q", got, FallbackShell)
	}
	t.Setenv("SHELL", "/bin/zsh")
	if got := NewRunner(".").Shell(); got != "/bin/zsh" {
		t.Errorf("$SHELL: got %q want /bin/zsh", got)
	}
}
