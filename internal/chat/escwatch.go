package chat

import "sync"

const (
	// escByte is the ESC control character.
	escByte = 0x1b

	// escReadBufBytes sizes the watcher's read buffer. An escape sequence
	// is a handful of bytes and a burst of type-ahead is discarded anyway,
	// so this only needs to be comfortably larger than one sequence.
	escReadBufBytes = 64
)

// escScanner disambiguates a bare Esc keypress from the ESC that
// introduces an arrow / function key.
//
// The rule, and the whole reason cbreak sets VMIN=0/VTIME=1:
//
//   - ESC followed by '[' (CSI) or 'O' (SS3) is a key sequence. Consume
//     through its final byte and ignore it — a stray arrow key must never
//     abort the user's turn.
//   - ESC followed by NOTHING for one read window is a bare Esc: abort.
//     The empty chunk fed by an expired read window is the proof; there is
//     no wall-clock timer anywhere in this state machine.
//   - ESC followed by any other byte is a modified keystroke (Alt-x).
//     Swallowed, no abort.
//
// It is a pure state machine over byte slices with no I/O, no clock and
// no terminal, which is what lets the interesting cases be table-tested
// off a TTY.
type escScanner struct {
	pendingEsc bool // an ESC arrived and nothing has resolved it yet
	inSequence bool // consuming the body of a CSI/SS3 sequence
}

// feed consumes one terminal read and reports whether a BARE Esc
// completed.
//
// An EMPTY chunk is not "nothing happened" — it is the VMIN=0/VTIME=1
// idle tick, i.e. the read window expired with nothing typed. That tick
// is what resolves a pending ESC, and it also abandons a sequence that
// was cut short, so a truncated sequence cannot wedge the scanner.
func (s *escScanner) feed(chunk []byte) bool {
	if len(chunk) == 0 {
		s.inSequence = false
		if s.pendingEsc {
			s.pendingEsc = false
			return true
		}
		return false
	}
	for _, c := range chunk {
		switch {
		case s.inSequence:
			// An ESC inside a sequence means the sequence was interrupted.
			// Restart on it rather than consuming forward forever.
			switch {
			case c == escByte:
				s.inSequence = false
				s.pendingEsc = true
			case isSequenceFinal(c):
				s.inSequence = false
			}
		case s.pendingEsc:
			s.pendingEsc = false
			switch c {
			case '[', 'O':
				s.inSequence = true
			case escByte:
				s.pendingEsc = true
			}
		case c == escByte:
			s.pendingEsc = true
		}
		// Any other byte is an ordinary keystroke typed while the model is
		// working. There is no reader for it, so it is discarded.
	}
	return false
}

// isSequenceFinal reports whether c terminates a CSI or SS3 escape
// sequence — the ECMA-48 final-byte range 0x40–0x7E. It covers both
// shapes: "ESC [ A" ends on 'A', "ESC O P" ends on 'P', and a
// parameterised "ESC [ 1 ; 5 A" ends on 'A' because the intermediate
// bytes all sit below 0x40.
func isSequenceFinal(c byte) bool { return c >= 0x40 && c <= 0x7e }

// escWatcher reads the terminal for the duration of ONE turn and fires
// onEsc when a bare Esc arrives.
//
// It owns no terminal state: the caller enters cbreak before starting it
// and restores the saved mode only after close() has returned, so no read
// can ever straddle a mode change or outlive the turn that armed it.
type escWatcher struct {
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// startEscWatcher launches the watcher. read is one terminal read (see
// readStdin); it must return promptly with (0, nil) when nothing was
// typed, because that bounded return is both the stop latency and the Esc
// disambiguation signal. onEsc fires at most once — the watcher retires
// itself immediately afterwards, handing the terminal straight back.
func startEscWatcher(read func([]byte) (int, error), onEsc func()) *escWatcher {
	w := &escWatcher{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		var sc escScanner
		buf := make([]byte, escReadBufBytes)
		for {
			select {
			case <-w.stop:
				return
			default:
			}
			n, err := read(buf)
			if err != nil {
				return // stdin is gone (EOF or a hard error); nothing left to watch
			}
			if sc.feed(buf[:n]) {
				onEsc()
				return
			}
		}
	}()
	return w
}

// close stops the watcher and JOINS its goroutine, so on return nothing
// is reading the terminal. Idempotent, and safe to call after the
// goroutine has already retired itself.
func (w *escWatcher) close() {
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
}
