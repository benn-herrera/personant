package term

import (
	"bufio"
	"os"
	"strings"
)

// history is the §4.3.1 recall store behind ↑/↓: newline-delimited text on
// disk, deduplicated on consecutive entries, capped at [HistoryLimit].
//
// The file format is liner's, unchanged, because the number and the shape
// were liner's defaults and an on-disk file that survives the editor's
// replacement is worth more than a tidier format. There is NO migration
// and none is needed.
type history struct {
	path string
	// entries is oldest first, so the file reads in the order it was
	// written and ↑ from the prompt walks backwards from the end.
	entries []string
}

// loadHistory reads path, tolerating everything: a missing file is an
// empty history, and an unreadable one is too. A session that cannot
// recall its past lines is a smaller loss than a session that refuses to
// start.
func loadHistory(path string) *history {
	h := &history{path: path}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		h.add(sc.Text())
	}
	return h
}

// add records an accepted line. Blank lines and consecutive duplicates are
// dropped and the store is capped, so the three properties hold on the
// in-memory list and the file inherits them at flush rather than being
// pruned separately.
func (h *history) add(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == line {
		return
	}
	h.entries = append(h.entries, line)
	if over := len(h.entries) - HistoryLimit; over > 0 {
		h.entries = append(h.entries[:0], h.entries[over:]...)
	}
}

// flush writes the store back. Called once, from [Terminal.Close], which
// is also the forced-exit path — so it must be safe with no deferred
// cleanup around it, and it is: one create, one write, one close.
func (h *history) flush() error {
	if h == nil || h.path == "" {
		return nil
	}
	f, err := os.Create(h.path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, e := range h.entries {
		_, _ = w.WriteString(e)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
