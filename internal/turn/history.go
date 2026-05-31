package turn

import (
	"context"
	"sort"

	"personant/internal/memops"
)

// ThreadMeta is a local alias to avoid the memops-qualified type literal
// in the createNewThread frontmatter construction. Kept at package scope
// so the literal in the function body reads naturally.
type ThreadMeta = memops.ThreadMeta

// historyCapPerThread is the v0.1 default for `history.cap-per-thread`
// (spec §2.6.1). Directive-file lookup arrives in Phase 3+; until then
// the cap is a constant.
const historyCapPerThread = 40

// frontmatterFromSpine builds a minimal-but-valid ThreadMeta from a
// SpineRecord. Used when a thread's on-disk file is missing while its
// spine entry persists — the engagement update synthesizes a fresh file
// rather than failing the turn.
func frontmatterFromSpine(rec memops.SpineRecord) ThreadMeta {
	return ThreadMeta{
		ID:              rec.ID,
		Project:         rec.Project,
		Anchors:         append([]string(nil), rec.Anchors...),
		Summary:         rec.Summary,
		Description:     rec.Description,
		State:           rec.State,
		Created:         rec.Created,
		LastEngaged:     rec.LastEngaged,
		StateChanged:    rec.StateChanged,
		TurnCount:       rec.TurnCount,
		RecallFires:     rec.RecallFires,
		LastEngagedTurn: rec.LastEngagedTurn,
	}
}

// mergeHistorySymbols folds the per-turn coalesced symbol set into an
// existing history_symbols list and enforces the cap (spec §2.3 +
// §2.6.1, default 40).
//
// For each per-turn symbol:
//   - if its normalized form already appears: increment count, upgrade
//     source per §2.7.3 dominance.
//   - else: append a new entry with first_seen_turn = currentTurn.
//
// Eviction policy when the merged list exceeds the cap: lowest count
// first; ties broken by lowest first_seen_turn (oldest among lowest).
// Spec §2.3 OPEN flags the precise weight formula as deferred to v0.1.1;
// v0.1 uses count alone. Order of return is stable: existing entries
// retain insertion order, new entries append in the input order.
func mergeHistorySymbols(existing []memops.HistorySymbol, turnSymbols []coalescedSymbol, currentTurn int) []memops.HistorySymbol {
	// Index existing by normalized for O(1) lookup.
	idx := make(map[string]int, len(existing))
	out := make([]memops.HistorySymbol, len(existing))
	copy(out, existing)
	for i, h := range out {
		idx[h.Normalized] = i
	}

	// Sort turnSymbols by normalized for deterministic append order.
	turnSorted := make([]coalescedSymbol, len(turnSymbols))
	copy(turnSorted, turnSymbols)
	sort.Slice(turnSorted, func(i, j int) bool {
		return turnSorted[i].Normalized < turnSorted[j].Normalized
	})

	for _, sym := range turnSorted {
		if i, ok := idx[sym.Normalized]; ok {
			out[i].Count++
			out[i].Source = memops.DominantSource(out[i].Source, sym.Source)
			continue
		}
		raw := sym.Raw
		if raw == "" {
			raw = sym.Normalized
		}
		out = append(out, memops.HistorySymbol{
			Raw:           raw,
			Normalized:    sym.Normalized,
			FirstSeenTurn: currentTurn,
			Count:         1,
			Source:        sym.Source,
		})
		idx[sym.Normalized] = len(out) - 1
	}

	// NOTE: eviction is deliberately NOT performed here. Per the
	// anchor-lifecycle ordering (SOLUTION §2 / Build-Plan Risk R5) the
	// sequence at owner-turn close is merge → project → evict: ProjectAnchors
	// must run on the FULL merged set first (latching EverCentral and
	// flipping supersession), and only THEN may eviction consume the
	// freshly-latched flags. A symbol that enters the projection this turn is
	// thereby protected from eviction this turn. The owner-turn caller
	// (engage.go) runs ProjectAnchors then capHistorySymbols. The merge step
	// returns the uncapped set so it cannot strand a symbol the projection is
	// about to make ever-central.
	return out
}

// populateDerivedFrom is the §2.7.3 origin-provenance population step. It
// is pure (no I/O): the caller loads the recall-surfaced threads' symbol
// sets and passes them in.
//
// For every entry in merged whose Normalized form coincides with a
// recall-surfaced thread X's symbol set, X's threadID is unioned into that
// entry's DerivedFrom. Attribution applies to both NEWLY-emitted symbols
// (the synthesis-carry-in case) AND re-emitted existing symbols that newly
// coincide with a recall this turn — origins accumulate, never clear
// (monotonic), and the union is idempotent for an origin already recorded.
// Because the union is applied to EVERY coinciding entry (spec §2.7.3:
// "origins accumulate, never clear"), newness is not a gate — the per-symbol
// monotonic union is the whole rule. selfID (the thread these symbols belong
// to) is never attributed to itself — a thread is not its own origin.
//
// DerivedFrom is kept sorted + deduplicated so the YAML/JSON serialization
// is git-diff-stable and runs are deterministic. The mutation is in place
// on merged's entries.
func populateDerivedFrom(merged []memops.HistorySymbol, surfaced map[string]map[string]struct{}, selfID string) {
	if len(surfaced) == 0 {
		return
	}
	for i := range merged {
		norm := merged[i].Normalized
		if norm == "" {
			continue
		}
		for originID, symSet := range surfaced {
			if originID == selfID {
				continue // a thread is never its own origin
			}
			if _, ok := symSet[norm]; !ok {
				continue
			}
			merged[i].DerivedFrom = unionSorted(merged[i].DerivedFrom, originID)
		}
	}
}

