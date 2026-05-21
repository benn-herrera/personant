package turn

import (
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

	if len(out) <= historyCapPerThread {
		return out
	}
	return evictLowestWeight(out, historyCapPerThread)
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
// the symbols that make THIS thread recallable. The fix partitions on
// re-derived specificity (memops.IsHighSpecificity over the persisted Raw,
// NOT Source — see that function's doc) and evicts the evictable set
// first. Only if the protected set alone exceeds the cap do we evict among
// protected by the same Count/FirstSeenTurn rule: the cap is a hard
// storage bound (§2.3) and is never exceeded.
func evictLowestWeight(out []memops.HistorySymbol, cap int) []memops.HistorySymbol {
	if len(out) <= cap {
		return out
	}

	// Partition original indices into protected (high-specificity Raw)
	// and evictable. Iterate over out to keep partitions in insertion
	// order, which preserves stable survivor ordering at the end.
	var protected, evictable []int
	for i := range out {
		if memops.IsHighSpecificity(out[i].Raw) {
			protected = append(protected, i)
		} else {
			evictable = append(evictable, i)
		}
	}

	keepIdx := make(map[int]struct{}, cap)
	if len(protected) >= cap {
		// Graceful degradation: protected alone overflows the cap. Keep
		// the highest-weight protected entries and drop the rest; the
		// evictable set is discarded entirely.
		for _, i := range topByWeight(out, protected, cap) {
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
// highest by the v0.1 weight rule: highest Count first, ties broken by
// highest FirstSeenTurn (newest survives). If n >= len(idxs) all are
// returned. The returned slice is the kept-index set; survivor ordering is
// re-established by the caller scanning out in insertion order.
func topByWeight(out []memops.HistorySymbol, idxs []int, n int) []int {
	if n >= len(idxs) {
		return idxs
	}
	ranked := make([]int, len(idxs))
	copy(ranked, idxs)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := &out[ranked[i]], &out[ranked[j]]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.FirstSeenTurn > b.FirstSeenTurn
	})
	return ranked[:n]
}
