package turn

import (
	"strings"

	"personant/internal/memops"
)

// display.go holds the ONE §2.2.2 display-form derivation the turn package
// hands to the experience layer. Two surfaces need it — the §3.4 recall
// offer (describeCandidates) and the §3.5 closure offer/notice — and a
// second copy of the chain is exactly how one of them quietly starts
// showing a bare thr_N again.
//
// The runtime resolves the two fields; the front end renders them. Nothing
// here formats: no separators, no parentheses, no id decoration. That is
// the front end's wording, and it stays there.

// gistAnchors bounds the anchor fallback in a gist. Enough to identify the
// thread, short enough to stay one readable line beside a display name.
const gistAnchors = 4

// threadDisplay is the thread's working name: the spine Description (the
// utterance that spawned it, set once at creation and never rewritten),
// else its Summary, else — last resort — its id.
func threadDisplay(rec memops.SpineRecord) string {
	switch {
	case rec.Description != "":
		return rec.Description
	case rec.Summary != "":
		return rec.Summary
	default:
		return rec.ID
	}
}

// threadGist is what the thread is about, in one line: the curator's
// Summary, else the projected anchors. It returns "" when the record
// carries neither — a caller that needs a non-empty gist supplies its own
// last resort, because the right filler ("no summary yet", or the id, or
// nothing at all) is a rendering decision and belongs to the caller.
//
// A Summary that is ALSO serving as the display name is skipped: /topic
// sets Description and Summary to the same given name, and "knot theory —
// knot theory" describes nothing.
func threadGist(rec memops.SpineRecord) string {
	if rec.Summary != "" && rec.Summary != threadDisplay(rec) {
		return rec.Summary
	}
	if len(rec.Anchors) > 0 {
		return strings.Join(rec.Anchors[:min(len(rec.Anchors), gistAnchors)], ", ")
	}
	return ""
}
