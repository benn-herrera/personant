package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	"personant/internal/clock"
	pnlog "personant/internal/log"
)

// memSampleEveryNSteps is the per-step memory-sample cadence. A 6 m sim
// runs ~65 k steps; N=100 yields ~650 samples per run — enough resolution
// to spot growth curves, low enough overhead. Named so the cadence is a
// single-edit knob.
const memSampleEveryNSteps = 100

// memWatchdogCheckInterval is the wall-clock interval between watchdog
// HeapInuse checks. Long enough that the goroutine is invisible in CPU
// profiles, short enough that a runaway leak trips the cap before the
// macOS jetsam pager kills us.
const memWatchdogCheckInterval = 5 * time.Second

// memTelemetryFilename is the per-scenario rundata sibling of
// <scenario>.metrics.json that holds the per-step memory sample stream.
const memTelemetryFilename = "mem.jsonl"

// memSample is one JSONL record. Field names are stable: the file is a
// forensic-data artifact a future leak hunt will grep and chart.
type memSample struct {
	Step            int    `json:"step"`
	SimClockNs      int64  `json:"sim_clock_ns"`
	WallNs          int64  `json:"wall_ns"`
	AllocBytes      uint64 `json:"alloc_bytes"`
	HeapInUseBytes  uint64 `json:"heap_in_use_bytes"`
	HeapObjects     uint64 `json:"heap_objects"`
	SysBytes        uint64 `json:"sys_bytes"`
	NumGoroutine    int    `json:"num_goroutine"`
	GCPauseNsRecent uint64 `json:"gc_pause_ns_recent"`
}

// memTelemetry is the SIGKILL-survivable per-step memory-sample writer.
//
// Contract:
//   - Append-mode only. Each sample is one os.File.Write of a complete
//     JSON-object line ending in \n. POSIX guarantees small O_APPEND
//     writes are atomic.
//   - f.Sync() after each sample. The last successful append must be on
//     disk before the next step runs — worst case on a kill: lose the
//     in-progress write, never the prior one.
//   - Opened once at scenario start, held through the run, closed at
//     end-of-run.
//
// A zero-value memTelemetry is the "disabled" form — every method becomes
// a no-op. The harness uses that for any scenario that should not write
// mem.jsonl (currently none — every scenario gets the file).
type memTelemetry struct {
	mu   sync.Mutex
	f    *os.File
	dir  string // parent directory; used by the watchdog for pprof drops
	path string // full mem.jsonl path; informational only
}

// newMemTelemetry opens the per-scenario mem.jsonl file in append mode
// under dir. dir is the rundata home, already created by runDataHome.
// Returns an error rather than failing the test directly so the caller
// can decide whether the missing telemetry is fatal.
func newMemTelemetry(dir string) (*memTelemetry, error) {
	path := filepath.Join(dir, memTelemetryFilename)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("memtel: open %s: %w", path, err)
	}
	return &memTelemetry{f: f, dir: dir, path: path}, nil
}

