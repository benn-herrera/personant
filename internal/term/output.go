package term

import "io"

// --- Output: the §5 2x2, with one cell that has no name -------------
//
//	                 | scrollback (persists) | ephemeral (erasable)
//	-----------------+-----------------------+---------------------
//	always (TTY+pipe)| Out(), Diag()         | FORBIDDEN
//	tty-only         | Reasoning()           | Status()
//
// The forbidden cell is (always, ephemeral) and it is forbidden for a
// mechanical reason: a pipe has no erasure. There is no method for it,
// no channel enum with a fourth member, and no way to compose one out of
// the three that exist — that is what "inexpressible" means here. Any
// client that believes it needs it wants Out().
//
// The missing (tty-only, scrollback) cell is precisely why thinking.go
// existed and why it ended up the writer for EVERYTHING: it was the only
// object that knew about the reasoning-to-content transition, so every
// other producer had to route through it to be serialized. Naming the
// cell dissolves the object. All four channels share one serialization
// point inside term — Terminal.mu — so the transition is handled once,
// invisibly.
//
// # The one invariant the serialization point maintains
//
// AT MOST ONE of {the status slot is occupied, a reasoning run is open}
// holds at any instant, and neither survives a committed write:
//
//   - Out / Diag  → retire the slot, close the run, then emit.
//   - Reasoning   → retire the slot, open (or continue) the run, emit.
//   - Status.Set  → close the run, then draw the slot.
//   - Status.Clear→ close the run, then erase the slot.
//
// That is the whole of it, and it is why no client needs a flag. Bug 1
// was a client holding two flags that could disagree; there are no
// client flags here to disagree with each other, and the arbiter's own
// two are mutually exclusive by construction.
//
// # W3 adds a third transient occupant, and it wins
//
// The EDITOR's block is on the live line while a read is in flight, and it
// is repainted RELATIVE to where term believes it starts. So:
//
//   - While an editor is on the terminal the ephemeral slot is SUSPENDED:
//     [Status.Set] writes nothing and records nothing. A frame drawn over
//     a line being typed is a frame the editor's next repaint has to
//     account for, and it cannot — the frame may have taken a newline
//     first. Suspending is one rule; reconciling two owners of one line is
//     the defect this package exists to remove.
//   - Every COMMITTED write invalidates the block. The rows the editor
//     drew are no longer the rows above the cursor, so the next repaint
//     starts a fresh block below rather than moving up over content that
//     is not its to take back.

// The EMITTED ALPHABET, in full. internal/term/screentest models exactly
// these and FAILS on anything else, so the commit that teaches term a new
// sequence is the commit that teaches the model to interpret it — or it
// does not go green. Keep this list short and keep it here.
//
// There is no absolute positioning, no alternate screen, no scroll region
// and no damage model: personant is a scrollback CLI (axiom §4.1), and
// every sequence below is RELATIVE, so nothing term draws can reach above
// the rows term itself put on the screen.
const (
	// eraseLine returns the cursor to column 0 and clears to end of line.
	// The ephemeral status slot's whole vocabulary.
	eraseLine = "\r\x1b[K"

	// The editor's three, added in W3. A repaint is: back to the block's
	// first row (cursorUp), clear everything from there down (eraseDown),
	// rewrite the rows, then walk the cursor to its cell (cursorUp again,
	// then cursorRight from column 0).
	//
	// eraseDown rather than a per-row eraseLine because the block is
	// multi-row and the rows below the cursor must go too — and because one
	// sequence per repaint is one sequence the model has to agree about
	// instead of one per row.
	eraseDown   = "\x1b[J"
	cursorUp    = "\x1b[%dA"
	cursorRight = "\x1b[%dC"

	// dimOn / dimOff bracket reasoning output in ANSI SGR faint. Faint
	// (2) rather than a colour: it degrades sanely on terminals that
	// render it as normal text, and it makes no assumption about the
	// user's palette. Reset (0) rather than 22 for the same reason — 22
	// is the exact undo, but 0 is the sequence every terminal implements.
	dimOn  = "\x1b[2m"
	dimOff = "\x1b[0m"

	// reasoningOpenMark / reasoningCloseMark are the no-ANSI fallback. On
	// a dumb terminal dimming is unavailable, so the distinction has to
	// be carried by words: reasoning that could be mistaken for the
	// answer is the one failure this channel must not have.
	reasoningOpenMark  = "[thinking] "
	reasoningCloseMark = " [/thinking]"
)

