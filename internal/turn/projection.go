package turn

import (
	"sort"

	"personant/internal/memops"
)

// ProjectAnchors re-derives a thread's headline anchor set as a
// deterministic projection of its history_symbols by current salience
// (spec §2.2 / §2.7.4; anchor-lifecycle Inc 2 / C1). It ranks ALL retained
// symbols, takes the top-max as the projected anchor set, latches lifecycle
// state on the mutated symbol slice, and reports whether the projected SET
// changed.
//
// Ranking discipline (shared with topByWeight via symbolRankLess):
//   - class: B11 high-specificity OR ever-central symbols rank above
//     ordinary symbols (the retention discriminator);
//   - then Count descending;
//   - then recency — most-recent LastActiveTurn, then FirstSeenTurn, both
//     descending (newest first);
//   - then Normalized ascending as a stable final tiebreak.
//
// Candidacy is current salience, not lifecycle: ALL retained symbols —
// active and superseded alike — compete on rank for the top-max slots.
// "Superseded" is a descriptive / eviction-priority label, never a
// candidacy gate.
//
// Lifecycle transitions (deterministic, no TTL — SOLUTION C2) fall out of
// the ranking, they are not driven by it:
//   - a symbol that enters the active projection this turn latches
//     EverCentral=true and has its LastActiveTurn set to turn;
//   - a symbol that WAS ever-central but falls out of the top-max
//     projection flips to LifecycleSuperseded (rank-dropout);
//   - a previously-superseded symbol whose salience has since risen
//     re-ranks into the top-max and is thereby flipped back to active. This
//     reappearance is EMERGENT from current-salience ranking — there is no
//     special "return to active" pathway, just the same entry transition.
//
// Returns the projected anchor list (the symbols' Normalized forms, in
// rank order, length <= max), the mutated symbol slice (a copy — the input
// is not modified), and changed=true iff the projected anchor set differs
// from prior. The prior projected set is reconstructed from the pre-call
// lifecycle state (the active symbols ranked and capped to max — the same
// shape the projection stores), so the idempotent-write guard fires only
// when the headline genuinely moves.
func ProjectAnchors(syms []memops.HistorySymbol, max, turn int) (anchors []string, updated []memops.HistorySymbol, changed bool) {
	updated = make([]memops.HistorySymbol, len(syms))
	copy(updated, syms)

	if max < 0 {
		max = 0
	}

	// Prior projected set: the symbols the spine currently holds,
	// reconstructed from pre-call lifecycle. A symbol was in the prior
	// headline iff it is EverCentral AND active — EverCentral latches on
	// projection entry and a superseded symbol has dropped out, so this is
	// exactly the stored active slice. A freshly-merged never-projected
	// symbol is active-by-default but NOT ever-central, so it correctly does
	// not count toward prior (its arrival is a genuine change). Rank-capped
	// to max to match the projection's cardinality.
	priorActive := make([]int, 0, len(updated))
	for i := range updated {
		if updated[i].EverCentral && isActive(updated[i].Lifecycle) {
			priorActive = append(priorActive, i)
		}
	}
	prior := rankedNormalized(updated, priorActive, max)
	priorSet := toSet(prior)

	// Candidate set for the new projection: ALL retained symbols, active and
	// superseded alike. The projection is a pure function of current salience
	// (rank), not of lifecycle label — "superseded" is a descriptive /
	// eviction-priority marker, never a candidacy gate. A superseded symbol
	// whose Count has since risen competes on equal footing and, if it
	// re-ranks into the top-max, is re-admitted by the transition logic below
	// (flipped back to active). Reappearance is therefore EMERGENT from
	// current-salience ranking, not a special "return to active" pathway.
	candidates := make([]int, 0, len(updated))
	for i := range updated {
		candidates = append(candidates, i)
	}
	top := topRanked(updated, candidates, max)

	// Build the projected anchor list and the in-projection index set.
	inProjection := make(map[int]struct{}, len(top))
	anchors = make([]string, 0, len(top))
	for _, i := range top {
		if updated[i].Normalized == "" {
			continue
		}
		inProjection[i] = struct{}{}
		anchors = append(anchors, updated[i].Normalized)
	}

	// Apply lifecycle transitions over the full symbol set.
	for i := range updated {
		_, in := inProjection[i]
		switch {
		case in:
			// Entered (or remained in) the active projection: latch
			// EverCentral, refresh recency, ensure active lifecycle.
			updated[i].EverCentral = true
			updated[i].LastActiveTurn = turn
			updated[i].Lifecycle = memops.LifecycleActive
		case updated[i].EverCentral && updated[i].Lifecycle != memops.LifecycleSuperseded:
			// Ever-central but no longer in the top-max projection:
			// rank-dropout supersession (no TTL).
			updated[i].Lifecycle = memops.LifecycleSuperseded
		}
	}

	changed = !setsEqual(priorSet, toSet(anchors))
	return anchors, updated, changed
}

