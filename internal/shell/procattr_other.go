//go:build !unix

package shell

import (
	"errors"
	"os/exec"
)

// setProcessGroup is a no-op where process groups are not the platform's
// job-control model (notably Windows, deliberately deferred alongside the
// rest of the chat package's terminal handling). The command still runs;
// only Ctrl-C forwarding is unavailable.
func setProcessGroup(*exec.Cmd) {}

// groupInterruptSupported is false here, so Runner.Interrupt declines the
// interrupt and the REPL's ordinary Ctrl-C handling still applies. A
// silently-swallowed Ctrl-C would be worse than an un-interruptible
// command: the user would have no working key at all.
const groupInterruptSupported = false

func interruptGroup(int) error {
	return errors.New("shell: process-group interrupt unsupported on this platform")
}
