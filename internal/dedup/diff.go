// Package dedup is the reverse-delta content-versioning engine for spec
// §3.9 (working-set content dedup).
//
// A [Chain] is the reverse-delta-encoded version history of one content
// blob (one file). The current version is kept as a full literal — hot,
// the thing active cognition works on — and older versions are stored as
// reverse-deltas: the unified diff that transforms a newer state back
// into the older one. History reconstructs backward from the live
// literal; periodic anchor literals bound the reverse-delta chain length
// (spec §3.9.1).
//
// The package is pure: a data structure plus an algorithm, no file I/O,
// no dependency on other internal packages. A separate persistence layer
// marshals [Chain] to disk; this package only guarantees the type
// round-trips losslessly through encoding/json.
package dedup

import (
	"fmt"
	"strings"
)

// This file is a minimal, self-contained line-based unified-diff
// implementation: an LCS line diff plus unified-format emit and apply.
//
// A well-established diff dependency was considered and rejected: the
// scope here is bounded (line granularity, one hunk grouping pass, no
// fuzzy context matching), correctness is fully exercised by the
// round-trip tests, and a hand-rolled implementation keeps the package
// stdlib-only as the project prefers. The unified-diff text emitted here
// is the format the LLM will eventually see (spec §3.9.3).

// splitLines splits s into lines, preserving the information needed to
// reconstruct s byte-exactly. Each returned element retains its
// terminating "\n" if present; a final line without a newline has none.
// The empty string yields an empty slice (zero lines).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// lcsLineProductMax bounds the memory the LCS dynamic-programming table
// (lcs, below) is allowed to demand. lcs allocates an (n+1)×(m+1) table of
// int, so delta-encoding a large tracked file (tens of thousands of lines)
// can require gigabytes: two 100k-line versions would need
// ~100k × 100k × 8 B = 80 GB. Above this line-count product, the caller
// (encodeDemoted) skips delta-encoding and keeps the full literal instead.
// Correctness is preserved — Chain.Reconstruct handles literals natively;
// only this one version's compression is sacrificed for a bounded memory
// ceiling.
//
// Budget arithmetic: cap the worst-case table at ~64 MB. An int is 8 bytes,
// so 64 MiB / 8 B = 8,388,608 table entries; rounded down to a clean
// lcsLineProductMax = 8,000,000 line-pairs (a ~61 MB worst-case table).
const lcsLineProductMax = 8_000_000

// countLines returns the number of lines splitLines would produce for s,
// without allocating the slice — used to size-check the LCS table before
// building it. It mirrors splitLines exactly: the empty string is zero
// lines; otherwise the count is the number of '\n' bytes plus one when the
// final line has no trailing newline.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// lcs returns the longest common subsequence of a and b as a slice of
// index pairs (ai, bi) such that a[ai] == b[bi], in increasing order.
func lcs(a, b []string) [][2]int {
	n, m := len(a), len(b)
	// table[i][j] = LCS length of a[i:] and b[j:].
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var pairs [][2]int
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			pairs = append(pairs, [2]int{i, j})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}
	return pairs
}

// op is a single line-level edit operation in a diff.
type op struct {
	kind byte   // ' ' context, '-' delete (from a), '+' insert (into b)
	line string // the line text, including any trailing newline
}

// diffOps computes the edit script transforming a into b.
func diffOps(a, b []string) []op {
	pairs := lcs(a, b)
	var ops []op
	ai, bi := 0, 0
	for _, p := range pairs {
		for ai < p[0] {
			ops = append(ops, op{'-', a[ai]})
			ai++
		}
		for bi < p[1] {
			ops = append(ops, op{'+', b[bi]})
			bi++
		}
		ops = append(ops, op{' ', a[ai]})
		ai++
		bi++
	}
	for ai < len(a) {
		ops = append(ops, op{'-', a[ai]})
		ai++
	}
	for bi < len(b) {
		ops = append(ops, op{'+', b[bi]})
		bi++
	}
	return ops
}

// noNewline is the marker emitted (on its own line) immediately after a
// diff line whose text does not end in "\n". It matches the GNU diff
// convention "\ No newline at end of file" and lets apply reconstruct a
// missing trailing newline byte-exactly.
const noNewline = "\\ No newline at end of file"

// diffContext is the number of unchanged lines emitted on each side of a
// changed region — the standard unified-diff context window. Bounded
// context is what lets a small edit produce a small delta; emitting the
// whole file as context would make every delta larger than the literal.
const diffContext = 3

// makeDiff produces a line-based unified diff transforming oldContent
// into newContent. The result, applied via applyDiff to oldContent,
// yields newContent byte-for-byte. Identical inputs yield "".
//
// Output is standard multi-hunk unified diff: changed regions are
// grouped into hunks, each carrying up to diffContext unchanged lines of
// surrounding context. Unchanged lines outside any hunk's context are
// not emitted, so an edit's delta size scales with the edit, not the
// file.
func makeDiff(oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	a := splitLines(oldContent)
	b := splitLines(newContent)
	ops := diffOps(a, b)

	var sb strings.Builder
	// Walk the op list, carving out hunks. A hunk starts at the first
	// changed op and extends through the last changed op, with up to
	// diffContext context ops on each end; consecutive changed regions
	// closer than 2*diffContext context ops are merged into one hunk.
	i := 0
	for i < len(ops) {
		// Skip leading context until the next change.
		if ops[i].kind == ' ' {
			i++
			continue
		}
		// hunkStart: back up over up to diffContext context ops.
		hunkStart := i
		for hunkStart > 0 && ops[hunkStart-1].kind == ' ' && i-hunkStart < diffContext {
			hunkStart--
		}
		// Extend hunkEnd over changes, absorbing short context gaps.
		hunkEnd := i
		for hunkEnd < len(ops) {
			if ops[hunkEnd].kind != ' ' {
				hunkEnd++
				continue
			}
			// Count the run of context ops; if a change follows within
			// 2*diffContext, the gap is absorbed, else the hunk ends.
			run := hunkEnd
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run < len(ops) && run-hunkEnd <= 2*diffContext {
				hunkEnd = run // absorb gap, continue
				continue
			}
			break
		}
		// Trailing context: up to diffContext ops past the last change.
		tail := hunkEnd
		for tail < len(ops) && ops[tail].kind == ' ' && tail-hunkEnd < diffContext {
			tail++
		}
		writeHunk(&sb, ops, hunkStart, tail)
		i = tail
	}
	return sb.String()
}

