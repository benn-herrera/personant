package store

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"personant/internal/memops"
)

// validLastActiveID reports whether s is a project id acceptable in the
// last-active marker file: either ProjectIDPattern (prj_<n>) or the
// reserved DefaultProjectID sentinel.
func validLastActiveID(s string) bool {
	return s == DefaultProjectID || memops.ProjectIDPattern.MatchString(s)
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

	if err := WriteFileAtomic(paths.LastActive, []byte(projectID+"\n")); err != nil {
		return fmt.Errorf("write last-active: %w", err)
	}
	return nil
}
