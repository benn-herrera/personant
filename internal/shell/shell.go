// Package shell implements the SPEC §4.4 user shell escape: the `$`
// (fire-and-forget) and `#` (capture) prefixes the REPL accepts in place
// of a prompt.
//
// # Per-command execution (deliberate SPEC §4.4.2 deviation)
//
// SPEC §4.4.2 specifies one long-lived `$SHELL -i` subprocess owning the
// whole session, with `$`/`#` lines written to its stdin. This package
// does NOT do that. Each `$`/`#` line runs a fresh `$SHELL -c <command>`
// (falling back to /bin/sh).
//
// The persistent shell exists mainly to preserve cwd/env/aliases across
// invocations, but §4.4.3 already excludes the interactive applications a
// persistent PTY would principally serve, and a long-lived shell on pipes
// needs a sentinel-marker protocol with a timeout and hang recovery for
// commands that consume the sentinel from stdin. Per-command execution
// gets exit codes for free, cannot hang the session, and — with the cwd
// epilogue below — satisfies §4.5.1's "shell cwd" concept, which is the
// load-bearing part. The REPL-facing seam (Runner.Run) is identical
// either way, so a persistent shell can replace this implementation later
// without touching the surface.
//
// Accepted loss, stated plainly: aliases, shell functions, and exported
// environment defined MID-SESSION do not persist from one `$`/`#` line to
// the next. Only the cwd does. What rc files contribute was measured, not
// assumed (macOS zsh 5.9 / bash-3.2-as-sh):
//
//   - `zsh -c` sources /etc/zshenv and ~/.zshenv ONLY. ~/.zshrc is NOT
//     sourced — it is interactive-only — so aliases a user defines in
//     .zshrc are unavailable here. Aliases in .zshenv ARE available.
//   - `sh -c` (bash in POSIX mode) sources NOTHING: not ~/.profile, and
//     not $ENV or $BASH_ENV (both are interactive-only for `sh`).
//
// # Security posture (read before "hardening" this)
//
// Passing the user's line to `$SHELL -c` as a shell STRING is correct
// here, not a vulnerability. Per §4.4.4 these are user-initiated: the
// user authored the line and runs it with their own full privileges, and
// shell metacharacters (`|`, `&&`, `>`, globs) are the entire point of a
// shell escape. There is no privilege boundary to cross.
//
// This is the exact OPPOSITE of the future model-facing `fs.*` tools,
// where the arguments come from the LLM and MUST use argv exec with no
// shell interpolation. Do not "fix" this file by adding quoting,
// allow-lists, or argv splitting — that would convert a working shell
// escape into a useless one without protecting anything.
//
// # Interactive applications
//
// §4.4.3 excludes vim/less/top. The child's stdin is the null device, so
// an interactive program hits EOF and exits instead of wedging the REPL
// on input that will never arrive. Terminal-mode handoff for nested
// full-screen apps is out of scope.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Mode discriminates the two §4.4.1 prefixes.
type Mode string

const (
	// ModeFire is `$`: run and stream to the terminal, capture nothing.
	ModeFire Mode = "$"
	// ModeCapture is `#`: run, stream to the terminal, AND capture the
	// output for inclusion in the next turn's context.
	ModeCapture Mode = "#"
)

const (
	// FallbackShell is used when $SHELL is unset.
	FallbackShell = "/bin/sh"

	// DefaultCaptureMax bounds the IN-MEMORY capture buffer for one `#`
	// command. This is a memory bound, not the context bound: the §6.5
	// byte cap that governs what actually reaches the model is applied far
	// later, in internal/turn. Without this bound a `# cat 4gb.bin` would
	// balloon the process before the §6.5 cap ever ran.
	DefaultCaptureMax = 256 << 10

	// trailerTimeout bounds the wait for the cwd epilogue after the child
	// has exited. It is only reachable when a BACKGROUNDED grandchild
	// inherited the trailer fd and is holding it open; in that case the
	// reported cwd is simply unavailable and the tracked cwd stands.
	trailerTimeout = 2 * time.Second

	// trailerFD is the child fd the cwd epilogue writes to. exec places
	// Cmd.ExtraFiles[0] at fd 3.
	trailerFD = 3
)

