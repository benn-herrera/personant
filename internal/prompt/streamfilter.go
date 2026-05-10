package prompt

import (
	"bytes"
	"io"
)

// StreamFilter wraps an io.Writer and suppresses a topic-tag-shaped first
// line. Subsequent writes pass through unchanged. Buffered content is
// flushed once the first complete line is observed and classified.
//
// The classification is deferred until the first newline is seen (or
// Close, whichever comes first). Until then, bytes accumulate in an
// internal buffer; downstream sees nothing. This is correct behavior:
// streaming a topic tag verbatim to the user before the runtime can
// strip it would briefly leak `*topic: thr_42 [...]*` to the terminal.
type StreamFilter struct {
	w       io.Writer
	buffer  []byte // pre-classification buffer
	flushed bool   // true once we have decided what to do with the first line
}

// NewStreamFilter wraps w. Writes to the filter forward bytes to w except
// for a topic-tag-shaped first line, which is suppressed.
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
	// Append, then look for the first newline that terminates the
	// first line. Any data after that newline passes through.
	f.buffer = append(f.buffer, p...)
	nl := bytes.IndexByte(f.buffer, '\n')
	if nl < 0 {
		// First line not yet complete. Hold everything.
		return len(p), nil
	}
	first := f.buffer[:nl]
	rest := f.buffer[nl+1:]
	if isTopicTagLine(first) {
		// Suppress the tag and its terminating newline; forward whatever
		// followed the newline.
		if len(rest) > 0 {
			if _, err := f.w.Write(rest); err != nil {
				f.flushed = true
				f.buffer = nil
				return 0, err
			}
		}
	} else {
		// Not a tag — forward the whole buffered content unchanged.
		if _, err := f.w.Write(f.buffer); err != nil {
			f.flushed = true
			f.buffer = nil
			return 0, err
		}
	}
	f.flushed = true
	f.buffer = nil
	return len(p), nil
}

// Close flushes any remaining buffered content. If the entire stream was
// a single line and it didn't match the topic-tag regex, that line is
// flushed to the underlying writer here. Idempotent.
func (f *StreamFilter) Close() error {
	if f.flushed {
		return nil
	}
	f.flushed = true
	if len(f.buffer) == 0 {
		return nil
	}
	defer func() { f.buffer = nil }()
	if isTopicTagLine(f.buffer) {
		// Single-line topic tag with no trailing newline — suppress.
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
