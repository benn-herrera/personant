package sim

import (
	"encoding/json"
	"flag"
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

// corpusQueriesPath is the recall_madlibs corpus query artifact the
// generator binds threads to. It is a committed, read-only input (152
// Wikipedia topics × 10 queries = 1520 distinguishable slots).
var corpusQueriesPath = filepath.Join("..", "testdata", "recall_madlibs", "corpus_queries.json")

// corpusQueryDoc mirrors the subset of corpus_queries.json the
// generator consumes: the flat `queries` array, each entry a
// distinguishable slot.
type corpusQueryDoc struct {
	Queries []struct {
		Topic     string   `json:"topic"`
		Tags      []string `json:"tags"`
		UserInput string   `json:"user_input"`
	} `json:"queries"`
}

// loadCorpusSlots reads corpus_queries.json and returns its query slots
// as the generator's CorpusSlot pool. The artifact is committed, so an
// absence is a hard failure (not a skip): the rung walk cannot run
// without it.
func loadCorpusSlots(t *testing.T) []CorpusSlot {
	t.Helper()
	body, err := os.ReadFile(corpusQueriesPath)
	if err != nil {
		t.Fatalf("read corpus %s: %v", corpusQueriesPath, err)
	}
	var doc corpusQueryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse corpus %s: %v", corpusQueriesPath, err)
	}
	if len(doc.Queries) == 0 {
		t.Fatalf("corpus %s: empty queries array", corpusQueriesPath)
	}
	slots := make([]CorpusSlot, len(doc.Queries))
	for i, q := range doc.Queries {
		if q.Topic == "" || len(q.Tags) == 0 || q.UserInput == "" {
			t.Fatalf("corpus %s: query %d malformed (topic=%q tags=%v input=%q)",
				corpusQueriesPath, i, q.Topic, q.Tags, q.UserInput)
		}
		slots[i] = CorpusSlot{
			Topic:     q.Topic,
			Tags:      append([]string(nil), q.Tags...),
			UserInput: q.UserInput,
		}
	}
	return slots
}

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

// simDuration selects the simulation span at run time. The default
// `1d` keeps `make test` (which passes no flag) on the ~27 s 1-day
// smoke rung; longer rungs are run via `make sim DURATION=…`.
var simDuration = flag.String("sim.duration", "1d",
	"simulation span: 1d|1w|1m|2m|6m, or a Go duration like 168h")

// parseSimDuration maps the -sim.duration flag value to a span. The
// named rungs (1d/1w/1m/2m/6m) are the six-month rung walk; any other
// value falls through to time.ParseDuration so an ad-hoc span like
// `72h` still works.
func parseSimDuration(s string) (time.Duration, error) {
	switch s {
	case "1d":
		return 24 * time.Hour, nil
	case "1w":
		return 7 * 24 * time.Hour, nil
	case "1m":
		return 30 * 24 * time.Hour, nil
	case "2m":
		return 60 * 24 * time.Hour, nil
	case "6m":
		return 180 * 24 * time.Hour, nil
	default:
		return time.ParseDuration(s)
	}
}

// TestSim is the six-month simulation rung: it generates a
// deterministic workload of the span chosen by -sim.duration (default
// 1d), drives it through the existing scenario harness, and logs a
// summary plus the extrapolated 6-month runtime. RunScenario already
// asserts no turn.Run error and runs DefaultInvariants per step, so
// "completes clean, invariants hold" comes for free; the assertions
// here are a light smoke check only.
func TestSim(t *testing.T) {
	d, err := parseSimDuration(*simDuration)
	if err != nil {
		t.Fatalf("invalid -sim.duration %q: %v (use 1d|1w|1m|2m|6m or a Go duration like 168h)",
			*simDuration, err)
	}

	// Light sanity band — a smoke rung, not a tuning gate. Derivation:
	// a work day is 2 sessions of 6 h turn-active time = 12 h of
	// inter-turn gaps. With the 6:1 rapid:work weighting the mean gap is
	// ~(6*RapidGap + 1*WorkGap)/7 ≈ (6*1m + 6m)/7 ≈ 1.7m, so 12 h / 1.7m
	// ≈ 420 turns; ±30% jitter and weighting variance widen that to a
	// [250, 600] band. Re-derive this if RapidGap/WorkGap or the
	// rapid:work weights change. The band is 1-day-specific, so it
	// applies only when the span is exactly 24 h — longer rungs still
	// get runSimRung's clean-completion + contiguity assertions, just
	// not this turn-count band. GenerateWorkload is deterministic for a
	// fixed (Seed, Duration), so counting turns here and again inside
	// runSimRung yields the identical workload.
	corpus := loadCorpusSlots(t)
	if d == simDayDuration {
		turns := len(GenerateWorkload(WorkloadConfig{
			Seed: simSeed, Duration: d, Corpus: corpus,
		}).Steps)
		if turns < 250 || turns > 600 {
			t.Errorf("turn count %d outside plausible band [250, 600]", turns)
		}
	}

	runSimRung(t, "sim-"+*simDuration, d, corpus)
}

