package sim

import (
	"fmt"
	"strings"

	"personant/internal/prompt"
	"personant/internal/recall/scoring"
	"personant/internal/scenarios"
	"personant/internal/store"
	"personant/internal/turn"
)

// Candidate-A long-running-thread + intra-thread probe tuning (§9.1/§8.2,
// #109). The main thread is the never-retired, deep-trajectory thread whose
// excerpt count grows into the thousands across the rung, driving FIFO
// scroll-out and debt-cap flushes — the intra-thread (#109) target. Its
// engagements and probes are injected as EXTRA, rng-FREE steps (the
// wanderProbe discipline), so the canonical action stream and the
// determinism contract are untouched.
const (
	// mainThreadEngageEvery is the emitted-step cadence at which the
	// generator injects a main-thread engagement. Every cadence steps one
	// extra owner-engagement of the main thread fires, appending one
	// turn-excerpt (one shadow chunk). At ~430 turns/sim-day a cadence of 2
	// yields ~215 main-thread excerpts/day → past ThreadTurnWindow (512) in
	// ~2.5 sim-days and into the thousands across the 15d→120d ladder, the
	// continuous-scroll-out + debt-flush regime #109 targets. Seed value,
	// tunable on the rung walk.
	mainThreadEngageEvery = 2

	// mainThreadWanderEvery is how many main-thread engagements pass between
	// wanders to a fresh, distinct-topic slot. The main thread is the
	// UNBOUNDED-trajectory variant (§9.1): unlike a normal thread capped at
	// wanderMaxHops, it accretes a new topical slot every mainThreadWanderEvery
	// engagements for life, so its early content is vocabulary-distinct from
	// its current content — the realistic intra-thread drift-recovery case.
	// 24 engagements/topic ≈ a sustained dwell before the topic moves on.
	mainThreadWanderEvery = 24

	// mainThreadProbeEvery is the emitted-step cadence at which the generator
	// injects an intra-thread probe (§8.2): a query re-issuing one of the main
	// thread's EARLIER (scrolled-out) trajectory slots, predicting whether the
	// runtime should surface the main thread via spine.intra-match-fire. Like
	// the wander probe it is rng-free and zero-TimeDelta (part of the canonical
	// stream, determinism-safe), bucketed by hop distance.
	mainThreadProbeEvery = 16

	// convoTransientPct / toolTransientPct are the PRNG keep/toss model's
	// transient RATES (#111 / design §7.3): the sim's deterministic stand-in for
	// the production §3.10.8 `lifetime:` marker. Per retained-excerpt-to-be, a
	// fixed-seed PRNG (g.trimRng, derived from cfg.Seed) marks the excerpt
	// transient with this probability; a transient excerpt is TRIMMED — it is
	// NOT retained into the fine tier (no shadow chunk), modelling keep/toss
	// shrinking the leaf set (the constant-factor slope win that composes with
	// the hierarchy's O(log n) order win). convoTransientPct (60) is the
	// conversation-turn rate; toolTransientPct (75) the tool-run rate (tool
	// output churns faster, so a larger fraction is transient). These are the §9
	// calibration starting values, tunable on the rung walk. The whole-thread
	// SYMBOL retained set (emittedSyms) is unaffected — keep/toss trims fine-tier
	// LEAVES, not history_symbols; the symbols a turn contributed stay in the
	// thread's history union regardless of whether its excerpt leaf is retained.
	convoTransientPct = 60
	toolTransientPct  = 75

	// transientSelector is the [0,100) draw cap the keep/toss PRNG compares the
	// rate against: g.trimRng.Intn(transientSelector) < rate marks transient.
	transientSelector = 100

	// keepTossSeedOffset derives the keep/toss PRNG seed from cfg.Seed (XOR) so
	// trim draws are deterministic for a given seed yet isolated from g.rng's
	// action-selection stream. An arbitrary fixed constant.
	keepTossSeedOffset = 0x6b656570 // "keep"
)

