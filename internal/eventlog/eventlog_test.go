package eventlog

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/store"
)

func TestLogWritesLine(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)

	fixed := time.Date(2026, 5, 9, 14, 23, 5, 0, time.FixedZone("PST", -8*3600))
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	if err := Log(paths, "session", "started", "version=0.1.0"); err != nil {
		t.Fatalf("Log: %v", err)
	}

	expected := filepath.Join(paths.LogsDir, "2026-05-09.log")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(data)
	want := fixed.Format(time.RFC3339) + " session.started version=0.1.0\n"
	if got != want {
		t.Fatalf("line mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestLogRotatesAcrossDays(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)

	day1 := time.Date(2026, 5, 9, 23, 59, 30, 0, time.UTC)
	day2 := time.Date(2026, 5, 10, 0, 0, 30, 0, time.UTC)
	current := day1
	restore := clock.SetTimeline(func() time.Time { return current })
	defer restore()

	if err := Log(paths, "system", "bootstrap", "n=1"); err != nil {
		t.Fatalf("day1 log: %v", err)
	}
	current = day2
	if err := Log(paths, "system", "bootstrap", "n=2"); err != nil {
		t.Fatalf("day2 log: %v", err)
	}

	for _, c := range []struct {
		name    string
		matches string
	}{
		{name: "2026-05-09.log", matches: "system.bootstrap n=1"},
		{name: "2026-05-10.log", matches: "system.bootstrap n=2"},
	} {
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, c.name))
		if err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		if !strings.Contains(string(data), c.matches) {
			t.Errorf("%s missing %q; got %q", c.name, c.matches, string(data))
		}
	}
}

func TestLogConcurrentWritersDoNotTear(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)

	fixed := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	const writers = 8
	const perWriter = 64
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if err := Log(paths, "test", "concurrent", "w="+strconv.Itoa(i)+" j="+strconv.Itoa(j)); err != nil {
					t.Errorf("Log: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(paths.LogsDir, "2026-05-09.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if got, want := len(lines), writers*perWriter; got != want {
		t.Fatalf("line count: got %d want %d", got, want)
	}
	prefix := fixed.Format(time.RFC3339) + " test.concurrent "
	for i, ln := range lines {
		if !strings.HasPrefix(ln, prefix) {
			t.Fatalf("line %d torn or malformed: %q", i, ln)
		}
	}
}

func TestLogContextModified(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	fixed := time.Date(2026, 5, 9, 9, 0, 0, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	if err := LogContextModified(paths, "user.prompt", 137); err != nil {
		t.Fatalf("LogContextModified: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(paths.LogsDir, "2026-05-09.log"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "context.modified source=user.prompt bytes=137"
	if !strings.Contains(string(data), want) {
		t.Errorf("missing %q in %q", want, string(data))
	}
}

func TestLogRejectsBadInput(t *testing.T) {
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	cases := []struct {
		name             string
		category, action string
	}{
		{"empty category", "", "x"},
		{"empty action", "x", ""},
		{"category whitespace", "a b", "x"},
		{"action whitespace", "x", "a\tb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Log(paths, c.category, c.action, ""); err == nil {
				t.Fatalf("expected error for %s", c.name)
			}
		})
	}
}
