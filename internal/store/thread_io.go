package store

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrThreadFileNotFound is returned by LoadThread when the canonical
// thread file does not exist. Callers distinguish "no file yet" (a new
// thread is about to be created) from a parse failure.
var ErrThreadFileNotFound = errors.New("store: thread file not found")

// Thread is the in-memory shape of a thread file: frontmatter plus
// markdown body. The frontmatter is canonical for metadata; the body is
// operational content (turn excerpts, curator-summarized milestones at
// retirement). Per spec §2.3.
type Thread struct {
	Frontmatter ThreadFrontmatter
	Body        string // markdown body; trailing newline preserved
}

// thrFrontmatterDelimiter is the literal `---` line that brackets the
// YAML frontmatter block at the head of every thread file.
const thrFrontmatterDelimiter = "---"

// ThreadPath returns the canonical filesystem path for a thread by id:
// <Home>/threads/thr_<n>.md.
func ThreadPath(paths PersonantPaths, threadID string) string {
	return filepath.Join(paths.ThreadsDir, threadID+".md")
}

// LoadThread reads and parses the thread file for threadID.
//
// File format:
//
//	---
//	<YAML frontmatter>
//	---
//	<markdown body>
//
// Returns ErrThreadFileNotFound when the file is absent. Returns a
// wrapped error when the frontmatter delimiters are missing, the YAML
// fails to parse, or required fields (id/project) are unset.
func LoadThread(paths PersonantPaths, threadID string) (Thread, error) {
	path := ThreadPath(paths, threadID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Thread{}, fmt.Errorf("load thread %s: %w", threadID, ErrThreadFileNotFound)
		}
		return Thread{}, fmt.Errorf("load thread %s: read: %w", threadID, err)
	}

	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return Thread{}, fmt.Errorf("load thread %s: %w", threadID, err)
	}

	var thread Thread
	if err := yaml.Unmarshal(fm, &thread.Frontmatter); err != nil {
		return Thread{}, fmt.Errorf("load thread %s: parse yaml: %w", threadID, err)
	}
	if thread.Frontmatter.ID == "" {
		return Thread{}, fmt.Errorf("load thread %s: frontmatter missing required field: id", threadID)
	}
	if thread.Frontmatter.Project == "" {
		return Thread{}, fmt.Errorf("load thread %s: frontmatter missing required field: project", threadID)
	}
	thread.Body = body
	return thread, nil
}