// chunkRecord is the generator's shadow of ONE fine-tier chunk: the
// turn-excerpt number the runtime assigns the engaged owner's excerpt and
// the symbol set emitted on that turn (§8.2). The ordered list
// shadowChunks[idx] is the generator's shadow of thread idx's fine tier —
// scored by the intra-thread oracle, never read from a runtime index (F6).
type chunkRecord struct {
	turnNumber int      // runtime owner-excerpt turn number (1-based, monotonic)
	slotIdx    int      // the corpus slot whose tags this turn emitted
	tags       []string // the per-turn emitted symbol set (this chunk's content)

	// transient is the PRNG keep/toss verdict for this excerpt (#111 / design
	// §7.3): true → keep/toss WOULD trim it from the fine tier. The chunk stays
	// in the shadow list regardless, because PRODUCTION OVER-RETAINS (the
	// §3.10.8 marker is unbuilt; negative constraint) — so the runtime's fine
	// tier, the intra-thread coherence oracle, and the W1 descent-vs-flat gate
	// all see the FULL retained set, byte-aligned with the runtime's turn
	// numbering. The flag drives ONLY the reported leaf-set metric
	// (fine_chunks_main = the durable subset), modelling the constant-factor
	// slope win keep/toss would deliver without desynchronizing the oracle from
	// the over-retaining runtime.
	transient bool
}

// intraProbe is the per-step metadata of an intra-thread (#109) probe. It
// re-issues an EARLIER scrolled-out trajectory slot of the main thread and
// is scored by hop distance — how many wander-boundaries back the queried
// slot sits behind the main thread's current slot.
type intraProbe struct {
	threadID   string // the probed main thread's runtime id
	hops       int    // current-traj-index − queried-slot-traj-index (>=1)
	predictHit bool   // oracle prediction: a recallable chunk clears threshold
	// turnDepth is currentTurn − targetTurn: how many turns back the
	// oracle-predicted recall target scrolled out — the TURN-DEPTH axis of the
	// #109 H2 quality curve (recall_intra_*_recall_bydepth). It is the more
	// causal multi-year-thread no-decay axis (vs. the topic-drift `hops` axis).
	// Set (>=1) only when the oracle predicted a target chunk (predictHit); 0
	// means "no predicted target turn available" → the depth tally is SKIPPED
	// for that probe (report-only characterization, never fabricate a bucket).
	turnDepth int

	// inDebtWindow marks the FLUSH-LAG DEAD ZONE probe (B1 completeness floor,
	// #123): the oracle-predicted target chunk (predictHit) scrolled out of the
	// assembly window AND sits within the most-recent turn.EmbeddingDebtCap below
	// the window floor — i.e. ThreadTurnWindow <= turnDepth < ThreadTurnWindow +
	// turn.EmbeddingDebtCap. In this band the §3.4 EMBEDDING fine tier has no
	// vector for the target yet (the debt-cap flush has not embedded it), so a
	// runtime intra hit can ONLY come from the bounded lexical completeness floor
	// (debtWindowTurns). This is the exact case the floor exists to cover and the
	// one place where surfacing the thread PROVES the floor fired (design FM2).
	// The completeness assertion (completenessFloorAsserts) requires an observed
	// hit for every such probe, and FAILS if zero were observed (non-vacuity,
	// design invariant 3). Set only when predictHit (an unpredicted probe has no
	// target turn to classify); false otherwise.
	inDebtWindow bool
}

// depthOrZero returns the turn-depth (cur − target) when the oracle predicted
// a target turn (target>0 and cur>target), else 0 (skip the depth tally — no
// predicted target, never fabricate a bucket). Result is >=1 when non-zero.
func depthOrZero(cur, target int) int {
	if target <= 0 || cur <= target {
		return 0
	}
	return cur - target
}

