// Package metrics is the v0.1 scaffold for spec §11.6's metrics emission.
//
// One Run holds the metrics collected during a single test or production
// session. The schema written to disk is stable across versions so
// cross-run comparison (§11.9) works without per-version migration.
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Run is one measurement session: a set of named counters, histograms
// (numeric observation series), and gauges (single-value-per-name),
// plus run-level labels and a start timestamp.
//
// Methods are safe for concurrent use. The mutex is at Run granularity;
// metric series themselves are not exposed to callers, so no fine-grained
// locking is needed.
type Run struct {
	mu sync.Mutex

	startedAt  time.Time
	labels     map[string]string
	counters   map[string]int64
	histograms map[string][]float64
	gauges     map[string]float64
}

// New starts a new metrics run with the given labels (e.g. test name,
// scenario, parameter sweep cell). A nil or empty map is fine.
func New(labels map[string]string) *Run {
	clone := make(map[string]string, len(labels))
	for k, v := range labels {
		clone[k] = v
	}
	return &Run{
		startedAt:  time.Now().UTC(),
		labels:     clone,
		counters:   map[string]int64{},
		histograms: map[string][]float64{},
		gauges:     map[string]float64{},
	}
}

// Counter increments a named counter by delta. Negative deltas are
// allowed but unusual.
func (r *Run) Counter(name string, delta int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[name] += delta
}

// Record appends value to the histogram-like series at name. Histograms
// are stored as raw observation arrays so consumers can compute
// percentiles, mean, or whatever statistic the analysis needs without
// the emitter committing to a bucket scheme.
func (r *Run) Record(name string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms[name] = append(r.histograms[name], value)
}

// Set replaces the gauge at name. Use Counter for accumulating
// quantities, Set for snapshots ("queue depth at end of turn").
func (r *Run) Set(name string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[name] = value
}

// runJSON is the on-disk schema. Field order follows §11.6 prose.
//
// LabelsField is always emitted (possibly as {}). Counters/Histograms/
// Gauges are emitted as {} when empty so consumers can rely on the
// fields being present.
type runJSON struct {
	StartedAt  string               `json:"started_at"`
	EndedAt    string               `json:"ended_at"`
	Labels     map[string]string    `json:"labels"`
	Counters   map[string]int64     `json:"counters"`
	Histograms map[string][]float64 `json:"histograms"`
	Gauges     map[string]float64   `json:"gauges"`
}

// WriteJSON marshals the run's metrics to path. Atomic: writes a temp
// file in the same directory, fsyncs, then renames over path.
func (r *Run) WriteJSON(path string) error {
	r.mu.Lock()
	doc := runJSON{
		StartedAt:  r.startedAt.Format(time.RFC3339Nano),
		EndedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Labels:     copyStringMap(r.labels),
		Counters:   copyInt64Map(r.counters),
		Histograms: copyHistogramMap(r.histograms),
		Gauges:     copyFloat64Map(r.gauges),
	}
	r.mu.Unlock()

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("metrics: marshal: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".metrics-*.json.tmp")
	if err != nil {
		return fmt.Errorf("metrics: temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("metrics: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("metrics: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("metrics: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("metrics: rename: %w", err)
	}
	cleanup = false
	return nil
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyInt64Map(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyFloat64Map(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyHistogramMap(in map[string][]float64) map[string][]float64 {
	out := make(map[string][]float64, len(in))
	for k, v := range in {
		s := make([]float64, len(v))
		copy(s, v)
		out[k] = s
	}
	return out
}
