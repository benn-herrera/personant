package store

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"personant/internal/memops"
)

// thrFrontmatterDelimiter is the literal `---` line that brackets the
// YAML frontmatter block at the head of every thread.md file.
const thrFrontmatterDelimiter = "---"

// ThreadTurnWindow is the count of per-turn excerpt files retained in a
// thread's turns/ directory. Appending a new excerpt FIFO-evicts the
// lowest-numbered files until the count is back to ThreadTurnWindow, so
// the live thread body is recency-windowed and per-turn write cost is
// O(1) instead of O(thread length).
//
// v0.1 best-guess calibration: 512 turn-excerpts is a generous live
// window — older operational detail is recoverable from the §2.8 event
// log and the distilled symbol memory persists in frontmatter
// history_symbols. Calibratable against six-month-sim data.
const ThreadTurnWindow = 512

// turnFileDigits is the zero-padding width of a turn-excerpt filename:
// turns/0000024.md. Seven digits comfortably covers any thread's
// lifetime turn count.
const turnFileDigits = 7

// ThreadDir returns the canonical directory for a thread by id:
// <Home>/threads/thr_<n>/.
func ThreadDir(paths PersonantPaths, threadID string) string {
	return filepath.Join(paths.ThreadsDir, threadID)
}

// ThreadMetaPath returns the path to a thread's metadata file:
// <Home>/threads/thr_<n>/thread.md (frontmatter + title, no body).
func ThreadMetaPath(paths PersonantPaths, threadID string) string {
	return filepath.Join(ThreadDir(paths, threadID), "thread.md")
}

// ThreadTurnsDir returns the path to a thread's turn-excerpt directory:
// <Home>/threads/thr_<n>/turns/.
func ThreadTurnsDir(paths PersonantPaths, threadID string) string {
	return filepath.Join(ThreadDir(paths, threadID), "turns")
}

// turnFileName returns the zero-padded excerpt filename for a turn
// number: turnFileName(24) == "0000024.md".
func turnFileName(turnNumber int) string {
	return fmt.Sprintf("%0*d.md", turnFileDigits, turnNumber)
}

// ListThreadIDs returns the canonical thread IDs of every subdirectory
// under paths.ThreadsDir whose name matches thr_<digits>. Order is
// sorted lexically. A missing ThreadsDir returns (nil, nil) — that is
// the fresh-init state, not an error.
//
// Entries that do not match the canonical naming pattern are silently
// ignored (e.g. temp directories left from a crashed save). The
// validator in `personant verify` is the place to flag drift.
func ListThreadIDs(paths PersonantPaths) ([]string, error) {
	entries, err := os.ReadDir(paths.ThreadsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list threads: read dir %s: %w", paths.ThreadsDir, err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if !memops.ThreadIDPattern.MatchString(id) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// LoadAllThreadFrontmatter reads every threads/thr_<n>/thread.md and
// returns their parsed frontmatter (the body is not assembled —
// frontmatter alone is enough for derived-index building).
//
// Tolerate-and-continue policy: a thread whose thread.md fails to read
// or parse is logged via logf and skipped. Index building tolerates
// drift; `personant verify` is the validator that fails hard on bad
// files. A nil logf is silent.
//
// A missing ThreadsDir returns (nil, nil) — fresh-init state.
func LoadAllThreadFrontmatter(paths PersonantPaths, logf func(format string, args ...any)) ([]memops.ThreadMeta, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ids, err := ListThreadIDs(paths)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]memops.ThreadMeta, 0, len(ids))
	for _, id := range ids {
		fm, err := LoadThreadFrontmatter(paths, id)
		if err != nil {
			logf("load thread frontmatter: skip %s: %v", ThreadMetaPath(paths, id), err)
			continue
		}
		out = append(out, fm)
	}
	return out, nil
}