// isActive reports whether a lifecycle value is the active state. The zero
// value "" is active (back-compatible — SOLUTION §8).
func isActive(l memops.SymbolLifecycle) bool {
	return l == memops.LifecycleActive || l == ""
}

// symbolRankLess is the shared ranking comparator (DRY: the discipline
// reused by ProjectAnchors and topByWeight). It reports whether a should
// rank ahead of b — the projection's "more central" ordering. Class (B11
// high-specificity) ranks first, then Count descending, then recency
// (LastActiveTurn then FirstSeenTurn, newest first), then Normalized
// ascending for stability.
//
// EverCentral is deliberately NOT a ranking tier: it is the eviction-
// retention discriminator, applied as a separate protection partition in
// evictLowestWeight. If EverCentral ranked symbols up here, a once-central
// premise could never be outranked, and supersession (the inversion case —
// SOLUTION §2 C2) would be impossible. The intrinsic discrimination signal
// (B11) is the only durable class boost in the projection contest.
func symbolRankLess(a, b *memops.HistorySymbol) bool {
	ac, bc := rankClass(a), rankClass(b)
	if ac != bc {
		return ac > bc
	}
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	if a.LastActiveTurn != b.LastActiveTurn {
		return a.LastActiveTurn > b.LastActiveTurn
	}
	if a.FirstSeenTurn != b.FirstSeenTurn {
		return a.FirstSeenTurn > b.FirstSeenTurn
	}
	return a.Normalized < b.Normalized
}

// rankClass returns the projection priority class of a symbol: 1 if it is a
// B11 high-specificity identifier (intrinsically discriminative — a URL,
// file path, git SHA), 0 otherwise. A high-class symbol wins the projection
// contest against an ordinary symbol of equal Count.
func rankClass(s *memops.HistorySymbol) int {
	if memops.IsHighSpecificity(s.Raw) {
		return 1
	}
	return 0
}

// topRanked returns the up-to-n original indices from idxs ranked highest by
// symbolRankLess. If n >= len(idxs) all are returned (still rank-sorted, so
// the projected anchor list is in rank order).
func topRanked(syms []memops.HistorySymbol, idxs []int, n int) []int {
	ranked := make([]int, len(idxs))
	copy(ranked, idxs)
	sort.SliceStable(ranked, func(i, j int) bool {
		return symbolRankLess(&syms[ranked[i]], &syms[ranked[j]])
	})
	if n < len(ranked) {
		ranked = ranked[:n]
	}
	return ranked
}

// rankedNormalized returns the Normalized forms of the top-n ranked symbols
// from idxs (skipping empty Normalized), in rank order.
func rankedNormalized(syms []memops.HistorySymbol, idxs []int, n int) []string {
	out := make([]string, 0, n)
	for _, i := range topRanked(syms, idxs, n) {
		if syms[i].Normalized == "" {
			continue
		}
		out = append(out, syms[i].Normalized)
	}
	return out
}

// toSet builds a set from a string slice.
func toSet(xs []string) map[string]struct{} {
	s := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		s[x] = struct{}{}
	}
	return s
}

// setsEqual reports whether two string sets are equal.
func setsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
