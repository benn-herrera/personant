package sim

import (
	"fmt"
	"strings"

	"personant/internal/prompt"
	"personant/internal/scenarios"
	"personant/internal/turn"
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

// wanderProbe is the per-step metadata of an abandoned-topic probe. The
// probe issues a query on the probed thread's EARLIER (abandoned)
// trajectory slot and is scored by hop distance — how far the queried
// slot sits behind the thread's current slot in its trajectory.
type wanderProbe struct {
	threadID   string // the probed (wandering, dormant) thread's runtime id
	hops       int    // current-traj-index − queried-slot-traj-index (>=1)
	predictHit bool   // oracle prediction: simJaccard(Q, retained) >= threshold
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
		if g.isCarrier(i) || g.isMainThread(i) {
			// A measurement carrier is never a genuine probe target; the
			// Candidate-A main thread is measured by the intra-thread probe, not
			// the 40-symbol-bounded abandoned-topic wander probe (its retained
			// set is unbounded by design — isMainThread).
			continue
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
	g.recordEmission(engageIdx, extractedSymbolsFor(probeUserInput, Q), convoTransientPct)

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
