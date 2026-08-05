package term

import (
	"context"
	"errors"
	"io"
	"testing"
)

// The decoder, driven through the REAL S1→S2 composition — the byte-level
// fake plugged into NewUnixPlatform — rather than against the state
// machine directly. That is the point of the two seams: these are terminal
// byte sequences, arriving in the read-sized chunks a terminal actually
// delivers them in, and the disambiguation that needs a timeout gets one
// as DATA (an idle step) with no clock and no sleep anywhere.

// drain pulls every event the script produces, stopping at end of input.
func drain(t *testing.T, script []scriptStep) []Event {
	t.Helper()
	p := NewUnixPlatform(&fakeDevice{script: script, size: Size{Cols: 80, Rows: 24}})
	var got []Event
	for range 200 { // a bound, so a decoder that never terminates fails here
		ev, err := p.NextEvent(context.Background())
		if errors.Is(err, io.EOF) {
			t.Fatal("the platform reported EOF without emitting EventEOF first")
		}
		if err != nil {
			t.Fatalf("NextEvent: %v", err)
		}
		got = append(got, ev)
		if ev.Kind == EventEOF {
			return got
		}
	}
	t.Fatal("the decoder produced 200 events from a bounded script")
	return nil
}

func key(name KeyName, r rune) Event { return Event{Kind: EventKey, Key: Key{Name: name, Rune: r}} }

func TestDecoder_TerminalByteSequences(t *testing.T) {
	tests := []struct {
		name   string
		script []scriptStep
		want   []Event
	}{
		{
			"printable ascii and enter",
			[]scriptStep{typed("hi\r")},
			[]Event{key(KeyRune, 'h'), key(KeyRune, 'i'), key(KeyEnter, 0)},
		},
		{
			"a multi-byte rune",
			[]scriptStep{typed("é")},
			[]Event{key(KeyRune, 'é')},
		},
		{
			"a rune SPLIT across two reads",
			[]scriptStep{typed("\xc3"), typed("\xa9")},
			[]Event{key(KeyRune, 'é')},
		},
		{
			"the CSI arrows",
			[]scriptStep{typed("\x1b[A\x1b[B\x1b[C\x1b[D")},
			[]Event{key(KeyUp, 0), key(KeyDown, 0), key(KeyRight, 0), key(KeyLeft, 0)},
		},
		{
			"the SS3 arrows, which application-cursor-key mode sends instead",
			[]scriptStep{typed("\x1bOA\x1bOD")},
			[]Event{key(KeyUp, 0), key(KeyLeft, 0)},
		},
		{
			"home and end, both spellings",
			[]scriptStep{typed("\x1b[H\x1b[F\x1b[1~\x1b[4~")},
			[]Event{key(KeyHome, 0), key(KeyEnd, 0), key(KeyHome, 0), key(KeyEnd, 0)},
		},
		{
			"delete, and backspace in both encodings",
			[]scriptStep{typed("\x1b[3~\x7f\x08")},
			[]Event{key(KeyDelete, 0), key(KeyBackspace, 0), key(KeyBackspace, 0)},
		},
		{
			"a modified arrow keeps its base key; the modifier is not in the vocabulary",
			[]scriptStep{typed("\x1b[1;5C")},
			[]Event{key(KeyRight, 0)},
		},
		{
			"a sequence SPLIT across reads is not two keys",
			[]scriptStep{typed("\x1b"), typed("["), typed("A")},
			[]Event{key(KeyUp, 0)},
		},
		{
			"a sequence split with the ESC alone and no expiry between",
			[]scriptStep{typed("\x1b"), typed("[A")},
			[]Event{key(KeyUp, 0)},
		},
		{
			"a BARE Esc, proven by an expired window",
			[]scriptStep{typed("\x1b"), idle()},
			[]Event{key(KeyEsc, 0)},
		},
		{
			"a bare Esc at the very end of the stream",
			[]scriptStep{typed("\x1b")},
			[]Event{key(KeyEsc, 0)},
		},
		{
			"an ESC followed LATER by an ordinary byte is Alt, not Esc-then-byte",
			[]scriptStep{typed("\x1b"), typed("x")},
			[]Event{{Kind: EventKey, Key: Key{Name: KeyRune, Rune: 'x', Alt: true}}},
		},
		{
			"an ESC followed by an expiry and THEN a byte is two keys",
			[]scriptStep{typed("\x1b"), idle(), typed("x")},
			[]Event{key(KeyEsc, 0), key(KeyRune, 'x')},
		},
		{
			"two Escs are two Escs",
			[]scriptStep{typed("\x1b\x1b"), idle()},
			[]Event{key(KeyEsc, 0), key(KeyEsc, 0)},
		},
		{
			"an arrow followed by a bare Esc in one read",
			[]scriptStep{typed("\x1b[A\x1b"), idle()},
			[]Event{key(KeyUp, 0), key(KeyEsc, 0)},
		},
		{
			"a TRUNCATED sequence is abandoned by the expiry, not left to wedge",
			[]scriptStep{typed("\x1b["), idle(), typed("x")},
			[]Event{key(KeyRune, 'x')},
		},
		{
			"an ESC interrupting a sequence restarts it rather than being swallowed",
			[]scriptStep{typed("\x1b[\x1b[A")},
			[]Event{key(KeyUp, 0)},
		},
		{
			// The enhanced-keyboard form. Legacy input cannot express it at
			// all — Enter and Shift+Enter are both 0x0D — so this sequence is
			// the ONLY way the modifier reaches personant, and it arrives
			// only from a terminal configured to send it.
			"CSI-u Shift+Enter",
			[]scriptStep{typed("\x1b[13;2u")},
			[]Event{key(KeyShiftEnter, 0)},
		},
		{
			"CSI-u Enter with no modifier is a plain Enter",
			[]scriptStep{typed("\x1b[13u")},
			[]Event{key(KeyEnter, 0)},
		},
		{
			// A terminal with the protocol enabled sends Alt+Enter this way
			// instead of ESC CR, so the universal line-break key has to
			// survive the very configuration that enables the other one.
			"CSI-u Alt+Enter keeps the Alt form",
			[]scriptStep{typed("\x1b[13;3u")},
			[]Event{{Kind: EventKey, Key: Key{Name: KeyEnter, Alt: true}}},
		},
		{
			"CSI-u Ctrl+Enter falls back to the unmodified key",
			[]scriptStep{typed("\x1b[13;5u")},
			[]Event{key(KeyEnter, 0)},
		},
		{
			"a CSI-u sequence SPLIT across reads is still one key",
			[]scriptStep{typed("\x1b[13"), typed(";2u")},
			[]Event{key(KeyShiftEnter, 0)},
		},
		{
			"a CSI-u keycode outside the vocabulary is dropped, not guessed",
			[]scriptStep{typed("\x1b[97;2ua")},
			[]Event{key(KeyRune, 'a')},
		},
		{
			"Ctrl-C and Ctrl-Z are KEYS, because ModeSession clears ISIG",
			[]scriptStep{typed("\x03\x1a")},
			[]Event{key(KeyCtrl, 'c'), key(KeyCtrl, 'z')},
		},
		{
			"the named control keys are named, not Ctrl-I/M/H",
			[]scriptStep{typed("\t\r\n")},
			[]Event{key(KeyTab, 0), key(KeyEnter, 0), key(KeyEnter, 0)},
		},
		{
			"the emacs editing controls",
			[]scriptStep{typed("\x01\x05\x0b\x15\x17\x04")},
			[]Event{
				key(KeyCtrl, 'a'), key(KeyCtrl, 'e'), key(KeyCtrl, 'k'),
				key(KeyCtrl, 'u'), key(KeyCtrl, 'w'), key(KeyCtrl, 'd'),
			},
		},
		{
			"a function key is decoded and DROPPED — it is not in the vocabulary",
			[]scriptStep{typed("\x1bOP\x1b[15~a")},
			[]Event{key(KeyRune, 'a')},
		},
		{
			"an idle terminal produces no events at all",
			[]scriptStep{idle(), idle(), idle()},
			nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := append(append([]Event(nil), tc.want...), Event{Kind: EventEOF})
			got := drain(t, tc.script)
			if len(got) != len(want) {
				t.Fatalf("decoded %v\nwant %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("event %d = %v, want %v\nfull: %v", i, got[i], want[i], got)
				}
			}
		})
	}
}

