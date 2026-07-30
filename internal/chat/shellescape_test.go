package chat

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/shell"
	"personant/internal/store"
	"personant/internal/turn"
)

// fakeAPIKey is key-SHAPED test material, not a credential. No test in
// this package reads a real key file.
const fakeAPIKey = "sk-chat-0123456789abcdefghijklmn"

// shellHarness wires the pieces runShellEscape needs, over in-memory
// streams. Stdin is deliberately not os.Stdin, so interactiveTTY is false
// and the terminal handoff is a no-op — the non-TTY path production takes
// under piped stdin.
type shellHarness struct {
	opts   Options
	ops    memops.MemoryOps
	ctl    *control
	sh     *shell.Runner
	red    *shell.Redactor
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	paths  store.PersonantPaths
}

func newShellHarness(t *testing.T, keys ...string) *shellHarness {
	t.Helper()
	// Pin the shell so a developer's $SHELL cannot change what is measured.
	t.Setenv("SHELL", "/bin/sh")
	paths := scaffoldHome(t)
	var stdout, stderr bytes.Buffer
	opts := Options{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	lr := &bufLineReader{in: bufio.NewReader(opts.Stdin), out: &stdout}
	pr := newProgress(&stdout, interactiveTTY(opts))
	sh := shell.NewRunner(t.TempDir())
	ctl := newControl(opts, lr, pr, func() {})
	// Mirror Run's wiring: the control consults the runner so a Ctrl-C
	// during a command goes to the command.
	ctl.sh = sh
	return &shellHarness{
		opts:   opts,
		ops:    newOps(paths),
		ctl:    ctl,
		sh:     sh,
		red:    shell.NewRedactor(keys),
		stdout: &stdout,
		stderr: &stderr,
		paths:  paths,
	}
}

func (h *shellHarness) run(t *testing.T, line string) *turn.Delta {
	t.Helper()
	d, err := runShellEscape(context.Background(), h.opts, h.ops, h.ctl, h.sh, h.red, line)
	if err != nil {
		t.Fatalf("runShellEscape(%q): %v", line, err)
	}
	return d
}

// `$` runs and shows output but yields no context delta; `#` yields one.
func TestRunShellEscape_CaptureDiscriminant(t *testing.T) {
	h := newShellHarness(t)

	if d := h.run(t, "$echo fire"); d != nil {
		t.Errorf("$ must not produce a context delta, got %+v", d)
	}
	if !strings.Contains(h.stdout.String(), "fire") {
		t.Errorf("$ output missing from the terminal: %q", h.stdout.String())
	}

	h.stdout.Reset()
	d := h.run(t, "#echo capture")
	if d == nil {
		t.Fatal("# must produce a context delta")
	}
	if d.Source != memops.SourceUserShellCapture {
		t.Errorf("source: got %q want %q", d.Source, memops.SourceUserShellCapture)
	}
	if d.Retention != memops.RetentionTask {
		t.Errorf("retention: got %q want %q", d.Retention, memops.RetentionTask)
	}
	if d.Content != "capture\n" {
		t.Errorf("content: got %q", d.Content)
	}
	if !strings.Contains(h.stdout.String(), "capture") {
		t.Errorf("# output must ALSO reach the terminal: %q", h.stdout.String())
	}
}

// Both prefixes emit a §2.8 user.shell line carrying the invocation, the
// discriminant, the exit status and (for #) the captured byte count —
// never the captured content.
func TestRunShellEscape_EventLine(t *testing.T) {
	h := newShellHarness(t)
	h.run(t, "$echo fire")
	h.run(t, "#echo 12345")

	logs := readAllLogs(t, h.paths)
	if !strings.Contains(logs, `user.shell kind=$ exit=0`) {
		t.Errorf("missing $ event line:\n%s", logs)
	}
	if !strings.Contains(logs, `user.shell kind=# exit=0`) {
		t.Errorf("missing # event line:\n%s", logs)
	}
	if !strings.Contains(logs, "bytes=6") {
		t.Errorf("captured byte count not logged:\n%s", logs)
	}
	if strings.Contains(logs, "12345\n") {
		t.Errorf("captured CONTENT must never reach the single-line log:\n%s", logs)
	}
}

// §6.4/§8.2.1: key material is stripped on the path into context while
// the user's terminal keeps the unredacted output, and the event says
// that redaction fired without saying what matched.
func TestRunShellEscape_RedactsContextPathOnly(t *testing.T) {
	h := newShellHarness(t, fakeAPIKey)
	d := h.run(t, "#echo KEY="+fakeAPIKey)

	if d == nil {
		t.Fatal("expected a capture delta")
	}
	if strings.Contains(d.Content, fakeAPIKey) {
		t.Fatalf("key material reached the context path: %q", d.Content)
	}
	if !strings.Contains(d.Content, shell.RedactionPlaceholder) {
		t.Errorf("placeholder missing from redacted capture: %q", d.Content)
	}
	if !strings.Contains(h.stdout.String(), fakeAPIKey) {
		t.Errorf("the user's terminal must still show the unredacted output: %q", h.stdout.String())
	}

	logs := readAllLogs(t, h.paths)
	if !strings.Contains(logs, "permissions.redaction-fire") {
		t.Errorf("redaction event not logged:\n%s", logs)
	}
	if strings.Contains(logs, fakeAPIKey) {
		t.Fatalf("key material appeared in a log line — §8.2.1 forbids it:\n%s", logs)
	}
}

// A key typed into the COMMAND is redacted before the invocation is
// logged: §8.2.1's never-in-a-log-line rule does not care which end
// produced the bytes.
func TestRunShellEscape_RedactsLoggedCommand(t *testing.T) {
	h := newShellHarness(t, fakeAPIKey)
	h.run(t, "$true "+fakeAPIKey)
	if logs := readAllLogs(t, h.paths); strings.Contains(logs, fakeAPIKey) {
		t.Fatalf("key material appeared in the logged command:\n%s", logs)
	}
}

// §4.4.3: an interactive program finds stdin at the null device, exits,
// and the failure is announced rather than looking like a silent no-op.
func TestRunShellEscape_InteractiveAppFailureIsLegible(t *testing.T) {
	h := newShellHarness(t)
	// `read` with stdin at /dev/null hits EOF and returns non-zero — the
	// same shape as vim/less bailing out.
	h.run(t, "$read line")
	if !strings.Contains(h.stdout.String(), "[exit ") {
		t.Errorf("a non-zero exit must be announced, got %q", h.stdout.String())
	}
}

// A bare prefix is a usage hint, not an empty shell invocation.
func TestRunShellEscape_BarePrefix(t *testing.T) {
	h := newShellHarness(t)
	if d := h.run(t, "$"); d != nil {
		t.Errorf("bare $ must not produce a delta")
	}
	if !strings.Contains(h.stderr.String(), "usage:") {
		t.Errorf("bare prefix must print a usage hint, got %q", h.stderr.String())
	}
}

// The shell cwd persists across invocations and is tracked SEPARATELY
// from the active project root (§4.5.1).
func TestRunShellEscape_CwdPersists(t *testing.T) {
	h := newShellHarness(t)
	dir := t.TempDir()
	h.run(t, "$cd "+dir)
	d := h.run(t, "#pwd")
	if d == nil || strings.TrimSpace(d.Content) == "" {
		t.Fatal("expected a pwd capture")
	}
	// Compare on the basename: macOS reports /private/var for /var.
	if !strings.HasSuffix(strings.TrimSpace(d.Content), lastPathElement(dir)) {
		t.Errorf("cwd did not persist: pwd reported %q, cd was to %q", strings.TrimSpace(d.Content), dir)
	}
}

func lastPathElement(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func TestExitNotice(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{1, "[exit 1]"},
		{127, "[exit 127]"},
		{-1, "[interrupted]"},
	}
	for _, tt := range tests {
		if got := exitNotice(tt.code); got != tt.want {
			t.Errorf("exitNotice(%d) = %q want %q", tt.code, got, tt.want)
		}
	}
}

// appendCapture preserves order and holds the buffered total under
// pendingCaptureMax by dropping the OLDEST captures.
func TestAppendCaptureOrderAndBound(t *testing.T) {
	mk := func(s string) turn.Delta {
		return turn.Delta{Source: memops.SourceUserShellCapture, Content: s}
	}

	pending := appendCapture(nil, mk("first"))
	pending = appendCapture(pending, mk("second"))
	pending = appendCapture(pending, mk("third"))
	if len(pending) != 3 || pending[0].Content != "first" || pending[2].Content != "third" {
		t.Fatalf("order not preserved: %+v", pending)
	}

	// Three near-cap captures: the first two must be evicted.
	big := strings.Repeat("x", pendingCaptureMax*2/3)
	pending = appendCapture(nil, mk(big))
	pending = appendCapture(pending, mk(big))
	pending = appendCapture(pending, mk(big))
	total := 0
	for _, p := range pending {
		total += len(p.Content)
	}
	if total > pendingCaptureMax {
		t.Errorf("pending buffer exceeded its bound: %d > %d", total, pendingCaptureMax)
	}
	if len(pending) != 1 {
		t.Errorf("oldest captures should have been dropped, %d remain", len(pending))
	}

	// A single capture larger than the whole bound is still delivered —
	// dropping the ONLY capture would silently answer a question about
	// output the user can see on screen. The §6.5 cap truncates it later.
	huge := strings.Repeat("y", pendingCaptureMax*2)
	if got := appendCapture(nil, mk(huge)); len(got) != 1 {
		t.Errorf("the last capture must never be dropped, got %d", len(got))
	}
}

// End-to-end through the REPL: two `#` commands before a prompt are both
// delivered as pre-prompt deltas, in order, ahead of the user.prompt
// delta of the same turn.
func TestREPL_CapturesDeliveredInOrderToNextTurn(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	runChat(t, paths, topicTagMock(), "prj_1", "#echo one\n#echo two\nhello\n/quit\n")

	logs := readAllLogs(t, paths)
	firstCap := strings.Index(logs, "context.modified source=user.shell-capture")
	if firstCap < 0 {
		t.Fatalf("no shell-capture delta reached the chain:\n%s", logs)
	}
	secondCap := strings.Index(logs[firstCap+1:], "context.modified source=user.shell-capture")
	if secondCap < 0 {
		t.Fatalf("only one of two captures was delivered:\n%s", logs)
	}
	prompt := strings.Index(logs, "context.modified source=user.prompt")
	if prompt < 0 || prompt < firstCap+1+secondCap {
		t.Errorf("captures must fire BEFORE the user.prompt delta of the same turn:\n%s", logs)
	}
	// Two user.shell event lines, one per command.
	if n := strings.Count(logs, "user.shell kind=#"); n != 2 {
		t.Errorf("user.shell lines: got %d want 2:\n%s", n, logs)
	}
}

// Ctrl-C while a `$`/`#` child runs interrupts THE COMMAND, not the
// session — and leaves the session's interrupt state exactly as it found
// it, so the next Ctrl-C at the prompt is still the first one.
//
// This is the testable half of the terminal/signal story. What is NOT
// covered here, and is inspection-only, is the termios handoff itself:
// verifying that a child inherits ICANON/ECHO on requires a real pty, and
// this suite deliberately takes no pty dependency.
func TestOnSignal_ShellChildTakesTheInterrupt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	h := newShellHarness(t)
	cancelled := false
	h.ctl.cancel = func() { cancelled = true }

	dir := t.TempDir()
	marker := dir + "/running"
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := h.sh.Run(context.Background(), shell.ModeFire,
			"touch "+marker+"; sleep 30", nil); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()

	// Poll for the child rather than for a pgid: probing via Interrupt
	// would consume the very signal under test.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.ctl.onSignal()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the interrupt did not reach the shell child")
	}
	if cancelled {
		t.Error("Ctrl-C during a shell command must NOT cancel the session")
	}
	if n := h.ctl.interrupts.Load(); n != 0 {
		t.Errorf("session interrupt count must be untouched, got %d", n)
	}

	// With no command running the same signal is the session's again, and
	// it counts as the FIRST interrupt — not a second one that force-quits.
	h.ctl.onSignal()
	if !cancelled {
		t.Error("Ctrl-C at the prompt must still end the session")
	}
	if n := h.ctl.interrupts.Load(); n != 1 {
		t.Errorf("interrupt count: got %d want 1 (a forwarded interrupt must not have pre-charged it)", n)
	}
}

// On a session that drives no terminal there is nothing to hand off, and
// the handoff must be a silent no-op rather than a warning or an error.
func TestHandoffTerminal_NonTTYIsInert(t *testing.T) {
	h := newShellHarness(t)
	if h.ctl.origTerm != nil {
		t.Fatal("a non-terminal session must capture no terminal state")
	}
	h.ctl.handoffTerminal()()
	if h.stderr.Len() != 0 {
		t.Errorf("handoff wrote to stderr on a non-terminal session: %q", h.stderr.String())
	}
}

// A session that ends with captures still buffered drops them and says
// so. They are deliberately not persisted: a capture is a convenience for
// the next prompt, not durable state.
func TestREPL_UnconsumedCapturesDroppedAndLogged(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	runChat(t, paths, topicTagMock(), "prj_1", "#echo orphan\n/quit\n")

	logs := readAllLogs(t, paths)
	if !strings.Contains(logs, "user.shell-capture-dropped count=1") {
		t.Errorf("unconsumed capture not reported:\n%s", logs)
	}
	if strings.Contains(logs, "context.modified source=user.shell-capture") {
		t.Errorf("an unconsumed capture must never reach the chain:\n%s", logs)
	}
}
