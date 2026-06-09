// Package exact is the §3.4 EXACT-recall tier (#117) — the deterministic,
// exhaustive counterpart to the approximate O(log n) within-thread summary
// tree (#111) and the parallel embedding/symbolic signals (measure).
//
// Two genuinely different primitives live behind this tier because
// embeddings answer ABOUT-NESS, never CONTAINS-NESS:
//
//   - Lexical exact — "find every occurrence of token/pattern X" — answered
//     by a regexp scan over the retained plain-text substrate. Exact +
//     exhaustive + cheap. This package (GrepThreads).
//   - Semantic exact — "find EVERYTHING we discussed about X" where X is
//     paraphrased and completeness matters — answered by a full O(n) flat
//     cosine scan over a thread's chunk vectors with no top-Kf truncation.
//     That one needs the embedding index, so it lives on the recall Service
//     (measure.Service.ExhaustiveIntraScan), not here.
//
// # Import direction
//
// GrepThreads reads substrate text through the memops.MemoryOps port
// (LoadThreadExcerpts / LoadThreadMeta), so it sits on the application side
// of the port-and-adapter boundary, a sibling of recall/measure — both
// depend on the port, neither is depended on by it. It imports memops and
// stdlib only; nothing in memops or store imports exact, so there is no
// cycle. It deliberately does NOT live in recall/scoring (which is pure and
// substrate-free — it does no I/O) nor inside measure.Service (an embedding-
// index concern; the lexical scan needs no embedder, no index, no snapshot).
//
// # Deferred (NOT this package's job — future routing layer)
//
//   - LLM query-class ROUTING (is a query a grep / an exhaustive-semantic /
//     an everyday approximate-recall query?) is a fuzzy LLM-judgment call,
//     design-pending. GrepThreads is the deterministic primitive it will
//     call once it has decided this is a lexical-exact query.
//   - User-facing surface (a slash command for the "I know we talked about
//     this" case): the interactive front end is not built to v0.1 yet; no
//     user command wires to this primitive now.
//   - Scope policy (active-only vs. include archived/superseded threads) is a
//     routing decision; the primitive takes a CALLER-SUPPLIED thread set
//     precisely so the policy stays out of here.
//   - Composition with the §3.4 layers + the top-3 offer surface.
package exact

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"personant/internal/memops"
)

// metaTurnNumber is the synthetic turn number GrepThreads assigns to a match
// found in a thread's metadata (thread.md) text rather than in a numbered
// turn-excerpt. Turn numbers are positive (§ AppendThreadTurn rejects <= 0),
// so 0 is a free, deterministically-lowest sentinel that sorts the metadata
// match ahead of every turn-excerpt match for the same thread.
const metaTurnNumber = 0

// excerptSource is the narrow slice of memops.MemoryOps GrepThreads needs:
// per-thread retained text. Defined at the point of use (small interface)
// so a caller can scan an arbitrary text source — and the test can drive a
// fake without the full port — but the production caller passes a
// memops.MemoryOps, which satisfies it.
type excerptSource interface {
	// LoadThreadExcerpts returns a thread's retained turn-excerpts in
	// turn-number order.
	LoadThreadExcerpts(ctx context.Context, threadID string) ([]memops.ThreadExcerpt, error)
	// LoadThreadMeta returns a thread's metadata block (title/summary text
	// that lives in thread.md alongside the frontmatter).
	LoadThreadMeta(ctx context.Context, threadID string) (memops.ThreadMeta, error)
}

// Match is one lexical-exact hit: where it was found and the matched line.
// Enough locating info for the routing/surface layer to cite or re-fetch
// the source — thread id + turn number (the turns/<n>.md file, or
// metaTurnNumber for a thread.md/metadata match) + the 1-based line within
// that excerpt + the line text and the matched substring.
type Match struct {
	ThreadID   string
	TurnNumber int    // turns/<n>.md number; metaTurnNumber (0) for a metadata match
	Line       int    // 1-based line number within the excerpt text
	Offset     int    // byte offset of the match within the excerpt text
	LineText   string // the full line the match fell on (trimmed of trailing CR/LF)
	MatchText  string // the exact substring the pattern matched
}

