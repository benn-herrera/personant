package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"personant/internal/store"
	"personant/internal/version"
)

// Versioning CLI surface (SPEC §9.1): `--version` / `version`, and the
// un-bypassable home on-disk format gate with its --allow-newer-home
// override.

// writeHomeFormat stamps a raw format revision onto an existing home,
// standing in for "a home a newer personant wrote".
func writeHomeFormat(t *testing.T, home string, format int) {
	t.Helper()
	paths := store.PathsForHome(home)
	if err := os.WriteFile(paths.HomeVersion, []byte("format = "+strconv.Itoa(format)+"\n"), 0o644); err != nil {
		t.Fatalf("write version.toml: %v", err)
	}
}

// TestVersionFlagPrintsShortFormOnly: `personant --version` prints
// version.Short() and nothing else — cobra's default template would wrap
// it in a second "personant version X" line.
func TestVersionFlagPrintsShortFormOnly(t *testing.T) {
	out := runCLI(t, t.TempDir(), "--version")
	if out != version.Short()+"\n" {
		t.Errorf("--version = %q, want %q", out, version.Short()+"\n")
	}
}

// TestVersionSubcommandOnAbsentHome: `version` is the diagnostic you run
// when the home is missing — it reports the absence and still exits 0.
func TestVersionSubcommandOnAbsentHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "never-created")
	out := runCLI(t, home, "version")

	if !strings.Contains(out, version.Long()) {
		t.Errorf("version output missing the long form:\n%s", out)
	}
	if !strings.Contains(out, "home:        "+home) {
		t.Errorf("version output missing the resolved home path:\n%s", out)
	}
	if !strings.Contains(out, "absent") {
		t.Errorf("version output does not report an absent stamp:\n%s", out)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("version created something at %s (stat err=%v); it must never write", home, err)
	}
}

// TestVersionSubcommandNeverGates: a home newer than this binary REFUSES
// every gated verb, but `version` still reports it and exits 0 — that is
// the whole point of the diagnostic.
func TestVersionSubcommandNeverGates(t *testing.T) {
	home := t.TempDir()
	runCLI(t, home, "init", "--quiet")
	writeHomeFormat(t, home, 99)

	if err := execCLI(home, "archive", "list"); err == nil {
		t.Fatal("archive list on a newer home succeeded; the gate is bypassable")
	}
	out := runCLI(t, home, "version")
	if !strings.Contains(out, "home stamp:  99") {
		t.Errorf("version did not report the on-disk format 99:\n%s", out)
	}
}

// TestVersionSubcommandReportsUnreadableStamp: a corrupt stamp is text in
// the report, not a failure.
func TestVersionSubcommandReportsUnreadableStamp(t *testing.T) {
	home := t.TempDir()
	runCLI(t, home, "init", "--quiet")
	if err := os.WriteFile(store.PathsForHome(home).HomeVersion, []byte("not toml"), 0o644); err != nil {
		t.Fatalf("corrupt version.toml: %v", err)
	}
	out := runCLI(t, home, "version")
	if !strings.Contains(out, "unreadable:") {
		t.Errorf("version did not report an unreadable stamp:\n%s", out)
	}
}

// TestGateRefusesNewerHome: every verb that opens the substrate goes
// through the shared helper, so none of them can be used to sidestep the
// refusal.
func TestGateRefusesNewerHome(t *testing.T) {
	home := t.TempDir()
	runCLI(t, home, "init", "--quiet")
	writeHomeFormat(t, home, 99)

	for _, args := range [][]string{
		{"verify", "--quiet"},
		{"index", "check"},
		{"archive", "list"},
		{"init", "--quiet"},
	} {
		err := execCLI(home, args...)
		if err == nil || !strings.Contains(err.Error(), "newer personant") {
			t.Errorf("%v on a newer home = %v, want the newer-home refusal", args, err)
		}
	}

	// `init` refused BEFORE scaffolding, so it did not overwrite the stamp.
	if _, format, err := store.ReadHomeFormat(store.PathsForHome(home)); err != nil || format != 99 {
		t.Errorf("stamp after refused init = (%d, %v), want 99", format, err)
	}
}

// TestAllowNewerHomeOverrideLeavesATrace: the override works, and using it
// is recorded in the event log as well as on stderr — a knowingly-unsafe
// open must be reconstructable after the fact.
func TestAllowNewerHomeOverrideLeavesATrace(t *testing.T) {
	home := t.TempDir()
	runCLI(t, home, "init", "--quiet")
	writeHomeFormat(t, home, 99)

	if err := execCLI(home, "archive", "list", "--allow-newer-home"); err != nil {
		t.Fatalf("archive list --allow-newer-home: %v", err)
	}

	logs := readHomeLogs(t, home)
	if !strings.Contains(logs, "system.home-format-override on-disk=99 binary=1") {
		t.Errorf("event log missing the override trace:\n%s", logs)
	}
	// system.bootstrap carries the EFFECTIVE revision the session ran
	// against, not the constant this binary writes.
	if !strings.Contains(logs, "home-format=99") {
		t.Errorf("event log bootstrap line does not carry the effective format:\n%s", logs)
	}
}

// TestUnversionedHomeIsAdoptedForward: a home written before versioning
// existed is stamped at version.UnversionedHomeFormat on the next open.
func TestUnversionedHomeIsAdoptedForward(t *testing.T) {
	home := t.TempDir()
	runCLI(t, home, "init", "--quiet")
	if err := os.Remove(store.PathsForHome(home).HomeVersion); err != nil {
		t.Fatalf("remove version.toml: %v", err)
	}

	if err := execCLI(home, "archive", "list"); err != nil {
		t.Fatalf("archive list on an unversioned home: %v", err)
	}

	found, format, err := store.ReadHomeFormat(store.PathsForHome(home))
	if err != nil || !found || format != version.UnversionedHomeFormat {
		t.Errorf("stamp after adopt-forward = (%v, %d, %v), want (true, %d, nil)",
			found, format, err, version.UnversionedHomeFormat)
	}
}

func readHomeLogs(t *testing.T, home string) string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(store.PathsForHome(home).LogsDir, "*.log"))
	if err != nil {
		t.Fatalf("glob logs: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(e)
		if err != nil {
			t.Fatalf("read %s: %v", e, err)
		}
		b.Write(data)
	}
	return b.String()
}
