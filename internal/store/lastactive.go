package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validLastActiveID reports whether s is a project id acceptable in the
// last-active marker file: either ProjectIDPattern (prj_<n>) or the
// reserved DefaultProjectID sentinel.
func validLastActiveID(s string) bool {
	return s == DefaultProjectID || ProjectIDPattern.MatchString(s)
}

// ReadLastActive reads <Home>/last-active and returns the project id
// recorded there.
//
// A nonexistent file returns ("", nil) — the fresh-install signal the
// bootstrap waterfall (§4.5.7) treats as "no last-active to confirm".
//
// Validation: the file's trimmed contents must match ProjectIDPattern or
// equal DefaultProjectID. Anything else returns ("", wrapped-error) so a
// corrupted file does not silently feed a bogus id into bootstrap.
func ReadLastActive(paths PersonantPaths) (string, error) {
	data, err := os.ReadFile(paths.LastActive)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read last-active: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", nil
	}
	if !validLastActiveID(id) {
		return "", fmt.Errorf("read last-active: malformed id %q", id)
	}
	return id, nil
}

// WriteLastActive atomically writes the project id to <Home>/last-active.
//
// Validates projectID against ProjectIDPattern or DefaultProjectID before
// writing — a malformed id is rejected without touching the file. Atomic:
// temp+rename in the home directory.
func WriteLastActive(paths PersonantPaths, projectID string) error {
	if !validLastActiveID(projectID) {
		return fmt.Errorf("write last-active: invalid project id %q", projectID)
	}
	if paths.Home == "" {
		return errors.New("write last-active: PersonantPaths.Home is empty")
	}

	tmp, err := os.CreateTemp(paths.Home, ".last-active-*.tmp")
	if err != nil {
		return fmt.Errorf("write last-active: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.WriteString(projectID + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write last-active: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write last-active: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write last-active: close: %w", err)
	}
	if err := os.Rename(tmpPath, paths.LastActive); err != nil {
		return fmt.Errorf("write last-active: rename %s: %w", filepath.Base(paths.LastActive), err)
	}
	cleanup = false
	return nil
}
