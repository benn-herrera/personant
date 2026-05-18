// Package sim is a deterministic, seeded synthetic-workload generator
// for the Personant scenario harness (spec §9.1, §9.10).
//
// GenerateWorkload turns a WorkloadConfig into a scenarios.Scenario: a
// stream of synthetic turns modelling a user moving among a handful of
// topical threads across a day. The same config produces a
// byte-identical Scenario — all randomness flows from one math/rand
// source seeded by WorkloadConfig.Seed.
//
// Topic model — the recall_madlibs corpus. Each generated thread is
// bound to one distinguishable corpus query slot drawn from the
// Wikipedia-derived recall_madlibs corpus (Phase C). A slot carries a
// fixed set of mad-libs query tags and the matching natural-language
// query string. A thread's anchors ARE its slot's tags, so a
// recall-opportunity turn — which re-issues that slot's query — fires
// the §3.4 symbolic Jaccard layer for exactly the threads bound to
// that slot. With 1520 corpus slots the recall oracle resolves to a
// small, well-defined expected-match set instead of dozens of
// indistinguishable same-symbol threads. See the corpusModel doc and
// WorkloadConfig.Corpus for the binding mechanics.
//
// This package feeds the rung walk toward the six-month acceptance
// simulation. TestSim drives the generated Scenario through
// scenarios.RunScenario unchanged; no new runner is needed. Rate parameters here are seed values meant to be tuned as
// the rung walk climbs — Duration is the only knob that must change to
// lengthen a run.
//
// Dormant-thread resumption is exercised. Alongside `continue`
// (re-engage the active thread), `switch` (re-engage a warm,
// non-active Layer-B thread) and `new` (spawn a fresh thread), the
// generator schedules a `resume` action: it re-engages a thread that
// has fallen out of Layer B — a dormant thread still on the spine.
// Naming a dormant thr_N in the topic tag triggers the §5.5 mid-turn
// fetch (the runtime aborts the in-flight stream, fetches the thread,
// re-prompts), the realistic "pick up earlier work" pattern.
//
// Resume only targets a *recently* dormant thread (resumeWindowTurns):
// archival deletes the coldest retired threads and the generator
// cannot perfectly predict runtime archival, but a recently-dormant
// thread is very unlikely to have been archived yet. An occasional
// resume that hits an already-archived thread is harmless — the §5.5
// fetch logs thread.fetch-miss and the turn proceeds.
//
// The mock serves responses by scenario step, not per consult (see
// model.MockClient): the harness selects the step's one response, and
// every consult during the step — including the §5.5 re-prompt —
// re-serves it. So a turn that triggers a mid-turn fetch needs no
// fetch prediction from the generator and no second queued response.
package sim

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
	"time"

	"personant/internal/scenarios"
	"personant/internal/turn"
)

// CorpusSlot is one distinguishable recall_madlibs query slot a thread
// can be bound to. Tags is the slot's fixed mad-libs symbol set (5
// synonyms, one drawn from each of the topic's columns); a thread bound
// to this slot is anchored on exactly these tags. UserInput is the
// natural-language mad-libs query that mentions those tags as
// #-prefixed symbols — re-issuing it fires the §3.4 recall layer for
// every thread bound to this slot. Topic is the slot's wiki-* topic
// name, retained for annotations.
//
// The slots are the corpus_queries.json `queries` array verbatim: 152
// Wikipedia topics × 10 queries = 1520 slots. Loading is the test's
// job (it owns the testdata path); GenerateWorkload takes the slice as
// part of its config so it stays a pure function.
type CorpusSlot struct {
	Topic     string
	Tags      []string
	UserInput string
}

