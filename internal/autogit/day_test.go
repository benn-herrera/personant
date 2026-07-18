package autogit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDayTrailer_RoundTrip(t *testing.T) {
	msg := DayCommitMessage(42)
	day, ok := ParseDayTrailer(msg)
	if !ok || day != 42 {
		t.Fatalf("ParseDayTrailer(DayCommitMessage(42)) = (%d,%v)", day, ok)
	}
	if !isDayCommitMessage(msg, 42) {
		t.Error("DayCommitMessage(42) does not satisfy its own shape guard")
	}
	if isDayCommitMessage(msg, 41) {
		t.Error("shape guard matched the wrong day")
	}
}

// TestDayTrailer_ShapeGuardRejectsTraileredNonDayShape is the §4
// detection-vs-guard split: every primary commit carries the trailer
// (F3), so a trailer-only guard would let an adopt or roll-forward
// stamp falsely satisfy day-commit idempotence and WEDGE the real
// day-commit.
func TestDayTrailer_ShapeGuardRejectsTraileredNonDayShape(t *testing.T) {
	for _, msg := range []string{
		WithDayTrailer("recovery: adopt pre-existing content", 42),
		WithDayTrailer("archive: stamp 3 index entries (roll-forward)", 42),
		WithDayTrailer("archive: capture 2 thread(s)", 42),
	} {
		if day, ok := ParseDayTrailer(msg); !ok || day != 42 {
			t.Errorf("detection lost the trailer on %q: (%d,%v)", msg, day, ok)
		}
		if isDayCommitMessage(msg, 42) {
			t.Errorf("non-day-shape message satisfied the INV-2 guard: %q", msg)
		}
	}
}

func TestParseDayTrailer_Malformed(t *testing.T) {
	cases := []string{
		"day 5\n",                       // subject only, no trailer
		"nothing here",                  //
		"Personant-Day: not-a-number",   // malformed value
		"x\n\nPersonant-Turn: t9-123\n", // wrong trailer key
	}
	for _, msg := range cases {
		if day, ok := ParseDayTrailer(msg); ok {
			t.Errorf("ParseDayTrailer(%q) = (%d,true), want ⊥", msg, day)
		}
	}
}

func TestDayIndexOf_UTCWholeDays(t *testing.T) {
	d0 := DayIndexOf(time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	dLate := DayIndexOf(time.Date(2026, 5, 29, 23, 59, 59, 0, time.UTC))
	d1 := DayIndexOf(time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC))
	if d0 != dLate {
		t.Errorf("same UTC day split: %d vs %d", d0, dLate)
	}
	if d1 != d0+1 {
		t.Errorf("next UTC day = %d, want %d", d1, d0+1)
	}
	// Offset-carrying instants normalize to UTC.
	off := DayIndexOf(time.Date(2026, 5, 29, 23, 0, 0, 0, time.FixedZone("m8", -8*3600)))
	if off != d1 {
		t.Errorf("offset instant (=2026-05-30T07:00Z) day = %d, want %d", off, d1)
	}
}