// ansiCapable reports whether TERM names a terminal that can handle the
// erase and SGR sequences above. "dumb" and an unset TERM cannot;
// anything else is assumed to.
func ansiCapable(termEnv string) bool { return termEnv != "" && termEnv != "dumb" }

// channel identifies one of the three writer channels. One type with
// three values rather than three writer implementations: the channels
// differ only in their destination fd and in what the serialization
// point does before the bytes land, and two functions projecting the
// same shape is the smell this project names explicitly.
type channel int

const (
	chanOut channel = iota
	chanDiag
	chanReasoning
)

// channelWriter is the io.Writer face of one channel. It holds no state
// of its own — all of it is the arbiter's, under the arbiter's lock.
type channelWriter struct {
	t  *Terminal
	ch channel
}

func (w *channelWriter) Write(b []byte) (int, error) { return w.t.write(w.ch, b) }

// Out is the committed content channel: the streamed response body, menu
// and question text, banners, ordinary notices. Present on a TTY and in
// a pipe alike, and byte-identical between them — the sim and scenario
// goldens are the gate on that.
//
// A write to Out first retires whatever ephemeral decoration is on
// screen (the status slot) and closes any open reasoning run, so content
// can never arrive dim or jammed onto the tail of a scratch line.
//
// Body text passes through UNWRAPPED (user ruling, 2026-07-31): term
// does not re-flow the streamed response, and the terminal's own soft
// wrap does the work. In-app wrapping would insert real newlines that
// survive copy-paste, `tee` and every downstream reader, which is
// exactly what the §4.1 scrollback-fidelity bar forbids — what is on
// screen must be what the user gets when they take it away. This is a
// starting position, not a closed question: if a case for wrapping here
// appears, it is a ruling to revisit, not a rule of the architecture.
//
// It says nothing about the other two places term does width math.
// [Status] truncation and the editor's wrap are term's business by
// construction: both write to a region term must be able to erase or
// repaint, so both need the width and neither commits a newline to
// scrollback.
func (t *Terminal) Out() io.Writer { return t.outW }

// Diag is the committed diagnostic channel — errors and warnings. It
// lands on stderr, but it is INSIDE the arbiter (§5, U2): it passes
// through the same serialization point as Out, which is what stops an
// error mid-stream from interleaving into the status line. stderr
// escaping the arbiter is the collision W1 closes.
//
// Diag is (always, scrollback), not ephemeral: an error the user did not
// see is worse than an error that stayed on screen.
func (t *Terminal) Diag() io.Writer { return t.diagW }

// Reasoning is the live model-reasoning channel: dimmed, tty-only, and
// PERSISTENT in scrollback (user ruling, 2026-07-31 — arbitration item 1;
// the brief's "or scrollback" phrasing was an error). It is a channel,
// NOT a bounded rewriteable region: dimmed reasoning stays in scrollback
// exactly as it does today and survives scrolling back.
//
// tty-only means it writes ZERO bytes when [Terminal.Interactive] is
// false, so piped, sim and test output are unaffected regardless of the
// /thinking setting. Axiom §4.5 — reasoning is model scratch and never
// enters memory — is unaffected by this package: there is no path from
// here to storage, and none may be created.
//
// Interleaving with Out is term's problem, not the caller's: an open
// reasoning run is closed (dim reset, newline) by the first byte written
// to Out or Diag, and by either [Status] operation. The caller has no
// "end the run" call to forget, because there is nothing for it to
// forget — every other way of touching the terminal closes it.
func (t *Terminal) Reasoning() io.Writer { return t.reasonW }

