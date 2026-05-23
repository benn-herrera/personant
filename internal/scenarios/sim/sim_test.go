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

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/scenarios"
	"personant/internal/store"
)

// corpusQueriesPath is the recall_madlibs corpus query artifact the
// generator binds threads to. It is a committed, read-only input (301
// Wikipedia topics × 10 queries = 3010 distinguishable slots).
var corpusQueriesPath = filepath.Join("..", "testdata", "recall_madlibs", "corpus_queries.json")

// corpusTemplatesDir holds the per-topic column data behind the
// recall_madlibs query draw. It is the ground truth for which cell of
// each column a slot's Tags[i] was drawn from — and in particular
// whether that cell is the "loose" (drift) cell at the column's tail.
// loadCorpusSlots consults it to populate each slot's LooseMask.
var corpusTemplatesDir = filepath.Join("..", "testdata", "recall_madlibs", "corpus_templates")

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

// templateDoc mirrors the subset of corpus_templates/<topic>.json the
// loader consumes: the per-column cell lists. Order within a column
// follows the generator's depth convention — the last cell is the
// "loose" drift cell (a deliberate semantic stretch); earlier cells are
// canonical (index 0) and progressively-drifted synonyms (1..n-2).
type templateDoc struct {
	Topic   string     `json:"topic"`
	Columns [][]string `json:"columns"`
}

// loadCorpusSlots reads corpus_queries.json and returns its query slots
// as the generator's CorpusSlot pool. The artifact is committed, so an
// absence is a hard failure (not a skip): the rung walk cannot run
// without it.
//
// Each slot's LooseMask is filled in by consulting the corresponding
// corpus_templates file for the slot's topic: a slot Tag at position i
// is "loose" iff it equals the LAST cell of column i (the conventional
// drift position). A missing or malformed template is a hard failure —
// the workload binding depends on the mask to make recall miss
// probabilistically, and silently falling back to an all-false mask
// would re-introduce the trivially-perfect-recall failure the loose
// binding is meant to fix.
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
	columnsByTopic := loadCorpusColumns(t)
	slots := make([]CorpusSlot, len(doc.Queries))
	for i, q := range doc.Queries {
		if q.Topic == "" || len(q.Tags) == 0 || q.UserInput == "" {
			t.Fatalf("corpus %s: query %d malformed (topic=%q tags=%v input=%q)",
				corpusQueriesPath, i, q.Topic, q.Tags, q.UserInput)
		}
		cols, ok := columnsByTopic[q.Topic]
		if !ok {
			t.Fatalf("corpus %s: query %d topic %q has no template columns",
				corpusQueriesPath, i, q.Topic)
		}
		if len(cols) != len(q.Tags) {
			t.Fatalf("corpus %s: query %d topic %q: %d columns vs %d tags",
				corpusQueriesPath, i, q.Topic, len(cols), len(q.Tags))
		}
		mask := make([]bool, len(q.Tags))
		for ci, tag := range q.Tags {
			col := cols[ci]
			if len(col) == 0 {
				t.Fatalf("corpus %s: topic %q column %d is empty",
					corpusQueriesPath, q.Topic, ci)
			}
			mask[ci] = tag == col[len(col)-1]
		}
		slots[i] = CorpusSlot{
			Topic:     q.Topic,
			Tags:      append([]string(nil), q.Tags...),
			LooseMask: mask,
			UserInput: q.UserInput,
		}
	}
	return slots
}

// loadCorpusColumns reads every corpus_templates/*.json file and
// returns a topic→columns lookup. Used by loadCorpusSlots to compute
// each slot's LooseMask.
func loadCorpusColumns(t *testing.T) map[string][][]string {
	t.Helper()
	entries, err := os.ReadDir(corpusTemplatesDir)
	if err != nil {
		t.Fatalf("read corpus templates dir %s: %v", corpusTemplatesDir, err)
	}
	out := make(map[string][][]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(corpusTemplatesDir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read corpus template %s: %v", path, err)
		}
		var doc templateDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("parse corpus template %s: %v", path, err)
		}
		if doc.Topic == "" || len(doc.Columns) == 0 {
			t.Fatalf("corpus template %s: missing topic or columns", path)
		}
		out[doc.Topic] = doc.Columns
	}
	if len(out) == 0 {
		t.Fatalf("corpus templates dir %s: no templates loaded", corpusTemplatesDir)
	}
	return out
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

