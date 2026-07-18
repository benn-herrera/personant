package store

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Derived watermark primitive (#94, SPEC §4.5.8). The watermark file
// holds the commit hash the derived state (symbols.jsonl + project
// digests) was last regenerated against. Recovery's clean-open path
// compares it to HEAD: equal (with a clean worktree) means derived
// rebuild can be skipped — the O(1) startup of principle 11.
//
// Trust contract: the watermark alone never certifies freshness. It is
// only meaningful when the worktree is clean — a dirty worktree means
// canonical state has moved past every commit, so the reader (recovery)
// must treat derived as stale regardless of the watermark. That rule
// lives in internal/recovery; this file is the path-and-bytes primitive,
// matching marker.go / journal.go.

// WriteDerivedWatermark atomically records commitHash as the commit the
// derived state was built from. An empty hash is a caller bug, refused
// at the boundary.
func WriteDerivedWatermark(paths PersonantPaths, commitHash string) error {
	if paths.DerivedWatermark == "" {
		return errors.New("store: WriteDerivedWatermark: PersonantPaths.DerivedWatermark is empty")
	}
	if commitHash == "" {
		return errors.New("store: WriteDerivedWatermark: commitHash is empty")
	}
	if err := WriteFileAtomic(paths.DerivedWatermark, []byte(commitHash+"\n")); err != nil {
		return fmt.Errorf("store: WriteDerivedWatermark: %w", err)
	}
	return nil
}

// ReadDerivedWatermark returns the recorded built-from commit hash. An
// absent file — and an empty one — report present=false with no error:
// "no watermark" is the legitimate greenfield/pre-upgrade state, not
// corruption (an empty hash could never have been written by
// WriteDerivedWatermark, so an empty file is treated the same as none).
func ReadDerivedWatermark(paths PersonantPaths) (hash string, present bool, err error) {
	if paths.DerivedWatermark == "" {
		return "", false, errors.New("store: ReadDerivedWatermark: PersonantPaths.DerivedWatermark is empty")
	}
	data, rerr := os.ReadFile(paths.DerivedWatermark)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("store: ReadDerivedWatermark: %w", rerr)
	}
	hash = strings.TrimSpace(string(data))
	if hash == "" {
		return "", false, nil
	}
	return hash, true, nil
}
