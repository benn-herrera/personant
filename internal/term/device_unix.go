//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package term

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

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
// Capturing the entry mode is a READ. W1 installs nothing — liner still
// owns the mode until W3 (see [Open]) — so opening the device here cannot
// collide with it.
func NewUnixDevice(in, out *os.File) (Device, error) {
	if in == nil || out == nil {
		return nil, errNotTerminal
	}
	entry, err := unix.IoctlGetTermios(int(in.Fd()), ioctlGetTermios)
	if err != nil {
		return nil, fmt.Errorf("term: read terminal mode: %w", err)
	}
	return &unixDevice{in: in, out: out, entry: *entry}, nil
}

// unixDevice is the S1 implementation. It holds the entry termios so
// [ModeEntry] can be restored byte for byte — "put back exactly what we
// found" rather than "construct something that looks cooked".
type unixDevice struct {
	in, out *os.File
	entry   unix.Termios
}

// Read is one read(2), deliberately NOT os.File.Read: under a read window
// that expires with nothing typed the kernel returns zero bytes, and
// os.File maps a zero-byte read to io.EOF — which would turn every idle
// window into a spurious end of input. The raw syscall reports the honest
// zero, and that zero is what the ESC disambiguator reads as proof.
//
// Lands with the pump in W3.
func (d *unixDevice) Read([]byte) (int, error) { panic(notImplementedW3) }

func (d *unixDevice) Write(p []byte) (int, error) { return d.out.Write(p) }

// InstallMode is W3's. Until then liner owns the mode and term must not
// touch it; installing here would be bug 3 with the parties reversed.
func (d *unixDevice) InstallMode(ModeIntent) error { panic(notImplementedW3) }

func (d *unixDevice) Size() (Size, error) {
	ws, err := unix.IoctlGetWinsize(int(d.out.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return Size{}, fmt.Errorf("term: read window size: %w", err)
	}
	return Size{Cols: int(ws.Col), Rows: int(ws.Row)}, nil
}

// Resized is fed by the SIGWINCH handler term installs with the pump in
// W3. Signal registration is deferred with mode ownership for one reason:
// a handler that repaints or re-installs a mode term never installed
// would be acting on somebody else's terminal.
func (d *unixDevice) Resized() <-chan struct{} { panic(notImplementedW3) }

// Close releases the device WITHOUT closing the files. They are the
// process's own standard descriptors, handed in rather than opened here,
// and closing another owner's fd is the same category of mistake this
// package exists to remove. Restoring the mode is [Terminal.Close]'s
// explicit final install, from W3.
func (d *unixDevice) Close() error { return nil }
