package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"personant/internal/dedup"
	"personant/internal/memops"
)

// trackedRecord is one files.jsonl line. Field order is the on-disk key
// order.
type trackedRecord struct {
	Path    string          `json:"path"`
	ID      string          `json:"id"`
	Size    int             `json:"size"`
	Turn    int             `json:"turn"`
	At      string          `json:"at"`
	Changes []trackedChange `json:"changes"`
}

type trackedChange struct {
	Turn int    `json:"turn"`
	At   string `json:"at"`
	From string `json:"from"`
	To   string `json:"to"`
	Diff bool   `json:"diff"`
	Size int    `json:"size"`
}

// TrackedIndexPath returns the path of a thread's tracked-file index:
// <Home>/threads/thr_<n>/files.jsonl.
func TrackedIndexPath(paths PersonantPaths, threadID string) string {
	return filepath.Join(ThreadDir(paths, threadID), trackedIndexName)
}

// TrackedBlobPath returns the path of the blob holding the bytes whose
// content id is id: <Home>/threads/thr_<n>/files/<id>.
func TrackedBlobPath(paths PersonantPaths, threadID, id string) string {
	return filepath.Join(ThreadDir(paths, threadID), trackedFilesDirName, id)
}

// TrackedDiffPath returns the path of the kept forward diff from content
// id from to content id to: <Home>/threads/thr_<n>/files/<from>-<to>.diff.
func TrackedDiffPath(paths PersonantPaths, threadID, from, to string) string {
	return filepath.Join(ThreadDir(paths, threadID), trackedFilesDirName, trackedDiffName(from, to))
}

func trackedDiffName(from, to string) string { return from + "-" + to + ".diff" }

