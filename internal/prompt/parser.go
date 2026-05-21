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
//     memops.ThreadIDPattern or equal to "*new-topic*") and a non-empty
//     anchor list (after normalization, with empty entries dropped).
//   - Subsequent valid tags become a warning naming the count of extras;
//     they are not stripped from the body.
//   - Anchor count outside the spec §2.2 hard range [4, 8]
//     (memops.MinAnchorsPerThread / memops.MaxAnchorsPerThread) emits a
//     warning but does not fail. Spine creation will reject if the count
//     is unrecoverable; the parser's job is to surface it, not gate.
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

		anchors := parseAnchorList(anchorsRaw)
		if len(anchors) == 0 {
			continue
		}

		validCount++
		if chosenIdx == -1 {
			chosen = TopicTag{Threads: threads, Anchors: anchors}
			chosenIdx = i
			if n := len(anchors); n < memops.MinAnchorsPerThread || n > memops.MaxAnchorsPerThread {
				warnings = append(warnings, fmt.Sprintf("anchor count %d outside spec range [%d, %d]", n, memops.MinAnchorsPerThread, memops.MaxAnchorsPerThread))
			}
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
