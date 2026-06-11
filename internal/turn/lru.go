package turn

import (
	"context"

	"personant/internal/memops"
)

// embeddingDebtCap is the §6.5 debt window: the count of turn-excerpts a
// thread may accrue scrolled-out-of-the-assembly-window before the turn
// loop enqueues a §3.4 fine-tier flush of that thread (and resets its
// debt). At ThreadTurnWindow=512 a cap of 16 means the fine tier lags the
// assembly window by at most ~3%, and a continuously active thread
// flushes ~32 times per window's worth of scroll-out — frequent enough to
// keep recent early content recallable within a bounded lag, infrequent
// enough that the embed-call rate stays well under one batch per turn. A
// §9 calibration window, not a frozen value.
const embeddingDebtCap = 16

// EmbeddingDebt returns the count of turn-excerpts threadID has accrued
// scrolled-out-of-the-assembly-window that are NOT YET flushed to the §3.4
// fine tier — i.e. the live §6.5 debt-window depth. 0 means just-flushed (or
// unknown thread). It climbs to just under the internal flush cap between
// flushes, so an observer reads ACTUAL recallability lag instead of re-deriving
// the boundary from a copied constant.
func (s *State) EmbeddingDebt(threadID string) int {
	return s.embeddingDebt[threadID]
}

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

// recordExcerptScrollOut is the §6.2 debt-cap hook. The owner-excerpt
// write path calls it once per turn-excerpt that scrolls out of a thread's
// assembly window (a newer excerpt pushed it past the most-recent
// ThreadTurnWindow boundary — it stays retained on disk but leaves the
// assembled context). It accrues per-thread embedding debt; when debt
// reaches embeddingDebtCap it enqueues a fine-tier flush of that thread
// (passing state.TurnNumber as the I2 dispatch watermark) and resets the
// counter.
//
// A no-op when the installed Recaller does not satisfy flushEnqueuer
// (symbolic-only default → no embedder → no debt tracking, I7).
func recordExcerptScrollOut(state *State, threadID string) {
	enq, ok := state.Recaller.(flushEnqueuer)
	if !ok {
		return
	}
	if state.embeddingDebt == nil {
		state.embeddingDebt = make(map[string]int)
	}
	state.embeddingDebt[threadID]++
	if state.embeddingDebt[threadID] >= embeddingDebtCap {
		enq.EnqueueFlush(threadID, state.TurnNumber)
		state.embeddingDebt[threadID] = 0
	}
}

// flushOnDormancy is the §6.2 dormancy hook. touchActiveLRU calls it when
// a thread is demoted out of Layer B (decays out of the working set), so
// the thread's remaining debt is flushed into the fine tier before it
// becomes a pure recall target. It enqueues a flush (passing
// state.TurnNumber as the I2 dispatch watermark) and clears the thread's
// debt counter so the next active episode starts clean.
//
// A no-op when the installed Recaller does not satisfy flushEnqueuer (I7).
func flushOnDormancy(state *State, threadID string) {
	enq, ok := state.Recaller.(flushEnqueuer)
	if !ok {
		return
	}
	enq.EnqueueFlush(threadID, state.TurnNumber)
	delete(state.embeddingDebt, threadID)
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
// head of state.DormantThreads and capping that slice at
// dormantThreadsCap. Used by the §5.5 mid-turn fetch and by the §3.4
// recall-accept path (Part B).
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
// the head of state.DormantThreads, and cap the dormant slice at
// dormantThreadsCap. The single-id operation shared by both the
// per-engagement loop in updateLayerLRU and the single-promotion
// callers (promoteToLayerB).
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
	// Cap DormantThreads by count.
	if len(state.DormantThreads) > dormantThreadsCap {
		state.DormantThreads = state.DormantThreads[:dormantThreadsCap]
	}
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