// inDebtWindowDepth reports whether a turn-depth (cur − target) lands strictly
// in the FLUSH-LAG DEAD ZONE: scrolled out of the assembly window
// (depth >= ThreadTurnWindow) AND within the most-recent (1+pending)×
// turn.EmbeddingDebtCap below the window floor
// (depth < ThreadTurnWindow + (1+pending)*turn.EmbeddingDebtCap). Both bounds
// reference the runtime's exported constants directly (no mirror): ThreadTurnWindow
// and turn.EmbeddingDebtCap are public contract for exactly this consumer (#126).
// In that band the §3.4 embedding fine tier has no vector yet, so a runtime intra
// hit can come ONLY from the #123 bounded lexical completeness floor — the B1
// assertion target. depth must be the predicted-target depth (>=1); 0 (no
// predicted target) is never in the dead zone.
//
// THE (1+P)×cap WIDENING (B2 / BD-8). pending is the runtime's per-thread
// pending-flush depth P at the measurement instant: the runtime's lexical floor
// widens its scan to (1+P)×cap while P flush jobs are enqueued-but-unpublished
// (Recall multiplies EngagedDebtWindow by 1+pendingFlushDepth — see
// measure.debtWindowTurns). The classifier must track that same band so the
// oracle requires a floor hit for exactly the targets the runtime's widened
// floor covers, and no more.
//
// pending SOURCE (the OBSERVABILITY gap, reported for B2). There is NO public
// runtime observable for the instantaneous per-thread P at the sim/generator
// level: measure.Service.pendingFlush is unexported (guarded by pendingMu), and
// the only sim-observable flush signal is the CUMULATIVE flushCalls/flushChunks
// counters folded from turn.State — call totals, not instantaneous depth. So the
// generator oracle passes pending=0 (see buildIntraProbeStep): on every mock and
// live-embedding acceptance rung the embedder drains each flush before the next
// probe fires (P is genuinely 0 at every measurement point), so the 1×cap band
// is exact and unchanged. The widened band is exercised only where P is KNOWN by
// construction — the slow-embedder test, whose gated wrapper holds (and OBSERVES,
// via slowEmbedder.InFlight) exactly the batches it blocks, so the pending it
// passes here is ground truth, not a model of runtime internals.
func inDebtWindowDepth(depth, pending int) bool {
	if pending < 0 {
		pending = 0
	}
	upper := store.ThreadTurnWindow + (1+pending)*turn.EmbeddingDebtCap
	return depth >= store.ThreadTurnWindow && depth < upper
}

// intraDepthBucket maps a turn-depth (>=1) to a LOG-SCALE bucket index, using
// powers of the summary-tree branching factor B (scoring.TreeBranchingFactor =
// 16) so each bucket maps to one descent level of the engaged-thread summary
// tree: d0 = depth 1..15 (leaf level), d1 = 16..255 (one internal level),
// d2 = 256..4095, d3 = 4096..65535, … — i.e. bucket = floor(log_B(depth)).
// Powers of 16 (over plain powers of 2) are chosen because turn-depth on the
// long Candidate-A thread spans 1..thousands and the recall path descends that
// exact B-ary tree, so bucket edges that track descent depth read causally
// against the cost curve. intraDepthBucketLabel renders the legible range.
func intraDepthBucket(depth int) int {
	if depth < scoring.TreeBranchingFactor {
		return 0
	}
	b := 0
	for hi := scoring.TreeBranchingFactor; depth >= hi; hi *= scoring.TreeBranchingFactor {
		b++
	}
	return b
}

// intraDepthBucketLabel renders the inclusive turn-distance range for bucket b
// (B=16): d0 → "1-15", d1 → "16-255", d2 → "256-4095", … — the legible x-axis
// label for the by-depth summary log.
func intraDepthBucketLabel(b int) string {
	lo := 1
	for i := 0; i < b; i++ {
		lo *= scoring.TreeBranchingFactor
	}
	hi := lo * scoring.TreeBranchingFactor
	if b == 0 {
		lo = 1
	}
	return fmt.Sprintf("%d-%d", lo, hi-1)
}

// mainThreadChunkCount returns the Candidate-A main thread's DURABLE (kept)
// shadow chunk count — the post-keep/toss fine-tier leaf count, the length
// axis the I3 perf-decay gate watches (§7.3: the trim shrinks n_main by the
// constant transient factor). Transient-marked chunks are excluded (they are
// the leaves keep/toss would trim). 0 when the main thread was never created.
func (g *generator) mainThreadChunkCount() int {
	if g.mainThreadIdx < 0 {
		return 0
	}
	return durableChunkCount(g.shadowChunks[g.mainThreadIdx])
}

// mainThreadTurns returns the Candidate-A main thread's RAW turn-excerpt count
// (its current turn number = len of the shadow chunk list, transient chunks
// INCLUDED — production over-retains, so the runtime's turn numbering counts
// them). This is the span's realized arithmetic the B1 completeness-floor gate
// uses to decide whether the workload could STRUCTURALLY scroll a probe target
// into the flush-lag dead zone: a dead-zone probe needs a scrolled-out target at
// turn-depth in [ThreadTurnWindow, ThreadTurnWindow+EmbeddingDebtCap), and the
// deepest depth a probe can reach is (mainThreadTurns − 1), so a span whose main
// thread never accrues that many turns cannot probe the band at all. Unlike
// mainThreadChunkCount (durable/kept, the perf-decay length axis) this is the RAW
// count, because the dead-zone depth is measured in the runtime's turn numbering,
// which the keep/toss trim does not renumber. 0 when the main thread was never
// created.
func (g *generator) mainThreadTurns() int {
	if g.mainThreadIdx < 0 {
		return 0
	}
	return len(g.shadowChunks[g.mainThreadIdx])
}