// WorkloadConfig parameterises a generated workload. Seed, Duration,
// and Corpus are the load-bearing knobs; the rate fields have sensible
// defaults applied by withDefaults so a zero-value-but-for-those
// config is valid.
type WorkloadConfig struct {
	// Seed seeds the single rand source. Fixed seed → identical
	// Scenario.
	Seed int64

	// Duration is the total simulated wall span the workload covers —
	// the simulated clock advances by this much across the whole run,
	// counting inter-turn gaps, the inter-session gap, the overnight
	// gap, and the weekly day-off gap alike. The generator emits whole
	// calendar days until the simulated clock has advanced by Duration.
	// A 1-day run passes 24*time.Hour; a 1-week run passes 7*24*time.Hour.
	Duration time.Duration

	// Corpus is the recall_madlibs query-slot pool a thread is bound
	// to. The caller (the test) loads it from corpus_queries.json;
	// passing it through the config keeps GenerateWorkload pure — same
	// config (Corpus included) → byte-identical Scenario. A nil Corpus
	// is a caller error surfaced as an empty Scenario.
	Corpus []CorpusSlot

	// RapidGap and WorkGap are the mean inter-turn gaps for the two
	// turn classes. Each sampled gap carries modest multiplicative
	// jitter (see sampleGap).
	RapidGap time.Duration
	WorkGap  time.Duration

	// InterSessionGap is the larger gap inserted once per work day,
	// between that day's two sessions.
	InterSessionGap time.Duration

	// OvernightGap is the idle span from the end of one work day to the
	// start of the next (~12 h, jittered at use).
	OvernightGap time.Duration

	// DayOffGap is the idle span a weekly day off advances the clock by
	// (~24–36 h). A day off emits no turns; the long gap is deliberate —
	// it exercises the §3.5 wall-clock decay path.
	DayOffGap time.Duration

	// RapidWeight / WorkWeight bias turn-type selection by count. The
	// default 6:1 yields a roughly 50/50 split by *simulated time*,
	// since a work turn consumes ~6× the gap of a rapid turn.
	RapidWeight int
	WorkWeight  int

	// ContinueWeight / SwitchWeight / ResumeWeight / NewWeight bias the
	// per-turn action selection. They need not sum to anything in
	// particular. ResumeWeight drives dormant-thread resumption — a
	// realistic user resumes earlier work far more often than starting
	// fresh, so the seed weights make NewWeight a minority. These are
	// seed values, to be tuned on the rung walk.
	ContinueWeight int
	SwitchWeight   int
	ResumeWeight   int
	NewWeight      int

	// ResumeWindowTurns bounds how far back a `resume` action may reach:
	// only a thread that went dormant within the last ResumeWindowTurns
	// turns is a resume candidate. A recently-dormant thread is very
	// unlikely to have been archived (archival hits the coldest), so the
	// window keeps resume-onto-archived rare. 0 → default.
	ResumeWindowTurns int

	// FamilySize is the number of threads bound to the same corpus
	// slot — a small bounded "family" sharing one topic. A family of 2
	// means each recall opportunity has exactly one well-defined
	// expected match (the engaged thread's dormant sibling). See
	// corpusModel for why a family rather than a unique slot per
	// thread: a unique slot would mean a recall opportunity never has
	// any dormant same-slot thread to surface, so recall would never be
	// exercised. 0 → default of 2.
	FamilySize int
}

// withDefaults returns a copy of cfg with any zero rate field replaced
// by its default. Seed, Duration, and Corpus are left untouched.
func (cfg WorkloadConfig) withDefaults() WorkloadConfig {
	if cfg.RapidGap == 0 {
		cfg.RapidGap = 1 * time.Minute
	}
	if cfg.WorkGap == 0 {
		cfg.WorkGap = 6 * time.Minute
	}
	if cfg.InterSessionGap == 0 {
		// ~3.5 h between the day's two sessions (jittered at use).
		cfg.InterSessionGap = 3*time.Hour + 30*time.Minute
	}
	if cfg.OvernightGap == 0 {
		// ~12 h from one work day's end to the next day's start.
		cfg.OvernightGap = 12 * time.Hour
	}
	if cfg.DayOffGap == 0 {
		// ~30 h idle for a weekly day off (jittered at use).
		cfg.DayOffGap = 30 * time.Hour
	}
	if cfg.RapidWeight == 0 {
		cfg.RapidWeight = 6
	}
	if cfg.WorkWeight == 0 {
		cfg.WorkWeight = 1
	}
	if cfg.ContinueWeight == 0 {
		cfg.ContinueWeight = 50
	}
	if cfg.SwitchWeight == 0 {
		cfg.SwitchWeight = 20
	}
	if cfg.ResumeWeight == 0 {
		cfg.ResumeWeight = 25
	}
	if cfg.NewWeight == 0 {
		cfg.NewWeight = 5
	}
	if cfg.ResumeWindowTurns == 0 {
		cfg.ResumeWindowTurns = 40
	}
	if cfg.FamilySize == 0 {
		cfg.FamilySize = 2
	}
	return cfg
}

