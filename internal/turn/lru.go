package turn

import (
	"context"

	"personant/internal/memops"
)

// EmbeddingDebtCap is the §6.5 debt window: the count of turn-excerpts a
// thread may accrue scrolled-out-of-the-assembly-window before the turn
// loop enqueues a §3.4 fine-tier flush of that thread (and resets its
// debt). At ThreadTurnWindow=512 a cap of 16 means the fine tier lags the
// assembly window by at most ~3%, and a continuously active thread
// flushes ~32 times per window's worth of scroll-out — frequent enough to
// keep recent early content recallable within a bounded lag, infrequent
// enough that the embed-call rate stays well under one batch per turn. A
// §9 calibration window, not a frozen value.
//
// PUBLIC CONTRACT. This value is exported because it is the boundary of the
// flush-lag dead zone the acceptance sim's B1 completeness-floor band
// legitimately consumes (internal/scenarios/sim references it directly to
// mark which probe targets only the #123 lexical floor can surface). Per
// ARCHITECTURE.md's harness↔system coupling rule, a contract value the
// harness depends on is exported and referenced — never hand-mirrored into a
// second const that can silently drift.
const EmbeddingDebtCap = 16

// flushEnqueuer is the narrow optional interface a Recaller may satisfy to
// receive §3.4 index-flush signals (design §11.3, I7). The embedding
// measure.Service implements EnqueueFlush; the symbolic-only default does
// not — so with no embedder installed the debt/dormancy hooks below are
// inert and behavior is byte-identical to today (the type assertion
// simply fails). turn signals only "this thread's retained body changed,
// flush it" with the dispatch turncount for the I2 watermark; it never
// embeds, touches vectors, or knows coarse/fine.
type flushEnqueuer interface {
	EnqueueFlush(threadID string, dispatchTurncount int)
}

// debtWindowBound is the bound for the §3.4 recall-completeness lexical pass
// (#123): the embedding-debt CAP when an embedder-capable recaller is
// installed and a thread is engaged, else 0. It is the MAX scrolled-out tail
// that may be awaiting an async fine-tier flush — the window the recall path
// lexically scans so durable, not-yet-embedded content stays findable
// continuously, closing the flush-lag dead zone.
//
// Gated on flushEnqueuer (the embedder-capable recaller) for the same reason
// the debt hooks are (I7): with no embedder there is no fine tier, so no lag
// dead zone exists and the symbolic-only path keeps its byte-identical prior
// behaviour (0 → the Service skips the debt pass entirely). The CAP, not the
// live per-thread debt depth, because a debt-cap flush resets the live counter
// to 0 while its embed is still in flight — a live-count bound would re-open
// the dead zone for the in-flight batch. The cap alone does not span that
// window either once scroll-outs continue past the flush (BD-8): the recall
// Service, which alone knows a flush is unpublished, widens the lexical scan
// by its per-thread pending-flush depth — this side always passes the plain
// cap (see measure.Request.EngagedDebtWindow).
func debtWindowBound(state *State, engagedOwner string) int {
	if engagedOwner == "" {
		return 0
	}
	if _, ok := state.Recaller.(flushEnqueuer); !ok {
		return 0
	}
	return EmbeddingDebtCap
}

// recordExcerptScrollOut is the §6.2 debt-cap hook. The owner-excerpt
// write path calls it once per turn-excerpt that scrolls out of a thread's
// assembly window (a newer excerpt pushed it past the most-recent
// ThreadTurnWindow boundary — it stays retained on disk but leaves the
// assembled context). It accrues per-thread embedding debt; when debt
// reaches EmbeddingDebtCap it fires a fine-tier flush of that thread
// (passing state.TurnNumber as the I2 dispatch watermark) and resets the
// counter.
//
// The debt accrual and the flush-cost COUNTERS (flushCalls/flushChunks) run
// unconditionally — they are the §6.5 policy's observed cost for this
// workload, which is the same whether or not an embedder is installed. Only
// the actual index DISPATCH (EnqueueFlush) is gated on flushEnqueuer: with no
// embedder there is no fine tier to fill, so the dispatch is skipped while the
// cost the policy WOULD pay is still measured (the harness reads it as the
// recall_index_flush_* gauge). Recall behavior is unchanged on the
// symbolic-only path — debtWindowBound stays embedder-gated, so the debt map's
// population here never reaches the recall path (I7).
func recordExcerptScrollOut(state *State, threadID string) {
	if state.embeddingDebt == nil {
		state.embeddingDebt = make(map[string]int)
	}
	state.embeddingDebt[threadID]++
	if state.embeddingDebt[threadID] >= EmbeddingDebtCap {
		state.flushCalls++
		state.flushChunks += state.embeddingDebt[threadID]
		if enq, ok := state.Recaller.(flushEnqueuer); ok {
			enq.EnqueueFlush(threadID, state.TurnNumber)
		}
		state.embeddingDebt[threadID] = 0
	}
}

