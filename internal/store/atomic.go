package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteTempPattern is the canonical temp-file name pattern used by
// every WriteFileAtomic invocation across the codebase. One pattern means
// crash-residue cleanup audits only have to recognise one shape.
const atomicWriteTempPattern = ".atomic-*.tmp"

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
// This is the single substrate-wide implementation of the temp-write +
// fsync + rename pattern (MAD coding-standards review item D3-3). New
// persistence sites must route through this helper rather than open-code
// the sequence.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, atomicWriteTempPattern)
	if err != nil {
		return fmt.Errorf("atomic write %s: create temp: %w", path, err)
	}
	tmpPath := tmp.Name()
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