// simMemoryCapBytes arms the harness's heap watchdog for sim rungs. The
// 8 GiB value sits well under the 38 GiB development machine's RAM and
// far below the macOS jetsam threshold (which has been observed firing
// above ~30 GB compressed in prior incidents), so a runaway leak trips
// the cap and gets a Go stack trace + heap profile before the OS pager
// kills the process. Handwritten unit-test scenarios leave the field
// zero and see no watchdog.
const simMemoryCapBytes = 8 * 1024 * 1024 * 1024

// simHeavyInvariantCadence is the simulated-time interval between
// firings of the heavy invariants (full-spine sweeps:
// VerifySpineIntegrity, VerifyIndexFresh,
// VerifyThreadMetaMatchesSpine) during a sim run. Cheap
// invariants still fire every step, and the heavy set always fires once
// at end-of-run regardless of cadence, so end-of-run substrate
// well-formedness is unchanged. Per-step firing made the heavy checks
// the dominant wall-time cost of long sim rungs; daily cadence catches
// a substrate break within a reasonable window while keeping per-step
// cost O(1)-ish.
const simHeavyInvariantCadence = 24 * time.Hour

// simDuration selects the simulation span at run time. The default
// `1d` keeps `make test` (which passes no flag) on the ~27 s 1-day
// smoke rung; longer rungs are run via `make sim DURATION=…`.
var simDuration = flag.String("sim.duration", "1d",
	"simulation span: 1d|1w|1m|2m|6m, or `<N>d` calendar days like 30d, or a Go duration like 168h")

// parseSimDuration maps the -sim.duration flag value to a span. The
// named rungs (1d/1w/1m/2m/6m) are convenience aliases on the six-month
// rung walk. A bare `<N>d` form (e.g. 30d, 120d) parses as N calendar
// days, since time.ParseDuration has no day unit. Any other value falls
// through to time.ParseDuration so an ad-hoc span like `72h` still works.
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
		// Whole-day `<N>d` form: time.ParseDuration has no day unit, so
		// parse N ourselves. Fractional days (1.5d) are intentionally
		// unsupported — use the `h` form for sub-day spans.
		if rest, ok := strings.CutSuffix(s, "d"); ok {
			if n, err := strconv.Atoi(rest); err == nil && n > 0 {
				return time.Duration(n) * 24 * time.Hour, nil
			}
		}
		return time.ParseDuration(s)
	}
}

func TestParseSimDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"named 1d", "1d", 24 * time.Hour, false},
		{"general 15d", "15d", 360 * time.Hour, false},
		{"general 30d", "30d", 720 * time.Hour, false},
		{"general 120d", "120d", 2880 * time.Hour, false},
		{"go duration 168h", "168h", 168 * time.Hour, false},
		{"empty", "", 0, true},
		{"non-numeric day", "xyzd", 0, true},
		{"zero days", "0d", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSimDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSimDuration(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("parseSimDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
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
		t.Fatalf("invalid -sim.duration %q: %v (use 1d|1w|1m|2m|6m, or `<N>d` calendar days like 30d, or a Go duration like 168h)",
			*simDuration, err)
	}

	corpus := loadCorpusSlots(t)
	h := runSimRung(t, "sim-"+*simDuration, d, corpus)

	// Light sanity band — a smoke rung, not a tuning gate. Derivation:
	// a work day is 2 sessions of 6 h turn-active time = 12 h of
	// inter-turn gaps. With the 6:1 rapid:work weighting the mean gap is
	// ~(6*RapidGap + 1*WorkGap)/7 ≈ (6*1m + 6m)/7 ≈ 1.7m, so 12 h / 1.7m
	// ≈ 420 turns; ±30% jitter and weighting variance widen that to a
	// [250, 600] band. Re-derive this if RapidGap/WorkGap or the
	// rapid:work weights change. The band is 1-day-specific, so it
	// applies only when the span is exactly 24 h — longer rungs still
	// get runSimRung's clean-completion + well-formedness assertions,
	// just not this turn-count band.
	//
	// The on-demand generator yields steps one at a time, so the count
	// is not knowable up front — it is asserted against the post-run
	// `turns` counter the harness accumulated as the run proceeded.
	if d == simDayDuration {
		m, err := readMetrics(h.MetricsPath)
		if err != nil {
			t.Fatalf("read metrics blob for turn-count band: %v", err)
		}
		turns := m.Counters["turns"]
		if turns < 250 || turns > 600 {
			t.Errorf("turn count %d outside plausible band [250, 600]", turns)
		}
	}
}

// withRunTimestampSuffix appends a wall-clock suffix to a sim
// scenario's Name so each `go test` invocation gets its own rundata
// directory. The harness's runDataHome RemoveAll's the per-scenario
// home at run start; without a unique suffix, two sim runs in one test
// invocation collide, and stragglers (working-set writer, autogit ops,
// log appenders) from the prior run race with the new RemoveAll —
// surfacing as "directory not empty", "log shrank", or spurious
// invariant failures (MAD review #56 burn-down).
//
// clock.Profiling() is the real wall-clock; it is unaffected by the
// Timeline override the harness installs during a run.
func withRunTimestampSuffix(name string) string {
	return name + "." + clock.Profiling().Format("060102150405")
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

	// Relax the heavy-invariant cadence to sim-daily. The sim's per-step
	// heavy-invariant sweeps were super-linear in turn count and ate the
	// wall budget on long rungs; the cheap subset still fires per step
	// and the heavy set still fires once at end-of-run, so a substrate
	// break is caught within ~one sim-day and the acceptance gate is
	// unchanged. Handwritten scenarios leave this zero and keep
	// per-step heavy firing.
	sc.HeavyInvariantCadence = simHeavyInvariantCadence

	// Arm the heap watchdog. A leaking sim previously got SIGKILL'd by
	// macOS memorystatus mid-run with no diagnostics; capping HeapInuse
	// at 8 GiB converts that into a Go panic plus a heap profile under
	// the rundata directory.
	sc.MemoryCapBytes = simMemoryCapBytes

	// Append a wall-clock timestamp suffix so each sim run gets its own
	// forensic-data directory. GenerateWorkload keys sc.Name on
	// (seed, duration) only, so two runs of the same duration would
	// otherwise reuse one test/rundata/<name>/ directory and the
	// harness's runDataHome RemoveAll would destroy the prior run's data.
	sc.Name = withRunTimestampSuffix(sc.Name)

	// The workload is generated on demand — the harness pulls one step
	// at a time from sc.StepSource — so the turn count is not knowable
	// before the run. It is read back from the `turns` counter the
	// metrics blob accumulated as the run proceeded.
	t.Logf("driving on-demand workload over %s simulated", d)

	// Hold the generator pointer so the post-run episode stats can be
	// pulled out. The generator is the only thing that knows about
	// refinement episodes — the harness sees them as ordinary steps.
	gen := sc.StepSource.(*generator)

	start := clock.Profiling()
	h := scenarios.RunScenario(t, sc)
	wall := clock.Since(start)

	// Fold the generator's miss → refinement episode stats into the
	// metrics blob. RunScenario already wrote the blob, but h.Metrics
	// is still live in memory; record the new samples and re-write so
	// readMetrics below sees them. recall_episode_queries_to_hit is a
	// histogram series (one sample per HIT episode, n in {1,2,3});
	// recall_episode_unresolved is a counter (episodes that failed to
	// hit within 3 attempts or were superseded before closing).
	for _, n := range gen.episodeQueriesToHit {
		h.Metrics.Record("recall_episode_queries_to_hit", float64(n))
	}
	if gen.episodeUnresolved > 0 {
		h.Metrics.Counter("recall_episode_unresolved", int64(gen.episodeUnresolved))
	}
	if gen.userDictatedCount > 0 {
		h.Metrics.Counter("workload_user_dictated_turns", int64(gen.userDictatedCount))
	}

	// Fold the anchor-lifecycle (Inc 5) metrics: the generator-owned recall
	// oracles (drift/dest/abandoned-premise buckets + vague became-matchable
	// turn) and the post-run frontmatter-derived steady-state signals
	// (ever-central / history-length / superseded-precision /
	// projection-churn). These are the §9.4 instrument the redesign validates.
	recordLifecycleMetrics(t, h, gen)

	// Fold the within-session interleaving telemetry (SPEC §9.1 "too
	// coherent" concern, measured rather than asserted): distinct
	// slots/threads per session, turns per session, and topic dwell
	// run-length. Pure post-emission bookkeeping accumulated by the
	// generator; no rng, no control-flow influence.
	recordInterleaveMetrics(h, gen)

	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Fatalf("re-write metrics blob with episode stats: %v", err)
	}

	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}
	turns := int(m.Counters["turns"])

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
	t.Logf("user-dictated turns: %d (Realism C variant — user prompt carries the literal line being appended)",
		gen.userDictatedCount)

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
	//
	// Truth-in-labeling (sim-vs-reality MAD T0-1): this figure measures
	// ONLY the symbolic Jaccard layer. The acceptance run uses a nil
	// embedder (embedding recall is off) and derives ground truth from
	// slot-tag equality — the same signal Jaccard keys on. Reporting it
	// as bare "recall" overstates what is validated, so it is labeled
	// "symbolic-only recall" until embedding recall is actually measured
	// (T2-1 live-inference mode + T3-1 embedding-fidelity metric).
	if steps := m.Counters["recall_fidelity_adversarial_steps"]; steps > 0 {
		t.Logf("symbolic-only recall (Jaccard):  measured over %d steps, mean symbolic-only recall=%.3f mean F1=%.3f",
			steps,
			mean(m.Histograms["recall_fidelity_adversarial_recall"]),
			mean(m.Histograms["recall_fidelity_adversarial_f1"]))
	} else {
		t.Logf("symbolic-only recall (Jaccard):  no measured steps this run")
	}

	// Miss → refinement episode summary. queries-to-hit is a per-episode
	// sample (n attempts to first hit, n ∈ {1,2,3}); unresolved is a
	// counter of episodes that exhausted 3 attempts without a hit or
	// were superseded by a new opportunity before closure.
	qth := m.Histograms["recall_episode_queries_to_hit"]
	unresolved := m.Counters["recall_episode_unresolved"]
	if len(qth) > 0 || unresolved > 0 {
		t.Logf("recall episodes:  mean queries-to-hit %.2f (%d episodes, %d unresolved)",
			mean(qth), len(qth), unresolved)
	} else {
		t.Logf("recall episodes:  no closed episodes this run")
	}

	// Anchor-lifecycle (Inc 5) metric summary — the redesign's instrument.
	t.Logf("=== anchor-lifecycle metrics ===")
	t.Logf("drift_recall_origin:      %.3f (%d obs) — abandoned origin premise still matchable",
		m.Gauges["drift_recall_origin"], int(m.Gauges["drift_recall_origin_obs"]))
	t.Logf("drift_recall_dest:        %.3f (%d obs) — active (destination) projection recall",
		m.Gauges["drift_recall_dest"], int(m.Gauges["drift_recall_dest_obs"]))
	t.Logf("abandoned_premise_recall: %.3f (%d obs) — inverted-premise thread surfaces on original query",
		m.Gauges["abandoned_premise_recall"], int(m.Gauges["abandoned_premise_recall_obs"]))
	t.Logf("superseded_precision:     %.3f — recall matches dominated by intended (not abandoned) threads",
		m.Gauges["superseded_precision"])
	t.Logf("ever_central_count:       total=%d mean/thread=%.2f max/thread=%d (steady-state: flat across rung length)",
		int(m.Gauges["ever_central_count"]), m.Gauges["ever_central_mean"], int(m.Gauges["ever_central_max"]))
	t.Logf("history_len:              total=%d mean/thread=%.2f max/thread=%d (cap=%d; flat = steady-state proof)",
		int(m.Gauges["history_len_total"]), m.Gauges["history_len_mean"], int(m.Gauges["history_len_max"]), historyCapForReport)
	t.Logf("superseded symbols:       total=%d (retained-not-evicted, ever-central protected)",
		int(m.Gauges["superseded_count"]))
	t.Logf("projection_churn:         %.3f (mean AnchorsProjectedAtTurn / TurnCount; idempotent-write guard keeps it bounded)",
		m.Gauges["projection_churn"])
	t.Logf("vague-new became-matchable turn: mean %.2f over %d campaigns (unmatchable before accretion)",
		m.Gauges["vague_match_turn_mean"], int(m.Gauges["vague_match_campaigns"]))

	// Within-session interleaving (SPEC §9.1). Low mean dwell + high
	// distinct-slots ⇒ highly interleaved; high dwell + low distinct ⇒
	// coherent/clustered. Measures the concern instead of asserting it.
	slots := m.Histograms[metricSessionDistinctSlots]
	threadsPer := m.Histograms[metricSessionDistinctThreads]
	turnsPer := m.Histograms[metricSessionTurns]
	dwell := m.Histograms[metricTopicDwellRunlen]
	t.Logf("=== workload interleaving ===")
	t.Logf("distinct slots/session:   mean %.2f max %d (within-session topic diversity)",
		mean(slots), int(maxOf(slots)))
	t.Logf("distinct threads/session: mean %.2f max %d",
		mean(threadsPer), int(maxOf(threadsPer)))
	t.Logf("turns/session:            mean %.2f", mean(turnsPer))
	t.Logf("topic dwell run-length:   mean %.2f (consecutive turns on one thread before a cross-topic move)",
		mean(dwell))

	// R6 (Build-Plan §4): history_len / ever_central must not blow past the
	// hard cap. The cap is a storage invariant the runtime enforces; a
	// max-per-thread above it at 1d would be an unbounded-growth design
	// signal (surface it, do not paper over it). RunScenario's per-step
	// invariants already assert substrate well-formedness; this is the
	// metric-level tripwire the §9.4 instrument exists to provide.
	if maxHist := int(m.Gauges["history_len_max"]); maxHist > historyCapForReport {
		t.Errorf("R6 design signal: max history_len/thread %d exceeds hard cap %d — "+
			"history is growing unbounded; investigate before tuning away", maxHist, historyCapForReport)
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

// historyCapForReport mirrors turn.historyCapPerThread (the §2.6.1 hard
// total cap, default 40) for the rung-summary reporting + R6 tripwire. It
// is duplicated here rather than imported because turn.historyCapPerThread
// is unexported; if that default changes, update this in lockstep.
const historyCapForReport = 40

// recordLifecycleMetrics folds the anchor-lifecycle (Inc 5) §9.4 metrics
// into the run's metrics blob. It draws from two sources, both
// metrics-package-free at the generator boundary:
//
//   - generator-owned recall oracles: the drift-origin / drift-dest /
//     abandoned-premise recall buckets (per-step hit/total tallied off
//     StepFeedback) and the vague-new became-matchable turn. The generator
//     exposes these as plain fields (gen.recallBuckets, gen.vagueMatchTurns)
//     the way it already exposes episodeQueriesToHit.
//   - post-run frontmatter + spine: per-thread ever-central count, history
//     length, superseded-symbol count, and the projection-churn watermark
//     ratio (AnchorsProjectedAtTurn / TurnCount). These are the steady-state
//     signals SOLUTION §5 obliges the sim to show flat across rung length.
//
// superseded_precision reuses the harness's global adversarial precision
// (mean over RecallMeasureOnly steps): low precision is the signal that
// abandoned/superseded threads are crowding out focused ones — the
// counterexample that would justify lowering recall.superseded-weight.
func recordLifecycleMetrics(t *testing.T, h *scenarios.Harness, gen *generator) {
	t.Helper()

	// Generator-owned recall buckets → recall ratio + observation count.
	for _, b := range []string{"drift_recall_origin", "drift_recall_dest", "abandoned_premise_recall"} {
		tally := gen.recallBuckets[b]
		ratio := 0.0
		obs := 0
		if tally != nil {
			obs = tally.total
			if tally.total > 0 {
				ratio = float64(tally.hits) / float64(tally.total)
			}
		}
		h.Metrics.Set(b, ratio)
		h.Metrics.Set(b+"_obs", float64(obs))
	}

	// Vague-new oracle: mean became-matchable turn over completed campaigns.
	if n := len(gen.vagueMatchTurns); n > 0 {
		sum := 0
		for _, v := range gen.vagueMatchTurns {
			sum += v
		}
		h.Metrics.Set("vague_match_turn_mean", float64(sum)/float64(n))
		h.Metrics.Set("vague_match_campaigns", float64(n))
	}

	// superseded_precision = mean global adversarial precision (already
	// recorded per RecallMeasureOnly step by the harness).
	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("recordLifecycleMetrics: read metrics blob: %v", err)
	}
	h.Metrics.Set("superseded_precision", mean(m.Histograms["recall_fidelity_adversarial_precision"]))

	// Post-run frontmatter + spine: per-thread lifecycle steady-state.
	fms, err := store.LoadAllThreadFrontmatter(h.Paths, nil)
	if err != nil {
		t.Fatalf("recordLifecycleMetrics: load frontmatter: %v", err)
	}
	spine, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		t.Fatalf("recordLifecycleMetrics: read spine: %v", err)
	}
	projectedAt := make(map[string]int, len(spine))
	for _, r := range spine {
		projectedAt[r.ID] = r.AnchorsProjectedAtTurn
	}

	var everCentralTotal, historyTotal, supersededTotal, everCentralMax, historyMax int
	var churnSum float64
	churnDenom := 0
	for _, fm := range fms {
		ec, hl, sup := 0, len(fm.HistorySymbols), 0
		for _, hs := range fm.HistorySymbols {
			if hs.EverCentral {
				ec++
			}
			if hs.Lifecycle == memops.LifecycleSuperseded {
				sup++
			}
		}
		everCentralTotal += ec
		historyTotal += hl
		supersededTotal += sup
		if ec > everCentralMax {
			everCentralMax = ec
		}
		if hl > historyMax {
			historyMax = hl
		}
		if fm.TurnCount > 0 {
			churnSum += float64(projectedAt[fm.ID]) / float64(fm.TurnCount)
			churnDenom++
		}
	}
	n := len(fms)
	meanOf := func(total int) float64 {
		if n == 0 {
			return 0
		}
		return float64(total) / float64(n)
	}
	h.Metrics.Set("ever_central_count", float64(everCentralTotal))
	h.Metrics.Set("ever_central_mean", meanOf(everCentralTotal))
	h.Metrics.Set("ever_central_max", float64(everCentralMax))
	h.Metrics.Set("history_len_total", float64(historyTotal))
	h.Metrics.Set("history_len_mean", meanOf(historyTotal))
	h.Metrics.Set("history_len_max", float64(historyMax))
	h.Metrics.Set("superseded_count", float64(supersededTotal))
	churn := 0.0
	if churnDenom > 0 {
		churn = churnSum / float64(churnDenom)
	}
	h.Metrics.Set("projection_churn", churn)
}

