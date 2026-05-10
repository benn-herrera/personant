package turn

import (
	"regexp"
	"strings"

	"personant/internal/store"
)

// urlRE matches an http(s) URL. The terminating character class is
// permissive — surrounding punctuation that often hugs URLs in prose
// (`,`, `.`, `;`, `:`, `!`, `?`, `'`, `"`) is kept on the match here
// and trimmed below; explicit closers `)` and `]` are excluded so a
// parenthesized URL like `(see https://x/y)` doesn't capture the
// closing paren.
var urlRE = regexp.MustCompile(`https?://[^\s)\]]+`)

// filePathRE matches tokens that look like file paths and end in one
// of the curated extensions below. Two accept shapes per §3.3:
//
//   - any non-whitespace token containing at least one `/`
//     (e.g. `internal/turn/turn.go`, `/etc/foo.toml`); or
//   - a token starting with `./` or `~/`, even without a further `/`
//     (e.g. `./README.md`, `~/.config/foo.yaml`).
//
// The character class excludes whitespace and the quote/angle-bracket
// runes that mark line-noise contexts (HTML fragments, quoted
// strings).
//
// Extension list is intentionally narrow — broadening it later is a
// change with low risk; over-matching now would pollute the symbol
// store with false positives.
var filePathRE = regexp.MustCompile(
	`(?:` +
		`(?:\./|~/)[^\s<>"']*` + // ./README.md or ~/.config/foo.yaml
		`|` +
		`[^\s<>"']*/[^\s<>"']+` + // internal/turn/turn.go, /etc/foo.toml
		`)` +
		`\.(?:go|md|jsonl?|ya?ml|toml|txt|sh|py|c|h|cpp|hpp|rs|tsx|ts|jsx|js|html|css)\b`,
)

// hexIDRE matches a git-SHA-shaped lowercase hex run of 7..40 chars.
// Word boundaries on both sides keep it from snipping the middle of a
// longer hex run; rejection of `0x...` and `#...` neighborhoods
// happens in the caller because Go's regexp doesn't have lookbehind.
var hexIDRE = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// urlTrailingPunct is the set of trailing prose-punctuation runes
// stripped from a captured URL before emit.
const urlTrailingPunct = `.,;:!?'"`

// deterministicExtract finds all §2.7.1 identifier-category symbols in
// content (URLs, file paths, git-SHA-shaped hex IDs) and feeds them to
// state.coalesce as SourceDeterministic per §3.3 pass 1.
//
// Order is irrelevant — coalesce dedups by normalized form and the
// dominance rule (§2.7.3) ensures a higher-source emission for the
// same key still wins when this pass runs alongside source-specific
// extractors. Efficiency is not yet a concern: regex passes are
// bounded by content size which is bounded by §6.5's per-delta cap.
func deterministicExtract(state *State, content string) {
	for _, m := range urlRE.FindAllString(content, -1) {
		raw := strings.TrimRight(m, urlTrailingPunct)
		if raw == "" {
			continue
		}
		emitIdentifier(state, raw)
	}
	for _, m := range filePathRE.FindAllString(content, -1) {
		emitIdentifier(state, m)
	}
	for _, idx := range hexIDRE.FindAllStringIndex(content, -1) {
		start, end := idx[0], idx[1]
		// Reject hex literals embedded in source: `0x<hex>` or color/
		// fragment-id `#<hex>` shapes. Go's regexp has no lookbehind, so
		// peek the preceding bytes here.
		if start >= 2 && content[start-2] == '0' && (content[start-1] == 'x' || content[start-1] == 'X') {
			continue
		}
		if start >= 1 && content[start-1] == '#' {
			continue
		}
		emitIdentifier(state, content[start:end])
	}
}

// emitIdentifier records one identifier-category symbol in the
// coalesce buffer with SourceDeterministic provenance. Identifier
// normalization is identity (§2.7.2), so raw == normalized; we still
// route through store.Normalize for symmetry with the other
// extractors and to centralize any future identifier rules.
func emitIdentifier(state *State, raw string) {
	normalized := store.Normalize(raw, store.SymbolIdentifier)
	state.coalesce.addSymbol(raw, normalized, store.SourceDeterministic)
}
