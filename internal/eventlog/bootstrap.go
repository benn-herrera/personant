package eventlog

import (
	"fmt"

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
	details := fmt.Sprintf("version=%s frontend=%s home-format=%d commit=%s home=%s",
		version.Substrate,
		version.FrontEnd,
		homeFormat,
		version.ReadBuild().CommitLabel(),
		paths.Home,
	)
	return Log(paths, memops.LogCategorySystem, "bootstrap", details)
}