// flushOnDormancy is the §6.2 dormancy hook. touchActiveLRU calls it when
// a thread is demoted out of Layer B (decays out of the working set), so
// the thread's remaining debt is flushed into the fine tier before it
// becomes a pure recall target. It fires a flush (passing state.TurnNumber
// as the I2 dispatch watermark) and clears the thread's debt counter so the
// next active episode starts clean.
//
// As in recordExcerptScrollOut, the flush-cost counters accrue regardless of
// embedder (the §6.5 policy fired); only the EnqueueFlush dispatch is gated.
// A dormancy flush ALWAYS fires (and counts one call), matching the dispatch
// contract: a flush re-embeds the thread's coarse body — one embed call — even
// when no excerpt debt remains, so the call cost is real with or without a
// chunk tail. flushChunks accrues the remaining debt (0 when the thread had
// nothing scrolled out).
func flushOnDormancy(state *State, threadID string) {
	state.flushCalls++
	state.flushChunks += state.embeddingDebt[threadID]
	if enq, ok := state.Recaller.(flushEnqueuer); ok {
		enq.EnqueueFlush(threadID, state.TurnNumber)
	}
	delete(state.embeddingDebt, threadID)
}

// FlushCalls returns this session's observed §6.5 fine-tier flush count: how
// many times the debt-cap or dormancy trigger fired a flush. FlushChunks
// returns the total turn-excerpts those flushes carried into the fine tier.
// Both are the §6.2 policy's OBSERVED cost — they accrue even on the
// symbolic-only path (where the embed dispatch no-ops), so the harness can
// report the real flush cost N pays instead of modeling it from the internal
// debt cap. Session-scoped; a restart rebuilds a fresh State, so the harness
// folds each session's count into the run total before discarding it.
func (s *State) FlushCalls() int  { return s.flushCalls }
func (s *State) FlushChunks() int { return s.flushChunks }

// crossProjectDecline is the single owner of the §3.2 cross-project policy
// and its decline-log convention. It reports whether a thread belonging to
// `project` must be declined because it is not the active project, logging the
// decline (LogCategoryThread + the "thr=/project=/active=" detail convention)
// when so. Cross-project engagement is reserved for Phase 5; v0.1 declines to
// touch or promote another project's thread.
//
// Shared by the two seams that resolve a tagged thr_<n> against the active
// project: the turn-close engagement resolver (resolveEngagedThread) and the
// §5.5 mid-turn fetch (fetchThreadForReprompt). `act` names the calling seam's
// log act ("engaged-cross-project" / "fetch-cross-project") so each stays
// forensically distinct while the decision + detail format live in ONE place.
// The empty project sentinel is never cross-project (a project-less legacy
// record engages under the active project, matching the prior inline check).
func crossProjectDecline(ctx context.Context, state *State, threadID, project, act string) (declined bool, err error) {
	if project == "" || project == state.ActiveProject.ID {
		return false, nil
	}
	return true, state.Ops.Log(ctx, memops.LogCategoryThread, act,
		"thr="+threadID+" project="+project+" active="+state.ActiveProject.ID)
}

// fetchThreadForReprompt loads thread thrID from disk, fires a
// thread.fetched context delta, and promotes the thread into
// state.ActiveThreads (de-duped, capped by Budget.BTopK; overflow
// demotes the tail to the head of state.DormantThreads).
//
// On a load error (missing file or otherwise unloadable), logs
// thread.fetch-miss and returns false — the caller skips that thread
// and proceeds with whichever others succeeded.
//
// fetchThreadForReprompt deliberately does not add thrID to
// coalesce.threads: engagement is owed by the second response's tag
// (which the model emits against the augmented context), not by the
// fetch action itself.
func fetchThreadForReprompt(ctx context.Context, state *State, thrID string) bool {
	thr, err := state.Ops.LoadThread(ctx, thrID)
	if err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryThread, "fetch-miss",
			"thr="+thrID+" err="+memops.SanitizeDetail(err.Error()))
		return false
	}
	// §3.2 cross-project decline (parity with the turn-close engagement
	// resolver, resolveEngagedThread): a mid-turn fetch never promotes a
	// thread that belongs to another project. Cross-project engagement is
	// reserved for Phase 5; v0.1 declines. Checked BEFORE the thread.fetched
	// delta so a foreign thread's body never enters the context/coalesce, and
	// before promoteToLayerB so it never reaches the working set. A log error
	// is swallowed (as on the fetch-miss path) — the decline still holds.
	if declined, _ := crossProjectDecline(ctx, state, thrID, thr.Meta.Project, "fetch-cross-project"); declined {
		return false
	}
	if err := onContextDelta(ctx, state, Delta{
		Source:  memops.SourceThreadFetched,
		Content: thr.Body,
		Meta:    map[string]string{"thr": thrID},
	}); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryThread, "fetch-miss",
			"thr="+thrID+" err="+memops.SanitizeDetail(err.Error()))
		return false
	}

	promoteToLayerB(state, thrID)
	return true
}

