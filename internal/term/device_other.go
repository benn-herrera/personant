//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package term

import (
	"errors"
	"os"
)

// errNoDevice is the whole of the non-unix S1 story for now. Windows is
// deliberately deferred (axiom §4.6): the requirement is that the S2
// platform seam ADMITS a ReadConsoleInput backend later, not that W1
// writes one.
var errNoDevice = errors.New("term: no terminal device implementation on this platform")

// NewUnixDevice always fails here, which selects the plain backend for
// the whole session (U12) — the same path a pipe takes. A personant
// session on such a platform runs with no line editing and no
// decoration, exactly as a piped one does, rather than not running.
func NewUnixDevice(in, out *os.File) (Device, error) { return nil, errNoDevice }

// raiseTSTP has no job here: the plain backend is the only one this
// platform selects, so term never owns a mode to restore and the in-band
// Ctrl-Z that would call this is never decoded.
func raiseTSTP() {}