// runSimRung is the shared run-and-report logic for every rung of the
// six-month simulation rung walk. It generates a deterministic workload
// of simulated span d, drives it through the scenario harness, asserts
// clean completion (RunScenario does the per-step invariant + turn.Run
// error checks) plus thread-id well-formedness, and logs the summary
// the rung walk tracks. It is untagged so it compiles into every test
// build, and returns the post-run *Harness for any rung-specific
// follow-up assertions.
func runSimRung(t *testing.T, label string, d time.Duration, corpus []CorpusSlot) *scenarios.Harness {
	t.Helper()

	sc := GenerateWorkload(WorkloadConfig{
		Seed:     simSeed,
		Duration: d,
		Corpus:   corpus,
	})

	// Pin the metrics blob to this test's TempDir so the summary can
	// read it back. RunScenario writes the blob at scenario completion.
	sc.MetricsPath = filepath.Join(t.TempDir(), "sim.metrics.json")

	turns := len(sc.Steps)
	t.Logf("generated workload: %d turns over %s simulated", turns, d)

	start := time.Now()
	h := scenarios.RunScenario(t, sc)
	wall := time.Since(start)

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

	// Thread-id well-formedness: every spine thread ID must be a
	// well-formed thr_<n> and unique. Gaps are LEGAL — archival deletes
	// retired thread records from the spine, leaving holes in the live
	// id set; the runtime never reuses an id, so the
	// creation-order → thr_{N+1} mapping the generator relies on stays
	// valid even with gaps. (This deliberately does NOT assert
	// contiguity / no-gaps; see threadID()'s precondition.)
	assertThreadIDsWellFormed(t, h)
	finalPopulation := finalThreadPopulation(t, h)

	t.Logf("=== %s summary ===", label)
	t.Logf("turns:            %d", turns)
	t.Logf("threads created:  %d", threadsCreated)
	t.Logf("closures:         %d", closures)
	t.Logf("final thread pop: %d (final spine size)", finalPopulation)
	t.Logf("turn latency:     P50=%.1fms P95=%.1fms mean=%.2fms", p50, p95, meanMs)

	// Closure: counted from retire.complete log lines. The turn path
	// creates threads in the Active state (spec §2.2.1), and the §3.5
	// decay scan offers any thread idle past the decay threshold for
	// closure — the workload scripts ClosureResolved on every step, so
	// any decayed thread closes. A multi-turn workload spans enough idle
	// turns to decay-close some threads, so a non-zero count is the
	// expected, healthy signal that the closure flow is live.
	if closures == 0 {
		t.Errorf("closures: got 0; the §3.5 closure flow is dead — expected >0 over the %s workload", label)
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

	t.Logf("wall-clock runtime: %s", wall.Round(time.Millisecond))

	// Headline output: a LOWER BOUND on the six-month simulation
	// runtime. 62400 is the approximate turn count of the six-month
	// acceptance run (spec §9.1). Scaling the rung's mean per-turn wall
	// cost by it is only a floor: per-turn cost grows with spine size
	// (recall scans, closeTurnAndUpdateEngagement, ReadSpine all scale
	// with the thread population), and at short rungs the population is
	// still small. The real 6 m run will exceed this number.
	const sixMonthTurns = 62400
	perTurnMs := meanMs
	floor := time.Duration(sixMonthTurns*perTurnMs) * time.Millisecond
	t.Logf("per-turn cost:    %.2fms (mean wall)", perTurnMs)
	t.Logf("extrapolated 6 m runtime ≥ %s (floor; real per-turn cost grows "+
		"with spine size, so the actual run will exceed this)", floor.Round(time.Second))

	return h
}

// TestGenerateWorkload_Deterministic verifies the core contract: a
// fixed config produces a byte-identical Scenario across runs.
func TestGenerateWorkload_Deterministic(t *testing.T) {
	corpus := loadCorpusSlots(t)
	cfg := WorkloadConfig{Seed: simSeed, Duration: simDayDuration, Corpus: corpus}
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
	c := GenerateWorkload(WorkloadConfig{Seed: simSeed + 1, Duration: simDayDuration, Corpus: corpus})
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

// finalThreadPopulation returns the post-run spine record count — the
// final thread population. ReadSpine is the authoritative source: the
// harness exposes the substrate path it actually wrote to.
func finalThreadPopulation(t *testing.T, h *scenarios.Harness) int {
	t.Helper()
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		t.Fatalf("finalThreadPopulation: ReadSpine: %v", err)
	}
	return len(recs)
}

// assertThreadIDsWellFormed reads the post-run spine and verifies every
// thread ID is a well-formed thr_<n> with a positive integer suffix and
// that the IDs are unique. It deliberately does NOT assert contiguity:
// thread archival deletes retired records from the spine, leaving gaps
// in the live id set. The runtime never reuses an id, so a gapped set
// is legal and expected on the longer rungs.
func assertThreadIDsWellFormed(t *testing.T, h *scenarios.Harness) {
	t.Helper()
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		t.Fatalf("assertThreadIDsWellFormed: ReadSpine: %v", err)
	}
	seen := make(map[int]bool, len(recs))
	for _, r := range recs {
		if !strings.HasPrefix(r.ID, "thr_") {
			t.Errorf("spine thread ID %q is not of the form thr_<n>", r.ID)
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(r.ID, "thr_"))
		if err != nil || n < 1 {
			t.Errorf("spine thread ID %q has a non-positive-integer suffix", r.ID)
			continue
		}
		if seen[n] {
			t.Errorf("spine thread ID %q is duplicated", r.ID)
			continue
		}
		seen[n] = true
	}
}
