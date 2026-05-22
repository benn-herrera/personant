// Package prompt assembles the system prompt and parses the model's topic
// tag from each response. The package is pure logic — no I/O, no LLM calls.
//
// The two responsibilities are intentionally co-located: the prompt
// instructs the model on the tag format, and the parser is the
// deterministic counterpart that reads it back. Drift between the two would
// break the engagement-update path (spec §3.2), so they live next to each
// other and are exercised by the same test suite.
package prompt

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"personant/internal/memops"
)

// ErrNoTopicTag is returned by Parse when no valid topic tag is present in
// the model response. The caller (turn loop) is expected to warn-log and
// continue without an engagement update — see spec §5.1.2.
var ErrNoTopicTag = errors.New("prompt: no valid topic tag found")

// topicTagRE is the spec §5.1.2 regex, multi-line so each line is anchored
// independently. Group 1 is the thread list (before `[`); group 2 is the
// anchor list (between `[` and `]`).
var topicTagRE = regexp.MustCompile(`(?m)^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$`)

// PreambleScanLineCap bounds how many leading lines of a streaming model
// response are scanned for a topic tag before giving up. Real models emit
// a `<think>…</think>` reasoning block, conversational filler, or a code
// fence before the §5.1.2 tag, so first-line-only detection (the mock's
// shape) misses the tag entirely. Scanning is bounded — not the whole
// response — so streaming latency stays bounded: until the scan resolves,
// the stream filter must hold its buffer rather than forward bytes, and
// the §5.5 mid-turn fetch decision must wait. PreambleScanLineCap lines or
// PreambleScanByteCap bytes (whichever comes first) is the worst-case
// hold before bytes flow to the terminal. A reasoning block plus a short
// preamble fits comfortably inside this bound; a response that buries its
// tag deeper is treated as tag-less (same as today's no-tag path).
const PreambleScanLineCap = 64

// PreambleScanByteCap is the byte ceiling on the preamble scan, a second
// bound alongside PreambleScanLineCap so a single pathological long line
// (no newlines) cannot defer the flush indefinitely. ~8 KB comfortably
// holds a reasoning block; past it the preamble is treated as tag-less.
const PreambleScanByteCap = 8 << 10

// PreambleScan looks for the first §5.1.2 topic-tag line within the
// bounded leading region of buf (the accumulated start of a streaming
// response). It is the streaming counterpart to Parse: Parse classifies a
// complete response; PreambleScan answers, incrementally, whether enough
// of the response has arrived to locate (or rule out) a leading tag.
//
// Returns:
//   - found == true: buf[start:end] is the tag line's byte span (NOT
//     including a trailing newline). The caller suppresses that span and
//     forwards the surrounding preamble.
//   - found == false, done == true: the scan bound was reached (line cap,
//     byte cap) without a tag — buf has no leading tag, treat as tag-less.
//   - found == false, done == false: undecided — more bytes may yield a
//     tag or hit the bound; the caller should accumulate and re-scan.
//
// A "complete" tag requires its terminating newline to be present in buf
// (so a partially-streamed final line is not misclassified); the one
// exception is when the byte/line bound is already exceeded, at which
// point the trailing partial line cannot be a within-bound tag anyway.
func PreambleScan(buf []byte) (start, end int, found, done bool) {
	limit := preambleScanLimit(buf)
	if loc := topicTagRE.FindIndex(buf); loc != nil && loc[0] < limit {
		// FindIndex's match may swallow the trailing newline (the regex
		// ends with `\s*$` in multi-line mode). Trim it back so the
		// reported span is the tag text alone; the caller decides what to
		// do with the newline.
		s, e := loc[0], loc[1]
		for e > s && (buf[e-1] == '\n' || buf[e-1] == '\r') {
			e--
		}
		// Require the tag line to be terminated (newline present after it)
		// OR the scan to already be bound-exhausted, so a still-streaming
		// final line is not classified prematurely.
		terminated := e < len(buf) && (buf[e] == '\n' || buf[e] == '\r')
		if terminated || limit < len(buf) {
			return s, e, true, true
		}
	}
	if limit < len(buf) {
		// The bound falls within buf, and no in-bound tag was located →
		// no leading tag will ever appear; the scan is resolved.
		return 0, 0, false, true
	}
	return 0, 0, false, false
}

// preambleScanLimit returns the exclusive byte offset past which a topic
// tag is too deep to count as a leading tag — the smaller of the byte cap
// and the offset of the (PreambleScanLineCap)-th newline. A tag must start
// before this offset to be a within-bound leading tag. When the bound has
// not yet been reached within buf, the returned limit equals len(buf), so
// callers can detect "still room — keep scanning" via limit == len(buf).
func preambleScanLimit(buf []byte) int {
	limit := len(buf)
	if PreambleScanByteCap < limit {
		limit = PreambleScanByteCap
	}
	// Find the offset just past the PreambleScanLineCap-th newline.
	nl := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == '\n' {
			nl++
			if nl == PreambleScanLineCap {
				if i+1 < limit {
					limit = i + 1
				}
				break
			}
		}
	}
	return limit
}

