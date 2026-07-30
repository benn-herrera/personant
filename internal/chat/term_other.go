//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package chat

import "errors"

// errCbreakUnsupported is returned on platforms with no cbreak
// implementation (notably Windows, which is deliberately deferred). The
// session still runs: Esc-to-abort is simply unavailable, the progress
// indicator does not advertise it, and Ctrl-C keeps its full semantics.
var errCbreakUnsupported = errors.New("chat: terminal cbreak mode unsupported on this platform")

type termState struct{}

func enterCbreak() (*termState, error) { return nil, errCbreakUnsupported }

// captureTerm has no implementation here for the same reason enterCbreak
// does not: the §4.4 shell escape simply skips the terminal handoff, and
// the child inherits whatever mode the session is in.
func captureTerm() (*termState, error) { return nil, errCbreakUnsupported }

func (s *termState) restore() error { return nil }

func readStdin([]byte) (int, error) { return 0, errCbreakUnsupported }