// cwdEpilogue is appended to every command so the runtime learns the
// shell's final working directory (§4.5.1 "shell cwd").
//
// It is an EPILOGUE, deliberately not `cd`-parsing. Intercepting `cd`
// looks simpler and is wrong: it misses `cd /x && ls`, `pushd`, a cd
// inside a function or subshell, and any rc-driven change. Asking the
// shell where it ended up cannot miss any of them.
//
// The report goes to a DEDICATED fd rather than being delimited inside
// stdout. A trailer mixed into stdout has to be stripped back out of both
// the live terminal stream and the capture buffer, which means holding
// back the tail of a stream while scanning for a sentinel that command
// output could in principle contain. A separate fd cannot collide with
// command output at all, so there is nothing to strip and nothing to
// disambiguate — the whole class of problem is removed rather than
// mitigated. The trailer is therefore ONE field, the cwd, taken verbatim
// to EOF: no delimiter is needed, so a path containing a newline, a NUL,
// or a space is carried exactly.
//
// The exit status is NOT in the trailer — `exit $__personant_rc` hands
// the user command's status back through the process, so
// os/exec.ProcessState is the single source of truth for it.
const cwdEpilogue = "\n__personant_rc=$?\nprintf '%s' \"$PWD\" >&" +
	trailerFDString + "\nexit \"$__personant_rc\"\n"

// trailerFDString must equal trailerFD; it exists because a const string
// cannot interpolate an int const.
const trailerFDString = "3"

// Result is the outcome of one `$`/`#` invocation.
type Result struct {
	// ExitCode is the command's exit status, or a NEGATIVE value when the
	// shell was terminated by a signal (the Ctrl-C path).
	ExitCode int

	// Capture is the command's combined stdout+stderr, empty for ModeFire.
	// It is the RAW capture — redaction (§6.4) is the caller's step, so
	// that the terminal keeps showing what the user actually ran.
	Capture string

	// OutputBytes is how many bytes the command produced in total, before
	// the DefaultCaptureMax memory bound clipped Capture. Equal to
	// len(Capture) unless Truncated.
	OutputBytes int

	// Truncated says the in-memory capture bound clipped the output.
	Truncated bool

	// Dir is the tracked shell cwd after the command.
	Dir string
}

// Runner executes `$`/`#` commands and tracks the shell cwd across them.
//
// The tracked cwd is deliberately DISTINCT from the active project root
// (§4.5.1): they diverge freely, and `$ cd /etc` must not change which
// project the agent is anchored to or grant it anything.
//
// One Runner per session. Run is called only from the REPL goroutine;
// Interrupt is called from the signal-handler goroutine.
//
// # Interrupt-ownership invariant
//
//	From the moment Run commits to a `$`/`#` command until that child is
//	reaped, a SIGINT must NEVER reach the session-cancel path.
//
// This is an INVARIANT, not a best effort, because the failure is
// user-hostile and silent: chat.onSignal asks Interrupt first and falls
// through to "end the session" when it declines, so any instant in which
// a command is running and Interrupt says otherwise is an instant in
// which Ctrl-C quits personant instead of killing the command — the
// §4.3.3 design's stated non-goal.
//
// The obvious implementation — publish the child's process group as early
// as possible and treat "have pgid" as "have child" — cannot hold it. It
// makes the bad window SMALL rather than EMPTY, and a small window is
// exactly what a loaded machine finds. Ownership is therefore tracked
// separately from deliverability, in three states:
//
//	inFlight=false, pgid=0  no command; the signal is the session's
//	inFlight=true,  pgid=0  committed, group not yet in existence: the
//	                        signal is CLAIMED and QUEUED
//	inFlight=true,  pgid>0  running: the signal is forwarded at once
//
// Claiming without a pgid is what makes the window empty; queuing is what
// keeps the claimed signal from being swallowed. The claim is opened
// before anything that could block or fail, and closed atomically with
// the pgid at reap.
type Runner struct {
	shell      string
	captureMax int

	mu sync.Mutex
	// dir is the tracked shell cwd, adopted from each command's epilogue.
	dir string
	// inFlight spans the whole ownership window above: set when Run
	// commits to a command, cleared when its child has been reaped (or
	// when the child could never be created at all).
	inFlight bool
	// pgid is the process group of the in-flight child, or 0 when there is
	// no child to signal — which includes the interval between committing
	// to a command and the group existing.
	pgid int
	// pendingInterrupt records an interrupt claimed while pgid was still 0.
	// publishPGID delivers it the instant the group lands.
	pendingInterrupt bool
}