// write is THE serialization point. Every byte this package emits on a
// client's behalf goes through here, under one lock, which is what makes
// "an error mid-stream cannot interleave into the status line" a
// property of the code rather than a convention between writers.
func (t *Terminal) write(ch channel, b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Committed content ends the editor's claim on its block, whichever
	// channel carried it: on a terminal both fds are the same tty, and a
	// Diag line pushes the editor's rows up exactly as an Out line does.
	t.forgetEditorBlockLocked()

	if ch == chanReasoning {
		if !t.interactive {
			// The tty-only half of the 2x2. Reporting the bytes as
			// written is not a lie about the terminal — it is the
			// io.Writer contract: a short write is an error, and there
			// is no error here, there is simply nowhere for reasoning to
			// go.
			return len(b), nil
		}
		t.retireStatusLocked()
		t.openReasoningLocked()
		return t.emitLocked(ch, b)
	}

	t.retireStatusLocked()
	t.endReasoningLocked()
	return t.emitLocked(ch, b)
}

// emitLocked writes payload bytes and updates the cursor-column belief
// from what they contain. Callers hold t.mu.
func (t *Terminal) emitLocked(ch channel, b []byte) (int, error) {
	var (
		n   int
		err error
	)
	if ch == chanDiag {
		n, err = t.diag.Write(b)
	} else {
		n, err = t.plat.Write(b)
	}
	if n > 0 {
		if b[n-1] == '\n' {
			t.column = ColumnStart
		} else {
			t.column = ColumnMid
		}
	}
	return n, err
}

// emitControlLocked writes decoration — an escape sequence or a marker —
// and deliberately does NOT infer a cursor column from it. A column
// belief derived from the last byte of "\x1b[2m" would say "mid-line"
// because the byte is 'm'. Every caller of this sets t.column itself, or
// knows the decoration did not move the cursor.
//
// Errors are dropped, as they were in progress.go and thinking.go before
// this: this is decoration around a stream whose real payload reports its
// own errors, and there is no per-byte recovery for a terminal that
// stopped accepting bytes.
func (t *Terminal) emitControlLocked(s string) {
	_, _ = io.WriteString(t.plat, s)
}

// openReasoningLocked starts a reasoning run if one is not already open.
// Deltas arrive with no line structure of their own, so the run is
// BRACKETED rather than prefixed per line — a per-line prefix would need
// lookahead the stream does not offer.
func (t *Terminal) openReasoningLocked() {
	if t.reasoningOpen {
		return
	}
	t.reasoningOpen = true
	if t.ansi {
		t.emitControlLocked(dimOn)
		return
	}
	t.emitControlLocked(reasoningOpenMark)
}

// endReasoningLocked closes an open run and returns the terminal to a
// clean, undimmed line. Idempotent, and a no-op when no run is open.
//
// The trailing newline is unconditional: the answer must start on its
// own line, never on the tail of the scratch. The dim reset alone would
// leave the two sharing a row.
func (t *Terminal) endReasoningLocked() {
	if !t.reasoningOpen {
		return
	}
	t.reasoningOpen = false
	if t.ansi {
		t.emitControlLocked(dimOff)
	} else {
		t.emitControlLocked(reasoningCloseMark)
	}
	t.emitControlLocked("\n")
	t.column = ColumnStart
}

// --- The ephemeral slot ----------------------------------------------

// Status returns the session's single ephemeral slot — the phase-labeled
// progress indicator's home.
//
// It is TOKEN-LESS by design (§5, retired contention C13): every caller
// gets the same slot and there is no handle proving ownership, so two
// producers cannot each believe they own a line. That belief was bug 1.
// The slot holds one line, replaced whole; it is not an io.Writer,
// because append semantics on an erasable region is the thing that made
// the old indicator track its own previous frame.
//
// On a non-interactive session the slot is inert and writes nothing.
func (t *Terminal) Status() *Status { return t.status }

// Status is the ephemeral single-line slot. See [Terminal.Status].
//
// Literally one object per session: [Terminal.Status] hands out the same
// pointer to every caller, which is the token-less design made concrete.
type Status struct{ t *Terminal }

