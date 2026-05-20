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
// that slot. With 3010 corpus slots the recall oracle resolves to a
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
// The mock serves one response per scenario step (see model.MockClient):
// the harness installs the step's response before the turn, and every
// consult during the step — including the §5.5 re-prompt — re-serves it.
// So a turn that triggers a mid-turn fetch needs no fetch prediction
// from the generator and no second queued response.
//
// On-demand generation. GenerateWorkload does NOT materialise the whole
// step list — a six-month run is ~59 000+ steps. Instead it returns a
// scenarios.Scenario whose StepSource is a *generator: a resumable state
// machine the harness pulls one step at a time. The generator advances
// the simulated calendar one day at a time, buffering that day's steps
// (~400 at most) and handing them out via Next(); when the buffer
// drains it generates the next day, until the simulated clock has
// advanced by Duration. Determinism is unchanged — the same (Seed,
// Duration) draws from the RNG in the identical order and yields the
// identical run; the workload is simply no longer pre-computed.
//
// Next(StepFeedback) receives the recall outcome of the step that just
// ran and drives the miss → refinement loop: when a recall-opportunity
// step misses (observed match-fires < expected), the generator injects
// a refinement turn ahead of the next buffered step — same primary
// thread, a different slot of the same topic. Up to three attempts per
// episode; each closed episode contributes one sample to
// `recall_episode_queries_to_hit` (or increments
// `recall_episode_unresolved` if all three miss). The canonical
// (zero-feedback) step stream — the workload before any refinement
// injection — remains a pure function of (Seed, Duration, Corpus), so
// the determinism contract holds when no feedback is supplied
// (drainSteps in the determinism tests).
package sim

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"slices"
	"strings"
	"time"

	"personant/internal/prompt"
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
// LooseMask is aligned position-wise with Tags: LooseMask[i] is true
// iff Tags[i] was drawn from its column's "loose" (drift) cell — the
// last cell of the template's column, deliberately a semantic stretch
// rather than a close synonym. The mask is the load-bearing input to
// the thread-anchor binding: a new thread's anchors are slot.Tags
// FILTERED to drop the loose positions, so the §3.4 Jaccard layer's
// thread-set (T) excludes loose drift while the recall query (Q) still
// contains those terms (the query is the verbatim UserInput including
// the loose #-tags). The resulting Q vs. T asymmetry is what makes
// recall miss probabilistically on loose-heavy slots — see buildStep.
// Loading the mask is the test's job (it consults the corpus_templates
// column data); slots without column ground truth pass an all-false
// mask, restoring the previous all-tags-are-anchors binding.
//
// The slots are the corpus_queries.json `queries` array verbatim: 301
// Wikipedia topics × 10 queries = 3010 slots. Loading is the test's
// job (it owns the testdata path); GenerateWorkload takes the slice as
// part of its config so it stays a pure function.
type CorpusSlot struct {
	Topic     string
	Tags      []string
	LooseMask []bool
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

// nonLooseTags returns the slot's tags with loose-cell positions
// dropped, preserving order. A slot whose LooseMask is nil (no column
// ground truth available) yields a copy of Tags unchanged — the prior
// all-tags-are-anchors binding. If every position is loose (vanishingly
// rare under Binomial(5, 1/5)) the result is nil; callers downstream
// handle that via the spec §2.2 anchor-cap padding path.
func nonLooseTags(slot CorpusSlot) []string {
	if len(slot.LooseMask) != len(slot.Tags) {
		return append([]string(nil), slot.Tags...)
	}
	out := make([]string, 0, len(slot.Tags))
	for i, t := range slot.Tags {
		if !slot.LooseMask[i] {
			out = append(out, t)
		}
	}
	return out
}

// firstNonLooseTag returns the first tag in slot.Tags whose LooseMask
// entry is false — the user-input #-mention on a new-thread turn must
// be a non-loose tag, so the runtime's symbol extraction does not
// re-introduce a loose term into the new thread's coalesced anchor set
// (which would defeat the loose-filter on anchorTags). Falls back to
// Tags[0] when no mask is provided or every position is loose.
func firstNonLooseTag(slot CorpusSlot) string {
	if len(slot.LooseMask) == len(slot.Tags) {
		for i, loose := range slot.LooseMask {
			if !loose {
				return slot.Tags[i]
			}
		}
	}
	return slot.Tags[0]
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

// userDictatedPct is the percentage of *eligible* work turns that emit a
// "user-dictated file content" variant (Realism C) instead of the default
// agentic-edit shape. Eligibility excludes recall-opportunity turns (whose
// UserInput must be the slot's mad-libs query verbatim) and new-thread
// turns (whose extracted user-prompt symbols become spine anchors, and
// must stay loose-clean). At 15% on a 1-week rung this yields ~30–50
// variant episodes — enough to observe the user-prompt symbol-extraction,
// §3.0 classification, and §3.9 live-window paths firing, without
// shifting the existing recall-band statistics calibrated on the
// current mix.
const userDictatedPct = 15

// userDictatedSelector is the [0,100) draw cap that decides variant vs.
// agentic for an eligible turn. Drawn from g.rng inside buildStep, so the
// selection is seed-deterministic.
const userDictatedSelector = 100

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

// GenerateWorkload turns a WorkloadConfig into a scenarios.Scenario
// driven by an on-demand StepSource. Same WorkloadConfig → identical
// run: the workload is a pure function of (Seed, Duration, Corpus), it
// is simply produced one step at a time rather than all up front.
//
// It models the simulated user as a thread population evolving across
// calendar days, emitting one scenarios.Step per turn. The day is the
// repeating primitive: the generator emits calendar days until the
// simulated clock has advanced by cfg.Duration. A week is 6 work days
// plus 1 day off: a work day is two ~6 h sessions split by an
// inter-session gap, followed by an overnight gap; a day off emits no
// turns and simply advances the clock by DayOffGap, deliberately
// exercising the §3.5 wall-clock decay path.
//
// A nil/empty Corpus yields a Scenario whose StepSource is immediately
// exhausted — the caller must supply the recall_madlibs slot pool.
func GenerateWorkload(cfg WorkloadConfig) scenarios.Scenario {
	cfg = cfg.withDefaults()
	g := &generator{
		cfg: cfg,
		rng: rand.New(rand.NewSource(cfg.Seed)),
		model: corpusModel{
			slots:      cfg.Corpus,
			familySize: cfg.FamilySize,
		},
		files: map[int]*fileState{},
	}
	return scenarios.Scenario{
		Name:       fmt.Sprintf("sim-workload-seed%d-dur%s", cfg.Seed, cfg.Duration),
		StepSource: g,
	}
}

// bufStep is one buffered step plus the per-step generator metadata the
// refinement loop needs: the corpus slot the step issued and the
// 0-based corpus-creation-order index of the engaged thread. These are
// cheap to record at buildStep time and avoid re-parsing the step's
// Annotation. Refinement-injected steps never live in a day buffer, so
// only the canonical buffered steps carry bufStep wrappers.
type bufStep struct {
	step       scenarios.Step
	slotIdx    int // corpus slot the step issued the query of
	engagedIdx int // creation-order index of the engaged thread
}

// dayBuf is one generated calendar day's steps plus the global step
// index of its first step, so a global step index can be translated
// into an offset within steps.
type dayBuf struct {
	steps     []bufStep
	firstStep int // global step index of steps[0]
}

// Next yields the next scenarios.Step, implementing scenarios.StepSource.
// It hands out the current day's buffered steps one at a time; when that
// day drains it advances the generator until another day with turns is
// ready, until the simulated clock has advanced by cfg.Duration. The
// returned bool is false once the run is complete.
//
// feedback carries the recall outcome of the step that just ran. The
// generator consumes it to drive the miss → refinement loop: on an
// open episode, an observed hit closes the episode (recording
// attempts), an observed miss with attempts < 3 INJECTS a refinement
// turn ahead of the next buffered step, and a miss on the 3rd attempt
// closes the episode as unresolved. Refinement injection does not
// reshape the day buffer — it simply emits one extra step before the
// next buffered draw, leaving downstream Layer-B/thread assumptions
// intact. A zero feedback (RecallExpected == 0) is the
// "no-recall-opportunity-observed" signal and never triggers
// refinement, so the canonical step stream remains a pure function of
// (Seed, Duration, Corpus) — the determinism contract drainSteps
// relies on.
func (g *generator) Next(feedback scenarios.StepFeedback) (scenarios.Step, bool) {
	// Process the just-completed step's outcome.
	if g.recallEpisodeOpen && feedback.RecallExpected > 0 {
		switch {
		case feedback.RecallMatchFires >= feedback.RecallExpected:
			// HIT — close the episode and record queries-to-hit.
			g.closeEpisodeHit()
		case g.recallAttempts < 3:
			// MISS with attempts remaining — inject a refinement turn
			// (a different slot of the same topic, same primary thread).
			g.recallAttempts++
			return g.buildRefinementStep(), true
		default:
			// MISS on the 3rd attempt — close as unresolved.
			g.closeEpisodeUnresolved()
		}
	}

	// Draw the next buffered step, advancing days as needed.
	for g.ready == nil || g.readyPos >= len(g.ready.steps) {
		if !g.advance() {
			// Run is over. If an episode is still open without a clean
			// close (e.g. final step was a missed recall opportunity and
			// no further steps remain), count it as unresolved.
			if g.recallEpisodeOpen {
				g.closeEpisodeUnresolved()
			}
			return scenarios.Step{}, false
		}
	}
	bs := g.ready.steps[g.readyPos]
	g.readyPos++

	// If this buffered step opens a new recall opportunity, start an
	// episode. A new opportunity supersedes any still-open prior episode
	// (the prior never got a clean close — record it as unresolved so
	// it is not silently dropped from the count).
	if len(bs.step.ExpectedRecallMatches) > 0 {
		if g.recallEpisodeOpen {
			g.closeEpisodeUnresolved()
		}
		g.openEpisode(bs)
	}

	return bs.step, true
}

// openEpisode begins a new recall episode for the just-emitted buffered
// step. The slot the step issued is captured so a later refinement can
// pick the NEXT slot of the same topic.
func (g *generator) openEpisode(bs bufStep) {
	g.recallEpisodeOpen = true
	g.recallAttempts = 1
	g.currentRecallSlotIdx = bs.slotIdx
	g.currentRecallTopic = g.model.slots[bs.slotIdx].Topic
}

// closeEpisodeHit records a hit episode (n attempts to first hit) and
// resets episode state.
func (g *generator) closeEpisodeHit() {
	g.episodeQueriesToHit = append(g.episodeQueriesToHit, g.recallAttempts)
	g.resetEpisode()
}

// closeEpisodeUnresolved increments the unresolved counter and resets
// episode state.
func (g *generator) closeEpisodeUnresolved() {
	g.episodeUnresolved++
	g.resetEpisode()
}

// resetEpisode clears the per-episode state. Called after every close.
func (g *generator) resetEpisode() {
	g.recallEpisodeOpen = false
	g.recallAttempts = 0
	g.currentRecallSlotIdx = 0
	g.currentRecallTopic = ""
}

// buildRefinementStep constructs one refinement turn: re-engage the
// current primary thread (Layer-B head — re-engaging the head is an
// LRU no-op, so the day buffer's downstream Layer-B assumptions stay
// valid) and issue a DIFFERENT slot of the same topic so the query
// phrasing genuinely differs from the missed attempt. The recall
// oracle for the refinement is computed the same slot-equality way as
// canonical steps — dormant non-Layer-B threads bound to the new slot.
//
// Refinement does NOT mutate the generator's Layer-B/last-engaged
// bookkeeping: the runtime sees the engagement in its own Layer B, but
// the generator's view of the world is the one that drives future
// buffered-day generation, and we want minimal disturbance to that.
func (g *generator) buildRefinementStep() scenarios.Step {
	// The primary thread to re-engage is layerB[0]. layerB is non-empty
	// because the episode-opening canonical step engaged some thread,
	// which is now layerB[0].
	idx := g.layerB[0]

	// Pick the next slot of the same topic. slotsForTopic caches the
	// list of corpus-wide slot indices for a topic (10 per topic in the
	// recall_madlibs corpus, but slotsForTopic uses the actual count).
	topicSlots := g.slotsForTopic(g.currentRecallTopic)
	// currentRecallSlotIdx is the last-issued slot; advance to its next
	// sibling in topicSlots (modulo for wraparound on long episodes,
	// though attempts <= 3 means we will never wrap in practice with
	// 10 slots per topic).
	pos := 0
	for i, si := range topicSlots {
		if si == g.currentRecallSlotIdx {
			pos = i
			break
		}
	}
	newSlotIdx := topicSlots[(pos+1)%len(topicSlots)]
	g.currentRecallSlotIdx = newSlotIdx
	slot := g.model.slots[newSlotIdx]

	// Recall oracle for the refinement: dormant threads (not in Layer B,
	// not the engaged primary) bound to the NEW slot. Same algorithm as
	// canonical buildStep — only the slot-index differs.
	var recallIDs []string
	for _, cand := range g.threads {
		if cand.order == idx || g.inLayerB(cand.order) {
			continue
		}
		if cand.slotIdx == newSlotIdx {
			recallIDs = append(recallIDs, cand.threadID())
		}
	}

	thr := g.threads[idx]
	step := scenarios.Step{
		// The refinement's TimeDelta is a small jittered rapid gap — the
		// user issuing a re-phrased follow-up moments later. This
		// consumes rng, but rng is consumed only on refinement injection
		// (conditional on real feedback), so the canonical (zero-feedback)
		// step stream's rng draws are unchanged.
		TimeDelta: jitter(g.rng, g.cfg.RapidGap),
		UserInput: slot.UserInput,
		// MockResponse threads: re-engagement of the engaged thread (no
		// new-topic; refinements never spawn).
		MockResponse: scenarios.NewMockResponseWithTag(
			[]string{thr.threadID()},
			nonLooseTags(slot),
			fmt.Sprintf("Refining %s — %s.",
				slot.Topic, strings.Join(slot.Tags[:min(2, len(slot.Tags))], ", ")),
		),
		Annotation: fmt.Sprintf("refinement attempt %d (%s)",
			g.recallAttempts, slot.Topic),
		ClosureAck: &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:  &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
	}
	if len(recallIDs) > 0 {
		step.ExpectedRecallMatches = recallIDs
		step.RecallMode = scenarios.RecallMeasureOnly
	}
	return step
}

// slotsForTopic returns the corpus-wide slot indices belonging to topic,
// in corpus order. Built lazily on first call and memoised — every call
// after the first is a map lookup. Topic order is stable for a given
// corpus, so the result is deterministic.
func (g *generator) slotsForTopic(topic string) []int {
	if g.topicSlots == nil {
		g.topicSlots = map[string][]int{}
		for i, s := range g.model.slots {
			g.topicSlots[s.Topic] = append(g.topicSlots[s.Topic], i)
		}
	}
	return g.topicSlots[topic]
}

// advance moves the next day-with-turns into g.ready, returning false
// once the run is exhausted.
//
// It runs a one-day-deep pipeline: g.pending holds the most recently
// generated day-with-turns, not yet released. advance generates calendar
// days (a day off emits nothing and is skipped) until either another
// day-with-turns is produced — at which point the old g.pending is
// promoted to g.ready and the new day becomes g.pending — or the run
// ends, at which point the final g.pending is promoted as the last day.
//
// The one-day hold is what makes the final-session restart mark
// resolvable on demand: g.pending is released only once we know whether
// a later day-with-turns exists, so the last one can be marked before
// it leaves the generator. Memory stays bounded at two days (~800
// steps) versus the whole ~59 000+-step run.
func (g *generator) advance() bool {
	for {
		day, ok := g.generateNextDay()
		if !ok {
			// Run over. Promote the final pending day, if any, marking
			// its last session as the final-session restart point.
			if g.pending == nil {
				return false
			}
			g.markFinalSession(g.pending)
			g.ready, g.readyPos, g.pending = g.pending, 0, nil
			return true
		}
		if len(day.steps) == 0 {
			continue // a day off emits no turns; skip it
		}
		d := day
		if g.pending == nil {
			g.pending = &d
			continue // hold the first day-with-turns for one more lap
		}
		// A later day-with-turns exists, so g.pending is not the final
		// day — release it and hold the new day.
		g.ready, g.readyPos, g.pending = g.pending, 0, &d
		return true
	}
}

// generateNextDay advances the simulated calendar by exactly one day,
// returning that day's steps. ok is false when the run is complete: an
// empty corpus (nothing to generate) or the simulated clock having
// reached cfg.Duration.
//
// The day-loop control flow is the build-all generator's verbatim: emit
// calendar days while g.simNow < cfg.Duration; every 7th day (index 6,
// 13, …) is the week's day off, which emits no turns. The only change
// is that a day's steps land in a fresh per-day buffer rather than one
// run-long slice, and the second-session restart mark is applied as the
// day is generated rather than in a final pass.
func (g *generator) generateNextDay() (dayBuf, bool) {
	if len(g.cfg.Corpus) == 0 || g.simNow >= g.cfg.Duration {
		return dayBuf{}, false
	}

	day := dayBuf{firstStep: g.stepIndex}
	g.day = &day // runSession appends into day.steps via g.day

	if g.dayIndex%7 == 6 {
		g.runDayOff()
	} else {
		g.runWorkDay()
	}
	g.dayIndex++
	g.day = nil

	g.markSecondSession(day)
	return day, true
}

// markSecondSession flags the start of the run's SECOND session with
// RestartSession if it falls inside the just-generated day — a clean
// shutdown→relaunch near the run start, at minimum persisted history.
//
// g.sessionStarts accumulates every emitting session's global step
// index; the second entry is the second session's start. Step 0 is
// never marked (a restart before any turn is meaningless).
func (g *generator) markSecondSession(day dayBuf) {
	if len(g.sessionStarts) < 2 {
		return
	}
	markRestartIn(day, g.sessionStarts[1])
}

// markFinalSession flags the start of the run's FINAL session — the
// last emitting session of the last day with turns — with
// RestartSession, a clean shutdown→relaunch near the run end, at
// maximum persisted history.
func (g *generator) markFinalSession(day *dayBuf) {
	if len(g.sessionStarts) == 0 {
		return
	}
	markRestartIn(*day, g.sessionStarts[len(g.sessionStarts)-1])
}

// markRestartIn sets RestartSession on the step at global index
// globalIdx, if that step falls within day's buffer. Step 0 is never
// marked. Setting the same step twice is harmless (idempotent), so a
// short run whose second- and final-session boundaries collide is fine.
func markRestartIn(day dayBuf, globalIdx int) {
	if globalIdx == 0 {
		return
	}
	off := globalIdx - day.firstStep
	if off >= 0 && off < len(day.steps) {
		day.steps[off].step.RestartSession = true
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

// layerBCap mirrors memops.DefaultBTopK (spec §2.6.1 layer.b-top-k):
// the runtime keeps the 3 most-recently-engaged threads in Layer B,
// fully present in the working set. A topic tag naming a thr_N NOT in
// Layer B triggers the §5.5 mid-turn fetch + re-prompt. The generator
// models Layer B so it can distinguish a `switch` (warm, in-Layer-B,
// no fetch) from a `resume` (dormant, out of Layer B, triggers the
// fetch) — and so the recall oracle can exclude resident threads.
const layerBCap = 3

// generator is the resumable workload state machine. It implements
// scenarios.StepSource: each Next() call yields one scenarios.Step,
// advancing the simulated calendar a day at a time rather than building
// the whole step list up front.
//
// It carries the mutable state the build-all generator carried — the
// rng, the corpus binding model, the live thread model (Layer-B LRU +
// per-thread last-engaged turn), the simulated clock — plus the
// on-demand machinery: the day-loop position, the global step counter,
// and the two-day step buffer pipeline (see advance).
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

	simNow     time.Duration
	pendingGap time.Duration // gap to apply as the next step's TimeDelta

	// dayIndex is the 0-based calendar day the day loop is on; stepIndex
	// is the running global 0-based index of the next step to emit (the
	// build-all generator read this as len(g.steps)).
	dayIndex  int
	stepIndex int

	// day is the buffer the current generateNextDay call appends into;
	// nil outside a day-generation pass.
	day *dayBuf

	// ready is the day whose steps Next() is currently handing out;
	// readyPos is the next offset within it. pending is the most
	// recently generated day-with-turns, held one lap so the
	// final-session restart mark is resolvable on demand (see advance).
	ready    *dayBuf
	readyPos int
	pending  *dayBuf

	// sessionStarts records the global 0-based step index that begins
	// each session (each runSession call — runSession always emits at
	// least one turn). The shutdown/restart marking uses this to locate
	// two distinct session boundaries — one near the run start (minimum
	// history) and one near the run end (maximum history).
	sessionStarts []int

	// Per-recall-episode state. An episode opens when Next() emits a
	// buffered step with ExpectedRecallMatches > 0 and closes on
	// observed hit (record queries-to-hit), unresolved exhaustion (after
	// 3 attempts of misses), or supersession (a new recall opportunity
	// emitted before the prior closed).
	recallEpisodeOpen    bool
	recallAttempts       int    // 1..3 — number of recall queries issued in the current episode
	currentRecallSlotIdx int    // corpus-wide slot index of the LAST issued recall query
	currentRecallTopic   string // topic of the open episode (its slots form the refinement pool)

	// topicSlots maps a topic name to the corpus-wide slot indices
	// belonging to that topic, in corpus order. Built lazily on first
	// refinement (slotsForTopic).
	topicSlots map[string][]int

	// episodeQueriesToHit accumulates the attempt count (1..3) of every
	// closed-by-hit episode; episodeUnresolved counts episodes that
	// failed to hit within 3 attempts or were superseded before
	// closure. The test reads these post-run and writes them to the
	// metrics blob — keeping the generator independent of the metrics
	// package.
	episodeQueriesToHit []int
	episodeUnresolved   int

	// userDictatedCount tallies work turns that emitted the Realism C
	// "user-dictated file content" variant — the user prompt itself
	// carrying the literal line being appended to the file. The test
	// reads this post-run and surfaces it in the rung summary as
	// observability for the §3.9 / §3.0 live-window paths the variant
	// exercises.
	userDictatedCount int
}

// appendStep buffers one generated step plus its generator-side
// metadata into the current day, and advances the global step counter.
// It is the on-demand replacement for the build-all generator's
// `g.steps = append(g.steps, step)`.
func (g *generator) appendStep(bs bufStep) {
	g.day.steps = append(g.day.steps, bs)
	g.stepIndex++
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
	return slices.Contains(g.layerB, idx)
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
	sessionStart := g.stepIndex
	emitted := false
	var spent time.Duration
	for spent < active {
		tt := g.sampleTurnType()
		act := g.sampleAction(g.stepIndex)

		// The TimeDelta for this step is whatever gap accumulated
		// before it: the inter-session/overnight/day-off gap (pendingGap)
		// on the first turn after one of those, or the previous turn's
		// sampled intra-turn gap.
		td := g.pendingGap
		g.pendingGap = 0

		bs := g.buildStep(tt, act)
		bs.step.TimeDelta = td
		g.appendStep(bs)
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

// buildStep constructs one buffered step for the given turn type and
// action, mutating the thread model (creating the new thread, updating
// the Layer-B LRU). TimeDelta is filled in by the caller. The returned
// bufStep carries the generator-side metadata (slot index, engaged
// thread index) the refinement loop reads on emission.
func (g *generator) buildStep(tt turnType, act action) bufStep {
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
		cands := g.resumeCandidates(g.stepIndex)
		idx = cands[g.rng.Intn(len(cands))]
	}

	thr := g.threads[idx]
	slot := g.model.slots[thr.slotIdx]

	// Recall-opportunity oracle: a dormant thread (not in Layer B, not
	// the one engaged this turn) bound to the SAME corpus slot is
	// anchored on the slot's NON-LOOSE tags. The engaging turn's symbol
	// set Q is the slot's full UserInput tags (loose cells included);
	// the sibling's symbol set T is the filtered subset that drops loose
	// positions, so the §3.4 Jaccard layer's score is (5-L) / (|T|+L)
	// where L = #loose cells in this slot — a hit when L is small, a
	// probabilistic miss when L is large enough to push the score below
	// the 0.4 threshold (the §2.2 anchor-cap padding from 5-L up to 4
	// inflates the union and makes L≥3 a clean miss). With FamilySize=2
	// there is still at most one such dormant sibling, so the
	// expected-match set is small and well-defined; what changed is
	// that the sibling can now legitimately miss, exercising the
	// miss→refinement loop. Collected in ascending creation order for
	// determinism.
	//
	// New-thread turns suppress the recall-opportunity emission: the
	// engaging userInput on a recall opportunity is the slot's full
	// UserInput (5 #-tags including any loose cells), and on a
	// thread-creation turn that pollutes the new thread's coalesced
	// anchors with the loose terms via §1 symbol extraction — defeating
	// the loose-filter on anchorTags. The dormant first-family-member
	// still surfaces as the sibling on later continue/switch/resume
	// turns; we drop at most one recall opportunity per family pair
	// (the creation turn of the second member), which costs a handful
	// of samples even on the long rungs.
	var recallIDs []string
	if !isNew {
		for _, cand := range g.threads {
			if cand.order == idx || g.inLayerB(cand.order) {
				continue
			}
			if cand.slotIdx == thr.slotIdx {
				recallIDs = append(recallIDs, cand.threadID())
			}
		}
	}

	// Anchors declared in the §5.1 topic tag are ALWAYS the slot's tags
	// FILTERED to drop loose-cell positions, on every step regardless
	// of action. The runtime's §3.3 model-response extractor folds the
	// declared anchors into coalesce, which then feeds (a) the new
	// thread's spine anchors on creation and (b) every engaged thread's
	// `history_symbols` on update. Declaring the filtered list keeps
	// the loose term out of those persistent symbol sets — the
	// asymmetry that lets a same-slot recall query (whose user input
	// brings the loose term in for THAT turn only) sometimes fail to
	// overlap the sibling enough to clear the §3.4 0.4 threshold. A
	// dormant sibling's symbol set thus stabilises at the non-loose
	// substrate, and the recall opportunity's query (Q) differs from
	// the sibling's set (T) by the slot's loose positions.
	anchorTags := nonLooseTags(slot)

	// UserInput: on a recall opportunity, re-issue the slot's mad-libs
	// query verbatim — its #-prefixed tags (loose cells included) drive
	// the runtime's symbol extraction, and the coalesced query set is
	// the full slot.Tags, firing recall against the dormant sibling's
	// filtered anchors. Otherwise a plain engaging line mentioning a
	// couple of the tags so the turn still extracts on-topic symbols —
	// and on the new-thread case the #-prefixed mention must be a
	// NON-LOOSE tag, so the §1 symbol extraction does not slip a loose
	// term into the new thread's spine anchors via coalesce.
	var userInput string
	if len(recallIDs) > 0 {
		userInput = slot.UserInput
	} else {
		mention := firstNonLooseTag(slot)
		second := slot.Tags[1%len(slot.Tags)]
		userInput = fmt.Sprintf("working on #%s and %s", mention, second)
	}

	// Realism C — user-dictated file content. On an *eligible* work
	// turn (non-recall-opportunity, non-new, work-class), draw against
	// userDictatedPct to decide whether this turn is the user-dictated
	// variant. The draw must happen unconditionally inside the
	// eligibility gate so the rng sequence is determined by buildStep's
	// inputs alone — same (Seed, Duration, Corpus) → same draws.
	//
	// Eligibility:
	//   - recall opportunity: UserInput is reserved for the slot's
	//     verbatim mad-libs query; replacing it would break recall
	//     measurement.
	//   - new-thread: user-prompt symbol extraction on a creation turn
	//     feeds the new thread's spine anchors via §3.3 → coalesce, and
	//     embedding a file path / arbitrary literal would pollute those
	//     anchors with non-topical noise.
	//   - non-work: there is no fs.write to share the literal with.
	//
	// What the variant exercises (canonical spec §3.9 paragraph in
	// ARCHITECTURE.md):
	//   (a) user-prompt symbol extraction on file-content literals — the
	//       prompt embeds the file path, which the deterministic pass on
	//       user.prompt extracts as an identifier symbol.
	//   (b) §3.0 transient-data classification — the same prompt
	//       plausibly carries decision-class (high-level #-tag intent)
	//       AND task-class (literal value via the file path arriving
	//       through fs.read/fs.write).
	//   (c) §3.9.2/§3.9.4 live-window — the literal line text appears in
	//       both the user.prompt content and the fs.write content,
	//       exercising dedup across delta sources.
	//
	// The variant flag is consulted again below to align the fs.write
	// modify line with the literal embedded in the prompt — same
	// literal in both deltas is the live-window case.
	userDictated := false
	if tt == turnWork && !isNew && len(recallIDs) == 0 {
		if g.rng.Intn(userDictatedSelector) < userDictatedPct {
			userDictated = true
			mention := firstNonLooseTag(slot)
			// pendingWriteCount is the writeCount the upcoming fs.write
			// will use — workFile + writeCount++ happen later, so peek
			// the current value and add one. This keeps the literal in
			// the prompt byte-identical to what the modify step appends.
			fs := g.workFile(idx)
			pendingWriteCount := fs.writeCount + 1
			userInput = fmt.Sprintf(
				"append this exact line to %s: // edit %d: %s — #%s",
				fs.path, pendingWriteCount, slot.Topic, mention)
			g.userDictatedCount++
		}
	}

	// MockResponse threads: *new-topic* for a new thread, else the
	// engaged thread's thr_N id.
	var threads []string
	if isNew {
		threads = []string{prompt.NewTopicLiteral}
	} else {
		threads = []string{thr.threadID()}
	}
	body := fmt.Sprintf("Working through %s — %s.",
		slot.Topic, strings.Join(slot.Tags[:min(2, len(slot.Tags))], ", "))
	resp := scenarios.NewMockResponseWithTag(threads, anchorTags, body)

	annotation := fmt.Sprintf("turn %d: %s %s (%s)",
		g.stepIndex+1, turnTypeName(tt), actionName(act), slot.Topic)
	if userDictated {
		annotation += " [user-dictated]"
	}
	step := scenarios.Step{
		UserInput:    userInput,
		MockResponse: resp,
		Annotation:   annotation,
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
		// write count so successive writes produce real diffs. On a
		// user-dictated turn the appended line is the literal the user
		// already named in the prompt — the same byte sequence appears in
		// both deltas, exercising the §3.9.2/§3.9.4 live-window dedup
		// path. Otherwise the agentic-edit default line is used.
		fs.writeCount++
		if userDictated {
			fs.content += fmt.Sprintf("\n// edit %d: %s — #%s\n",
				fs.writeCount, slot.Topic, firstNonLooseTag(slot))
		} else {
			fs.content += fmt.Sprintf("\n// edit %d: %s\n",
				fs.writeCount, slot.Topic)
		}
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
	// this turn's engagement. g.stepIndex is this turn's 0-based index.
	g.engage(idx, g.stepIndex)
	return bufStep{step: step, slotIdx: thr.slotIdx, engagedIdx: idx}
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
