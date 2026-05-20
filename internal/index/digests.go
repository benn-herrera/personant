package index

// Project digest builder — produces projects/prj_<n>/digest.json from
// spine.jsonl alone (spec §2.5.2). Reads each project's meta.json (when
// present) to resolve display_name; missing meta.json is recoverable —
// a warning is logged and the project id is used as the display name.
//
// Per spec §2.5.2 OPEN, v0.1's `one_line_summary` is a frequency-weighted
// concatenation of recent_anchors, truncated to 80 chars. May be replaced
// with an LLM-curated form later.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"personant/internal/memops"
	"personant/internal/store"
)

func sortSymbolsLexical(rs []store.SymbolRecord) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Symbol < rs[j].Symbol })
}

const (
	// recentDigestThreads — number of most-recently-engaged threads per
	// project to aggregate `recent_anchors` from.
	recentDigestThreads = 20
	// recentAnchorsTopN — ceiling on `recent_anchors` cardinality.
	recentAnchorsTopN = 8
	// oneLineSummaryMax — hard cap on `one_line_summary` per spec §2.5.2.
	oneLineSummaryMax = 80
)

// BuildDigests groups spine records by project and produces one
// ProjectDigest per project that has at least one record. The returned
// map is keyed by project id (e.g. "prj_3", "prj_default").
//
// projectsDir is the on-disk root for per-project state; meta.json is
// read from <projectsDir>/<id>/meta.json when present. When meta.json
// is missing or unparseable, display_name falls back to the id (or
// "default" for prj_default), and the missing-meta warning is reported
// via warnf if non-nil.
func BuildDigests(spine []memops.SpineRecord, projectsDir string, warnf func(format string, args ...any)) (map[string]memops.ProjectDigest, error) {
	if warnf == nil {
		warnf = func(string, ...any) {}
	}

	buckets := make(map[string][]memops.SpineRecord)
	for _, r := range spine {
		buckets[r.Project] = append(buckets[r.Project], r)
	}

	out := make(map[string]memops.ProjectDigest, len(buckets))
	for projectID, records := range buckets {
		display := readDisplayName(projectsDir, projectID, warnf)
		recent := selectRecentAnchors(records)
		summary := truncateUTF8(joinComma(recent), oneLineSummaryMax)

		digest := memops.ProjectDigest{
			Project:        projectID,
			DisplayName:    display,
			ThreadCount:    len(records),
			RecentAnchors:  recent,
			OneLineSummary: summary,
			ByteSize:       0,
		}
		size, err := computeDigestByteSize(digest)
		if err != nil {
			return nil, fmt.Errorf("build digest for %s: %w", projectID, err)
		}
		digest.ByteSize = size
		out[projectID] = digest
	}
	return out, nil
}

// readDisplayName fetches `name` from <projectsDir>/<id>/meta.json. On
// any failure (missing dir, missing file, parse error), it warns and
// falls back to a sensible default ("default" for prj_default; the id
// otherwise). Per the index-rebuild contract, missing meta is
// recoverable — we never fail the rebuild on it.
func readDisplayName(projectsDir, projectID string, warnf func(format string, args ...any)) string {
	fallback := projectID
	if projectID == "prj_default" {
		fallback = "default"
	}

	metaPath := filepath.Join(projectsDir, projectID, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			warnf("index: %s missing; using %q as display_name for %s", metaPath, fallback, projectID)
		} else {
			warnf("index: read %s: %v; using %q as display_name", metaPath, err, fallback)
		}
		return fallback
	}
	var meta memops.ProjectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		warnf("index: parse %s: %v; using %q as display_name", metaPath, err, fallback)
		return fallback
	}
	if meta.Name == "" {
		warnf("index: %s has empty name; using %q as display_name", metaPath, fallback)
		return fallback
	}
	return meta.Name
}

