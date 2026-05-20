package scenarios

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runTimestampSuffixRE matches a trailing `.<12 digits>` run-timestamp
// suffix — Go layout `060102150405` (yymmddhhMMss). Compiled once at
// package scope, consistent with the userTagRE-style pattern elsewhere.
var runTimestampSuffixRE = regexp.MustCompile(`\.\d{12}$`)

// runDataHome resolves and prepares the persistent per-scenario run
// home: <repo-root>/test/rundata/<scenario-name>/. Run data is forensic
// data — it deliberately does NOT live under t.TempDir() (which Go
// auto-deletes) so a developer can inspect spine/threads/logs/metrics
// after an interesting or failing run. The directory is cleared and
// recreated at the start of each run so it always holds the latest run
// of that scenario; no cleanup is registered, so it persists after the
// test exits. test/rundata/ is gitignored.
//
// The `<name>.<12-digit-suffix>` shape applies ONLY to simulation-run
// directories — the ones named `sim-workload-...` (produced by
// GenerateWorkload). runSimRung stamps a real wall-clock timestamp suffix
// so each sim run gets a unique directory; a sim-workload run that
// arrives un-suffixed (e.g. TestSim_DormantResumptionDrivesMidTurnFetch,
// which calls RunScenario directly) is given the all-zeros sentinel
// `.000000000000`. The sentinel is deliberate: it marks a sim directory
// whose run was NOT stamped with a real timestamp, and because it is
// constant the directory name is stable across runs, so that scenario
// overwrites in place (no accumulation). Non-sim scenario directories
// (decay-triggered-closure, recall-madlibs-*, project-switching, etc.)
// get NO suffix at all — bare names. Sim scenario names never
// legitimately end in `.<12 digits>`, so detecting an existing suffix is
// safe and avoids double-suffixing.
func runDataHome(t *testing.T, name string) string {
	t.Helper()
	if strings.HasPrefix(name, "sim-workload-") && !runTimestampSuffixRE.MatchString(name) {
		name += ".000000000000"
	}
	home := filepath.Join(repoRoot(t), "test", "rundata", name)
	if err := os.RemoveAll(home); err != nil {
		t.Fatalf("scenario %s: clear run home %s: %v", name, home, err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("scenario %s: create run home %s: %v", name, home, err)
	}
	return home
}

// measurementBlobPath resolves a persistent path for a standalone
// measurement's metrics blob (tests that write a metrics blob directly
// rather than driving the scenario harness). The blob is forensic data:
// it lives under <repo-root>/test/rundata/ — gitignored, never
// auto-deleted — alongside the per-scenario run homes. The parent
// directory is created if absent.
func measurementBlobPath(t *testing.T, filename string) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "test", "rundata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create rundata dir %s: %v", dir, err)
	}
	return filepath.Join(dir, filename)
}

// repoRoot walks up from the test's working directory until it finds a
// go.mod file, returning that directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root (containing go.mod) not found from %s", dir)
		}
		dir = parent
	}
}