// NewTopicLiteral is the literal token a model emits to indicate a new
// thread should be created (spec §5.1.1). It is matched as a whole token
// after thread-list comma-splitting. Exported as the single source of
// truth for the wire-protocol sentinel: callers across the runtime
// (turn dispatch, mock LLM, scenario sim, prompt template) reference
// this constant instead of duplicating the literal — a typo in any
// consumer would otherwise silently break the new-topic protocol with
// no compile-time signal.
const NewTopicLiteral = "*new-topic*"

// TopicTag is the parsed form of a single topic-tag line. Threads contains
// either canonical "thr_<n>" identifiers (matching memops.ThreadIDPattern)
// or the literal "*new-topic*". Anchors are normalized via
// memops.Normalize(_, memops.SymbolEntity).
type TopicTag struct {
	Threads []string
	Anchors []string
}

// ParseResult bundles the chosen tag, the response body with that tag
// stripped, and any non-fatal warnings the caller may want to surface.
type ParseResult struct {
	Tag      TopicTag
	Body     string
	Warnings []string
}

// Parse extracts the topic tag from a model response.
//
// Behavior (spec §5.1.2):
//
//   - The first fully valid tag in document order wins.
//   - A "valid" tag has a non-empty thread list (each entry either matching
//     memops.ThreadIDPattern or equal to "*new-topic*"). The anchor list
//     is advisory and MAY be empty: the tag's job is thread binding;
//     anchors are the model's per-turn symbol contribution (spec §5.1 /
//     §2.7.x). A tag whose anchor list normalizes to empty (0 anchors —
//     a vague-start emission) is still a valid tag, not ErrNoTopicTag.
//   - Subsequent valid tags become a warning naming the count of extras;
//     they are not stripped from the body.
//   - The anchor count is never gated. The §2.2 anchor projection owns
//     the AnchorProjectionMax ceiling deterministically; the parser does
//     not warn or fail on cardinality.
//   - The chosen tag's exact line (and its trailing newline, if present)
//     is removed from Body. Other whitespace is preserved.
//   - If no valid tag is found, returns ErrNoTopicTag with Body equal to
//     the unmodified response.
func Parse(response string) (ParseResult, error) {
	matches := topicTagRE.FindAllStringSubmatchIndex(response, -1)
	if len(matches) == 0 {
		return ParseResult{Body: response}, ErrNoTopicTag
	}

	var (
		chosen     TopicTag
		chosenIdx  = -1
		validCount int
		warnings   []string
	)

	for i, m := range matches {
		// m is [matchStart, matchEnd, group1Start, group1End, group2Start, group2End].
		threadsRaw := response[m[2]:m[3]]
		anchorsRaw := response[m[4]:m[5]]

		threads, ok := parseThreadList(threadsRaw)
		if !ok {
			continue
		}

		// R1 reconciliation (anchor-lifecycle Inc 1): an anchor list that
		// normalizes to empty is NOT a tag-validity failure. 0 anchors is
		// legal (a vague-start emission, spec §5.1 / §2.7.x) — the tag's
		// job is thread binding, which the valid thread list above already
		// satisfies. Anchors are advisory, so an empty list yields a valid
		// tag rather than falling through to ErrNoTopicTag.
		anchors := parseAnchorList(anchorsRaw)

		validCount++
		if chosenIdx == -1 {
			chosen = TopicTag{Threads: threads, Anchors: anchors}
			chosenIdx = i
		}
	}

	if chosenIdx == -1 {
		return ParseResult{Body: response}, ErrNoTopicTag
	}

	if extras := validCount - 1; extras > 0 {
		warnings = append(warnings, fmt.Sprintf("additional topic tag(s) ignored: %d", extras))
	}

	body := stripTagLine(response, matches[chosenIdx][0], matches[chosenIdx][1])

	return ParseResult{
		Tag:      chosen,
		Body:     body,
		Warnings: warnings,
	}, nil
}

// parseThreadList splits and validates the thread-list group. Returns the
// validated entries and whether every entry passed validation; a single
// failed entry rejects the whole tag (per spec §5.1.2 the thread list must
// match `thr_\d+|\*new-topic\*`).
func parseThreadList(raw string) ([]string, bool) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			// An empty entry (e.g. trailing comma) fails the §5.1.2 alternation.
			return nil, false
		}
		if t == NewTopicLiteral || memops.ThreadIDPattern.MatchString(t) {
			out = append(out, t)
			continue
		}
		return nil, false
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// parseAnchorList splits, trims, normalizes, and drops empties. An anchor
// that normalizes to the empty string (e.g. all-punctuation input) is
// dropped silently — its presence in the input was a model error, not a
// parser-level violation.
func parseAnchorList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		n := memops.Normalize(t, memops.SymbolEntity)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// stripTagLine removes the matched tag from the response, including its
// trailing newline if one is present. Other whitespace inside the response
// is preserved verbatim — the parser does not normalize the body.
//
// Note: the spec §5.1.2 regex ends with `\s*$` in multi-line mode, so the
// engine may already consume the line-terminator newline as part of the
// match. Advancing past one further newline is conditional: only when the
// match did not already include it.
func stripTagLine(response string, start, end int) string {
	matchAlreadyAteNewline := end > 0 && response[end-1] == '\n'
	if !matchAlreadyAteNewline && end < len(response) && response[end] == '\n' {
		end++
	}
	return response[:start] + response[end:]
}