// NewRunner builds a session's runner. dir is the initial shell cwd —
// the process cwd at startup. An empty $SHELL falls back to /bin/sh.
func NewRunner(dir string) *Runner {
	sh := os.Getenv("SHELL")
	if strings.TrimSpace(sh) == "" {
		sh = FallbackShell
	}
	return &Runner{shell: sh, captureMax: DefaultCaptureMax, dir: dir}
}

// Dir reports the tracked shell cwd.
func (r *Runner) Dir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dir
}

// Shell reports the resolved shell binary.
func (r *Runner) Shell() string { return r.shell }

// Parse splits a REPL line into its §4.4.1 mode and command. ok is false
// when the line carries no shell prefix; a bare prefix with no command
// parses as ok with an empty command, which the caller rejects with a
// usage hint rather than running an empty shell.
func Parse(line string) (mode Mode, command string, ok bool) {
	switch {
	case strings.HasPrefix(line, string(ModeFire)):
		return ModeFire, strings.TrimSpace(line[1:]), true
	case strings.HasPrefix(line, string(ModeCapture)):
		return ModeCapture, strings.TrimSpace(line[1:]), true
	default:
		return "", "", false
	}
}

// Run executes one command, streaming its combined output to term as it
// arrives and (for ModeCapture) tee'ing it into a bounded buffer.
//
// A non-zero exit status is NOT an error: it is reported in the Result,
// exactly as a shell reports it. The returned error is reserved for
// failures to run the command at all (the shell binary is missing, the
// tracked cwd vanished, the pipe could not be created).
//
// One inherent difference between the modes: under ModeFire the child
// inherits the terminal's own descriptor, so isatty-sensitive tools
// colourize and lay out as usual. Under ModeCapture the output must pass
// through this process to be tee'd, so the child sees a PIPE — the same
// thing it sees inside `$(...)`, and unavoidable if the bytes are to be
// captured at all.
func (r *Runner) Run(ctx context.Context, mode Mode, command string, term io.Writer) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{Dir: r.Dir()}, errors.New("shell: empty command")
	}
	if term == nil {
		term = io.Discard
	}

	// The commit point. Everything below — the pipe, the fork, the exec —
	// can block or fail, and from here on an interrupt belongs to the
	// command. The deferred release is the backstop that closes the window
	// on every early return; the happy path releases earlier, at the reap.
	r.claimInterrupts()
	defer r.releaseInterrupts()

	trailerR, trailerW, err := os.Pipe()
	if err != nil {
		return Result{Dir: r.Dir()}, fmt.Errorf("shell: trailer pipe: %w", err)
	}
	defer trailerR.Close()

	cmd := exec.CommandContext(ctx, r.shell, "-c", command+cwdEpilogue)
	cmd.Dir = r.Dir()
	cmd.ExtraFiles = []*os.File{trailerW}
	// Stdin stays nil ON PURPOSE: os/exec routes a nil Stdin to the null
	// device, which is exactly the §4.4.3 contract. An interactive program
	// reads EOF and exits with its own complaint instead of wedging the
	// REPL forever on input that will never come.
	cmd.Stdin = nil

	// Assigning the SAME writer value to both Stdout and Stderr is
	// load-bearing, not tidiness: os/exec detects the two are == and gives
	// the child ONE descriptor for both, so stdout/stderr interleave in
	// true emission order the way `2>&1` does. Two distinct writers would
	// mean two pipes and two copier goroutines, and the ordering between
	// them would be arbitrary.
	var capBuf *boundedBuffer
	var sink io.Writer = term
	if mode == ModeCapture {
		capBuf = &boundedBuffer{max: r.captureMax}
		sink = io.MultiWriter(term, capBuf)
	}
	cmd.Stdout, cmd.Stderr = sink, sink

	setProcessGroup(cmd)

	at(HookChildStarting)

	if err := cmd.Start(); err != nil {
		trailerW.Close()
		// No child was ever created, so an interrupt claimed in the window
		// above has nothing to kill. The deferred release drops it — NOT
		// silently: the returned error is what tells the user the command
		// did not run, which is the outcome the interrupt was asking for.
		// Handing it back to the session instead would quit personant on a
		// keypress aimed at a command, which is the whole point of the
		// invariant.
		return Result{Dir: r.Dir()}, fmt.Errorf("shell: start %s: %w", r.shell, err)
	}
	// Make the group exist for the PARENT before publishing it, then
	// publish — which also delivers an interrupt claimed before now.
	confirmProcessGroup(cmd.Process.Pid)
	r.publishPGID(cmd.Process.Pid)

	// The parent's copy of the write end must go, or the trailer read
	// below never sees EOF.
	trailerW.Close()

	trailerCh := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(trailerR)
		trailerCh <- b
	}()

	waitErr := cmd.Wait()
	// The child is reaped: the invariant's window ends exactly here, so the
	// signal goes back to meaning "end the session" from this instruction
	// on. The trailer wait below can take seconds when a backgrounded
	// grandchild holds fd 3, and Ctrl-C must not be dead through it.
	r.releaseInterrupts()

	res := Result{ExitCode: exitCodeOf(cmd), Dir: r.Dir()}
	if capBuf != nil {
		res.Capture = capBuf.String()
		res.OutputBytes = capBuf.total
		res.Truncated = capBuf.truncated()
	}

	// The child has exited, so the trailer is already written unless a
	// backgrounded grandchild inherited fd 3 and is holding it open.
	// Bound that wait and carry on with the cwd we had.
	timer := time.NewTimer(trailerTimeout)
	defer timer.Stop()
	var trailer []byte
	select {
	case trailer = <-trailerCh:
	case <-timer.C:
		trailerR.Close() // unblocks the reader goroutine; its send is buffered
	}
	res.Dir = r.adoptDir(string(trailer))

	// A signalled shell (Ctrl-C) and a non-zero exit are ordinary outcomes
	// reported through Result, not errors. Only a failure to run at all is
	// an error, and Start already covered that — so an *exec.ExitError here
	// is swallowed deliberately.
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return res, fmt.Errorf("shell: wait: %w", waitErr)
	}
	return res, nil
}

