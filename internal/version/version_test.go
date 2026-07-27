package version

import (
	"strings"
	"testing"
)

// TestUnversionedHomeFormatIsPinned documents the invariant that gives
// UnversionedHomeFormat its reason to exist: it is a SEPARATE constant
// pinned at 1 forever, not an alias of CurrentHomeFormat. The two values
// coincide today (there has been exactly one format), so the test asserts
// the literal — writing `UnversionedHomeFormat = CurrentHomeFormat` would
// pass today and fail here the moment CurrentHomeFormat becomes 2, which
// is precisely when the distinction starts mattering: an unversioned home
// must resolve to 1 and take its migration, not be declared modern.
func TestUnversionedHomeFormatIsPinned(t *testing.T) {
	if UnversionedHomeFormat != 1 {
		t.Errorf("UnversionedHomeFormat = %d, want 1 (pinned forever)", UnversionedHomeFormat)
	}
	if CurrentHomeFormat < UnversionedHomeFormat {
		t.Errorf("CurrentHomeFormat (%d) < UnversionedHomeFormat (%d): formats only move forward",
			CurrentHomeFormat, UnversionedHomeFormat)
	}
}

func TestReadBuildNeverPanics(t *testing.T) {
	// Test binaries carry no VCS settings, so this exercises the
	// unstamped path end-to-end; the assertion is simply that it returns.
	b := ReadBuild()
	if b.Dirty && b.Revision == "" {
		t.Errorf("dirty flag set with no revision: %+v", b)
	}
}

func TestBuildCommitRendering(t *testing.T) {
	tests := []struct {
		name      string
		build     Build
		wantShort string
		wantLabel string
	}{
		{name: "unstamped", build: Build{}, wantShort: "", wantLabel: Unknown},
		{
			name:      "clean",
			build:     Build{Revision: "abc1234def5678"},
			wantShort: "abc1234",
			wantLabel: "abc1234",
		},
		{
			name:      "dirty",
			build:     Build{Revision: "abc1234def5678", Dirty: true},
			wantShort: "abc1234",
			wantLabel: "abc1234-dirty",
		},
		{
			name:      "short revision kept whole",
			build:     Build{Revision: "abc12"},
			wantShort: "abc12",
			wantLabel: "abc12",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.build.ShortRevision(); got != tc.wantShort {
				t.Errorf("ShortRevision() = %q, want %q", got, tc.wantShort)
			}
			if got := tc.build.CommitLabel(); got != tc.wantLabel {
				t.Errorf("CommitLabel() = %q, want %q", got, tc.wantLabel)
			}
		})
	}
}

func TestShortForm(t *testing.T) {
	stamped := shortForm(Build{Revision: "abc1234def", Time: "2026-07-27T00:00:00Z"})
	for _, want := range []string{"personant " + Substrate, "front end " + FrontEnd, "commit abc1234", "home format 1"} {
		if !strings.Contains(stamped, want) {
			t.Errorf("stamped short form %q missing %q", stamped, want)
		}
	}

	// Unstamped: the commit clause degrades by disappearing, not by
	// rendering an empty or bogus value.
	bare := shortForm(Build{})
	if strings.Contains(bare, "commit") {
		t.Errorf("unstamped short form mentions a commit: %q", bare)
	}
	for _, want := range []string{"personant " + Substrate, "front end " + FrontEnd, "home format 1"} {
		if !strings.Contains(bare, want) {
			t.Errorf("unstamped short form %q missing %q", bare, want)
		}
	}
}

func TestLongForm(t *testing.T) {
	stamped := longForm(Build{Revision: "abc1234def", Dirty: true, Time: "2026-07-27T00:00:00Z"})
	for _, want := range []string{
		"substrate:", "front end:", "home format:", "commit:", "built:", "go:",
		Substrate, FrontEnd, "abc1234def", "(dirty)", "2026-07-27T00:00:00Z",
	} {
		if !strings.Contains(stamped, want) {
			t.Errorf("stamped long form missing %q:\n%s", want, stamped)
		}
	}

	// Unstamped: every line is still present, with Unknown standing in for
	// the two build-derived values.
	bare := longForm(Build{})
	lines := strings.Split(strings.TrimSuffix(bare, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("long form has %d lines, want 6:\n%s", len(lines), bare)
	}
	if !strings.Contains(bare, "commit:      "+Unknown) || !strings.Contains(bare, "built:       "+Unknown) {
		t.Errorf("unstamped long form does not degrade to %q:\n%s", Unknown, bare)
	}
	if strings.Contains(bare, "dirty") {
		t.Errorf("unstamped long form claims dirty:\n%s", bare)
	}
}
