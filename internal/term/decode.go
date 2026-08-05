package term

import "unicode/utf8"

// The decoder: S1 bytes in, [Event] out (§7). It is the whole reason no
// consumer may read the fd for itself.
//
// # Why the lookahead cannot be handed over
//
// A bare Esc and the ESC that introduces an arrow key differ only in what
// arrives AFTERWARDS. Deciding between them therefore requires holding
// bytes read-but-not-yet-interpreted across a boundary in time, and a
// lease on the fd can be released at an instant — so "the lessee reads"
// makes every release lossy exactly when a sequence straddles it (§3).
// The lookahead lives here, in the one place that never hands over.
//
// # The timeout is DATA, not a clock
//
// There is no wall clock anywhere below. What resolves a pending ESC is a
// read window that closed with nothing typed — [Device.Read] returning
// (0, nil) — which the pump reports as expired == true. That is why the
// device must never map its honest zero to io.EOF, and it is what lets
// every interesting case be table-tested against a byte-level fake with
// no terminal and no sleep.

const (
	escByte = 0x1b

	// readBufBytes sizes one read(2). An escape sequence is a handful of
	// bytes; this only has to be comfortably larger than one, plus room for
	// a paste-sized burst so type-ahead arrives in few reads.
	readBufBytes = 1024
)

// decoder is the pump's byte reservoir and state. There is exactly one per
// session and it is touched only by the pump goroutine.
type decoder struct {
	// pend holds bytes that are not yet a complete key: a lone ESC, a CSI
	// sequence cut in half by a read boundary, a partial UTF-8 rune.
	pend []byte
}

// feed consumes one read's worth of bytes and returns every event that is
// now unambiguous. expired reports that the read window closed with
// nothing typed, which is the only thing that can resolve a pending ESC
// and the only thing that abandons a truncated sequence.
func (d *decoder) feed(chunk []byte, expired bool) []Event {
	d.pend = append(d.pend, chunk...)
	var evs []Event
	for len(d.pend) > 0 {
		k, n, st := decodeOne(d.pend, expired)
		if st == decodeIncomplete {
			break
		}
		d.pend = d.pend[n:]
		if st == decodeEmit {
			evs = append(evs, Event{Kind: EventKey, Key: k})
		}
	}
	if expired {
		// Whatever is left after an expiry can never be completed: the
		// window closed on it. Dropping it is what stops a truncated
		// sequence from wedging the decoder against the next keystroke.
		d.pend = d.pend[:0]
	}
	return evs
}

// decodeStatus says what decodeOne did with the front of the buffer.
type decodeStatus int

const (
	// decodeIncomplete means the bytes are a prefix of something longer and
	// nothing was consumed. The caller waits for more, or for an expiry.
	decodeIncomplete decodeStatus = iota
	// decodeEmit means n bytes produced the returned key.
	decodeEmit
	// decodeSkip means n bytes were consumed and produced nothing — a
	// sequence this vocabulary deliberately does not carry (a function key,
	// a mouse report) or a byte that is not a key at all.
	decodeSkip
)

// decodeOne interprets the front of p.
func decodeOne(p []byte, expired bool) (Key, int, decodeStatus) {
	if p[0] != escByte {
		return decodePlain(p, expired)
	}
	if len(p) == 1 {
		if expired {
			return Key{Name: KeyEsc}, 1, decodeEmit // the bare Esc, proven by silence
		}
		return Key{}, 0, decodeIncomplete
	}
	switch p[1] {
	case '[':
		return decodeCSI(p, expired)
	case 'O':
		return decodeSS3(p, expired)
	case escByte:
		// Two Escs in a row are two Escs. Emitting the first here rather
		// than waiting keeps the second one's own disambiguation intact.
		return Key{Name: KeyEsc}, 1, decodeEmit
	default:
		// ESC-prefixed anything else is the Meta/Alt form.
		k, n, st := decodePlain(p[1:], expired)
		if st == decodeIncomplete {
			return Key{}, 0, decodeIncomplete
		}
		k.Alt = true
		return k, n + 1, st
	}
}

// decodeCSI interprets "ESC [ <params/intermediates> <final>" (ECMA-48:
// parameter bytes 0x30-0x3f, intermediates 0x20-0x2f, final 0x40-0x7e).
func decodeCSI(p []byte, expired bool) (Key, int, decodeStatus) {
	i := 2
	for i < len(p) && p[i] >= 0x20 && p[i] <= 0x3f {
		i++
	}
	if i == len(p) {
		if expired {
			return Key{}, i, decodeSkip // the window closed mid-sequence
		}
		return Key{}, 0, decodeIncomplete
	}
	if p[i] < 0x20 {
		// A control byte inside a sequence means the sequence was
		// interrupted. Consume what came before it and let the control byte
		// be decoded on its own — an ESC here restarts, rather than being
		// swallowed as a parameter.
		return Key{}, i, decodeSkip
	}
	k, ok := csiKey(p[2:i], p[i])
	if !ok {
		return Key{}, i + 1, decodeSkip
	}
	return k, i + 1, decodeEmit
}