// promoteToLayerB promotes thrID into state.ActiveThreads at the front
// (de-duped, capped by Budget.BTopK), demoting the displaced tail to the
// head of state.DormantThreads. The dormant slice is bounded by Layer C's
// byte budget at render time (workset.Compose), not by a count cap (the
// v0.1 count cap was dropped in #127). Used by the §5.5 mid-turn fetch
// and by the §3.4 recall-accept path (Part B).
//
// This is the shared recall-promotion chokepoint, so it owns the §2.7.3
// recallSurfaced marking for BOTH callers: fetchThreadForReprompt (§5.5
// mid-turn fetch) and applyRecallResolution (§3.4 recall-accept). Marking
// here makes each path record the origin at its natural turn-lifecycle
// point — accept runs at turn-close (after the merge), so its attribution
// lands one turn later; a mid-turn fetch runs before the merge, so its
// attribution lands same-turn — timing falls out of residency for free.
// touchActiveLRU is NOT routed through here (updateLayerLRU calls it
// directly for engaged threads), so engagement never marks recallSurfaced.
func promoteToLayerB(state *State, thrID string) {
	if state.recallSurfaced == nil {
		state.recallSurfaced = make(map[string]struct{})
	}
	state.recallSurfaced[thrID] = struct{}{}
	touchActiveLRU(state, thrID)
}

// touchActiveLRU is the §3.1 Layer B/C LRU primitive: lift thrID to the
// front of state.ActiveThreads, demoting any Budget.BTopK overflow to
// the head of state.DormantThreads. The dormant slice is bounded by
// Layer C's byte budget at render time (workset.Compose), not a count
// cap (#127 dropped the v0.1 cap). The single-id operation shared by
// both the per-engagement loop in updateLayerLRU and the
// single-promotion callers (promoteToLayerB).
func touchActiveLRU(state *State, thrID string) {
	bTopK := state.Budget.BTopK
	if bTopK <= 0 {
		bTopK = memops.DefaultBTopK
	}
	// Drop from current positions in either layer.
	state.ActiveThreads = removeString(state.ActiveThreads, thrID)
	state.DormantThreads = removeString(state.DormantThreads, thrID)
	// Insert at the front of ActiveThreads.
	state.ActiveThreads = append([]string{thrID}, state.ActiveThreads...)
	// Overflow: tail of ActiveThreads demotes to head of DormantThreads.
	// Demotion is the §6.2 dormancy trigger — a thread decaying out of the
	// working set flushes its remaining embedding debt into the §3.4 fine
	// tier so its full retained body is recall-indexed before it becomes a
	// pure recall target. Inert when no embedder is installed (I7).
	for len(state.ActiveThreads) > bTopK {
		demoted := state.ActiveThreads[len(state.ActiveThreads)-1]
		state.ActiveThreads = state.ActiveThreads[:len(state.ActiveThreads)-1]
		state.DormantThreads = append([]string{demoted}, state.DormantThreads...)
		flushOnDormancy(state, demoted)
	}
	// DormantThreads is not count-capped (#127): Layer C's byte budget
	// truncates the rendered dormant set at workset.Compose time, which is
	// the real bound and avoids the m3 silent-drop of the count cap.
}

// updateLayerLRU applies the §3.1 Layer B/C eviction policy after a
// turn's engagements are committed.
//
// For each engaged thread (in coalesce-order — map iteration is
// non-deterministic, but every thread in the slice was definitely
// touched this turn so any order is correct):
//
//   - If the thread is already in ActiveThreads, move it to the
//     front (most-recent).
//   - Otherwise, prepend it. If ActiveThreads then exceeds Budget.BTopK,
//     pop the tail and prepend it to DormantThreads.
//   - If a thread newly entering ActiveThreads is currently in
//     DormantThreads, remove it from there before inserting at front.
//
// Threads not engaged this turn are left in place; decay is by
// overflow at the next engagement (per §3.1's "no per-thread decay
// counter; eviction is bounded by budget pressure"). DormantThreads
// is capped by count to keep the slice bounded across a long session.
func updateLayerLRU(state *State, engaged []string) {
	for _, id := range engaged {
		// An engaged thread has started a fresh idle clock; any
		// §3.5 defer-suppression grace from a prior idle episode is
		// now meaningless, so prune it to avoid wrongly suppressing a
		// future legitimate re-prompt.
		delete(state.closureDeferUntil, id)
		touchActiveLRU(state, id)
	}
}

// removeString returns ss with the first occurrence of v removed. ss
// is unmodified; the result aliases its tail when v is found at the
// head, otherwise allocates a fresh slice. Order-preserving.
func removeString(ss []string, v string) []string {
	for i, s := range ss {
		if s == v {
			out := make([]string, 0, len(ss)-1)
			out = append(out, ss[:i]...)
			out = append(out, ss[i+1:]...)
			return out
		}
	}
	return ss
}