// corpusModel is the deterministic thread→corpus-slot binding.
//
// Threads are created in a fixed order (creation order N maps to the
// runtime's thr_{N+1}); the binding is purely a function of that order
// and the FamilySize, so the same config yields the same Scenario.
//
// Binding rule: thread k is bound to slot (k / familySize) mod
// len(slots). familySize consecutive threads thus share one slot — a
// "family". The load-bearing property: a recall-opportunity turn
// re-issues the engaged thread's slot's mad-libs query, whose symbol
// set is exactly that slot's tags; every thread bound to that slot is
// anchored on exactly those tags, so the §3.4 Jaccard layer (threshold
// 0.4) fires for the whole family and nothing else. With familySize=2
// the expected-match set of a recall opportunity is exactly the one
// dormant sibling — a well-defined, small set that does not blur as the
// thread population scales into the thousands.
//
// Why a family of 2 rather than one unique slot per thread: a unique
// slot would mean no two threads ever share a slot, so a recall
// opportunity would never have a dormant same-slot thread to surface
// and recall would never be exercised at all. A bounded family of 2 is
// the minimum that gives every recall opportunity exactly one
// expected match.
type corpusModel struct {
	slots      []CorpusSlot
	familySize int
}

// slotFor returns the corpus slot index a thread of creation order
// `order` is bound to.
func (m corpusModel) slotFor(order int) int {
	return (order / m.familySize) % len(m.slots)
}

// thread is the generator's in-memory model of one simulated thread:
// its creation order and its bound corpus slot. The generator does not
// know the runtime's thr_N ids at construction time; threadID() maps
// creation order to the id the runtime is guaranteed to assign.
//
// Per-thread lifecycle intent (decay/closure) is deliberately not
// modelled here — the runtime owns that, and the generator drives it
// purely by emitting idle turns and advancing the clock. The generator
// tracks two pieces of thread state: Layer-B membership
// (generator.layerB) and the last-engaged turn index
// (generator.lastEngagedTurn), the latter so a `resume` action can
// pick a recently-dormant thread.
type thread struct {
	order   int // 0-based creation order
	slotIdx int // index into the corpus slot pool
}

// threadID returns the thr_N id the runtime assigns to a thread by
// creation order. Threads are numbered thr_1, thr_2, … in the order
// they are created.
//
// Precondition: this mapping holds because the runtime assigns
// thr_{max+1} for every new thread and never reuses an id. Archival
// may DELETE retired spine records, leaving gaps in the live id set —
// that is fine: creation order N still maps to thr_{N+1}, the
// generator just may name a thr_N whose record has since been
// archived. A `resume` action names a recently-dormant thread; the
// resume window keeps it very unlikely to have been archived (archival
// hits the coldest threads). If a resume does name an archived id, the
// §5.5 fetch logs thread.fetch-miss and the turn proceeds — harmless.
func (t thread) threadID() string {
	return fmt.Sprintf("thr_%d", t.order+1)
}

// fileState is the §3.9 per-thread tracked-file model the generator
// carries to drive a realistic read/modify/write cycle on `work` turns.
// path is derived deterministically from the thread's bound corpus topic;
// content starts from a deterministic topic-keyed baseline and grows by
// one line per write. writeCount counts writes applied so far — it both
// keys the per-write content mutation (so successive writes produce real
// diffs) and triggers a periodic fs.commit (every commitEvery writes).
type fileState struct {
	path       string
	content    string
	writeCount int
}

// commitEvery is N for the "every Nth write emits an fs.commit" rule. A
// commit exercises the §3.9 git-commit pointer that checkpoint 5d's
// clock-aging consumes.
const commitEvery = 5

// workFile returns the §3.9 file state for thread idx, creating it on
// first use. The path is <topic>.go (topic slug sanitized to a filename);
// the baseline content is a deterministic topic-keyed package stub. The
// binding is purely a function of the thread's bound slot, so the same
// config yields byte-identical file content.
func (g *generator) workFile(idx int) *fileState {
	if fs, ok := g.files[idx]; ok {
		return fs
	}
	thr := g.threads[idx]
	slot := g.model.slots[thr.slotIdx]
	name := fileSlug(slot.Topic)
	fs := &fileState{
		path: name + ".go",
		content: fmt.Sprintf("// %s.go — %s\npackage %s\n",
			name, slot.Topic, name),
	}
	g.files[idx] = fs
	return fs
}

