package term

// The decoded input vocabulary.
//
// It is deliberately MINIMAL: no key-up, no key-repeat count, no mouse,
// no left/right modifier distinction, no bracketed paste. Personant is a
// scrollback CLI, not a full-screen application (axiom §4.1), and every
// event kind added here is one more thing two backends must agree on and
// one more branch in every consumer. The write side stays BYTES at both
// seams — there is no cell model, no damage tracking, and no renderer.
//
// This vocabulary lives at S2, the platform seam (§7). A Windows
// ReadConsoleInput backend produces these directly from an INPUT_RECORD;
// the unix backend produces them by decoding bytes from S1.

// EventKind discriminates [Event].
type EventKind int

const (
	// EventKey is a keystroke. Event.Key carries it.
	EventKey EventKind = iota

	// EventResize is a window-size change. Event.Size carries the new
	// geometry.
	//
	// Resize is an event here even on unix, where it arrives as SIGWINCH
	// rather than as bytes. That is the correction to the "just bytes"
	// view of the seam: a byte-level reader can never see it, so the
	// device seam has always needed an out-of-band size channel and the
	// platform seam folds it into the one stream the pump consumes.
	EventResize

	// EventEOF is end of input — the stream closed. Consumers see it
	// once; the pump stops after emitting it.
	EventEOF
)

// Event is one decoded input event.
type Event struct {
	Kind EventKind
	Key  Key
	Size Size
}

// KeyName names a key that is not a printable character. [KeyRune] means
// the character itself is in Key.Rune.
type KeyName uint8

const (
	// KeyRune means Key.Rune carries a printable character.
	KeyRune KeyName = iota

	// KeyCtrl means Key.Rune carries the control letter in LOWERCASE:
	// Ctrl-A arrives as {Name: KeyCtrl, Rune: 'a'}, not as 0x01.
	// Decoding it here rather than at each consumer is the point of
	// having a decoder — a consumer that switches on 0x03 is a consumer
	// that has to know the encoding.
	//
	// Because [ModeSession] clears ISIG, this includes Ctrl-C: it is a
	// key here, not a signal, whenever term owns the fd.
	//
	// Ctrl-Z is DECODED here and never DELIVERED. The distinction is not
	// pedantry — it is where the seam falls. This vocabulary is S2, and a
	// Windows ReadConsoleInput backend produces the same event from its own
	// INPUT_RECORD, so a decoder that dropped 0x1A would be hiding a fact
	// the platform genuinely reported. What consumes it is the PUMP, one
	// layer above: term owns suspend and exposes no hook for it, because no
	// client needs to observe one and none has standing to veto one (see
	// [Open]). So {KeyCtrl, 'z'} is a legal event that no [Handler.Key]
	// will ever be offered.
	KeyCtrl

	KeyEnter

	// KeyShiftEnter is Shift+Enter, and it exists only where the terminal
	// can SAY so.
	//
	// Legacy terminal input encodes Shift+Enter and Enter as the SAME byte
	// (0x0D). The modifier is visible only under an enhanced keyboard
	// protocol, and the one decoded here is the kitty CSI-u form,
	// "ESC [ 13 ; 2 u". Personant does not REQUEST that protocol: a
	// protocol push/pop is a new piece of mode-ownership surface, and this
	// package exists because mode ownership was scattered. (Requesting it —
	// under the same single owner as every other mode change — is a
	// plausible future item, not a gap.) So this key arrives only from a
	// terminal already configured to send it, and the UNIVERSAL way to type
	// a line break is Alt/Option+Enter, which every terminal encodes as
	// ESC CR and which arrives here as {Name: KeyEnter, Alt: true}.
	//
	// It is a NAME rather than a Shift field on [Key], and the choice is
	// the same one Alt's doc argues from the other side. Alt is a field
	// because the terminal reports it as a PREFIX on any key. Shift is not
	// reported at all in this vocabulary — csiKey discards the modifier
	// parameter on the arrows and on Home/End deliberately — so a Shift
	// bool would be a dimension that is false for every key ever
	// constructed but one, while advertising a capability the decoder does
	// not have. One more name costs each consumer an ignorable case; one
	// more field costs every consumer a question to answer for every key.
	KeyShiftEnter

	KeyEsc
	KeyTab
	KeyBackspace
	KeyDelete
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
)

// Key is one keystroke.
//
// A BARE Esc arrives as {Name: KeyEsc}. Disambiguating it from the ESC
// that introduces an arrow key is the decoder's job and nobody else's:
// it requires holding bytes read-but-not-yet-interpreted across a
// timeout, inside the pump, where the lookahead cannot be stranded by a
// handover (§3). Before W3 that disambiguation lived in chat's escwatch.go
// while liner held three separate byte reservoirs over the same fd, so an
// Esc typed as type-ahead was silently never seen.
type Key struct {
	Name KeyName
	Rune rune
	// Alt reports the ESC-prefixed (Meta) form. Ctrl is carried by
	// [KeyCtrl] rather than as a second boolean, because that is how the
	// terminal actually delivers it and inventing a symmetry here would
	// mean encoding, then decoding, the same fact.
	Alt bool
}

// Size is a window geometry in character cells.
type Size struct {
	Cols int
	Rows int
}
