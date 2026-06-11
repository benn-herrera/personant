package sim

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/scenarios"
	"personant/internal/store"
	"personant/internal/turn"
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
// re-anchored calendar day (SPEC §9.4). Each day is exactly 24h wide, so
// Duration/24h is the day count and 24h is exactly one work day: two ~6 h
// sessions split by a ~1 h break (~13 h of work), with the remaining ~11 h
// overnight emergent from the (absent, on a 1-day run) next re-anchor.
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
	"simulation span: 1d|1w, or `<N>d` calendar days like 30d, or a Go duration like 168h")

// parseSimDuration maps the -sim.duration flag value to a span. `1d` is
// the smoke rung and `1w` is the mock default (the §9.1 acceptance ladder
// proper is the day-based 1d/7d smoke + 15d→30d→60d→120d). A bare `<N>d`
// form (e.g. 30d, 120d) parses as N calendar days, since time.ParseDuration
// has no day unit. Any other value falls through to time.ParseDuration so
// an ad-hoc span like `72h` still works.
//
// The month aliases 1m/2m/6m are deliberately NOT accepted: they map to no
// ladder rung and were a source of conflation with the real day-based
// rungs. They must ERROR rather than silently misparse — and because
// time.ParseDuration("1m") would otherwise succeed as one MINUTE, the
// sub-hour range is rejected explicitly so the three stale aliases (and any
// other degenerate `m`/`s` span) fail loudly instead of running.
func parseSimDuration(s string) (time.Duration, error) {
	switch s {
	case "1d":
		return 24 * time.Hour, nil
	case "1w":
		return 7 * 24 * time.Hour, nil
	default:
		// Whole-day `<N>d` form: time.ParseDuration has no day unit, so
		// parse N ourselves. Fractional days (1.5d) are intentionally
		// unsupported — use the `h` form for sub-day spans.
		if rest, ok := strings.CutSuffix(s, "d"); ok {
			if n, err := strconv.Atoi(rest); err == nil && n > 0 {
				return time.Duration(n) * 24 * time.Hour, nil
			}
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, err
		}
		// Reject sub-hour spans so the retired month aliases 1m/2m/6m (which
		// ParseDuration reads as minutes) error instead of running a
		// degenerate span. Sim spans are days/weeks; the `h` form covers any
		// legitimate sub-day ad-hoc run.
		if d < time.Hour {
			return 0, fmt.Errorf("unsupported sim duration %q: use 1d|1w, `<N>d` calendar days, or a Go duration of at least 1h (the month aliases 1m/2m/6m were removed)", s)
		}
		return d, nil
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
		{"named 1w", "1w", 7 * 24 * time.Hour, false},
		{"general 15d", "15d", 360 * time.Hour, false},
		{"general 30d", "30d", 720 * time.Hour, false},
		{"general 120d", "120d", 2880 * time.Hour, false},
		{"go duration 168h", "168h", 168 * time.Hour, false},
		{"empty", "", 0, true},
		{"non-numeric day", "xyzd", 0, true},
		{"zero days", "0d", 0, true},
		// The retired month aliases must now ERROR, not silently misparse.
		// ParseDuration reads "1m"/"2m"/"6m" as minutes; the sub-hour guard
		// rejects them so a stale alias fails loudly instead of running a
		// degenerate span.
		{"retired alias 1m", "1m", 0, true},
		{"retired alias 2m", "2m", 0, true},
		{"retired alias 6m", "6m", 0, true},
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

// TestSim is the ONE configurable simulation entry point (#98). It
// generates a deterministic workload of the span chosen by -sim.duration
// (default 1d), drives it through the scenario harness, and logs a summary
// plus the extrapolated 6-month runtime. RunScenario already asserts no
// turn.Run error and runs DefaultInvariants per step, so "completes clean,
// invariants hold" comes for free; the smoke-band assertion here is a light
// check only.
//
// The two live toggles select live-vs-stand-in PER element, independently:
//
//   - both off (the default — bare `make test` / `make sim`): the full
//     deterministic MOCK ACCEPTANCE GATE. nil embedder (symbolic-only
//     stand-in), scripted mock client (canned-response stand-in), EVERY hard
//     gate active (recall_unexplained_absence==0, wander coherence, criteria
//     b/e, closures, R6, determinism). No config load, no endpoint touch, no
//     skip — this path is byte-for-byte the pre-#98 mock rung.
//   - -sim.live-embedding on: install the real §3.4 embedder as the recaller
//     (live config load from test/rundata + endpoint guard, FAIL-not-skip on
//     missing/unreachable). Mock inference stays, so the recall oracle stays
//     valid → the symbolic-vs-embedding head-to-head (Inc 2).
//   - -sim.live-inference on: install the live chat client and set oracleBlind
//     (downgrade the oracle/plan-dependent gates to logs, keep the storage
//     invariants hard — Inc 3); behavior-validation metrics; the SHORT
//     duration cap applies (a too-long span is refused, not silently run).
//
// The live config-load / endpoint-guard / duration-cap / oracle-blind are all
// CONDITIONAL branches gated on the flags, so the both-false `make test` path
// never reaches them.
func TestSim(t *testing.T) {
	d, err := parseSimDuration(*simDuration)
	if err != nil {
		t.Fatalf("invalid -sim.duration %q: %v (use 1d|1w, or `<N>d` calendar days like 30d, or a Go duration like 168h)",
			*simDuration, err)
	}

	// Live-vs-stand-in selection (#98). Both off → the mock acceptance gate
	// below runs with a nil recaller, the scripted mock client, oracleBlind
	// false, and no config/endpoint touch. setupLiveElements returns those
	// zero values when neither toggle is set, so the gate path is unchanged.
	recaller, liveClient, liveModel, oracleBlind := setupLiveElements(t, d)

	label := "sim-" + *simDuration
	if *liveEmbedding || *liveInference {
		label = "sim-live-" + *simDuration
	}

	corpus := loadCorpusSlots(t)
	h := runSimRung(t, label, d, corpus, recaller, oracleBlind, liveClient, liveModel)

	// Inference-in-loop behavior-validation summary (#98, Inc 3). These are the
	// real-model behaviors the mock structurally cannot exercise, derived from
	// the runtime's OWN event log (never the canned plan) — honest about the
	// live model even though the recall oracle is blind. Skipped on the mock
	// path (where -sim.live-inference is off).
	if *liveInference {
		reportInferenceBehavior(t, h)
	}

	// Light sanity band — a smoke rung, not a tuning gate. Derivation
	// (re-anchored 24h day, SPEC §9.4): a work day is 2 sessions of 6 h
	// turn-active time = 12 h of per-turn spans. With the 6:1 rapid:work
	// weighting the mean span is ~(6*RapidGap + 1*WorkGap)/7 = (6*42s +
	// 252s)/7 = 72 s (the deliberate ~72 s/turn center), so 12 h / 72 s
	// ≈ 600 NATURAL turns; ±30% jitter and weighting variance widen that to
	// ~[450, 750]. The Candidate-A main thread (#109) then injects one EXTRA
	// rng-free engagement every mainThreadEngageEvery natural turns (plus a
	// handful of intra-thread probes once the main thread scrolls past the
	// assembly window), so the OBSERVED turn count is ~natural × (1 +
	// 1/mainThreadEngageEvery) — at every-2 that is ~1.5×, widening the band
	// to [675, 1200]. Re-derive this if RapidGap/WorkGap, the rapid:work
	// weights, or mainThreadEngageEvery change. The band is 1-day-specific, so
	// it applies only when the span is exactly 24 h — longer rungs still get
	// runSimRung's clean-completion + well-formedness assertions, just not
	// this turn-count band.
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
		if turns < 675 || turns > 1200 {
			t.Errorf("turn count %d outside plausible band [675, 1200]", turns)
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
// runSimRung's recaller argument is the embedding-in-loop seam (#98): the
// mock acceptance path (TestSim with both toggles off) passes nil → the
// harness keeps the symbolic-only default recaller, and the run is
// byte-for-byte the pre-#98 mock rung. The live-embedding path (TestSim under
// -sim.live-embedding) passes a factory that builds
// measure.NewService(ops, embedder); the harness installs it, Prepares the
// index, and keeps it current via the per-thread-creation AddThread seam.
//
// oracleBlind is the inference-in-loop guard (#98, Inc 3). When true (set
// only by TestSim under -sim.live-inference), every gate whose ground
// truth is the deterministic generator's CANNED responses is downgraded
// from a t.Errorf failure to a logged observation: a real chat model emits
// different topic tags / symbols than the plan, so the runtime engages and
// creates different threads, which (a) invalidates the recall oracle and
// (b) breaks the generator's forward-planning coherence. The blinded gates
// are the recall-fidelity / wander-coherence / abandoned-topic-recall
// oracles, the §4.3 no-eviction coherence criteria (e), the closure-flow
// liveness check (thread engagement is plan-driven), and the
// recall_unexplained_absence canary. The substrate-invariant gates that do
// NOT depend on the oracle — the R6 history_len hard cap, thread-id
// well-formedness, RunScenario's per-step invariants — stay LIVE under
// blinding, because a real model cannot legitimately break a storage
// invariant. With oracleBlind false (every mock path) the run is unchanged.
func runSimRung(t *testing.T, label string, d time.Duration, corpus []CorpusSlot,
	recaller func(ops memops.MemoryOps) measure.Recaller, oracleBlind bool,
	liveClient model.Client, liveModel string) *scenarios.Harness {
	t.Helper()

	sc := GenerateWorkload(WorkloadConfig{
		Seed:     simSeed,
		Duration: d,
		Corpus:   corpus,
	})
	sc.Recaller = recaller
	// Inference-in-loop (#98, Inc 3): when a live chat client is supplied the
	// harness drives turns against it instead of the scripted mock, and
	// oracleBlind is set in lockstep (every caller pairs them). nil keeps the
	// mock path.
	sc.LiveClient = liveClient
	sc.LiveModel = liveModel

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

	// Per-sim-day-close stats series (#120): at the close of every sim-day the
	// harness fires this handler, which appends one run-to-date stats record to
	// daily.jsonl in the scenario directory. The acceptance-ladder rung points
	// (15/30/60/120d) and every intermediate day are then just READ from the
	// series — one long run replaces the re-simulate-from-zero rung walk — and a
	// run terminated mid-flight has all stats to that point safely cached. The
	// CORRECTNESS INVARIANT is that the run-to-date record at the close of day N
	// equals the end-of-run summary of a standalone N-day run (same seed): the
	// shadow-derived stats come from the SAME computeIntraThreadGauges the
	// end-of-run summary uses, and counters/percentiles are read run-to-date.
	daily := newDailySnapshotWriter(gen)
	sc.OnSimDayClose = daily.onDayClose

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

	// Fold the within-thread wander telemetry (§3.2/§5, criteria a/b): the
	// per-thread trajectory shape and the hop-graded abandoned-topic probe
	// buckets (current-topic recall, per-hop decay, per-hop coherence,
	// total divergence). Generator-owned, metrics-package-free at the seam.
	recordWanderMetrics(h, gen)

	// Fold the synthesis-thread telemetry (§2.7.3, #47/#42): the synthesis
	// event count, the per-event parent-count histogram, the borrowed-symbol
	// volume, and the per-sim-day material-structural-change histogram (#42).
	// Generator-owned, metrics-package-free at the seam.
	recordSynthesisMetrics(h, gen)

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
	p99 := percentile(durations, 0.99)
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

	// Fold the intra-thread recall telemetry (§9.2, #109): the §4.3 perf-decay
	// series (coarse_size==T, fine_chunks[_main], modeled cosine_ops, latency
	// p50/p95/p99, modeled flush rate) and the intra-thread oracle's per-hop
	// coherence curve + divergence. Needs the final spine size T and the turn
	// latency percentiles, so it runs here (after both are known) and re-writes
	// the blob, then re-reads m so the summary + gates below see the new series.
	recordIntraThreadMetrics(h, gen, finalPopulation, p50, p95, p99)
	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Fatalf("re-write metrics blob with intra-thread series: %v", err)
	}
	if m, err = readMetrics(h.MetricsPath); err != nil {
		t.Fatalf("re-read metrics blob: %v", err)
	}

	// Per-sim-day series final record (#120): emit the LAST daily.jsonl record
	// now that the end-of-run summary inputs (finalPopulation = liveThreads, the
	// turn-latency percentiles) are computed, using those EXACT values. The
	// interior ticks (days 1..N-1) already fired via sc.OnSimDayClose; this is
	// day N, carrying the trailing partial day the cadence tick at N×24h does not
	// catch (the run ends a few jittered steps past the tick). Because it uses the
	// summary's own inputs, this last record's run_to_date equals the end-of-run
	// summary by construction — the #120 correctness invariant that lets the
	// acceptance-ladder rungs be read from the series.
	daily.finalize(h, h.PinnedClock(), finalPopulation, p50, p95, p99)

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
		// Closure depends on which threads the runtime engaged/decayed, which is
		// plan-driven; under inference-in-loop the real model engages different
		// threads than the canned plan, so a zero is an observation, not a defect.
		if oracleBlind {
			t.Logf("closures: got 0 (live-inference: plan-driven closure not asserted)")
		} else {
			t.Errorf("closures: got 0; the §3.5 closure flow is dead — expected >0 over the %s workload", label)
		}
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

	// Embedding-vs-symbolic head-to-head (#98). Present only on an
	// embedding-in-loop run (embed_recall_fidelity_steps > 0); the mock
	// acceptance run never installs an embedder, so the series is absent and
	// this line is skipped — keeping the mock summary unchanged. The two
	// recall means below are scored against the SAME archival-forgiven
	// expected set on the SAME workload (recordEmbedRecallFidelity), so the
	// comparison is apples-to-apples. The REAL gap-closure number is produced
	// by `make sim LIVE_EMBEDDING=true` against reaper; a MockEmbedder
	// run exercises the machinery but not semantic recall quality.
	if esteps := m.Counters["embed_recall_fidelity_steps"]; esteps > 0 {
		t.Logf("=== embedding-vs-symbolic recall head-to-head (#98) ===")
		t.Logf("embedding recall: measured over %d steps, mean recall=%.3f mean precision=%.3f mean F1=%.3f",
			esteps,
			mean(m.Histograms["embed_recall_fidelity_recall"]),
			mean(m.Histograms["embed_recall_fidelity_precision"]),
			mean(m.Histograms["embed_recall_fidelity_f1"]))
		t.Logf("(symbolic adversarial mean recall=%.3f over %d steps — compare; embedding closes the #96 gap iff it recovers what symbolic misses)",
			mean(m.Histograms["recall_fidelity_adversarial_recall"]),
			m.Counters["recall_fidelity_adversarial_steps"])
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

	// Within-thread wander (#96 increment 2). Criterion (a): trajectory
	// shape — mean distinct topics > 1 with a tail to wanderMaxHops proves
	// threads are non-monotonic. Criterion (b): current-topic recall holds
	// (~0.95), abandoned-topic recall DECAYS with hop distance, and the
	// oracle/runtime COHERENCE is the pass (zero divergence; the decay is
	// the deliverable, not a failure).
	topicsDistinct := m.Histograms[metricThreadTopicsDistinct]
	wanderHopsHist := m.Histograms[metricThreadWanderHops]
	t.Logf("=== within-thread wander (#96) ===")
	t.Logf("thread topics distinct:   mean %.3f max %d (criterion a: >1.0 with tail to %d = non-monotonic)",
		mean(topicsDistinct), int(maxOf(topicsDistinct)), wanderMaxHops)
	t.Logf("thread wander hops:       mean %.3f max %d (trajectory length distribution)",
		mean(wanderHopsHist), int(maxOf(wanderHopsHist)))
	t.Logf("wander_current_recall:    %.3f (%d obs) — current topic must surface (>=~0.95)",
		m.Gauges[metricWanderCurrentRecall], int(m.Gauges[metricWanderCurrentRecall+"_obs"]))
	// Per-hop decay + coherence curve (hop 0 = current topic, included so
	// the curve shows the full active→abandoned gradient).
	// embedHeadToHead is true on an embedding-in-loop run; it gates the
	// per-hop embedding column so the mock summary stays unchanged.
	embedHeadToHead := m.Counters["embed_recall_fidelity_steps"] > 0
	for hop := 0; hop < wanderMaxHops; hop++ {
		key := fmt.Sprintf("_h%d", hop)
		obs := int(m.Gauges[metricWanderOriginRecallByHops+key+"_obs"])
		if obs == 0 {
			continue
		}
		if embedHeadToHead {
			// Gap-closure view (#98): symbolic vs embedding per-hop recall on
			// one denominator. Embedding "closes the gap" iff embed_recall
			// stays high at hops where symbolic origin_recall has decayed.
			t.Logf("  hop %d: symbolic_recall=%.3f embed_recall=%.3f coherence=%.3f (%d obs)",
				hop,
				m.Gauges[metricWanderOriginRecallByHops+key],
				m.Gauges[metricWanderEmbedRecallByHops+key],
				m.Gauges[metricWanderCoherenceByHops+key],
				obs)
			continue
		}
		t.Logf("  hop %d: origin_recall=%.3f coherence=%.3f (%d obs)",
			hop,
			m.Gauges[metricWanderOriginRecallByHops+key],
			m.Gauges[metricWanderCoherenceByHops+key],
			obs)
	}
	divergence := int(m.Gauges[metricWanderCoherenceDivergence])
	t.Logf("wander coherence divergence: %d (criterion b PASS = 0; decay is expected, divergence is the failure)",
		divergence)

	// Criterion (b) pass condition: oracle and runtime must AGREE per hop.
	// Real divergence (oracle predicts hit, runtime misses or vice-versa)
	// beyond a tiny band means the §4.3 eviction-free coherence assumption
	// broke — STOP, do not paper over it (the design's trip-wire). A small
	// band absorbs borderline-Jaccard rounding where the runtime's exact
	// scorer and the oracle's set-Jaccard sit on opposite sides of 0.4 by a
	// hair; a structural break shows up as divergence scaling with obs.
	totalProbeObs := 0
	for hop := 0; hop < wanderMaxHops; hop++ {
		key := fmt.Sprintf("_h%d", hop)
		totalProbeObs += int(m.Gauges[metricWanderOriginRecallByHops+key+"_obs"])
	}
	if totalProbeObs > 0 && !oracleBlind {
		divergenceBand := totalProbeObs / 20 // 5% rounding band
		if divergence > divergenceBand {
			t.Errorf("criterion (b) FAILURE: wander coherence divergence %d exceeds band %d over %d probe obs — "+
				"oracle and runtime DISAGREE on abandoned-topic recall; the §4.3 eviction-free coherence "+
				"assumption broke (check criterion e / history_len_max). Do NOT trust the decay curve until resolved.",
				divergence, divergenceBand, totalProbeObs)
		}
	} else if oracleBlind && totalProbeObs > 0 {
		// The abandoned-topic recall oracle compares the runtime's match-fires
		// against the canned plan; under live inference the runtime fired on a
		// different thread set, so divergence here measures plan-vs-model drift,
		// not a coherence break. Report, do not fail.
		t.Logf("wander coherence divergence %d over %d probe obs (live-inference: oracle blind — not asserted)",
			divergence, totalProbeObs)
	}

	// Intra-thread recall (#109, §9.2). The §4.3 perf-decay series (the I3/I4
	// gates) and the intra-thread oracle's per-hop coherence curve. H2 HONESTY:
	// the hop-recall curve here is the SYMBOLIC shadow-chunk oracle's predicted
	// recoverability — a COHERENCE signal, not validated user recall. The
	// embedding-quality head-to-head is the live-embedding run (Inc 6 / #98), not
	// this number; a green symbolic curve does NOT mean "recall works for users".
	embeddingRun := gen.embeddingRun
	t.Logf("=== intra-thread recall (#109) ===")
	t.Logf("recall_index_coarse_size: %d (== final spine size T; flat per-thread-length, the I4 check)",
		int(m.Gauges[metricRecallIndexCoarseSize]))
	t.Logf("recall_index_fine_chunks: %d total, %d main-thread durable/kept (the I3 length axis; PRNG keep/toss trim active — convo=%d%% tool=%d%% transient)",
		int(m.Gauges[metricRecallIndexFineChunks]), int(m.Gauges[metricRecallIndexFineChunksMain]),
		convoTransientPct, toolTransientPct)
	t.Logf("recall_query_cosine_ops:  %.1f per-query MEASURED (§7.2; counted at the scoring call sites — coarse + engaged flat/descent; 0 on the symbolic mock run where no embedding recall ran)",
		m.Gauges[metricRecallQueryCosineOps])
	t.Logf("recall_query_latency:     P50=%.1fms P95=%.1fms P99=%.1fms (I3 gate: P99 flat across the rung ladder)",
		m.Gauges[metricRecallQueryLatencyP50], m.Gauges[metricRecallQueryLatencyP95], m.Gauges[metricRecallQueryLatencyP99])
	t.Logf("recall_index_flush:       %d calls / %d chunks (modeled debt-cap N=%d flush rate — the cost N pays)",
		int(m.Gauges[metricRecallIndexFlushCalls]), int(m.Gauges[metricRecallIndexFlushChunks]), intraThreadDebtCap)

	intraProbeObs := 0
	for _, total := range gen.intraHopTotal {
		intraProbeObs += total
	}
	if intraProbeObs == 0 {
		t.Logf("intra-thread probes:      none this run (rung too short to scroll the main thread past the assembly window + debt cap)")
	} else if embeddingRun {
		// Symbolic-vs-embedding intra-thread head-to-head (#109/#111 Finding B):
		// the LIVE replacement for the mock-only intra coherence gate. The
		// SYMBOLIC column is the oracle's predicted/coherence curve (H2 — what
		// the oracle says should be recoverable, NOT validated user recall); the
		// EMBEDDING column is the recall users ACTUALLY get (the runtime's intra
		// fine-tier match-fire for the same forgiven ground truth). Both scored
		// against the identical predicted-leaf ground truth on one denominator,
		// exactly the wander head-to-head. ORDERING (#109 Finding A): until A
		// lands (beam k raised → recall_intra_descent_divergence==0), the
		// embedding column reads slightly low vs symbolic — expected, resolves
		// with A; do NOT misread a pre-A shortfall as recall loss.
		t.Logf("intra-thread hop-recall head-to-head (symbolic predicted/coherence curve, H2 — vs embedding-observed = actual user recall):")
		maxHop := 0
		for hop := range gen.intraHopTotal {
			if hop > maxHop {
				maxHop = hop
			}
		}
		for hop := 1; hop <= maxHop; hop++ {
			key := fmt.Sprintf("_h%d", hop)
			obs := int(m.Gauges[metricRecallIntraHopRecall+key+"_obs"])
			if obs == 0 {
				continue
			}
			t.Logf("  hop %d: symbolic_recall=%.3f embed_recall=%.3f coherence=%.3f (%d obs)",
				hop, m.Gauges[metricRecallIntraHopRecall+key],
				m.Gauges[metricRecallIntraEmbedHopRecall+key],
				m.Gauges[metricRecallIntraHopRecall+key+"_coherence"], obs)
		}
		t.Logf("recall_intra_blindspot_misses: %d (predicted+observed miss inside the debt-window blind spot — by-design lag, not loss)",
			int(m.Gauges[metricRecallIntraBlindspotMisses]))
	} else {
		// Symbolic-only (mock) run: the intra layer never fires (no embedder), so
		// only the oracle's PREDICTED recoverability curve is meaningful; observed
		// recall and coherence are by-construction 0 (no layer to fire), exactly
		// as the embedding head-to-head column is absent on the mock run.
		t.Logf("intra-thread hop-recall (oracle PREDICTED recoverability — symbolic-only run, intra layer off; observed n/a):")
		maxHop := 0
		for hop := range gen.intraHopTotal {
			if hop > maxHop {
				maxHop = hop
			}
		}
		for hop := 1; hop <= maxHop; hop++ {
			key := fmt.Sprintf("_h%d", hop)
			obs := int(m.Gauges[metricRecallIntraHopRecall+key+"_obs"])
			if obs == 0 {
				continue
			}
			t.Logf("  hop %d: predicted_recall=%.3f (%d obs)",
				hop, m.Gauges[metricRecallIntraHopRecall+key], obs)
		}
	}

	// Intra-thread recall by TURN-DEPTH (#109 H2 quality curve, turn-depth axis)
	// — the SIBLING of the hop curve above, bucketed by how many turns back the
	// oracle-predicted recall target scrolled out (log-scale, powers of B=16).
	// REPORT-ONLY characterization (no gate, no floor): the multi-year-thread
	// no-decay fidelity, the complement to the recall_query_cosine_ops cost
	// curve. The embedding column is 0 at every bucket on the mock run (intra
	// layer off), exactly as the hop embedding column is. Sorted by bucket so the
	// shallow→deep ordering reads left-to-right.
	if intraProbeObs > 0 {
		depthBuckets := make([]int, 0, len(gen.intraDepthTotal))
		for b := range gen.intraDepthTotal {
			depthBuckets = append(depthBuckets, b)
		}
		sort.Ints(depthBuckets)
		if len(depthBuckets) > 0 {
			if embeddingRun {
				t.Logf("intra-thread recall by turn-depth (symbolic predicted vs embedding-observed = actual user recall; report-only):")
			} else {
				t.Logf("intra-thread recall by turn-depth (oracle PREDICTED recoverability — symbolic-only run, observed n/a; report-only):")
			}
		}
		for _, b := range depthBuckets {
			key := fmt.Sprintf("_d%d", b)
			obs := int(m.Gauges[metricRecallIntraRecallByDepth+key+"_obs"])
			if obs == 0 {
				continue
			}
			predicted := m.Gauges[metricRecallIntraRecallByDepth+key]
			embed := m.Gauges[metricRecallIntraEmbedRecallByDepth+key]
			// Range-validate to [0,1] as a sanity log only (mirrors the hop
			// curve's intent) — report-only, never fail the test on these values.
			if predicted < 0 || predicted > 1 || embed < 0 || embed > 1 {
				t.Logf("  WARN depth %s: ratio out of [0,1] (predicted=%.3f embed=%.3f)",
					intraDepthBucketLabel(b), predicted, embed)
			}
			if embeddingRun {
				t.Logf("  depth %s: predicted=%.3f embed=%.3f (%d obs)",
					intraDepthBucketLabel(b), predicted, embed, obs)
			} else {
				t.Logf("  depth %s: predicted=%.3f (%d obs)",
					intraDepthBucketLabel(b), predicted, obs)
			}
		}
	}

	// Intra-thread coherence divergence — the #109 tripwire. SCOPED MOCK-ONLY
	// (#109/#111 Finding B). This gate compares the SYMBOLIC shadow-chunk
	// oracle's predicted recoverability against the runtime's intra fine-tier
	// match-fire. That comparison is only valid when both sides are symbolic:
	//   - MOCK run (*liveEmbedding == false): the intra tier fires from the
	//     deterministic MockEmbedder (≈ symbolic), so oracle and runtime agree
	//     and divergence==0 is a real coherence canary — ZERO-TOLERANCE, same
	//     discipline as the wander-coherence gate. KEPT HARD here.
	//   - LIVE-EMBEDDING run (*liveEmbedding == true): the intra tier fires from
	//     a real embedding descent/flat scan. Per #96 (symbolic Jaccard ≠
	//     embedding recall — an established, intended difference, not a bug), a
	//     symbolic oracle CANNOT predict embedding recall, so this comparison
	//     legitimately diverges (a 14d live run measured 21/251) and a ==0 hard
	//     gate cannot hold across the symbolic/embedding boundary. We still
	//     COMPUTE and REPORT the divergence (do not hide the signal) but do NOT
	//     fail on it. The live coherence story is the deferred embedding↔symbolic
	//     head-to-head curve (Inc 6 / #98), not a coherence==0 gate.
	// Contrast with the wander-coherence gate (symbolic-oracle-vs-SYMBOLIC
	// match-fire — valid on live, stays asserted) and the W1 descent gate
	// (runtime-vs-runtime, same embeddings — valid on live, untouched here).
	intraDivergence := int(m.Gauges[metricRecallIntraCoherenceDivergence])
	t.Logf("intra-thread coherence divergence: %d (#109 mock PASS = 0; the decay curve is the deliverable, divergence is the failure)",
		intraDivergence)
	if *liveEmbedding {
		// Live-embedding run: report only — see the scoping rationale above (#96).
		t.Logf("intra-thread coherence divergence %d over %d probe obs (live-embedding: symbolic oracle vs embedding runtime — "+
			"NOT asserted; the head-to-head curve is the live deliverable, not coherence==0)",
			intraDivergence, intraProbeObs)
	} else if embeddingRun && !oracleBlind && intraDivergence != 0 {
		t.Errorf("#109 FAILURE: intra-thread coherence divergence %d != 0 over %d probe obs — the shadow-chunk oracle and "+
			"the runtime fine tier DISAGREE on early-content recall outside the debt-window blind spot. Root-cause the "+
			"oracle/runtime coherence (do NOT widen forgiveness — that disables the canary).",
			intraDivergence, intraProbeObs)
	}

	// W1 recall-preservation QUALITY MEASURE (#111 / design §7.1, the
	// approximation-drift canary). recall_intra_descent_divergence is the
	// run-total top-Kf leaf set difference between the summary-tree DESCENT and
	// the flat scan, accumulated by the harness over every intra-probe step on
	// an embedding-live run (recall_intra_descent_probes is its denominator).
	// The metric keys mirror the scenarios-package harness constants
	// (metricRecallIntraDescent*); they are the string contract between the
	// harness emitter and this reader, like recall_unexplained_absence below.
	//
	// REPORTED, NOT GATED (#119): the within-thread summary tree is an
	// APPROXIMATE O(log n) recall heuristic that trades exactness for speed.
	// A nonzero divergence means the heuristic substituted a within-top-Kf leaf,
	// NOT necessarily that recall was lost (a strict_miss is a real ranking
	// defect; a tie/tree_mismatch is a sub-perceptible boundary effect — see the
	// §7.1 classification below). Exhaustive/exact recall is the job of the
	// separate EXACT tiers (#117 grep + flat-scan), where it is guaranteed; it
	// is not the approximate tree's contract. So this value is logged as a
	// drift canary, not asserted to zero. On the symbolic mock run no W1 probe
	// runs (no embedder, no usable tree), so probes==0 and divergence==0
	// trivially — reported as "no probe ran".
	descentDivergence := int(m.Counters["recall_intra_descent_divergence"])
	descentProbes := int(m.Counters["recall_intra_descent_probes"])
	if descentProbes == 0 {
		t.Logf("recall_intra_descent_divergence: n/a — no W1 probe ran this rung (symbolic mock run, or main thread never grew a usable summary tree)")
	} else {
		t.Logf("recall_intra_descent_divergence (quality measure): %d over %d W1 probes — descent-vs-flat top-Kf divergence; approximation-drift canary. Nonzero = the heuristic substituted a within-Kf leaf, NOT necessarily lost recall — see the §7.1 classification below and the exact tiers (#117) for guaranteed-exhaustive recall.",
			descentDivergence, descentProbes)
		// W1 divergence classification (#111 §7.1 DIAGNOSTIC) — the headline
		// that says WHICH kind each divergence is: strict-miss (a strictly
		// better leaf was pruned → real recall loss, fix keys/beam), tie (an
		// equal-cosine leaf was substituted → not lost recall, fix a tie-break),
		// or tree-mismatch (the flat scan ranked a leaf the tree does not
		// contain → staleness/build edge). Two prior key strategies both gave
		// exactly the same divergence count; this breakdown settles whether
		// that is key-quality (strict-miss) or structural (tie/tree-mismatch).
		// Pure observation; per-probe detail is in logs/ as recall.W1-diag.
		t.Logf("recall_intra_w1 classification: strict_miss=%d tie=%d tree_mismatch=%d (#111 §7.1 diagnostic — strict_miss=real loss/fix keys; tie=equal-cosine/fix tie-break; tree_mismatch=staleness/build edge)",
			int(m.Counters[metricRecallIntraW1StrictMiss]),
			int(m.Counters[metricRecallIntraW1Tie]),
			int(m.Counters[metricRecallIntraW1TreeMismatch]))
	}

	// F-B rebuild trip-wire (#111 / design §7.2, fork F-B). recall_intra_tree_
	// rebuild_calls counts within-thread summary-tree (re)builds across the run's
	// sleep cycles. It is a WATCH metric, NOT a hard gate: if it trends up with
	// main-thread length on the long rungs, semantic rebalancing is not staying
	// bounded and the design escalates to the hybrid recency-cap MAD. Reported
	// beside the W1 gate so the rebuild churn is visible on every rung.
	t.Logf("recall_intra_tree_rebuild_calls: %d (F-B trip-wire — semantic-rebalance churn; watch, no gate; escalates to the hybrid MAD if it trends with main-thread length)",
		int(m.Counters["recall_intra_tree_rebuild_calls"]))

	// Criterion (e): max per-thread retained symbol count must stay under
	// the eviction cap — the no-eviction precondition the oracle's
	// coherence rests on (§4.3). The generator's own shadow retained set is
	// the authoritative per-thread emitted-symbol union; assert its max is
	// below the cap. (history_len_max above is the runtime-side companion.)
	maxRetained := 0
	for i := range gen.threads {
		if gen.isCarrier(i) || gen.isMainThread(i) {
			// Dedicated measurement carriers absorb the abandoned-topic-probe
			// and campaign-recall query pollution by design (#96 inc 2); they
			// are excluded from the recall oracle on both sides, so their
			// bloated shadow set is not a no-eviction-coherence hazard and is
			// not counted against the cap. Runtime-side their history is
			// strictly capped at 40 (history.cap-per-thread), and their
			// ~5-symbol-query Jaccard denominator dilutes below the §3.4
			// threshold, so they never fire as a false recall match.
			//
			// The Candidate-A main thread (#109) is likewise excluded: its
			// retained set is UNBOUNDED by design — its multi-year trajectory is
			// the intra-thread fine tier's reason to exist. The wander oracle's
			// no-eviction (criterion e) precondition is about the 40-symbol
			// abandoned-topic threads; the main thread's intra-thread recall is
			// scored by the dedicated intra-thread probe (recall_intra_hop_recall),
			// not by symbolic whole-thread Jaccard, so its growth is not a
			// criterion-(e) hazard.
			continue
		}
		if n := len(gen.shadowRetainedSet(i)); n > maxRetained {
			maxRetained = n
		}
	}
	t.Logf("max per-thread retained symbols: %d (criterion e: must stay < eviction cap %d)",
		maxRetained, historyCapForReport)
	if maxRetained >= historyCapForReport && !oracleBlind {
		// This is the generator's shadow-set no-eviction precondition for the
		// abandoned-topic oracle. Under live inference that oracle is off, so the
		// precondition it guards is moot — the shadow set still reflects the canned
		// plan, not what the live runtime retained. Report only.
		t.Errorf("criterion (e) FAILURE: max per-thread retained symbols %d >= eviction cap %d — "+
			"the oracle's no-eviction assumption (§4.3) breaks; eviction modeling now required",
			maxRetained, historyCapForReport)
	}

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

	// Genuine-loss canary (#100): recall_unexplained_absence counts every
	// recall expectation whose thread is OFF the live spine AND NOT in the
	// archive.archived log — a thread that is neither recall-live nor
	// recoverable-via-fetch, the recall-measurement sibling of
	// VerifyThreadAccounting's unexplained loss. The oracle no longer
	// manufactures these (the refinement expected-set excludes threads the
	// runtime has not yet materialized on the spine — recallExpectedForMaterialized),
	// so the counter is a clean integrity signal: any nonzero value is a
	// real off-spine-not-archived data loss. Gate hard on ==0 so a future
	// genuine loss FAILS the sim rather than hiding in the summary. This is
	// the payoff of the #100 oracle fix — the canary is now usable.
	if absent := m.Counters["recall_unexplained_absence"]; absent != 0 {
		// The canary keys off the recall oracle's expected set (built from canned
		// tags). Under inference-in-loop the runtime engages/creates threads the
		// plan never named, so the oracle names threads the runtime never
		// materialized — manufacturing false absences. The gate is meaningless
		// when the model is real; report the count, do not fail.
		if oracleBlind {
			t.Logf("recall_unexplained_absence = %d (live-inference: oracle expected-set is plan-derived, "+
				"runtime diverged — canary not asserted)", absent)
		} else {
			t.Errorf("recall_unexplained_absence = %d, want 0 — a recall expectation names a thread that is "+
				"OFF the live spine AND NOT archived (genuine integrity loss, or an uncovered oracle/execution-timing "+
				"artifact). This is the genuine-loss canary; do NOT relax it without root-causing every count.", absent)
		}
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

	reportSleepCycles(t, m, d)

	return h
}

// sleepCycleDayOffPeriod is the day index modulus the workload uses to
// schedule a day-off (every 7th day, index 6/13/…). A run spanning at
// least this many calendar days crosses ≥1 day-off and must therefore fire
// ≥1 sleep cycle (#108).
const sleepCycleDayOffPeriod = 7

// reportSleepCycles logs the sleep-cycle (#108) summary — count fired and
// the .git footprint reclaimed by substrate gc — and, when the run spanned
// at least one day-off, hard-asserts that ≥1 cycle ran with its pre/post
// .git-size gauges populated. A run too short to reach a day-off records 0
// and asserts nothing (the marker simply never fired).
func reportSleepCycles(t *testing.T, m metricsBlob, d time.Duration) {
	t.Helper()
	// Metric keys match the harness-side constants in package scenarios
	// (harness_run.go): sleep_cycles, git_dir_bytes_pre_gc/post_gc,
	// git_dir_bytes_reclaimed. The harness owns the emit-side constants; the
	// metrics blob is consumed cross-package by string key, the same
	// convention as "turns"/"turn_duration_ms" elsewhere in this file.
	cycles := m.Counters["sleep_cycles"]
	pre := m.Histograms["git_dir_bytes_pre_gc"]
	post := m.Histograms["git_dir_bytes_post_gc"]
	reclaimed := m.Counters["git_dir_bytes_reclaimed"]

	t.Logf("=== sleep cycles (#108) ===")
	t.Logf("sleep_cycles:     %d", cycles)
	t.Logf("git_dir_bytes:    pre-gc mean=%.0f post-gc mean=%.0f reclaimed total=%d",
		mean(pre), mean(post), reclaimed)

	spannedDayOff := d >= sleepCycleDayOffPeriod*24*time.Hour
	if !spannedDayOff {
		return
	}
	if cycles < 1 {
		t.Errorf("sleep_cycles = %d over a %s run that spans ≥1 day-off — the day-off "+
			"consolidation hook is dead (expected ≥1)", cycles, d)
	}
	if len(pre) == 0 || len(post) == 0 {
		t.Errorf("sleep-cycle .git-size gauges missing: pre samples=%d post samples=%d "+
			"(expected one per cycle, %d cycles)", len(pre), len(post), cycles)
	}
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

// cadenceDayStartThreshold separates a re-anchored day boundary from an
// in-day session break: the emergent overnight idle is ~11 h and a day-off
// idle is ~24+ h, while the inter-session break is ~1 h and per-turn spans are
// ~72 s. A TimeDelta at or above this threshold marks the FIRST turn of a new
// re-anchored calendar day. 5 h sits cleanly between the ~1 h break and the
// ~11 h overnight.
const cadenceDayStartThreshold = 5 * time.Hour

// dayStartInstants drains the step stream and returns the absolute simulated
// instant (cumulative TimeDelta sum from t=0) of each re-anchored day's first
// turn — the steps whose TimeDelta crosses cadenceDayStartThreshold, plus the
// very first step (t=0). SleepCycle day-off markers are included as day starts
// (they carry the day's emergent idle as their TimeDelta), matching the
// re-anchored-day model. This reconstructs the timeline the harness builds by
// accumulating TimeDeltas onto its pinned clock.
func dayStartInstants(steps []scenarios.Step) []time.Duration {
	out := dayStartBoundaries(steps)
	starts := make([]time.Duration, len(out))
	for i, b := range out {
		starts[i] = b.instant
	}
	return starts
}

// dayBoundary is one re-anchored day's first step: its absolute simulated
// instant (cumulative TimeDelta sum from t=0) and whether that step is the
// no-turn SleepCycle day-off marker (#108) rather than a work turn. The kind
// matters to the no-precession guard: the original guard ran only 7 days and so
// never reached a WORK boundary AFTER a day-off, the exact slot a phase-shifting
// day-off would corrupt.
type dayBoundary struct {
	instant time.Duration
	sleep   bool
}

// dayStartBoundaries reconstructs each re-anchored day's first step the way the
// harness's pinned clock does — by accumulating TimeDeltas from t=0 — and
// records, per boundary, the absolute instant and whether it is the day-off
// SleepCycle marker. The day-off marker carries the day's emergent idle as its
// TimeDelta, so it crosses cadenceDayStartThreshold and counts as that calendar
// day's boundary, keeping the boundary index aligned with the calendar/grid day.
func dayStartBoundaries(steps []scenarios.Step) []dayBoundary {
	var out []dayBoundary
	var now time.Duration
	for i, s := range steps {
		now += s.TimeDelta
		if i == 0 || s.TimeDelta >= cadenceDayStartThreshold {
			out = append(out, dayBoundary{instant: now, sleep: s.SleepCycle})
		}
	}
	return out
}

// TestCadence_DayStartsNoPrecession is the #121 regression guard: with the
// re-anchored 24h day, consecutive day-starts are ~24 h apart and stay inside
// the ±15 min jitter window of base+N*24h — the work pattern does NOT precess.
// The prior accumulator summed 27.5 h per "day" (6h + 3.5h + 6h + 12h) and
// drifted ~3.5 h/day, which this test would catch as both an out-of-window
// start and a >24h+slack inter-day spacing.
//
// It spans ≥9 calendar days so it crosses the 7th-day day-off (index 6) AND
// asserts the WORK days AFTER it (indices 7, 8, …) stay on the grid. This is
// the gap the original 7-day guard missed: a day-off that fails to occupy a
// clean 24h grid slot shifts every subsequent work-day's phase (the #121
// residual — a ~11 h jump observed in a live run), and that shift only shows up
// at boundary 7+, past where the 7-day run ended. The day-off is a CLEAN
// 24h-preserving grid slot — its own dayIndex slot, on the base+N*24h grid —
// so work resumes the next day at the SAME phase (SPEC §9.4.1).
func TestCadence_DayStartsNoPrecession(t *testing.T) {
	corpus := loadCorpusSlots(t)
	// 9 calendar days: 6 work + 1 day off + 2 more work days — long enough to
	// cross the day-off re-anchor (index 6) AND land two work boundaries after
	// it (indices 7, 8), where a phase-shifting day-off would surface.
	steps := drainSteps(GenerateWorkload(WorkloadConfig{
		Seed: simSeed, Duration: 9 * 24 * time.Hour, Corpus: corpus,
	}))
	bounds := dayStartBoundaries(steps)
	if len(bounds) < 9 {
		t.Fatalf("expected >=9 day boundaries over a 9-day run, got %d", len(bounds))
	}

	// The 7th day (index 6) must be the day-off SleepCycle marker, and the days
	// around it work turns — confirms the run actually crosses a day-off so the
	// post-day-off anchoring below is genuinely exercised (not a coverage no-op).
	if !bounds[6].sleep {
		t.Errorf("boundary 6 should be the day-off SleepCycle marker, but it is a work boundary")
	}
	if bounds[5].sleep || bounds[7].sleep {
		t.Errorf("boundaries 5 and 7 should be work boundaries (got sleep=%v, %v)",
			bounds[5].sleep, bounds[7].sleep)
	}

	// (a) each day boundary — INCLUDING the work days after the day-off — is
	// within ±dayStartJitter of its calendar anchor N*24h. The off-day occupies
	// its own grid slot (index 6), so N counts calendar/grid days and the
	// post-off work days sit at index 7, 8, … on the same grid. A day-off that
	// introduced any systematic phase shift would push boundary 7+ out of this
	// window — the regression the 7-day guard could not see.
	for n, b := range bounds {
		anchor := time.Duration(n) * dayLength
		drift := b.instant - anchor
		if drift < -dayStartJitter || drift > dayStartJitter {
			kind := "work"
			if b.sleep {
				kind = "day-off"
			}
			t.Errorf("day %d (%s) start at %v drifts %v from anchor %v (must be within ±%v — precession/day-off-shift regression)",
				n, kind, b.instant, drift, anchor, dayStartJitter)
		}
	}

	// (b) consecutive day boundaries are 24 h apart to within the jitter span
	// (a ±15m start on each end → at most ±30m spacing wobble). This holds
	// ACROSS the day-off too: work-day-5 → day-off and day-off → work-day-7 are
	// each one clean 24h slot, so the day-off adds no extra inter-day spacing.
	for i := 1; i < len(bounds); i++ {
		gap := bounds[i].instant - bounds[i-1].instant
		lo, hi := dayLength-2*dayStartJitter, dayLength+2*dayStartJitter
		if gap < lo || gap > hi {
			t.Errorf("day-boundary gap %d→%d = %v, want ~24h within [%v, %v] (no precession; day-off must not add phase)",
				i-1, i, gap, lo, hi)
		}
	}
}

// TestCadence_DayOffPreservesPhase asserts the day-off itself advances the
// clock by exactly one clean 24h grid slot — phase in == phase out (SPEC
// §9.4.1). It compares the time-of-day (instant mod 24h, i.e. phase against the
// base+N*24h grid) of the last WORK boundary BEFORE the day-off with the first
// WORK boundary AFTER it. They must match within the jitter span: the day-off
// must add no SYSTEMATIC offset, only the zero-mean ±15 min start fuzz on each
// end. The OLD behavior (an additive day-off gap that knocked the anchor ~11 h
// off the grid) would fail this; the clean 24h-preserving slot passes it.
func TestCadence_DayOffPreservesPhase(t *testing.T) {
	corpus := loadCorpusSlots(t)
	steps := drainSteps(GenerateWorkload(WorkloadConfig{
		Seed: simSeed, Duration: 9 * 24 * time.Hour, Corpus: corpus,
	}))
	bounds := dayStartBoundaries(steps)
	if len(bounds) < 8 {
		t.Fatalf("expected >=8 day boundaries, got %d", len(bounds))
	}

	// Boundary 6 is the day-off marker; 5 is the work day before it, 7 the work
	// day after. The day-off must advance the clock by EXACTLY one clean 24h grid
	// slot on EACH side: work-day-5 → day-off and day-off → work-day-7. An
	// additive day-off gap (the old ~30 h idle) makes the in-gap (5→6) blow past
	// 24h and the out-gap (6→7) fall short — phase in != phase out. Each clean
	// slot is 24h ± the two jittered endpoints (≤ ±30 min spacing wobble).
	if bounds[6].sleep == false {
		t.Fatalf("boundary 6 expected to be the day-off marker; got a work boundary")
	}
	lo, hi := dayLength-2*dayStartJitter, dayLength+2*dayStartJitter
	gapIn := bounds[6].instant - bounds[5].instant   // work-day-5 → day-off
	gapOut := bounds[7].instant - bounds[6].instant  // day-off → work-day-7
	if gapIn < lo || gapIn > hi {
		t.Errorf("work-day → day-off advance = %v, want one clean 24h slot within [%v, %v] — "+
			"the day-off must occupy a clean 24h grid slot, not an additive idle gap (SPEC §9.4.1)",
			gapIn, lo, hi)
	}
	if gapOut < lo || gapOut > hi {
		t.Errorf("day-off → work-day advance = %v, want one clean 24h slot within [%v, %v] — "+
			"work must resume at the SAME phase the day before the day-off (SPEC §9.4.1)",
			gapOut, lo, hi)
	}

	// Phase in == phase out: the work day before the day-off and the work day
	// after sit at the SAME time-of-day on the grid. Their grid-drifts differ by
	// at most the two ±15 min start-jitters; a systematic day-off offset would
	// open this gap.
	before := signedGridDrift(bounds[5].instant)
	after := signedGridDrift(bounds[7].instant)
	if delta := after - before; delta < -2*dayStartJitter || delta > 2*dayStartJitter {
		t.Errorf("day-off shifted work phase by %v (before=%v after=%v); must be 0 within ±%v (SPEC §9.4.1)",
			delta, before, after, 2*dayStartJitter)
	}
}

// signedGridDrift folds an absolute instant onto its signed distance to the
// nearest base+N*24h grid line, in (-12h, 12h]. A start jittered ±15 min around
// the grid yields a drift of ±15 min regardless of which side of midnight it
// landed, so two on-grid starts compare cleanly without a midnight-wrap
// artifact.
func signedGridDrift(instant time.Duration) time.Duration {
	d := instant % dayLength
	if d > dayLength/2 {
		d -= dayLength
	}
	return d
}

// TestCadence_SessionSpansAndBreak asserts the within-day structure: each of
// the two daily sessions covers ~6 h of turn-active time, and the break
// between them is ~1 h (SPEC §9.4). It walks ONE work day's natural turns
// (drained from a 1-day run, skipping zero-delta injected steps) and finds the
// single large in-day gap (the inter-session break); the spans before and
// after it are the two session active windows.
func TestCadence_SessionSpansAndBreak(t *testing.T) {
	corpus := loadCorpusSlots(t)
	steps := drainSteps(GenerateWorkload(WorkloadConfig{
		Seed: simSeed, Duration: simDayDuration, Corpus: corpus,
	}))

	// Reconstruct per-step absolute instants; the first step is t=0 (day
	// start). The inter-session break is the lone in-day TimeDelta in the
	// ~1 h band; the day's last instant minus the break splits the two
	// sessions. Per-turn spans are ~72 s, so the break stands out cleanly.
	var now time.Duration
	var breakIdx = -1
	var breakDelta, lastInstant time.Duration
	for i, s := range steps {
		now += s.TimeDelta
		lastInstant = now
		if i == 0 {
			continue // day start, no in-day gap
		}
		// In-day break: bigger than any per-turn span, smaller than the
		// overnight day boundary (none in a 1-day run anyway).
		if s.TimeDelta > 20*time.Minute && s.TimeDelta < cadenceDayStartThreshold {
			if breakIdx != -1 {
				t.Fatalf("found >1 in-day break (steps %d and %d) — a work day has exactly one inter-session break", breakIdx, i)
			}
			breakIdx, breakDelta = i, s.TimeDelta
		}
	}
	if breakIdx == -1 {
		t.Fatal("no inter-session break found in a 1-day run")
	}

	// (a) break ≈ 1 h, within the ±30% jitter band.
	wantBreak := 1 * time.Hour
	if breakDelta < jitterLo(wantBreak) || breakDelta > jitterHi(wantBreak) {
		t.Errorf("inter-session break = %v, want ~%v within jitter [%v, %v]",
			breakDelta, wantBreak, jitterLo(wantBreak), jitterHi(wantBreak))
	}

	// Session 1 active span = instant just before the break (the break step's
	// instant minus the break delta). Session 2 active span = last instant −
	// break-step instant. Each must be ~6 h (the last turn crossing the
	// threshold adds up to one per-turn span of slop above 6 h).
	var breakInstant time.Duration
	now = 0
	for i, s := range steps {
		now += s.TimeDelta
		if i == breakIdx {
			breakInstant = now
			break
		}
	}
	sess1 := breakInstant - breakDelta
	sess2 := lastInstant - breakInstant
	for _, sess := range []struct {
		name string
		span time.Duration
	}{{"session 1", sess1}, {"session 2", sess2}} {
		// Active span ≈ sessionActive; the loop ends on the first turn whose
		// cumulative span crosses 6 h, so the realized span is [6h, 6h + one
		// per-turn span]. Allow a generous +1 h ceiling for jitter on that last
		// span and a small floor below 6 h.
		if sess.span < sessionActive-30*time.Minute || sess.span > sessionActive+1*time.Hour {
			t.Errorf("%s active span = %v, want ~%v (±jitter)", sess.name, sess.span, sessionActive)
		}
	}
}

// jitterLo / jitterHi are the ±30% bounds jitter() can produce for base (see
// the [0.7, 1.3) factor). Used by the cadence tests to band a single jittered
// duration.
func jitterLo(base time.Duration) time.Duration { return time.Duration(float64(base) * 0.7) }
func jitterHi(base time.Duration) time.Duration { return time.Duration(float64(base) * 1.3) }

// TestCadence_PerTurnCenterAndTurnsPerDay asserts the two headline cadence
// numbers (SPEC §9.4): the blended per-turn clock advance centers at ~72 s,
// and a work day yields ~600 NATURAL turns. It measures the per-turn span as
// the mean of the small (per-turn) TimeDeltas — excluding the day-start and
// inter-session-break gaps and the zero-delta injected steps — over a 1-day run.
func TestCadence_PerTurnCenterAndTurnsPerDay(t *testing.T) {
	corpus := loadCorpusSlots(t)
	steps := drainSteps(GenerateWorkload(WorkloadConfig{
		Seed: simSeed, Duration: simDayDuration, Corpus: corpus,
	}))

	var spanSum time.Duration
	var spanCount, naturalTurns int
	for i, s := range steps {
		// Natural turns are the ones that advanced the clock by a per-turn
		// span: non-zero, sub-break TimeDelta. The first step (t=0) and the
		// inter-session break are excluded from the per-turn mean; the
		// zero-delta injected probe/main-thread steps are excluded too.
		if i == 0 {
			naturalTurns++ // the day's first turn is a natural turn (zero delta)
			continue
		}
		if s.TimeDelta == 0 {
			continue // injected zero-delta step (probe / main-thread engage)
		}
		// A non-zero delta is a natural turn; the inter-session break is the
		// session-2 opener (a natural turn) but its delta is the break, not a
		// per-turn span — count it as a turn, exclude it from the span mean.
		naturalTurns++
		if s.TimeDelta >= 20*time.Minute {
			continue // inter-session break, not a per-turn span
		}
		spanSum += s.TimeDelta
		spanCount++
	}
	if spanCount == 0 {
		t.Fatal("no per-turn spans measured")
	}

	// (d) per-turn center ≈ 72 s. The blend of 42 s / 252 s at 6:1 is exactly
	// 72 s; ±30% jitter and weighting variance keep the realized mean within a
	// tight band around it.
	meanSpan := spanSum / time.Duration(spanCount)
	const wantCenter = 72 * time.Second
	if meanSpan < 60*time.Second || meanSpan > 84*time.Second {
		t.Errorf("mean per-turn span = %v, want ~%v (±~17%%)", meanSpan, wantCenter)
	}
	t.Logf("measured per-turn center: %v over %d spans", meanSpan, spanCount)

	// (e) ~600 natural turns/day. 12 h of active time / 72 s ≈ 600; ±30%
	// jitter and weighting variance widen the band.
	if naturalTurns < 450 || naturalTurns > 750 {
		t.Errorf("natural turns/day = %d, want ~600 within [450, 750]", naturalTurns)
	}
	t.Logf("measured natural turns/day: %d", naturalTurns)
}

// TestIntraThreadWindowMirrorsRuntime asserts the intra-thread oracle's
// scroll-out boundary (intraThreadTurnWindow) tracks the runtime's assembly
// window (store.ThreadTurnWindow). The two MUST agree or the oracle's "this
// chunk is index material" predicate diverges from the runtime's I6 boundary,
// silently breaking intra-thread coherence. The sim package cannot import
// turn's unexported embeddingDebtCap, so the debt-cap mirror is a documented
// invariant (intraThreadDebtCap == turn.embeddingDebtCap == 16) checked by
// hand; the window mirror is machine-checked here.
func TestIntraThreadWindowMirrorsRuntime(t *testing.T) {
	if intraThreadTurnWindow != store.ThreadTurnWindow {
		t.Fatalf("intraThreadTurnWindow=%d must mirror store.ThreadTurnWindow=%d "+
			"(the I6 scroll-out boundary the runtime and the intra-thread oracle share)",
			intraThreadTurnWindow, store.ThreadTurnWindow)
	}
}

// TestKeepTossTrim_DeterministicAndShrinksLeafSet exercises the PRNG keep/toss
// model (#111 / design §7.3): the durable (kept) leaf count must (a) be
// deterministic for a given seed — two generators draw the identical
// classification — and (b) shrink the emitted excerpt set toward the
// (1-rate) keep fraction. The SYMBOL union (emittedSyms) and the full shadow
// chunk list are unaffected (the oracle/W1 alignment), only the durable count
// reported as fine_chunks is reduced.
func TestKeepTossTrim_DeterministicAndShrinksLeafSet(t *testing.T) {
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	slot := CorpusSlot{Topic: "physics", Tags: tags}
	newGen := func() *generator {
		return GenerateWorkload(WorkloadConfig{
			Seed: simSeed, Duration: simDayDuration,
			Corpus: []CorpusSlot{slot}, FamilySize: 1,
		}).StepSource.(*generator)
	}

	const n = 1000
	emit := func(g *generator) {
		g.threads = append(g.threads, newThread(0, 0))
		for i := 0; i < n; i++ {
			g.recordEmission(0, tags, convoTransientPct)
		}
	}

	g1, g2 := newGen(), newGen()
	emit(g1)
	emit(g2)

	// (a) Determinism: identical seed → identical kept/trimmed split and an
	// identical per-chunk transient flag sequence.
	if g1.retainedChunks != g2.retainedChunks || g1.trimmedChunks != g2.trimmedChunks {
		t.Fatalf("keep/toss not deterministic: g1 kept/trimmed = %d/%d, g2 = %d/%d",
			g1.retainedChunks, g1.trimmedChunks, g2.retainedChunks, g2.trimmedChunks)
	}
	for i := range g1.shadowChunks[0] {
		if g1.shadowChunks[0][i].transient != g2.shadowChunks[0][i].transient {
			t.Fatalf("transient flag diverged at chunk %d", i)
		}
	}

	// (b) Shrink: durable count ≈ (1-convoTransientPct/100)·n, within a band.
	// The full shadow list is untrimmed (oracle/W1 alignment); only the durable
	// count is reduced.
	if got := len(g1.shadowChunks[0]); got != n {
		t.Errorf("shadow chunk list = %d, want all %d emitted (trim must NOT drop chunks — production over-retains)", got, n)
	}
	kept := durableChunkCount(g1.shadowChunks[0])
	wantKeep := n * (100 - convoTransientPct) / 100
	band := n / 10 // ±10% sampling band
	if kept < wantKeep-band || kept > wantKeep+band {
		t.Errorf("durable kept = %d, want ~%d (1-%d%% of %d) within ±%d", kept, wantKeep, convoTransientPct, n, band)
	}
	// The symbol union is unaffected by the trim (keep/toss trims leaves, not
	// history_symbols): all five tags plus the salt remain.
	if got := len(g1.shadowRetainedSet(0)); got < len(tags) {
		t.Errorf("symbol union = %d, want >= %d — trim wrongly shrank history_symbols", got, len(tags))
	}
}

// TestIntraProbeOracle_BlindSpotBoundary is a focused logic test of the
// intra-thread oracle's three-band scroll-out predicate (§8.2): a chunk is
// (1) still in the assembly window → not index material; (2) scrolled out but
// inside the debt-window blind spot → predicted MISS (by-design lag); (3)
// scrolled out past the blind spot AND clearing the threshold → predicted HIT.
// It seeds the main thread's shadow chunks directly and drives one probe,
// asserting the oracle's prediction matches the band the queried slot's
// oldest chunk falls in.
func TestIntraProbeOracle_BlindSpotBoundary(t *testing.T) {
	corpus := loadCorpusSlots(t)
	g := GenerateWorkload(WorkloadConfig{Seed: simSeed, Duration: simDayDuration, Corpus: corpus}).
		StepSource.(*generator)

	// Stand up a main thread with two trajectory slots: an EARLY slot whose
	// chunks are deep in the past (well past the blind spot) and a CURRENT
	// slot. cur = total chunk count.
	earlySlot := 0
	currentSlot := 1
	for g.model.slots[currentSlot].Topic == g.model.slots[earlySlot].Topic {
		currentSlot++ // ensure a distinct topic so the trajectory is a real wander
	}
	g.mainThreadIdx = g.createThread(len(g.threads), earlySlot)
	g.threads[g.mainThreadIdx].traj = []int{earlySlot, currentSlot}
	g.threads[g.mainThreadIdx].cur = currentSlot

	earlyTags := nonLooseTags(g.model.slots[earlySlot])
	if len(earlyTags) == 0 {
		t.Skip("early slot has no non-loose tags; pick another corpus")
	}
	// One early chunk far in the past (recallable), then enough current-slot
	// chunks to push the early chunk well past window+debtCap.
	g.shadowChunks[g.mainThreadIdx] = []chunkRecord{
		{turnNumber: 1, slotIdx: earlySlot, tags: earlyTags},
	}
	for n := 2; n <= intraThreadTurnWindow+intraThreadDebtCap+50; n++ {
		g.shadowChunks[g.mainThreadIdx] = append(g.shadowChunks[g.mainThreadIdx],
			chunkRecord{turnNumber: n, slotIdx: currentSlot, tags: nonLooseTags(g.model.slots[currentSlot])})
	}

	// Force the probe to query hop 1 (the early slot).
	g.intraProbeHopCursor = 0
	bs, ok := g.buildIntraProbeStep()
	if !ok {
		t.Fatal("buildIntraProbeStep returned ok=false; expected a probe with a scrolled-out early slot")
	}
	if bs.intraProbe == nil {
		t.Fatal("probe bufStep carries no intraProbe metadata")
	}
	if !bs.intraProbe.predictHit {
		t.Errorf("oracle predicted MISS for an early chunk well past the blind spot that re-issues its own tags; want HIT")
	}
	if bs.intraProbe.blindspot {
		t.Errorf("oracle flagged blind spot for a chunk scrolled out far past window+debtCap; want false")
	}

	// Now make the early chunk's age land INSIDE the blind spot: shrink the
	// current-slot tail so cur - 1 sits in [window, window+debtCap).
	g2 := GenerateWorkload(WorkloadConfig{Seed: simSeed, Duration: simDayDuration, Corpus: corpus}).
		StepSource.(*generator)
	g2.mainThreadIdx = g2.createThread(len(g2.threads), earlySlot)
	g2.threads[g2.mainThreadIdx].traj = []int{earlySlot, currentSlot}
	g2.threads[g2.mainThreadIdx].cur = currentSlot
	g2.shadowChunks[g2.mainThreadIdx] = []chunkRecord{{turnNumber: 1, slotIdx: earlySlot, tags: earlyTags}}
	// cur such that cur-1 is >= window (scrolled out) but < window+debtCap (blind spot).
	for n := 2; n <= intraThreadTurnWindow+intraThreadDebtCap/2; n++ {
		g2.shadowChunks[g2.mainThreadIdx] = append(g2.shadowChunks[g2.mainThreadIdx],
			chunkRecord{turnNumber: n, slotIdx: currentSlot, tags: nonLooseTags(g2.model.slots[currentSlot])})
	}
	g2.intraProbeHopCursor = 0
	bs2, ok := g2.buildIntraProbeStep()
	if !ok {
		t.Fatal("buildIntraProbeStep (blind-spot case) returned ok=false")
	}
	if bs2.intraProbe.predictHit {
		t.Errorf("oracle predicted HIT for an early chunk INSIDE the debt-window blind spot; want MISS (by-design lag)")
	}
	if !bs2.intraProbe.blindspot {
		t.Errorf("oracle did not flag blind spot for a chunk scrolled out < debtCap turns ago; want true")
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

// recordWanderMetrics folds the within-thread wander telemetry (§3.2/§5,
// criteria (a)/(b)) into the run's metrics blob:
//
//   - per-thread trajectory shape (criterion a): distinct-topics and
//     wander-hops histograms, proving threads are non-monotonic (mean > 1,
//     tail to wanderMaxHops).
//   - the abandoned-topic probe buckets (criterion b): the current-topic
//     control recall, the per-hop abandoned-topic recall DECAY curve, and
//     the per-hop oracle/runtime COHERENCE rate plus the total divergence
//     count (the pass condition — coherence, not a recall floor).
//
// The generator owns the raw counters (no rng, pure measurement
// bookkeeping); this keeps the generator metrics-package-free, mirroring
// recordLifecycleMetrics / recordInterleaveMetrics.
func recordWanderMetrics(h *scenarios.Harness, gen *generator) {
	// Per-thread trajectory shape. One sample per thread; distinct topics
	// is the set of distinct slots in the trajectory (== len(traj) given
	// nextWanderSlot's always-different-topic move, but computed as a set
	// so the metric is honest if that ever changes).
	for i := range gen.threads {
		if gen.isCarrier(i) || gen.isMainThread(i) {
			// A measurement carrier is not a genuine user thread; the
			// Candidate-A main thread (#109) has an UNBOUNDED trajectory by
			// design and would dominate the topics-distinct / wander-hops
			// histograms, which characterize ordinary within-thread wander
			// (capped at wanderMaxHops). Its trajectory shape is the intra-thread
			// probe's concern, not the wander curve's.
			continue
		}
		thr := gen.threads[i]
		distinct := map[int]struct{}{}
		for _, s := range thr.traj {
			distinct[s] = struct{}{}
		}
		h.Metrics.Record(metricThreadTopicsDistinct, float64(len(distinct)))
		h.Metrics.Record(metricThreadWanderHops, float64(len(thr.traj)))
	}

	// Current-topic control recall.
	if gen.wanderCurrentTotal > 0 {
		h.Metrics.Set(metricWanderCurrentRecall,
			float64(gen.wanderCurrentHits)/float64(gen.wanderCurrentTotal))
		h.Metrics.Set(metricWanderCurrentRecall+"_obs", float64(gen.wanderCurrentTotal))
	}

	// Per-hop abandoned-topic recall decay + oracle/runtime coherence.
	divergence := 0
	for hop, total := range gen.wanderHopTotal {
		if total == 0 {
			continue
		}
		key := fmt.Sprintf("_h%d", hop)
		h.Metrics.Set(metricWanderOriginRecallByHops+key,
			float64(gen.wanderHopHits[hop])/float64(total))
		h.Metrics.Set(metricWanderOriginRecallByHops+key+"_obs", float64(total))
		h.Metrics.Set(metricWanderCoherenceByHops+key,
			float64(gen.wanderHopCoherent[hop])/float64(total))
		// Embedding-layer per-hop recall (#98 head-to-head), same denominator
		// as the symbolic curve above. Zero at every hop on the mock run
		// (no embed-match-fires); the gap-closure signal on an embedding-in-
		// loop run is this curve staying high where the symbolic curve decays.
		h.Metrics.Set(metricWanderEmbedRecallByHops+key,
			float64(gen.wanderHopEmbedHits[hop])/float64(total))
		divergence += gen.wanderHopDiverge[hop]
	}
	h.Metrics.Set(metricWanderCoherenceDivergence, float64(divergence))
}

// recordSynthesisMetrics folds the synthesis-thread telemetry (§2.7.3,
// #47/#42) into the run's metrics blob:
//
//   - metricSynthesisEvents (counter): synthesis threads created.
//   - metricSynthesisParents (histogram): parent count per event (= K
//     today, recorded so a future variable-K distribution is visible).
//   - metricSynthesisBorrowedSymbols (counter): total borrowed parent tags
//     blended in — sim-side observability for the derived_from realism
//     element (NOT a production stamp; see the metric doc / Part-2 finding).
//   - metricMaterialStructuralChangesPerDay (histogram): one sample per
//     sim-day, composing thread-creations + synthesis events (#42).
//
// The generator owns the raw counters (no rng, pure measurement
// bookkeeping); this keeps the generator metrics-package-free, mirroring
// recordWanderMetrics / recordLifecycleMetrics.
func recordSynthesisMetrics(h *scenarios.Harness, gen *generator) {
	if gen.synthesisEvents > 0 {
		h.Metrics.Counter(metricSynthesisEvents, int64(gen.synthesisEvents))
	}
	for _, n := range gen.synthesisParentCounts {
		h.Metrics.Record(metricSynthesisParents, float64(n))
	}
	if gen.synthesisBorrowedSymbols > 0 {
		h.Metrics.Counter(metricSynthesisBorrowedSymbols, int64(gen.synthesisBorrowedSymbols))
	}
	for _, n := range gen.structuralChangesPerDay {
		h.Metrics.Record(metricMaterialStructuralChangesPerDay, float64(n))
	}
}

// recordIntraThreadMetrics folds the intra-thread recall telemetry (§9.2,
// #109) into the run's metrics blob — the §4.3 perf-decay series and the
// intra-thread oracle's hop-recall coherence curve:
//
//   - coarse_size == T (the I4 check), fine_chunks (total) + _main (the
//     length axis), the modeled cosine_ops (O(T·D + Kc·C·D), §4.3),
//     latency p50/p95/p99 (the I3 gate; sourced from turn_duration_ms), and
//     the modeled flush call/chunk rate (the cost N pays).
//   - the per-hop intra-thread recall curve (recall_intra_hop_recall, the
//     symbolic predicted-recoverability curve — H2, NOT embedding quality),
//     its _obs denominators and _coherence companions, the blind-spot miss
//     count, and the run-total coherence divergence (the #109 tripwire).
//
// liveThreads is the final spine size T; p50/p95/p99 are the turn-latency
// percentiles already computed by the caller. All shadow reads go through the
// generator (no runtime index read, F6); the generator stays
// metrics-package-free at the seam, mirroring recordWanderMetrics.
func recordIntraThreadMetrics(h *scenarios.Harness, gen *generator, liveThreads int, p50, p95, p99 float64) {
	// MEASURED recall_query_cosine_ops (§7.2): read the recaller's actual cosine
	// comparison tally (counted at the scoring call sites — coarse + the engaged
	// flat-scan OR descent), divided by the queries that performed them. This
	// REPLACES the prior modeled formula: the O(C_main)→O(log n) perf bend is now
	// empirical, not derived. On the symbolic-only mock run no embedding recall
	// ran, so the reporter reports 0 queries → the metric is 0 (honestly
	// unavailable; the bend is measurable only on an embedding-live run, where a
	// usable tree drives the descent). recordCosineOpsMeasured handles both.
	//
	// recordCosineOpsMeasured reads the LIVE recaller (not the generator shadow),
	// so it stays here as a direct Set — it is not part of the pure shadow-derived
	// compute the daily snapshot reuses.
	recordCosineOpsMeasured(h)
	// W1 divergence classification (#111 §7.1 diagnostic): the strict-miss /
	// tie / tree-mismatch breakdown of recall_intra_descent_divergence is
	// accumulated by the HARNESS at the per-probe call site (the
	// recall_intra_w1_* COUNTERS), in lockstep with the divergence counter, so
	// it survives the per-session Service instance churn a RestartSession step
	// causes — the #111 fix. The summary below reads those counters directly
	// (m.Counters), so no post-run remap is needed here. 0 on the mock run; the
	// headline on a live-embedding run.

	// Every remaining gauge is a pure function of the generator shadow state +
	// (liveThreads, p50/p95/p99); compute them once and Set them, so the daily
	// run-to-date snapshot (#120) can call the SAME pure function mid-run without
	// re-deriving the math or mutating the final gauges. The end-of-run numbers
	// are byte-for-byte what computeIntraThreadGauges returns here.
	for k, v := range computeIntraThreadGauges(gen, liveThreads, p50, p95, p99) {
		h.Metrics.Set(k, v)
	}
}

// computeIntraThreadGauges is the PURE shadow-derived half of
// recordIntraThreadMetrics (#120): it returns the §4.3 perf-decay + intra-thread
// hop/depth gauge map from the generator's CURRENT shadow state plus the supplied
// (liveThreads, p50/p95/p99), WITHOUT touching h.Metrics. Because the generator
// accumulates its shadow counters step-by-step, calling this mid-run yields the
// run-to-date values; calling it at end-of-run yields the final values — and the
// last daily snapshot therefore equals the end-of-run summary for the same seed
// (the #120 correctness invariant). recordCosineOpsMeasured (which reads the live
// recaller, not the shadow) and the W1 classification counters are NOT included
// here — they are set/accumulated by their own call sites.
func computeIntraThreadGauges(gen *generator, liveThreads int, p50, p95, p99 float64) map[string]float64 {
	g := map[string]float64{}

	// §4.3 perf-decay series.
	g[metricRecallIndexCoarseSize] = float64(liveThreads)
	g[metricRecallIndexFineChunks] = float64(gen.totalFineChunks())
	g[metricRecallIndexFineChunksMain] = float64(gen.mainThreadChunkCount())
	g[metricRecallQueryLatencyP50] = p50
	g[metricRecallQueryLatencyP95] = p95
	g[metricRecallQueryLatencyP99] = p99
	// Modeled flush rate: every scrolled-out chunk is eventually flushed
	// (flush_chunks), in batches of intraThreadDebtCap (flush_calls). This is
	// the embed-call cost the debt cap N pays over the rung.
	scrolledOut := gen.mainThreadScrolledOut()
	g[metricRecallIndexFlushChunks] = float64(scrolledOut)
	g[metricRecallIndexFlushCalls] = float64(scrolledOut / intraThreadDebtCap)

	// Per-hop intra-thread recall + coherence.
	divergence := 0
	for hop, total := range gen.intraHopTotal {
		if total == 0 {
			continue
		}
		key := fmt.Sprintf("_h%d", hop)
		// Symbolic predicted-recoverability curve (H2): the fraction of probes
		// at this hop the oracle predicts recoverable. This is the #109 fidelity
		// curve "symbolic now" — a coherence signal, NOT validated user recall.
		g[metricRecallIntraHopRecall+key] = float64(gen.intraHopPredictHit[hop]) / float64(total)
		g[metricRecallIntraHopRecall+key+"_obs"] = float64(total)
		// Oracle/runtime coherence at this hop — meaningful only on an
		// embedding-live run (the mock run leaves the observed/coherence tallies
		// at 0, so this reads 0.0 there and is reported as "n/a — symbolic-only").
		g[metricRecallIntraHopRecall+key+"_coherence"] = float64(gen.intraHopCoherent[hop]) / float64(total)
		// Embedding-OBSERVED per-hop recall (#109/#111 Finding B head-to-head),
		// scored against the SAME forgiven ground truth as the symbolic curve
		// above and on the SAME denominator (intraHopTotal), so the two columns
		// are directly comparable. intraHopObservedHit is the runtime's
		// spine.intra-match-fire for the oracle-predicted leaf, recorded only on
		// an embedding-live run — 0 at every hop on the mock run (no intra
		// layer), exactly as metricWanderEmbedRecallByHops is on the mock run.
		// This is the recall users actually get; mirror of how wander derives
		// metricWanderEmbedRecallByHops from its embed-match-fire observations.
		g[metricRecallIntraEmbedHopRecall+key] = float64(gen.intraHopObservedHit[hop]) / float64(total)
		divergence += gen.intraHopDiverge[hop]
	}

	// Per-turn-depth intra-thread recall (#109 H2 quality curve, turn-depth
	// axis) — the SIBLING of the per-hop curve, on a log-scale turn-depth bucket
	// (powers of B=16). Same derivation as the hop pair: symbolic predicted curve
	// (recall_intra_recall_bydepth, every run) + an _obs companion, plus the
	// embedding-observed curve (recall_intra_embed_recall_bydepth, 0 at every
	// bucket on the mock run — no intra layer — exactly as the embed-hop column
	// is). REPORT-ONLY characterization: no gate, no floor.
	for bucket, total := range gen.intraDepthTotal {
		if total == 0 {
			continue
		}
		key := fmt.Sprintf("_d%d", bucket)
		g[metricRecallIntraRecallByDepth+key] = float64(gen.intraDepthPredictHit[bucket]) / float64(total)
		g[metricRecallIntraRecallByDepth+key+"_obs"] = float64(total)
		g[metricRecallIntraEmbedRecallByDepth+key] = float64(gen.intraDepthObservedHit[bucket]) / float64(total)
	}
	g[metricRecallIntraCoherenceDivergence] = float64(divergence)
	g[metricRecallIntraBlindspotMisses] = float64(gen.intraBlindspotMisses)

	return g
}

// dailyFilename is the per-sim-day stats series the #120 day-close handler
// appends to, inside the scenario's rundata directory (h.RunHome). One JSON
// object per line, one line per sim-day, in day order.
const dailyFilename = "daily.jsonl"

// dailyRecord is one sim-day's stats line in daily.jsonl (#120). RunToDate is
// the cumulative state at the close of day Day; DayDelta is the difference from
// the prior day's close (so the series carries both the rung-point reads and a
// per-day trajectory for spike detection). The CORRECTNESS INVARIANT: the
// RunToDate of the LAST record equals the end-of-run summary for the same run
// (and day-N of a long run equals an Nd run), because RunToDate is computed from
// the same counters/percentiles/computeIntraThreadGauges the summary uses.
type dailyRecord struct {
	Day       int        `json:"day"`
	SimDate   string     `json:"sim_date"`
	RunToDate dailyStats `json:"run_to_date"`
	DayDelta  dailyDelta `json:"day_delta"`
}

// dailyStats is the cumulative run-to-date snapshot. The headline scalars are
// named; the full sim-derived intra-thread gauge set (the H2 depth buckets
// recall_intra_recall_bydepth_d<k>, the per-hop recall curve, divergence,
// flush, cosine-ops, etc.) rides in IntraGauges so the series carries the §9.2
// curve per day without a field per bucket.
type dailyStats struct {
	Turns          int64   `json:"turns"`
	PerTurnMsMean  float64 `json:"per_turn_ms_mean"`
	LatencyP50     float64 `json:"latency_p50"`
	LatencyP95     float64 `json:"latency_p95"`
	LatencyP99     float64 `json:"latency_p99"`
	ThreadsCreated int64   `json:"threads_created"`
	SpineSize      int     `json:"spine_size"`
	SleepCycles    int64   `json:"sleep_cycles"`

	RecallQueryCosineOps     float64 `json:"recall_query_cosine_ops"`
	RecallIndexFlushCalls    float64 `json:"recall_index_flush_calls"`
	RecallIntraDescentDiverg int64   `json:"recall_intra_descent_divergence"`
	RecallIntraW1StrictMiss  int64   `json:"recall_intra_w1_strict_miss"`
	RecallIntraW1Tie         int64   `json:"recall_intra_w1_tie"`
	RecallIntraW1TreeMism    int64   `json:"recall_intra_w1_tree_mismatch"`

	// IntraGauges carries the full shadow-derived intra-thread gauge set
	// (computeIntraThreadGauges) — H2 depth buckets, per-hop recall, divergence,
	// blindspot, fine-chunk counts. This is the run-to-date #109 curve.
	IntraGauges map[string]float64 `json:"intra_gauges"`
}

// dailyDelta is the per-day movement: counters diffed against the prior day's
// close, and turn-latency percentiles computed over ONLY this day's slice of
// the turn_duration_ms histogram (histogram[prevLen:currLen]).
type dailyDelta struct {
	TurnsThisDay         int64   `json:"turns_this_day"`
	PerTurnMsMeanThisDay float64 `json:"per_turn_ms_mean_this_day"`
	LatencyP50ThisDay    float64 `json:"latency_p50_this_day"`
	LatencyP95ThisDay    float64 `json:"latency_p95_this_day"`
	LatencyP99ThisDay    float64 `json:"latency_p99_this_day"`
}

// dailySnapshotWriter appends one run-to-date stats record to daily.jsonl per
// sim-day (#120). The generator is the shadow-state source for
// computeIntraThreadGauges; prevTurns / prevHistLen carry the prior close's
// cumulative turns and turn_duration_ms histogram length so the per-day delta
// is a clean diff. day is the running 1-based ordinal it stamps.
//
// Firing model — interior + final, mirroring the heavy-invariant cadence
// ("always fires once at end-of-run regardless of cadence"):
//
//   - onDayClose fires at each interior cadence tick (days 1..N-1 of an N-day
//     run): the simulated clock crosses 24h, 48h, …, well inside the run.
//   - finalize fires ONCE from runSimRung after the end-of-run summary is
//     computed (the LAST tick, N×24h, does NOT fire on its own — the run ends
//     when simNow ≥ Duration, a few jittered steps past the tick, so the
//     trailing partial day would otherwise be uncaptured). finalize emits day N
//     with the EXACT end-of-run summary values, which is what makes the LAST
//     daily record equal the end-of-run summary (the #120 correctness
//     invariant) and "day-N of a long run == an Nd run" valid.
type dailySnapshotWriter struct {
	gen         *generator
	day         int
	prevTurns   int64
	prevHistLen int
}

func newDailySnapshotWriter(gen *generator) *dailySnapshotWriter {
	return &dailySnapshotWriter{gen: gen}
}

// onDayClose is the scenarios.Scenario.OnSimDayClose handler for an INTERIOR
// sim-day tick. It computes run-to-date stats from the LIVE harness state
// (cumulative counters + percentiles over the full turn_duration_ms histogram so
// far, current spine size for liveThreads) and the generator shadow, then appends
// one record. It reads h.Metrics through the read-only accessors and writes only
// the daily file, so it NEVER mutates the final gauges — the end-of-run summary
// is unchanged. simDate is the tick instant; the harness's `day` argument is
// ignored in favor of the writer's own ordinal so interior and final records
// share one monotonic counter.
func (w *dailySnapshotWriter) onDayClose(h *scenarios.Harness, _ int, simDate time.Time) {
	durations := h.Metrics.HistogramSnapshot("turn_duration_ms")
	spineSize := 0
	if recs, err := store.ReadSpine(h.Paths.Spine); err == nil {
		spineSize = len(recs)
	}
	p50 := percentile(durations, 0.50)
	p95 := percentile(durations, 0.95)
	p99 := percentile(durations, 0.99)
	w.appendRecord(h, simDate, durations, spineSize, p50, p95, p99)
}

// finalize emits the LAST daily record (day N) from runSimRung, AFTER the
// end-of-run summary is computed, using the EXACT summary inputs — the final
// histogram, the final spine population (liveThreads), and the summary's
// p50/p95/p99 — so the record's run_to_date equals the end-of-run summary by
// construction (#120 correctness invariant). simDate is the run's final pinned
// instant. It must be called exactly once, after the last onDayClose.
func (w *dailySnapshotWriter) finalize(h *scenarios.Harness, simDate time.Time, liveThreads int, p50, p95, p99 float64) {
	durations := h.Metrics.HistogramSnapshot("turn_duration_ms")
	w.appendRecord(h, simDate, durations, liveThreads, p50, p95, p99)
}

// appendRecord builds one dailyRecord from the supplied run-to-date inputs and
// appends it to daily.jsonl. The shared body of onDayClose (interior, live
// state) and finalize (end-of-run, summary state) so the two emit byte-identical
// schema. durations is the run-to-date turn_duration_ms slice; liveThreads is the
// run-to-date spine size (the coarse-tier vector count); p50/p95/p99 are over
// durations. The per-day delta is over durations[prevHistLen:], the slice added
// since the prior record.
func (w *dailySnapshotWriter) appendRecord(h *scenarios.Harness, simDate time.Time, durations []float64, liveThreads int, p50, p95, p99 float64) {
	w.day++
	currHistLen := len(durations)
	// Guard the delta slice bounds: a record can in principle be emitted with no
	// new turns since the prior one (an empty day), so clamp prevHistLen.
	lo := w.prevHistLen
	if lo > currHistLen {
		lo = currHistLen
	}
	dayHist := durations[lo:currHistLen]

	// Run-to-date sim-derived gauges from the SAME pure function the end-of-run
	// summary uses — fed the run-to-date liveThreads + percentiles. At finalize
	// these are the exact end-of-run values (the #120 correctness invariant).
	intra := computeIntraThreadGauges(w.gen, liveThreads, p50, p95, p99)

	turns := h.Metrics.CounterValue("turns")
	rec := dailyRecord{
		Day:     w.day,
		SimDate: simDate.Format("2006-01-02"),
		RunToDate: dailyStats{
			Turns:          turns,
			PerTurnMsMean:  mean(durations),
			LatencyP50:     p50,
			LatencyP95:     p95,
			LatencyP99:     p99,
			ThreadsCreated: h.Metrics.CounterValue("threads_created"),
			SpineSize:      liveThreads,
			SleepCycles:    h.Metrics.CounterValue("sleep_cycles"),
			// recall_query_cosine_ops is the LIVE-recaller gauge, NOT a shadow
			// value (computeIntraThreadGauges does not compute it). Read it
			// DIRECTLY off the recaller's run-to-date accessors (the SAME source
			// recordCosineOpsMeasured uses for the end-of-run gauge), NOT off
			// metricRecallQueryCosineOps — that gauge is only Set at end-of-run,
			// so reading it here would leave every INTERIOR day stuck at 0 even on
			// an embedding-live run (#120 interior-record bug). The recaller's
			// CosineOps()/RecallQueries() accumulate throughout the run, so this is
			// a true run-to-date value; 0 on the mock path, where the perf bend is
			// honestly unmeasurable. At finalize this equals the summary's gauge by
			// construction (one source) — the #120 last-record==summary invariant.
			RecallQueryCosineOps:     liveCosineOpsPerQuery(h),
			RecallIndexFlushCalls:    intra[metricRecallIndexFlushCalls],
			RecallIntraDescentDiverg: h.Metrics.CounterValue("recall_intra_descent_divergence"),
			RecallIntraW1StrictMiss:  h.Metrics.CounterValue("recall_intra_w1_strict_miss"),
			RecallIntraW1Tie:         h.Metrics.CounterValue("recall_intra_w1_tie"),
			RecallIntraW1TreeMism:    h.Metrics.CounterValue("recall_intra_w1_tree_mismatch"),
			IntraGauges:              intra,
		},
		DayDelta: dailyDelta{
			TurnsThisDay:         turns - w.prevTurns,
			PerTurnMsMeanThisDay: mean(dayHist),
			LatencyP50ThisDay:    percentile(dayHist, 0.50),
			LatencyP95ThisDay:    percentile(dayHist, 0.95),
			LatencyP99ThisDay:    percentile(dayHist, 0.99),
		},
	}

	line, err := json.Marshal(rec)
	if err != nil {
		h.T.Errorf("daily snapshot day %d: marshal: %v", w.day, err)
		return
	}
	path := filepath.Join(h.RunHome, dailyFilename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		h.T.Errorf("daily snapshot day %d: open %s: %v", w.day, path, err)
		return
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		h.T.Errorf("daily snapshot day %d: write: %v", w.day, err)
	}
	if err := f.Close(); err != nil {
		h.T.Errorf("daily snapshot day %d: close: %v", w.day, err)
	}

	w.prevTurns = turns
	w.prevHistLen = currHistLen
}

// cosineOpsReporter is the recaller surface the MEASURED recall_query_cosine_ops
// metric reads (#111 / design §7.2). measure.Service satisfies it; the
// symbolic-only default does not (no embedding recall ran), so the metric is 0
// on the mock acceptance path — honestly, the perf bend is measurable only on
// an embedding-live run. Mirrors the harness's optional-interface discipline.
type cosineOpsReporter interface {
	CosineOps() int64
	RecallQueries() int64
}

// liveCosineOpsPerQuery reads the LIVE recaller's run-to-date MEASURED per-query
// cosine-op count (§7.2): the recaller's run-total cosine comparisons divided by
// the queries that performed them. The recaller's CosineOps()/RecallQueries()
// accumulate throughout the run (atomic, read concurrent-safe), so this is a
// valid run-to-date read at ANY day-close — not just end-of-run. Returns 0 when
// no embedding recaller is installed or no embedding query ran (the symbolic mock
// path), where the perf bend is honestly unmeasurable. Single source of truth for
// both the end-of-run summary (recordCosineOpsMeasured) and the per-day snapshot
// (appendRecord), so the LAST daily record equals the summary by construction.
func liveCosineOpsPerQuery(h *scenarios.Harness) float64 {
	if h.State != nil && h.State.Recaller != nil {
		if rep, ok := h.State.Recaller.(cosineOpsReporter); ok {
			if q := rep.RecallQueries(); q > 0 {
				return float64(rep.CosineOps()) / float64(q)
			}
		}
	}
	return 0
}

// recordCosineOpsMeasured emits the MEASURED per-query cosine-op count (§7.2)
// into the end-of-run gauge. Reads the live recaller off the harness's State via
// liveCosineOpsPerQuery; emits 0 when no embedding recaller is installed or no
// embedding query ran (the symbolic mock path). Counted at the scoring call sites
// (scoring.CosineCounter), so the O(C_main)→O(log n) bend is empirical, not
// modeled.
func recordCosineOpsMeasured(h *scenarios.Harness) {
	h.Metrics.Set(metricRecallQueryCosineOps, liveCosineOpsPerQuery(h))
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

// TestRecallExpectedMaterializationFilter pins the #100 oracle fix: the
// execution-time refinement oracle must exclude candidate threads the
// runtime has not yet materialized on the spine — those whose creating
// step has not executed (createdAtStep >= materializedBefore). The
// canonical (generation-time) path passes noMaterializationFilter and must
// be unaffected: it names every clearing sibling regardless of
// createdAtStep, because at generation time g.threads only holds threads
// generated (= executed) earlier.
//
// Setup: four sibling threads bound to one slot (so each clears the
// Jaccard threshold against the slot's query), none in Layer B, none a
// carrier. The engaged thread is order 0. The other three sit at
// ascending createdAtStep. With a materialization cutoff between them, the
// filter must drop exactly the candidates created at-or-after the cutoff.
func TestRecallExpectedMaterializationFilter(t *testing.T) {
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	slot := CorpusSlot{Topic: "physics", Tags: tags}

	g := &generator{
		model:       corpusModel{slots: []CorpusSlot{slot}, familySize: 4},
		emittedSyms: map[int]map[string]struct{}{},
		carrierIdx:  map[int]struct{}{},
		carrier:     -1,
		layerB:      nil, // every thread dormant, so each is a candidate
	}
	// Four siblings on the single slot, all dormant. createdAtStep ascends:
	// the engaged thread (0) is created first, then candidates at 10/20/30.
	created := []int{0, 10, 20, 30}
	for order, cas := range created {
		thr := newThread(order, 0)
		thr.createdAtStep = cas
		g.threads = append(g.threads, thr)
		// Each emits the slot's tags + its own salt — the sibling retained
		// set that clears the threshold against Q below.
		g.recordEmission(order, append(append([]string(nil), tags...), saltSymbol(order)), convoTransientPct)
	}

	// Q is the slot's tags plus the engaged thread's salt (mirrors the
	// runtime's coalesced query; see buildStep). The engaged thread (0) is
	// excluded as the candidate's own turn; threads 1,2,3 are siblings that
	// clear the threshold.
	Q := append(append([]string(nil), tags...), saltSymbol(0))

	// Canonical (sentinel) path: every clearing sibling is named, regardless
	// of createdAtStep — generation-time behavior is unchanged.
	if got, want := g.recallExpectedFor(0, Q, false), []string{"thr_2", "thr_3", "thr_4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sentinel (canonical) oracle = %v, want %v (all siblings, no materialization filter)", got, want)
	}

	// Materialization cutoff = 21: threads created at 10 and 20 are
	// materialized (createdAtStep < 21); the one at 30 is not yet executed
	// and must be excluded.
	if got, want := g.recallExpectedForMaterialized(0, Q, false, 21), []string{"thr_2", "thr_3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("filtered oracle (cutoff 21) = %v, want %v (thr_4 created at 30 is unmaterialized)", got, want)
	}

	// Cutoff = 11: only the thread created at 10 is materialized.
	if got, want := g.recallExpectedForMaterialized(0, Q, false, 11), []string{"thr_2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("filtered oracle (cutoff 11) = %v, want %v", got, want)
	}

	// Cutoff = 0: nothing materialized yet — empty expected set, never a
	// false miss.
	if got := g.recallExpectedForMaterialized(0, Q, false, 0); len(got) != 0 {
		t.Errorf("filtered oracle (cutoff 0) = %v, want empty (no thread materialized)", got)
	}

	// The sentinel and a cutoff past every createdAtStep must agree — the
	// filter is a no-op once all candidates are materialized.
	if got, want := g.recallExpectedForMaterialized(0, Q, false, 1000), g.recallExpectedFor(0, Q, false); !reflect.DeepEqual(got, want) {
		t.Errorf("filter past all createdAtStep = %v, want sentinel result %v (must be a no-op)", got, want)
	}
}

// TestDailyStatsSeries pins the #120 per-sim-day stats series. A short
// multi-day MOCK sim drives the day-close hook; the test then asserts the four
// load-bearing properties of daily.jsonl:
//
//	(a) exactly one record per sim-day, in monotonic day order;
//	(b) the run-to-date cumulative counters are non-decreasing across days;
//	(c) the LAST record's run_to_date equals the end-of-run summary for the
//	    SAME run — the correctness invariant that lets the acceptance-ladder
//	    rung points be READ from the series instead of re-simulated; and
//	(d) day 1's day_delta equals its run_to_date (the first day's movement IS
//	    the whole run so far).
//
// It uses the nil-recaller mock path (the daily plumbing is embedder-
// independent) and a 2-day span — the minimum that yields an interior tick
// (day 1) plus the end-of-run finalize record (day 2).
func TestDailyStatsSeries(t *testing.T) {
	corpus := loadCorpusSlots(t)
	h := runSimRung(t, "daily-series-2d", 2*24*time.Hour, corpus, nil, false, nil, "")

	recs := readDailyRecords(t, h.RunHome)
	if len(recs) < 2 {
		t.Fatalf("expected >=2 daily records (a multi-day run), got %d", len(recs))
	}

	// (a) one record per sim-day, in order: day field is 1,2,3,…
	for i, r := range recs {
		if r.Day != i+1 {
			t.Errorf("record %d has day=%d, want %d (one-per-day, in order)", i, r.Day, i+1)
		}
		if r.SimDate == "" {
			t.Errorf("record day %d has empty sim_date", r.Day)
		}
	}

	// (b) run-to-date cumulative counters are non-decreasing across days.
	for i := 1; i < len(recs); i++ {
		prev, cur := recs[i-1].RunToDate, recs[i].RunToDate
		if cur.Turns < prev.Turns {
			t.Errorf("day %d turns %d < day %d turns %d (must be non-decreasing)", recs[i].Day, cur.Turns, recs[i-1].Day, prev.Turns)
		}
		if cur.ThreadsCreated < prev.ThreadsCreated {
			t.Errorf("day %d threads_created %d < prior %d", recs[i].Day, cur.ThreadsCreated, prev.ThreadsCreated)
		}
		if cur.SleepCycles < prev.SleepCycles {
			t.Errorf("day %d sleep_cycles %d < prior %d", recs[i].Day, cur.SleepCycles, prev.SleepCycles)
		}
		if cur.RecallIntraDescentDiverg < prev.RecallIntraDescentDiverg {
			t.Errorf("day %d descent_divergence %d < prior %d", recs[i].Day, cur.RecallIntraDescentDiverg, prev.RecallIntraDescentDiverg)
		}
	}

	// (c) the LAST record's run_to_date == the end-of-run summary for this run.
	// The summary is the metrics blob runSimRung wrote (counters) plus the
	// percentiles/gauges it computed; the daily finalize used those exact inputs,
	// so the two must agree field-for-field.
	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}
	last := recs[len(recs)-1].RunToDate
	durations := m.Histograms["turn_duration_ms"]
	if want := m.Counters["turns"]; last.Turns != want {
		t.Errorf("last record turns=%d, end-of-run summary turns=%d", last.Turns, want)
	}
	if want := m.Counters["threads_created"]; last.ThreadsCreated != want {
		t.Errorf("last record threads_created=%d, summary=%d", last.ThreadsCreated, want)
	}
	if want := m.Counters["sleep_cycles"]; last.SleepCycles != want {
		t.Errorf("last record sleep_cycles=%d, summary=%d", last.SleepCycles, want)
	}
	assertFloatEq(t, "latency_p50", last.LatencyP50, percentile(durations, 0.50))
	assertFloatEq(t, "latency_p95", last.LatencyP95, percentile(durations, 0.95))
	assertFloatEq(t, "latency_p99", last.LatencyP99, percentile(durations, 0.99))
	assertFloatEq(t, "per_turn_ms_mean", last.PerTurnMsMean, mean(durations))
	// spine_size == the summary's final coarse-tier gauge (= final spine size).
	if want := int(m.Gauges[metricRecallIndexCoarseSize]); last.SpineSize != want {
		t.Errorf("last record spine_size=%d, summary coarse_size=%d", last.SpineSize, want)
	}
	// recall_query_cosine_ops: the LIVE-recaller per-query gauge. The daily
	// finalize reads it from the SAME live source recordCosineOpsMeasured wrote
	// into metricRecallQueryCosineOps, so the last record must equal the summary
	// gauge field-for-field (0 on this mock path — no embedder — but the
	// invariant is pinned regardless). This is the #120 interior-record fix:
	// interior days now read the live accumulator too, not a stuck end-of-run 0.
	assertFloatEq(t, "recall_query_cosine_ops", last.RecallQueryCosineOps, m.Gauges[metricRecallQueryCosineOps])
	// W1 + descent counters.
	if want := m.Counters["recall_intra_descent_divergence"]; last.RecallIntraDescentDiverg != want {
		t.Errorf("last record descent_divergence=%d, summary=%d", last.RecallIntraDescentDiverg, want)
	}
	// Every sim-derived intra gauge in the last record's intra_gauges map must
	// equal the corresponding end-of-run gauge in the blob (the H2 depth buckets,
	// per-hop recall curve, divergence, flush, fine-chunk counts).
	if len(last.IntraGauges) == 0 {
		t.Error("last record intra_gauges is empty; expected the §9.2 sim-derived gauge set")
	}
	for k, v := range last.IntraGauges {
		assertFloatEq(t, "intra_gauge["+k+"]", v, m.Gauges[k])
	}

	// (d) day 1's day_delta == its run_to_date (first day's movement is the run
	// so far).
	d1 := recs[0]
	if d1.DayDelta.TurnsThisDay != d1.RunToDate.Turns {
		t.Errorf("day 1 delta turns=%d != run_to_date turns=%d", d1.DayDelta.TurnsThisDay, d1.RunToDate.Turns)
	}
	assertFloatEq(t, "day1 delta p50", d1.DayDelta.LatencyP50ThisDay, d1.RunToDate.LatencyP50)
	assertFloatEq(t, "day1 delta p99", d1.DayDelta.LatencyP99ThisDay, d1.RunToDate.LatencyP99)
	assertFloatEq(t, "day1 delta mean", d1.DayDelta.PerTurnMsMeanThisDay, d1.RunToDate.PerTurnMsMean)
}

// fakeCosineRecaller is a minimal measure.Recaller that also satisfies
// cosineOpsReporter, returning canned run-to-date CosineOps/RecallQueries
// accumulator values. It exists only to drive liveCosineOpsPerQuery with a
// NON-ZERO accumulator — the mock sim path leaves the real recaller at 0/0
// (no embedder), so it cannot prove the daily field tracks a live, non-zero
// accumulator rather than a stuck end-of-run gauge.
type fakeCosineRecaller struct {
	ops, queries int64
}

func (f *fakeCosineRecaller) Prepare(context.Context) error { return nil }
func (f *fakeCosineRecaller) Recall(context.Context, measure.Request) ([]measure.Result, error) {
	return nil, nil
}
func (f *fakeCosineRecaller) Close() error         { return nil }
func (f *fakeCosineRecaller) CosineOps() int64     { return f.ops }
func (f *fakeCosineRecaller) RecallQueries() int64 { return f.queries }

// TestLiveCosineOpsPerQuery pins the #120 interior-record fix: the per-day
// snapshot reads recall_query_cosine_ops from the LIVE recaller's accumulating
// accessors (CosineOps()/RecallQueries()), NOT from the metricRecallQueryCosineOps
// gauge that is only Set at end-of-run. Reading the gauge would leave every
// INTERIOR daily record stuck at 0 even on an embedding-live run; reading the
// live accumulator gives a true run-to-date per-query average at any day-close.
//
// The mock sim path has a nil-embedder recaller (0 ops / 0 queries), so it would
// always read 0 — that is correct, not a bug, but it cannot prove the WIRING.
// This test installs a recaller with a NON-ZERO accumulator and asserts the live
// read returns the per-query average, so a non-zero accumulator → non-zero daily
// field. liveCosineOpsPerQuery is the single source both the daily snapshot and
// the end-of-run gauge read, so proving it here proves the interior records track
// the accumulator.
func TestLiveCosineOpsPerQuery(t *testing.T) {
	// No recaller installed → 0 (the honest "unmeasurable" value, not a panic).
	if got := liveCosineOpsPerQuery(&scenarios.Harness{}); got != 0 {
		t.Errorf("nil State: liveCosineOpsPerQuery = %.3f, want 0", got)
	}
	if got := liveCosineOpsPerQuery(&scenarios.Harness{State: &turn.State{}}); got != 0 {
		t.Errorf("nil Recaller: liveCosineOpsPerQuery = %.3f, want 0", got)
	}

	// Zero queries → 0 (no division by zero; honestly unmeasurable).
	zeroQ := &scenarios.Harness{State: &turn.State{Recaller: &fakeCosineRecaller{ops: 500, queries: 0}}}
	if got := liveCosineOpsPerQuery(zeroQ); got != 0 {
		t.Errorf("zero queries: liveCosineOpsPerQuery = %.3f, want 0", got)
	}

	// Non-zero accumulator → the per-query average. This is the load-bearing
	// case: an interior day-close reading a live, non-zero accumulator yields a
	// non-zero daily field — exactly what the stuck end-of-run gauge could not.
	h := &scenarios.Harness{State: &turn.State{Recaller: &fakeCosineRecaller{ops: 600, queries: 8}}}
	if got, want := liveCosineOpsPerQuery(h), 75.0; got != want {
		t.Errorf("liveCosineOpsPerQuery = %.3f, want %.3f (600 ops / 8 queries)", got, want)
	}
}

// readDailyRecords reads daily.jsonl from a scenario's rundata directory and
// returns its records in file order. A missing file is a hard failure — the
// #120 series must exist after a multi-day sim.
func readDailyRecords(t *testing.T, runHome string) []dailyRecord {
	t.Helper()
	path := filepath.Join(runHome, dailyFilename)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var recs []dailyRecord
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var r dailyRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parse daily record %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

// assertFloatEq fails the test if got and want differ by more than a tiny
// epsilon. The daily and summary values are computed by the SAME helpers over
// the SAME inputs, so they should be bit-identical; the epsilon only guards
// against an incidental float reassociation.
func assertFloatEq(t *testing.T, label string, got, want float64) {
	t.Helper()
	if absDiff(got, want) > 1e-9 {
		t.Errorf("%s: daily=%.6f end-of-run=%.6f (must match — #120 correctness invariant)", label, got, want)
	}
}