// writeHunk emits one unified-diff hunk covering ops[start:end].
func writeHunk(sb *strings.Builder, ops []op, start, end int) {
	// 1-based source/target start lines and line counts for this hunk.
	aStart, bStart := 1, 1
	for _, o := range ops[:start] {
		if o.kind != '+' {
			aStart++
		}
		if o.kind != '-' {
			bStart++
		}
	}
	var aCount, bCount int
	for _, o := range ops[start:end] {
		if o.kind != '+' {
			aCount++
		}
		if o.kind != '-' {
			bCount++
		}
	}
	if aCount == 0 {
		aStart-- // empty source side: GNU convention uses start-1
	}
	if bCount == 0 {
		bStart--
	}
	fmt.Fprintf(sb, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
	for _, o := range ops[start:end] {
		writeDiffLine(sb, o.kind, o.line)
	}
}

// writeDiffLine emits one prefixed diff line, appending the no-newline
// marker when the line text lacks a trailing newline.
func writeDiffLine(sb *strings.Builder, kind byte, line string) {
	sb.WriteByte(kind)
	sb.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		sb.WriteByte('\n')
		sb.WriteString(noNewline)
		sb.WriteByte('\n')
	}
}

// applyDiff applies a unified diff (as produced by makeDiff) to
// oldContent and returns the transformed content. An empty diff is the
// identity. A diff that does not describe oldContent is a corruption
// signal and returns an error rather than silently producing garbage.
func applyDiff(oldContent, diff string) (string, error) {
	if diff == "" {
		return oldContent, nil
	}
	a := splitLines(oldContent)
	lines := splitLines(diff)

	var result strings.Builder
	ai := 0 // next unconsumed source line
	i := 0
	for i < len(lines) {
		header := strings.TrimRight(lines[i], "\n")
		if !strings.HasPrefix(header, "@@ ") {
			return "", fmt.Errorf("dedup: malformed diff: expected hunk header, got %q", header)
		}
		aStart, err := parseHunkSourceStart(header)
		if err != nil {
			return "", err
		}
		i++
		// Source line numbers are 1-based; aStart==0 means the hunk
		// targets an empty source. Copy intervening unchanged lines.
		copyTo := aStart - 1
		if copyTo < ai || copyTo > len(a) {
			return "", fmt.Errorf("dedup: hunk start %d out of range", aStart)
		}
		for ; ai < copyTo; ai++ {
			result.WriteString(a[ai])
		}
		// Consume hunk body until the next header or end of diff.
		for i < len(lines) {
			raw := lines[i]
			if raw == "" {
				i++
				continue
			}
			if strings.HasPrefix(strings.TrimRight(raw, "\n"), "@@ ") {
				break
			}
			i++
			kind := raw[0]
			text := raw[1:]
			if i < len(lines) && strings.TrimRight(lines[i], "\n") == noNewline {
				text = strings.TrimSuffix(text, "\n")
				i++
			}
			switch kind {
			case ' ':
				if ai >= len(a) || a[ai] != text {
					return "", fmt.Errorf("dedup: diff context mismatch at source line %d", ai+1)
				}
				result.WriteString(text)
				ai++
			case '-':
				if ai >= len(a) || a[ai] != text {
					return "", fmt.Errorf("dedup: diff delete mismatch at source line %d", ai+1)
				}
				ai++
			case '+':
				result.WriteString(text)
			default:
				return "", fmt.Errorf("dedup: malformed diff line: %q", raw)
			}
		}
	}
	// Trailing unchanged lines past the last hunk.
	for ; ai < len(a); ai++ {
		result.WriteString(a[ai])
	}
	return result.String(), nil
}

// parseHunkSourceStart extracts the 1-based source start line from a
// unified-diff hunk header "@@ -aStart,aCount +bStart,bCount @@".
func parseHunkSourceStart(header string) (int, error) {
	// header form: "@@ -A,B +C,D @@"
	rest, ok := strings.CutPrefix(header, "@@ -")
	if !ok {
		return 0, fmt.Errorf("dedup: malformed hunk header: %q", header)
	}
	comma := strings.IndexByte(rest, ',')
	space := strings.IndexByte(rest, ' ')
	if comma < 0 || space < 0 || comma > space {
		return 0, fmt.Errorf("dedup: malformed hunk header: %q", header)
	}
	var n int
	if _, err := fmt.Sscanf(rest[:comma], "%d", &n); err != nil {
		return 0, fmt.Errorf("dedup: malformed hunk header %q: %w", header, err)
	}
	if n == 0 {
		return 1, nil // empty-source convention: start before line 1
	}
	return n, nil
}
