package turn

import (
	"strconv"
	"strings"

	"personant/internal/prompt"
)

// turnAnchorList returns the anchor list to print in the per-turn
// excerpt header. Prefers the model's own topic-tag anchors so the
// excerpt header matches the wire format exactly; falls back to the
// per-turn coalesced set when the model omitted a tag.
func turnAnchorList(responseBody string, fallback []string) []string {
	if pr, err := prompt.Parse(responseBody); err == nil {
		return append([]string(nil), pr.Tag.Anchors...)
	}
	return append([]string(nil), fallback...)
}

// renderTurnExcerpt renders a single per-turn excerpt block — the unit
// the new thread format stores as one turns/<n>.md file (spec §2.3 body
// content guidelines):
//
//	## Turn <N> · <RFC3339> · [a, b, c]
//
//	**user:** <userInput>
//
//	**agent:** <responseBody with topic tag stripped>
//
// Exactly one trailing newline is preserved.
func renderTurnExcerpt(turnN int, when string, anchors []string, userInput, responseBody string) string {
	stripped := responseBody
	if pr, err := prompt.Parse(responseBody); err == nil {
		stripped = pr.Body
	}
	stripped = strings.TrimRight(stripped, "\n")
	user := strings.TrimRight(userInput, "\n")

	var b strings.Builder
	b.Grow(len(userInput) + len(responseBody) + 64)
	b.WriteString("## Turn ")
	b.WriteString(strconv.Itoa(turnN))
	b.WriteString(" · ")
	b.WriteString(when)
	b.WriteString(" · [")
	b.WriteString(strings.Join(anchors, ", "))
	b.WriteString("]\n\n")
	b.WriteString("**user:** ")
	b.WriteString(user)
	b.WriteString("\n\n")
	b.WriteString("**agent:** ")
	b.WriteString(stripped)
	b.WriteString("\n")
	return b.String()
}

// descriptionFromNewThread derives a thread's Description (spec §2.3):
// the triggering utterance — the user prompt that spawned the thread.
// v0.1 sets it deterministically (no LLM paraphrase): collapse the
// userInput's whitespace and truncate to the same length bound as the
// summary so the field stays within the §2.2 spine budget. Distinct
// from summarizeForNewThread, which derives from the response body.
func descriptionFromNewThread(userInput string) string {
	const maxLen = 120
	s := strings.Join(strings.Fields(userInput), " ")
	if s == "" {
		return "(new topic)"
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// summarizeForNewThread takes the response body's stripped form (tag
// already removed by the caller-provided string, or the raw body) and
// returns a short summary. v0.1 keeps it crude: first 120 chars,
// collapsed whitespace, ellipsized if truncated. Phase 4 retirement
// will replace this with a curator-drafted summary.
func summarizeForNewThread(body string) string {
	const maxLen = 120
	stripped := body
	if pr, err := prompt.Parse(body); err == nil {
		stripped = pr.Body
	}
	stripped = strings.TrimSpace(stripped)
	stripped = strings.Join(strings.Fields(stripped), " ")
	if stripped == "" {
		return "(new topic)"
	}
	if len(stripped) <= maxLen {
		return stripped
	}
	return stripped[:maxLen-3] + "..."
}