// drainSteps pulls every step out of a scenario's on-demand StepSource,
// returning them as a slice. The feedback passed into each Next() call
// is the zero value (Index -1) — the generator does not branch on
// feedback, so a fixed feedback yields the canonical workload.
func drainSteps(sc scenarios.Scenario) []scenarios.Step {
	var out []scenarios.Step
	src := sc.StepSource
	if src == nil {
		return sc.Steps
	}
	for {
		step, ok := src.Next(scenarios.StepFeedback{Index: -1})
		if !ok {
			return out
		}
		out = append(out, step)
	}
}

// TestGenerateWorkload_Deterministic verifies the core contract: a
// fixed config produces an identical step stream across runs. The
// generator is on-demand, so the streams are drained and compared
// step-for-step.
func TestGenerateWorkload_Deterministic(t *testing.T) {
	corpus := loadCorpusSlots(t)
	cfg := WorkloadConfig{Seed: simSeed, Duration: simDayDuration, Corpus: corpus}
	aSc, bSc := GenerateWorkload(cfg), GenerateWorkload(cfg)

	if aSc.Name != bSc.Name {
		t.Errorf("scenario name differs: %q vs %q", aSc.Name, bSc.Name)
	}
	a, b := drainSteps(aSc), drainSteps(bSc)
	if len(a) != len(b) {
		t.Fatalf("step count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Errorf("step %d differs between runs", i)
		}
	}

	// A different seed must produce a different step stream (sanity
	// check that the seed actually drives generation).
	c := drainSteps(GenerateWorkload(WorkloadConfig{Seed: simSeed + 1, Duration: simDayDuration, Corpus: corpus}))
	if reflect.DeepEqual(a, c) {
		t.Errorf("different seeds produced identical step lists")
	}
}

