package sim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"personant/internal/scenarios"
)

// simDayDuration is the simulated span the 1-day rung generates.
//
// "1 day" here means one 12 h work day: the generator structures a day
// as two ~6 h sessions separated by one larger inter-session gap, and
// stops at cumulative simulated time == Duration. 12 h yields exactly
// that two-session shape. (24 h would produce two work days.)
const simDayDuration = 12 * time.Hour

// simSeed is the fixed seed for the rung. A fixed seed makes the
// generated Scenario — and therefore the run — reproducible.
const simSeed = 0x5e1f

// TestSim_1d is the 1-simulated-day rung: it generates a deterministic
// workload, drives it through the existing scenario harness, and logs
// a summary plus the extrapolated 6-month runtime. RunScenario already
// asserts no turn.Run error and runs DefaultInvariants per step, so
// "completes clean, invariants hold" comes for free; the assertions
// here are a light smoke check only.
func TestSim_1d(t *testing.T) {
	sc := GenerateWorkload(WorkloadConfig{
		Seed:     simSeed,
		Duration: simDayDuration,
	})

	// Pin the metrics blob to this test's TempDir so the summary can
	// read it back. RunScenario writes the blob at scenario completion.
	sc.MetricsPath = filepath.Join(t.TempDir(), "sim-1d.metrics.json")

	turns := len(sc.Steps)
	t.Logf("generated workload: %d turns over %s simulated", turns, simDayDuration)

	// Light sanity band — this is a smoke rung, not a tuning gate.
	if turns < 250 || turns > 600 {
		t.Errorf("turn count %d outside plausible band [250, 600]", turns)
	}

	scenarios.RunScenario(t, sc)

	m, err := readMetrics(sc.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}

	durations := m.Histograms["turn_duration_ms"]
	p50 := percentile(durations, 0.50)
	p95 := percentile(durations, 0.95)
	meanMs := mean(durations)

	threadsCreated := m.Counters["threads_created"]
	closures := closureCount(t)

	t.Logf("=== TestSim_1d summary ===")
	t.Logf("turns:            %d", turns)
	t.Logf("threads created:  %d", threadsCreated)
	t.Logf("turn latency:     P50=%.1fms P95=%.1fms mean=%.2fms", p50, p95, meanMs)

	// Closure: counted from retire.complete log lines. The turn path
	// creates threads in the Active state (spec §2.2.1), and the §3.5
	// decay scan offers any thread idle past the decay threshold for
	// closure — the workload scripts ClosureResolved on every step, so
	// any decayed thread closes. A 1-day workload spans enough idle
	// turns to decay-close some threads, so a non-zero count is the
	// expected, healthy signal that the closure flow is live.
	t.Logf("closures:         %d", closures)
	if closures == 0 {
		t.Errorf("closures: got 0; the §3.5 closure flow is dead — expected >0 over a 1-day workload")
	}

	// Recall fidelity is measured (RecallMeasureOnly), never pass/fail
	// at this rung. The generator schedules every `switch` as a recall
	// opportunity, so the adversarial series carries the samples.
	if steps := m.Counters["recall_fidelity_adversarial_steps"]; steps > 0 {
		t.Logf("recall fidelity:  measured over %d steps, mean recall=%.3f mean F1=%.3f",
			steps,
			mean(m.Histograms["recall_fidelity_adversarial_recall"]),
			mean(m.Histograms["recall_fidelity_adversarial_f1"]))
	} else {
		t.Logf("recall fidelity:  no measured steps this run")
	}

	// Headline output: extrapolate the six-month simulation runtime.
	// 62400 is the approximate turn count of the six-month acceptance
	// run (spec §9.1) — scaling the measured per-turn wall cost by it
	// tells us, before climbing, whether the 6 m run is minutes or an
	// hour.
	const sixMonthTurns = 62400
	perTurnMs := meanMs
	extrapolated := time.Duration(sixMonthTurns*perTurnMs) * time.Millisecond
	t.Logf("per-turn cost:    %.2fms (mean wall)", perTurnMs)
	t.Logf("extrapolated 6 m runtime (62400 turns): %s", extrapolated.Round(time.Second))
}

// TestGenerateWorkload_Deterministic verifies the core contract: a
// fixed config produces a byte-identical Scenario across runs.
func TestGenerateWorkload_Deterministic(t *testing.T) {
	cfg := WorkloadConfig{Seed: simSeed, Duration: simDayDuration}
	a := GenerateWorkload(cfg)
	b := GenerateWorkload(cfg)

	if a.Name != b.Name {
		t.Errorf("scenario name differs: %q vs %q", a.Name, b.Name)
	}
	if len(a.Steps) != len(b.Steps) {
		t.Fatalf("step count differs: %d vs %d", len(a.Steps), len(b.Steps))
	}
	for i := range a.Steps {
		if !reflect.DeepEqual(a.Steps[i], b.Steps[i]) {
			t.Errorf("step %d differs between runs", i)
		}
	}

	// A different seed must produce a different Scenario (sanity check
	// that the seed actually drives generation).
	c := GenerateWorkload(WorkloadConfig{Seed: simSeed + 1, Duration: simDayDuration})
	if reflect.DeepEqual(a.Steps, c.Steps) {
		t.Errorf("different seeds produced identical step lists")
	}
}

// metricsBlob mirrors the stable §11.6 metrics-blob schema for the
// fields the rung summary consumes.
type metricsBlob struct {
	Counters   map[string]int64     `json:"counters"`
	Histograms map[string][]float64 `json:"histograms"`
	Gauges     map[string]float64   `json:"gauges"`
}

func readMetrics(path string) (metricsBlob, error) {
	var m metricsBlob
	body, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(body, &m)
	return m, err
}

// percentile returns the q-quantile (0..1) of xs using nearest-rank.
// Returns 0 for an empty series.
func percentile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	idx := int(q * float64(len(sorted)-1))
	return sorted[idx]
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// closureCount walks the day-log files the harness wrote (under its
// own TempDir, which shares a parent with this test's TempDir per Go's
// testing.TempDir layout) and counts `retire.complete` lines — one per
// thread closed during the run.
func closureCount(t *testing.T) int {
	t.Helper()
	root := filepath.Dir(t.TempDir())
	count := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".log") {
			return nil //nolint:nilerr // skip unreadable entries; this is best-effort diagnostics
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		count += strings.Count(string(body), "retire.complete ")
		return nil
	})
	if err != nil {
		t.Logf("closureCount: walk %s: %v (reporting partial count)", root, err)
	}
	return count
}
