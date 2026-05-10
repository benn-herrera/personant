package store

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrDuplicateThreadID is returned by AppendSpineRecord when the record's id
// already exists in the spine.
var ErrDuplicateThreadID = errors.New("store: duplicate thread id in spine")

// ErrThreadNotFound is returned by UpdateSpineRecord when no record with the
// given id is present in the spine.
var ErrThreadNotFound = errors.New("store: thread id not found in spine")

// AppendSpineRecord adds a new SpineRecord to the spine.jsonl file. Atomic:
// the file is read, the new record is added in memory, and the full set is
// rewritten via WriteSpine (temp+rename). Returns ErrDuplicateThreadID if a
// record with the same id is already present.
func AppendSpineRecord(paths PersonantPaths, rec SpineRecord) error {
	records, err := ReadSpine(paths.Spine)
	if err != nil {
		return fmt.Errorf("append spine record: %w", err)
	}
	for i := range records {
		if records[i].ID == rec.ID {
			return fmt.Errorf("append spine record %s: %w", rec.ID, ErrDuplicateThreadID)
		}
	}
	records = append(records, rec)
	if err := WriteSpine(paths.Spine, records); err != nil {
		return fmt.Errorf("append spine record: %w", err)
	}
	return nil
}

// UpdateSpineRecord replaces the existing record whose id matches rec.ID.
// Returns ErrThreadNotFound if no such record exists. Atomic.
func UpdateSpineRecord(paths PersonantPaths, rec SpineRecord) error {
	records, err := ReadSpine(paths.Spine)
	if err != nil {
		return fmt.Errorf("update spine record: %w", err)
	}
	idx := -1
	for i := range records {
		if records[i].ID == rec.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("update spine record %s: %w", rec.ID, ErrThreadNotFound)
	}
	records[idx] = rec
	if err := WriteSpine(paths.Spine, records); err != nil {
		return fmt.Errorf("update spine record: %w", err)
	}
	return nil
}

// FindSpineRecord returns the spine record with the given id. The second
// return is false if no such record exists. Errors only on read failures.
func FindSpineRecord(paths PersonantPaths, id string) (SpineRecord, bool, error) {
	records, err := ReadSpine(paths.Spine)
	if err != nil {
		return SpineRecord{}, false, fmt.Errorf("find spine record: %w", err)
	}
	for i := range records {
		if records[i].ID == id {
			return records[i], true, nil
		}
	}
	return SpineRecord{}, false, nil
}

// SpineRecordsByProject returns all records whose Project field matches the
// given project id, sorted by ID ascending.
func SpineRecordsByProject(paths PersonantPaths, projectID string) ([]SpineRecord, error) {
	records, err := ReadSpine(paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("spine records by project: %w", err)
	}
	out := make([]SpineRecord, 0)
	for i := range records {
		if records[i].Project == projectID {
			out = append(out, records[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// NextThreadID returns the next available thr_<n> id for a spine in the
// given state — max(existing n) + 1, or "thr_1" if records is empty. Records
// whose ID does not match ThreadIDPattern are ignored when computing the
// maximum; this lets the function tolerate (rare) older or hand-edited
// states without ever returning an ID that collides with an existing one.
func NextThreadID(records []SpineRecord) string {
	max := 0
	for _, r := range records {
		n, ok := parseSerialID(r.ID, "thr_")
		if !ok {
			continue
		}
		if n > max {
			max = n
		}
	}
	return "thr_" + strconv.Itoa(max+1)
}

// parseSerialID extracts the integer suffix of an id like "thr_<n>" or
// "prj_<n>". Returns (n, true) for valid forms with n >= 1; (0, false)
// otherwise.
func parseSerialID(id, prefix string) (int, bool) {
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	rest := id[len(prefix):]
	if rest == "" {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