// totalFineChunks returns the total DURABLE (kept) shadow chunk count across
// all threads — the post-keep/toss fine tier's modeled size (§7.3).
func (g *generator) totalFineChunks() int {
	total := 0
	for _, chunks := range g.shadowChunks {
		total += durableChunkCount(chunks)
	}
	return total
}

// durableChunkCount counts the chunks the PRNG keep/toss model RETAINED (not
// transient) — the modeled fine-tier leaf set (§7.3).
func durableChunkCount(chunks []chunkRecord) int {
	kept := 0
	for _, c := range chunks {
		if !c.transient {
			kept++
		}
	}
	return kept
}

// isMainThread reports whether idx is the Candidate-A long-running main
// thread (§9.1, #109). The main thread's retained set is DELIBERATELY
// unbounded — its multi-year trajectory accretes thousands of turn-excerpts,
// which is the entire point of intra-thread recall. It is therefore excluded
// from the within-thread WANDER oracle (probeTarget, recordWanderMetrics) and
// the criterion (e) no-eviction shadow-max measurement, exactly as a carrier
// is: its growth is the intra-thread fine tier's reason to exist, not a
// no-eviction-coherence hazard for the symbolic abandoned-topic oracle. Its
// intra-thread recall is measured by the dedicated intra-thread probe, not the
// 40-symbol-bounded wander probe.
func (g *generator) isMainThread(idx int) bool {
	return g.mainThreadIdx >= 0 && idx == g.mainThreadIdx
}

// ensureMainThread lazily creates the Candidate-A long-running main thread
// (§9.1, #109) on its first injected engagement, bound to corpus slot 0 as
// its birth topic, and returns its creation-order index. Subsequent calls
// return the existing index. The main thread is never retired and accretes
// an unbounded multi-topic trajectory — its birth-slot binding is just the
// trajectory head. Pure (no rng): creation order is len(g.threads) at first
// use, keeping the threadID mapping intact and the canonical stream
// deterministic.
func (g *generator) ensureMainThread() int {
	if g.mainThreadIdx >= 0 {
		return g.mainThreadIdx
	}
	g.mainThreadIdx = g.createThread(len(g.threads), 0)
	return g.mainThreadIdx
}

// advanceMainThreadTrajectory moves the main thread to a fresh, distinct-
// topic slot every mainThreadWanderEvery engagements — the UNBOUNDED-
// trajectory variant (§9.1), unlike a normal thread capped at wanderMaxHops.
// It reuses nextWanderSlot (the deterministic, rng-free destination walk),
// so the main thread's early content becomes vocabulary-distinct from its
// current content over its life — the realistic intra-thread drift-recovery
// case. Pure (no rng).
func (g *generator) advanceMainThreadTrajectory(idx int) {
	if g.mainEngageCount%mainThreadWanderEvery != 0 {
		return
	}
	thr := &g.threads[idx]
	dest := nextWanderSlot(*thr, g.model.slots)
	if dest == thr.cur {
		return // degenerate single-topic corpus
	}
	thr.traj = append(thr.traj, dest)
	thr.cur = dest
}