// A resize is out of band because SIGWINCH is a signal and never a byte —
// the correction to the "just bytes" view of the seam. It carries the NEW
// geometry, read at the moment it is reported rather than when it is
// consumed.
func TestDecoder_ResizeArrivesOutOfBandWithTheNewGeometry(t *testing.T) {
	d := &fakeDevice{
		size:    Size{Cols: 80, Rows: 24},
		script:  []scriptStep{typed("a"), resized(40, 12), typed("b")},
		resized: make(chan struct{}, 1),
	}
	p := NewUnixPlatform(d)
	var kinds []EventKind
	var size Size
	for range 10 {
		ev, err := p.NextEvent(context.Background())
		if err != nil {
			t.Fatalf("NextEvent: %v", err)
		}
		kinds = append(kinds, ev.Kind)
		if ev.Kind == EventResize {
			size = ev.Size
		}
		if ev.Kind == EventEOF {
			break
		}
	}
	want := []EventKind{EventKey, EventResize, EventKey, EventEOF}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range kinds {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if size != (Size{Cols: 40, Rows: 12}) {
		t.Errorf("resize carried %v, want 40x12", size)
	}
}

// A cancelled context stops the reader between reads. This is what makes
// ReadLine's ctx reach a read ALREADY IN PROGRESS: no read blocks longer
// than one poll window, so cancellation never has to wait for a keystroke.
func TestDecoder_ContextCancellationStopsTheReader(t *testing.T) {
	p := NewUnixPlatform(&fakeDevice{script: []scriptStep{idle(), idle(), idle()}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.NextEvent(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// The plain backend has no keystrokes to decode: off a terminal there are
// LINES, delivered by the buffered reader on Terminal. Saying so with an
// error rather than a stub event is what stops a pump started here from
// quietly eating the stream that reader owns.
func TestPlainPlatform_ProducesNoEventStream(t *testing.T) {
	p := NewPlainPlatform(nil, nil)
	if _, err := p.NextEvent(context.Background()); !errors.Is(err, errNoEventStream) {
		t.Errorf("err = %v, want errNoEventStream", err)
	}
}
