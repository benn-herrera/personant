//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// deviceReadWindow is how long one [Device.Read] waits for the terminal to
// become readable before reporting an expired window.
//
// It is an argument to poll(2), NOT terminal state, and that is the whole
// of §3's "zero prompt/turn transitions". The old code carried the same
// number as VMIN=0/VTIME=1 in the installed termios, so it had to be put
// there before a turn and taken away after — two transitions per turn that
// something else could observe, adopt, or leave stale, which is bug 3. A
// parameter has none of those properties.
//
// 100ms because it does triple duty and each use wants a bound rather than
// a precise value: it is the ESC-disambiguation window (an expiry with
// nothing typed is what proves an ESC was bare), the pump's stop and
// context-cancellation latency, and how long a queued event can sit in the
// reservoir before a newly-registered consumer is offered it.
const deviceReadWindow = 100 * time.Millisecond

// NewUnixDevice opens the S1 device over a terminal, capturing the entry
// mode. in supplies reads, termios and the winsize ioctl; out receives
// every emitted byte. See the two-files note in seam.go for why it is not
// one file.
//
// It fails when in is not a terminal or the mode cannot be read — and
// that failure is what selects the plain backend for the WHOLE session
// (U12). Reading the mode doubles as the is-this-a-terminal test: the
// ioctl is exactly the question, and asking it once at Open is what makes
// the answer a session-long fact rather than a per-call one.
//
// Capturing the entry mode is a READ. Nothing is installed here; the
// session mode arrives at [Open] and is put back, byte for byte, at
// [Terminal.Close].
func NewUnixDevice(in, out *os.File) (Device, error) {
	if in == nil || out == nil {
		return nil, errNotTerminal
	}
	entry, err := unix.IoctlGetTermios(int(in.Fd()), ioctlGetTermios)
	if err != nil {
		return nil, fmt.Errorf("term: read terminal mode: %w", err)
	}
	d := &unixDevice{in: in, out: out, entry: *entry, resized: make(chan struct{}, 1)}
	d.watchResize()
	return d, nil
}

// unixDevice is the S1 implementation. It holds the entry termios so
// [ModeEntry] can be restored byte for byte — "put back exactly what we
// found" rather than "construct something that looks cooked".
type unixDevice struct {
	in, out *os.File
	entry   unix.Termios

	// The SIGWINCH plumbing. signal.Notify needs a chan os.Signal and the
	// seam carries a chan struct{}, so one forwarder goroutine bridges them
	// and Close joins it.
	winch     chan os.Signal
	resized   chan struct{}
	winchDone chan struct{}
	closeOnce sync.Once
}

// Read is one poll(2) plus at most one read(2).
//
// It returns (0, nil) when the poll window expires with nothing typed.
// That honest zero is DATA — the decoder reads it as proof that a pending
// ESC was bare — and it is deliberately NOT os.File.Read, which maps a
// zero-byte read to io.EOF and would turn every idle window into a
// spurious end of input.
//
// EINTR is not an error: a SIGWINCH or any other delivered signal just
// means the window ended early, which is indistinguishable from it ending
// on time and is handled by the same (0, nil).
func (d *unixDevice) Read(p []byte) (int, error) {
	fd := int(d.in.Fd())
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(deviceReadWindow/time.Millisecond))
	if err != nil {
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			return 0, nil
		}
		return 0, fmt.Errorf("term: poll stdin: %w", err)
	}
	if n == 0 {
		return 0, nil // the expired window
	}
	if fds[0].Revents&unix.POLLIN == 0 && fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
		return 0, io.EOF
	}
	m, err := unix.Read(fd, p)
	if m < 0 {
		m = 0
	}
	if err != nil {
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			return m, nil
		}
		return m, fmt.Errorf("term: read stdin: %w", err)
	}
	if m == 0 {
		// poll said readable and the read returned nothing: the other end
		// is gone. This is the ONE zero that means end of input.
		return 0, io.EOF
	}
	return m, nil
}

func (d *unixDevice) Write(p []byte) (int, error) { return d.out.Write(p) }

// InstallMode installs the named mode.
//
// Both intents are derived from the ENTRY termios rather than from each
// other, so [ModeEntry] is a byte-for-byte restore of what personant found
// and [ModeSession] cannot accumulate drift across a session's transitions.
//
// [ModeSession] is cbreak, not raw (axiom §4.4, as amended by the
// 2026-07-31 ruling):
//
//   - ICANON, ECHO off — bytes arrive as typed and the editor, not the
//     kernel, decides what appears.
//   - ISIG, IEXTEN, IXON off — Ctrl-C, Ctrl-Z, Ctrl-V and Ctrl-S/Q arrive
//     as BYTES. Ctrl-C in band is what permits a disposition keyed on the
//     editor's buffer (clear the line), which an async signal handler
//     cannot read without racing the editor; Ctrl-Z in band is what lets
//     the fd's owner restore the entry mode before it suspends.
//   - OPOST untouched. Clearing it would stop a bare "\n" implying a
//     carriage return and staircase every writer in the program.
//   - VMIN/VTIME untouched. Readiness is poll(2)'s argument, not installed
//     state — see [deviceReadWindow].
func (d *unixDevice) InstallMode(m ModeIntent) error {
	mode := d.entry
	if m == ModeSession {
		mode.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG | unix.IEXTEN
		mode.Iflag &^= unix.IXON
	}
	if err := unix.IoctlSetTermios(int(d.in.Fd()), ioctlSetTermios, &mode); err != nil {
		return fmt.Errorf("term: install terminal mode: %w", err)
	}
	return nil
}

func (d *unixDevice) Size() (Size, error) {
	ws, err := unix.IoctlGetWinsize(int(d.out.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return Size{}, fmt.Errorf("term: read window size: %w", err)
	}
	return Size{Cols: int(ws.Col), Rows: int(ws.Row)}, nil
}

// watchResize starts the SIGWINCH forwarder. The resized channel is
// buffered to one and fed with a NON-BLOCKING send: a resize is an edge,
// not a queue, and the consumer always re-reads the current size anyway,
// so coalescing two rapid resizes into one notification is correct rather
// than lossy.
func (d *unixDevice) watchResize() {
	d.winch = make(chan os.Signal, 1)
	d.winchDone = make(chan struct{})
	signal.Notify(d.winch, unix.SIGWINCH)
	go func() {
		defer close(d.winchDone)
		for range d.winch {
			select {
			case d.resized <- struct{}{}:
			default:
			}
		}
	}()
}

// Resized fires whenever the window size changed. It is out of band
// because SIGWINCH is a signal, never a byte — a byte seam could never
// have carried it, on unix any more than on Windows.
func (d *unixDevice) Resized() <-chan struct{} { return d.resized }

// Close releases the device WITHOUT closing the files. They are the
// process's own standard descriptors, handed in rather than opened here,
// and closing another owner's fd is the same category of mistake this
// package exists to remove. Restoring the mode is [Terminal.Close]'s
// explicit final install, which has already happened by the time this
// runs.
func (d *unixDevice) Close() error {
	d.closeOnce.Do(func() {
		signal.Stop(d.winch)
		close(d.winch)
		<-d.winchDone
	})
	return nil
}

// raiseTSTP stops this process the way the kernel would have if ISIG were
// still on. SIGTSTP keeps its DEFAULT disposition — nothing calls
// signal.Notify for it, so the Go runtime leaves it alone — which is what
// makes this an actual suspend rather than a delivered signal nobody
// handles. It returns when SIGCONT resumes the process.
func raiseTSTP() { _ = unix.Kill(unix.Getpid(), unix.SIGTSTP) }
