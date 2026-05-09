// Package index regenerates the derived files described in spec §2.1
// (symbols.jsonl and projects/prj_<n>/digest.json) from canonical sources.
//
// TODO(phase-2-threads): the full symbols.jsonl definition (spec §2.4)
// draws from spine.jsonl AND threads/*.md frontmatter (the latter
// contributing history_symbols). Thread files use YAML frontmatter, and
// Go's stdlib has no YAML parser. Phase 1 deliberately consumes spine
// only; the spine-only index is a strict subset of the full index, and
// degenerates cleanly to "empty symbols index" on a fresh init.
//
// Consequences for SymbolRecord (spec §2.4) until thread-frontmatter
// parsing lands:
//   - threads:        thread IDs whose anchors include this symbol
//   - anchor_in:      same as threads (no history-only appearances yet)
//   - source_dominant: SourceCurator (anchors are curator-selected
//                     per spec §2.7.3)
//
// When thread-writing lands in a later phase, this builder will be
// extended to merge history_symbols, with the user's explicit input on
// the YAML parser choice.
package index

import (
	"fmt"
	"sort"

	"personant/internal/store"
)

// BuildSymbols computes the SymbolRecord set implied by the given spine
// records. Pure: no I/O, no globals.
//
// Output is sorted lexically by symbol. Per-record `threads` and
// `anchor_in` lists are sorted by descending recall_fires of the
// referenced thread (most-recalled first), with ties broken by lexical
// thread ID for determinism (spec §2.4).
func BuildSymbols(spine []store.SpineRecord) []store.SymbolRecord {
	// recall_fires lookup for ordering threads within a SymbolRecord.
	// Built once over the spine; threads not in the index will not be
	// referenced by anchors, so a missing entry is impossible by
	// construction here.
	recallFires := make(map[string]int, len(spine))
	for _, r := range spine {
		recallFires[r.ID] = r.RecallFires
	}

	// symbol -> set of thread IDs (deduped).
	type bucket struct {
		threads map[string]struct{}
	}
	buckets := make(map[string]*bucket)

	for _, r := range spine {
		for _, a := range r.Anchors {
			b, ok := buckets[a]
			if !ok {
				b = &bucket{threads: make(map[string]struct{})}
				buckets[a] = b
			}
			b.threads[r.ID] = struct{}{}
		}
	}

	out := make([]store.SymbolRecord, 0, len(buckets))
	for sym, b := range buckets {
		ids := make([]string, 0, len(b.threads))
		for id := range b.threads {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			ri, rj := recallFires[ids[i]], recallFires[ids[j]]
			if ri != rj {
				return ri > rj // descending
			}
			return ids[i] < ids[j] // ties: lexical
		})
		// anchor_in == threads under the spine-only stub (see file header).
		// Allocate a fresh slice so callers can mutate either independently.
		anchorIn := append([]string(nil), ids...)
		out = append(out, store.SymbolRecord{
			Symbol:         sym,
			Threads:        ids,
			AnchorIn:       anchorIn,
			SourceDominant: store.SourceCurator,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// RebuildSymbols reads spine.jsonl, builds the symbol index, and writes
// it atomically to symbols.jsonl. Returns the records that were written
// so callers (e.g. Check) can compare without re-reading.
func RebuildSymbols(paths store.PersonantPaths) ([]store.SymbolRecord, error) {
	spine, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("rebuild symbols: read spine: %w", err)
	}
	records := BuildSymbols(spine)
	if err := store.WriteSymbols(paths.Symbols, records); err != nil {
		return nil, fmt.Errorf("rebuild symbols: write: %w", err)
	}
	return records, nil
}
