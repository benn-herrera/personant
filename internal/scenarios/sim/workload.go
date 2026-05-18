// Package sim is a deterministic, seeded synthetic-workload generator
// for the Personant scenario harness (spec §9.1, §9.10).
//
// GenerateWorkload turns a WorkloadConfig into a scenarios.Scenario: a
// stream of synthetic turns modelling a user moving among a handful of
// topical threads across a day. The same config produces a
// byte-identical Scenario — all randomness flows from one math/rand
// source seeded by WorkloadConfig.Seed.
//
// This package feeds the rung walk toward the six-month acceptance
// simulation. The 1-day rung (TestSim_1d) drives the generated
// Scenario through scenarios.RunScenario unchanged; no new runner is
// needed. Rate parameters here are seed values meant to be tuned as
// the rung walk climbs — Duration is the only knob that must change to
// lengthen a run.
package sim

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"personant/internal/scenarios"
	"personant/internal/turn"
)

// WorkloadConfig parameterises a generated workload. Seed and Duration
// are the load-bearing knobs; the rate fields have sensible defaults
// applied by withDefaults so a zero-value-but-for-Seed-and-Duration
// config is valid.
type WorkloadConfig struct {
	// Seed seeds the single rand source. Fixed seed → identical
	// Scenario.
	Seed int64

	// Duration is the simulated span to generate. The generator emits
	// turns until cumulative simulated time reaches Duration. For the
	// 1-day rung this is one 12 h work day (see TestSim_1d).
	Duration time.Duration

	// RapidGap and WorkGap are the mean inter-turn gaps for the two
	// turn classes. Each sampled gap carries modest multiplicative
	// jitter (see sampleGap).
	RapidGap time.Duration
	WorkGap  time.Duration

	// InterSessionGap is the larger gap inserted once, between the two
	// sessions of the day.
	InterSessionGap time.Duration

	// RapidWeight / WorkWeight bias turn-type selection by count. The
	// default 6:1 yields a roughly 50/50 split by *simulated time*,
	// since a work turn consumes ~6× the gap of a rapid turn.
	RapidWeight int
	WorkWeight  int

	// ContinueWeight / SwitchWeight / NewWeight bias the per-turn
	// action selection. They need not sum to anything in particular.
	ContinueWeight int
	SwitchWeight   int
	NewWeight      int

	// TopicCount is the size of the synthetic topic pool a thread can
	// be bound to.
	TopicCount int
}

// withDefaults returns a copy of cfg with any zero rate field replaced
// by its default. Seed and Duration are left untouched (Duration of 0
// is a caller error surfaced as an empty Scenario).
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
	if cfg.RapidWeight == 0 {
		cfg.RapidWeight = 6
	}
	if cfg.WorkWeight == 0 {
		cfg.WorkWeight = 1
	}
	if cfg.ContinueWeight == 0 {
		cfg.ContinueWeight = 55
	}
	if cfg.SwitchWeight == 0 {
		cfg.SwitchWeight = 25
	}
	if cfg.NewWeight == 0 {
		cfg.NewWeight = 20
	}
	if cfg.TopicCount == 0 {
		cfg.TopicCount = 12
	}
	return cfg
}

// topic is a synthetic subject a thread binds to. Symbols are the 6–8
// distinct strings drawn on for the thread's anchors and turn text.
type topic struct {
	name    string
	symbols []string
}

// makeTopics builds a deterministic pool of n topics, each with 6–8
// symbols named topic-<i>-<suffix>. Generation order is fixed, so the
// pool is identical for a given (n, rng-sequence).
func makeTopics(rng *rand.Rand, n int) []topic {
	// A fixed suffix alphabet — index into it for symbol names.
	suffixes := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	topics := make([]topic, n)
	for i := range topics {
		count := 6 + rng.Intn(3) // 6, 7, or 8
		syms := make([]string, count)
		for j := range syms {
			syms[j] = fmt.Sprintf("topic-%d-%s", i, suffixes[j])
		}
		topics[i] = topic{
			name:    fmt.Sprintf("topic-%d", i),
			symbols: syms,
		}
	}
	return topics
}

// thread is the generator's in-memory model of one simulated thread:
// its creation order and its bound topic. The generator does not know
// the runtime's thr_N ids at construction time; threadID() maps
// creation order to the id the runtime is guaranteed to assign.
//
// Per-thread engagement timing and lifecycle intent are deliberately
// not modelled here — the runtime owns thread decay/closure, and the
// generator drives that purely by emitting idle turns and advancing
// the clock. Layer-B membership (generator.layerB) is the only thread
// state the generator must track to stay faithful to the runtime.
type thread struct {
	order    int // 0-based creation order
	topicIdx int // index into the topic pool
}

