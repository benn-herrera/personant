package eventlog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// HealTail repairs a torn final line in an append-only log by appending
// exactly one '\n' when the file's last byte is not already a newline.
// It is purely additive: it never truncates or rewrites existing bytes,
// so a partially-written final line is preserved on disk (recovery
// surfaces it) but is separated from any subsequent append — the next
// Log call cannot merge its record onto the torn fragment.
//
// An absent or empty file is a no-op (nothing to heal). A file already
// ending in '\n' is a no-op.
func HealTail(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("eventlog: HealTail stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("eventlog: HealTail open %s: %w", path, err)
	}
	defer f.Close()

	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return fmt.Errorf("eventlog: HealTail read last byte %s: %w", path, err)
	}
	if last[0] == '\n' {
		return nil // already terminated — nothing torn
	}
	if _, err := f.WriteAt([]byte{'\n'}, info.Size()); err != nil {
		return fmt.Errorf("eventlog: HealTail append newline %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("eventlog: HealTail fsync %s: %w", path, err)
	}
	return nil
}

// ReadLinesTolerant reads path and returns its complete lines with the
// trailing newline stripped. A final line lacking a trailing newline is a
// torn append (a crash mid-write) and is skipped; tornBytes reports how
// many bytes that dropped fragment held, so a caller can log the loss. An
// absent file yields (nil, 0, nil).
//
// This is the shared tail-skip reader for the crash-stability harness
// (R2/R4) and any log consumer that must tolerate an unclean tail. It is
// read-only; HealTail is the write-side repair.
func ReadLinesTolerant(path string) (lines []string, tornBytes int, err error) {
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("eventlog: ReadLinesTolerant %s: %w", path, rerr)
	}
	if len(data) == 0 {
		return nil, 0, nil
	}

	if data[len(data)-1] != '\n' {
		if nl := bytes.LastIndexByte(data, '\n'); nl >= 0 {
			tornBytes = len(data) - (nl + 1)
			data = data[:nl+1]
		} else {
			tornBytes = len(data) // single unterminated line
			data = nil
		}
	}

	for _, ln := range bytes.Split(data, []byte{'\n'}) {
		if len(ln) == 0 {
			continue // trailing empty element after the final '\n'
		}
		lines = append(lines, string(ln))
	}
	return lines, tornBytes, nil
}
