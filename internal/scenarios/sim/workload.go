// Package sim is a deterministic, seeded synthetic-workload generator
// for the Personant scenario harness (spec §9.1, §9.4).
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

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/recall/scoring"
	"personant/internal/scenarios"
	"personant/internal/turn"
)

// simRecallThreshold is the minimum symbolic-Jaccard score for the
// shadow-set recall oracle (§4.2) to declare a dormant thread an
// expected match. It is set to the PRODUCTION threshold constant rather
// than a copied literal so the oracle and the runtime's §3.4 layer-1
// matcher cannot silently diverge: if scoring.DefaultThreshold moves,
// the oracle moves with it. This is the one permitted coupling — the
// oracle shares the scoring rule's THRESHOLD and the symbol-set UNION
// DEFINITION (a contract), while re-deriving set membership
// independently from the generator's own emission log.
const simRecallThreshold = scoring.DefaultThreshold

// saltSymbol returns the per-thread unique salt symbol for a thread of
// creation order `order` (§2.2). It is pure and deterministic — derived
// from creation order alone, no rng — so the canonical step stream
// stays a pure function of (Seed, Duration, Corpus).
//
// The salt is emitted as an extra model anchor tag on every emission
// for the thread, so it folds into the runtime's history_symbols and
// the thread's retained-symbol set. Its sole job: inflate each thread's
// retained-set union denominator with a private token so that two
// NON-sibling threads' Jaccard drops below the unrelated-collision
// regime as the population scales — without ever being a match symbol
// (the salt is never placed in a recall query Q). The `t%dz` form is a
// plain lowercase identifier and does NOT trip memops.IsHighSpecificity
// (it is neither a URL, file path, nor SHA-shaped hex), so it carries
// no projection-class boost (design risk #3).
func saltSymbol(order int) string {
	return fmt.Sprintf("t%dz", order)
}

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

	// ContinueWeight / SwitchWeight / ResumeWeight / NewWeight /
	// CampaignWeight bias the per-turn action selection. They need not sum
	// to anything in particular. ResumeWeight drives dormant-thread
	// resumption — a realistic user resumes earlier work far more often than
	// starting fresh, so the seed weights make NewWeight a minority.
	// CampaignWeight drives the lifecycle ground-truth programs (vague /
	// drift / invert); it is modest because each campaign spans many turns
	// and one in flight at a time is enough to keep the lifecycle metrics
	// fed. These are seed values, to be tuned on the rung walk.
	ContinueWeight int
	SwitchWeight   int
	ResumeWeight   int
	NewWeight      int
	CampaignWeight int

	// InterleaveStrength raises unrelated-topic distractor pressure within a
	// session by biasing action selection AWAY from continue (same-thread
	// clustering) and TOWARD switch/new (cross-topic moves): each switch/new
	// weight is scaled up by this integer factor relative to continue. A
	// session therefore mixes unrelated slots rather than dwelling on one
	// topic, stressing the §3.4 recall layer against more same-symbol noise.
	// It deliberately does NOT touch the deterministic corpus binding
	// (slotFor) — the recall oracle keys on slot equality, so steering which
	// SLOT a thread binds to would corrupt the ground truth. 0 → default of
	// 1 (no extra bias). Seed value, tunable on the rung walk.
	InterleaveStrength int

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
	if cfg.CampaignWeight == 0 {
		cfg.CampaignWeight = 20
	}
	if cfg.InterleaveStrength == 0 {
		cfg.InterleaveStrength = 2
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
// rare under Binomial(5, 1/5)) the result is nil.
//
// This is the raw drift set; it is NEVER padded (anchor-lifecycle Inc 5
// deleted the §5.1 4-anchor floor and its stub-padding scaffold). Every
// emission — new-thread, engagement, refinement — carries this set
// verbatim. 0 anchors is legal (vague start); the runtime's projection
// owns the AnchorProjectionMax ceiling and latches EverCentral, so the
// harness never synthesizes anchors. nonLooseTags still models per-turn
// vocabulary drift by dropping the loose cells; the recall gap that drop
// once created is now created by the lifecycle mechanics (drift/invert
// supersession), not by a Jaccard asymmetry the old stub-padding
// preserved.
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
	slotIdx int // index into the corpus slot pool — traj[0], the birth slot

	// cur is the thread's CURRENT corpus slot — the slot whose tags it
	// emits on the next engagement. traj is the ordered visited-slot
	// trajectory (§3.1): traj[0] == slotIdx (the birth slot) and the
	// last element == cur. A monotonic (non-wandering) thread keeps
	// traj == [slotIdx] and cur == slotIdx for life; a wandering thread
	// (§3.2) appends a different-topic slot each time it wanders, so its
	// trajectory length grows (capped at wanderMaxHops) and cur tracks the
	// tail. The shadow retained set (shadowRetainedSet) spans the WHOLE
	// trajectory's emitted tags, so the recall oracle stays trajectory-
	// aware without any per-slot bookkeeping beyond recordEmission.
	cur  int
	traj []int

	// engagementCount is the number of times this thread has been engaged
	// (continue/switch/resume — every buildStep that selects it). It gates
	// wander eligibility (a thread must dwell wanderMinDwell engagements on
	// a topic family before it may wander, §3.2) so a thread does not
	// wander on its very first turns, protecting the family-pair recall
	// window (design risk #5).
	engagementCount int

	// createdAtStep is the global 0-based index of the canonical buffered
	// step that creates this thread (the actNew/campaign/carrier step that
	// first appends it to g.threads). It is the thread's MATERIALIZATION
	// point: the runtime emits the thread's spine record only when that
	// step EXECUTES, but the day-ahead buffer means the generator appends
	// the thread to g.threads at GENERATION time — up to a full day before
	// the creating step runs. The materialization filter (#100) uses this
	// to keep the execution-time refinement oracle from naming a thread the
	// runtime has not yet put on the spine. Set at creation; never mutated.
	createdAtStep int

	// borrowedTags holds the parent-thread tags a SYNTHESIS thread (§2.7.3,
	// #47) carries forward from ≥2 prior threads — a bounded sample of each
	// parent's salient (non-loose) tags. It is emitted as extra model
	// anchors on the synthesis thread's CREATION turn only (buildStep
	// appends it to anchorTags once, then clears it), so the runtime
	// accretes the borrowed symbols into the synthesis thread's
	// history_symbols and the shadow retained set mirrors that accretion
	// (recordEmission). nil for an ordinary (non-synthesis) thread, which
	// is the common case. The borrow DILUTES single-parent recall queries
	// (another symbolic-misses/embedding-recovers candidate, like a
	// wanderer) but stays bounded so it never manufactures a false match —
	// see synthesisParentTagSample.
	borrowedTags []string
}

// newThread constructs a thread bound to slotIdx as its birth slot. It
// seeds the trajectory state (§3.1) so cur == traj[0] == slotIdx — a
// thread starts monotonic and stays so until it wanders. slotIdx is kept
// as the back-compat birth-slot field for annotations and the §3.9 file
// binding (which keys off the birth topic, not the wandered topic).
func newThread(order, slotIdx int) thread {
	return thread{
		order:   order,
		slotIdx: slotIdx,
		cur:     slotIdx,
		traj:    []int{slotIdx},
	}
}

// createThread appends a new thread of the given creation order bound to
// slotIdx and returns its index. It is the single creation site: it
// stamps createdAtStep with the global index of the step currently being
// built (g.stepIndex — appendStep increments it only after buildStep
// returns), so every thread records the execution position at which the
// runtime materializes its spine record. Pure (no rng); the canonical
// step stream stays a function of (Seed, Duration, Corpus).
func (g *generator) createThread(order, slotIdx int) int {
	t := newThread(order, slotIdx)
	t.createdAtStep = g.stepIndex
	g.threads = append(g.threads, t)
	// A thread creation is a material structural change for #42 (a new
	// spine record the runtime commits). Tallied per sim-day in
	// g.dayStructuralChanges, flushed once per day in generateNextDay.
	// createThread is the single creation site (actNew, campaign, carrier),
	// so this counts every materialized thread exactly once. Synthesis adds
	// its own increment in maybeSynthesize (the borrow is an extra event).
	g.dayStructuralChanges++
	return order
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
	// actCampaign advances the generator's active lifecycle campaign —
	// the vague-new / drift / invert ground-truth program (anchor-lifecycle
	// Inc 5). One actCampaign draw services one campaign turn; the campaign
	// state machine (campaign.go region below) owns the dedicated thread and
	// the phase progression. When no campaign is active the draw starts the
	// next one (kind rotates deterministically); buildStep dispatches it
	// before the ordinary action handling.
	actCampaign
)

// campaignKind identifies which lifecycle program a campaign runs.
type campaignKind int

const (
	// campaignVague creates a thread with 0 anchors for vagueTurns turns,
	// then begins accreting its bound slot's tags. It is recall-unmatchable
	// until accretion makes its first symbol central; the generator records
	// the turn it becomes matchable (the actVagueNew oracle).
	campaignVague campaignKind = iota
	// campaignDrift walks a thread's emitted symbols origin→destination over
	// driftTurns turns: it stops re-emitting the origin set and emits a fresh
	// destination set every turn, climbing the destination Counts until the
	// origin symbols are outranked out of the top-AnchorProjectionMax
	// projection (Lifecycle=superseded, EverCentral retained). The origin set
	// stays matchable via history_symbols (drift_recall_origin); the
	// destination set is the active projection (drift_recall_dest).
	campaignDrift
	// campaignInvert is drift's premise-inversion sibling: a high-Count
	// discovery (destination) set outranks the original anchors so the
	// original premise supersedes while EverCentral stays true. An
	// abandoned-premise query for the original set must still surface the
	// thread (abandoned_premise_recall).
	campaignInvert
)

// Campaign-phase turn budgets. Deliberately generous so the destination
// Counts climb well past the origin Counts and the origin set is reliably
// outranked out of the top-AnchorProjectionMax projection (origin symbols
// keep their creation-turn Count while each destination turn increments the
// destination Counts). Seed values, tunable on the rung walk.
const (
	vagueTurns  = 4  // turns a vague thread stays 0-anchor before accreting
	driftTurns  = 12 // origin→destination walk length
	invertTurns = 12 // discovery-set inversion length
)

// Within-thread wander tuning (§3.2). A fraction of normal threads
// carry a multi-topic trajectory, generalizing the campaign drift
// mechanic: on each eligible engagement a thread may wander to a
// different-topic slot, superseding its earlier topics. These are seed
// values tuned on the rung walk (design risk #2 — too low loses
// non-monotonicity, too high blows past the eviction cap).
const (
	// wanderPct / wanderSelector set the per-eligible-engagement wander
	// probability: g.rng.Intn(wanderSelector) < wanderPct fires the
	// wander. 18/100 ≈ the campaign drift cadence (§3.2). The draw is made
	// UNCONDITIONALLY inside the eligibility gate (maybeWander) so the rng
	// order stays input-determined — same discipline as maybeUserDictated.
	wanderPct      = 8
	wanderSelector = 100

	// wanderMinDwell is the minimum engagement count a thread must reach on
	// a topic before it may wander (§3.2). It protects the family-pair
	// recall window (design risk #5): the dormant sibling's recall
	// opportunity fires on early turns before either family member has
	// dwelt long enough to wander off its birth slot.
	wanderMinDwell = 3

	// wanderMaxHops caps a thread's trajectory length (§3.2): a thread
	// accretes at most this many distinct topical slots over its life, so
	// its retained-symbol set stays bounded (≈ wanderMaxHops×~5 tags + 1
	// salt ≈ 21) and well under the 40-symbol eviction cap — the
	// no-eviction precondition the oracle's coherence rests on (§4.3,
	// criterion (e)).
	wanderMaxHops = 4

	// wanderProbeEvery is the emitted-step cadence at which buildStep
	// injects an abandoned-topic probe (criterion (b)) when an eligible
	// wandering dormant thread exists. The probe is a measurement bucket,
	// not an episode-gated recall opportunity: it issues a query on one of
	// the probed thread's EARLIER (abandoned) trajectory slots and records
	// whether the runtime surfaced the thread, bucketed by hop distance —
	// recording a predicted+observed MISS at high hops as data, never
	// suppressing it. It is a deterministic, rng-free step (pure from
	// generator state), so it is part of the canonical stream and the
	// determinism guard stays byte-identical.
	//
	// DOCUMENTED CHOICE (the design left probe cadence open, §6/risks): a
	// modest cadence (every ~12 emitted steps) yields a few hundred probe
	// observations across the rungs — enough to populate every hop bucket
	// (1..wanderMaxHops-1) without materially shifting the action mix or
	// the core recall-fidelity statistics, which the probe is interleaved
	// alongside (it does not replace an ordinary turn — see runSession).
	wanderProbeEvery = 12
)