// SaveThread writes a thread file atomically (temp + fsync + rename).
// Frontmatter is marshaled via yaml.v3 with field order matching the
// ThreadFrontmatter struct declaration. The body is written verbatim
// after the closing delimiter, with a single trailing newline ensured
// (idempotent on repeated saves).
func SaveThread(paths PersonantPaths, thread Thread) error {
	if thread.Frontmatter.ID == "" {
		return fmt.Errorf("save thread: frontmatter id is empty")
	}
	if thread.Frontmatter.Project == "" {
		return fmt.Errorf("save thread %s: frontmatter project is empty", thread.Frontmatter.ID)
	}

	fmBytes, err := marshalFrontmatter(thread.Frontmatter)
	if err != nil {
		return fmt.Errorf("save thread %s: marshal yaml: %w", thread.Frontmatter.ID, err)
	}

	body := thread.Body
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}

	var buf bytes.Buffer
	buf.Grow(len(fmBytes) + len(body) + 16)
	buf.WriteString(thrFrontmatterDelimiter)
	buf.WriteByte('\n')
	buf.Write(fmBytes)
	buf.WriteString(thrFrontmatterDelimiter)
	buf.WriteByte('\n')
	if body != "" {
		buf.WriteByte('\n')
		buf.WriteString(body)
	}

	path := ThreadPath(paths, thread.Frontmatter.ID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save thread %s: mkdir %s: %w", thread.Frontmatter.ID, dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".thread-*.tmp")
	if err != nil {
		return fmt.Errorf("save thread %s: create temp: %w", thread.Frontmatter.ID, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("save thread %s: write temp: %w", thread.Frontmatter.ID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("save thread %s: fsync: %w", thread.Frontmatter.ID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save thread %s: close temp: %w", thread.Frontmatter.ID, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save thread %s: rename: %w", thread.Frontmatter.ID, err)
	}
	cleanup = false
	return nil
}

// splitFrontmatter scans data for the opening `---\n` delimiter, then for
// the matching closing `---\n` delimiter. Returns the frontmatter bytes
// (excluding the delimiters) and the body bytes that follow. A single
// leading newline immediately after the closing delimiter is consumed
// (the conventional blank line between frontmatter and body) but no
// further trimming is applied — user content is preserved verbatim.
//
// The closing delimiter must be matched against an entire line; an
// embedded `---` inside a code block in the body is therefore safe (the
// scanner stops at the *first* `---` line, which by construction is the
// frontmatter boundary).
func splitFrontmatter(data []byte) (fm []byte, body string, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// First non-empty token line must be the opening delimiter.
	if !scanner.Scan() {
		return nil, "", fmt.Errorf("frontmatter: file is empty")
	}
	if strings.TrimRight(scanner.Text(), " \t") != thrFrontmatterDelimiter {
		return nil, "", fmt.Errorf("frontmatter: missing opening delimiter %q (got %q)",
			thrFrontmatterDelimiter, scanner.Text())
	}

	var fmBuf bytes.Buffer
	closed := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimRight(line, " \t") == thrFrontmatterDelimiter {
			closed = true
			break
		}
		fmBuf.WriteString(line)
		fmBuf.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("frontmatter: scan: %w", err)
	}
	if !closed {
		return nil, "", fmt.Errorf("frontmatter: missing closing delimiter %q", thrFrontmatterDelimiter)
	}

	// Compute body offset from the original byte stream so we preserve
	// embedded newlines and CRs verbatim. Find the closing delimiter by
	// re-scanning data from the top: the body starts right after the
	// second `---\n` line.
	bodyOffset := bodyStart(data)
	if bodyOffset < 0 {
		// Should be unreachable given the scanner already saw the close.
		return nil, "", fmt.Errorf("frontmatter: closing delimiter not located in data")
	}

	bodyBytes := data[bodyOffset:]
	// One leading newline (the conventional blank line after `---`) is
	// stripped; preserve all subsequent content.
	if len(bodyBytes) > 0 && bodyBytes[0] == '\n' {
		bodyBytes = bodyBytes[1:]
	}
	return fmBuf.Bytes(), string(bodyBytes), nil
}

// bodyStart returns the byte offset in data of the first byte after the
// closing `---` line of the frontmatter, or -1 if the structure is not
// well-formed. Used by splitFrontmatter to reach the body without
// reconstructing line endings.
func bodyStart(data []byte) int {
	// Find opening delimiter line.
	openEnd := lineStartingWith(data, 0, thrFrontmatterDelimiter)
	if openEnd < 0 {
		return -1
	}
	// Find closing delimiter line, starting after the open.
	closeEnd := lineStartingWith(data, openEnd, thrFrontmatterDelimiter)
	if closeEnd < 0 {
		return -1
	}
	return closeEnd
}

// lineStartingWith finds the next line that, after right-trimming spaces
// and tabs, equals prefix exactly, starting search at offset start.
// Returns the offset of the first byte *after* that line's terminating
// `\n`. Returns -1 if no such line exists.
func lineStartingWith(data []byte, start int, prefix string) int {
	i := start
	for i < len(data) {
		// Find end of this line.
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		line := data[i:j]
		trimmed := strings.TrimRight(string(line), " \t")
		if trimmed == prefix {
			if j < len(data) {
				return j + 1
			}
			return j
		}
		if j >= len(data) {
			return -1
		}
		i = j + 1
	}
	return -1
}

// marshalFrontmatter encodes f as YAML. yaml.v3 honors struct
// declaration order, so the on-disk field sequence is stable across
// writes — git diffs stay record-grain.
func marshalFrontmatter(f ThreadFrontmatter) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&f); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
