package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"personant/internal/dedup"
)

// FileEntry is the §3.9.1 persistent record for one tracked file
// within one thread.
type FileEntry struct {
	// Path is the workspace-relative file path. It is the map key in
	// ThreadFiles.Files, denormalized here so a FileEntry is self-contained.
	Path string `json:"path"`
	// Chain is the reverse-delta version history; Chain.Current() is the
	// live literal. It serializes inline.
	Chain dedup.Chain `json:"chain"`
	// LastCommit is the git commit hash recorded at the last fs.commit, or
	// "" when there are uncommitted edits since that commit.
	LastCommit string `json:"last_commit"`
	// CommittedAt is the RFC3339 timestamp of that commit (clock.Timeline()
	// form, supplied by the caller). It is "" iff LastCommit is "".
	CommittedAt string `json:"committed_at"`
}

// ThreadFiles is the per-thread tracked-file store, persisted as the JSON
// sidecar <Home>/threads/<thr_id>.files.json.
type ThreadFiles struct {
	ThreadID string                `json:"thread_id"`
	Files    map[string]*FileEntry `json:"files"`
}

// ThreadFilesPath returns the canonical sidecar path for a thread's
// tracked-file store: <ThreadsDir>/<threadID>.files.json, a sibling to the
// thread's .md file (see ThreadPath).
func ThreadFilesPath(paths PersonantPaths, threadID string) string {
	return filepath.Join(paths.ThreadsDir, threadID+".files.json")
}

// LoadThreadFiles reads and parses the tracked-file sidecar for threadID.
//
// A missing sidecar is NOT an error: it is the fresh state, and a valid
// empty ThreadFiles is returned. A present-but-corrupt file IS an error.
func LoadThreadFiles(paths PersonantPaths, threadID string) (ThreadFiles, error) {
	path := ThreadFilesPath(paths, threadID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ThreadFiles{ThreadID: threadID, Files: map[string]*FileEntry{}}, nil
		}
		return ThreadFiles{}, fmt.Errorf("load thread files %s: read: %w", threadID, err)
	}
	var tf ThreadFiles
	if err := json.Unmarshal(data, &tf); err != nil {
		return ThreadFiles{}, fmt.Errorf("load thread files %s: parse %s: %w", threadID, path, err)
	}
	if tf.Files == nil {
		tf.Files = map[string]*FileEntry{}
	}
	return tf, nil
}

// SaveThreadFiles writes the tracked-file sidecar atomically (temp file in
// the ThreadsDir, fsync, rename). The ThreadsDir is created if absent.
// JSON is indented with two spaces so the sidecar diffs cleanly in the
// home-substrate git repo.
func SaveThreadFiles(paths PersonantPaths, tf ThreadFiles) error {
	if tf.ThreadID == "" {
		return errors.New("save thread files: ThreadID is empty")
	}
	dir := paths.ThreadsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save thread files %s: mkdir %s: %w", tf.ThreadID, dir, err)
	}
	path := ThreadFilesPath(paths, tf.ThreadID)

	tmp, err := os.CreateTemp(dir, ".threadfiles-*.tmp")
	if err != nil {
		return fmt.Errorf("save thread files %s: create temp: %w", tf.ThreadID, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tf); err != nil {
		tmp.Close()
		return fmt.Errorf("save thread files %s: encode: %w", tf.ThreadID, err)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("save thread files %s: flush: %w", tf.ThreadID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("save thread files %s: fsync: %w", tf.ThreadID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save thread files %s: close temp: %w", tf.ThreadID, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save thread files %s: rename: %w", tf.ThreadID, err)
	}
	cleanup = false
	return nil
}

// RecordWrite is the read/modify/write hook for a tracked file. It looks
// up the entry for path (creating one with a fresh dedup.Chain if absent),
// then appends content as the new current version.
//
// A write always invalidates the commit pointer: LastCommit and
// CommittedAt are cleared, since the file now has uncommitted edits.
func (tf *ThreadFiles) RecordWrite(path, content string) {
	if tf.Files == nil {
		tf.Files = map[string]*FileEntry{}
	}
	e, ok := tf.Files[path]
	if !ok {
		e = &FileEntry{Path: path}
		tf.Files[path] = e
	}
	e.Chain.Append(content)
	e.LastCommit = ""
	e.CommittedAt = ""
}

// RecordCommit sets the git-commit pointer on a tracked file. It returns
// an error if path is not tracked (a file the store has never seen cannot
// be committed) or if hash or committedAt is empty.
func (tf *ThreadFiles) RecordCommit(path, hash, committedAt string) error {
	if hash == "" {
		return fmt.Errorf("record commit %s: hash is empty", path)
	}
	if committedAt == "" {
		return fmt.Errorf("record commit %s: committedAt is empty", path)
	}
	e, ok := tf.Files[path]
	if !ok {
		return fmt.Errorf("record commit %s: file is not tracked", path)
	}
	e.LastCommit = hash
	e.CommittedAt = committedAt
	return nil
}

// Entry returns the tracked-file entry for path, if present.
func (tf *ThreadFiles) Entry(path string) (*FileEntry, bool) {
	e, ok := tf.Files[path]
	return e, ok
}