// Synthesis-thread tuning (§2.7.3, #47/#42). A fraction of new-thread
// creations are SYNTHESIS threads: a fresh framing that borrows a
// bounded sample of salient tags from ≥2 prior threads, modelling "new
// thread as synthesis of multiple prior threads." The borrowed tags
// dilute single-parent recall queries exactly as a wanderer does — a
// real symbolic-misses/embedding-recovers signal, not a manufactured
// match. These are seed values, tunable on the rung walk.
const (
	// synthesisPct / synthesisSelector set the per-new-thread synthesis
	// probability: g.rng.Intn(synthesisSelector) < synthesisPct converts
	// an eligible actNew creation into a synthesis. The draw is made
	// UNCONDITIONALLY whenever actNew fires (same discipline as
	// maybeWander), so the rng draw ordering stays a pure function of
	// (Seed, Duration, Corpus). Modest (≈ wanderPct scale) so synthesis
	// is a steady minority of creations, not the common case.
	synthesisPct      = 8
	synthesisSelector = 100

	// synthesisParents is K, the number of prior threads a synthesis
	// borrows from. Fixed at 2 for now (the minimal "multiple" the #47
	// realism element names); the synthesis_parents histogram records it
	// per event so a future distribution is visible without a code
	// change. Eligibility requires at least this many candidate parents
	// in the dormant/materialized/non-carrier pool.
	synthesisParents = 2

	// synthesisParentTagSample caps how many non-loose tags a synthesis
	// borrows from EACH parent's current slot. Bounded so the synthesis
	// thread's emitted set stays well under the 40-symbol eviction cap
	// (freshSlot ≈5 + K×sample + salt = 5 + 2×2 + 1 = 10 ≪ 40), the
	// no-eviction precondition the oracle's coherence rests on (§4.3).
	// Bounded also keeps any single-parent query's Jaccard against the
	// synthesis set modest (intersection ≤ sample against the ~10-symbol
	// union → ≈0.15 ≪ the 0.4 threshold), so the borrow DILUTES without
	// manufacturing a false expected-match the runtime would miss
	// (coherence holds; unexplained_absence stays 0).
	synthesisParentTagSample = 2
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
		files:              map[int]*fileState{},
		recallBuckets:      map[string]*recallTally{},
		emittedSyms:        map[int]map[string]struct{}{},
		carrierIdx:         map[int]struct{}{},
		carrier:            -1,
		wanderHopHits:      map[int]int{},
		wanderHopTotal:     map[int]int{},
		wanderHopCoherent:  map[int]int{},
		wanderHopDiverge:   map[int]int{},
		wanderHopEmbedHits: map[int]int{},
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

	// recallBucket names the lifecycle metric this step's recall outcome
	// feeds, or "" for an ordinary (non-campaign) step. The generator reads
	// it back off the just-run step's StepFeedback (RecallMatchFires vs
	// RecallExpected) and tallies a hit/total into the named bucket — the
	// drift_recall_origin / drift_recall_dest / abandoned_premise_recall
	// oracles. Buckets are exposed post-run the way episodeQueriesToHit is.
	recallBucket string

	// suppressEpisode is set on a campaign recall step: the step still
	// declares ExpectedRecallMatches (so the harness records match-fires and
	// the feedback carries them for bucketing), but it must NOT open a
	// miss→refinement episode. Refinement re-engages layerB[0] with a
	// sibling slot, which would entangle and corrupt the campaign's
	// controlled origin/dest measurement.
	suppressEpisode bool

	// vagueCampaignTurn is the 1-based campaign-turn index of a vague
	// campaign's recall step, used to record the first matchable turn for
	// the actVagueNew oracle. 0 when not a vague recall step.
	vagueCampaignTurn int

	// probe carries the hop-graded abandoned-topic probe metadata (§3.2,
	// criterion (b)) when this step is a wander probe; nil otherwise. The
	// generator reads it back off the just-run step's StepFeedback to bucket
	// the observation by hop distance and detect oracle/runtime divergence.
	probe *wanderProbe
}

// wanderProbe is the per-step metadata of an abandoned-topic probe. The
// probe issues a query on the probed thread's EARLIER (abandoned)
// trajectory slot and is scored by hop distance — how far the queried
// slot sits behind the thread's current slot in its trajectory.
type wanderProbe struct {
	threadID   string // the probed (wandering, dormant) thread's runtime id
	hops       int    // current-traj-index − queried-slot-traj-index (>=1)
	predictHit bool   // oracle prediction: simJaccard(Q, retained) >= threshold
}

// dayBuf is one generated calendar day's steps plus the global step
// index of its first step, so a global step index can be translated
// into an offset within steps.
type dayBuf struct {
	steps     []bufStep
	firstStep int // global step index of steps[0]
}

