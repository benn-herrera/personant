package turn

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"personant/internal/memops"
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
// delta.Content (URLs, file paths, git-SHA-shaped hex IDs) and routes
// them via addExtractedSymbol as SourceDeterministic per §3.3 pass 1.
// delta carries the retention class so the routing helper can send
// task-class symbols to the staging buffer and decision-class symbols
// to coalesce (§3.0 transient-data lifecycle).
//
// Order is irrelevant — coalesce dedups by normalized form and the
// dominance rule (§2.7.3) ensures a higher-source emission for the
// same key still wins when this pass runs alongside source-specific
// extractors. Efficiency is not yet a concern: regex passes are
// bounded by content size which is bounded by §6.5's per-delta cap.
func deterministicExtract(ctx context.Context, state *State, delta Delta) {
	content := delta.Content
	for _, m := range urlRE.FindAllString(content, -1) {
		raw := strings.TrimRight(m, urlTrailingPunct)
		if raw == "" {
			continue
		}
		emitIdentifier(ctx, state, delta, raw)
	}
	for _, m := range filePathRE.FindAllString(content, -1) {
		emitIdentifier(ctx, state, delta, m)
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
		emitIdentifier(ctx, state, delta, content[start:end])
	}
}

// emitIdentifier records one identifier-category symbol with
// SourceDeterministic provenance. Identifier normalization is identity
// (§2.7.2), so raw == normalized; we still route through memops.Normalize
// for symmetry with the other extractors and to centralize any future
// identifier rules. The actual buffer choice (staging vs coalesce) is
// made by addExtractedSymbol based on delta.Retention.
func emitIdentifier(ctx context.Context, state *State, delta Delta, raw string) {
	normalized := memops.Normalize(raw, memops.SymbolIdentifier)
	addExtractedSymbol(ctx, state, delta, raw, normalized, memops.SourceDeterministic)
}

// addExtractedSymbol is the canonical write path for extracted symbols
// from any delta. Task-class deltas route to the cross-turn staging
// buffer (awaiting cross-reference promotion per B.3); decision-class
// deltas route directly to the turn-scoped coalesce buffer (immediate
// participation in turn-close engagement) AND trigger the §3.0
// cross-reference promotion check: if a staged symbol with the same
// normalized form is awaiting citation, it is promoted out of staging
// into coalesce.
//
// delta carries the class via delta.Retention (populated by
// onContextDelta's source-driven default plus any explicit caller
// override). raw is the original surface form; normalized is the
// canonical form per §2.7.2; source is the §2.7.3 provenance.
//
// Empty normalized is silently dropped — same convention as the
// underlying buffers.
func addExtractedSymbol(ctx context.Context, state *State, delta Delta, raw, normalized string, source memops.SymbolSource) {
	if normalized == "" {
		return
	}
	if delta.Retention == memops.RetentionTask {
		state.staging.add(stagedSymbol{
			Normalized: normalized,
			Raw:        raw,
			Source:     source,
			StagedAt:   state.TurnNumber,
		})
		return
	}
	// Decision-class: add the citing delta's symbol to coalesce first,
	// then promote any matching staged entry. Order matters for Raw —
	// coalesce.addSymbol only sets Raw on first insert, so the citing
	// delta's surface form wins as the "Raw that survived into the
	// decision". The §2.7.3 source-dominance merge is commutative, so
	// the order does not affect the resulting Source.
	state.coalesce.addSymbol(raw, normalized, source)
	staged, found := state.staging.lookup(normalized)
	if !found {
		return
	}
	state.coalesce.addSymbol(staged.Raw, staged.Normalized, staged.Source)
	state.staging.remove(normalized)
	// Instrumentation for window-K calibration and recall-fidelity
	// measurement. A log-write failure must not fail the chain —
	// promotion is internal bookkeeping, not user-visible.
	_ = state.Ops.Log(ctx, "staging", "promoted",
		fmt.Sprintf("normalized=%s staged_at=%d cited_at=%d turn_span=%d",
			normalized, staged.StagedAt, state.TurnNumber, state.TurnNumber-staged.StagedAt))
}
