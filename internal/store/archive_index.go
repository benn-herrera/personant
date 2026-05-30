package store

import (
	"errors"
	"fmt"
	"os"

	"personant/internal/memops"
)

// Archive index store ops (§3.8 / design §3.3). The archive index is
// canonical (spec §2.1), one memops.ArchiveEntry per line, sorted by
// thr_id for line-grain diffs. It is never a derived file and is never
// rewritten on recovery except to stamp RecoveredAt.
//
// The deletion commit named by each entry is the canonical store; this
// index is the lookup. Losing the index never loses the data — the
// commit stays reachable by git history.

// LoadArchiveIndex reads the archive index. A missing file is treated as
// an empty index (the canonical "nothing archived yet" state, matching
// ReadSpine's missing-file semantics). Entries are returned in file order
// (which is sorted by thr_id, since every write goes through
// WriteJSONL's sort).
func LoadArchiveIndex(paths PersonantPaths) ([]memops.ArchiveEntry, error) {
	records, err := ReadJSONL[memops.ArchiveEntry](paths.ArchiveIndex)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load archive index: %w", err)
	}
	return records, nil
}

// AppendArchiveEntries merges entries into the archive index and rewrites
// it sorted by thr_id, atomically (temp+rename via WriteJSONL). An entry
// whose ThrID already exists replaces the existing one — recovery stamps
// RecoveredAt by re-appending the same id, and an archival of an id that
// was previously recovered legitimately supersedes the old breadcrumb.
// A no-op (empty entries) makes no write.
func AppendArchiveEntries(paths PersonantPaths, entries []memops.ArchiveEntry) error {
	if len(entries) == 0 {
		return nil
	}
	existing, err := LoadArchiveIndex(paths)
	if err != nil {
		return fmt.Errorf("append archive entries: %w", err)
	}
	byID := make(map[string]memops.ArchiveEntry, len(existing)+len(entries))
	for _, e := range existing {
		byID[e.ThrID] = e
	}
	for _, e := range entries {
		byID[e.ThrID] = e
	}
	merged := make([]memops.ArchiveEntry, 0, len(byID))
	for _, e := range byID {
		merged = append(merged, e)
	}
	if err := WriteJSONL(paths.ArchiveIndex, merged, func(e memops.ArchiveEntry) string { return e.ThrID }); err != nil {
		return fmt.Errorf("append archive entries: %w", err)
	}
	return nil
}

// FindArchiveEntry returns the archive entry for thrID. The second return
// is false when no such entry exists (the not-found signal — callers map
// it to memops.ErrArchiveEntryNotFound at the port boundary). Errors only
// on read failures.
func FindArchiveEntry(paths PersonantPaths, thrID string) (memops.ArchiveEntry, bool, error) {
	records, err := LoadArchiveIndex(paths)
	if err != nil {
		return memops.ArchiveEntry{}, false, fmt.Errorf("find archive entry: %w", err)
	}
	for i := range records {
		if records[i].ThrID == thrID {
			return records[i], true, nil
		}
	}
	return memops.ArchiveEntry{}, false, nil
}