// ReadTrackedIndex loads a thread's tracked-file index as plan entries, in
// path order. A missing index is no tracked files. An index that does not
// decode, is not strictly ascending by path, or names a malformed content
// id or timestamp is an error.
func ReadTrackedIndex(paths PersonantPaths, threadID string) ([]dedup.FileEntry, error) {
	recs, err := ReadJSONL[trackedRecord](TrackedIndexPath(paths, threadID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tracked index %s: %w", threadID, err)
	}
	entries := make([]dedup.FileEntry, 0, len(recs))
	for i, r := range recs {
		if i > 0 {
			switch prev := recs[i-1].Path; {
			case r.Path == prev:
				return nil, fmt.Errorf("read tracked index %s: record %d: duplicate path %q", threadID, i+1, r.Path)
			case r.Path < prev:
				return nil, fmt.Errorf("read tracked index %s: record %d: path %q sorts before %q", threadID, i+1, r.Path, prev)
			}
		}
		e, err := r.entry()
		if err != nil {
			return nil, fmt.Errorf("read tracked index %s: record %d (%q): %w", threadID, i+1, r.Path, err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (r trackedRecord) entry() (dedup.FileEntry, error) {
	if !isContentID(r.ID) {
		return dedup.FileEntry{}, fmt.Errorf("id %q is not a content id", r.ID)
	}
	at, err := time.Parse(time.RFC3339, r.At)
	if err != nil {
		return dedup.FileEntry{}, fmt.Errorf("at: %w", err)
	}
	e := dedup.FileEntry{Path: r.Path, ID: r.ID, Size: r.Size, Turn: r.Turn, At: at}
	for _, c := range r.Changes {
		if !isContentID(c.From) || !isContentID(c.To) {
			return dedup.FileEntry{}, fmt.Errorf("change %q -> %q: not content ids", c.From, c.To)
		}
		cat, err := time.Parse(time.RFC3339, c.At)
		if err != nil {
			return dedup.FileEntry{}, fmt.Errorf("change at: %w", err)
		}
		e.Changes = append(e.Changes, dedup.FileChange{Turn: c.Turn, At: cat, From: c.From, To: c.To, Diff: c.Diff, Size: c.Size})
	}
	return e, nil
}

func trackedRecordOf(e dedup.FileEntry) trackedRecord {
	r := trackedRecord{
		Path: e.Path, ID: e.ID, Size: e.Size, Turn: e.Turn, At: e.At.Format(time.RFC3339Nano),
		Changes: make([]trackedChange, 0, len(e.Changes)),
	}
	for _, c := range e.Changes {
		r.Changes = append(r.Changes, trackedChange{
			Turn: c.Turn, At: c.At.Format(time.RFC3339Nano), From: c.From, To: c.To, Diff: c.Diff, Size: c.Size,
		})
	}
	return r
}

// isContentID reports whether s has the form of a content id: 40 lowercase
// hex digits. Ids from the index name files, so this also keeps them from
// naming a path outside files/.
func isContentID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ReadTrackedBlob returns the bytes of the blob named id, failing when id
// is not a content id or the bytes do not hash to it. A missing blob's
// error satisfies errors.Is(err, os.ErrNotExist).
func ReadTrackedBlob(paths PersonantPaths, threadID, id string) ([]byte, error) {
	if !isContentID(id) {
		return nil, fmt.Errorf("read tracked blob %s: %q is not a content id", threadID, id)
	}
	data, err := os.ReadFile(TrackedBlobPath(paths, threadID, id))
	if err != nil {
		return nil, fmt.Errorf("read tracked blob %s: %w", threadID, err)
	}
	if got := dedup.ContentID(data); got != id {
		return nil, fmt.Errorf("read tracked blob %s: %s hashes to %s", threadID, id, got)
	}
	return data, nil
}

// ApplyFileEvents applies one turn's file events, in order, to a thread's
// tracked-file store and returns the repo-relative paths it wrote. turn is
// the owner excerpt number and at its header timestamp.
//
// A path's first event creates its entry with no change. A later event
// whose content differs records a change from the previous content, with
// its diff written when dedup.Diff keeps it, and trims the changes to the
// dedup.LiveDiffWindow most recent. Every event sets the entry's current
// id, size, turn and at. Blobs and diffs are written only when absent, and
// all of them before the index, which is written once at the end. On error
// the paths already written are returned with it.
func ApplyFileEvents(paths PersonantPaths, threadID string, turn int, at time.Time, events []memops.FileEvent) ([]string, error) {
	if len(events) == 0 {
		return nil, nil
	}
	entries, err := ReadTrackedIndex(paths, threadID)
	if err != nil {
		return nil, fmt.Errorf("apply file events %s: %w", threadID, err)
	}
	filesDir := filepath.Join(ThreadDir(paths, threadID), trackedFilesDirName)
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("apply file events %s: mkdir %s: %w", threadID, filesDir, err)
	}

	var written []string
	put := func(path, rel string, data []byte) error {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := WriteFileAtomic(path, data); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	}

	byPath := make(map[string]int, len(entries))
	for i, e := range entries {
		byPath[e.Path] = i
	}
	for _, ev := range events {
		id := dedup.ContentID([]byte(ev.Content))
		i, ok := byPath[ev.Path]
		if !ok {
			i = len(entries)
			byPath[ev.Path] = i
			entries = append(entries, dedup.FileEntry{Path: ev.Path})
		}
		e := &entries[i]
		if ok && e.ID != id {
			old, err := ReadTrackedBlob(paths, threadID, e.ID)
			if err != nil {
				return written, fmt.Errorf("apply file events %s: %s: %w", threadID, ev.Path, err)
			}
			diff, kept := dedup.Diff(string(old), ev.Content)
			if kept {
				if err := put(TrackedDiffPath(paths, threadID, e.ID, id), TrackedDiffRel(threadID, e.ID, id), []byte(diff)); err != nil {
					return written, fmt.Errorf("apply file events %s: %s: %w", threadID, ev.Path, err)
				}
			}
			e.Changes = append(e.Changes, dedup.FileChange{Turn: turn, At: at, From: e.ID, To: id, Diff: kept, Size: len(diff)})
			e.Changes = e.Changes[max(0, len(e.Changes)-dedup.LiveDiffWindow):]
		}
		if err := put(TrackedBlobPath(paths, threadID, id), TrackedBlobRel(threadID, id), []byte(ev.Content)); err != nil {
			return written, fmt.Errorf("apply file events %s: %s: %w", threadID, ev.Path, err)
		}
		e.ID, e.Size, e.Turn, e.At = id, len(ev.Content), turn, at
	}

	recs := make([]trackedRecord, len(entries))
	for i, e := range entries {
		recs[i] = trackedRecordOf(e)
	}
	if err := WriteJSONL(TrackedIndexPath(paths, threadID), recs, func(r trackedRecord) string { return r.Path }); err != nil {
		return written, fmt.Errorf("apply file events %s: %w", threadID, err)
	}
	return append(written, TrackedIndexRel(threadID)), nil
}
