package sim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"personant/internal/scenarios"
	"personant/internal/store"
)

// simDayDuration is the simulated wall span the 1-day rung covers — one
// calendar day. Duration is total simulated wall time (inter-turn,
// inter-session, and overnight gaps all count toward it), so 24 h is
// exactly one work day: two ~6 h sessions split by an inter-session
// gap, then the overnight gap that carries the clock past 24 h and ends
// the day loop.
const simDayDuration = 24 * time.Hour

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

	// Light sanity band — a smoke rung, not a tuning gate. Derivation:
	// a work day is 2 sessions of 6 h turn-active time = 12 h of
	// inter-turn gaps. With the 6:1 rapid:work weighting the mean gap is
	// ~(6*RapidGap + 1*WorkGap)/7 ≈ (6*1m + 6m)/7 ≈ 1.7m, so 12 h / 1.7m
	// ≈ 420 turns; ±30% jitter and weighting variance widen that to a
	// [250, 600] band. Re-derive this if RapidGap/WorkGap or the
	// rapid:work weights change.
	if turns < 250 || turns > 600 {
		t.Errorf("turn count %d outside plausible band [250, 600]", turns)
	}

	h := scenarios.RunScenario(t, sc)

	m, err := readMetrics(sc.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}

	durations := m.Histograms["turn_duration_ms"]
	p50 := percentile(durations, 0.50)
	p95 := percentile(durations, 0.95)
	meanMs := mean(durations)

	threadsCreated := m.Counters["threads_created"]
	closures := closureCount(t, h)

	// Thread-id contiguity: the generator maps creation order →
	// thr_1, thr_2, …, and every downstream assumption (recall oracle,
	// threadID()) depends on that. Assert the spine's thread IDs are
	// exactly thr_1 .. thr_N with no gaps. This holds while threads are
	// append-only and no spine record is deleted (see threadID()'s
	// precondition); archival will break it.
	assertThreadIDsContiguous(t, h)

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

	// Headline output: a LOWER BOUND on the six-month simulation
	// runtime. 62400 is the approximate turn count of the six-month
	// acceptance run (spec §9.1). Scaling the 1-day mean per-turn wall
	// cost by it is only a floor: per-turn cost grows with spine size
	// (recall scans, closeTurnAndUpdateEngagement, ReadSpine all scale
	// with the thread population), and at 1 day the population is tiny.
	// The real 6 m run will exceed this number. A growth slope —
	// needed for an honest estimate — comes once the 1-week rung lands.
	const sixMonthTurns = 62400
	perTurnMs := meanMs
	floor := time.Duration(sixMonthTurns*perTurnMs) * time.Millisecond
	t.Logf("per-turn cost:    %.2fms (mean wall, 1-day spine — small)", perTurnMs)
	t.Logf("extrapolated 6 m runtime ≥ %s (floor; real per-turn cost grows "+
		"with spine size, so the actual run will exceed this — refine once "+
		"the 1-week rung gives a growth slope)", floor.Round(time.Second))
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

// closureCount counts `retire.complete` events in the harness's event
// log — one per thread closed during the run. It reads h.Paths.LogsDir
// directly: the harness exposes the substrate paths it actually wrote
// to, so there is no reliance on undocumented testing.TempDir
// sibling-directory topology.
func closureCount(t *testing.T, h *scenarios.Harness) int {
	t.Helper()
	entries, err := os.ReadDir(h.Paths.LogsDir)
	if err != nil {
		t.Logf("closureCount: read %s: %v (reporting 0)", h.Paths.LogsDir, err)
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(h.Paths.LogsDir, e.Name()))
		if rerr != nil {
			t.Logf("closureCount: read %s: %v (skipping)", e.Name(), rerr)
			continue
		}
		count += strings.Count(string(body), "retire.complete ")
	}
	return count
}

// assertThreadIDsContiguous reads the post-run spine and verifies its
// thread IDs are exactly thr_1 .. thr_N with no gaps — the contract
// the generator's creation-order → thr_N mapping depends on.
func assertThreadIDsContiguous(t *testing.T, h *scenarios.Harness) {
	t.Helper()
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		t.Fatalf("assertThreadIDsContiguous: ReadSpine: %v", err)
	}
	// Parse the numeric suffix of each thr_N id and check the set is
	// exactly {1 .. N}. A string sort would mis-order thr_2 vs thr_10,
	// so sort numerically.
	nums := make([]int, 0, len(recs))
	for _, r := range recs {
		n, err := strconv.Atoi(strings.TrimPrefix(r.ID, "thr_"))
		if err != nil || !strings.HasPrefix(r.ID, "thr_") {
			t.Errorf("spine thread ID %q is not of the form thr_N", r.ID)
			return
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for i, n := range nums {
		if n != i+1 {
			t.Errorf("spine thread IDs not contiguous: position %d is thr_%d, want thr_%d (sorted: %v)",
				i, n, i+1, nums)
			return
		}
	}
}
