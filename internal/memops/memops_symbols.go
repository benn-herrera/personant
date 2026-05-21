package memops

import (
	"regexp"
	"strings"
	"unicode"
)

// This file holds pure, no-I/O helpers that operate on the domain
// model: symbol-surface normalization (spec §2.7.2), source-precedence
// resolution (spec §2.7.3), and the ID-format regexes. They belong with
// the port's contract rather than the substrate — they perform no I/O
// and only need stdlib (strings, regexp, unicode).

// ID format patterns. RE2-compiled once at package init for reuse by JSONL
// readers/writers and the future `personant verify` command.
var (
	ThreadIDPattern    = regexp.MustCompile(`^thr_\d+$`)
	ProjectIDPattern   = regexp.MustCompile(`^prj_\d+$`)
	ProjectNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// High-specificity identifier patterns. These are the §2.7.1 /
// §3.3-pass-1 deterministic identifier shapes — URLs, file paths, and
// git-SHA-shaped hex IDs — that the extractor finds *within* prose
// content. They live here, in the memops domain layer, so the single
// definition is shared by two consumers: the turn-package extractor
// (which scans prose for these shapes) and the history-eviction logic
// (which classifies an already-extracted symbol's Raw surface form to
// protect high-discrimination identifiers from Count-based eviction).
//
// URLProseRE and FilePathProseRE are intentionally NOT anchored so the
// extractor can FindAllString over a content blob. HexIDProseRE carries
// \b word boundaries for the same reason. The whole-token classifier
// IsHighSpecificity anchors each match itself (see below), so a symbol's
// Raw must match end-to-end to count as high-specificity — a generic
// word that merely contains a hex-shaped substring does not qualify.
var (
	// URLProseRE matches an http(s) URL. The terminating character
	// class is permissive — surrounding prose punctuation is kept on
	// the match and trimmed by the caller; explicit closers `)` and
	// `]` are excluded so a parenthesized URL doesn't capture them.
	URLProseRE = regexp.MustCompile(`https?://[^\s)\]]+`)

	// FilePathProseRE matches tokens that look like file paths ending
	// in one of the curated extensions. Two accept shapes per §3.3:
	// any non-whitespace token containing at least one `/`, or a token
	// starting with `./` or `~/`. The extension list is intentionally
	// narrow; broadening later is low-risk, over-matching now pollutes
	// the symbol store.
	FilePathProseRE = regexp.MustCompile(
		`(?:` +
			`(?:\./|~/)[^\s<>"']*` + // ./README.md or ~/.config/foo.yaml
			`|` +
			`[^\s<>"']*/[^\s<>"']+` + // internal/turn/turn.go, /etc/foo.toml
			`)` +
			`\.(?:go|md|jsonl?|ya?ml|toml|txt|sh|py|c|h|cpp|hpp|rs|tsx|ts|jsx|js|html|css)\b`,
	)

	// HexIDProseRE matches a git-SHA-shaped lowercase hex run of 7..40
	// chars. Word boundaries on both sides keep it from snipping the
	// middle of a longer hex run; rejection of `0x...`/`#...`
	// neighborhoods happens in the extractor caller (Go regexp has no
	// lookbehind).
	HexIDProseRE = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// IsHighSpecificity reports whether raw is a high-discrimination
// identifier surface form — a URL, file path, or git-SHA-shaped hex ID.
// It is the classification predicate shared with the extractor's pattern
// set (URLProseRE / FilePathProseRE / HexIDProseRE), re-derived from the
// symbol's persisted Raw rather than its Source: Source is provenance and
// is upgraded toward higher authority by DominantSource (§2.7.3), so a
// discriminative path the model also anchored becomes SourceModel and the
// specificity signal is erased. Authority is not specificity; the surface
// form is the durable signal.
//
// Matching is whole-token: the entire raw string must match a pattern,
// not merely contain one. A generic word that happens to embed a
// hex-shaped substring (e.g. "deadbeef" inside prose) is reached here as
// an already-isolated token, so anchoring guards against a free word
// being mistaken for an ID. Empty input is not high-specificity.
func IsHighSpecificity(raw string) bool {
	if raw == "" {
		return false
	}
	return matchWhole(URLProseRE, raw) ||
		matchWhole(FilePathProseRE, raw) ||
		matchWhole(HexIDProseRE, raw)
}

// matchWhole reports whether re matches raw end-to-end. The prose
// patterns are unanchored (so the extractor can scan a blob), so the
// classifier checks that the located match spans the entire token.
func matchWhole(re *regexp.Regexp, raw string) bool {
	loc := re.FindStringIndex(raw)
	return loc != nil && loc[0] == 0 && loc[1] == len(raw)
}

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

// DominantSource implements the spec §2.7.3 source-precedence rule:
//
//	curator > user > model > deterministic
//
// When a normalized symbol is observed from two sources — within a single
// turn (per-turn coalescing) or across turns (cumulative history merge) —
// the dominant source wins. An empty source is treated as the lowest
// rank, so any non-empty source beats unset.
//
// This is a pure precedence helper with no I/O; it lives in memops rather
// than turn or index so both packages (and any future consumer that
// merges symbol provenance) can call it without crossing the
// turn → index dependency direction.
func DominantSource(a, b SymbolSource) SymbolSource {
	if sourceRank(a) >= sourceRank(b) {
		return a
	}
	return b
}

// sourceRank returns the §2.7.3 precedence rank. Higher rank wins.
// An empty/unrecognized source ranks 0, which lets DominantSource
// upgrade an unset value to any concrete source on first sighting.
func sourceRank(s SymbolSource) int {
	switch s {
	case SourceCurator:
		return 4
	case SourceUser:
		return 3
	case SourceModel:
		return 2
	case SourceDeterministic:
		return 1
	}
	return 0
}
