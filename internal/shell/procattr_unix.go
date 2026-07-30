//go:build unix

package shell

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in a process group of its own, rooted at
// its own pid.
//
// This is what makes Ctrl-C safe during a `$`/`#` command. personant's
// SIGINT handler means "end the session"; a shell command must instead
// take the standard meaning, "kill what is running and give me back the
// prompt". With the child in its own group the terminal's SIGINT is
// delivered ONLY to personant, which forwards it to the group deliberately
// (Runner.Interrupt) instead of both processes racing to interpret it.
//
// The child is a BACKGROUND group as far as the terminal is concerned, so
// it would take SIGTTIN if it read the terminal — it cannot, because its
// stdin is the null device (§4.4.3). Terminal writes from a background
// group are permitted unless TOSTOP is set, which it is not by default,
// so no tcsetpgrp handoff is needed.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// groupInterruptSupported gates Runner.Interrupt: where it is false, a
// Ctrl-C during a shell command must fall through to the REPL's own
// handling rather than being silently swallowed by a no-op.
const groupInterruptSupported = true

// interruptGroup sends SIGINT to every process in pgid. The negative pid
// is the POSIX "whole process group" form, so a pipeline dies as a unit
// the way it would under a real shell.
func interruptGroup(pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGINT)
}