// TestHeadDay_AcrossRepos: the day trailer lives on PRIMARY commits;
// daily commits never carry it (⊥).
func TestHeadDay_AcrossRepos(t *testing.T) {
	paths := scaffoldHome(t)
	ctx := context.Background()

	// Fresh init: primary HEAD (init commit) carries no trailer.
	if _, ok, err := HeadDay(ctx, paths, Primary); err != nil || ok {
		t.Fatalf("HeadDay on trailerless primary = (ok=%v, err=%v), want ⊥", ok, err)
	}

	// A trailered primary commit defines HEADDAY.
	if err := os.WriteFile(filepath.Join(paths.Home, "note.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Add(ctx, paths, Primary, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := CommitAllowEmpty(ctx, paths, Primary, DayCommitMessage(7), 0, 0); err != nil {
		t.Fatalf("day commit: %v", err)
	}
	day, ok, err := HeadDay(ctx, paths, Primary)
	if err != nil || !ok || day != 7 {
		t.Fatalf("HeadDay = (%d,%v,%v), want 7", day, ok, err)
	}
	isDay, err := HeadIsDayCommit(ctx, paths, Primary, 7)
	if err != nil || !isDay {
		t.Fatalf("HeadIsDayCommit(7) = (%v,%v), want true", isDay, err)
	}
	if isDay, _ := HeadIsDayCommit(ctx, paths, Primary, 8); isDay {
		t.Error("HeadIsDayCommit(8) true on a day-7 HEAD")
	}

	// Daily HEAD (baseline) never carries the day trailer.
	if _, ok, err := HeadDay(ctx, paths, Daily); err != nil || ok {
		t.Errorf("HeadDay on daily = (ok=%v, err=%v), want ⊥", ok, err)
	}
}

// TestProbeDaily_States covers the DAILY observable (F7): present on an
// Init'd home; missing after a nuke; half-created for a storage-only or
// garbage git-dir — never an error.
func TestProbeDaily_States(t *testing.T) {
	paths := scaffoldHome(t)
	if st := ProbeDaily(paths); st != DailyPresent {
		t.Fatalf("fresh home daily = %v, want present", st)
	}
	if err := NukeDaily(paths); err != nil {
		t.Fatalf("NukeDaily: %v", err)
	}
	if st := ProbeDaily(paths); st != DailyMissing {
		t.Fatalf("nuked daily = %v, want missing", st)
	}
	// Storage init without a baseline commit = half-created.
	if err := InitDaily(context.Background(), paths); err != nil {
		t.Fatalf("InitDaily: %v", err)
	}
	if st := ProbeDaily(paths); st != DailyHalfCreated {
		t.Fatalf("headless daily = %v, want half-created", st)
	}
	// Garbage dir = half-created, never an error.
	if err := NukeDaily(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.GitDaily, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.GitDaily, "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := ProbeDaily(paths); st != DailyHalfCreated {
		t.Fatalf("corrupt daily = %v, want half-created (F7)", st)
	}
}

// TestDualHandles_ShareOneWorktree: a commit into DAILY does not move
// PRIMARY's HEAD and vice versa, while both see the same worktree
// bytes — the dual-repo split's foundational property.
func TestDualHandles_ShareOneWorktree(t *testing.T) {
	paths := scaffoldHome(t)
	ctx := context.Background()
	priBefore, err := HeadHash(ctx, paths, Primary)
	if err != nil {
		t.Fatalf("primary HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.Home, "shared.md"), []byte("one worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Add(ctx, paths, Daily, "."); err != nil {
		t.Fatalf("daily Add: %v", err)
	}
	if err := Commit(ctx, paths, Daily, "daily-only", 0, 0); err != nil {
		t.Fatalf("daily Commit: %v", err)
	}
	if got, _ := HeadHash(ctx, paths, Primary); got != priBefore {
		t.Error("a DAILY commit moved PRIMARY HEAD")
	}
	// Primary can then commit the same worktree state independently.
	if err := Add(ctx, paths, Primary, "."); err != nil {
		t.Fatalf("primary Add: %v", err)
	}
	if err := Commit(ctx, paths, Primary, "primary-capture", 0, 0); err != nil {
		t.Fatalf("primary Commit: %v", err)
	}
	// Neither repo sees the other's git-dir as tracked or dirty (INV-6).
	for _, which := range []Repo{Primary, Daily} {
		wt, err := Worktree(ctx, paths, which)
		if err != nil {
			t.Fatalf("Worktree(%v): %v", which, err)
		}
		for _, p := range append(append([]string{}, wt.DirtyPaths...), wt.Untracked...) {
			if p == ".git" || p == ".git-daily" ||
				len(p) > 9 && (p[:5] == ".git/" || p[:11] == ".git-daily/") {
				t.Errorf("%v status leaked a git-dir path: %s (INV-6)", which, p)
			}
		}
	}
}
