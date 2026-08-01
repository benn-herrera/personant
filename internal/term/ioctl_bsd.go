//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package term

import "golang.org/x/sys/unix"

// The BSD family names the termios ioctls TIOCGETA / TIOCSETA. Kept
// untyped so they satisfy both spellings of x/sys/unix's ioctl helpers
// (uint on BSD, int on Linux) without a conversion at the call site.
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
