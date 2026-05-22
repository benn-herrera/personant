package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteTempPrefix is the prefix for the temp file every
// WriteFileAtomic invocation writes through. The temp is part of an atomic
// write to a *fixed* target, so it needs no timestamp or random suffix: a
// deterministic "tmp-<target>" sibling is preferable because (a) a leftover
// temp after a crash clearly maps back to its target and stays visible for
// forensics, (b) the filesystem mtime is the timestamp, and (c) the
// deterministic temp->target mapping is exactly what a future roll-forward
// crash-recovery would replay.
//
// This is the single substrate-wide implementation of the temp-write +
// fsync + rename pattern (MAD coding-standards review item D3-3). New
// persistence sites must route through this helper rather than open-code
// the sequence.
const atomicWriteTempPrefix = "tmp-"

// WriteFileAtomic writes data to path atomically: create a temp file in
// the same directory as path, write data, fsync, close, then rename over
// path. The parent directory must already exist — atomic substitution is
// the responsibility of this helper, directory provisioning is not.
//
// On any failure before the rename, the temp file is removed; after a
// successful rename it becomes the new path. The rename itself is the
// atomic commit point: a reader either sees the pre-existing path or the
// fully-written replacement, never a partial file.
//
// The temp is a deterministic "tmp-<base>" sibling of the target. O_TRUNC
// deterministically overwrites any stale leftover from a prior crashed
// write; the substrate is single-writer per target, so there is no
// concurrent-writer collision concern.
func WriteFileAtomic(path string, data []byte) error {
	tmpPath := filepath.Join(filepath.Dir(path), atomicWriteTempPrefix+filepath.Base(path))
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("atomic write %s: create temp: %w", path, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("atomic write %s: write: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("atomic write %s: fsync: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomic write %s: close temp: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic write %s: rename: %w", path, err)
	}
	cleanup = false
	return nil
}