// TestWorkloadInterleaveMetrics_Invariants drains a small deterministic
// workload and asserts the within-session interleaving telemetry (SPEC
// §9.1) is populated and structurally consistent. It checks invariants,
// not exact values (which would be brittle): the four per-session series
// have equal length (one sample per non-empty session); within each
// session distinct-thread count ≤ turns and ≥ 1; and the dwell run-length
// samples partition the turns (their sum equals total turns).
func TestWorkloadInterleaveMetrics_Invariants(t *testing.T) {
	corpus := loadCorpusSlots(t)
	cfg := WorkloadConfig{Seed: simSeed, Duration: simDayDuration, Corpus: corpus}
	sc := GenerateWorkload(cfg)
	gen := sc.StepSource.(*generator)

	// Draining the on-demand stream runs every session, populating the
	// generator's interleaving slices as a side effect.
	steps := drainSteps(sc)
	if len(steps) == 0 {
		t.Fatal("no steps emitted; cannot exercise interleaving telemetry")
	}

	n := len(gen.sessionTurns)
	if n == 0 {
		t.Fatal("sessionTurns empty; interleaving telemetry not populated")
	}
	if len(gen.sessionDistinctSlots) != n || len(gen.sessionDistinctThreads) != n {
		t.Fatalf("per-session series length mismatch: turns=%d slots=%d threads=%d",
			n, len(gen.sessionDistinctSlots), len(gen.sessionDistinctThreads))
	}

	// Per-session structural invariants.
	totalTurns := 0
	for i := 0; i < n; i++ {
		turns := gen.sessionTurns[i]
		totalTurns += turns
		if turns < 1 {
			t.Errorf("session %d: turns=%d, want >=1 (only non-empty sessions push samples)", i, turns)
		}
		if dt := gen.sessionDistinctThreads[i]; dt < 1 || dt > turns {
			t.Errorf("session %d: distinct threads=%d not in [1,%d]", i, dt, turns)
		}
		if ds := gen.sessionDistinctSlots[i]; ds < 1 || ds > turns {
			t.Errorf("session %d: distinct slots=%d not in [1,%d]", i, ds, turns)
		}
	}

	// Dwell runs partition the emitted turns: every step belongs to
	// exactly one maximal same-thread run, so the run lengths sum to the
	// total turn count and each is >=1.
	dwellSum := 0
	for i, r := range gen.topicDwellRuns {
		if r < 1 {
			t.Errorf("dwell run %d: length=%d, want >=1", i, r)
		}
		dwellSum += r
	}
	if dwellSum != totalTurns {
		t.Errorf("dwell run-lengths sum to %d, want total turns %d", dwellSum, totalTurns)
	}
}

