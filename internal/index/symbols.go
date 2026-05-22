// Package index regenerates the derived files described in spec §2.1
// (symbols.jsonl and projects/prj_<n>/digest.json) from canonical sources.
//
// Symbol-index build: for each normalized symbol, aggregate the threads
// in which it appears, distinguishing the "anchor" relation (curator-
// selected, recorded in SpineRecord.Anchors) from the broader "history"
// relation (any HistorySymbol in a thread's frontmatter). The two
// canonical inputs:
//
//   - spine.jsonl       — SpineRecord.Anchors per thread (curator picks)
//   - threads/thr_<n>.md — ThreadMeta.HistorySymbols per thread
//
// For each emitted SymbolRecord (spec §2.4):
//
//   - threads:         thread IDs whose anchors OR history_symbols include
//     this symbol (deduped union); sorted by descending
//     recall_fires of the referenced thread, lexical id
//     ties.
//   - anchor_in:       subset of threads where the symbol is a curator-
//     selected anchor; same sort.
//   - source_dominant: the highest-rank source observed across all
//     (thread, symbol) emissions, applying §2.7.3
//     (curator > user > model > deterministic). Anchors
//     contribute SourceCurator; history_symbols
//     contribute their stored Source.
package index

import (
	"fmt"
	"sort"

	"personant/internal/memops"
	"personant/internal/store"
)

// BuildSymbols computes the SymbolRecord set implied by the given spine
// records and thread frontmatter. Pure: no I/O, no globals.
//
// Output is sorted lexically by symbol. Per-record `threads` and
// `anchor_in` lists are sorted by descending recall_fires of the
// referenced thread (most-recalled first), with ties broken by lexical
// thread ID for determinism (spec §2.4).
//
// A symbol observed only in history_symbols (never an anchor) appears
// with anchor_in empty and source_dominant set to the highest-rank
// source seen across its history-symbol emissions. A symbol observed
// only as an anchor appears with anchor_in == threads and
// source_dominant == SourceCurator.
func BuildSymbols(spine []memops.SpineRecord, threads []memops.ThreadMeta) []store.SymbolRecord {
	// recall_fires lookup for ordering threads within a SymbolRecord.
	// Built from spine records (the canonical source for engagement
	// counters); thread frontmatter mirrors the same value but spine is
	// authoritative. A thread referenced via history_symbols but absent
	// from spine ranks 0 (treated as least-recalled).
	recallFires := make(map[string]int, len(spine))
	for _, r := range spine {
		recallFires[r.ID] = r.RecallFires
	}

	// Per-symbol bucket: set of threads (any relation), set of threads
	// where the symbol is an anchor (subset), and the running dominant
	// source per §2.7.3 across all (thread, symbol) emissions.
	type bucket struct {
		threads        map[string]struct{}
		anchorIn       map[string]struct{}
		supersededIn   map[string]struct{}
		sourceDominant memops.SymbolSource
	}
	get := func(buckets map[string]*bucket, sym string) *bucket {
		b, ok := buckets[sym]
		if !ok {
			b = &bucket{
				threads:      make(map[string]struct{}),
				anchorIn:     make(map[string]struct{}),
				supersededIn: make(map[string]struct{}),
			}
			buckets[sym] = b
		}
		return b
	}

	buckets := make(map[string]*bucket)

	// Anchors → SourceCurator. Anchors are stored on the spine record
	// already in normalized form (SpineRecord.Anchors values are the
	// same canonical form used for symbol equality).
	for _, r := range spine {
		for _, a := range r.Anchors {
			b := get(buckets, a)
			b.threads[r.ID] = struct{}{}
			b.anchorIn[r.ID] = struct{}{}
			b.sourceDominant = memops.DominantSource(b.sourceDominant, memops.SourceCurator)
		}
	}

	// History symbols → use the stored Source per emission. Keyed by
	// HistorySymbol.Normalized (the canonical form); Raw is irrelevant
	// for index keying.
	for _, t := range threads {
		for _, h := range t.HistorySymbols {
			if h.Normalized == "" {
				continue
			}
			b := get(buckets, h.Normalized)
			b.threads[t.ID] = struct{}{}
			if h.Lifecycle == memops.LifecycleSuperseded {
				b.supersededIn[t.ID] = struct{}{}
			}
			b.sourceDominant = memops.DominantSource(b.sourceDominant, h.Source)
		}
	}

	out := make([]store.SymbolRecord, 0, len(buckets))
	for sym, b := range buckets {
		threadIDs := sortedThreadIDs(b.threads, recallFires)
		anchorIDs := sortedThreadIDs(b.anchorIn, recallFires)
		supersededIDs := sortedThreadIDs(b.supersededIn, recallFires)
		out = append(out, store.SymbolRecord{
			Symbol:         sym,
			Threads:        threadIDs,
			AnchorIn:       anchorIDs,
			SupersededIn:   supersededIDs,
			SourceDominant: b.sourceDominant,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// sortedThreadIDs returns the keys of set as a slice ordered by
// descending recall_fires (per spec §2.4), with lexical ID as the
// tiebreaker. Returns a non-nil empty slice when set is empty so the
// caller can rely on a stable, JSON-encodable shape.
func sortedThreadIDs(set map[string]struct{}, recallFires map[string]int) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ri, rj := recallFires[ids[i]], recallFires[ids[j]]
		if ri != rj {
			return ri > rj // descending
		}
		return ids[i] < ids[j] // ties: lexical
	})
	return ids
}

// RebuildSymbols reads spine.jsonl and threads/*.md, builds the symbol
// index, and writes it atomically to symbols.jsonl. Returns the records
// that were written so callers (e.g. Check) can compare without
// re-reading.
//
// Thread-frontmatter parse errors are tolerated: an unreadable thread
// file is skipped and the build proceeds with the remaining inputs.
// Hard validation is the job of `personant verify`. The logger is nil
// here (RebuildSymbols is library-level and not always wired to
// eventlog); cmd-level callers that want skip diagnostics should call
// LoadAllThreadFrontmatter directly with their own logger.
func RebuildSymbols(paths store.PersonantPaths) ([]store.SymbolRecord, error) {
	spine, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("rebuild symbols: read spine: %w", err)
	}
	threads, err := store.LoadAllThreadFrontmatter(paths, nil)
	if err != nil {
		return nil, fmt.Errorf("rebuild symbols: load threads: %w", err)
	}
	records := BuildSymbols(spine, threads)
	if err := store.WriteSymbols(paths.Symbols, records); err != nil {
		return nil, fmt.Errorf("rebuild symbols: write: %w", err)
	}
	return records, nil
}