// buildMainThreadStep injects one owner-engagement of the Candidate-A main
// thread (§9.1, #109). It is rng-FREE — every choice is a pure function of
// generator state — so it is part of the canonical stream and the
// determinism guard stays byte-identical. Each engagement emits the main
// thread's CURRENT slot tags + salt (one turn-excerpt → one shadow chunk via
// recordEmission), and the trajectory advances on the wander cadence. The
// main thread is engaged through g.engage (it is a genuine topical thread,
// not a measurement carrier), so it stays resident and its excerpt count
// grows monotonically into the thousands, driving FIFO scroll-out + debt
// flushes — the #109 regime.
func (g *generator) buildMainThreadStep() bufStep {
	idx := g.ensureMainThread()
	isNew := g.firstEngagement(idx)
	g.mainEngageCount++
	g.threads[idx].engagementCount++
	g.advanceMainThreadTrajectory(idx)

	thr := g.threads[idx]
	slot := g.model.slots[thr.cur]
	anchorTags := append(nonLooseTags(slot), saltSymbol(thr.order))
	userInput := defaultUserInput(slot, false)
	// Candidate-A engagements are conversation-class (no file edit); the
	// keep/toss model trims them at the conversation rate (§7.3), so
	// fine_chunks_main reports ~(1-convoTransientPct/100) of the emitted count.
	g.recordEmission(idx, extractedSymbolsFor(userInput, anchorTags), convoTransientPct)

	threads := []string{thr.threadID()}
	if isNew {
		threads = []string{prompt.NewTopicLiteral}
	}
	step := scenarios.Step{
		UserInput:    userInput,
		MockResponse: scenarios.NewMockResponseWithTag(threads, anchorTags, fmt.Sprintf("main thread — %s.", slot.Topic)),
		Annotation:   fmt.Sprintf("turn %d: main-thread engage %s (%s)", g.stepIndex+1, thr.threadID(), slot.Topic),
		ClosureAck:   &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:    &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
	}
	g.engage(idx, g.stepIndex)
	return bufStep{step: step, slotIdx: thr.cur, engagedIdx: idx}
}

