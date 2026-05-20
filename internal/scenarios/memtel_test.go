package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
)

// TestMemTelemetry_AppendsParseableSamples is the contract test for the
// memTelemetry writer: each sample is a complete JSON object on its own
// line, the file is created in append mode, and a re-open does not
// truncate prior content. The lifecycle the harness exercises in
// production.
func TestMemTelemetry_AppendsParseableSamples(t *testing.T) {
	dir := t.TempDir()
	m, err := newMemTelemetry(dir)
	if err != nil {
		t.Fatalf("newMemTelemetry: %v", err)
	}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		m.sample(i, now.Add(time.Duration(i)*time.Minute))
	}
	m.close()

	// Re-open and append one more sample; the prior five must still be
	// present (O_APPEND, not O_TRUNC).
	m2, err := newMemTelemetry(dir)
	if err != nil {
		t.Fatalf("newMemTelemetry (reopen): %v", err)
	}
	m2.sample(5, now.Add(5*time.Minute))
	m2.close()

	body, err := os.ReadFile(filepath.Join(dir, memTelemetryFilename))
	if err != nil {
		t.Fatalf("read mem.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("mem.jsonl line count: got %d want 6 (re-open must append, not truncate)", len(lines))
	}
	for i, ln := range lines {
		var rec memSample
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Errorf("line %d: not parseable JSON: %v (line=%q)", i, err, ln)
			continue
		}
		if rec.Step != i {
			t.Errorf("line %d: step got %d want %d", i, rec.Step, i)
		}
		if rec.WallNs == 0 {
			t.Errorf("line %d: wall_ns is zero", i)
		}
		if rec.SysBytes == 0 {
			t.Errorf("line %d: sys_bytes is zero (runtime.MemStats not read?)", i)
		}
	}
}

// TestMemTelemetry_NilReceiverIsNoOp guards the harness's "open
// failure degrades to no-op" path: every method must tolerate a nil
// memTelemetry without panicking.
func TestMemTelemetry_NilReceiverIsNoOp(t *testing.T) {
	var m *memTelemetry
	m.sample(0, clock.Timeline()) // must not panic
	m.flush()               // must not panic
	m.close()               // must not panic
}

// TestMemWatchdog_TripPanicsAndDumps verifies the load-bearing payload of
// the watchdog: trip() panics with the expected message format and writes
// a heap profile + goroutine dump under the rundata directory. The panic
// is what converts a future SIGKILL into a Go stack trace; the pprof
// files are what convert it into an actionable diagnosis.
func TestMemWatchdog_TripPanicsAndDumps(t *testing.T) {
	dir := t.TempDir()
	tel, err := newMemTelemetry(dir)
	if err != nil {
		t.Fatalf("newMemTelemetry: %v", err)
	}
	defer tel.close()

	w := newMemWatchdog(1, tel) // cap=1 byte; any real HeapInuse exceeds it

	// Recover the panic the trip is contractually obliged to produce.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("trip did not panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value not a string: %T %v", r, r)
		}
		// The message must name both the observed HeapInuse and the cap
		// so a future leak hunt can read the verdict from the stack trace
		// alone.
		if !strings.Contains(msg, "memory watchdog") {
			t.Errorf("panic message missing 'memory watchdog' prefix: %q", msg)
		}
		if !strings.Contains(msg, "cap=1") {
			t.Errorf("panic message missing cap=1: %q", msg)
		}

		// The pprof drops must exist alongside the rundata directory so
		// a developer running `ls test/rundata/<scenario>/` sees them.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read rundata dir: %v", err)
		}
		var heapFound, goFound bool
		for _, e := range entries {
			switch {
			case strings.HasPrefix(e.Name(), "heap.pprof."):
				heapFound = true
				if fi, _ := e.Info(); fi != nil && fi.Size() == 0 {
					t.Errorf("heap pprof %s is empty", e.Name())
				}
			case strings.HasPrefix(e.Name(), "goroutine.") && strings.HasSuffix(e.Name(), ".txt"):
				goFound = true
				if fi, _ := e.Info(); fi != nil && fi.Size() == 0 {
					t.Errorf("goroutine dump %s is empty", e.Name())
				}
			}
		}
		if !heapFound {
			t.Errorf("no heap.pprof.* file written to %s", dir)
		}
		if !goFound {
			t.Errorf("no goroutine.*.txt file written to %s", dir)
		}
	}()

	w.trip(2) // simulate observed HeapInuse of 2 bytes (> cap=1)
	t.Fatalf("unreachable: trip must panic")
}

// TestMemWatchdog_StopExitsCleanly proves the goroutine honors its stop
// signal and does not leak. A cap of math.MaxUint64-equivalent (a value
// real HeapInuse will never reach) keeps the loop quiet until the test
// stops it.
func TestMemWatchdog_StopExitsCleanly(t *testing.T) {
	tel, err := newMemTelemetry(t.TempDir())
	if err != nil {
		t.Fatalf("newMemTelemetry: %v", err)
	}
	defer tel.close()

	// cap = ~1 EiB; HeapInuse will never approach this in a test.
	const unreachableCap = uint64(1) << 60
	w := newMemWatchdog(unreachableCap, tel)
	// Override the interval to make the test bounded — the goroutine
	// blocks in select on the ticker, so stop is responsive regardless,
	// but a short interval keeps the test fast under heavy CI load.
	w.interval = 10 * time.Millisecond
	w.start()

	// Give the goroutine a moment to enter its select loop.
	time.Sleep(20 * time.Millisecond)
	w.stopAndWait() // must return promptly

	// Calling stopAndWait twice must be safe (idempotent close).
	w.stopAndWait()
}
