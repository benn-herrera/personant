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

// TestLogBootstrapHealsTornTailFirst: LogBootstrap is the first event a
// process writes and it lands BEFORE Reconcile's phase-0 heal, so it must
// carry the heal itself — otherwise it fuses its record onto a torn final
// line and destroys both records.
func TestLogBootstrapHealsTornTailFirst(t *testing.T) {
	home := t.TempDir()
	paths := store.PathsForHome(home)

	fixed := time.Date(2026, 5, 8, 2, 55, 44, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	day := filepath.Join(paths.LogsDir, "2026-05-08.log")
	if err := os.MkdirAll(paths.LogsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	const torn = "2026-05-08T02:00:00Z thread.engaged thr_1 turn_cou"
	if err := os.WriteFile(day, []byte(torn), 0o644); err != nil {
		t.Fatalf("seed torn tail: %v", err)
	}

	if err := LogBootstrap(paths, version.CurrentHomeFormat); err != nil {
		t.Fatalf("LogBootstrap: %v", err)
	}

	data, err := os.ReadFile(day)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2 (torn fragment + bootstrap): %q", len(lines), data)
	}
	// The fragment is preserved byte-exact — the heal is additive, so
	// Reconcile still finds and reports it.
	if lines[0] != torn {
		t.Errorf("torn fragment = %q, want it preserved as %q", lines[0], torn)
	}
	if !strings.HasPrefix(lines[1], fixed.Format(time.RFC3339)+" system.bootstrap ") {
		t.Errorf("bootstrap line was merged or malformed: %q", lines[1])
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
