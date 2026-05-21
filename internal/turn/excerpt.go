package turn

import (
	"fmt"
	"strconv"
	"strings"

	"personant/internal/memops"
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

// padOrTruncateAnchors enforces the §2.2 hard range [4, 8] — see
// memops.MinAnchorsPerThread / memops.MaxAnchorsPerThread. If the input
// has fewer than min entries, append "anchor-<n>" placeholders. If it
// has more than the maximum, take the first MaxAnchorsPerThread.
func padOrTruncateAnchors(in []string, min int) []string {
	out := append([]string(nil), in...)
	if len(out) > memops.MaxAnchorsPerThread {
		out = out[:memops.MaxAnchorsPerThread]
	}
	for i := len(out); i < min; i++ {
		out = append(out, fmt.Sprintf("anchor-%d", i+1))
	}
	return out
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
