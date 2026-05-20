package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"personant/internal/memops"
)

// ReadJSONL reads a JSONL file into a slice of T. One JSON object per line;
// blank lines are skipped. A nonexistent file is an error; an existing but
// empty file returns (nil, nil).
//
// Malformed lines (parse error or trailing non-whitespace after the JSON
// object) are reported with the 1-based line number wrapped in the error.
func ReadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var out []T
	scanner := bufio.NewScanner(f)
	// Allow lines longer than the default 64KB buffer; spine summaries are
	// short, but project meta or digest content could push past it.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		var rec T
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("%s:%d: decode: %w", path, lineNo, err)
		}
		// Reject trailing content after the JSON object on the same line.
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s:%d: trailing content after JSON object", path, lineNo)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// WriteJSONL writes records to path, one JSON object per line, sorted by
// keyFn ascending. Trailing newline. Atomic: writes to a temp file in the
// same directory, fsyncs, then renames over path. Duplicate keys are
// detected before any write and returned as an error.
func WriteJSONL[T any](path string, records []T, keyFn func(T) string) error {
	if keyFn == nil {
		return fmt.Errorf("write %s: keyFn is nil", path)
	}

	sorted := make([]T, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		return keyFn(sorted[i]) < keyFn(sorted[j])
	})

	seen := make(map[string]struct{}, len(sorted))
	for _, r := range sorted {
		k := keyFn(r)
		if _, dup := seen[k]; dup {
			return fmt.Errorf("write %s: duplicate key %q", path, k)
		}
		seen[k] = struct{}{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range sorted {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("write %s: encode key %q: %w", path, keyFn(r), err)
		}
	}
	if err := WriteFileAtomic(path, buf.Bytes()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ReadSpine reads a spine.jsonl file. A missing file is treated as an
// empty spine — the canonical "no records yet" state, semantically
// equivalent to an empty file. Other read errors propagate.
func ReadSpine(path string) ([]memops.SpineRecord, error) {
	records, err := ReadJSONL[memops.SpineRecord](path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return records, err
}

// WriteSpine writes a spine.jsonl file, sorted by thread ID.
func WriteSpine(path string, records []memops.SpineRecord) error {
	return WriteJSONL(path, records, func(r memops.SpineRecord) string { return r.ID })
}

// ReadSymbols reads a symbols.jsonl file. Missing-file semantics match
// ReadSpine: treated as an empty index.
func ReadSymbols(path string) ([]SymbolRecord, error) {
	records, err := ReadJSONL[SymbolRecord](path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return records, err
}

// WriteSymbols writes a symbols.jsonl file, sorted by symbol.
func WriteSymbols(path string, records []SymbolRecord) error {
	return WriteJSONL(path, records, func(r SymbolRecord) string { return r.Symbol })
}
