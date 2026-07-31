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

// confirmProcessGroup makes the child's process group exist from the
// PARENT's point of view before Runner publishes it.
//
// exec.Cmd's Setpgid runs inside the CHILD, between fork and exec. The
// parent returns from Start as soon as the fork succeeds, so there is a
// brief interval in which kill(-pid) finds no such group and fails with
// ESRCH — the classic fork/setpgid race every job-control shell has to
// deal with. Publishing a pgid that cannot yet be signalled would leave a
// hole in the interrupt-ownership invariant precisely where it was just
// closed everywhere else.
//
// POSIX lets the parent make the same call, and that is the standard fix:
// whichever call lands first wins and the other is a no-op. Both failure
// modes are benign, which is why the error is deliberately dropped rather
// than propagated — EACCES means the child has already exec'd, which can
// only happen after its own setpgid ran, and ESRCH means it has already
// exited. In every outcome the group is established (or moot) by the time
// this returns.
func confirmProcessGroup(pid int) {
	_ = syscall.Setpgid(pid, pid)
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
