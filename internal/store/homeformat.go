package store

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Home on-disk format stamp (SPEC §9.1). <home>/version.toml is the
// CANONICAL record of which layout revision this home was written in. It
// holds exactly one key:
//
//	format = 1
//
// TOML because it is the house default for structured data files (JSONL
// is reserved for tailable logs). One integer key, no semver: an on-disk
// layout is either readable by a binary or it is not.
//
// Write discipline — load-bearing for the pre-Reconcile read (see
// memops.MemoryOps.HomeFormat): the file is written ONLY by `personant
// init` (touch-if-missing) and by a future migration. Nothing on a turn
// path touches it, so a mid-turn crash cannot tear it, and every write
// goes through WriteFileAtomic anyway (temp + fsync + rename), so a
// reader sees either the prior stamp or the new one — never a partial.

const (
	homeVersionFileName  = "version.toml"
	homeVersionFormatKey = "format"
)

// homeVersionDoc is the on-disk shape of version.toml.
type homeVersionDoc struct {
	Format int `toml:"format"`
}

// homeVersionTOML renders the file's canonical bytes. Shared by
// WriteHomeFormat and Init's scaffold step so the two cannot drift.
func homeVersionTOML(format int) []byte {
	return []byte(fmt.Sprintf("%s = %d\n", homeVersionFormatKey, format))
}

// ReadHomeFormat reads the home's on-disk format stamp.
//
// A MISSING file is not an error: it reports found=false, which is the
// legitimate "home predates versioning" state that adopt-forward gating
// handles (memops.GateHomeFormat). A file that is present but
// unparseable, or that carries no positive `format` key, IS an error —
// treating corruption as "unversioned" would let a garbled stamp be
// silently overwritten with a wrong revision.
func ReadHomeFormat(paths PersonantPaths) (found bool, format int, err error) {
	if paths.HomeVersion == "" {
		return false, 0, errors.New("store: ReadHomeFormat: PersonantPaths.HomeVersion is empty")
	}
	data, rerr := os.ReadFile(paths.HomeVersion)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("store: ReadHomeFormat: %w", rerr)
	}
	var doc homeVersionDoc
	if _, derr := toml.Decode(string(data), &doc); derr != nil {
		return false, 0, fmt.Errorf("store: ReadHomeFormat: parse %s: %w", paths.HomeVersion, derr)
	}
	if doc.Format <= 0 {
		return false, 0, fmt.Errorf("store: ReadHomeFormat: %s has no positive %q key",
			paths.HomeVersion, homeVersionFormatKey)
	}
	return true, doc.Format, nil
}

// WriteHomeFormat atomically records format as the home's on-disk
// revision, replacing any existing stamp. A non-positive format is a
// caller bug, refused at the boundary.
func WriteHomeFormat(paths PersonantPaths, format int) error {
	if paths.HomeVersion == "" {
		return errors.New("store: WriteHomeFormat: PersonantPaths.HomeVersion is empty")
	}
	if format <= 0 {
		return fmt.Errorf("store: WriteHomeFormat: format %d is not a valid revision", format)
	}
	if err := WriteFileAtomic(paths.HomeVersion, homeVersionTOML(format)); err != nil {
		return fmt.Errorf("store: WriteHomeFormat: %w", err)
	}
	return nil
}