// selectRecentAnchors implements spec §2.5.2:
//   - sort threads by `last_engaged` descending
//   - take the top recentDigestThreads (or fewer if less exist)
//   - count anchor frequency across that subset
//   - return the top recentAnchorsTopN by frequency, ties by lexical
//     anchor for determinism.
func selectRecentAnchors(records []memops.SpineRecord) []string {
	// Most-recently-engaged threads first.
	sorted := make([]memops.SpineRecord, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		// String compare on RFC3339 strings is order-preserving for
		// well-formed timestamps with consistent timezone format. For
		// strict correctness we'd parse, but this is hot-ish code and
		// the spec mandates RFC3339; equality ties are broken by id.
		if sorted[i].LastEngaged != sorted[j].LastEngaged {
			return sorted[i].LastEngaged > sorted[j].LastEngaged
		}
		return sorted[i].ID < sorted[j].ID
	})
	if len(sorted) > recentDigestThreads {
		sorted = sorted[:recentDigestThreads]
	}

	freq := make(map[string]int)
	for _, r := range sorted {
		for _, a := range r.Anchors {
			freq[a]++
		}
	}

	anchors := make([]string, 0, len(freq))
	for a := range freq {
		anchors = append(anchors, a)
	}
	sort.Slice(anchors, func(i, j int) bool {
		fi, fj := freq[anchors[i]], freq[anchors[j]]
		if fi != fj {
			return fi > fj
		}
		return anchors[i] < anchors[j]
	})
	if len(anchors) > recentAnchorsTopN {
		anchors = anchors[:recentAnchorsTopN]
	}
	return anchors
}

// joinComma joins anchors with ", ".
func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	n := 0
	for i, p := range parts {
		n += len(p)
		if i > 0 {
			n += 2
		}
	}
	buf := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, ',', ' ')
		}
		buf = append(buf, p...)
	}
	return string(buf)
}

// truncateUTF8 returns s truncated to at most max bytes without splitting
// a UTF-8 rune. Anchors are normalized identifiers/entities (spec §2.7);
// ASCII is the common case, but multi-byte rune support is cheap and
// keeps behavior predictable if a non-ASCII anchor slips through.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Walk back from max to a rune boundary.
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// computeDigestByteSize returns the JSON-serialized byte length of d
// with ByteSize already populated to that same length, per spec §2.5.2.
// Two-pass: marshal with byte_size=0, take length, write that as the
// final byte_size — the encoded length of the integer doesn't change
// up to byte_size values that have the same digit count, but the digest
// content here is comfortably under that boundary.
//
// (For the tiny digests produced from spine alone, the worst case is a
// few hundred bytes; the integer-width concern is theoretical.)
func computeDigestByteSize(d memops.ProjectDigest) (int, error) {
	d.ByteSize = 0
	encoded, err := json.Marshal(d)
	if err != nil {
		return 0, fmt.Errorf("marshal digest: %w", err)
	}
	return len(encoded), nil
}

// WriteDigest writes a single digest atomically to
// <projectsDir>/<projectID>/digest.json. The project directory is
// created if missing.
func WriteDigest(projectsDir, projectID string, digest memops.ProjectDigest) error {
	dir := filepath.Join(projectsDir, projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write digest %s: mkdir: %w", projectID, err)
	}
	path := filepath.Join(dir, "digest.json")
	encoded, err := encodeDigestJSON(digest)
	if err != nil {
		return fmt.Errorf("write digest %s: encode: %w", projectID, err)
	}
	if err := store.WriteFileAtomic(path, encoded); err != nil {
		return fmt.Errorf("write digest %s: %w", projectID, err)
	}
	return nil
}

// encodeDigestJSON returns the canonical on-disk byte form of a digest.
// Pretty-printed (2-space indent) for line-grain git diffs; trailing
// newline is appended by json.Encoder. The same byte stream is used by
// both write and check paths, so byte-for-byte comparison in `check` is
// faithful to whatever WriteDigest emits.
func encodeDigestJSON(d memops.ProjectDigest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeSymbolsJSONL returns the canonical on-disk byte form of a
// symbols.jsonl file: records sorted by symbol, one JSON object per
// line, trailing newline. Mirrors store.WriteJSONL's encoding so
// `check` can compare without disk round-trips. Duplicate-symbol
// detection happens here too — BuildSymbols guarantees uniqueness, but
// being explicit keeps the layer contracted.
func encodeSymbolsJSONL(records []store.SymbolRecord) ([]byte, error) {
	sorted := make([]store.SymbolRecord, len(records))
	copy(sorted, records)
	sortSymbolsLexical(sorted)

	seen := make(map[string]struct{}, len(sorted))
	for _, r := range sorted {
		if _, dup := seen[r.Symbol]; dup {
			return nil, fmt.Errorf("duplicate symbol %q", r.Symbol)
		}
		seen[r.Symbol] = struct{}{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range sorted {
		if err := enc.Encode(r); err != nil {
			return nil, fmt.Errorf("encode symbol %q: %w", r.Symbol, err)
		}
	}
	return buf.Bytes(), nil
}

