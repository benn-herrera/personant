package dedup

import "fmt"

// Default dedup.* parameters from spec §2.6.1. They are package
// constants for now; directive plumbing (per-project overrides) is later
// work. Names mirror the directive keys.
const (
	// AnchorCadence is the dedup.anchor-cadence default (K). Every K-th
	// version is kept as a full literal regardless of delta size, to
	// bound reverse-delta chain length and prevent cumulative drift —
	// the same role I-frames play in video compression.
	AnchorCadence = 10

	// DiffLiteralThreshold is the dedup.diff-literal-threshold default.
	// If a reverse-delta's size is >= this fraction of the literal it
	// would replace, the literal is stored instead — the delta is not
	// earning its keep (same heuristic as lzma's literal-vs-match).
	DiffLiteralThreshold = 0.7
)

// versionKind discriminates how a stored version is encoded.
type versionKind string

const (
	kindLiteral versionKind = "literal" // full content
	kindDelta   versionKind = "delta"   // reverse-delta against the next-newer version
)

// version is one recorded state of the content blob. Exactly one of
// Literal / Delta is meaningful, per Kind.
//
// Fields are exported so the whole [Chain] round-trips through
// encoding/json with no custom marshaller.
type version struct {
	Kind versionKind `json:"kind"`
	// Literal holds the full content when Kind == kindLiteral.
	Literal string `json:"literal,omitempty"`
	// Delta holds a unified diff transforming the next-newer version's
	// content back into this version's content, when Kind == kindDelta.
	Delta string `json:"delta,omitempty"`
}

// Chain is the reverse-delta-encoded version history of one content
// blob (spec §3.9.1). The zero value is a valid, empty chain ready for
// Append.
//
// Internally versions[0] is the oldest and versions[len-1] is the
// current live literal. Every version that is not a literal is a
// reverse-delta against the version immediately newer than it (index
// i+1). Reconstruct walks backward from the live literal — or from the
// nearest newer anchor literal — applying deltas.
//
// Chain is not safe for concurrent use; callers serialize access.
type Chain struct {
	Versions []version `json:"versions"`
}

// New returns an empty Chain. The zero value is equally usable; New
// exists only for call-site clarity.
func New() *Chain { return &Chain{} }

// Len reports the number of versions recorded.
func (c *Chain) Len() int { return len(c.Versions) }

// Current returns the live literal — the most-recent content. It is
// O(1). Current on an empty chain returns "".
func (c *Chain) Current() string {
	if len(c.Versions) == 0 {
		return ""
	}
	cur := c.Versions[len(c.Versions)-1]
	// The current version is always stored as a literal (see Append).
	return cur.Literal
}

// Append records content as the new current version (spec §3.9.1).
//
// The previously-current literal is demoted: re-encoded as a
// reverse-delta against the new content, unless an exception keeps it a
// literal —
//
//   - Anchor cadence: every AnchorCadence-th version (by zero-based
//     index) stays a full literal regardless, bounding chain length.
//   - Diff-literal threshold: if the reverse-delta is >=
//     DiffLiteralThreshold of the literal size, the literal is kept
//     instead — the delta is not earning its keep.
//
// The new content becomes the live literal.
func (c *Chain) Append(content string) {
	if len(c.Versions) > 0 {
		// Demote the outgoing current literal at index n-1.
		idx := len(c.Versions) - 1
		old := c.Versions[idx].Literal
		c.Versions[idx] = encodeDemoted(idx, old, content)
	}
	c.Versions = append(c.Versions, version{Kind: kindLiteral, Literal: content})
}

// encodeDemoted decides how the version at index idx (whose full content
// is oldContent) is stored once newContent supersedes it.
func encodeDemoted(idx int, oldContent, newContent string) version {
	// Anchor cadence: an anchor index is always a full literal.
	if idx%AnchorCadence == 0 {
		return version{Kind: kindLiteral, Literal: oldContent}
	}
	delta := makeDiff(newContent, oldContent)
	// Threshold: a delta that is too large relative to the literal it
	// would replace does not earn its keep.
	if float64(len(delta)) >= DiffLiteralThreshold*float64(len(oldContent)) {
		return version{Kind: kindLiteral, Literal: oldContent}
	}
	return version{Kind: kindDelta, Delta: delta}
}

// Reconstruct returns version v (0 = oldest … Len()-1 = current),
// reconstructed byte-exactly.
//
// It locates the nearest literal at index >= v (the current live
// literal, or a newer anchor / threshold literal) and walks backward
// applying reverse-deltas until it reaches v.
func (c *Chain) Reconstruct(v int) (string, error) {
	if v < 0 || v >= len(c.Versions) {
		return "", fmt.Errorf("dedup: version %d out of range [0,%d)", v, len(c.Versions))
	}
	// Find the nearest literal anchor at index >= v.
	anchor := v
	for anchor < len(c.Versions) && c.Versions[anchor].Kind != kindLiteral {
		anchor++
	}
	if anchor >= len(c.Versions) {
		// Unreachable for a well-formed chain: the current version is
		// always a literal. Treated as corruption rather than ignored.
		return "", fmt.Errorf("dedup: no literal anchor at or after version %d", v)
	}
	content := c.Versions[anchor].Literal
	// Walk backward: each delta at index i is a reverse-delta against
	// version i+1, so applying it to version i+1's content yields
	// version i's content.
	for i := anchor - 1; i >= v; i-- {
		next, err := applyDiff(content, c.Versions[i].Delta)
		if err != nil {
			return "", fmt.Errorf("dedup: reconstructing version %d at step %d: %w", v, i, err)
		}
		content = next
	}
	return content, nil
}
