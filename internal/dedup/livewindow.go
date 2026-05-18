package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// LiveDiffWindow is the dedup.live-diff-window default (N). The N
// versions immediately before the current one are rendered as literal
// unified diffs in the working window (spec §3.9.2); older versions
// collapse to content-addressed identifiers. The name mirrors the
// directive key, like AnchorCadence / DiffLiteralThreshold in chain.go.
const LiveDiffWindow = 3

// windowKind discriminates how a version is rendered in the live window.
type windowKind string

const (
	// kindCurrent is the most-recent version, rendered as a full literal.
	kindCurrent windowKind = "current"
	// kindDiff is one of the recentDiffs versions before current,
	// rendered as a reverse-delta unified diff.
	kindDiff windowKind = "diff"
	// kindIdentifier is an older version, collapsed to a
	// content-addressed identifier.
	kindIdentifier windowKind = "identifier"
)

// WindowEntry is one version's rendering in the §3.9.2 live-context
// composition. Kind selects the meaning of Text:
//
//   - "current"    — Text is the full literal current content.
//   - "diff"       — Text is the unified diff transforming the
//     next-newer version back into this one (reverse-delta direction).
//   - "identifier" — Text is the bracketed content-addressed identifier
//     "[content #<hash> — see most-recent position]".
type WindowEntry struct {
	// Version is the chain version index (0 = oldest … Len()-1 = current).
	Version int
	// Kind is one of "current", "diff", "identifier".
	Kind windowKind
	// Text is the rendered representation; its meaning depends on Kind.
	Text string
}

// LiveWindow classifies every version of the chain into the spec §3.9.2
// live-context representation: the current version as a full literal,
// the recentDiffs versions immediately before it as literal reverse-delta
// unified diffs, and all older versions as content-addressed identifiers.
//
// Entries are returned oldest-first (index 0 = oldest, last = current)
// so the caller renders them in temporal order. An empty chain returns
// an empty slice and nil error. recentDiffs must be >= 0; 0 is valid and
// yields current + identifiers only.
//
// Reconstruction errors from the chain propagate unchanged.
func (c *Chain) LiveWindow(recentDiffs int) ([]WindowEntry, error) {
	if recentDiffs < 0 {
		return nil, fmt.Errorf("dedup: live-diff-window %d is negative", recentDiffs)
	}
	n := len(c.Versions)
	if n == 0 {
		return nil, nil
	}

	// diffStart is the first (oldest) version index rendered as a diff;
	// versions [diffStart, n-1) are diffs, version n-1 is current.
	diffStart := n - 1 - recentDiffs
	if diffStart < 0 {
		diffStart = 0
	}

	entries := make([]WindowEntry, 0, n)
	for v := 0; v < n; v++ {
		switch {
		case v == n-1:
			entries = append(entries, WindowEntry{
				Version: v,
				Kind:    kindCurrent,
				Text:    c.Current(),
			})
		case v >= diffStart:
			// Reverse-delta: the diff transforms version v+1 back into v.
			older, err := c.Reconstruct(v)
			if err != nil {
				return nil, err
			}
			newer, err := c.Reconstruct(v + 1)
			if err != nil {
				return nil, err
			}
			entries = append(entries, WindowEntry{
				Version: v,
				Kind:    kindDiff,
				Text:    makeDiff(newer, older),
			})
		default:
			content, err := c.Reconstruct(v)
			if err != nil {
				return nil, err
			}
			entries = append(entries, WindowEntry{
				Version: v,
				Kind:    kindIdentifier,
				Text:    identifierText(content),
			})
		}
	}
	return entries, nil
}

// identifierText renders an older version's content-addressed
// identifier: a short hex SHA-256 prefix of the content, in the exact
// §3.9.2 form. The 12-hex-char prefix (48 bits) is ample to keep distinct
// versions of one file distinguishable while staying compact.
func identifierText(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("[content #%s — see most-recent position]",
		hex.EncodeToString(sum[:])[:12])
}
