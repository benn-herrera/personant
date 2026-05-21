package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// workingSetFile is the on-disk shape of the persisted session
// working-set artifact (<Home>/working-set.json). Only the two ordered
// ID lists are stored; session-volatile state is deliberately omitted.
type workingSetFile struct {
	ActiveThreads  []string `json:"active_threads"`
	DormantThreads []string `json:"dormant_threads"`
}

// SaveWorkingSet persists Layer B/C membership to <Home>/working-set.json.
// The artifact is volatile session state — it is gitignored in the home
// tree (see store.Init's seedGitignore), so the per-turn rewrite does not
// dirty the substrate's git repo. Atomic: encode to a temp file in the
// home directory, fsync, rename — the same pattern as SaveProjectMeta.
func SaveWorkingSet(paths PersonantPaths, activeThreads, dormantThreads []string) error {
	if paths.Home == "" {
		return errors.New("save working set: PersonantPaths.Home is empty")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(workingSetFile{
		ActiveThreads:  activeThreads,
		DormantThreads: dormantThreads,
	}); err != nil {
		return fmt.Errorf("save working set: encode: %w", err)
	}
	if err := WriteFileAtomic(paths.WorkingSet, buf.Bytes()); err != nil {
		return fmt.Errorf("save working set: %w", err)
	}
	return nil
}

// LoadWorkingSet reads <Home>/working-set.json. A nonexistent file yields
// (nil, nil, nil) — the fresh-launch state — mirroring how
// ReadLastActive treats an absent marker. A present but malformed file
// yields an error.
func LoadWorkingSet(paths PersonantPaths) (activeThreads, dormantThreads []string, err error) {
	data, err := os.ReadFile(paths.WorkingSet)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("load working set: %w", err)
	}
	var ws workingSetFile
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, nil, fmt.Errorf("load working set: parse %s: %w", paths.WorkingSet, err)
	}
	return ws.ActiveThreads, ws.DormantThreads, nil
}
