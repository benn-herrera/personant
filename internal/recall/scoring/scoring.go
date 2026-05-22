// Package scoring implements the within-project symbolic Jaccard
// pre-filter for opportunistic thread recall (spec §3.4 layer 1).
//
// Given a set of query symbols and the current spine + thread index,
// ProposeFromIndex returns candidate threads ranked by Jaccard
// similarity of their symbol set against the query. This is the cheap
// layer; the embedding-similarity and model-judgment layers (§3.4
// layers 2 and 3) are not invoked from here.
//
// The package is substrate-free: it depends only on the memops domain
// model (SpineRecord, ThreadMeta). Application-side recall that
// needs to talk to the substrate lives in recall/measure and uses the
// memops.MemoryOps port.
package scoring

import (
	"sort"

	"personant/internal/memops"
)

// Defaults from spec §2.6.1. The 0.4 threshold mirrors
// `recall.symbolic-threshold`; 10 is the v0.1 standard cap on returned
// candidates (the embedding/model layers will narrow further when wired
// in Phase 4+).
const (
	DefaultThreshold = 0.4
	DefaultLimit     = 10
	// DefaultWeight is the superseded-symbol contribution when
	// Options.SupersededWeight is left zero. 1.0 means superseded
	// matches count identically to active matches — the legacy
	// plain-count numerator (spec §3.4, recall.superseded-weight).
	DefaultWeight = 1.0
)

// Candidate is one recall match: a thread, its Jaccard score against
// the query symbol set, and the symbols that overlap (used by the turn
// loop for surface-UI lines and structured log records).
type Candidate struct {
	ThreadID       string
	Score          float64
	MatchedSymbols []string // sorted lexically; the query ∩ thread set
}

// Options governs recall behavior. Zero-valued fields fall back to the
// spec defaults documented per-field.
type Options struct {
	// Threshold is the minimum Jaccard score for a candidate to be
	// returned. 0 → DefaultThreshold (spec §2.6.1
	// recall.symbolic-threshold default of 0.4).
	Threshold float64

	// Project restricts candidates to threads whose SpineRecord.Project
	// equals this ID. Empty string disables project filtering. v0.1's
	// standard caller passes the active project's ID; cross-project
	// recall is Phase 5+.
	Project string

	// Exclude is the set of thread IDs to omit from results — typically
	// threads already engaged in the current turn. Nil/empty → no
	// exclusion.
	Exclude map[string]struct{}

	// Limit caps the number of returned candidates. 0 → DefaultLimit
	// (10). Negative → unbounded.
	Limit int

	// SupersededWeight scales a matched symbol's contribution to the
	// Jaccard numerator when that symbol is superseded in the thread
	// (abandoned premise, still in history_symbols). 0 → DefaultWeight
	// (1.0), which is byte-identical to the legacy plain-count scorer.
	// The §9 sim's superseded_precision/abandoned_premise_recall sweep
	// selects any value below 1.0 (spec §3.4, recall.superseded-weight).
	SupersededWeight float64
}

