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
//
// # An entry may contain newlines; a LINE of the file may not
//
// One entry per line is the format, and multi-line input (see editor.go)
// would otherwise split one entry into several — corrupting not just the
// new entry but every older one after it, since the file is a positional
// list. The newline inside an entry is therefore escaped on the way out
// and unescaped on the way in, with the classic pair: '\n' becomes
// backslash-'n' and a backslash becomes two.
//
// # Why that pair cannot collide with content
//
// Escaping the escape is what makes the encoding INJECTIVE: every literal
// backslash in the text is doubled, so a lone backslash in the file always
// introduces an escape and never occurs in content. A user who types the
// two characters backslash-'n' gets three on disk and reads back exactly
// what they typed. Nothing fancier — a sentinel rune, a length prefix, a
// JSON line — buys anything, and each costs the plain-text inspectability
// the substrate rules ask for everywhere else.
//
// The one honest caveat: an entry written BEFORE this encoding existed
// that literally contains backslash-'n' reads back as a line break, once.
// The alternative is a format marker on a file whose whole virtue is that
// it has never needed one, to protect a case (a Windows path or an escape
// sequence typed at the prompt) that costs a re-typed history entry. An
// undefined escape — backslash-'t' — is left exactly as typed, and the
// re-encode on the next flush is stable rather than accumulating
// backslashes.
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
		h.add(decodeEntry(sc.Text()))
	}
	return h
}

// encodeEntry renders one entry as a single line of the file.
func encodeEntry(s string) string {
	if !strings.ContainsAny(s, "\\\n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// decodeEntry is encodeEntry's inverse, and it is TOLERANT in one
// direction: an escape this format does not define keeps its backslash and
// its following byte, so a pre-encoding entry holding a Windows path
// survives unchanged rather than losing a character.
//
// Byte-wise is safe here: every byte of a multi-byte rune is >= 0x80 and
// can never be mistaken for the backslash.
func decodeEntry(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case 'n':
			b.WriteByte('\n')
			i++
		case '\\':
			b.WriteByte('\\')
			i++
		default:
			b.WriteByte('\\')
		}
	}
	return b.String()
}

// add records an accepted line. Blank lines and consecutive duplicates are
// dropped and the store is capped, so the three properties hold on the
// in-memory list and the file inherits them at flush rather than being
// pruned separately.
//
// Entries are held DECODED — the escaping is the file's, not the store's —
// so the dedup comparison and the blank-line test see what the user typed,
// newlines and all, and an entry recalled with ↑ needs no unescaping at
// the point of use.
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
		_, _ = w.WriteString(encodeEntry(e))
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