// The kitty CSI-u modifier parameter is 1 + a bitmask, so an unmodified
// key is 1, Shift is 2 and Alt is 3. Only these two bits are read; Ctrl,
// Super and the rest fall through to the unmodified key, which is what
// this vocabulary would have reported for them anyway.
const (
	csiModShift = 1 << 0
	csiModAlt   = 1 << 1

	// csiCodeEnter is CR, the only CSI-u keycode this package interprets.
	// See [KeyShiftEnter] for why Shift+Enter is the one modifier
	// combination that earns a name.
	csiCodeEnter = 13
)

// csiKey maps a final byte (and, for "~" and "u", its parameters) to this
// package's minimal vocabulary. Modifier parameters are otherwise ignored
// rather than encoded: [Key] carries no modifier field but Alt,
// deliberately.
func csiKey(params []byte, final byte) (Key, bool) {
	switch final {
	case 'A':
		return Key{Name: KeyUp}, true
	case 'B':
		return Key{Name: KeyDown}, true
	case 'C':
		return Key{Name: KeyRight}, true
	case 'D':
		return Key{Name: KeyLeft}, true
	case 'H':
		return Key{Name: KeyHome}, true
	case 'F':
		return Key{Name: KeyEnd}, true
	case '~':
		switch csiParam(params, 0) {
		case 1, 7:
			return Key{Name: KeyHome}, true
		case 3:
			return Key{Name: KeyDelete}, true
		case 4, 8:
			return Key{Name: KeyEnd}, true
		}
	case 'u':
		// The enhanced-keyboard (kitty CSI-u) form, for the one keycode
		// that changes what personant does with it. A terminal that has the
		// protocol enabled sends Alt+Enter this way too, so decoding the
		// Alt bit here is not extra vocabulary — it is what keeps the
		// universal line-break key working on the very terminals that can
		// also send Shift+Enter.
		if csiParam(params, 0) != csiCodeEnter {
			break
		}
		mods := max(csiParam(params, 1)-1, 0)
		if mods&csiModShift != 0 {
			return Key{Name: KeyShiftEnter}, true
		}
		return Key{Name: KeyEnter, Alt: mods&csiModAlt != 0}, true
	}
	return Key{}, false
}

// csiParam reads the i-th semicolon-separated numeric parameter, or 0 when
// there is none. Sub-parameters (the ':' form) are not in this vocabulary
// and read as absent.
func csiParam(params []byte, i int) int {
	n, field := 0, 0
	for _, c := range params {
		switch {
		case c == ';':
			if field == i {
				return n
			}
			field, n = field+1, 0
		case c >= '0' && c <= '9':
			n = n*10 + int(c-'0')
		default:
			return 0
		}
	}
	if field == i {
		return n
	}
	return 0
}

// decodeSS3 interprets "ESC O <final>" — the application-cursor-key form
// some terminals send for the arrows and Home/End.
func decodeSS3(p []byte, expired bool) (Key, int, decodeStatus) {
	if len(p) < 3 {
		if expired {
			return Key{}, len(p), decodeSkip
		}
		return Key{}, 0, decodeIncomplete
	}
	if k, ok := csiKey(nil, p[2]); ok {
		return k, 3, decodeEmit
	}
	return Key{}, 3, decodeSkip // F1-F4 and friends: not in the vocabulary
}

// decodePlain interprets one non-ESC byte or rune.
//
// The three control bytes that have a NAME are matched before the generic
// control range, because Tab is Ctrl-I, Enter is Ctrl-M and Backspace is
// Ctrl-H, and a consumer that had to know that would be a consumer doing
// the decoder's job.
func decodePlain(p []byte, expired bool) (Key, int, decodeStatus) {
	c := p[0]
	switch {
	case c == '\r' || c == '\n':
		return Key{Name: KeyEnter}, 1, decodeEmit
	case c == '\t':
		return Key{Name: KeyTab}, 1, decodeEmit
	case c == 0x7f || c == 0x08:
		return Key{Name: KeyBackspace}, 1, decodeEmit
	case c >= 0x01 && c <= 0x1a:
		// Ctrl-A..Ctrl-Z, delivered as the LOWERCASE letter (see [KeyCtrl]).
		// ISIG is cleared in [ModeSession], so Ctrl-C and Ctrl-Z are here
		// rather than in the signal path.
		return Key{Name: KeyCtrl, Rune: rune('a' + c - 1)}, 1, decodeEmit
	case c < 0x20:
		return Key{}, 1, decodeSkip // NUL and the 0x1c-0x1f separators
	case c < utf8.RuneSelf:
		return Key{Name: KeyRune, Rune: rune(c)}, 1, decodeEmit
	}
	r, size := utf8.DecodeRune(p)
	if r == utf8.RuneError && size <= 1 {
		if len(p) < utf8.UTFMax && !expired {
			return Key{}, 0, decodeIncomplete // a rune split across two reads
		}
		return Key{}, 1, decodeSkip
	}
	return Key{Name: KeyRune, Rune: r}, size, decodeEmit
}