// fileSlug turns a corpus topic name into a filename-safe lowercase slug:
// non-alphanumeric runs collapse to a single underscore. Deterministic.
func fileSlug(topic string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(topic) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		return "topic"
	}
	return s
}

// turnType is the rapid/work classification driving gap length and
// whether a PreEvents file-edit cycle is attached.
type turnType int

const (
	turnRapid turnType = iota
	turnWork
)

// action is the per-turn thread-population move.
type action int

const (
	// actContinue re-engages the active thread (Layer B head).
	actContinue action = iota
	// actSwitch re-engages a warm, non-active Layer-B thread.
	actSwitch
	// actResume re-engages a recently-dormant thread — one that has
	// fallen out of Layer B but is still on the spine. Naming it in the
	// topic tag triggers the §5.5 mid-turn fetch.
	actResume
	// actNew spawns a brand-new thread.
	actNew
)

// sessionActive is the turn-active simulated span of one session — the
// total of its inter-turn gaps. Two of these plus the inter-session and
// overnight gaps make up a work day.
const sessionActive = 6 * time.Hour

// GenerateWorkload is a pure function: same WorkloadConfig → identical
// scenarios.Scenario. It models the simulated user as a thread
// population evolving across calendar days, emitting one scenarios.Step
// per turn.
//
// The day is the repeating primitive. The generator emits calendar
// days until the simulated clock has advanced by cfg.Duration. A week
// is 6 work days plus 1 day off: a work day is two ~6 h sessions split
// by an inter-session gap, followed by an overnight gap; a day off
// emits no turns and simply advances the clock by DayOffGap, deliberately
// exercising the §3.5 wall-clock decay path.
//
// A nil/empty Corpus yields an empty Scenario — the caller must supply
// the recall_madlibs slot pool.
func GenerateWorkload(cfg WorkloadConfig) scenarios.Scenario {
	cfg = cfg.withDefaults()
	rng := rand.New(rand.NewSource(cfg.Seed))

	g := &generator{
		cfg: cfg,
		rng: rng,
		model: corpusModel{
			slots:      cfg.Corpus,
			familySize: cfg.FamilySize,
		},
		files: map[int]*fileState{},
	}

	// An empty corpus has no slot to bind threads to — emit nothing.
	if len(cfg.Corpus) > 0 {
		// Emit calendar days until the simulated clock has advanced by
		// Duration. dayIndex 0-based; every 7th day (index 6, 13, …) is
		// the week's day off.
		for dayIndex := 0; g.simNow < cfg.Duration; dayIndex++ {
			if dayIndex%7 == 6 {
				g.runDayOff()
			} else {
				g.runWorkDay()
			}
		}
	}

	g.markRestartSteps()

	return scenarios.Scenario{
		Name:  fmt.Sprintf("sim-workload-seed%d-dur%s", cfg.Seed, cfg.Duration),
		Steps: g.steps,
	}
}

// markRestartSteps flags two steps with RestartSession so the workload
// exercises a clean shutdown→relaunch at minimum and maximum history
// load. The first mark is the start of the SECOND session (a restart
// after the first session has completed — near the run start, minimum
// persisted history); the second mark is the start of the FINAL session
// (near the run end, maximum history).
//
// Constraints honored: never the very first step of the run (step 0 — a
// restart before any turn has run is meaningless, the working set would
// be trivially empty on both sides), and never the same step twice (a
// very short run whose two boundaries collide marks at most one step).
func (g *generator) markRestartSteps() {
	mark := map[int]bool{}
	// Near run start: the start of the second session. sessionStarts[0]
	// is step 0 (skipped by the never-step-0 rule); sessionStarts[1] is
	// the first post-first-session boundary.
	if len(g.sessionStarts) >= 2 && g.sessionStarts[1] != 0 {
		mark[g.sessionStarts[1]] = true
	}
	// Near run end: the start of the final session.
	if len(g.sessionStarts) >= 1 {
		last := g.sessionStarts[len(g.sessionStarts)-1]
		if last != 0 {
			mark[last] = true
		}
	}
	for idx := range mark {
		g.steps[idx].RestartSession = true
	}
}