// Set replaces the slot's contents and redraws it in place. The text is
// truncated to Size().Cols-[StatusColumnInset]; see that constant for
// why the final column is left alone. An empty text is [Status.Clear].
//
// Set never draws OVER committed content. If the cursor is mid-line and
// the slot is not already what is on that line, a newline is taken
// first; if the column is [ColumnUnknown] — a child owned the fd — the
// same newline is the deterministic answer rather than a guess. Which of
// those two cases holds is term's own bookkeeping, not a flag a client
// maintains, and that is the structural end of bug 1: the old indicator
// asked "is this line mine?" of a variable it set itself.
//
// # No-ANSI degradation
//
// On a terminal that cannot erase, an ephemeral slot cannot exist. The
// honest degradation is one COMMITTED line per occupancy: the first Set
// after a [Status.Clear] prints its line, and further Sets are dropped
// until the slot is released again. That keeps the animated caller's
// frames — which change on every tick — from becoming one line per tick,
// without the caller having to know which kind of terminal it is on.
// # The editor holds the line
//
// While a read is in flight the slot is SUSPENDED: this writes nothing and
// records nothing, so [StateSnapshot.Status] stays empty and an animated
// caller's heartbeat rule ("only redraw a frame that is still on screen")
// keeps it quiet without the caller knowing a prompt is up. The next
// announcement after the read completes draws normally. See the third
// transient occupant at the top of this file.
func (s *Status) Set(text string) {
	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.interactive || t.ed != nil {
		return
	}
	t.endReasoningLocked()
	text = truncateStatus(text, t.sizeLocked().Cols)
	if text == "" {
		t.retireStatusLocked()
		return
	}
	if !t.ansi {
		if t.statusLive {
			return
		}
		if t.column != ColumnStart {
			t.emitControlLocked("\n")
		}
		t.emitControlLocked(text + "\n")
		t.column = ColumnStart
		t.statusLive, t.statusText = true, text
		return
	}
	if !t.statusLive && t.column != ColumnStart {
		t.emitControlLocked("\n")
	}
	t.emitControlLocked(eraseLine + text)
	t.column = ColumnMid
	t.statusLive, t.statusText = true, text
}

// Clear erases the slot and gives the line back. Idempotent, and safe
// from any goroutine including a signal path, where a half-drawn frame
// would otherwise be the last thing on the terminal.
//
// It also closes an open reasoning run. That is not a side errand: the
// slot and the run are the two transient occupants of the live line (see
// the invariant at the top of this file), and "the transient decoration
// is retired" has to mean both or it means nothing. It is also what
// gives a caller whose turn produced reasoning and no body a
// deterministic way to leave the terminal undimmed, without a second
// end-the-run API to keep in sync.
func (s *Status) Clear() {
	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.interactive {
		return
	}
	t.endReasoningLocked()
	t.retireStatusLocked()
}

// retireStatusLocked takes the slot off the screen. On ANSI that is an
// erase and the line comes back; with no ANSI the line was committed and
// stays, so only the bookkeeping is released. Callers hold t.mu.
func (t *Terminal) retireStatusLocked() {
	if !t.statusLive {
		return
	}
	if t.ansi {
		t.emitControlLocked(eraseLine)
		t.column = ColumnStart
	}
	t.statusLive, t.statusText = false, ""
}

// sizeLocked is [Terminal.Size] from inside the lock. The platform's
// size call does not re-enter term, so this is safe; it is a separate
// name only to make the locking discipline legible at the call site.
func (t *Terminal) sizeLocked() Size {
	if s, err := t.plat.Size(); err == nil && s.Cols > 0 && s.Rows > 0 {
		return s
	}
	return Size{Cols: DefaultCols, Rows: DefaultRows}
}

// truncateStatus cuts text to the slot's writable width.
func truncateStatus(text string, cols int) string {
	return truncateCells(text, cols-StatusColumnInset)
}

// truncateCells cuts text to max terminal cells. Runes, not bytes: a
// multi-byte glyph cut in half is worse than a line one column short.
//
// Shared by the status slot and by the editor's row-cap placeholder, which
// need the same thing for the same reason — both write a line that must
// not reach the final column and turn a redraw into a scroll.
func truncateCells(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(text) <= max { // fast path: ASCII, the common case
		return text
	}
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max])
}
