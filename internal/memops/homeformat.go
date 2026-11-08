package memops

import (
	"errors"
	"fmt"

	"personant/internal/version"
)

// Home on-disk format gating (SPEC §9.1).
//
// The home's format revision is a plain integer: the layout on disk is
// either one this binary understands or it is not. GateHomeFormat is the
// pure decision function over that comparison — no I/O, no side effects.
// The caller reads the stamp (MemoryOps.HomeFormat), asks this function
// what to do, performs any stamp it is told to (MemoryOps.StampHomeFormat),
// and refuses to open the home on error.
//
// ORDERING INVARIANT (load-bearing): the gate runs BEFORE Reconcile.
// Reconcile is crash recovery — it resets the worktree to a recovery
// point. Running it against a home whose layout this binary does not
// understand is exactly the wrong move: recovery would rearrange bytes it
// cannot interpret. Read the stamp, gate, then reconcile.

var (
	// ErrHomeFormatNewer reports a home written by a NEWER personant than
	// this binary. Refusal is the default; `allowNewer` overrides it.
	ErrHomeFormatNewer = errors.New("home format is newer than this binary understands")

	// ErrHomeFormatNoMigration reports an OLDER home with no registered
	// chain of migration steps to the current format.
	ErrHomeFormatNoMigration = errors.New("no migration path registered for this home format")

	// ErrHomeFormatInvalid reports a nonsense revision (zero or negative).
	ErrHomeFormatInvalid = errors.New("home format is not a valid revision")
)

// HomeFormatMigration is one registered step: it reads a home at format
// From and leaves it at format To. The 1→2, 2→3 and 3→4 steps are
// restamps only; they change no bytes.
type HomeFormatMigration struct{ From, To int }

// homeFormatMigrations is the registry of steps, ascending by From.
var homeFormatMigrations = []HomeFormatMigration{{From: 1, To: 2}, {From: 2, To: 3}, {From: 3, To: 4}}

// HomeFormatDecision is what the caller does next after a successful gate.
// A non-nil error from GateHomeFormat means REFUSE; a nil error means
// proceed, subject to the fields below.
type HomeFormatDecision struct {
	// Format is the home's effective on-disk revision — the value read
	// from the stamp, or version.UnversionedHomeFormat when there was
	// none. Callers log this (system.bootstrap), not the constant.
	Format int

	// StampFormat, when nonzero, is the revision the caller must write to
	// the home's format stamp before proceeding (adopt-forward or
	// migration). The gate performs no I/O, so the write is the caller's
	// to make.
	StampFormat int

	// MigratedFrom is the on-disk format the home was migrated from; 0
	// when no migration ran. The caller logs it.
	MigratedFrom int

	// NewerAccepted reports that the home was written by a NEWER personant
	// and `allowNewer` let it through. The caller must emit a loud warning
	// and an event-log entry — this is a knowingly-unsafe open, not a
	// normal one.
	NewerAccepted bool
}

// GateHomeFormat decides whether a home whose stamp reads (found, onDisk)
// may be opened by a binary at revision `current`.
//
//   - No stamp → the home predates versioning, so it is taken to be
//     version.UnversionedHomeFormat and falls through the SAME comparison
//     arms below. When current advances it therefore takes the registered
//     migration instead of being declared current — which is the entire
//     reason UnversionedHomeFormat is a separate pinned constant rather
//     than an alias of CurrentHomeFormat.
//   - Equal → proceed.
//   - Newer on disk → refuse (ErrHomeFormatNewer) unless allowNewer, in
//     which case proceed with NewerAccepted set.
//   - Older on disk → migrate forward through homeFormatMigrations: the
//     decision carries StampFormat = current and MigratedFrom = onDisk.
//     Refuse (ErrHomeFormatNoMigration) when the chain of steps breaks
//     before reaching current. allowNewer does not affect this arm.
//   - Nonsense (onDisk <= 0) → refuse (ErrHomeFormatInvalid).
func GateHomeFormat(found bool, onDisk, current int, allowNewer bool) (HomeFormatDecision, error) {
	if current <= 0 {
		return HomeFormatDecision{}, fmt.Errorf("memops: current home format %d: %w", current, ErrHomeFormatInvalid)
	}

	var d HomeFormatDecision
	if !found {
		onDisk = version.UnversionedHomeFormat
		d.StampFormat = version.UnversionedHomeFormat
	}
	if onDisk <= 0 {
		return HomeFormatDecision{}, fmt.Errorf("memops: home format %d on disk: %w", onDisk, ErrHomeFormatInvalid)
	}
	d.Format = onDisk

	switch {
	case onDisk == current:
		return d, nil
	case onDisk > current:
		if !allowNewer {
			return HomeFormatDecision{}, fmt.Errorf(
				"memops: home format %d, this personant understands %d: the home was written by a newer personant — upgrade personant, or re-run with the allow-newer override to open it anyway (at your own risk): %w",
				onDisk, current, ErrHomeFormatNewer)
		}
		d.NewerAccepted = true
		return d, nil
	default:
		pos := onDisk
		for pos < current {
			next, ok := nextHomeFormat(pos)
			if !ok || next > current {
				return HomeFormatDecision{}, fmt.Errorf(
					"memops: home format %d, this personant writes %d, no migration step from format %d: %w",
					onDisk, current, pos, ErrHomeFormatNoMigration)
			}
			pos = next
		}
		d.Format = current
		d.StampFormat = current
		d.MigratedFrom = onDisk
		return d, nil
	}
}

// nextHomeFormat returns the format produced by the registered step that
// reads `from`.
func nextHomeFormat(from int) (int, bool) {
	for _, m := range homeFormatMigrations {
		if m.From == from && m.To > from {
			return m.To, true
		}
	}
	return 0, false
}
