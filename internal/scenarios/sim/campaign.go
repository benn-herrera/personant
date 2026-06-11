package sim

import (
	"fmt"
	"strings"

	"personant/internal/prompt"
	"personant/internal/scenarios"
	"personant/internal/turn"
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

// Recall bucket names — the lifecycle oracles fed off per-step feedback.
const (
	bucketDriftOrigin      = "drift_recall_origin"
	bucketDriftDest        = "drift_recall_dest"
	bucketAbandonedPremise = "abandoned_premise_recall"
)

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
	g.recordEmission(idx, extractedSymbolsFor(userInput, anchorTags), transientRateFor(tt))

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
	g.recordEmission(engageIdx, extractedSymbolsFor(userInput, querySymbols), transientRateFor(tt))
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
