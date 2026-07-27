package eventlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/store"
	"personant/internal/version"
)

func TestLogBootstrapLine(t *testing.T) {
	home := t.TempDir()
	paths := store.PathsForHome(home)

	fixed := time.Date(2026, 5, 8, 2, 55, 44, 0, time.FixedZone("PDT", -7*3600))
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	if err := LogBootstrap(paths, 4); err != nil {
		t.Fatalf("LogBootstrap: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(paths.LogsDir, "2026-05-08.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := strings.TrimSuffix(string(data), "\n")

	if !strings.HasPrefix(line, fixed.Format(time.RFC3339)+" system.bootstrap ") {
		t.Errorf("line does not carry the §2.8 timestamp + system.bootstrap prefix: %q", line)
	}
	for _, want := range []string{
		"version=" + version.Substrate,
		"frontend=" + version.FrontEnd,
		// The EFFECTIVE on-disk format the gate resolved, not the constant.
		"home-format=4",
		"commit=" + version.ReadBuild().CommitLabel(),
		"home=" + home,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("bootstrap line %q missing %q", line, want)
		}
	}
}

// TestLogBootstrapCommitDegrades: an unstamped binary (every `go test`
// build is one) still emits the commit field, as "unknown", so the line's
// shape is constant for scrapers.
func TestLogBootstrapCommitDegrades(t *testing.T) {
	if version.ReadBuild().Revision != "" {
		t.Skip("binary carries a VCS stamp; the degrade path is not observable here")
	}
	paths := store.PathsForHome(t.TempDir())
	fixed := time.Date(2026, 5, 8, 2, 55, 44, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	if err := LogBootstrap(paths, version.CurrentHomeFormat); err != nil {
		t.Fatalf("LogBootstrap: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(paths.LogsDir, "2026-05-08.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	want := fmt.Sprintf("commit=%s", version.Unknown)
	if !strings.Contains(string(data), want) {
		t.Errorf("bootstrap line %q missing %q", data, want)
	}
}