// LoadThreadFrontmatter reads only threads/thr_<n>/thread.md, parses the
// YAML frontmatter, and returns it. This is the cheap workhorse for
// index building, verification, and the scenario harness — it never
// touches the turns/ directory.
//
// thread.md format:
//
//	---
//	<YAML frontmatter>
//	---
//	# <title>
//
// Returns memops.ErrThreadFileNotFound when the thread directory or
// thread.md is absent. Returns a wrapped error when the frontmatter
// delimiters are missing, the YAML fails to parse, or required fields
// (id/project) are unset.
func LoadThreadFrontmatter(paths PersonantPaths, threadID string) (memops.ThreadMeta, error) {
	path := ThreadMetaPath(paths, threadID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return memops.ThreadMeta{}, fmt.Errorf("load thread %s: %w", threadID, memops.ErrThreadFileNotFound)
		}
		return memops.ThreadMeta{}, fmt.Errorf("load thread %s: read: %w", threadID, err)
	}

	fmBytes, _, err := splitFrontmatter(data)
	if err != nil {
		return memops.ThreadMeta{}, fmt.Errorf("load thread %s: %w", threadID, err)
	}

	var fm memops.ThreadMeta
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return memops.ThreadMeta{}, fmt.Errorf("load thread %s: parse yaml: %w", threadID, err)
	}
	if fm.ID == "" {
		return memops.ThreadMeta{}, fmt.Errorf("load thread %s: frontmatter missing required field: id", threadID)
	}
	if fm.Project == "" {
		return memops.ThreadMeta{}, fmt.Errorf("load thread %s: frontmatter missing required field: project", threadID)
	}
	return fm, nil
}

