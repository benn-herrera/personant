package store

import (
	"strings"
	"unicode"
)

// canonicalEntityStopWords is the spec §2.7.2 multi-word entity stop-word
// set. Project-scoped additions (ProjectMeta.IgnoreSymbols) are applied at
// the call site, not here.
var canonicalEntityStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {},
	"of": {}, "in": {}, "on": {}, "at": {}, "by": {}, "for": {},
	"to": {}, "with": {}, "from": {}, "as": {},
	"is": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "been": {}, "being": {},
}

// Normalize converts a raw symbol surface form to its canonical normalized
// form per spec §2.7.2. The category determines which rule set applies.
//
//   - SymbolIdentifier: returned unchanged. Identifiers are file paths, code
//     symbols, claim IDs, commit SHAs — collision requires exact match, so
//     case, whitespace, and punctuation are all preserved.
//   - SymbolEntity: lowercased; whitespace-separated tokens whose lowercase
//     matches the canonical stop-word list are dropped; remaining tokens are
//     joined with `-`; punctuation other than `-`, `.`, `_` is stripped.
//   - SymbolTag: leading `#` stripped; lowercased; whitespace replaced with
//     `-`; punctuation other than `-` stripped.
//
// Unicode handling: case folding uses unicode.ToLower (so "Ångström" →
// "ångström"). The punctuation filter keeps any rune that is a letter,
// digit, or one of the per-category allowed connectors. Non-Latin marks
// (combining diacritics) and unusual symbols are conservatively stripped;
// the input domain is anchor surface forms produced by the model and human
// users, where this is the right default.
//
// Empty input returns empty.
func Normalize(raw string, category SymbolCategory) string {
	if raw == "" {
		return ""
	}

	switch category {
	case SymbolIdentifier:
		return raw

	case SymbolEntity:
		return normalizeEntity(raw)

	case SymbolTag:
		return normalizeTag(raw)
	}

	// Unknown category: conservative passthrough rather than panic. The
	// SymbolCategory enum is closed (spec §2.7.1), so this is unreachable
	// from validated callers; surfacing input unchanged is the least
	// destructive behavior on a programming error.
	return raw
}

// normalizeEntity implements the §2.7.2 entity rule set. Tokenize on
// whitespace, drop tokens that match a canonical stop word, lowercase and
// strip disallowed punctuation from each remaining token, then rejoin with
// `-`.
//
// Stop-word dropping is applied only when the input has more than one
// whitespace-separated token. Per the spec, "stop words dropped from
// multi-word forms" — a single-token entity that happens to coincide with
// a stop word (e.g. an anchor literally named "a" or "is") is preserved
// rather than collapsed to empty. This is the conservative reading: the
// stop list exists to denoise multi-word phrases, not to forbid the words
// themselves as anchors.
//
// Per-token punctuation stripping (rather than post-join) ensures that an
// internal hyphen-joiner is never mistaken for an in-token character
// requiring removal: the `-` between tokens is emitted by the join, not by
// the token itself.
func normalizeEntity(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	multiWord := len(fields) > 1

	keepers := make([]string, 0, len(fields))
	for _, f := range fields {
		lower := strings.ToLower(f)
		if multiWord {
			if _, stop := canonicalEntityStopWords[lower]; stop {
				continue
			}
		}
		stripped := stripEntityPunct(lower)
		if stripped == "" {
			continue
		}
		keepers = append(keepers, stripped)
	}
	return strings.Join(keepers, "-")
}

// stripEntityPunct keeps letters, digits, and the entity-allowed connectors
// `-`, `.`, `_`. Everything else (including combining marks and other
// punctuation) is dropped.
func stripEntityPunct(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '-', '.', '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeTag implements the §2.7.2 tag rule set. Strip a single leading
// `#`, lowercase, replace whitespace runs with `-`, and strip punctuation
// other than `-`.
func normalizeTag(raw string) string {
	s := strings.TrimPrefix(raw, "#")
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevHyphen = false
		case r == '-':
			b.WriteRune(r)
			prevHyphen = true
		default:
			// disallowed punctuation: drop
		}
	}

	// Trim a single trailing hyphen produced by trailing whitespace input;
	// matches the intent that whitespace is a separator, not a content rune.
	return strings.TrimRight(b.String(), "-")
}