// buildIntraProbeStep constructs one intra-thread (#109, §8.2) probe if the
// main thread has an EARLIER slot whose chunks have scrolled out of the
// assembly window; ok=false otherwise. It is rng-FREE (every choice is a
// pure function of generator state), so it is part of the canonical stream.
//
// The probe ENGAGES the main thread (unlike the wander probe, which uses a
// carrier): intra-thread recall is recall of the engaged long thread's OWN
// early content, so the main thread must be Request.Engaged for the runtime's
// §4.1-step-3 fine pass to scan its scrolled-out chunks. The query is an
// EARLIER trajectory slot's tags; the runtime fires spine.intra-match-fire
// for the main thread iff a scrolled-out, flushed chunk of that slot clears
// the fine-tier threshold.
//
// The oracle predicts a hit iff SOME shadow chunk of the queried slot is
// (a) scrolled out of the assembly window (turnNumber <= cur - window), AND
// (b) clears simRecallThreshold against the query under simJaccard, scored
//
//	PER CHUNK (the same set/union semantics the runtime applies per chunk
//	vector — §8.2 coherence).
//
// There is NO debt-window blind spot: per SPEC §3.4 the runtime now keeps
// durable scrolled-out content findable continuously through the async-flush
// lag (the bounded lexical completeness floor, #123), so the oracle asserts
// recall across the FULL scrolled-out range. A real miss of any scrolled-out
// chunk is now a divergence (a bug), not a tolerated lag.
//
// The hop distance is (current traj index − queried traj index): hop >= 1 is
// an EARLIER abandoned topic. probeIntraHopCursor round-robins so every
// bucket fills.
func (g *generator) buildIntraProbeStep() (bufStep, bool) {
	if g.mainThreadIdx < 0 {
		return bufStep{}, false
	}
	idx := g.mainThreadIdx
	thr := g.threads[idx]
	if len(thr.traj) < 2 {
		return bufStep{}, false // no earlier slot yet
	}
	cur := len(g.shadowChunks[idx]) // the main thread's current turn number

	// Round-robin an earlier hop in [1, len(traj)-1]. lastIdx is the current
	// slot's trajectory position; queriedTrajIdx sits `hop` slots earlier.
	span := len(thr.traj) - 1
	hop := 1 + g.intraProbeHopCursor%span
	g.intraProbeHopCursor++
	queriedTrajIdx := (len(thr.traj) - 1) - hop
	if queriedTrajIdx < 0 {
		return bufStep{}, false
	}
	queriedSlotIdx := thr.traj[queriedTrajIdx]
	queriedSlot := g.model.slots[queriedSlotIdx]
	Q := nonLooseTags(queriedSlot)
	if len(Q) == 0 {
		return bufStep{}, false
	}

	// Oracle prediction over the generator's own shadow chunks (F6): scan the
	// queried slot's chunks for one that is scrolled out AND clears the threshold
	// per-chunk. Salt is unioned into each chunk's set defensively (the runtime
	// folds the thread salt into history_symbols, so the per-chunk denominator
	// carries it); Q carries no salt (the query is the earlier slot's topical
	// tags only). Per SPEC §3.4 every scrolled-out chunk is recall-eligible —
	// no debt-window dead zone (#123 completeness floor).
	predictHit, anyScrolledOut := false, false
	targetTurn := 0 // turn number of the oracle-predicted recall target (0 = none)
	for _, ch := range g.shadowChunks[idx] {
		if ch.slotIdx != queriedSlotIdx {
			continue
		}
		if cur-ch.turnNumber < store.ThreadTurnWindow {
			continue // still in the assembly window — not index material (I6)
		}
		anyScrolledOut = true
		set := make(map[string]struct{}, len(ch.tags)+1)
		for _, t := range ch.tags {
			set[t] = struct{}{}
		}
		set[saltSymbol(thr.order)] = struct{}{}
		if simJaccard(Q, set) >= simRecallThreshold {
			predictHit = true
			targetTurn = ch.turnNumber // the chunk this probe should retrieve
			break
		}
	}
	if !anyScrolledOut {
		return bufStep{}, false // nothing of this slot has scrolled out yet
	}

	var mentions strings.Builder
	for i, s := range Q {
		if i > 0 {
			mentions.WriteString(" ")
		}
		mentions.WriteString("#")
		mentions.WriteString(s)
	}
	probeUserInput := "earlier in this thread, " + mentions.String()

	// Engage the main thread as owner, re-emitting the queried (earlier) slot's
	// tags. recordEmission keeps the shadow aligned with what the runtime
	// accretes; the queried slot is already in the main thread's history, so
	// this adds no foreign symbol — only a fresh (non-scrolled-out) chunk that
	// the oracle and the index both exclude from prediction (I6).
	anchorTags := append(append([]string(nil), Q...), saltSymbol(thr.order))
	g.recordEmission(idx, extractedSymbolsFor(probeUserInput, anchorTags), convoTransientPct)

	step := scenarios.Step{
		UserInput:    probeUserInput,
		MockResponse: scenarios.NewMockResponseWithTag([]string{thr.threadID()}, anchorTags, "intra-thread probe."),
		Annotation:   fmt.Sprintf("turn %d: intra-probe %s hop=%d", g.stepIndex+1, thr.threadID(), hop),
		ClosureAck:   &scenarios.ClosureAck{Outcome: turn.ClosureResolved},
		RecallAck:    &scenarios.RecallAck{Reason: turn.DeclineNotRelevant},
		// W1 recall-preservation gate (#111 / design §7.1): the harness evaluates
		// IntraThreadDivergence for the engaged main thread on this step (on an
		// embedding-live run with a usable tree) and accumulates
		// recall_intra_descent_divergence. The intra probe is exactly the
		// scrolled-out-early-content query the descent must preserve, so it is the
		// natural W1 evaluation point.
		W1Engaged: thr.threadID(),
	}
	g.engage(idx, g.stepIndex)
	return bufStep{
		step:       step,
		slotIdx:    thr.cur,
		engagedIdx: idx,
		intraProbe: &intraProbe{
			threadID:   thr.threadID(),
			hops:       hop,
			predictHit: predictHit,
			// turn-depth = currentTurn − targetTurn, available only when the
			// oracle predicted a target chunk (targetTurn>0). 0 → no predicted
			// target → the depth tally skips this probe (never fabricate a bucket).
			turnDepth: depthOrZero(cur, targetTurn),
			// B1 dead-zone classification: the predicted target chunk is in the
			// flush-lag band (scrolled out, not yet flushed → only the #123 lexical
			// floor can surface it). Only meaningful when the oracle predicted a
			// target (predictHit); inDebtWindowDepth(0, _) is false for an unpredicted
			// probe, so the && predictHit guard is belt-and-suspenders.
			// pending=0: the generator has no runtime-observable per-thread P at
			// classification time, and on every mock/live-embedding rung the
			// embedder drains each flush before the next probe fires (P is genuinely
			// 0), so the 1×cap band is exact here — see inDebtWindowDepth's
			// pending-source note.
			inDebtWindow: predictHit && inDebtWindowDepth(depthOrZero(cur, targetTurn), 0),
		},
	}, true
}
