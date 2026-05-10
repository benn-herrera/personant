// Package eventlog appends events to the daily-rotated plain-text event
// log under <Home>/logs/YYYY-MM-DD.log (spec §2.8).
//
// Each line has the shape:
//
//	<RFC3339-timestamp> <category>.<action> <free-form-details>
//
// The package is intentionally small: open-append-close per line,
// guarded by a process-wide mutex. The day's file is created on demand.
// Concurrency is a single-process v0.1 concern; performance is a
// non-goal — write volume is dominated by user-paced turn events.
package eventlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"personant/internal/store"
)

// nowFn is the time source used to timestamp events and pick the day's
// file. Overridable from tests via SetClock; production callers should
// leave it alone.
var (
	clockMu sync.Mutex
	nowFn   = time.Now
)

// SetClock replaces the time source used for timestamps and file
// rotation. Returns a restore function that reinstalls the previous
// source. For test use only.
func SetClock(fn func() time.Time) func() {
	clockMu.Lock()
	defer clockMu.Unlock()
	prev := nowFn
	nowFn = fn
	return func() {
		clockMu.Lock()
		defer clockMu.Unlock()
		nowFn = prev
	}
}

func currentTime() time.Time {
	clockMu.Lock()
	defer clockMu.Unlock()
	return nowFn()
}

// writeMu serializes the open-append-close cycle so concurrent writers
// from the same process do not interleave their bytes mid-line.
var writeMu sync.Mutex

// Log appends one event line to <Home>/logs/<YYYY-MM-DD>.log.
//
// Creates the day's file (and the logs directory) if missing. category
// and action are joined with "." per spec §2.8 vocabulary; details is
// free-form. category and action must be non-empty and contain no
// whitespace; details may be empty (the line still gets a trailing
// space-then-newline).
func Log(paths store.PersonantPaths, category, action, details string) error {
	if category == "" || action == "" {
		return fmt.Errorf("eventlog: category and action are required")
	}
	if containsSpace(category) || containsSpace(action) {
		return fmt.Errorf("eventlog: category/action must not contain whitespace")
	}
	if paths.LogsDir == "" {
		return fmt.Errorf("eventlog: PersonantPaths.LogsDir is empty")
	}

	now := currentTime()
	line := fmt.Sprintf("%s %s.%s %s\n",
		now.Format(time.RFC3339),
		category, action,
		details,
	)

	dayPath := filepath.Join(paths.LogsDir, now.Format("2006-01-02")+".log")

	writeMu.Lock()
	defer writeMu.Unlock()

	if err := os.MkdirAll(paths.LogsDir, 0o755); err != nil {
		return fmt.Errorf("eventlog: mkdir %s: %w", paths.LogsDir, err)
	}
	f, err := os.OpenFile(dayPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("eventlog: open %s: %w", dayPath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("eventlog: write %s: %w", dayPath, err)
	}
	return nil
}

// LogContextModified emits the per-§3.0 chain logging step. source is
// one of the §3.0.1 event names ("user.prompt", "model.response",
// "tool.result", "thread.fetched", ...); byteSize is the delta size in
// bytes.
func LogContextModified(paths store.PersonantPaths, source string, byteSize int) error {
	return Log(paths, "context", "modified", fmt.Sprintf("source=%s bytes=%d", source, byteSize))
}

func containsSpace(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return true
		}
	}
	return false
}