// dayHasTurn reports whether the day buffered at least one real turn — any
// step that is not a no-turn SleepCycle marker (#108). Used to keep the
// day-off (sleep-only) from injecting a spurious 0 into the per-day
// structural-change distribution.
func dayHasTurn(day dayBuf) bool {
	for _, bs := range day.steps {
		if !bs.step.SleepCycle {
			return true
		}
	}
	return false
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
	// Tally the just-run step into its lifecycle recall bucket, if any. The
	// hit predicate matches the episode loop's: observed match-fires meet
	// the FORGIVEN expected count (archived/absent-from-spine expectations
	// removed, exactly as the F1 path forgives them), not the raw declared
	// count — counting an archived sibling the runtime cannot surface as a
	// miss would measure oracle staleness, not recall. A zero-feedback
	// drainSteps run reports RecallExpected==0, so the bucket total only
	// advances under a real run — the buckets are observability, not part
	// of the canonical stream.
	if g.lastBucket != "" && feedback.RecallExpected > 0 {
		tally := g.recallBuckets[g.lastBucket]
		if tally == nil {
			tally = &recallTally{}
			g.recallBuckets[g.lastBucket] = tally
		}
		tally.total++
		if feedback.RecallMatchFires >= feedback.RecallExpectedForgiven {
			tally.hits++
			if g.lastVagueTurn > 0 && g.vagueArmed {
				// Record only the FIRST matchable turn of this vague
				// campaign; disarm so later accretion hits do not re-record.
				g.vagueMatchTurns = append(g.vagueMatchTurns, g.lastVagueTurn)
				g.vagueArmed = false
			}
		}
	}
	g.lastBucket = ""
	g.lastVagueTurn = 0

	// Tally the just-run abandoned-topic probe (§3.2, criterion (b)), if
	// any. observedHit asks whether the RUNTIME surfaced the SPECIFIC probed
	// thread — checking its id against the match-fire id set, not just the
	// count, since a non-target collision must not be miscounted as a hit.
	// The probe declares no ExpectedRecallMatches (so RecallExpected==0),
	// so the gate is feedback.Index >= 0: a real run reports the step index,
	// while the zero-feedback drainSteps path reports Index==-1 and tallies
	// nothing — the buckets are observability, never part of the canonical
	// stream.
	if g.lastProbe != nil && feedback.Index >= 0 &&
		(feedback.TargetRecoverable == nil || feedback.TargetRecoverable(g.lastProbe.threadID)) {
		// Archival forgiveness (mirrors the F1/episode path): if the probed
		// target has been archived off the spine, the runtime can never fire
		// it again, so the oracle's predict-hit is not a genuine divergence —
		// it is the oracle not modeling archival. Drop the observation
		// entirely rather than scoring it as a divergence (measurement-side
		// only; the canonical step stream is untouched, so determinism holds).
		p := g.lastProbe
		observedHit := slices.Contains(feedback.RecallMatchFireIDs, p.threadID)
		// Embedding-layer observation (#98 head-to-head): did the EMBEDDING
		// layer surface this specific probed thread? Shares the symbolic
		// layer's per-hop denominator (wanderHopTotal, bumped below), so the
		// two per-hop recall curves are directly comparable. nil on the mock
		// path (EmbedMatchFireIDs nil) → no embed hits recorded.
		embedHit := slices.Contains(feedback.EmbedMatchFireIDs, p.threadID)
		if embedHit {
			g.wanderHopEmbedHits[p.hops]++
		}
		if p.hops == 0 {
			// Current-topic control (wander_current_recall). At
			// superseded-weight 1.0 the runtime scores against the FULL
			// retained union (active ∪ superseded, §4.3), so a DEEP-wander
			// thread's current 5 tags are diluted below threshold even on its
			// own current topic — the oracle predicts that same miss, so it
			// stays COHERENT. Track coherence here too (hop 0 in the
			// divergence maps) so the assertion covers the control.
			g.wanderCurrentTotal++
			if observedHit {
				g.wanderCurrentHits++
			}
			g.wanderHopTotal[0]++
			if observedHit {
				g.wanderHopHits[0]++
			}
			if p.predictHit == observedHit {
				g.wanderHopCoherent[0]++
			} else {
				g.wanderHopDiverge[0]++
			}
		} else {
			// Abandoned-topic hop bucket: record the observation (hit OR
			// predicted+observed miss alike — never suppressed) and the
			// oracle/runtime coherence at this hop. Divergence (predict !=
			// observe) is the criterion (b) FAILURE; the decay (both
			// predicting miss at high hops) is the expected deliverable.
			g.wanderHopTotal[p.hops]++
			if observedHit {
				g.wanderHopHits[p.hops]++
			}
			if p.predictHit == observedHit {
				g.wanderHopCoherent[p.hops]++
			} else {
				g.wanderHopDiverge[p.hops]++
			}
		}
	}
	g.lastProbe = nil

	// Process the just-completed step's outcome. The hit predicate uses
	// the FORGIVEN expected count: archived / absent-from-spine
	// expectations are removed (mirroring the F1/precision path in
	// recordRecallFidelity), so an episode whose expected set is ENTIRELY
	// archived (RecallExpectedForgiven == 0) closes as a HIT — there is
	// nothing recoverable to find — rather than missing through all 3
	// refinement attempts and inflating the unresolved rate in lockstep
	// with archival. The episode-open gate stays keyed on the raw declared
	// count (this WAS a recall opportunity), keeping the canonical step
	// stream a pure function of (Seed, Duration, Corpus); only this
	// measurement-side comparison forgives.
	if g.recallEpisodeOpen && feedback.RecallExpected > 0 {
		switch {
		case feedback.RecallMatchFires >= feedback.RecallExpectedForgiven:
			// HIT (or nothing recoverable) — close and record queries-to-hit.
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

	// Arm the lifecycle-bucket feedback read for the NEXT Next() call. A
	// campaign recall step carries a bucket name and (for vague) the
	// campaign-turn index; the outcome is tallied when its feedback arrives.
	g.lastBucket = bs.recallBucket
	g.lastVagueTurn = bs.vagueCampaignTurn
	g.lastProbe = bs.probe

	// If this buffered step opens a new recall opportunity, start an
	// episode — UNLESS it is a campaign step (suppressEpisode), whose
	// controlled origin/dest measurement must not be entangled with the
	// miss→refinement loop (refinement would re-engage layerB[0] with a
	// sibling slot). A new opportunity supersedes any still-open prior
	// episode (record the prior as unresolved so it is not silently lost).
	if len(bs.step.ExpectedRecallMatches) > 0 && !bs.suppressEpisode {
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
	thr := g.threads[idx]

	// The refining thread emits the new slot's non-loose tags plus its own
	// salt as model anchors, and its user prompt is the slot's mad-libs
	// query (slot.UserInput, mentioning every tag including loose cells).
	// Record the FULL extracted set (user #-tags ∪ model anchors) so the
	// shadow mirrors the runtime's accretion exactly (§4.2 coherence).
	anchorTags := append(nonLooseTags(slot), saltSymbol(thr.order))
	g.recordEmission(idx, extractedSymbolsFor(slot.UserInput, anchorTags))

	// Recall oracle for the refinement: the shadow-set Jaccard rule against
	// the new slot's query (same rule as canonical buildStep — DRY). Q
	// mirrors the runtime's coalesced query set: the slot tags PLUS the
	// engaged (refining) thread's salt, which it emits as a model anchor
	// this turn (see buildStep's Q note).
	Q := append(append([]string(nil), slot.Tags...), saltSymbol(thr.order))
	// #100: the refinement is injected at EXECUTION time, so its expected
	// set must consider only threads the runtime has already materialized
	// on the spine — those whose creating step has executed. Pass the
	// executed-step count as the materialization cutoff; a thread from the
	// day-ahead generation buffer whose creating step has not yet run is
	// excluded, preventing the off-spine-not-archived false miss.
	recallIDs := g.recallExpectedForMaterialized(idx, Q, false, g.executedStepCount())

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
			anchorTags,
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

	// Flush the day's material-structural-change tally (#42). Only a day
	// that emitted TURNS contributes a sample — a day off creates no
	// threads and would otherwise inject a spurious 0 into the
	// distribution. A day-off now buffers a single SleepCycle marker step
	// (#108), which is not a turn, so gate on "has a non-sleep step"
	// rather than "has any step." Reset the accumulator regardless so the
	// next day starts clean.
	if dayHasTurn(day) {
		g.structuralChangesPerDay = append(g.structuralChangesPerDay, g.dayStructuralChanges)
	}
	g.dayStructuralChanges = 0

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
//
// The day-off idle window is also where the ARCHITECTURE.md sleep cycle
// runs (#108): runDayOff appends one deterministic SleepCycle marker step
// (no turn, no LLM consult). The harness intercepts the marker and drives
// h.Ops.Consolidate (substrate gc) — the generator is a stateless
// StepSource and cannot reach Ops, so the marker is the seam. The marker
// fires every day-off and carries no seed-dependent payload, so it does
// not perturb the (Seed, Duration) determinism contract.
func (g *generator) runDayOff() {
	g.simNow -= g.pendingGap // discard any pending intra-day gap
	off := jitter(g.rng, g.cfg.DayOffGap)
	g.simNow += off
	g.pendingGap = off

	g.appendStep(bufStep{step: scenarios.Step{
		SleepCycle: true,
		Annotation: fmt.Sprintf("day %d: sleep-cycle (day-off consolidation)", g.dayIndex+1),
	}})
}

// layerBCap mirrors memops.DefaultBTopK (spec §2.6.1 layer.b-top-k):
// the runtime keeps the 3 most-recently-engaged threads in Layer B,
// fully present in the working set. A topic tag naming a thr_N NOT in
// Layer B triggers the §5.5 mid-turn fetch + re-prompt. The generator
// models Layer B so it can distinguish a `switch` (warm, in-Layer-B,
// no fetch) from a `resume` (dormant, out of Layer B, triggers the
// fetch) — and so the recall oracle can exclude resident threads.
const layerBCap = 3

// Within-session interleaving metric keys (SPEC §9.1 "too coherent"
// concern). Defined here so the generator data, the metrics fold, and
// the summary share one source of truth. Each is a histogram series with
// one sample per session (per maximal dwell run for the dwell key).
const (
	metricSessionDistinctSlots   = "workload_session_distinct_slots"
	metricSessionDistinctThreads = "workload_session_distinct_threads"
	metricSessionTurns           = "workload_session_turns"
	metricTopicDwellRunlen       = "workload_topic_dwell_runlen"
)

// Within-thread wander metric keys (§3.2/§5, criteria (a)/(b)). Defined
// here so the generator, the metrics fold, and the rung summary share one
// source of truth.
const (
	// metricThreadTopicsDistinct is a histogram with one sample per thread:
	// the count of distinct corpus SLOTS in its trajectory at run end
	// (proves non-monotonicity — mean > 1, tail to wanderMaxHops, criterion
	// (a)). metricThreadWanderHops is the trajectory LENGTH distribution
	// (== topics-distinct here, since nextWanderSlot always moves to a
	// new-topic slot, but kept distinct in case a future corpus allows a
	// repeat slot in a trajectory).
	metricThreadTopicsDistinct = "workload_thread_topics_distinct"
	metricThreadWanderHops     = "workload_thread_wander_hops"

	// metricWanderCurrentRecall is the current-topic control: a wandering
	// thread queried on its CURRENT slot must surface (≥~0.95).
	metricWanderCurrentRecall = "wander_current_recall"

	// metricWanderOriginRecallByHops, metricWanderCoherenceByHops, and
	// their _obs companions are PER-HOP gauges (one gauge per hop distance,
	// keyed metricWanderOriginRecallByHops+"_h<N>"). origin_recall is the
	// observed abandoned-topic recall at hop N (expected to DECAY:
	// hit at 1-2 hops, miss at >=3); coherence is the oracle/runtime
	// agreement rate at hop N (the PASS condition — must be ~1.0 at every
	// hop; divergence is the failure, not the decay).
	metricWanderOriginRecallByHops = "wander_origin_recall_byhops"
	metricWanderCoherenceByHops    = "wander_coherence_byhops"

	// metricWanderEmbedRecallByHops is the EMBEDDING-layer per-hop recall of
	// the abandoned-topic probe (#98 head-to-head): the fraction of probes at
	// hop N for which the embedding layer surfaced the probed thread. Shares
	// the symbolic layer's per-hop denominator (one observation per probe), so
	// comparing it against metricWanderOriginRecallByHops at the same hop is
	// the gap-closure view — does embedding recover origin/abandoned topics at
	// hops where symbolic decays to ~0. Populated only on an embedding-in-loop
	// run; absent on the mock acceptance run.
	metricWanderEmbedRecallByHops = "wander_embed_recall_byhops"

	// metricWanderCoherenceDivergence is the run-total count of
	// oracle/runtime divergences across all hops — the headline criterion
	// (b) tripwire. Zero (or a tiny rounding band) is the pass; any real
	// divergence means the §4.3 eviction-free coherence assumption broke.
	metricWanderCoherenceDivergence = "wander_coherence_divergence"
)

// Synthesis-thread metric keys (§2.7.3, #47/#42). Defined here so the
// generator, the metrics fold (recordWanderMetrics), and the rung summary
// share one source of truth.
const (
	// metricSynthesisEvents is the run-total count of new-thread creations
	// converted into synthesis threads (a counter).
	metricSynthesisEvents = "workload_synthesis_events"

	// metricSynthesisParents is a histogram with one sample per synthesis
	// event: the parent count (= synthesisParents today). Recorded as a
	// distribution so a future variable-K synthesis is visible without a
	// metric change.
	metricSynthesisParents = "synthesis_parents"

	// metricSynthesisBorrowedSymbols is the run-total count of borrowed
	// parent tags blended into synthesis threads — the sim-side
	// observability for the derived_from realism element. NOTE (§2.7.3
	// Part-2 finding): the sim's decline-all RecallAck means production
	// never promotes a parent into recallSurfaced ∩ Layer-B, so it does
	// NOT stamp derived_from end-to-end on these turns; this counter
	// measures the workload's borrow volume, not a production stamp. The
	// production rule is covered directly by Inc B unit tests.
	metricSynthesisBorrowedSymbols = "synthesis_derived_from_symbols"

	// metricMaterialStructuralChangesPerDay is a histogram with one sample
	// per sim-DAY: the count of material structural changes the generator
	// emitted that day — thread creations + synthesis events + thread
	// closures/archivals — the substrate events #42 reads as
	// commits-worthy-per-sim-day. It COMPOSES the generator's existing
	// structural counters rather than introducing a parallel tally.
	metricMaterialStructuralChangesPerDay = "material_structural_changes_per_simday"
)

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

	// emittedSyms[idx] is the generator's independent shadow of thread
	// idx's retained symbol set: the union of every model anchor tag the
	// generator has emitted for that thread (topical slot tags + the
	// thread's salt). It is the §4.2/§4.3 "shadow retained set" the recall
	// oracle scores against. Because the runtime's recall match set is the
	// eviction-free union (anchors ⊆ history_symbols) and the generator's
	// threads stay far under the 40-symbol eviction cap, this union equals
	// the runtime's T(thr) by construction — no projection ranking or
	// active/superseded labeling is needed at the default superseded-weight
	// of 1.0 (§4.3). It is advanced only by recordEmission (pure, no rng),
	// so it does not affect the canonical step stream's determinism.
	//
	// For increment 1 (monotonic threads) a thread's set is just
	// {nonLooseTags(slot)} ∪ {salt}; the per-thread accumulator is built as
	// a general union so increment 2's wander accretion extends it without
	// changing the oracle.
	emittedSyms map[int]map[string]struct{}

	// carrierIdx is the set of creation-order indices of DEDICATED
	// MEASUREMENT-CARRIER threads — synthetic threads created solely to
	// host an abandoned-topic probe or campaign-recall query (#96
	// increment 2). A measurement query is issued on a carrier so the
	// §3.4 recall scan can surface the genuine target/campaign thread
	// (the carrier is the engaged thread, excluded from its own turn's
	// scan); but the query's #-tags PERMANENTLY accrete into the engaged
	// thread's history_symbols, so engaging a real recall-candidate
	// thread would inflate its retained set with foreign measurement
	// symbols — a MEASUREMENT ARTIFACT that, on a popular resident reused
	// as carrier many times, blew the retained set past the 40-symbol
	// eviction cap and broke the oracle's no-eviction coherence (the
	// criterion (e)/(b) failure this increment fixes).
	//
	// A carrier absorbs that pollution instead: it is excluded from the
	// recall-oracle candidate scan (recallExpectedFor) and from
	// probeTarget on BOTH sides, and from the criterion (e) shadow
	// max-retained measurement, so its bloated retained set can neither be
	// scored by the oracle nor counted against the cap. Runtime-side a
	// heavily-used carrier's history is diluted so far (its ~5-symbol-query
	// Jaccard denominator is the full ~40-symbol evicted union → ≤0.125 ≪
	// the 0.4 threshold) that it cannot fire as a recall match, so it never
	// surfaces as a false collision either. carrierIdx membership is the
	// single source of truth for all these exclusions.
	carrierIdx map[int]struct{}

	// carrier is the creation-order index of the lone dedicated carrier
	// thread, or -1 until first use. One reused carrier (rather than a
	// fresh thread per measurement) is deliberate: it bloats past the
	// recall-firing dilution floor within its first handful of distinct
	// query topics and stays runtime-invisible to recall thereafter, so
	// the only window in which it could collide with a real query is its
	// first few uses — measured, not assumed (criterion (b) divergence).
	carrier int

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

	// Synthesis-thread telemetry (§2.7.3, #47/#42). synthesisEvents counts
	// new-thread creations converted into synthesis threads;
	// synthesisParentCounts records the parent count of each such event
	// (the synthesis_parents histogram source — = synthesisParents today,
	// recorded so a future distribution is visible). synthesisParentCursor
	// round-robins the deterministic parent pick across the eligible pool
	// so synthesis samples a spread of parents, not always the lowest
	// orders. synthesisBorrowedSymbols totals the borrowed parent tags
	// blended into synthesis threads — the sim-side observability for the
	// derived_from realism element (see the Part-2 finding in §2.7.3: the
	// sim's decline-all RecallAck means production never stamps it
	// end-to-end here). All advance only on actNew synthesis conversion;
	// the gate draw keeps them a pure function of (Seed, Duration, Corpus).
	synthesisEvents          int
	synthesisParentCounts    []int
	synthesisParentCursor    int
	synthesisBorrowedSymbols int

	// Material-structural-change-per-sim-day telemetry (#42). dayStructural-
	// Changes accumulates the current day's material structural changes
	// (thread creations + synthesis events — the substrate events the
	// GENERATOR knows it drives); generateNextDay flushes it into
	// structuralChangesPerDay (one sample per sim-day) and resets it. The
	// test folds the slice into the metricMaterialStructuralChangesPerDay
	// histogram post-run. Runtime-owned events (closures/archivals) are not
	// counted here — the generator cannot observe them; #42 reads those off
	// the substrate directly. Draws no rng (pure bookkeeping).
	dayStructuralChanges    int
	structuralChangesPerDay []int

	// campaign is the in-flight lifecycle program (vague / drift / invert),
	// or nil when none is running. One runs at a time: an actCampaign draw
	// starts it if nil and advances it otherwise; it self-clears on
	// completion and the next actCampaign draw starts the next kind in
	// rotation. nextCampaignKind is the kind the next start will use.
	campaign         *campaign
	nextCampaignKind campaignKind

	// lastBucket is the recallBucket of the most-recently-EMITTED buffered
	// step (set in Next when a bufStep is handed out). The next Next() call
	// reads it against that step's StepFeedback to tally a hit/total into
	// the named lifecycle bucket. Refinement steps never set it.
	lastBucket string

	// lastVagueTurn carries the most-recently-emitted vague campaign recall
	// step's campaign-turn index (0 when not such a step). On the next
	// Next()'s feedback, a hit records this turn into vagueMatchTurns — the
	// turn the vague thread first became matchable. vagueArmed gates that to
	// the FIRST matchable hit per campaign (set when the campaign starts
	// accreting, cleared on the first recorded match).
	lastVagueTurn int
	vagueArmed    bool

	// Within-thread wander hop-graded probe state (§3.2, criterion (b)).
	//   - emittedSinceProbe counts emitted steps since the last probe, the
	//     deterministic cadence counter (no rng).
	//   - probeHopCursor round-robins which abandoned hop distance the next
	//     probe targets, so all hop buckets fill over the run.
	//   - lastProbe is the most-recently-EMITTED probe's metadata, read back
	//     on the next Next()'s feedback to bucket the observation.
	//   - wanderHopHits/wanderHopTotal tally per-hop observed recall
	//     (RUNTIME surfaced the thread). wanderHopCoherent/wanderHopDiverge
	//     tally per-hop oracle/runtime agreement: divergence (oracle
	//     predicted hit but runtime missed, or vice-versa) is the criterion
	//     (b) FAILURE; the decay itself (both predicting miss at high hops)
	//     is the expected, correct deliverable. wanderCurrentHits/Total tally
	//     the current-topic control probe (hop 0 ≈ should always surface).
	emittedSinceProbe  int
	probeHopCursor     int
	probeTargetCursor  int
	lastProbe          *wanderProbe
	wanderHopHits      map[int]int
	wanderHopTotal     map[int]int
	wanderHopCoherent  map[int]int
	wanderHopDiverge   map[int]int
	wanderCurrentHits  int
	wanderCurrentTotal int

	// Embedding-layer per-hop probe hits (#98 head-to-head). Parallel to
	// wanderHopHits but counting whether the EMBEDDING layer
	// (spine.embed-match-fire) surfaced the probed thread, so the rung
	// summary can show whether embedding recovers origin/abandoned topics at
	// hop distances where symbolic decays to ~0 — the gap-closure view. Keyed
	// by hop (0 = current-topic control). Populated only on an embedding-in-
	// loop run (feedback carries EmbedMatchFireIDs); nil/zero otherwise, so
	// the mock acceptance run is unaffected. The total per hop is the SAME
	// wanderHopTotal the symbolic layer uses (one observation per probe), so
	// the two layers' per-hop recall share one denominator — apples-to-apples.
	wanderHopEmbedHits map[int]int

	// recallBuckets accumulates per-bucket {hits, total} recall tallies for
	// the lifecycle oracles (drift_recall_origin, drift_recall_dest,
	// abandoned_premise_recall). A step counts toward `total` once its
	// feedback arrives; toward `hits` when RecallMatchFires >=
	// RecallExpectedForgiven (the same forgiven hit predicate the episode
	// loop uses — archived expectations are not counted as misses). The test reads these
	// post-run and writes the ratios into the metrics blob, keeping the
	// generator metrics-package-free.
	recallBuckets map[string]*recallTally

	// vagueMatchTurns records, per completed vague campaign, the campaign
	// turn index at which the vague thread first became recall-matchable
	// (its first emitted recall opportunity that the runtime hit). The
	// oracle: a vague thread is unmatchable for its first vagueTurns turns
	// (0 anchors → empty match set) and matchable only after accretion.
	// (ever-central / history-length / superseded-symbol counts are folded
	// by the test from post-run frontmatter, not tracked on the generator.)
	vagueMatchTurns []int

	// Within-session interleaving telemetry — pure post-emission
	// bookkeeping accumulated from each session's emitted bufSteps
	// (slotIdx / engagedIdx). These draw no rng and do not influence
	// control flow; the test folds them into the metrics blob post-run
	// (SPEC §9.1 "too coherent" concern, measured rather than asserted).
	// One sample per session per series, plus one dwell sample per
	// maximal same-engagedIdx run.
	sessionDistinctSlots   []int // distinct slotIdx touched per session
	sessionDistinctThreads []int // distinct engagedIdx touched per session
	sessionTurns           []int // emitted-step count per session
	topicDwellRuns         []int // length of each maximal same-engagedIdx run

	// Per-session interleaving accumulators, reset at runSession start.
	sessSlots     map[int]struct{}
	sessThreads   map[int]struct{}
	sessTurnCount int
	dwellPrev     int  // engagedIdx of the current dwell run
	dwellLen      int  // length of the current dwell run
	dwellActive   bool // a run is in progress (>=1 step seen this session)
}

// recallTally is a {hits, total} pair for one lifecycle recall bucket.
type recallTally struct {
	hits, total int
}

// Recall bucket names — the lifecycle oracles fed off per-step feedback.
const (
	bucketDriftOrigin      = "drift_recall_origin"
	bucketDriftDest        = "drift_recall_dest"
	bucketAbandonedPremise = "abandoned_premise_recall"
)

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
		if g.inLayerB(idx) || g.isCarrier(idx) {
			// A measurement carrier is never a resume target; skipping it also
			// guards the lastEngagedTurn index, which is never extended for a
			// carrier (it is not run through g.engage).
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
	g.beginInterleaveSession()
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
		g.observeInterleave(bs)
		emitted = true

		// Abandoned-topic probe (criterion (b)): on the probe cadence,
		// inject a hop-graded measurement step if an eligible wandering
		// dormant thread exists. The probe is rng-FREE (pure from generator
		// state) and carries a zero TimeDelta — it is issued at the same
		// simulated instant as the step it follows and does NOT count toward
		// `spent` or advance the clock, so it perturbs neither the session's
		// rng draw order nor its length. The canonical stream therefore stays
		// byte-identical at a fixed seed (determinism guard) while the probe
		// observations accumulate.
		g.emittedSinceProbe++
		if g.emittedSinceProbe >= wanderProbeEvery {
			if pbs, ok := g.buildWanderProbeStep(); ok {
				g.emittedSinceProbe = 0
				pbs.step.TimeDelta = 0
				g.appendStep(pbs)
				g.observeInterleave(pbs)
			}
		}

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
		g.finalizeInterleaveSession()
	}
}

// beginInterleaveSession resets the per-session interleaving accumulators.
// Called once at the top of every runSession.
func (g *generator) beginInterleaveSession() {
	g.sessSlots = map[int]struct{}{}
	g.sessThreads = map[int]struct{}{}
	g.sessTurnCount = 0
	g.dwellPrev = 0
	g.dwellLen = 0
	g.dwellActive = false
}

// observeInterleave folds one just-emitted step into the per-session
// interleaving accumulators. Pure bookkeeping: no rng, no control flow.
func (g *generator) observeInterleave(bs bufStep) {
	g.sessSlots[bs.slotIdx] = struct{}{}
	g.sessThreads[bs.engagedIdx] = struct{}{}
	g.sessTurnCount++

	// Topic dwell: extend the current run while engagedIdx is unchanged;
	// otherwise flush the finished run and start a new one.
	if g.dwellActive && bs.engagedIdx == g.dwellPrev {
		g.dwellLen++
		return
	}
	if g.dwellActive {
		g.topicDwellRuns = append(g.topicDwellRuns, g.dwellLen)
	}
	g.dwellPrev = bs.engagedIdx
	g.dwellLen = 1
	g.dwellActive = true
}

// finalizeInterleaveSession pushes the per-session summary samples and
// flushes the final in-progress dwell run. Called once per non-empty
// session (a session that emitted no steps contributes no samples).
func (g *generator) finalizeInterleaveSession() {
	g.sessionDistinctSlots = append(g.sessionDistinctSlots, len(g.sessSlots))
	g.sessionDistinctThreads = append(g.sessionDistinctThreads, len(g.sessThreads))
	g.sessionTurns = append(g.sessionTurns, g.sessTurnCount)
	if g.dwellActive {
		g.topicDwellRuns = append(g.topicDwellRuns, g.dwellLen)
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
	// InterleaveStrength scales the cross-topic moves (switch/new) up
	// relative to continue, raising unrelated-topic distractor pressure. It
	// never touches the corpus binding, only the action mix.
	xtopic := g.cfg.InterleaveStrength
	if xtopic < 1 {
		xtopic = 1
	}

	var opts []opt
	if len(g.layerB) >= 1 {
		opts = append(opts, opt{actContinue, g.cfg.ContinueWeight})
	}
	if len(g.layerB) >= 2 {
		opts = append(opts, opt{actSwitch, g.cfg.SwitchWeight * xtopic})
	}
	if len(g.resumeCandidates(turn)) > 0 {
		opts = append(opts, opt{actResume, g.cfg.ResumeWeight})
	}
	opts = append(opts, opt{actNew, g.cfg.NewWeight * xtopic})
	// The lifecycle campaign is always selectable: starting one creates its
	// own dedicated thread, so it needs no pre-existing population.
	opts = append(opts, opt{actCampaign, g.cfg.CampaignWeight})

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
//
// The function is the orchestration spine: pick the engaged thread,
// compute the recall oracle, choose the user input (with the
// user-dictated variant overlay), attach a work-turn read/modify/write
// cycle, and stamp the recall-opportunity declaration. Each phase is a
// dedicated helper so the spine stays readable; rng draw ordering is
// preserved exactly across helpers — the canonical (zero-feedback) step
// stream remains a pure function of (Seed, Duration, Corpus).
func (g *generator) buildStep(tt turnType, act action) bufStep {
	if act == actCampaign {
		return g.buildCampaignStep(tt)
	}
	idx, isNew := g.selectEngagedThread(act)
	g.threads[idx].engagementCount++

	// recallOpp is evaluated on the engaged thread's CURRENT slot, BEFORE
	// any wander (the wander gate consumes recallOpp). thr.cur == thr.slotIdx
	// for a monotonic thread, and tracks the trajectory tail for a
	// wandering one (§3.1). Q is the engaging turn's query symbol set as the
	// RUNTIME builds it: the current slot's topical mad-libs tags PLUS the
	// engaged thread's own salt. The runtime coalesces the turn's
	// user-prompt symbols AND its model anchor tags into one query set
	// (turn/recall.go:76 → coalesce.symbolList()), and the engaged thread
	// emits its salt as a model anchor this turn — so the salt lands in Q at
	// the runtime. The oracle must mirror that exactly or it manufactures
	// false misses: omitting the engaged salt makes the oracle's union one
	// smaller than the runtime's, so on loose-heavy slots the oracle
	// predicts a hit the runtime (with the larger union) misses. Including
	// it keeps oracle and runtime coherent. The salt still suppresses
	// NON-sibling collisions — the engaged salt is in neither a sibling nor
	// a non-sibling, and each candidate carries its OWN distinct salt in the
	// union (§2.2/§2.3) — so the density mechanism is intact; the salt is
	// simply also a query symbol, contrary to the design's §3.4 assumption
	// (SURPRISE; report).
	curSlot := g.model.slots[g.threads[idx].cur]
	Q := append(append([]string(nil), curSlot.Tags...), saltSymbol(g.threads[idx].order))
	recallIDs := g.recallExpectedFor(idx, Q, isNew)
	recallOpp := len(recallIDs) > 0

	// §3.2 wander decision: a fraction of NORMAL threads, on an eligible
	// engagement, advance to a different-topic slot, generalizing the
	// campaign drift mechanic. The rng draw is made unconditionally inside
	// the eligibility gate (maybeWander), so the canonical step stream stays
	// a pure function of (Seed, Duration, Corpus). A recall-opportunity turn
	// never wanders (recallOpp gates it), so cur — and thus Q above — stays
	// consistent with the emission on recall-opportunity turns.
	g.maybeWander(idx, isNew, recallOpp)
	thr := g.threads[idx]
	slot := g.model.slots[thr.cur]

	// anchorTags drops loose-cell positions on every step regardless of
	// action (anchor-lifecycle Inc 5: the §5.1 4-floor and its stub-padding
	// are gone). It emits the thread's CURRENT slot's tags (post-wander), so
	// a wandering thread's earlier topics stop being re-emitted and fall out
	// of the active projection into superseded-retained (§3.3). A new-topic
	// turn carries nonLooseTags verbatim — 0 anchors is legal (vague start),
	// and the runtime's projection owns the AnchorProjectionMax ceiling. The
	// thread's per-thread salt symbol (§2.2) is appended so the runtime folds
	// it into history_symbols — it is a model anchor, never a query symbol.
	anchorTags := append(nonLooseTags(slot), saltSymbol(thr.order))
	// §2.7.3 (#47): a synthesis thread carries forward a bounded sample of
	// its ≥2 parents' salient tags on its CREATION turn only. Emitting them
	// as extra model anchors makes the runtime accrete them into the
	// synthesis thread's history_symbols (and recordEmission mirrors it
	// into the shadow set, so the oracle scans the same blended set). The
	// field is cleared after this single emission — subsequent engagements
	// ride only the thread's own slot tags, the natural drift of a borrowed
	// idea becoming the thread's own.
	if len(thr.borrowedTags) > 0 {
		anchorTags = append(anchorTags, thr.borrowedTags...)
		g.threads[idx].borrowedTags = nil
	}
	userInput := defaultUserInput(slot, recallOpp)
	userDictated := false
	if udInput, ok := g.maybeUserDictated(tt, isNew, recallOpp, idx, slot); ok {
		userInput = udInput
		userDictated = true
	}
	// Record the FULL symbol set the runtime extracts this turn — the
	// user-prompt #-tags from userInput (on a recall opportunity that is
	// the slot's mad-libs query, which mentions EVERY tag including loose
	// cells) UNION the model anchor tags. Folding the user-prompt tags is
	// what keeps the shadow retained set aligned with history_symbols: the
	// runtime accretes the loose #-mentions the model anchorTags drop, so
	// recording only anchorTags would under-count the retained union and
	// flip borderline recall comparisons (§4.2 coherence). Done after the
	// user-dictated overlay so the recorded userInput is the one emitted.
	g.recordEmission(idx, extractedSymbolsFor(userInput, anchorTags))

	annotation := fmt.Sprintf("turn %d: %s %s (%s)",
		g.stepIndex+1, turnTypeName(tt), actionName(act), slot.Topic)
	if userDictated {
		annotation += " [user-dictated]"
	}
	step := scenarios.Step{
		UserInput:    userInput,
		MockResponse: buildMockResponse(isNew, thr.threadID(), anchorTags, slot),
		Annotation:   annotation,
		// Closure is set on EVERY step: any thread that decay-closes
		// during the run is resolved, and a nil ClosureAck would
		// disable closure detection that step. RecallAck is always
		// decline-all (the generator cannot predict the runtime's
		// offer set, so accepting nothing is the only contract-safe
		// choice — recall is *measured* here, not acted on).
		ClosureAck: &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:  &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
	}

	if tt == turnWork {
		step.PreEvents = g.buildWorkPreEvents(idx, slot, userDictated)
	}

	// On a recall opportunity, declare the ground-truth match set so
	// the harness records per-step recall fidelity. RecallMeasureOnly:
	// a recall miss must not fail this smoke rung.
	if recallOpp {
		step.ExpectedRecallMatches = recallIDs
		step.RecallMode = scenarios.RecallMeasureOnly
	}

	// Update the Layer-B LRU and last-engaged bookkeeping to reflect
	// this turn's engagement. g.stepIndex is this turn's 0-based index.
	g.engage(idx, g.stepIndex)
	// slotIdx reports the CURRENT (post-wander) slot the step emitted, so
	// the within-session interleaving telemetry counts a wander as touching
	// a new distinct slot (the distinct-slots-per-session signal then
	// reflects genuine topic movement, not just the birth binding).
	return bufStep{step: step, slotIdx: thr.cur, engagedIdx: idx}
}

// campaign is one in-flight lifecycle ground-truth program. It owns a
// dedicated thread (threadIdx, created on the campaign's first turn) and
// walks a deterministic phase sequence keyed off the 1-based turn counter.
//
// Origin and destination symbol sets are recorded as oracles: origin is
// the dedicated thread's bound-slot nonLooseTags; dest is a disjoint set
// of real tags drawn from two slots of a DIFFERENT topic — disjoint so the
// destination Counts can climb past the origin Counts and outrank the
// origin symbols out of the top-AnchorProjectionMax projection, flipping
// them to superseded (EverCentral retained). drift and invert share this
// supersession mechanic; they differ only in which recall the campaign
// then measures (drift measures both origin-retained and dest-active;
// invert measures the abandoned-premise origin query surfacing the
// thread). vague accretes from 0 anchors and measures the became-matchable
// turn.
type campaign struct {
	kind       campaignKind
	threadIdx  int // creation order of the dedicated thread; -1 until created
	originSlot int // the dedicated thread's bound corpus slot
	destSlot   int // destination slot (a different topic than origin)
	turn       int // 1-based campaign-internal turn counter
	total      int // total turns this campaign runs
}

// destTags returns the campaign's destination symbol set: a single
// different-topic slot's nonLooseTags (de-duplicated, stable order). One
// slot (~4–5 disjoint real tags) is the deliberate calibration: combined
// with the origin set (~4–5) it pushes the active count just past
// AnchorProjectionMax (8), so the climbing destination Counts outrank the
// LOWEST origin symbols out of the top-max projection (→ superseded) while
// the rest of origin stays active. That partial supersession is what lets
// the abandoned-premise / drift-origin query still clear the §3.4 Jaccard
// threshold (the union stays tight) while genuine supersession still
// occurs — a maximal destination flood would supersede ALL origin but
// dilute the union so far that abandoned-premise recall collapses to 0,
// the degenerate case the calibration avoids.
func (g *generator) destTags(c *campaign) []string {
	return nonLooseTags(g.model.slots[c.destSlot])
}

// startCampaign initializes the next campaign: it picks the dedicated
// thread's origin slot and two destination slots of a DIFFERENT topic, all
// deterministically from the corpus order and a single rng draw, then
// rotates nextCampaignKind. The thread itself is created lazily on the
// campaign's first emitted turn (so its creation order is the live
// len(threads) at that moment, keeping the threadID mapping intact).
func (g *generator) startCampaign() {
	n := len(g.model.slots)
	// One rng draw selects the origin slot; the destination slots are a
	// fixed offset into a different topic so they never overlap origin.
	origin := g.rng.Intn(n)
	originTopic := g.model.slots[origin].Topic
	dest := (origin + n/3) % n
	for g.model.slots[dest].Topic == originTopic {
		dest = (dest + 1) % n
	}

	total := map[campaignKind]int{
		campaignVague:  vagueTurns + 4, // M vague turns + accrete + cooldown + recall
		campaignDrift:  driftTurns + 4, // walk + cooldown + origin-recall + dest-recall
		campaignInvert: invertTurns + 3,
	}[g.nextCampaignKind]

	g.campaign = &campaign{
		kind:       g.nextCampaignKind,
		threadIdx:  -1,
		originSlot: origin,
		destSlot:   dest,
		total:      total,
	}
	g.nextCampaignKind = (g.nextCampaignKind + 1) % 3
}

// buildCampaignStep services one campaign turn, starting the campaign if
// none is active. It is a pure function of generator state + one rng draw
// in startCampaign + the per-turn gap draw the caller makes — no feedback
// branch, so the canonical stream stays deterministic. On the campaign's
// final turn the campaign clears so the next actCampaign draw starts the
// next kind.
func (g *generator) buildCampaignStep(tt turnType) bufStep {
	if g.campaign == nil {
		g.startCampaign()
	}
	c := g.campaign
	c.turn++

	var bs bufStep
	switch c.kind {
	case campaignVague:
		bs = g.campaignVagueTurn(tt, c)
	case campaignDrift:
		bs = g.campaignDriftTurn(tt, c)
	default:
		bs = g.campaignInvertTurn(tt, c)
	}

	if c.turn >= c.total {
		g.campaign = nil
	}
	return bs
}

// ensureCampaignThread creates the campaign's dedicated thread on first
// use, bound to its origin slot, and returns its creation-order index.
// Subsequent calls return the existing index.
func (g *generator) ensureCampaignThread(c *campaign) int {
	if c.threadIdx >= 0 {
		return c.threadIdx
	}
	c.threadIdx = g.createThread(len(g.threads), c.originSlot)
	return c.threadIdx
}

// campaignEngage builds an engagement step on the campaign thread emitting
// the given anchor tags (the model's advisory symbol contribution). The
// user input names the first tag so the user-prompt symbol path also fires;
// when tags is empty the input is a generic vague line carrying no #-tag,
// so the thread accretes nothing (the 0-anchor vague phase). It updates the
// Layer-B LRU like buildStep does.
func (g *generator) campaignEngage(tt turnType, c *campaign, anchorTags []string, label string) bufStep {
	idx := g.ensureCampaignThread(c)
	thr := g.threads[idx]
	slot := g.model.slots[thr.slotIdx]
	isNew := thr.order == idx && g.firstEngagement(idx)

	var userInput string
	if len(anchorTags) == 0 {
		userInput = "still figuring out what this is about"
	} else {
		userInput = fmt.Sprintf("working on #%s", anchorTags[0])
	}

	// Append the thread's salt (§2.2/§3.4) and record the FULL extracted
	// set (userInput #-tags ∪ model anchors + salt) into the shadow retained
	// set so campaign threads share the same oracle bookkeeping as normal
	// threads (DRY: one emission path, one extraction rule). The salt
	// follows any topical tags so anchorTags[0] above stays a topical tag
	// (the user-prompt #-mention must be topical, not the salt). On a
	// 0-anchor vague turn the userInput carries no #-tag, so the only
	// emitted symbol is the salt — correct: a vague thread accretes nothing
	// topical, but owns a private salt token, which never appears in any
	// query Q so it cannot make the thread spuriously matchable.
	anchorTags = append(append([]string(nil), anchorTags...), saltSymbol(thr.order))
	g.recordEmission(idx, extractedSymbolsFor(userInput, anchorTags))

	threads := []string{thr.threadID()}
	if isNew {
		threads = []string{prompt.NewTopicLiteral}
	}
	body := fmt.Sprintf("%s — %s.", label, slot.Topic)

	step := scenarios.Step{
		UserInput:    userInput,
		MockResponse: scenarios.NewMockResponseWithTag(threads, anchorTags, body),
		Annotation:   fmt.Sprintf("turn %d: %s campaign-%s (%s)", g.stepIndex+1, turnTypeName(tt), label, slot.Topic),
		ClosureAck:   &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:    &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
	}
	if tt == turnWork {
		step.PreEvents = g.buildWorkPreEvents(idx, slot, false)
	}
	g.engage(idx, g.stepIndex)
	return bufStep{step: step, slotIdx: thr.slotIdx, engagedIdx: idx}
}

// firstEngagement reports whether thread idx has never been engaged (its
// last-engaged turn is still the zero default and it is not yet in the LRU).
// Used to drive the *new-topic* tag on a campaign thread's genesis turn.
func (g *generator) firstEngagement(idx int) bool {
	return !g.inLayerB(idx) && (idx >= len(g.lastEngagedTurn) || g.lastEngagedTurn[idx] == 0)
}

// campaignRecall builds a recall-opportunity step that issues querySymbols
// (origin or destination tags) on a turn that engages the DEDICATED
// MEASUREMENT CARRIER, declaring the campaign thread as the expected match.
// The campaign thread is not engaged this turn, so the §3.4 recall scan can
// surface it; the step carries bucket so the outcome is tallied
// post-feedback. suppressEpisode keeps it out of the miss→refinement loop.
// vagueTurn (>0) records the became-matchable turn for the vague oracle.
//
// The carrier (not a real recall-candidate thread) absorbs the query
// #-tags' permanent accretion, so the campaign measurement never inflates
// a genuine thread's retained set past the eviction cap (the #96 inc-2
// invariant — the same fix as the wander probe).
func (g *generator) campaignRecall(tt turnType, c *campaign, querySymbols []string, bucket string, vagueTurn int) bufStep {
	campIdx := c.threadIdx
	engageIdx, carrierCreated := g.measurementCarrier()

	// The query is a natural line mentioning the campaign's symbols as
	// #-tags so the runtime extracts them and the §3.4 layer fires against
	// the campaign thread's history.
	var mentions strings.Builder
	for i, s := range querySymbols {
		if i > 0 {
			mentions.WriteString(" ")
		}
		mentions.WriteString("#")
		mentions.WriteString(s)
	}
	userInput := "revisiting " + mentions.String()

	// The carrier's model tag re-emits the QUERY symbols (not its own slot
	// tags), so the turn's coalesced query set Q is exactly the query
	// symbols — user #-mentions and model anchors agree. Emitting unrelated
	// tags would dilute Q below the §3.4 threshold and mask the campaign
	// thread's match. Record the carrier's accretion into the shadow so it
	// stays internally consistent with what the runtime folds into the
	// carrier's history_symbols; the carrier is excluded from the
	// recall-oracle scan and criterion (e), so this accretion cannot inflate
	// a genuine candidate's set nor be scored as a future match.
	g.recordEmission(engageIdx, extractedSymbolsFor(userInput, querySymbols))
	carrierTag := g.threads[engageIdx].threadID()
	if carrierCreated {
		carrierTag = prompt.NewTopicLiteral
	}
	step := scenarios.Step{
		UserInput:             userInput,
		MockResponse:          scenarios.NewMockResponseWithTag([]string{carrierTag}, querySymbols, "context revisit."),
		Annotation:            fmt.Sprintf("turn %d: %s campaign-recall %s", g.stepIndex+1, turnTypeName(tt), bucket),
		ExpectedRecallMatches: []string{g.threads[campIdx].threadID()},
		RecallMode:            scenarios.RecallMeasureOnly,
		ClosureAck:            &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:             &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
	}
	// The carrier is deliberately NOT entered into the generator's Layer-B
	// model (no g.engage): it is a measurement artifact, not a topical
	// thread. The RUNTIME still engages it (the MockResponse threads it),
	// excluding it from its own turn's recall scan via engagedSet.
	return bufStep{
		step:              step,
		slotIdx:           g.threads[engageIdx].slotIdx,
		engagedIdx:        engageIdx,
		recallBucket:      bucket,
		suppressEpisode:   true,
		vagueCampaignTurn: vagueTurn,
	}
}

// campaignVagueTurn runs the vague-new program: 0-anchor turns, then
// accretion, a cooldown (engaging others so the vague thread is dormant),
// then a recall step whose first hit records the became-matchable turn.
func (g *generator) campaignVagueTurn(tt turnType, c *campaign) bufStep {
	switch {
	case c.turn <= vagueTurns:
		// 0-anchor vague turns — no symbols accrete (empty tag set).
		return g.campaignEngage(tt, c, nil, "vague")
	case c.turn == vagueTurns+1:
		// Begin accreting the thread's bound-slot tags; arm the vague match
		// oracle so the first subsequent recall hit records the turn.
		g.vagueArmed = true
		return g.campaignEngage(tt, c, nonLooseTags(g.model.slots[c.originSlot]), "accrete")
	case c.turn <= c.total-1:
		// Cooldown: keep accreting on the campaign thread (raises Count) —
		// these are not recall steps.
		return g.campaignEngage(tt, c, nonLooseTags(g.model.slots[c.originSlot]), "accrete")
	default:
		// Recall: the now-accreted thread should surface on its slot query.
		return g.campaignRecall(tt, c, nonLooseTags(g.model.slots[c.originSlot]), bucketDriftDest, c.turn)
	}
}

// campaignDriftTurn runs the drift program: create the thread on its origin
// tags, then walk to the destination set (emitted every turn so its Counts
// climb past origin and outrank origin out of the projection → origin
// superseded-retained). It then measures origin recall (retained-but-lower)
// and destination recall (active-high).
func (g *generator) campaignDriftTurn(tt turnType, c *campaign) bufStep {
	dest := g.destTags(c)
	switch {
	case c.turn == 1:
		// Genesis on the origin tags — origin becomes ever-central.
		return g.campaignEngage(tt, c, nonLooseTags(g.model.slots[c.originSlot]), "drift-origin")
	case c.turn <= driftTurns:
		// Walk: emit the destination set every turn; origin is never
		// re-emitted, so its Count stays low and it is outranked.
		return g.campaignEngage(tt, c, dest, "drift-dest")
	case c.turn == driftTurns+1:
		// Origin-recall: the abandoned origin premise stays matchable via
		// the retained superseded symbols.
		return g.campaignRecall(tt, c, nonLooseTags(g.model.slots[c.originSlot]), bucketDriftOrigin, 0)
	default:
		// Dest-recall: the active projection is the destination set.
		return g.campaignRecall(tt, c, dest, bucketDriftDest, 0)
	}
}

// campaignInvertTurn runs the invert program: same supersession mechanic as
// drift, but the measured signal is the abandoned-premise query for the
// ORIGINAL set surfacing the thread (EverCentral retention is what keeps it
// findable after the premise inverted to the discovery set).
func (g *generator) campaignInvertTurn(tt turnType, c *campaign) bufStep {
	dest := g.destTags(c)
	switch {
	case c.turn == 1:
		return g.campaignEngage(tt, c, nonLooseTags(g.model.slots[c.originSlot]), "invert-origin")
	case c.turn <= invertTurns:
		return g.campaignEngage(tt, c, dest, "invert-discovery")
	default:
		// Abandoned-premise recall: query the ORIGINAL set; the inverted
		// thread must still surface.
		return g.campaignRecall(tt, c, nonLooseTags(g.model.slots[c.originSlot]), bucketAbandonedPremise, 0)
	}
}

// selectEngagedThread picks the thread the upcoming step engages,
// dispatching on the per-turn action. actNew appends a fresh thread to
// g.threads with a deterministic corpus-slot binding; actSwitch and
// actResume consume one rng draw each from g.rng. actContinue and
// actNew make no rng draws — preserving the original rng draw ordering
// is what keeps buildStep's output a pure function of (Seed, Duration,
// Corpus).
func (g *generator) selectEngagedThread(act action) (idx int, isNew bool) {
	switch act {
	case actNew:
		idx = len(g.threads)
		// Corpus binding is deterministic by creation order — see
		// corpusModel.slotFor. familySize consecutive threads share one
		// slot; the binding never depends on rng, so the recall-oracle
		// fire rate is fixed and duration-independent (each rung
		// measures the same thing).
		g.createThread(idx, g.model.slotFor(idx))
		isNew = true
		// §2.7.3 (#47): a fraction of creations become synthesis threads,
		// borrowing salient tags from ≥2 prior threads. The gate draw is
		// made UNCONDITIONALLY here (same discipline as maybeWander) so the
		// rng draw ordering stays a pure function of (Seed, Duration,
		// Corpus): every actNew consumes exactly one synthesis draw.
		g.maybeSynthesize(idx)

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
	return idx, isNew
}

// nextWanderSlot returns the corpus slot a wandering thread moves to
// next (§3.2). It is PURE and deterministic — derived only from the
// thread's creation order and its current trajectory length, never from
// rng — so the wander destinations are a fixed per-thread sequence and
// the canonical step stream stays a pure function of (Seed, Duration,
// Corpus). The single rng draw that decides WHETHER to wander lives in
// maybeWander; this function decides only WHERE.
//
// The walk starts at (cur + offset) % len(slots) with offset derived
// from (order, hop count), then skips forward to the first slot of a
// DIFFERENT topic than cur — mirroring startCampaign's different-topic
// skip (so the destination tags are disjoint from the current topic's,
// letting the current topic genuinely supersede the origin once the
// destination Counts climb past it). Wander targets need not be in the
// thread's family.
func nextWanderSlot(thr thread, slots []CorpusSlot) int {
	n := len(slots)
	if n == 0 {
		return thr.cur
	}
	curTopic := slots[thr.cur].Topic
	// offset is a fixed function of (order, hop) so each thread walks a
	// distinct, reproducible sequence. +1 guards against a zero offset
	// landing back on cur before the different-topic skip runs.
	offset := (thr.order + len(thr.traj)*7) % n
	dest := (thr.cur + offset + 1) % n
	for slots[dest].Topic == curTopic {
		dest = (dest + 1) % n
		if dest == thr.cur {
			break // single-topic corpus — nothing different to wander to
		}
	}
	return dest
}

// maybeWander applies the §3.2 wander schedule to the engaged thread on
// the current turn. The eligibility gate is !isNew && !recallOpp &&
// engagementCount >= wanderMinDwell && trajectory not yet at the hop cap.
// The rng draw is made UNCONDITIONALLY INSIDE the gate (same discipline
// as maybeUserDictated) so the rng draw ordering stays a pure function of
// the generator's inputs: a thread that is eligible always consumes one
// draw whether or not it wanders, and an ineligible thread consumes none.
//
// On a true draw the thread appends nextWanderSlot to its trajectory and
// advances cur, so its subsequent emissions ride the new topic's tags and
// its earlier topics fall out of the active projection into
// superseded-retained (the campaign drift mechanic, §3.3) — no new
// runtime behavior. The wander decision precedes emission in buildStep,
// so the very turn a thread wanders already emits its new current slot.
func (g *generator) maybeWander(idx int, isNew, recallOpp bool) {
	thr := &g.threads[idx]
	if isNew || recallOpp || thr.engagementCount < wanderMinDwell || len(thr.traj) >= wanderMaxHops {
		return
	}
	if g.rng.Intn(wanderSelector) >= wanderPct {
		return
	}
	dest := nextWanderSlot(*thr, g.model.slots)
	if dest == thr.cur {
		return // degenerate single-topic corpus: no genuine wander available
	}
	thr.traj = append(thr.traj, dest)
	thr.cur = dest
}

// eligibleSynthesisParents returns the creation-order indices of threads
// a synthesis thread may borrow from, applying the SAME exclusions the
// recall oracle uses (recallExpectedForMaterialized): the new thread
// itself, any thread currently resident in Layer B, and any measurement
// carrier are excluded. The remaining dormant/materialized non-carrier
// threads are the candidate parent pool. Returned in ascending creation
// order; deterministic (no rng) so the canonical step stream stays a pure
// function of (Seed, Duration, Corpus).
func (g *generator) eligibleSynthesisParents(newIdx int) []int {
	var out []int
	for i := range g.threads {
		order := g.threads[i].order
		if order == newIdx || g.inLayerB(order) || g.isCarrier(order) {
			continue
		}
		out = append(out, order)
	}
	return out
}

// maybeSynthesize converts the just-created new thread newIdx into a
// SYNTHESIS thread (§2.7.3, #47): a fresh framing that borrows a bounded
// sample of salient tags from synthesisParents prior threads. The gate
// draw is consumed UNCONDITIONALLY on every actNew (the caller's
// discipline), so the rng draw ordering is input-determined; the borrow
// only happens when the draw fires AND at least synthesisParents eligible
// parents exist.
//
// Parents are picked DETERMINISTICALLY from the eligible pool by a
// round-robin cursor — no rng beyond the gate — so synthesis samples a
// spread of parents across the run rather than always the lowest orders.
// The borrowed tags are stamped on the thread's borrowedTags field;
// buildStep emits them as extra model anchors on the creation turn, so
// the runtime accretes them and the shadow retained set mirrors that.
func (g *generator) maybeSynthesize(newIdx int) {
	fires := g.rng.Intn(synthesisSelector) < synthesisPct
	if !fires {
		return
	}
	parents := g.eligibleSynthesisParents(newIdx)
	if len(parents) < synthesisParents {
		return // too few prior threads to synthesise from
	}
	borrowed := make([]string, 0, synthesisParents*synthesisParentTagSample)
	for k := 0; k < synthesisParents; k++ {
		p := parents[(g.synthesisParentCursor+k)%len(parents)]
		borrowed = append(borrowed, g.salientParentTags(p)...)
	}
	g.synthesisParentCursor++
	g.threads[newIdx].borrowedTags = borrowed
	g.synthesisEvents++
	g.synthesisParentCounts = append(g.synthesisParentCounts, synthesisParents)
	g.synthesisBorrowedSymbols += len(borrowed)
	// A synthesis is a material structural change beyond the bare creation
	// createThread already counted: it folds ≥2 prior threads' framings
	// into a new spine record (#42 reads it as a distinct commit-worthy
	// event). Tallied into the same per-day accumulator.
	g.dayStructuralChanges++
}

// salientParentTags returns a bounded sample (up to synthesisParentTagSample)
// of a parent thread's salient tags: the non-loose tags of its CURRENT slot
// — the same set the parent emits as its own anchors (nonLooseTags), so a
// synthesis genuinely carries forward the parent's live framing. Bounded so
// the synthesis thread's emitted set stays well under the eviction cap and
// the borrow dilutes without manufacturing a false recall match (§4.3).
// Deterministic (no rng).
func (g *generator) salientParentTags(parentIdx int) []string {
	tags := nonLooseTags(g.model.slots[g.threads[parentIdx].cur])
	if len(tags) > synthesisParentTagSample {
		tags = tags[:synthesisParentTagSample]
	}
	return tags
}

// extractedSymbolsFor returns the symbol set the RUNTIME extracts for the
// engaged thread on one turn, mirroring turn/chain.go's two relevant
// passes: the user-prompt hash-tag pass (turn.UserTagRE over userInput,
// each capture normalized via memops.Normalize(raw, memops.SymbolTag))
// UNION the model-response anchor tags (modelAnchors, already normalized
// by the generator). Both passes feed the engaged thread's coalesced
// symbols, which merge into its history_symbols at turn close — so this
// union is precisely what the runtime accretes, and feeding it to
// recordEmission keeps the shadow retained set byte-aligned with the
// runtime's history_symbols (§4.2 coherence).
//
// It shares the runtime's extraction RULE (the exported turn.UserTagRE
// regex and memops.Normalize), not the runtime code path — the same
// permitted coupling as sharing the threshold constant (§4.2). The third
// deterministic identifier pass (URL / file-path / git-SHA) is irrelevant
// here: the generated tags, salt, and mad-libs #-mentions match none of
// those shapes, and the user-dictated work-turn input's only path-like
// token is a bare "<slug>.go" (no leading slash), which turn's
// FilePathProseRE — requiring a "/" or "./"/"~/" prefix — does not match.
// It is PURE (no rng): the canonical step stream stays a function of
// (Seed, Duration, Corpus).
func extractedSymbolsFor(userInput string, modelAnchors []string) []string {
	out := append([]string(nil), modelAnchors...)
	for _, m := range turn.UserTagRE.FindAllStringSubmatch(userInput, -1) {
		out = append(out, memops.Normalize(m[1], memops.SymbolTag))
	}
	return out
}

// recordEmission folds the symbols extracted for thread idx on one turn
// into that thread's shadow retained set (§4.2). It is the generator's
// independent bookkeeping of what the runtime accretes into
// history_symbols — fed ONLY by what the generator emits, never by a
// runtime read. Callers pass the FULL extracted set
// (extractedSymbolsFor: user-prompt #-tags ∪ model anchor tags) so the
// shadow mirrors the runtime's accretion exactly. Pure (no rng); called
// from every emission path (buildStep, campaignEngage, refinement, probe)
// so the oracle and the runtime see the same accretion.
//
// The union grows as a thread wanders (increment 2): each new-topic
// emission accretes its slot's tags, and recordEmission's set union is
// the trajectory-spanning retained set the recall oracle scores against.
func (g *generator) recordEmission(idx int, tags []string) {
	set := g.emittedSyms[idx]
	if set == nil {
		set = map[string]struct{}{}
		g.emittedSyms[idx] = set
	}
	for _, t := range tags {
		set[t] = struct{}{}
	}
}

// shadowRetainedSet returns thread idx's shadow retained symbol set: the
// union of every tag the generator has emitted for it, with the thread's
// salt unioned in defensively (recordEmission already includes it, but a
// thread that has not yet emitted still carries its salt by definition).
// This mirrors the runtime's T(thr) = active ∪ superseded-retained ∪
// salt (§4.3) at the default superseded-weight, where the eviction-free
// union collapses the active/superseded distinction.
func (g *generator) shadowRetainedSet(idx int) map[string]struct{} {
	src := g.emittedSyms[idx]
	out := make(map[string]struct{}, len(src)+1)
	for s := range src {
		out[s] = struct{}{}
	}
	out[saltSymbol(g.threads[idx].order)] = struct{}{}
	return out
}

// simJaccard computes the set Jaccard of query symbols q against a
// thread's retained symbol set t: |q ∩ t| / |q ∪ t|. It mirrors the
// runtime scorer's score at the default superseded-weight 1.0
// (scoring.go:150-151: |matched| / (|q| + |threadSet| - |matched|)),
// computed independently from the generator's shadow set. Empty q or t
// scores 0. Duplicate symbols in q are de-duplicated to match the
// runtime's set semantics.
func simJaccard(q []string, t map[string]struct{}) float64 {
	if len(q) == 0 || len(t) == 0 {
		return 0
	}
	qset := make(map[string]struct{}, len(q))
	for _, s := range q {
		qset[s] = struct{}{}
	}
	inter := 0
	for s := range qset {
		if _, ok := t[s]; ok {
			inter++
		}
	}
	union := len(qset) + len(t) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// recallExpectedFor computes the recall-opportunity oracle for the
// engaging turn (§4.2): the dormant threads (not in Layer B, not the
// engaged thread) whose shadow retained symbol set clears the
// symbolic-Jaccard threshold against the query Q. The caller passes Q as
// the runtime builds it — the slot's topical mad-libs tags plus the
// engaged thread's own salt (the runtime coalesces the turn's model
// anchor tags into the query set, so the engaged salt lands in Q; see
// buildStep). The salt still suppresses non-sibling false positives:
// each candidate carries its own distinct salt in the union and neither
// shares the engaged salt, so the density mechanism holds (§2.2/§2.3).
// Returns thread IDs in ascending creation order.
//
// This replaces the old O(1) slot-equality rule, which was valid only
// while every thread was monotonic. It is independent of the runtime
// (no ProposeFromIndex / ProjectAnchors / store call) — it scores the
// generator's own emission log — yet coherent with the runtime by
// construction: same union definition (eviction-free retained-symbol
// set, anchors ⊆ history_symbols, §4.3), same threshold constant
// (simRecallThreshold = scoring.DefaultThreshold), and the generator's
// threads stay far under the 40-symbol eviction cap so no eviction
// modeling is needed.
//
// For a monotonic family sibling: Q = slot tags, the sibling's set =
// {nonLooseTags(slot)} ∪ {salt}. With no loose cells, Jaccard =
// |tags| / (|tags|+1) ≈ 5/6 ≫ threshold → the sibling still fires
// comfortably. A loose-heavy slot (L large) lowers the numerator and
// can drop below threshold — a legitimate miss the runtime also makes,
// exercising the miss→refinement loop. A non-sibling on a different
// slot shares at most incidental cross-topic synonyms and carries its
// own salt in the union, so its Jaccard sits well below threshold.
//
// New-thread turns suppress the recall-opportunity emission: the
// engaging userInput on a recall opportunity is the slot's full
// UserInput (#-tags including any loose cells), and on a
// thread-creation turn that pollutes the new thread's coalesced anchors
// with the loose terms via §3.3 symbol extraction — defeating the
// loose-filter on anchorTags. The dormant first-family-member still
// surfaces as the sibling on later continue/switch/resume turns.
func (g *generator) recallExpectedFor(idx int, Q []string, isNew bool) []string {
	return g.recallExpectedForMaterialized(idx, Q, isNew, noMaterializationFilter)
}

// noMaterializationFilter is the sentinel materializedBefore value that
// disables the #100 materialization filter — every thread in g.threads is
// considered materialized. Canonical (generation-time) buildStep passes
// it because at generation time g.threads only holds threads whose
// creating step was generated earlier, which is exactly earlier in
// execution; the filter would be a no-op there anyway, so the sentinel
// keeps the canonical-step oracle byte-for-byte unchanged.
const noMaterializationFilter = -1

// recallExpectedForMaterialized is recallExpectedFor with the #100
// execution-time materialization filter. materializedBefore is the count
// of canonical steps the harness has already EXECUTED (g.ready.firstStep +
// g.readyPos at the refinement-injection point): a candidate thread is
// materialized on the runtime spine iff its creating step has executed,
// i.e. createdAtStep < materializedBefore. The day-ahead buffer means
// g.threads holds threads whose creating step is generated but not yet
// run; naming such a thread as an expected match produces a thread the
// runtime has no spine record for yet — an off-spine-not-archived false
// miss that the harness charges to recall_unexplained_absence (#100). The
// refinement path (injected at execution time) passes the executed count
// so those unmaterialized threads are excluded; the canonical path passes
// noMaterializationFilter so its behavior is unchanged.
//
// This is the ONLY oracle-side coupling to execution position. It is
// measurement-side: the filter does not consume rng and does not reshape
// the canonical step stream (refinement is injected only under real
// feedback), so the determinism contract holds.
func (g *generator) recallExpectedForMaterialized(idx int, Q []string, isNew bool, materializedBefore int) []string {
	if isNew {
		return nil
	}
	var ids []string
	for _, cand := range g.threads {
		if cand.order == idx || g.inLayerB(cand.order) || g.isCarrier(cand.order) {
			// A measurement carrier is excluded from the oracle scan: its
			// retained set is a synthetic grab-bag of foreign measurement
			// queries, not a genuine recall candidate. Runtime-side its
			// accreted union is diluted far below the §3.4 threshold (a
			// ~5-symbol query against its ~40-symbol evicted set scores
			// ≤0.125), so it does not fire as a collision there either —
			// keeping oracle and runtime coherent without scoring it.
			continue
		}
		if materializedBefore != noMaterializationFilter && cand.createdAtStep >= materializedBefore {
			// Created by a step not yet executed: the runtime has not put
			// this thread on the spine at this execution point, so naming it
			// an expected match would be a false miss (#100). Exclude it.
			continue
		}
		if simJaccard(Q, g.shadowRetainedSet(cand.order)) >= simRecallThreshold {
			ids = append(ids, cand.threadID())
		}
	}
	return ids
}

// executedStepCount is the number of canonical buffered steps the harness
// has already handed out (executed) at the current Next() injection point.
// g.ready.firstStep is the global index of g.ready.steps[0] and g.readyPos
// is the next offset to draw, so their sum is the global index of the next
// step to draw — equivalently the count of steps already drawn. A thread
// whose createdAtStep is < this count has had its creating step executed
// and is materialized on the spine. Refinement is only injected while a
// g.ready buffer is active, so g.ready is non-nil here.
func (g *generator) executedStepCount() int {
	return g.ready.firstStep + g.readyPos
}

// probeTarget returns a WANDERING DORMANT thread to probe — one with a
// multi-topic trajectory (len(traj) >= 2) not currently in Layer B, so
// the §3.4 recall scan can surface it. It ROUND-ROBINS across all eligible
// threads (probeTargetCursor) rather than always returning the lowest
// order, so the probe samples shallow AND deep trajectories: a fixed
// lowest-order pick biases toward the deepest thread (most engagements →
// deepest wander), which would make wander_current_recall measure only
// the worst case. nil if none eligible. Deterministic (no rng), so the
// probe stays out of the canonical rng stream.
func (g *generator) probeTarget() *thread {
	var eligible []int
	for i := range g.threads {
		if g.isCarrier(i) {
			continue // a measurement carrier is never a genuine probe target
		}
		if len(g.threads[i].traj) >= 2 && !g.inLayerB(g.threads[i].order) {
			eligible = append(eligible, i)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	pick := eligible[g.probeTargetCursor%len(eligible)]
	g.probeTargetCursor++
	return &g.threads[pick]
}

// isCarrier reports whether thread idx is a dedicated measurement-carrier
// thread (see carrierIdx). Carriers are excluded from every recall-oracle
// candidate scan and from the criterion (e) shadow max-retained
// measurement.
func (g *generator) isCarrier(idx int) bool {
	_, ok := g.carrierIdx[idx]
	return ok
}

// measurementCarrier returns the creation-order index of the dedicated
// carrier thread used to host a measurement query (abandoned-topic probe
// or campaign recall), creating it lazily on first use. created is true
// only on that first call — the caller must then tag the carrier's
// MockResponse with prompt.NewTopicLiteral so the runtime CREATES the
// thread rather than treating an unknown id as a §5.5 fetch miss.
//
// The carrier is recorded in carrierIdx so it is excluded from the
// recall-oracle scan and the criterion (e) measurement; it is NOT entered
// into the generator's Layer-B model (engage is not called on it), so it
// never displaces a genuine topical thread from the switch/resume
// candidate pool — the canonical step stream stays a pure function of
// (Seed, Duration, Corpus). Its corpus binding (slot 0) is inert: a
// carrier emits only the measurement query's symbols as anchors, never its
// own slot's tags, and it is excluded from every oracle, so the binding
// never affects recall.
func (g *generator) measurementCarrier() (idx int, created bool) {
	if g.carrier >= 0 {
		return g.carrier, false
	}
	idx = g.createThread(len(g.threads), 0)
	g.carrierIdx[idx] = struct{}{}
	g.carrier = idx
	return idx, true
}

// buildWanderProbeStep constructs one abandoned-topic probe (§3.2,
// criterion (b)) if an eligible wandering dormant thread exists; ok=false
// skips it. It is rng-FREE — every choice (target, hop distance, query) is
// a pure function of generator state — so it is part of the canonical
// stream and the determinism guard stays byte-identical.
//
// The probe engages the DEDICATED MEASUREMENT CARRIER (not a real
// recall-candidate thread) and issues a query on ONE of the probed
// thread's trajectory slots, declaring the probed thread the expected
// match. The carrier — not the target — absorbs the query #-tags'
// permanent accretion, so the measurement never inflates a genuine
// recall-candidate's retained set past the eviction cap (the #96 inc-2
// invariant). The hop distance is (last traj index − queried traj index):
// hop 0 is the CURRENT topic (the wander_current_recall control, which
// must surface ≥~0.95); hop >=1 is an ABANDONED earlier topic, expected to
// DECAY with distance as later topics inflate the retained union past the
// §3.4 Jaccard threshold (the deliverable curve). The probed hop
// round-robins (probeHopCursor) so every bucket fills.
//
// The oracle's prediction (simJaccard(Q, retained) >= threshold) is
// recorded on the probe so the next Next()'s feedback can detect
// oracle/runtime DIVERGENCE — the criterion (b) pass condition is
// coherence (oracle and runtime agree per hop), not a high recall floor.
func (g *generator) buildWanderProbeStep() (bufStep, bool) {
	target := g.probeTarget()
	if target == nil {
		return bufStep{}, false
	}
	engageIdx, carrierCreated := g.measurementCarrier()

	// Choose the queried trajectory index. probeHopCursor round-robins the
	// hop distance across [0, len(traj)-1] so the current-topic control
	// (hop 0) and every abandoned hop fill over the run. lastIdx is the
	// current slot's trajectory position.
	lastIdx := len(target.traj) - 1
	hop := g.probeHopCursor % len(target.traj)
	g.probeHopCursor++
	queriedTrajIdx := lastIdx - hop
	queriedSlot := g.model.slots[target.traj[queriedTrajIdx]]

	// Q is the queried slot's topical tags — the carrier re-emits the
	// QUERY symbols as model anchors (like campaignRecall), so the runtime's
	// coalesced query set is exactly these tags (no salt: the carrier
	// does not emit the probed thread's salt, and the probed thread's own
	// salt sits only in its retained union, inflating the denominator). The
	// oracle scores the SAME Q against the probed thread's shadow retained
	// set with the SAME threshold — coherent by construction (§4.2).
	Q := nonLooseTags(queriedSlot)
	predictHit := simJaccard(Q, g.shadowRetainedSet(target.order)) >= simRecallThreshold

	var mentions strings.Builder
	for i, s := range Q {
		if i > 0 {
			mentions.WriteString(" ")
		}
		mentions.WriteString("#")
		mentions.WriteString(s)
	}

	// The probe declares NO ExpectedRecallMatches: the runtime fires
	// spine.match-fire from the query regardless of the harness's expected
	// declaration, and the probe reads the fired-id set (RecallMatchFireIDs)
	// directly. Declaring an expected set would route a deliberate high-hop
	// MISS through recordRecallFidelity into the recall_fidelity_adversarial_*
	// series — confounding the headline symbolic-only recall and
	// superseded_precision the rung reports (criterion c). Keeping it
	// unmeasured isolates the probe's measurement to its own hop buckets.
	probeUserInput := "revisiting " + mentions.String()

	// Record the carrier's accretion into the shadow: the runtime folds the
	// probe query symbols (user #-mentions ∪ model anchors, both Q here)
	// into the carrier's history_symbols. The carrier is excluded from the
	// recall-oracle scan (recallExpectedFor / probeTarget) and the
	// criterion (e) measurement, so this accretion can neither inflate a
	// genuine candidate's retained set nor be scored as a future match — it
	// stays internally consistent only (the shadow mirrors what the runtime
	// holds). The probed thread itself is NOT engaged, so it accretes
	// nothing this turn; only the carrier does.
	g.recordEmission(engageIdx, extractedSymbolsFor(probeUserInput, Q))

	carrierTag := g.threads[engageIdx].threadID()
	if carrierCreated {
		carrierTag = prompt.NewTopicLiteral
	}
	step := scenarios.Step{
		UserInput:    probeUserInput,
		MockResponse: scenarios.NewMockResponseWithTag([]string{carrierTag}, Q, "abandoned-topic probe."),
		Annotation:   fmt.Sprintf("turn %d: wander-probe %s hop=%d", g.stepIndex+1, target.threadID(), hop),
		ClosureAck:   &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:    &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
	}
	// The carrier is deliberately NOT entered into the generator's Layer-B
	// model (no g.engage): it is a measurement artifact, not a topical
	// thread, so it must not displace genuine threads from the
	// switch/resume candidate pool. The RUNTIME still engages it (the
	// MockResponse threads it), which excludes it from its own turn's
	// recall scan via engagedSet — exactly the coherence the probe needs.
	return bufStep{
		step:       step,
		slotIdx:    g.threads[engageIdx].slotIdx,
		engagedIdx: engageIdx,
		// No ExpectedRecallMatches → opens no episode; probe is measured
		// solely via its hop buckets off RecallMatchFireIDs.
		probe: &wanderProbe{
			threadID:   target.threadID(),
			hops:       hop,
			predictHit: predictHit,
		},
	}, true
}

// defaultUserInput returns the engaging line for a non-user-dictated
// turn: on a recall opportunity, the slot's mad-libs query verbatim —
// its #-prefixed tags (loose cells included) drive the runtime's
// symbol extraction, and the coalesced query set is the full
// slot.Tags, firing recall against the dormant sibling's filtered
// anchors. Otherwise a plain engaging line mentioning a couple of the
// tags so the turn still extracts on-topic symbols — and on the
// new-thread case the #-prefixed mention must be a NON-LOOSE tag, so
// the §3.3 symbol extraction does not slip a loose term into the new
// thread's spine anchors via coalesce.
func defaultUserInput(slot CorpusSlot, recallOpp bool) string {
	if recallOpp {
		return slot.UserInput
	}
	mention := firstNonLooseTag(slot)
	second := slot.Tags[1%len(slot.Tags)]
	return fmt.Sprintf("working on #%s and %s", mention, second)
}

// maybeUserDictated draws against userDictatedPct on an *eligible*
// work turn (non-recall-opportunity, non-new, work-class) to decide
// whether this turn is the Realism C "user-dictated file content"
// variant. On a true draw it returns the variant's user input — a
// prompt embedding the literal line the upcoming fs.write will
// append — and ok=true; otherwise (ineligible or draw missed)
// it returns ok=false and the caller keeps the default input.
//
// The draw must happen unconditionally inside the eligibility gate so
// the rng sequence is determined by buildStep's inputs alone — same
// (Seed, Duration, Corpus) → same draws.
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
//
//	(a) user-prompt symbol extraction on file-content literals — the
//	    prompt embeds the file path, which the deterministic pass on
//	    user.prompt extracts as an identifier symbol.
//	(b) §3.0 transient-data classification — the same prompt
//	    plausibly carries decision-class (high-level #-tag intent)
//	    AND task-class (literal value via the file path arriving
//	    through fs.read/fs.write).
//	(c) §3.9.2/§3.9.4 live-window — the literal line text appears in
//	    both the user.prompt content and the fs.write content,
//	    exercising dedup across delta sources.
//
// The ok return is consulted by buildWorkPreEvents to align the
// fs.write modify line with the literal embedded in the prompt — same
// literal in both deltas is the live-window case.
func (g *generator) maybeUserDictated(tt turnType, isNew, recallOpp bool, idx int, slot CorpusSlot) (string, bool) {
	if tt != turnWork || isNew || recallOpp {
		return "", false
	}
	if g.rng.Intn(userDictatedSelector) >= userDictatedPct {
		return "", false
	}
	mention := firstNonLooseTag(slot)
	// pendingWriteCount is the writeCount the upcoming fs.write will
	// use — workFile + writeCount++ happen later in buildWorkPreEvents,
	// so peek the current value and add one. This keeps the literal in
	// the prompt byte-identical to what the modify step appends.
	fs := g.workFile(idx)
	pendingWriteCount := fs.writeCount + 1
	g.userDictatedCount++
	return fmt.Sprintf(
		"append this exact line to %s: // edit %d: %s — #%s",
		fs.path, pendingWriteCount, slot.Topic, mention), true
}

// buildMockResponse builds the §5.1-tagged mock response: *new-topic*
// for a new thread, else the engaged thread's thr_N id. The body is a
// deterministic on-topic line keyed off the slot's first two tags.
func buildMockResponse(isNew bool, threadID string, anchorTags []string, slot CorpusSlot) model.Response {
	threads := []string{threadID}
	if isNew {
		threads = []string{prompt.NewTopicLiteral}
	}
	body := fmt.Sprintf("Working through %s — %s.",
		slot.Topic, strings.Join(slot.Tags[:min(2, len(slot.Tags))], ", "))
	return scenarios.NewMockResponseWithTag(threads, anchorTags, body)
}

// buildWorkPreEvents constructs a work turn's §3.9 read/modify/write
// cycle to exercise the working-set content-dedup integration on the
// heavy turns: fs.read of the file's current content, a deterministic
// modify, then fs.write of the new content. Every commitEvery-th write
// also emits an fs.commit with a deterministic synthetic hash.
//
// On a user-dictated turn the appended line is the literal the user
// already named in the prompt — the same byte sequence appears in
// both deltas, exercising the §3.9.2/§3.9.4 live-window dedup path.
// Otherwise the agentic-edit default line is used. Mutates the
// thread's fileState (content, writeCount).
func (g *generator) buildWorkPreEvents(idx int, slot CorpusSlot, userDictated bool) []turn.Delta {
	fs := g.workFile(idx)
	preEvents := []turn.Delta{{
		Source:  memops.SourceFSRead,
		Content: fs.content,
		Meta:    map[string]string{"path": fs.path},
	}}
	fs.writeCount++
	if userDictated {
		fs.content += fmt.Sprintf("\n// edit %d: %s — #%s\n",
			fs.writeCount, slot.Topic, firstNonLooseTag(slot))
	} else {
		fs.content += fmt.Sprintf("\n// edit %d: %s\n",
			fs.writeCount, slot.Topic)
	}
	preEvents = append(preEvents, turn.Delta{
		Source:  memops.SourceFSWrite,
		Content: fs.content,
		Meta:    map[string]string{"path": fs.path},
	})
	if fs.writeCount%commitEvery == 0 {
		preEvents = append(preEvents, turn.Delta{
			Source: memops.SourceFSCommit,
			Meta: map[string]string{
				"path": fs.path,
				"hash": syntheticHash(fs.content),
			},
		})
	}
	return preEvents
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
	case actCampaign:
		return "campaign"
	default:
		return "new"
	}
}