// Interrupt claims a SIGINT on behalf of an in-flight `$`/`#` command and
// reports whether it did. A claimed signal is forwarded to the child's
// process group, or QUEUED when the group does not exist yet.
//
// The return value is the load-bearing part: chat.onSignal ends the
// session whenever this reports false, so declining while a command is in
// flight — for any reason, including "the child was started a microsecond
// ago" — quits personant on a keypress the user aimed at the command.
// Hence the invariant on Runner: in flight is claimed, full stop.
//
// The child runs in its OWN process group (see setProcessGroup), so the
// kernel does not deliver the terminal's SIGINT to it — forwarding here is
// the whole delivery path, and personant's own handler is left untouched.
//
// Called from the signal-handler goroutine.
func (r *Runner) Interrupt() bool {
	if !groupInterruptSupported {
		return false
	}
	r.mu.Lock()
	inFlight, pgid := r.inFlight, r.pgid
	if inFlight && pgid <= 0 {
		// Committed to a command whose process group is not in existence
		// yet. Take the signal anyway and leave it for publishPGID, which
		// delivers it the moment there is something to deliver it to.
		r.pendingInterrupt = true
	}
	r.mu.Unlock()

	if !inFlight {
		return false
	}
	if pgid > 0 {
		// A group that already exited (the child finished between the signal
		// arriving and this call) reports ESRCH. Nothing to do, but the
		// interrupt WAS consumed by the shell command as far as the user is
		// concerned, so report true and leave the session alone.
		_ = interruptGroup(pgid)
	}
	return true
}