// runWorkDay emits one work day: two ~6 h sessions separated by an
// inter-session gap, then an overnight gap to the next day. The
// inter-session and overnight gaps are folded into the cumulative
// simulated clock (via pendingGap) so they count toward the Duration
// budget — Duration spans real idle time, not just turn-active time.
func (g *generator) runWorkDay() {
	g.runSession(sessionActive)

	// Inter-session gap. This overwrites pendingGap, intentionally
	// discarding the trailing intra-session gap session 1's last turn
	// sampled: the next step is the first of session 2, and the gap
	// before it is the inter-session gap, not a normal inter-turn gap.
	// runSession already advanced simNow by that discarded gap, so the
	// clock subtracts it back here before adding the inter-session gap.
	g.simNow -= g.pendingGap
	isg := jitter(g.rng, g.cfg.InterSessionGap)
	g.simNow += isg
	g.pendingGap = isg

	g.runSession(sessionActive)

	// Overnight gap to the next day. Same discard-and-replace as the
	// inter-session gap: drop session 2's trailing intra-session gap,
	// substitute the overnight gap. The overnight gap is carried as the
	// next day's first step's TimeDelta and counts toward Duration.
	g.simNow -= g.pendingGap
	overnight := jitter(g.rng, g.cfg.OvernightGap)
	g.simNow += overnight
	g.pendingGap = overnight
}

// runDayOff emits no turns; it advances the simulated clock by a
// jittered ~24–36 h day-off gap, carried as the next day's first step's
// TimeDelta. The long recurring gap is deliberate — it crosses the §3.5
// wall-clock decay threshold so the longer rungs exercise decay/closure.
func (g *generator) runDayOff() {
	g.simNow -= g.pendingGap // discard any pending intra-day gap
	off := jitter(g.rng, g.cfg.DayOffGap)
	g.simNow += off
	g.pendingGap = off
}

// layerBCap mirrors workset.DefaultBTopK (spec §2.6.1 layer.b-top-k):
// the runtime keeps the 3 most-recently-engaged threads in Layer B,
// fully present in the working set. A topic tag naming a thr_N NOT in
// Layer B triggers the §5.5 mid-turn fetch + re-prompt. The generator
// models Layer B so it can distinguish a `switch` (warm, in-Layer-B,
// no fetch) from a `resume` (dormant, out of Layer B, triggers the
// fetch) — and so the recall oracle can exclude resident threads.
const layerBCap = 3

// generator carries the mutable state threaded through workload
// construction: the rng, the corpus binding model, the live thread
// model (Layer-B LRU + per-thread last-engaged turn), the accumulating
// step list, and the simulated clock.
type generator struct {
	cfg   WorkloadConfig
	rng   *rand.Rand
	model corpusModel

	threads []thread // by creation order

	// layerB is the generator's model of the runtime's Layer B: thread
	// indices, most-recently-engaged first, capped at layerBCap. The
	// active thread is layerB[0]. A `continue` re-engages layerB[0]; a
	// `switch` targets one of layerB[1:] (warm but not active).
	layerB []int

	// lastEngagedTurn[idx] is the 0-based step index at which thread idx
	// was last engaged. A thread is "recently dormant" — a resume
	// candidate — when it is no longer in Layer B but was engaged within
	// the last ResumeWindowTurns turns.
	lastEngagedTurn []int

	// files[idx] is the §3.9 tracked-file state for thread idx: the
	// deterministic file path and the file's current content, which grows
	// by one line on every `work`-turn write. A thread gets a fileState
	// the first time a work turn engages it (see workFile).
	files map[int]*fileState

	steps      []scenarios.Step
	simNow     time.Duration
	pendingGap time.Duration // gap to apply as the next step's TimeDelta

	// sessionStarts records the 0-based step index that begins each
	// session (each runSession call that emitted at least one turn). The
	// shutdown/restart marking (markRestartSteps) uses this to locate two
	// distinct session boundaries — one near the run start (minimum
	// history) and one near the run end (maximum history).
	sessionStarts []int
}