// ReadThreadBody assembles a thread's body from its turns/ excerpt
// files. Excerpts are read newest-first (highest-numbered first); when
// byteBudget > 0, reading stops once the accumulated size reaches the
// budget, so a caller reads only ~budget worth of recent excerpts, not
// all ThreadTurnWindow files. byteBudget <= 0 reads every retained
// excerpt.
//
// The selected excerpts are returned joined in chronological order
// (oldest→newest) with a blank line between, so the result reads
// naturally top-to-bottom. A thread with no turns/ directory yet
// returns ("", nil).
func ReadThreadBody(paths PersonantPaths, threadID string, byteBudget int) (string, error) {
	turnsDir := ThreadTurnsDir(paths, threadID)
	nums, err := turnFileNumbers(turnsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read thread body %s: %w", threadID, err)
	}
	if len(nums) == 0 {
		return "", nil
	}

	// Walk newest-first, accumulating excerpts until the budget is met.
	// selected holds excerpts in newest-first order; it is reversed for
	// the final chronological join.
	selected := make([]string, 0, len(nums))
	total := 0
	for i := len(nums) - 1; i >= 0; i-- {
		path := filepath.Join(turnsDir, turnFileName(nums[i]))
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read thread body %s: read %s: %w", threadID, path, err)
		}
		excerpt := strings.TrimRight(string(data), "\n")
		selected = append(selected, excerpt)
		total += len(excerpt)
		if byteBudget > 0 && total >= byteBudget {
			break
		}
	}

	// Reverse to chronological order and join.
	var b strings.Builder
	b.Grow(total + 2*len(selected))
	for i := len(selected) - 1; i >= 0; i-- {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(selected[i])
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// LoadThread is the convenience whole-thread read: LoadThreadFrontmatter
// plus ReadThreadBody with no byte budget. For callers that genuinely
// need the entire thread (the closure curator). Index, verify, and the
// working-set assembler should use the cheaper narrower calls.
func LoadThread(paths PersonantPaths, threadID string) (memops.Thread, error) {
	fm, err := LoadThreadFrontmatter(paths, threadID)
	if err != nil {
		return memops.Thread{}, err
	}
	body, err := ReadThreadBody(paths, threadID, 0)
	if err != nil {
		return memops.Thread{}, err
	}
	return memops.Thread{Frontmatter: fm, Body: body}, nil
}

// SaveThreadFrontmatter writes a thread's thread.md atomically (temp +
// fsync + rename): the YAML frontmatter followed by a `# <title>` line.
// The title is derived from the frontmatter — its summary, or the ID
// when the summary is empty — matching the top heading the old
// whole-file body used. The thread directory is created if absent.
//
// This rewrites only the small bounded metadata file; the turn-excerpt
// directory is untouched.
func SaveThreadFrontmatter(paths PersonantPaths, threadID string, fm memops.ThreadMeta) error {
	if fm.ID == "" {
		return fmt.Errorf("save thread frontmatter: id is empty")
	}
	if fm.Project == "" {
		return fmt.Errorf("save thread frontmatter %s: project is empty", fm.ID)
	}

	fmBytes, err := marshalFrontmatter(fm)
	if err != nil {
		return fmt.Errorf("save thread frontmatter %s: marshal yaml: %w", fm.ID, err)
	}

	title := fm.Summary
	if title == "" {
		title = fm.ID
	}

	var buf bytes.Buffer
	buf.Grow(len(fmBytes) + len(title) + 16)
	buf.WriteString(thrFrontmatterDelimiter)
	buf.WriteByte('\n')
	buf.Write(fmBytes)
	buf.WriteString(thrFrontmatterDelimiter)
	buf.WriteByte('\n')
	buf.WriteString("# ")
	buf.WriteString(title)
	buf.WriteByte('\n')

	dir := ThreadDir(paths, threadID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save thread frontmatter %s: mkdir %s: %w", fm.ID, dir, err)
	}
	return WriteFileAtomic(ThreadMetaPath(paths, threadID), buf.Bytes())
}

// SeedThread materializes a whole thread in one call: it writes
// thread.md from the frontmatter and, when the body is non-empty, lays
// it down as turn-excerpt files. The body is split into turn blocks on
// the `## Turn ` heading (the format renderTurnExcerpt produces); a
// leading `# title` line, if present, is dropped (the title lives in
// thread.md). Each block is written as turn N where N is parsed from
// its `## Turn <N>` heading, falling back to sequential numbering when
// a heading carries no number.
//
// SeedThread is the convenience inverse of LoadThread for callers that
// have a fully-formed memops.Thread in hand — test fixtures and any
// future bulk-import path. The turn-excerpt FIFO window is NOT applied
// here: a seed is expected to be within the window already.
func SeedThread(paths PersonantPaths, thr memops.Thread) error {
	if err := SaveThreadFrontmatter(paths, thr.Frontmatter.ID, thr.Frontmatter); err != nil {
		return err
	}
	blocks := splitTurnBlocks(thr.Body)
	if len(blocks) == 0 {
		return nil
	}
	turnsDir := ThreadTurnsDir(paths, thr.Frontmatter.ID)
	if err := os.MkdirAll(turnsDir, 0o755); err != nil {
		return fmt.Errorf("seed thread %s: mkdir %s: %w", thr.Frontmatter.ID, turnsDir, err)
	}
	for i, b := range blocks {
		n := b.turn
		if n <= 0 {
			n = i + 1
		}
		body := b.text
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		path := filepath.Join(turnsDir, turnFileName(n))
		if err := WriteFileAtomic(path, []byte(body)); err != nil {
			return fmt.Errorf("seed thread %s: %w", thr.Frontmatter.ID, err)
		}
	}
	return nil
}

// turnBlock is one parsed turn excerpt: its turn number (0 when the
// heading carried none) and its verbatim text.
type turnBlock struct {
	turn int
	text string
}

// splitTurnBlocks splits a thread body into per-turn excerpt blocks on
// the `## Turn ` heading. Content before the first such heading (a
// `# title` line, blank lines) is discarded. A body with no `## Turn `
// heading yields a single block with turn 0 (sequential numbering will
// apply).
func splitTurnBlocks(body string) []turnBlock {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return nil
	}
	lines := strings.Split(body, "\n")
	var blocks []turnBlock
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		text := strings.TrimRight(strings.Join(cur, "\n"), "\n")
		if text != "" {
			blocks = append(blocks, turnBlock{turn: parseTurnHeading(cur[0]), text: text})
		}
		cur = nil
	}
	started := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## Turn ") {
			flush()
			started = true
		}
		if !started {
			continue
		}
		cur = append(cur, ln)
	}
	flush()
	if len(blocks) == 0 && strings.TrimSpace(body) != "" {
		// No `## Turn` heading at all — treat the whole body as one
		// turn-0 block so a hand-written seed body still lands somewhere.
		stripped := stripLeadingTitle(body)
		if strings.TrimSpace(stripped) != "" {
			blocks = append(blocks, turnBlock{turn: 0, text: strings.TrimRight(stripped, "\n")})
		}
	}
	return blocks
}

// stripLeadingTitle drops a leading `# ...` markdown heading line (and
// the blank line after it) from body.
func stripLeadingTitle(body string) string {
	if !strings.HasPrefix(body, "# ") {
		return body
	}
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		rest := body[i+1:]
		return strings.TrimLeft(rest, "\n")
	}
	return ""
}

