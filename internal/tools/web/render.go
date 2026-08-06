package web

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"personant/internal/tools"
)

// The ONE result-list rendering for the whole package.
//
// Every query tool here — web.search, web.wikipedia, web.arxiv,
// web.crossref, web.wikidata — hands the model a numbered shortlist of
// {title, URL, a few meta lines, a snippet} and a closing line naming the
// follow-up. Five renderers projecting that same shape would be five
// places for the shape to drift, and a model reading five subtly
// different layouts has to re-learn the format per tool.

// snippetMaxChars bounds one result's snippet. The snippet exists to let
// the model decide WHICH result to fetch; a longer one is the fetch it
// has not decided to make yet.
const snippetMaxChars = 400

// clampLimit resolves a shortlist size for every query tool in the
// package.
//
// Out-of-range is CLAMPED, never an error: a limit is a preference, and
// failing a call over one spends a tool round to learn something the
// clamp already decided. Non-positive means "unspecified" — JSON omission
// and an explicit `0` are indistinguishable in a plain int, and one
// convention beats a *int that exists only to tell two identical
// intentions apart.
func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}

// resultEntry is one rendered hit. Lines are emitted in order, indented
// under the URL, with empties skipped — so a tool composes its own meta
// (authors, venue, identifier, date, description, snippet) without the
// renderer knowing what any of them mean.
type resultEntry struct {
	Title string
	URL   string
	Lines []string
}

// renderList emits the shortlist. header and footer are complete lines
// (the caller owns the wording, because "articles" and "papers" and
// "entities" are not interchangeable); the ENTRY shape is fixed here.
func renderList(header string, entries []resultEntry, footer string) []byte {
	var b strings.Builder
	b.WriteString(header)
	for i, e := range entries {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, tools.OneLine(fallback(e.Title, "(untitled)")), e.URL)
		for _, l := range e.Lines {
			if l = strings.TrimSpace(l); l != "" {
				fmt.Fprintf(&b, "   %s\n", l)
			}
		}
	}
	b.WriteString(footer)
	return []byte(b.String())
}

// snippetLine prepares a free-form field for the shortlist: markup out,
// one line, clipped with an honest ellipsis.
func snippetLine(s string) string {
	return clip(tools.OneLine(stripHTML(s)), snippetMaxChars)
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// clip truncates on a rune boundary with an honest ellipsis.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

// stripHTML reduces an HTML fragment to its text.
//
// The Wikimedia REST APIs wrap matched terms in
// `<span class="searchmatch">…</span>` and escape the surrounding text, so
// an excerpt reaches us as markup even though it is prose. Tokenizing is
// the boring correct way to undo both at once: the tokenizer's text tokens
// are already entity-decoded, so tag removal and unescaping are one pass
// rather than a regex plus a separate html.UnescapeString (which, run in
// the wrong order, happily turns `&lt;script&gt;` back into a tag).
func stripHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			// The only terminal token: io.EOF for a complete fragment, and
			// for a malformed one the text seen so far is still the best
			// answer available.
			return b.String()
		case html.TextToken:
			b.Write(z.Text())
		}
	}
}
