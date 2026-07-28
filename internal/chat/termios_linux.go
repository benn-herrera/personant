//go:build linux

package chat

import "golang.org/x/sys/unix"

// Linux names the termios ioctls TCGETS / TCSETS. See termios_bsd.go for
// why these stay untyped.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