// threadID returns the thr_N id the runtime assigns to a thread by
// creation order. Threads are numbered thr_1, thr_2, … in the order
// they are created (harness/runtime guarantee).
func (t thread) threadID() string {
	return fmt.Sprintf("thr_%d", t.order+1)
}

// turnType is the rapid/work classification driving gap length and
// whether a PreEvents tool.result delta is attached.
type turnType int

const (
	turnRapid turnType = iota
	turnWork
)

// action is the per-turn thread-population move.
type action int

const (
	actContinue action = iota
	actSwitch
	actNew
)

// GenerateWorkload is a pure function: same WorkloadConfig → identical
// scenarios.Scenario. It models the simulated user as a thread
// population evolving over two sessions of a day, emitting one
// scenarios.Step per turn.
func GenerateWorkload(cfg WorkloadConfig) scenarios.Scenario {
	cfg = cfg.withDefaults()
	rng := rand.New(rand.NewSource(cfg.Seed))

	g := &generator{
		cfg:    cfg,
		rng:    rng,
		topics: makeTopics(rng, cfg.TopicCount),
	}

	// A day is two sessions of ~half the duration each, separated by
	// one larger gap. Session 1 runs until simNow reaches Duration/2;
	// the inter-session gap is then inserted and carried as the next
	// step's TimeDelta; session 2 runs until simNow reaches Duration.
	half := cfg.Duration / 2
	g.runSession(half)
	g.pendingGap = jitter(rng, cfg.InterSessionGap)
	g.runSession(cfg.Duration)

	return scenarios.Scenario{
		Name:  fmt.Sprintf("sim-workload-seed%d-dur%s", cfg.Seed, cfg.Duration),
		Steps: g.steps,
	}
}

// layerBCap mirrors workset.DefaultBTopK (spec §2.6.1 layer.b-top-k):
// the runtime keeps the 3 most-recently-engaged threads in Layer B,
// fully present in the working set. A topic tag naming a thr_N NOT in
// Layer B triggers the §5.5 mid-turn fetch + re-prompt, which consumes
// a second mock response — and RunScenario pre-queues exactly one
// response per step. So the generator must only ever name a thread
// that is still in Layer B; it models Layer B here to honour that.
const layerBCap = 3

// generator carries the mutable state threaded through workload
// construction: the rng, the topic pool, the live thread model
// (including a faithful Layer-B LRU), the accumulating step list, and
// the simulated clock.
type generator struct {
	cfg    WorkloadConfig
	rng    *rand.Rand
	topics []topic

	threads []thread // by creation order

	// layerB is the generator's model of the runtime's Layer B: thread
	// indices, most-recently-engaged first, capped at layerBCap. The
	// active thread is layerB[0]. A `continue` re-engages layerB[0]; a
	// `switch` targets one of layerB[1:] (warm but not active) so the
	// resulting topic tag never triggers a mid-turn fetch.
	layerB []int

	steps      []scenarios.Step
	simNow     time.Duration
	pendingGap time.Duration // gap to apply as the next step's TimeDelta
}