// ProposeFromIndex is the §3.4 layer-1 symbolic Jaccard matcher. The
// caller supplies pre-loaded spine + thread frontmatter; the function
// is pure and substrate-free.
//
// Algorithm (spec §3.4 layer 1):
//
//  1. Q = unique non-empty entries of query.
//  2. For each spine record (filtered by Project + not in Exclude):
//     T(thr) = anchors ∪ {h.Normalized for h in history_symbols}.
//     Anchors-only fallback when frontmatter is missing for that ID.
//  3. score = |Q ∩ T| / |Q ∪ T|; require |Q ∩ T| > 0 (no overlap →
//     no match, regardless of threshold).
//  4. Drop scores below opts.Threshold (inclusive >=).
//  5. Sort: score desc → recall_fires desc → thread ID asc.
//  6. Apply Limit.
func ProposeFromIndex(spine []memops.SpineRecord, threads []memops.ThreadMeta, query []string, opts Options) []Candidate {
	threshold := opts.Threshold
	if threshold == 0 {
		threshold = DefaultThreshold
	}
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	supersededWeight := opts.SupersededWeight
	if supersededWeight == 0 {
		supersededWeight = DefaultWeight
	}

	q := uniqueNonEmpty(query)
	if len(q) == 0 {
		return nil
	}

	fmIndex := make(map[string]memops.ThreadMeta, len(threads))
	for i := range threads {
		fmIndex[threads[i].ID] = threads[i]
	}

	out := make([]Candidate, 0, len(spine))
	for i := range spine {
		rec := spine[i]
		if opts.Project != "" && rec.Project != opts.Project {
			continue
		}
		if _, skip := opts.Exclude[rec.ID]; skip {
			continue
		}

		fmRec, haveFM := fmIndex[rec.ID]
		threadSet, supersededSet := buildThreadSet(rec, fmRec, haveFM)
		if len(threadSet) == 0 {
			continue
		}

		matched := intersectSorted(q, threadSet)
		if len(matched) == 0 {
			// No overlap → no match, regardless of threshold setting.
			continue
		}
		// Weighted numerator (spec §3.4): each matched symbol contributes
		// 1.0 if active in this thread, supersededWeight if superseded.
		// The superseded label is canonical — frontmatter Lifecycle, not
		// the derived symbols.jsonl (R3). At weight 1.0 this equals the
		// legacy plain count, so the score is byte-identical.
		weighted := 0.0
		for _, m := range matched {
			if _, sup := supersededSet[m]; sup {
				weighted += supersededWeight
			} else {
				weighted += 1.0
			}
		}
		union := len(q) + len(threadSet) - len(matched)
		score := weighted / float64(union)
		if score < threshold {
			continue
		}

		out = append(out, Candidate{
			ThreadID:       rec.ID,
			Score:          score,
			MatchedSymbols: matched,
		})
	}

	// Build a recall-fires lookup for tie-breaking; spine is the
	// authoritative source for that field.
	recallFires := make(map[string]int, len(spine))
	for i := range spine {
		recallFires[spine[i].ID] = spine[i].RecallFires
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		ri, rj := recallFires[out[i].ThreadID], recallFires[out[j].ThreadID]
		if ri != rj {
			return ri > rj
		}
		return out[i].ThreadID < out[j].ThreadID
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// uniqueNonEmpty returns the deduplicated, non-empty entries of in. The
// returned slice is sorted lexically; downstream intersect/union
// operations exploit that ordering.
func uniqueNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// buildThreadSet returns the symbol set for a thread:
// anchors ∪ {h.Normalized for h in history_symbols} when frontmatter is
// available; anchors-only when it isn't (degraded but still valid match
// surface — the spine is canonical for anchors).
//
// It also returns the subset of that set that is superseded in this
// thread. The label source is canonical frontmatter Lifecycle (R3): a
// symbol is superseded iff it has a history_symbol whose Lifecycle is
// LifecycleSuperseded AND it is not also an anchor (the projected
// anchor set counts as active, overriding a stale history label).
//
// The set is a sorted, deduplicated slice; empty strings dropped. The
// superseded map is nil when nothing is superseded.
func buildThreadSet(rec memops.SpineRecord, fm memops.ThreadMeta, haveFM bool) ([]string, map[string]struct{}) {
	cap := len(rec.Anchors)
	if haveFM {
		cap += len(fm.HistorySymbols)
	}
	if cap == 0 {
		return nil, nil
	}
	anchorSet := make(map[string]struct{}, len(rec.Anchors))
	for _, a := range rec.Anchors {
		anchorSet[a] = struct{}{}
	}
	combined := make([]string, 0, cap)
	combined = append(combined, rec.Anchors...)
	var superseded map[string]struct{}
	if haveFM {
		for _, h := range fm.HistorySymbols {
			combined = append(combined, h.Normalized)
			if h.Lifecycle != memops.LifecycleSuperseded || h.Normalized == "" {
				continue
			}
			if _, isAnchor := anchorSet[h.Normalized]; isAnchor {
				continue
			}
			if superseded == nil {
				superseded = make(map[string]struct{})
			}
			superseded[h.Normalized] = struct{}{}
		}
	}
	return uniqueNonEmpty(combined), superseded
}

// intersectSorted returns the intersection of two sorted, deduplicated
// string slices. Output is sorted (inherits the input order).
func intersectSorted(a, b []string) []string {
	out := make([]string, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}
