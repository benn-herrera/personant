package fileadapter

import (
	"context"
	"fmt"

	"personant/internal/store"
)

// Home on-disk format stamp (SPEC §9.1). The file substrate keeps it in
// <home>/version.toml — canonical, git-tracked, one `format = N` key.
//
// Deliberately absent from the per-turn write scope (touchTurnScope):
// nothing on a turn path writes this file. Init's bootstrap commit
// captures the scaffolded stamp; an adopt-forward stamp written at open
// is absorbed by the next commit that sweeps the tree. See the port doc
// on MemoryOps.HomeFormat for the pre-Reconcile ordering invariant this
// write discipline underwrites.

// HomeFormat reads the home's format stamp. A home with no stamp reports
// found=false with no error; a corrupt one errors.
func (a *FileAdapter) HomeFormat(ctx context.Context) (bool, int, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	found, format, err := store.ReadHomeFormat(a.paths)
	if err != nil {
		return false, 0, fmt.Errorf("fileadapter: read home format: %w", err)
	}
	return found, format, nil
}

// StampHomeFormat writes the home's format stamp atomically (temp +
// fsync + rename), so a reader concurrent with the write sees either the
// prior stamp or the new one.
func (a *FileAdapter) StampHomeFormat(ctx context.Context, format int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.WriteHomeFormat(a.paths, format); err != nil {
		return fmt.Errorf("fileadapter: stamp home format: %w", err)
	}
	return nil
}
