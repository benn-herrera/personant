package prompt

import (
	"io"
)

// StreamFilter wraps an io.Writer and suppresses the §5.1.2 topic-tag line
// wherever it appears within the bounded leading region of the stream —
// not just on the first line. Real models emit a `<think>…</think>`
// reasoning block, conversational filler, or a code fence before the tag,
// so first-line-only suppression leaks the tag to the terminal once the
// non-tag first line flushes. Detection is via the shared bounded scan
// (PreambleScan): the surrounding preamble text is forwarded, the tag line
// (and its terminating newline) is suppressed.
//
// Suppression is deferred until the scan resolves — a tag line is located,
// or the scan bound (PreambleScanLineCap / PreambleScanByteCap) is reached
// without one, or Close arrives. Until then, bytes accumulate in an
// internal buffer; downstream sees nothing. This bounds the hold: a tag
// streamed verbatim before the runtime can strip it would leak
// `*topic: thr_42 [...]*` to the terminal, and the bound caps how long the
// preamble is held before flushing.
type StreamFilter struct {
	w       io.Writer
	buffer  []byte // pre-resolution buffer
	flushed bool   // true once the tag (if any) has been located and the preamble flushed
}

// NewStreamFilter wraps w. Writes to the filter forward bytes to w except
// for a topic-tag-shaped line within the bounded preamble, which is
// suppressed.
func NewStreamFilter(w io.Writer) *StreamFilter {
	return &StreamFilter{w: w}
}

// Write conforms to io.Writer. Returns n = len(p) on success even when
// bytes were buffered or suppressed (the contract is "bytes accepted,"
// not "bytes written downstream").
func (f *StreamFilter) Write(p []byte) (int, error) {
	if f.flushed {
		if _, err := f.w.Write(p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	f.buffer = append(f.buffer, p...)
	start, end, found, done := PreambleScan(f.buffer)
	if !done {
		// Scan unresolved — more bytes may locate a tag or hit the bound.
		// Hold everything to avoid leaking a tag that has not arrived yet.
		return len(p), nil
	}
	if err := f.resolve(start, end, found); err != nil {
		f.flushed = true
		f.buffer = nil
		return 0, err
	}
	f.flushed = true
	f.buffer = nil
	return len(p), nil
}

// resolve forwards the buffered preamble, suppressing the located tag line
// span (and the single newline that terminates it) when found. When no tag
// was found within the bound, the whole buffer is forwarded unchanged.
func (f *StreamFilter) resolve(start, end int, found bool) error {
	if !found {
		_, err := f.w.Write(f.buffer)
		return err
	}
	// Forward everything before the tag line.
	if start > 0 {
		if _, err := f.w.Write(f.buffer[:start]); err != nil {
			return err
		}
	}
	// Skip the tag span [start:end] and its terminating newline, if present.
	rest := end
	if rest < len(f.buffer) && f.buffer[rest] == '\r' {
		rest++
	}
	if rest < len(f.buffer) && f.buffer[rest] == '\n' {
		rest++
	}
	if rest < len(f.buffer) {
		if _, err := f.w.Write(f.buffer[rest:]); err != nil {
			return err
		}
	}
	return nil
}

// Close flushes any remaining buffered content. If the stream ended before
// the preamble scan resolved (e.g. a single unterminated tag line, or a
// short preamble that never hit the bound), the buffer is classified here:
// a located tag line is suppressed, anything else is flushed. Idempotent.
func (f *StreamFilter) Close() error {
	if f.flushed {
		return nil
	}
	f.flushed = true
	if len(f.buffer) == 0 {
		return nil
	}
	defer func() { f.buffer = nil }()
	// At Close the stream is complete: a tag on the trailing (unterminated)
	// line is now classifiable. Run the bounded scan one final time; treat
	// the buffer as the whole preamble.
	start, end, found, _ := PreambleScan(f.buffer)
	if found {
		return f.resolve(start, end, found)
	}
	// No within-bound tag, or a trailing tag line PreambleScan declined for
	// lack of a terminator — fall back to the single-line check so a
	// newline-less trailing tag is still suppressed.
	if isTopicTagLine(f.buffer) {
		return nil
	}
	_, err := f.w.Write(f.buffer)
	return err
}

// isTopicTagLine returns true when line matches the §5.1.2 topic-tag
// regex. The regex is anchored multi-line in the parser; here we
// pre-strip any trailing CR and apply the same pattern with explicit
// line bounds.
func isTopicTagLine(line []byte) bool {
	// The shared parser regex is multi-line anchored — feed it a single
	// line by stripping a trailing \r if present. The regex enforces
	// `^...$` so this is sufficient.
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return topicTagRE.Match(line)
}
