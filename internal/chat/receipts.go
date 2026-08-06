package chat

import (
	"fmt"
	"io"
	"time"

	"personant/internal/term"
	"personant/internal/turn"
)

// §6.1 tool receipts — the on-screen half of turn.ToolReceipt.
//
// One committed dimmed line per executed tool call (user ruling
// 2026-08-05, option B): tool activity is verified by EYES, never by
// asking the model. The model claiming after the fact that it consulted
// an encyclopedia is not evidence of anything, and before this there was
// no evidence either way — the §4.3.2 phase label is ephemeral, and the
// indicator's reveal window suppresses it entirely for a call that
// finishes quickly, which is most of them.
//
// # Channel
//
// term.Reasoning: the §5 (tty-only, scrollback) cell. Dim, persistent,
// and ZERO BYTES when the session is not interactive — so a piped run,
// the sim and the committed goldens are untouched BY CONSTRUCTION rather
// than by a predicate this file maintains.
//
// # Not the /thinking sink
//
// This is a SECOND client of that channel, with its own writer, and it is
// deliberately NOT routed through thinking.go. `/thinking` gates the
// reasoning SINK — whether the model's scratch is offered to the channel
// at all — and a receipt is not scratch: it is the runtime's record of
// something it did. A user who turned reasoning off did not ask to stop
// being told what ran.
//
// Two dim producers need no coordination beyond what term already
// guarantees. term brackets a dim RUN at its single serialization point
// and closes it on the first byte of committed content, so neither
// producer can bleed its attribute into the other's output. What a
// receipt does need is a LINE of its own: reasoning arrives with no line
// structure, so a receipt written into an open run would land on the tail
// of a half-finished thought. Status.Clear is term's one documented way
// to close a run (it retires both transient occupants of the live line),
// so the receipt takes the line, writes, and gives it back.

// toolReceiptPrefix opens every receipt line. Short, unmistakably not
// model output, and greppable in a scrollback buffer.
const toolReceiptPrefix = "[tool] "

// toolReceipts builds the turn.State.OnToolReceipt hook over the
// session's terminal.
//
// The two Clear calls bracket the write and are not symmetric decoration:
// the first closes whatever dim run the model's reasoning left open, so
// the receipt starts on a clean line; the second closes the receipt's own
// run, so the terminal is left undimmed at column zero and whatever comes
// next — another receipt, more reasoning, the answer — starts fresh. Both
// are no-ops when nothing is open, and both write nothing off a terminal.
func toolReceipts(tm *term.Terminal) func(turn.ToolReceipt) {
	w, slot := tm.Reasoning(), tm.Status()
	return func(r turn.ToolReceipt) {
		slot.Clear()
		_, _ = io.WriteString(w, toolReceiptLine(r))
		slot.Clear()
	}
}

// toolReceiptLine renders one receipt:
//
//	[tool] web.wikipedia q="general relativity" → ok, 4.1kB (0.4s)
//	[tool] web.wikipedia q="general relativity" → error: web.wikipedia did not run: … (0.1s)
//
// The runtime supplies the facts and this supplies every word of the
// wording, including the "error:" label — turn.ToolReceipt.Err is the
// discriminator so the front end never has to sniff a string for it.
func toolReceiptLine(r turn.ToolReceipt) string {
	line := toolReceiptPrefix + r.Name
	if r.ArgsGist != "" {
		line += " " + r.ArgsGist
	}
	outcome := r.Outcome
	if r.Err {
		outcome = "error: " + outcome
	}
	return fmt.Sprintf("%s → %s (%s)", line, outcome, formatElapsed(r.Elapsed))
}

// formatElapsed renders a call's duration at the precision a reader can
// use: milliseconds under a second, tenths above it. Duration.String does
// the formatting — the only decision here is where to round, and a
// hand-rolled formatter for that is a second way to print a number.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}