// engage records thread index idx as the most-recently-engaged thread,
// updating the Layer-B LRU exactly as the runtime's updateLayerLRU
// does: move-to-front if present, else prepend; evict the tail past
// layerBCap. turn is the 0-based step index of the engaging turn.
func (g *generator) engage(idx, turn int) {
	for i, id := range g.layerB {
		if id == idx {
			g.layerB = append(g.layerB[:i], g.layerB[i+1:]...)
			break
		}
	}
	g.layerB = append([]int{idx}, g.layerB...)
	if len(g.layerB) > layerBCap {
		g.layerB = g.layerB[:layerBCap]
	}
	for idx >= len(g.lastEngagedTurn) {
		g.lastEngagedTurn = append(g.lastEngagedTurn, 0)
	}
	g.lastEngagedTurn[idx] = turn
}

// resumeCandidates returns the thread indices eligible for a `resume`
// at the given 0-based turn: threads not currently in Layer B that were
// last engaged within ResumeWindowTurns turns. The result is in
// ascending creation order for determinism.
func (g *generator) resumeCandidates(turn int) []int {
	var out []int
	for idx := range g.threads {
		if g.inLayerB(idx) {
			continue
		}
		if turn-g.lastEngagedTurn[idx] <= g.cfg.ResumeWindowTurns {
			out = append(out, idx)
		}
	}
	return out
}

// inLayerB reports whether thread index idx is currently in Layer B.
func (g *generator) inLayerB(idx int) bool {
	for _, id := range g.layerB {
		if id == idx {
			return true
		}
	}
	return false
}

// runSession emits turns until it has consumed `active` worth of
// inter-turn gaps — i.e. the session covers `active` of turn-active
// simulated time. The session's own intra-turn gaps advance simNow;
// the larger inter-session/overnight/day-off gaps are folded in
// separately by the day primitives, so session length stays decoupled
// from those inserted gaps.
//
// Each turn samples a type, an action, and an inter-turn gap; the gap
// is accumulated into simNow and emitted as the *next* step's
// TimeDelta (the first step of the run carries a zero TimeDelta,
// matching the harness's pinned-clock start).
func (g *generator) runSession(active time.Duration) {
	sessionStart := len(g.steps)
	emitted := false
	var spent time.Duration
	for spent < active {
		tt := g.sampleTurnType()
		act := g.sampleAction(len(g.steps))

		// The TimeDelta for this step is whatever gap accumulated
		// before it: the inter-session/overnight/day-off gap (pendingGap)
		// on the first turn after one of those, or the previous turn's
		// sampled intra-turn gap.
		td := g.pendingGap
		g.pendingGap = 0

		step := g.buildStep(tt, act)
		step.TimeDelta = td
		g.steps = append(g.steps, step)
		emitted = true

		// Advance the clock by this turn's gap; it becomes the next
		// step's TimeDelta. `spent` tracks only this session's
		// intra-turn gaps so the session ends after `active` of them.
		gap := g.sampleGap(tt)
		g.simNow += gap
		g.pendingGap = gap
		spent += gap
	}
	if emitted {
		g.sessionStarts = append(g.sessionStarts, sessionStart)
	}
}

// sampleTurnType picks rapid vs work weighted by count.
func (g *generator) sampleTurnType() turnType {
	if g.rng.Intn(g.cfg.RapidWeight+g.cfg.WorkWeight) < g.cfg.RapidWeight {
		return turnRapid
	}
	return turnWork
}

// sampleAction picks continue/switch/resume/new weighted by the config
// weights, with the candidate-dependent actions masked out when they
// have no valid target:
//
//   - continue needs an active thread (Layer B non-empty);
//   - switch needs a warm non-active thread (len(layerB) >= 2);
//   - resume needs at least one recently-dormant thread.
//
// A masked action contributes zero weight; the choice is drawn from
// whatever remains. With no thread at all, the choice is forced to new.
// `turn` is the 0-based index of the turn being sampled.
func (g *generator) sampleAction(turn int) action {
	type opt struct {
		act    action
		weight int
	}
	var opts []opt
	if len(g.layerB) >= 1 {
		opts = append(opts, opt{actContinue, g.cfg.ContinueWeight})
	}
	if len(g.layerB) >= 2 {
		opts = append(opts, opt{actSwitch, g.cfg.SwitchWeight})
	}
	if len(g.resumeCandidates(turn)) > 0 {
		opts = append(opts, opt{actResume, g.cfg.ResumeWeight})
	}
	opts = append(opts, opt{actNew, g.cfg.NewWeight})

	total := 0
	for _, o := range opts {
		total += o.weight
	}
	if total == 0 {
		return actNew
	}
	r := g.rng.Intn(total)
	for _, o := range opts {
		if r < o.weight {
			return o.act
		}
		r -= o.weight
	}
	return actNew
}