// Options governs GrepThreads.
type Options struct {
	// CaseInsensitive compiles the pattern with the (?i) flag. The pattern
	// is otherwise treated verbatim as a Go regexp.
	CaseInsensitive bool
}

// GrepThreads is the lexical-exact primitive: it scans the RETAINED on-disk
// text of every thread in threadIDs for pattern (a Go regexp; the caller is
// trusted internal code, not end-user-injected text) and returns EVERY
// match — exact and exhaustive over the supplied set, no ranking, no
// truncation. That exhaustiveness is the whole point: it is the tier that
// answers "every place we used the word 'quaternion'", which embeddings
// (about-ness) structurally cannot.
//
// Scope is the caller's: threadIDs is scanned verbatim, with no active-vs-
// archived policy baked in (deferred to the routing layer — see the package
// doc). A thread whose substrate cannot be read is skipped via the optional
// logf seam (nil → silent), mirroring store.LoadAllThreadFrontmatter's
// tolerate-and-continue policy: a grep is advisory, one unreadable thread
// must not sink the whole scan.
//
// The retained text scanned per thread is its turn-excerpts (the durable
// decision-class content kept past the FIFO assembly window, §2.3) plus its
// metadata text (the thread.md title/summary line). A pattern matching
// nothing returns (nil, nil) — empty, not an error.
//
// Ordering is deterministic: thread id, then turn number, then byte offset
// within the excerpt — so two runs over the same substrate return the
// identical slice.
func GrepThreads(ctx context.Context, src excerptSource, threadIDs []string, pattern string, opts Options, logf func(format string, args ...any)) ([]Match, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	expr := pattern
	if opts.CaseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("exact: compile pattern %q: %w", pattern, err)
	}

	// Scan threads in id order so the result is deterministic regardless of
	// the caller's slice order; within a thread, excerpts are already
	// turn-ordered and lines/offsets are ordered by construction.
	ids := append([]string(nil), threadIDs...)
	sort.Strings(ids)

	var out []Match
	for _, id := range ids {
		meta, err := src.LoadThreadMeta(ctx, id)
		if err != nil {
			logf("exact: skip metadata %s: %v", id, err)
		} else if meta.Summary != "" {
			out = appendMatches(out, id, metaTurnNumber, meta.Summary, re)
		}

		excerpts, err := src.LoadThreadExcerpts(ctx, id)
		if err != nil {
			logf("exact: skip excerpts %s: %v", id, err)
			continue
		}
		for _, ex := range excerpts {
			out = appendMatches(out, id, ex.TurnNumber, ex.Text, re)
		}
	}
	return out, nil
}

// appendMatches finds every non-overlapping match of re in text and appends
// one Match per hit, located by line and byte offset. Line numbers are
// 1-based; the offset is the absolute byte offset within text. Lines are
// split on '\n' with a trailing '\r' trimmed from the recorded line text so
// CRLF substrate reads the same as LF.
func appendMatches(out []Match, threadID string, turnNumber int, text string, re *regexp.Regexp) []Match {
	locs := re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return out
	}
	// lineStarts[k] is the byte offset of the first character of line k+1
	// (line 1 starts at offset 0). Built once per excerpt so locating each
	// match is a binary search rather than a rescan.
	lineStarts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	for _, loc := range locs {
		start := loc[0]
		line := sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > start }) // first start AFTER → line index
		lineStart := lineStarts[line-1]
		lineEnd := len(text)
		if line < len(lineStarts) {
			lineEnd = lineStarts[line] - 1 // up to but not including the '\n'
		}
		lineText := strings.TrimRight(text[lineStart:lineEnd], "\r")
		out = append(out, Match{
			ThreadID:   threadID,
			TurnNumber: turnNumber,
			Line:       line,
			Offset:     start,
			LineText:   lineText,
			MatchText:  text[loc[0]:loc[1]],
		})
	}
	return out
}