// Running reports whether a `$`/`#` command is in flight — equivalently,
// whether Interrupt() would claim the signal for the command instead of
// letting it reach the session.
//
// It spans the WHOLE ownership window, so it is already true in the
// instant before the child's process group exists. That is deliberate:
// "the child process exists" and "Interrupt owns the signal" are not the
// same event, and only the second one is actionable.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inFlight
}

// claimInterrupts opens the ownership window. Called once per Run, before
// anything that can block or fail.
func (r *Runner) claimInterrupts() {
	r.mu.Lock()
	r.inFlight, r.pgid, r.pendingInterrupt = true, 0, false
	r.mu.Unlock()
}

// releaseInterrupts closes the window, handing SIGINT back to the session.
// Idempotent — Run calls it at the reap and again on the deferred backstop
// path — and it clears inFlight and pgid TOGETHER, so no observer can see
// a runner that owns the signal but has nothing to signal.
func (r *Runner) releaseInterrupts() {
	r.mu.Lock()
	r.inFlight, r.pgid, r.pendingInterrupt = false, 0, false
	r.mu.Unlock()
}

// publishPGID records the started child's process group and delivers an
// interrupt that was claimed before the group existed.
//
// Delivery happens outside the lock; a group whose only member has already
// exited reports ESRCH, which is the right outcome anyway — the interrupt
// asked for a dead command and got one.
func (r *Runner) publishPGID(pgid int) {
	r.mu.Lock()
	r.pgid = pgid
	pending := r.pendingInterrupt
	r.pendingInterrupt = false
	r.mu.Unlock()
	if pending {
		_ = interruptGroup(pgid)
	}
}

// adoptDir takes the epilogue's reported cwd as the new tracked cwd and
// returns the resulting value.
//
// It refuses anything that is not currently a directory. That is not
// paranoia about the shell: `$ mkdir /tmp/x && cd /tmp/x && rmdir /tmp/x`
// leaves the shell reporting a cwd that no longer exists, and adopting it
// would make the NEXT command fail at chdir with a confusing error. The
// last known-good directory is the useful fallback.
func (r *Runner) adoptDir(reported string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reported != "" && reported != r.dir {
		if fi, err := os.Stat(reported); err == nil && fi.IsDir() {
			r.dir = reported
		}
	}
	return r.dir
}

// exitCodeOf reports the command's status. ProcessState.ExitCode is the
// single source of truth — the epilogue's `exit "$__personant_rc"` hands
// the user command's own status back through the process, so the trailing
// printf cannot mask it. A shell killed by a signal reports -1.
func exitCodeOf(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

// boundedBuffer accumulates up to max bytes while counting every byte
// written, so a runaway command is clipped in memory but still reports
// its true size. Writes never fail: dropping output must not kill the
// command that produced it.
type boundedBuffer struct {
	max   int
	total int
	buf   bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string  { return b.buf.String() }
func (b *boundedBuffer) truncated() bool { return b.total > b.buf.Len() }