// unionSorted returns the sorted, deduplicated union of existing and the
// single value v. The input slice is not mutated (a fresh slice is returned
// when v is absent; existing is returned as-is when v is already present).
func unionSorted(existing []string, v string) []string {
	i := sort.SearchStrings(existing, v)
	if i < len(existing) && existing[i] == v {
		return existing // already present
	}
	out := make([]string, 0, len(existing)+1)
	out = append(out, existing[:i]...)
	out = append(out, v)
	out = append(out, existing[i:]...)
	return out
}

// surfacedSymbolSets loads the recall-surfaced threads' normalized symbol
// sets — the I/O half of derived_from population, kept out of the pure
// populateDerivedFrom. For each surfaced thread it returns the set the §3.4
// recall scorer matches against: Anchors ∪ {h.Normalized for h in
// history_symbols}. This MIRRORS recall/scoring.buildThreadSet's notion of a
// thread's symbol set; it is replicated (not imported) because that function
// is unexported and returns a superseded-map second value this caller does
// not need — the replication is a few lines and avoids exporting scoring
// internals. A thread that fails to load is skipped (best-effort; provenance
// is opportunistic and must never abort turn close); selfID is never loaded
// (a thread is not its own origin).
//
// Eligibility (§2.7.3 "Deterministic population") is recallSurfaced ∩ Layer-B
// residency: a recall-promoted parent is an eligible origin only while it is
// still resident in state.ActiveThreads (Layer B). Once it is evicted from the
// working set — displaced past BTopK by other engagements — it is no longer in
// context and must not be newly attributed; provenance stays honest, not
// "ever-recalled". recallSurfaced remains the "arrived via recall, not direct
// engagement/switch" marker (not every ActiveThreads member is recall-sourced);
// ActiveThreads membership is the residency gate. Opportunistic pruning: ids in
// recallSurfaced that are no longer resident are dropped here — they cannot
// become eligible again without a fresh recall, which re-adds them — bounding
// the set over a long session.
func surfacedSymbolSets(ctx context.Context, state *State, selfID string) map[string]map[string]struct{} {
	if len(state.recallSurfaced) == 0 {
		return nil
	}
	resident := residentRecallOrigins(state)
	out := make(map[string]map[string]struct{}, len(resident))
	for id := range resident {
		if id == selfID {
			continue
		}
		// LoadThreadMeta reads only the metadata block (anchors +
		// history_symbols) — no body assembly — which is exactly the
		// scorer's match surface and cheaper than LoadThread.
		meta, err := state.Ops.LoadThreadMeta(ctx, id)
		if err != nil {
			continue // best-effort: a thread that won't load is not an origin
		}
		set := make(map[string]struct{}, len(meta.Anchors)+len(meta.HistorySymbols))
		for _, a := range meta.Anchors {
			if a != "" {
				set[a] = struct{}{}
			}
		}
		for _, h := range meta.HistorySymbols {
			if h.Normalized != "" {
				set[h.Normalized] = struct{}{}
			}
		}
		if len(set) > 0 {
			out[id] = set
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// residentRecallOrigins returns the eligible-origin set: recallSurfaced ∩
// Layer-B residency (state.ActiveThreads). It also opportunistically prunes
// state.recallSurfaced of ids that are no longer resident — they cannot become
// eligible again without a fresh recall (which re-adds them via recall.go), so
// dropping them here bounds the map over a long session. Deterministic: the
// returned set and the post-prune recallSurfaced depend only on the two input
// sets, not on iteration order.
func residentRecallOrigins(state *State) map[string]struct{} {
	active := make(map[string]struct{}, len(state.ActiveThreads))
	for _, id := range state.ActiveThreads {
		active[id] = struct{}{}
	}
	resident := make(map[string]struct{}, len(state.recallSurfaced))
	for id := range state.recallSurfaced {
		if _, ok := active[id]; ok {
			resident[id] = struct{}{}
		} else {
			delete(state.recallSurfaced, id) // evicted from Layer B → no longer eligible
		}
	}
	return resident
}

// capHistorySymbols enforces the §2.6.1 hard total cap (default 40) over the
// active+superseded set, applying the union-protection eviction predicate.
// It is the post-projection step of the merge → project → evict sequence:
// callers run ProjectAnchors first so the EverCentral flags eviction reads
// are already latched for this turn (Build-Plan Risk R5).
func capHistorySymbols(syms []memops.HistorySymbol) []memops.HistorySymbol {
	if len(syms) <= historyCapPerThread {
		return syms
	}
	return evictLowestWeight(syms, historyCapPerThread)
}

// evictLowestWeight returns out with the lowest-cumulative-weight entries
// removed until len == cap, while protecting high-specificity identifiers
// (MAD B11). Weight = count alone in v0.1; ties broken by highest
// first_seen_turn (newest survives when counts tie). Stable ordering of
// the survivors is preserved.
//
// B11: within-thread Count is anti-correlated with recall discrimination
// — generic words ("system", "data") accrue high Count yet appear in every
// thread (near-zero discrimination), while a specific identifier (a UUID,
// file path, URL, git SHA) appears once (low Count) but nowhere else
// (maximal discrimination). Pure Count-eviction therefore discards exactly
// the symbols that make THIS thread recallable. The protection predicate is
// the UNION EverCentral || IsHighSpecificity(Raw) (anchor-lifecycle Inc 2,
// SOLUTION §2 A5+C3): a once-central premise (the abandoned-premise recall
// handle) is retained alongside intrinsically discriminative identifiers.
// IsHighSpecificity is re-derived from the persisted Raw, NOT Source (see
// that function's doc — authority is not specificity). The evictable set is
// dropped first. Only if the protected set alone exceeds the cap do we
// degrade gracefully — evicting lowest-Count SUPERSEDED entries first (an
// abandoned premise yields the cap before a still-protected active one),
// then by the same Count/FirstSeenTurn rule: the cap is a hard storage
// bound (§2.3) and is never exceeded.
//
// A symbol is NEVER evicted while EverCentral so long as the protected set
// fits the cap; under the degradation branch the superseded-first rule
// still preferentially keeps active ever-central symbols.
func evictLowestWeight(out []memops.HistorySymbol, cap int) []memops.HistorySymbol {
	if len(out) <= cap {
		return out
	}

	// Partition original indices into protected (EverCentral OR
	// high-specificity Raw — the union predicate) and evictable. Iterate
	// over out to keep partitions in insertion order, which preserves stable
	// survivor ordering at the end.
	var protected, evictable []int
	for i := range out {
		if out[i].EverCentral || memops.IsHighSpecificity(out[i].Raw) {
			protected = append(protected, i)
		} else {
			evictable = append(evictable, i)
		}
	}

	keepIdx := make(map[int]struct{}, cap)
	if len(protected) >= cap {
		// Graceful degradation: protected alone overflows the cap. Evict
		// lowest-Count superseded entries first (abandoned premises yield
		// before active ever-central ones), then keep the highest-weight
		// remainder. The evictable set is discarded entirely.
		for _, i := range topProtected(out, protected, cap) {
			keepIdx[i] = struct{}{}
		}
	} else {
		// Keep all protected, then fill remaining slots with the
		// highest-weight evictable entries.
		for _, i := range protected {
			keepIdx[i] = struct{}{}
		}
		for _, i := range topByWeight(out, evictable, cap-len(protected)) {
			keepIdx[i] = struct{}{}
		}
	}

	survivors := make([]memops.HistorySymbol, 0, cap)
	for i := range out {
		if _, ok := keepIdx[i]; ok {
			survivors = append(survivors, out[i])
		}
	}
	return survivors
}

// topByWeight returns the n original indices from idxs whose entries rank
// highest by the shared ranking discipline (symbolRankLess in projection.go:
// class → Count → recency → Normalized) — the same comparator the anchor
// projection uses, so keep-the-strongest is one rule, not two (DRY). If
// n >= len(idxs) all are returned. The returned slice is the kept-index set;
// survivor ordering is re-established by the caller scanning out in
// insertion order.
func topByWeight(out []memops.HistorySymbol, idxs []int, n int) []int {
	if n >= len(idxs) {
		return idxs
	}
	ranked := make([]int, len(idxs))
	copy(ranked, idxs)
	sort.SliceStable(ranked, func(i, j int) bool {
		return symbolRankLess(&out[ranked[i]], &out[ranked[j]])
	})
	return ranked[:n]
}

// topProtected returns the n indices to KEEP under the graceful-degradation
// branch, where the protected set alone exceeds the cap. The superseded-
// first eviction sub-rule (SOLUTION §2 last sentence) means active
// ever-central symbols are preferred over superseded ones: rank superseded
// LAST regardless of weight, then by the shared discipline. Keeping the top
// n of that ranking evicts the lowest-Count superseded entries first.
func topProtected(out []memops.HistorySymbol, idxs []int, n int) []int {
	if n >= len(idxs) {
		return idxs
	}
	ranked := make([]int, len(idxs))
	copy(ranked, idxs)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := &out[ranked[i]], &out[ranked[j]]
		aSup := a.Lifecycle == memops.LifecycleSuperseded
		bSup := b.Lifecycle == memops.LifecycleSuperseded
		if aSup != bSup {
			return !aSup // active sorts ahead of superseded
		}
		return symbolRankLess(a, b)
	})
	return ranked[:n]
}