// TestSim_DormantResumptionDrivesMidTurnFetch proves the coverage gap
// the package doc used to flag as untested is now exercised: a
// generated workload schedules `resume` actions onto recently-dormant
// threads, each naming a thr_<n> that has fallen out of Layer B. That
// triggers the §5.5 mid-turn fetch — the runtime aborts the in-flight
// stream, fetches the thread, and re-prompts — a second model consult
// within one turn. The step-indexed mock re-serves the step's response
// for the re-prompt, so the run completes with no queue desync.
//
// The proof is the `topic.re-prompt` log line: it is emitted exactly
// when the §5.5 fetch fires. A non-zero count over a generated workload
// confirms resumption reached the runtime AND that RunScenario drove
// the two-consult turn cleanly (RunScenario t.Fatalf's on any turn.Run
// error, so reaching the assertion at all means no desync).
func TestSim_DormantResumptionDrivesMidTurnFetch(t *testing.T) {
	corpus := loadCorpusSlots(t)
	sc := GenerateWorkload(WorkloadConfig{
		Seed:     simSeed,
		Duration: simDayDuration,
		Corpus:   corpus,
	})

	// Timestamp suffix prevents rundata directory collision with prior
	// tests in the same `go test` invocation; see MAD review #56
	// burn-down. Without it, this test reuses the all-zeros sentinel
	// suffix and races with stragglers from a prior sim run.
	sc.Name = withRunTimestampSuffix(sc.Name)

	h := scenarios.RunScenario(t, sc)

	reprompts := logEventCount(t, h, "topic.re-prompt")
	if reprompts == 0 {
		t.Fatalf("no §5.5 mid-turn fetch observed: the generated workload " +
			"scheduled no resumption that reached the runtime")
	}
	t.Logf("§5.5 mid-turn fetches driven by dormant-thread resumption: %d", reprompts)

	// The fetch fires a thread.fetched context delta per resumed thread;
	// it must be at least the re-prompt count (one re-prompt may fetch
	// ≥1 thread).
	if fetched := logEventCount(t, h, "source=thread.fetched"); fetched < reprompts {
		t.Errorf("thread.fetched count %d < re-prompt count %d", fetched, reprompts)
	}
}

