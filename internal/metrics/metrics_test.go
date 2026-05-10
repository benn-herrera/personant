package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCounter(t *testing.T) {
	r := New(nil)
	r.Counter("hits", 1)
	r.Counter("hits", 2)
	r.Counter("hits", -1)
	r.Counter("misses", 5)

	doc := writeAndParse(t, r)
	if doc.Counters["hits"] != 2 {
		t.Errorf("hits: got %d, want 2", doc.Counters["hits"])
	}
	if doc.Counters["misses"] != 5 {
		t.Errorf("misses: got %d, want 5", doc.Counters["misses"])
	}
}

func TestRecord(t *testing.T) {
	r := New(nil)
	for _, v := range []float64{1.0, 2.5, 3.75, 4.0} {
		r.Record("latency_ms", v)
	}
	doc := writeAndParse(t, r)
	got := doc.Histograms["latency_ms"]
	want := []float64{1.0, 2.5, 3.75, 4.0}
	if len(got) != len(want) {
		t.Fatalf("histogram len: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("histogram[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSet(t *testing.T) {
	r := New(nil)
	r.Set("queue_depth", 5)
	r.Set("queue_depth", 12) // overwrite
	r.Set("budget_pct", 0.85)

	doc := writeAndParse(t, r)
	if doc.Gauges["queue_depth"] != 12 {
		t.Errorf("queue_depth: got %v, want 12", doc.Gauges["queue_depth"])
	}
	if doc.Gauges["budget_pct"] != 0.85 {
		t.Errorf("budget_pct: got %v, want 0.85", doc.Gauges["budget_pct"])
	}
}

func TestLabelsAndTimestamps(t *testing.T) {
	r := New(map[string]string{"scenario": "single-thread", "seed": "42"})
	r.Counter("turns", 1)

	doc := writeAndParse(t, r)
	if doc.Labels["scenario"] != "single-thread" {
		t.Errorf("scenario label: got %q, want single-thread", doc.Labels["scenario"])
	}
	if doc.Labels["seed"] != "42" {
		t.Errorf("seed label: got %q, want 42", doc.Labels["seed"])
	}
	if doc.StartedAt == "" {
		t.Errorf("started_at empty")
	}
	if doc.EndedAt == "" {
		t.Errorf("ended_at empty")
	}
}

// TestSchemaShape: every documented field is present on disk, even when
// empty. Consumers should be able to read counters/histograms/gauges
// without first checking whether the field exists.
func TestSchemaShape(t *testing.T) {
	r := New(nil)
	path := filepath.Join(t.TempDir(), "m.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"started_at", "ended_at", "labels", "counters", "histograms", "gauges"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("schema missing field %q", field)
		}
	}
}

// TestConcurrentWriters: N goroutines each increment N times; the final
// total must equal N*N. Run with -race to catch any unguarded access.
func TestConcurrentWriters(t *testing.T) {
	r := New(nil)
	const goroutines = 16
	const perGoroutine = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				r.Counter("ops", 1)
				r.Record("op_ms", float64(i))
				r.Set("last_g", float64(g))
			}
		}()
	}
	wg.Wait()

	doc := writeAndParse(t, r)
	want := int64(goroutines * perGoroutine)
	if doc.Counters["ops"] != want {
		t.Errorf("ops: got %d, want %d", doc.Counters["ops"], want)
	}
	if got := len(doc.Histograms["op_ms"]); got != int(want) {
		t.Errorf("op_ms samples: got %d, want %d", got, want)
	}
}

// TestWriteRoundTripStable: write → parse via the stable schema → semantic
// equality holds. Belt-and-braces over TestSchemaShape; this one covers
// values, not just presence.
func TestWriteRoundTripStable(t *testing.T) {
	r := New(map[string]string{"k": "v"})
	r.Counter("c", 7)
	r.Record("h", 1.5)
	r.Record("h", 2.5)
	r.Set("g", 3.14)

	doc := writeAndParse(t, r)
	if doc.Labels["k"] != "v" {
		t.Errorf("label: %v", doc.Labels)
	}
	if doc.Counters["c"] != 7 {
		t.Errorf("counter: %v", doc.Counters)
	}
	if len(doc.Histograms["h"]) != 2 || doc.Histograms["h"][0] != 1.5 || doc.Histograms["h"][1] != 2.5 {
		t.Errorf("hist: %v", doc.Histograms["h"])
	}
	if doc.Gauges["g"] != 3.14 {
		t.Errorf("gauge: %v", doc.Gauges["g"])
	}
}

// writeAndParse writes the run to a temp dir and re-parses it through the
// documented schema.
func writeAndParse(t *testing.T, r *Run) runJSON {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc runJSON
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}