// sampleGap returns a jittered inter-turn gap for the turn type.
func (g *generator) sampleGap(tt turnType) time.Duration {
	if tt == turnRapid {
		return jitter(g.rng, g.cfg.RapidGap)
	}
	return jitter(g.rng, g.cfg.WorkGap)
}

// jitter applies a modest multiplicative jitter (±30%) to base. Uses
// the supplied rng so the result stays deterministic.
func jitter(rng *rand.Rand, base time.Duration) time.Duration {
	// factor in [0.7, 1.3).
	factor := 0.7 + rng.Float64()*0.6
	return time.Duration(float64(base) * factor)
}

// buildStep constructs one scenarios.Step for the given turn type and
// action, mutating the thread model (creating the new thread, updating
// the Layer-B LRU). TimeDelta is filled in by the caller.
func (g *generator) buildStep(tt turnType, act action) scenarios.Step {
	var (
		idx   int // index of the engaged thread
		isNew bool
	)

	switch act {
	case actNew:
		idx = len(g.threads)
		// Corpus binding is deterministic by creation order — see
		// corpusModel.slotFor. familySize consecutive threads share one
		// slot; the binding never depends on rng, so the recall-oracle
		// fire rate is fixed and duration-independent (each rung
		// measures the same thing).
		g.threads = append(g.threads, thread{
			order:   idx,
			slotIdx: g.model.slotFor(idx),
		})
		isNew = true

	case actContinue:
		idx = g.layerB[0] // the active thread

	case actSwitch:
		// Switch targets a warm-but-not-active thread: one of
		// layerB[1:]. Staying inside Layer B means the topic tag names a
		// thread the runtime already has resident, so no §5.5 mid-turn
		// fetch is triggered.
		warm := g.layerB[1:]
		idx = warm[g.rng.Intn(len(warm))]

	case actResume:
		// Resume targets a recently-dormant thread — one no longer in
		// Layer B but engaged within ResumeWindowTurns. Naming it in the
		// topic tag triggers the §5.5 mid-turn fetch: the runtime aborts
		// the in-flight stream, fetches the thread, and re-prompts. The
		// step-indexed mock re-serves this step's response for the
		// re-prompt, so the turn runs clean with no second queued
		// response.
		cands := g.resumeCandidates(len(g.steps))
		idx = cands[g.rng.Intn(len(cands))]
	}

	thr := g.threads[idx]
	slot := g.model.slots[thr.slotIdx]

	// Recall-opportunity oracle: a dormant thread (not in Layer B, not
	// the one engaged this turn) bound to the SAME corpus slot is
	// anchored on exactly this slot's tags. The engaging turn's symbol
	// set is also exactly this slot's tags, so the §3.4 Jaccard layer
	// (threshold 0.4) fires for every such dormant thread — and ONLY
	// for them, since a different slot draws different synonyms. With
	// FamilySize=2 there is at most one such dormant sibling, so the
	// expected-match set is small and well-defined. The generator knows
	// the binding, so it is its own recall oracle. Collected in
	// ascending creation order for determinism.
	var recallIDs []string
	for _, cand := range g.threads {
		if cand.order == idx || g.inLayerB(cand.order) {
			continue
		}
		if cand.slotIdx == thr.slotIdx {
			recallIDs = append(recallIDs, cand.threadID())
		}
	}

	// Anchors are the slot's full tag set. A thread bound to this slot
	// is therefore anchored on exactly these tags — the lexical
	// substrate a same-slot recall query scans.
	anchors := append([]string(nil), slot.Tags...)

	// UserInput: on a recall opportunity, re-issue the slot's mad-libs
	// query verbatim — its #-prefixed tags drive the runtime's symbol
	// extraction, and the coalesced symbol set is exactly the slot's
	// tags, firing recall for the dormant same-slot thread(s).
	// Otherwise a plain engaging line mentioning a couple of the tags
	// so the turn still extracts on-topic symbols.
	var userInput string
	if len(recallIDs) > 0 {
		userInput = slot.UserInput
	} else {
		mention := slot.Tags[0]
		second := slot.Tags[1%len(slot.Tags)]
		userInput = fmt.Sprintf("working on #%s and %s", mention, second)
	}

	// MockResponse threads: *new-topic* for a new thread, else the
	// engaged thread's thr_N id.
	var threads []string
	if isNew {
		threads = []string{"*new-topic*"}
	} else {
		threads = []string{thr.threadID()}
	}
	body := fmt.Sprintf("Working through %s — %s.",
		slot.Topic, strings.Join(slot.Tags[:min(2, len(slot.Tags))], ", "))
	resp := scenarios.NewMockResponseWithTag(threads, anchors, body)

	step := scenarios.Step{
		UserInput:    userInput,
		MockResponse: resp,
		Annotation: fmt.Sprintf("turn %d: %s %s (%s)",
			len(g.steps)+1, turnTypeName(tt), actionName(act), slot.Topic),
		// Closure is set on EVERY step: any thread that decay-closes
		// during the run is resolved, and a nil ClosureAck would
		// disable closure detection that step.
		ClosureAck: &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
	}

	// work turns carry a real §3.9 read/modify/write cycle to exercise
	// the working-set content-dedup integration on the heavy turns:
	// fs.read of the file's current content, a deterministic modify, then
	// fs.write of the new content. Every commitEvery-th write also emits
	// an fs.commit with a deterministic synthetic hash. rapid turns have
	// no PreEvents.
	if tt == turnWork {
		fs := g.workFile(idx)
		// fs.read: the file's content as it stands before this turn's edit.
		preEvents := []turn.Delta{{
			Source:  "fs.read",
			Content: fs.content,
			Meta:    map[string]string{"path": fs.path},
		}}
		// Modify: append a deterministic line keyed off the topic and the
		// write count so successive writes produce real diffs.
		fs.writeCount++
		fs.content += fmt.Sprintf("\n// edit %d: %s\n", fs.writeCount, slot.Topic)
		// fs.write: the new content.
		preEvents = append(preEvents, turn.Delta{
			Source:  "fs.write",
			Content: fs.content,
			Meta:    map[string]string{"path": fs.path},
		})
		// Every commitEvery-th write commits to the user's project git.
		if fs.writeCount%commitEvery == 0 {
			preEvents = append(preEvents, turn.Delta{
				Source: "fs.commit",
				Meta: map[string]string{
					"path": fs.path,
					"hash": syntheticHash(fs.content),
				},
			})
		}
		step.PreEvents = preEvents
	}

	// RecallAck is set on every step (a nil ack would disable the
	// recall resolver for the step). It is always a decline-all: the
	// harness's recall resolver fails the test if an accepted thread ID
	// was not in the runtime's recall offer, and the generator cannot
	// predict the runtime's offer set — so accepting nothing is the
	// only contract-safe choice. Recall is *measured* here, not
	// acted on.
	step.RecallAck = &scenarios.RecallAck{Reason: turn.DeclineNotRelevant}

	// On a recall opportunity, declare the ground-truth match set so
	// the harness records per-step recall fidelity. RecallMeasureOnly:
	// a recall miss must not fail this smoke rung.
	if len(recallIDs) > 0 {
		step.ExpectedRecallMatches = recallIDs
		step.RecallMode = scenarios.RecallMeasureOnly
	}

	// Update the Layer-B LRU and last-engaged bookkeeping to reflect
	// this turn's engagement. len(g.steps) is this turn's 0-based index.
	g.engage(idx, len(g.steps))
	return step
}

// syntheticHash returns a short deterministic hex digest of content, used
// as the synthetic git-commit hash on an fs.commit delta. It is a content
// hash (FNV-64a), so it is reproducible across runs and distinct per
// content revision — exactly what checkpoint 5d's clock-aging needs from a
// commit pointer.
func syntheticHash(content string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(content))
	return fmt.Sprintf("%012x", h.Sum64()&0xffffffffffff)
}

func turnTypeName(tt turnType) string {
	if tt == turnRapid {
		return "rapid"
	}
	return "work"
}

func actionName(a action) string {
	switch a {
	case actContinue:
		return "continue"
	case actSwitch:
		return "switch"
	case actResume:
		return "resume"
	default:
		return "new"
	}
}
