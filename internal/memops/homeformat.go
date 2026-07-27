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
	// migration path. Deliberately unreachable today (see GateHomeFormat).
	ErrHomeFormatNoMigration = errors.New("no migration path registered for this home format")

	// ErrHomeFormatInvalid reports a nonsense revision (zero or negative).
	ErrHomeFormatInvalid = errors.New("home format is not a valid revision")
)

// HomeFormatDecision is what the caller does next after a successful gate.
// A non-nil error from GateHomeFormat means REFUSE; a nil error means
// proceed, subject to the fields below.
type HomeFormatDecision struct {
	// Format is the home's effective on-disk revision — the value read
	// from the stamp, or version.UnversionedHomeFormat when there was
	// none. Callers log this (system.bootstrap), not the constant.
	Format int

	// StampFormat, when nonzero, is the revision the caller must write to
	// the home's format stamp before proceeding (adopt-forward). The gate
	// performs no I/O, so the write is the caller's to make.
	StampFormat int

	// NewerAccepted reports that the home was written by a NEWER personant
	// and `allowNewer` let it through. The caller must emit a loud warning
	// and an event-log entry — this is a knowingly-unsafe open, not a
	// normal one.
	NewerAccepted bool
}

// GateHomeFormat decides whether a home whose stamp reads (found, onDisk)
// may be opened by a binary at revision `current`.
//
//   - No stamp → adopt-forward: the home predates versioning, so it is
//     taken to be version.UnversionedHomeFormat and the caller stamps it
//     as such. Note this then falls through the SAME comparison arms
//     below: today (current == 1) that proceeds, and when current advances
//     it will correctly refuse an un-migrated legacy home rather than
//     waving it through — which is the entire reason
//     UnversionedHomeFormat is a separate pinned constant rather than an
//     alias of CurrentHomeFormat.
//   - Equal → proceed.
//   - Newer on disk → refuse (ErrHomeFormatNewer) unless allowNewer, in
//     which case proceed with NewerAccepted set.
//   - Older on disk → refuse (ErrHomeFormatNoMigration). DELIBERATELY
//     UNREACHABLE today: there is exactly one format, so onDisk < current
//     cannot happen. The shape is defined now so the first real migration
//     changes a constant and registers a migration, not the gate's
//     structure.
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
		return HomeFormatDecision{}, fmt.Errorf(
			"memops: home format %d, this personant writes %d: %w",
			onDisk, current, ErrHomeFormatNoMigration)
	}
}