// sample writes one memSample line and fsyncs. step and simClock identify
// the sample; everything else is read live from runtime.MemStats. Errors
// are logged but never returned: a telemetry write failure must not halt
// the run — the run is the load-bearing forensic artifact, the telemetry
// is the supplement.
func (m *memTelemetry) sample(step int, simClock time.Time) {
	if m == nil || m.f == nil {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// PauseNs is a 256-entry circular buffer; freshest pause is at
	// (NumGC + 255) % 256. When NumGC is 0 the slot is 0 too — a benign
	// "no GCs yet" reading.
	recentPause := ms.PauseNs[(ms.NumGC+255)%256]
	rec := memSample{
		Step:            step,
		SimClockNs:      simClock.UnixNano(),
		WallNs:          clock.Profiling().UnixNano(),
		AllocBytes:      ms.Alloc,
		HeapInUseBytes:  ms.HeapInuse,
		HeapObjects:     ms.HeapObjects,
		SysBytes:        ms.Sys,
		NumGoroutine:    runtime.NumGoroutine(),
		GCPauseNsRecent: recentPause,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		pnlog.Warn("memtel: marshal sample step=%d: %v", step, err)
		return
	}
	line = append(line, '\n')
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.f.Write(line); err != nil {
		pnlog.Warn("memtel: write step=%d: %v", step, err)
		return
	}
	if err := m.f.Sync(); err != nil {
		pnlog.Warn("memtel: sync step=%d: %v", step, err)
	}
}

// flush forces an fsync without writing a sample. The watchdog calls
// this before it panics so any sample written just before the cap was
// crossed is guaranteed on disk.
func (m *memTelemetry) flush() {
	if m == nil || m.f == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.f.Sync(); err != nil {
		pnlog.Warn("memtel: flush sync: %v", err)
	}
}

// close writes nothing extra (the caller is expected to take one final
// sample first via sample()) and releases the file handle. Safe on a
// nil receiver.
func (m *memTelemetry) close() {
	if m == nil || m.f == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.f.Close(); err != nil {
		pnlog.Warn("memtel: close %s: %v", m.path, err)
	}
	m.f = nil
}

// memWatchdog is the heap-cap watchdog goroutine. Every
// memWatchdogCheckInterval it reads runtime.MemStats.HeapInuse and, if
// the value exceeds capBytes, captures a heap profile + goroutine dump,
// flushes the mem.jsonl telemetry, and panics — producing a Go stack
// trace via the test harness, which is infinitely more useful than a
// macOS SIGKILL.
//
// Lifetime: started by RunScenario when sc.MemoryCapBytes > 0, stopped
// via close(stop) at end-of-run. The goroutine exits within one tick of
// the stop signal.
type memWatchdog struct {
	cap      uint64
	interval time.Duration
	tel      *memTelemetry
	stop     chan struct{}
	done     chan struct{}
}

// newMemWatchdog returns a watchdog ready to start. tel may be nil
// (telemetry-disabled scenarios); in that case the watchdog still
// captures pprof drops next to the scenario rundata via dir.
func newMemWatchdog(cap uint64, tel *memTelemetry) *memWatchdog {
	return &memWatchdog{
		cap:      cap,
		interval: memWatchdogCheckInterval,
		tel:      tel,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// start launches the watchdog goroutine. It panics from that goroutine
// on cap exceed — Go's test harness will catch the panic and print the
// stack trace, which is the entire point of this instrumentation.
func (w *memWatchdog) start() {
	go w.run()
}

// stopAndWait signals the goroutine to exit and blocks until it does.
// Safe to call multiple times: the stop channel is closed once.
func (w *memWatchdog) stopAndWait() {
	select {
	case <-w.stop:
		// already stopped
	default:
		close(w.stop)
	}
	<-w.done
}

func (w *memWatchdog) run() {
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse >= w.cap {
				w.trip(ms.HeapInuse)
				// Unreachable — trip panics.
				return
			}
		}
	}
}

// trip captures forensic artifacts and panics. The captures are
// best-effort: if pprof fails, the panic still fires — the stack trace
// alone is more useful than a SIGKILL.
func (w *memWatchdog) trip(heapInUse uint64) {
	ts := clock.Profiling().Format("20060102-150405")
	dir := ""
	if w.tel != nil {
		dir = w.tel.dir
	}
	if dir != "" {
		// Heap profile.
		heapPath := filepath.Join(dir, "heap.pprof."+ts)
		if f, err := os.Create(heapPath); err == nil {
			if perr := pprof.Lookup("heap").WriteTo(f, 0); perr != nil {
				pnlog.Warn("memtel watchdog: write heap pprof: %v", perr)
			}
			if cerr := f.Close(); cerr != nil {
				pnlog.Warn("memtel watchdog: close heap pprof: %v", cerr)
			}
		} else {
			pnlog.Warn("memtel watchdog: create %s: %v", heapPath, err)
		}
		// Goroutine dump (debug=2 = full stacks).
		goPath := filepath.Join(dir, "goroutine."+ts+".txt")
		if f, err := os.Create(goPath); err == nil {
			if perr := pprof.Lookup("goroutine").WriteTo(f, 2); perr != nil {
				pnlog.Warn("memtel watchdog: write goroutine dump: %v", perr)
			}
			if cerr := f.Close(); cerr != nil {
				pnlog.Warn("memtel watchdog: close goroutine dump: %v", cerr)
			}
		} else {
			pnlog.Warn("memtel watchdog: create %s: %v", goPath, err)
		}
	}
	w.tel.flush()
	panic(fmt.Sprintf("memory watchdog: HeapInuse=%d exceeded cap=%d", heapInUse, w.cap))
}