// engage records thread index idx as the most-recently-engaged thread,
// updating the Layer-B LRU exactly as the runtime's updateLayerLRU
// does: move-to-front if present, else prepend; evict the tail past
// layerBCap.
func (g *generator) engage(idx int) {
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

// runSession emits turns until the simulated clock reaches untilSim.
// Each turn samples a type, an action, and an inter-turn gap; the gap
// is accumulated into simNow and emitted as the *next* step's
// TimeDelta (the first step of the run carries a zero TimeDelta,
// matching the harness's pinned-clock start).
func (g *generator) runSession(untilSim time.Duration) {
	for g.simNow < untilSim {
		tt := g.sampleTurnType()
		act := g.sampleAction()

		// The TimeDelta for this step is whatever gap accumulated
		// before it: the inter-session gap (pendingGap) on the first
		// turn of session 2, or the previous turn's sampled gap.
		td := g.pendingGap
		g.pendingGap = 0

		step := g.buildStep(tt, act)
		step.TimeDelta = td
		g.steps = append(g.steps, step)

		// Advance the clock by this turn's gap; it becomes the next
		// step's TimeDelta.
		gap := g.sampleGap(tt)
		g.simNow += gap
		g.pendingGap = gap
	}
}

// sampleTurnType picks rapid vs work weighted by count.
func (g *generator) sampleTurnType() turnType {
	if g.rng.Intn(g.cfg.RapidWeight+g.cfg.WorkWeight) < g.cfg.RapidWeight {
		return turnRapid
	}
	return turnWork
}

// sampleAction picks continue/switch/new weighted. switch is only
// offered when Layer B holds a warm non-active thread to switch to
// (len(layerB) >= 2); with no active thread at all the choice is
// forced to new.
func (g *generator) sampleAction() action {
	if len(g.layerB) == 0 {
		return actNew // no thread to continue or switch to
	}
	if len(g.layerB) < 2 {
		// Only the active thread is warm: switch has no in-Layer-B
		// target. Re-roll continue vs new from their relative weights.
		if g.rng.Intn(g.cfg.ContinueWeight+g.cfg.NewWeight) < g.cfg.ContinueWeight {
			return actContinue
		}
		return actNew
	}
	r := g.rng.Intn(g.cfg.ContinueWeight + g.cfg.SwitchWeight + g.cfg.NewWeight)
	switch {
	case r < g.cfg.ContinueWeight:
		return actContinue
	case r < g.cfg.ContinueWeight+g.cfg.SwitchWeight:
		return actSwitch
	default:
		return actNew
	}
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
		idx   int  // index of the engaged thread
		isNew bool
	)

	switch act {
	case actNew:
		idx = len(g.threads)
		g.threads = append(g.threads, thread{
			order:    idx,
			topicIdx: g.rng.Intn(len(g.topics)),
		})
		isNew = true

	case actContinue:
		idx = g.layerB[0] // the active thread

	case actSwitch:
		// Switch targets a warm-but-not-active thread: one of
		// layerB[1:]. Staying inside Layer B guarantees the topic tag
		// names a thread the runtime already has, so no §5.5 mid-turn
		// fetch (and no second mock response) is triggered.
		warm := g.layerB[1:]
		idx = warm[g.rng.Intn(len(warm))]
	}

	thr := &g.threads[idx]
	tp := g.topics[thr.topicIdx]

	// Recall-opportunity oracle: a dormant thread (not in Layer B, not
	// the one engaged this turn) bound to the SAME topic shares all of
	// this turn's topic symbols, so the runtime's symbolic recall layer
	// has a genuine spine.match-fire candidate. The generator knows
	// this because it tracks every thread's topic binding — it is its
	// own recall-fidelity oracle. Pick the lowest-index such thread for
	// determinism.
	recallID := ""
	for _, cand := range g.threads {
		if cand.order == idx || g.inLayerB(cand.order) {
			continue
		}
		if cand.topicIdx == thr.topicIdx {
			recallID = cand.threadID()
			break
		}
	}

	// Anchors: 4–8 of the topic's symbols. The topic has 6–8 symbols;
	// take a deterministic prefix sized in [4, len].
	anchorCount := 4 + g.rng.Intn(len(tp.symbols)-3)
	if anchorCount > len(tp.symbols) {
		anchorCount = len(tp.symbols)
	}
	anchors := append([]string(nil), tp.symbols[:anchorCount]...)

	// UserInput mentions a couple of the topic's symbols so the
	// runtime's deterministic symbol extraction picks them up. The #
	// prefix on the first makes it a user-tag-class symbol.
	mention := tp.symbols[0]
	second := tp.symbols[1%len(tp.symbols)]
	userInput := fmt.Sprintf("working on #%s and %s", mention, second)

	// MockResponse threads: *new-topic* for a new thread, else the
	// target thread's thr_N id.
	var threads []string
	if isNew {
		threads = []string{"*new-topic*"}
	} else {
		threads = []string{thr.threadID()}
	}
	body := fmt.Sprintf("Working through %s — %s.", tp.name, strings.Join(tp.symbols[:2], ", "))
	resp := scenarios.NewMockResponseWithTag(threads, anchors, body)

	step := scenarios.Step{
		UserInput:    userInput,
		MockResponse: resp,
		Annotation:   fmt.Sprintf("turn %d: %s %s (%s)", len(g.steps)+1, turnTypeName(tt), actionName(act), tp.name),
		// Closure is set on EVERY step: any thread that decay-closes
		// during the run is resolved, and a nil ClosureAck would
		// disable closure detection that step.
		ClosureAck: &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
	}

	// work turns carry a synthetic file-read delta to exercise the
	// Phase B transient-data lifecycle on the heavy turns. rapid turns
	// have no PreEvents.
	if tt == turnWork {
		fileText := fmt.Sprintf("cat %s.go returned: references %s",
			tp.name, strings.Join(tp.symbols[:3], " "))
		step.PreEvents = []turn.Delta{{
			Source:  "tool.result",
			Content: fileText,
		}}
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
	if recallID != "" {
		step.ExpectedRecallMatches = []string{recallID}
		step.RecallMode = scenarios.RecallMeasureOnly
	}

	// Update the Layer-B LRU to reflect this turn's engagement.
	g.engage(idx)
	return step
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
	default:
		return "new"
	}
}