// parseTurnHeading extracts the turn number from a `## Turn <N> ...`
// heading line. Returns 0 when the line is not such a heading or the
// number does not parse.
func parseTurnHeading(line string) int {
	const prefix = "## Turn "
	if !strings.HasPrefix(line, prefix) {
		return 0
	}
	rest := strings.TrimSpace(line[len(prefix):])
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// AppendThreadTurn writes a single turn excerpt to
// threads/thr_<n>/turns/<zero-padded>.md atomically, then FIFO-evicts
// the lowest-numbered excerpts until at most ThreadTurnWindow remain.
// The turns/ directory is created if absent.
//
// Appending is O(1): one small file written, at most one evicted. An
// empty excerpt is a no-op (the closure path re-engages a thread to
// rewrite frontmatter without adding a turn).
func AppendThreadTurn(paths PersonantPaths, threadID string, turnNumber int, excerpt string) error {
	if excerpt == "" {
		return nil
	}
	if turnNumber <= 0 {
		return fmt.Errorf("append thread turn %s: turn number %d must be positive", threadID, turnNumber)
	}
	turnsDir := ThreadTurnsDir(paths, threadID)
	if err := os.MkdirAll(turnsDir, 0o755); err != nil {
		return fmt.Errorf("append thread turn %s: mkdir %s: %w", threadID, turnsDir, err)
	}

	body := excerpt
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	path := filepath.Join(turnsDir, turnFileName(turnNumber))
	if err := WriteFileAtomic(path, []byte(body)); err != nil {
		return fmt.Errorf("append thread turn %s: %w", threadID, err)
	}

	nums, err := turnFileNumbers(turnsDir)
	if err != nil {
		return fmt.Errorf("append thread turn %s: list turns: %w", threadID, err)
	}
	// nums is sorted ascending; evict from the front until the count is
	// within the window.
	for i := 0; len(nums)-i > ThreadTurnWindow; i++ {
		evict := filepath.Join(turnsDir, turnFileName(nums[i]))
		if err := os.Remove(evict); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("append thread turn %s: evict %s: %w", threadID, evict, err)
		}
	}
	return nil
}

// turnFileNumbers returns the sorted turn numbers of every
// <digits>.md file in turnsDir. A non-existent directory surfaces
// os.ErrNotExist (callers decide whether that is benign). Files that do
// not match the <digits>.md pattern are ignored.
func turnFileNumbers(turnsDir string) ([]int, error) {
	entries, err := os.ReadDir(turnsDir)
	if err != nil {
		return nil, err
	}
	nums := make([]int, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		stem := strings.TrimSuffix(name, ".md")
		n, err := strconv.Atoi(stem)
		if err != nil || n <= 0 {
			continue
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums, nil
}

// splitFrontmatter scans data for the opening `---\n` delimiter, then
// for the matching closing `---\n` delimiter. Returns the frontmatter
// bytes (excluding the delimiters) and the body bytes that follow. A
// single leading newline immediately after the closing delimiter is
// consumed (the conventional blank line) but no further trimming is
// applied.
//
// The closing delimiter must be matched against an entire line; an
// embedded `---` inside a code block is therefore safe.
func splitFrontmatter(data []byte) (fm []byte, body string, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

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

	bodyOffset := bodyStart(data)
	if bodyOffset < 0 {
		return nil, "", fmt.Errorf("frontmatter: closing delimiter not located in data")
	}
	bodyBytes := data[bodyOffset:]
	if len(bodyBytes) > 0 && bodyBytes[0] == '\n' {
		bodyBytes = bodyBytes[1:]
	}
	return fmBuf.Bytes(), string(bodyBytes), nil
}

// bodyStart returns the byte offset in data of the first byte after the
// closing `---` line of the frontmatter, or -1 if the structure is not
// well-formed.
func bodyStart(data []byte) int {
	openEnd := lineStartingWith(data, 0, thrFrontmatterDelimiter)
	if openEnd < 0 {
		return -1
	}
	closeEnd := lineStartingWith(data, openEnd, thrFrontmatterDelimiter)
	if closeEnd < 0 {
		return -1
	}
	return closeEnd
}

// lineStartingWith finds the next line that, after right-trimming
// spaces and tabs, equals prefix exactly, starting search at offset
// start. Returns the offset of the first byte after that line's
// terminating `\n`, or -1 if no such line exists.
func lineStartingWith(data []byte, start int, prefix string) int {
	i := start
	for i < len(data) {
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
func marshalFrontmatter(f memops.ThreadMeta) ([]byte, error) {
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
