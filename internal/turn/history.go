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
			out[i].Source = upgradeSource(out[i].Source, sym.Source)
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

// upgradeSource is the persistent-history analogue of the per-turn
// coalesce path — same §2.7.3 precedence rule
// (curator > user > model > deterministic), applied on cumulative
// history when merging an incoming observation into an existing
// HistorySymbol entry. Delegates to memops.DominantSource so the rule
// has exactly one definition site.
func upgradeSource(existing, incoming memops.SymbolSource) memops.SymbolSource {
	return memops.DominantSource(existing, incoming)
}

// evictLowestWeight returns out with the lowest-cumulative-weight entries
// removed until len == cap. Weight = count alone in v0.1; ties broken by
// lowest first_seen_turn (evict oldest among lowest-count). Stable
// ordering of the survivors is preserved.
func evictLowestWeight(out []memops.HistorySymbol, cap int) []memops.HistorySymbol {
	if len(out) <= cap {
		return out
	}
	type indexed struct {
		idx int
		ref *memops.HistorySymbol
	}
	scored := make([]indexed, len(out))
	for i := range out {
		scored[i] = indexed{idx: i, ref: &out[i]}
	}
	// Sort: highest count first; ties broken by highest first_seen_turn
	// (newest survives when counts tie).
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i].ref, scored[j].ref
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.FirstSeenTurn > b.FirstSeenTurn
	})
	keepIdx := make(map[int]struct{}, cap)
	for i := range cap {
		keepIdx[scored[i].idx] = struct{}{}
	}
	survivors := make([]memops.HistorySymbol, 0, cap)
	for i := range out {
		if _, ok := keepIdx[i]; ok {
			survivors = append(survivors, out[i])
		}
	}
	return survivors
}
