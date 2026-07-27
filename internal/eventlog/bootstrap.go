package eventlog

import (
	"fmt"
	"path/filepath"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/version"
)

// LogBootstrap emits the SPEC §2.8 system.bootstrap event — the one line
// that records which binary opened which home, and in what layout
// revision it found it. Emitted once per session open, after the §9.1
// home-format gate has resolved the effective format.
//
//	<ts> system.bootstrap version=0.1.0 frontend=0.0.2 home-format=1 commit=abc1234 home=/home/user/.personant
//
// homeFormat is the EFFECTIVE on-disk revision the gate resolved
// (memops.HomeFormatDecision.Format), not version.CurrentHomeFormat: on
// an allow-newer open the two differ, and the forensic value of the line
// is knowing which one the session actually ran against. The commit
// degrades to "unknown" on an unstamped binary rather than dropping the
// field, so the line's shape is constant for log scrapers.
func LogBootstrap(paths store.PersonantPaths, homeFormat int) error {
	// Heal first. This line is BY CONSTRUCTION the first event a process
	// writes, and it lands BEFORE Reconcile — the §9.1 format gate must
	// precede crash recovery, so recovery's phase-0 log-tail heal has not
	// run yet. A crash mid-append leaves the day's log without a trailing
	// newline; a plain append would fuse this record onto that fragment,
	// which is exactly the merge HealTail exists to prevent. The heal is
	// additive (never truncates), so the torn fragment survives on disk for
	// Reconcile to find and report moments later.
	if err := HealTail(filepath.Join(paths.LogsDir, dayFileName(clock.Timeline()))); err != nil {
		return fmt.Errorf("eventlog: LogBootstrap: %w", err)
	}
	details := fmt.Sprintf("version=%s frontend=%s home-format=%d commit=%s home=%s",
		version.Substrate,
		version.FrontEnd,
		homeFormat,
		version.ReadBuild().CommitLabel(),
		paths.Home,
	)
	return Log(paths, memops.LogCategorySystem, "bootstrap", details)
}
