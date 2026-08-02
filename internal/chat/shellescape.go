package chat

import (
	"context"
	"fmt"
	"strconv"

	"personant/internal/memops"
	"personant/internal/shell"
	"personant/internal/turn"
)

// SPEC §4.4 shell escape — the REPL half.
//
// internal/shell owns execution, cwd tracking, capture bounding and
// redaction. This file owns dispatch and rendering only: parse the
// prefix, hand the terminal over and take it back, log the §2.8
// `user.shell` event, and turn a `#` capture into a pre-prompt delta for
// the next turn.

// pendingCaptureMax bounds the TOTAL bytes of `#` captures buffered
// between prompts, across however many commands the user ran.
//
// It is a second, coarser bound than shell.DefaultCaptureMax, which caps
// one command: ten `# cat`s of a large file would each pass the per-
// command bound and still accumulate. Neither is the §6.5 context cap —
// that one lives in internal/turn and governs what reaches the model.
// This is purely about process memory between two prompts.
const pendingCaptureMax = 512 << 10

// runShellEscape executes one `$`/`#` line and returns the pre-prompt
// delta it produced, or nil for `$` (fire-and-forget captures nothing).
//
// The returned error is a failure to RUN the command. A command that ran
// and exited non-zero is not an error — it is reported on the terminal
// and in the event line, exactly as a shell reports it.
func runShellEscape(
	ctx context.Context,
	ops memops.MemoryOps,
	ctl *control,
	sh *shell.Runner,
	red *shell.Redactor,
	line string,
) (*turn.Delta, error) {
	tm := ctl.t
	mode, command, ok := shell.Parse(line)
	if !ok {
		return nil, fmt.Errorf("not a shell escape: %q", line)
	}
	if command == "" {
		fmt.Fprintf(tm.Diag(), "usage: %s<command> — %s\n", mode, modeHint(mode))
		return nil, nil
	}

	// Nothing may be decorating the terminal while a child owns it. No turn
	// is in flight at the prompt, so the registration should already be
	// released and the indicator already retired — these two calls are
	// idempotent and assert that cheaply rather than trusting it.
	ctl.disarm()
	ctl.pr.stop()

	// term.Handoff is the §4.4 window, and it is a CALLBACK so it cannot be
	// leaked: the pump is joined before the child starts, the entry mode
	// (ISIG included, so the kernel still generates SIGINT for personant
	// while the child runs in its own process group) is restored for its
	// duration, and both are put back afterwards. Child spawn and terminal
	// handoff now compose in exactly one function — U9, which the old
	// capture/restore pair could not promise.
	//
	// The child's output is COMMITTED content and goes out on term's Out
	// channel like any other — which is also what keeps the cursor-column
	// belief current across the command.
	var res shell.Result
	var runErr error
	if herr := tm.Handoff(func() error {
		res, runErr = sh.Run(ctx, mode, command, tm.Out())
		return nil
	}); herr != nil {
		return nil, herr
	}
	if runErr != nil {
		return nil, runErr
	}

	// Legibility beats shell fidelity here. A bare shell prints nothing on
	// a non-zero exit and the user reads $?; inside a REPL there is no $?
	// to read, and the §4.4.3 interactive-app failure (vim finding its
	// stdin at /dev/null and bailing) would otherwise look like the command
	// silently doing nothing.
	if res.ExitCode != 0 {
		fmt.Fprintln(tm.Out(), exitNotice(res.ExitCode))
	}

	// The event line carries the invocation, never the output: §2.8 lines
	// are single-line free-form and captures are large and multi-line. The
	// COMMAND is redacted too — §8.2.1's "resolved key material must never
	// appear in any log line" binds regardless of which end typed it, and
	// `$ export API_KEY=...` is an ordinary thing for a user to do.
	loggedCmd, _, _ := red.Redact(command)
	details := fmt.Sprintf("kind=%s exit=%d dir=%q cmd=%q", mode, res.ExitCode, res.Dir, loggedCmd)
	if mode == shell.ModeCapture {
		details += " bytes=" + strconv.Itoa(len(res.Capture))
		if res.Truncated {
			details += " truncated=yes produced=" + strconv.Itoa(res.OutputBytes)
		}
	}
	if err := ops.Log(ctx, memops.LogCategoryUser, "shell", details); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: log user.shell: %v\n", err)
	}

	if mode != shell.ModeCapture || res.Capture == "" {
		return nil, nil
	}

	// §6.4/§8.2.1: redact on the path into context only. The unredacted
	// bytes have ALREADY streamed to the user's terminal above — that is
	// the policy, not an oversight.
	captured, hits, removed := red.Redact(res.Capture)
	if hits > 0 {
		if err := ops.Log(ctx, memops.LogCategoryPermissions, "redaction-fire",
			fmt.Sprintf("source=%s occurrences=%d bytes=%d", memops.SourceUserShellCapture, hits, removed)); err != nil {
			fmt.Fprintf(tm.Diag(), "warn: log permissions.redaction-fire: %v\n", err)
		}
	}

	return &turn.Delta{
		Source:    memops.SourceUserShellCapture,
		Content:   captured,
		Retention: memops.RetentionTask,
		Meta:      map[string]string{"command": loggedCmd, "exit": strconv.Itoa(res.ExitCode)},
	}, nil
}

func modeHint(mode shell.Mode) string {
	if mode == shell.ModeCapture {
		return "run it and add the output to the next turn's context"
	}
	return "run it; output goes to the terminal only"
}

// exitNotice renders a non-zero outcome. A negative code means the shell
// was killed by a signal — the Ctrl-C path — which is a user action, not
// a failure, and reads badly as "exit -1".
func exitNotice(code int) string {
	if code < 0 {
		return "[interrupted]"
	}
	return "[exit " + strconv.Itoa(code) + "]"
}

// appendCapture buffers a `#` capture for the next turn, holding the
// buffered total under pendingCaptureMax by dropping the OLDEST captures
// first. Recency wins because the user's next prompt is far more likely to
// be about what they just ran than about something six commands ago.
func appendCapture(pending []turn.Delta, d turn.Delta) []turn.Delta {
	pending = append(pending, d)
	total := 0
	for _, p := range pending {
		total += len(p.Content)
	}
	drop := 0
	for total > pendingCaptureMax && drop < len(pending)-1 {
		total -= len(pending[drop].Content)
		drop++
	}
	return pending[drop:]
}