// logEventCount counts occurrences of substr across every day-log file
// under the harness's LogsDir.
func logEventCount(t *testing.T, h *scenarios.Harness, substr string) int {
	t.Helper()
	entries, err := os.ReadDir(h.Paths.LogsDir)
	if err != nil {
		t.Fatalf("logEventCount: read %s: %v", h.Paths.LogsDir, err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(h.Paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("logEventCount: read %s: %v", e.Name(), err)
		}
		count += strings.Count(string(body), substr)
	}
	return count
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

// maxOf returns the largest sample, or 0 for an empty series.
func maxOf(xs []float64) float64 {
	var m float64
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

// recordInterleaveMetrics folds the generator's within-session
// interleaving telemetry (SPEC §9.1) into the run's metrics blob: one
// sample per session for distinct slots/threads and turns, one sample per
// maximal same-thread dwell run for run-length. The generator owns the
// raw slices (no rng, pure post-emission bookkeeping); this keeps the
// generator metrics-package-free, mirroring recordLifecycleMetrics.
func recordInterleaveMetrics(h *scenarios.Harness, gen *generator) {
	for _, v := range gen.sessionDistinctSlots {
		h.Metrics.Record(metricSessionDistinctSlots, float64(v))
	}
	for _, v := range gen.sessionDistinctThreads {
		h.Metrics.Record(metricSessionDistinctThreads, float64(v))
	}
	for _, v := range gen.sessionTurns {
		h.Metrics.Record(metricSessionTurns, float64(v))
	}
	for _, v := range gen.topicDwellRuns {
		h.Metrics.Record(metricTopicDwellRunlen, float64(v))
	}
}

// closureCount counts `retire.complete` events in the harness's event
// log — one per closure event: a thread may close, resume, and close
// again, so this can exceed the number of distinct threads created. It
// reads h.Paths.LogsDir
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

// TestNonLooseTagsNeverPads — anchor-lifecycle Inc 5 deleted the §5.1
// 4-anchor floor and its stub-padding scaffold. Every emission now carries
// nonLooseTags verbatim (new-thread, engagement, refinement alike); 0
// anchors is legal (vague start) and the runtime's projection owns the
// AnchorProjectionMax ceiling. This test pins that nonLooseTags drops the
// loose cells and NEVER pads — the drift gap is now produced by the
// lifecycle mechanics (drift/invert supersession), not by the dead
// loose-tag-drop Jaccard asymmetry the stubs once preserved.
func TestNonLooseTagsNeverPads(t *testing.T) {
	// Five real tags, four marked loose → only one real anchor survives.
	slot := CorpusSlot{
		Topic:     "physics",
		Tags:      []string{"alpha", "beta", "gamma", "delta", "epsilon"},
		LooseMask: []bool{false, true, true, true, true},
		UserInput: "alpha appears with beta in the text",
	}
	if raw := nonLooseTags(slot); len(raw) != 1 || raw[0] != "alpha" {
		t.Fatalf("nonLooseTags must drop loose cells and NOT pad: got %v want [alpha]", raw)
	}

	// All-loose → empty (a legal 0-anchor emission), no padding.
	allLoose := CorpusSlot{
		Tags:      []string{"a", "b"},
		LooseMask: []bool{true, true},
	}
	if raw := nonLooseTags(allLoose); len(raw) != 0 {
		t.Errorf("all-loose slot: got %v want [] (0 anchors legal, no padding)", raw)
	}

	// No mask → all tags pass through unchanged.
	plain := CorpusSlot{Tags: []string{"a", "b", "c", "d", "e"}}
	if got := nonLooseTags(plain); len(got) != 5 {
		t.Errorf("unmasked slot: got %d anchors want 5 (no filtering, no padding)", len(got))
	}
}

// TestSaltSymbolNotHighSpecificity pins design risk #3: the per-thread
// salt symbol must NOT trip memops.IsHighSpecificity. A salt classed as
// high-specificity would carry a projection-class boost and distort
// anchor ranking. The `t<N>z` form is a plain lowercase identifier — not
// a URL, file path, or SHA-shaped hex run — so it must classify as
// ordinary. Checked across a spread of creation orders (including the
// boundary forms whose digit run could resemble a short hex token).
func TestSaltSymbolNotHighSpecificity(t *testing.T) {
	for _, order := range []int{0, 1, 7, 42, 255, 1000, 123456, 9999999} {
		sym := saltSymbol(order)
		if memops.IsHighSpecificity(sym) {
			t.Errorf("saltSymbol(%d)=%q classed high-specificity; salt must be ordinary", order, sym)
		}
	}
}

// TestSimJaccardMatchesScorer pins the shadow-set oracle's scoring rule
// to the runtime scorer's plain set Jaccard (scoring.go: |matched| /
// (|q|+|threadSet|-|matched|)) at the default superseded-weight. It also
// pins the family-sibling coherence case (§2.3): a query of the slot's
// tags against a sibling carrying {nonLooseTags} ∪ {salt} clears the
// threshold, while a non-sibling sharing no symbols does not.
func TestSimJaccardMatchesScorer(t *testing.T) {
	set := func(xs ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, x := range xs {
			m[x] = struct{}{}
		}
		return m
	}
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	// Sibling: same 5 topical tags + one private salt. Jaccard = 5/6.
	sibling := set("alpha", "beta", "gamma", "delta", "epsilon", saltSymbol(3))
	if got := simJaccard(tags, sibling); got < simRecallThreshold {
		t.Errorf("family sibling Jaccard=%.4f below threshold %.2f; sibling must still fire", got, simRecallThreshold)
	}
	if want := 5.0 / 6.0; absDiff(simJaccard(tags, sibling), want) > 1e-9 {
		t.Errorf("sibling Jaccard=%.6f want %.6f", simJaccard(tags, sibling), want)
	}

	// Non-sibling on a disjoint slot: no topical overlap, own salt. → 0.
	nonSibling := set("zeta", "eta", "theta", "iota", "kappa", saltSymbol(9))
	if got := simJaccard(tags, nonSibling); got >= simRecallThreshold {
		t.Errorf("disjoint non-sibling Jaccard=%.4f >= threshold; must not fire", got)
	}

	// Empty inputs score 0.
	if simJaccard(nil, sibling) != 0 || simJaccard(tags, nil) != 0 {
		t.Error("empty query or empty set must score 0")
	}
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
