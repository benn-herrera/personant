//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package chat

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// cbreakReadDeciseconds is the VTIME value the mid-turn read runs under.
// VTIME is measured in DECIseconds, so 1 — 100 ms — is the finest window
// the terminal driver offers. It does double duty: it bounds how long the
// watcher goroutine can sit in a read before it notices a stop request,
// and it IS the Esc-disambiguation timer (an expired window with nothing
// typed is what proves an ESC was bare rather than the prefix of an arrow
// key). 100 ms is coarser than the ~50 ms an ad-hoc wall-clock timer
// would use, and deliberately so: the driver's own timer costs nothing
// and cannot drift against the read.
const cbreakReadDeciseconds = 1

// termState is a saved terminal mode. restore puts back exactly the bytes
// that were read, so whatever owned the terminal before (liner) finds its
// mode untouched.
type termState struct{ mode unix.Termios }

// captureTerm reads the terminal's current mode WITHOUT changing it.
//
// It exists for the §4.4 shell escape, which needs the mode personant
// found at startup — before liner took the terminal — so a child process
// runs under normal line discipline. liner.NewLiner applies ICANON/ECHO
// off ONCE for the whole session and only restores at Close, so "the
// current mode" during a session is liner's, not the user's; the session
// captures the real original at startup and hands that to children.
func captureTerm() (*termState, error) {
	current, err := unix.IoctlGetTermios(unix.Stdin, ioctlGetTermios)
	if err != nil {
		return nil, fmt.Errorf("chat: read terminal mode: %w", err)
	}
	return &termState{mode: *current}, nil
}

// enterCbreak switches stdin to the mid-turn read mode and returns the
// mode it replaced.
//
// This is cbreak, NOT raw. Exactly three things change:
//
//   - ICANON off — bytes are delivered as typed, so a lone Esc is visible
//     without waiting for a newline.
//   - ECHO off — stray keys pressed while the model is streaming do not
//     litter the response.
//   - VMIN=0 / VTIME=1 replace liner's VMIN=1 / VTIME=0, which is what
//     makes a read return on a timer instead of blocking until a keypress.
//
// Deliberately NOT touched, and this is the whole reason term.MakeRaw is
// wrong here:
//
//   - OPOST stays ON. Raw mode clears it, and then a bare "\n" no longer
//     implies a carriage return — the progress indicator and the streamed
//     response body both emit bare newlines, so raw mode would staircase
//     every line of output unless every writer were rewritten.
//   - ISIG stays ON. Raw mode clears it, and then Ctrl-C arrives as byte
//     0x03 instead of a signal, forcing a hand-rolled reimplementation of
//     the interrupt semantics this change deliberately keeps.
//
// In practice liner has already cleared ICANON and ECHO for the whole
// session (its NewLiner applies that mode once and only restores it at
// Close), so VMIN/VTIME is the sole load-bearing edit; the other two are
// belt-and-braces for the case where something else restored a cooked
// mode underneath us.
func enterCbreak() (*termState, error) {
	saved, err := captureTerm()
	if err != nil {
		return nil, err
	}
	mode := saved.mode
	mode.Lflag &^= unix.ICANON | unix.ECHO
	mode.Cc[unix.VMIN] = 0
	mode.Cc[unix.VTIME] = cbreakReadDeciseconds
	if err := unix.IoctlSetTermios(unix.Stdin, ioctlSetTermios, &mode); err != nil {
		return nil, fmt.Errorf("chat: set terminal mode: %w", err)
	}
	return saved, nil
}

// restore puts back the saved mode byte-for-byte.
func (s *termState) restore() error {
	mode := s.mode
	if err := unix.IoctlSetTermios(unix.Stdin, ioctlSetTermios, &mode); err != nil {
		return fmt.Errorf("chat: restore terminal mode: %w", err)
	}
	return nil
}

// readStdin is one read(2) against the terminal.
//
// Deliberately NOT os.Stdin.Read: under VMIN=0/VTIME=1 the kernel returns
// zero bytes when the window expires with nothing typed, and os.File maps
// a zero-byte read to io.EOF — which would turn every idle tick into a
// spurious end-of-input. The raw syscall reports it honestly as (0, nil),
// and that honest zero is exactly the signal the Esc disambiguator needs.
//
// EINTR is not an error here: a SIGWINCH (liner installs a handler) or the
// interrupt handler firing mid-read just means "try again".
func readStdin(p []byte) (int, error) {
	n, err := unix.Read(unix.Stdin, p)
	if n < 0 {
		n = 0
	}
	if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
		return n, nil
	}
	return n, err
}
