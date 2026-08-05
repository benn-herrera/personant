// Package version carries personant's version identity: the two
// independent semver lines, the home's on-disk format revision, and the
// build stamp Go's toolchain embeds.
//
// # Three version lines (SPEC §9.1)
//
//   - Substrate — the runtime + the MemoryOps port. Semver.
//   - FrontEnd — U/X and feature logic. Semver. During the living-with
//     phase every front-end feature change bumps the PATCH digit; MINOR
//     bumps are developer fiat at milestone gates.
//   - CurrentHomeFormat — the canonical on-disk layout revision of
//     $PERSONANT_HOME. A plain integer, deliberately NOT semver: an
//     on-disk layout is either readable by this binary or it is not, so
//     there is nothing for a three-part version to express.
//
// # Leaf package
//
// This package imports only the standard library. internal/store,
// internal/memops, internal/eventlog and cmd/ import it; it imports none
// of them. Keeping it a leaf is what lets the substrate stamp a version
// without dragging the substrate into every consumer of the version.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Substrate is the version of the runtime + MemoryOps port.
const Substrate = "0.2.0"

// FrontEnd is the version of the U/X + feature-logic line.
const FrontEnd = "0.0.17"

// CurrentHomeFormat is the home on-disk format revision this binary
// writes and understands.
const CurrentHomeFormat = 1

// UnversionedHomeFormat is the format a home is ASSUMED to be when it
// carries no format stamp — i.e. a home written before versioning
// existed.
//
// It is deliberately a SEPARATE constant, never an alias of
// CurrentHomeFormat, and its value must stay pinned at 1 forever. When
// CurrentHomeFormat later becomes 2, an unversioned home must resolve to
// 1 so that it takes its migration; resolving it to "whatever is current"
// would silently declare an old, unmigrated home modern and skip the
// migration that makes it readable. The two constants coincide today
// only because there has been exactly one format.
const UnversionedHomeFormat = 1

// Unknown is what the rendered forms show for an absent build stamp.
const Unknown = "unknown"

// shortRevisionLen is the abbreviated-commit width used everywhere a
// commit is rendered compactly (short form, event log).
const shortRevisionLen = 7

// Build is the VCS/build stamp Go records at link time. Under
// `make build` the toolchain fills vcs.revision / vcs.modified / vcs.time
// automatically — there are no git tags and no ldflags in this repo.
//
// The zero Build is the legitimate UNSTAMPED state: `go test` binaries
// carry no VCS settings, and neither does a `go run`. Every consumer must
// degrade gracefully rather than treat it as an error.
type Build struct {
	Revision string // full commit sha; empty when unstamped
	Dirty    bool   // working tree was modified at build time
	Time     string // RFC3339 build timestamp; empty when unstamped
}

// ReadBuild returns this binary's build stamp. Absent build info yields
// the zero Build — it never panics and never errors, because "I was not
// built with a VCS stamp" is a normal condition, not a failure.
func ReadBuild() Build {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Build{}
	}
	var b Build
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.modified":
			b.Dirty = s.Value == "true"
		case "vcs.time":
			b.Time = s.Value
		}
	}
	return b
}

// ShortRevision returns the abbreviated commit, or "" when unstamped.
func (b Build) ShortRevision() string {
	if len(b.Revision) <= shortRevisionLen {
		return b.Revision
	}
	return b.Revision[:shortRevisionLen]
}

// CommitLabel returns the abbreviated commit with a "-dirty" suffix when
// the tree was modified at build time, or Unknown when unstamped. It is
// the one rendering of a commit shared by the short form and the
// system.bootstrap event line.
func (b Build) CommitLabel() string {
	rev := b.ShortRevision()
	if rev == "" {
		return Unknown
	}
	if b.Dirty {
		return rev + "-dirty"
	}
	return rev
}

// Short returns the one-line version identity, e.g.
//
//	personant 0.1.0 (front end 0.0.3, commit abc1234, home format 1)
//
// The commit clause is omitted entirely when the binary is unstamped.
func Short() string { return shortForm(ReadBuild()) }

// Long returns the multi-line version identity: aligned key/value lines
// for both version lines, the home format, the commit, the build time and
// the Go toolchain/platform. Every line is newline-terminated, so it can
// be printed as-is.
func Long() string { return longForm(ReadBuild()) }

// shortForm / longForm are the pure renderers — Build in, string out —
// so the formatting is testable at both the stamped and unstamped ends
// without a linked-in VCS stamp to fake.
func shortForm(b Build) string {
	parts := []string{"front end " + FrontEnd}
	if b.ShortRevision() != "" {
		parts = append(parts, "commit "+b.CommitLabel())
	}
	parts = append(parts, fmt.Sprintf("home format %d", CurrentHomeFormat))
	return fmt.Sprintf("personant %s (%s)", Substrate, strings.Join(parts, ", "))
}

func longForm(b Build) string {
	commit := Unknown
	if b.Revision != "" {
		commit = b.Revision
		if b.Dirty {
			commit += " (dirty)"
		}
	}
	built := b.Time
	if built == "" {
		built = Unknown
	}
	rows := [][2]string{
		{"substrate", Substrate},
		{"front end", FrontEnd},
		{"home format", fmt.Sprintf("%d", CurrentHomeFormat)},
		{"commit", commit},
		{"built", built},
		{"go", fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)},
	}
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString(Row(r[0], r[1]))
	}
	return sb.String()
}
