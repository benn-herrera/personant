package turn

import "personant/internal/memops"

// coalescedSymbol carries the per-turn record for one normalized symbol:
// the surface form first observed, the dominant source per spec §2.7.3
// (`curator > user > model > deterministic`), and the normalized key
// itself for convenient iteration. Order of insertion is not preserved;
// callers that depend on order must sort.
type coalescedSymbol struct {
	Normalized string
	Raw        string
	Source     memops.SymbolSource
}

// coalesceBuffer is a per-turn accumulator for symbols and thread-IDs
// observed across the deltas of a single user turn. It is the data
// structure behind the §3.0.4 "engagement updates fire once per
// affected thread, with the union as input" contract.
//
// Symbols are keyed by normalized form; the dominant source per §2.7.3
// wins on collision. The first surface form seen for a given normalized
// key is preserved as `raw`.
//
// Buffer is owned by State; access is single-goroutine within Run.
type coalesceBuffer struct {
	symbols map[string]coalescedSymbol
	threads map[string]struct{}
}

func newCoalesceBuffer() *coalesceBuffer {
	return &coalesceBuffer{
		symbols: map[string]coalescedSymbol{},
		threads: map[string]struct{}{},
	}
}

// addSymbol records a symbol with provenance. raw is the original surface
// form (used as `raw` in HistorySymbol when this symbol later promotes
// to thread frontmatter); normalized is the canonical key (already
// normalized by the caller per §2.7.2). source is the emission origin
// per §2.7.3.
//
// On collision (same normalized key seen multiple times in one turn),
// the dominant source wins; raw is preserved from the first sighting so
// downstream HistorySymbol entries reflect the original surface form
// rather than a later, possibly less-faithful one.
func (b *coalesceBuffer) addSymbol(raw, normalized string, source memops.SymbolSource) {
	if normalized == "" {
		return
	}
	if existing, ok := b.symbols[normalized]; ok {
		// Dominant-source merge per §2.7.3.
		existing.Source = memops.DominantSource(existing.Source, source)
		b.symbols[normalized] = existing
		return
	}
	b.symbols[normalized] = coalescedSymbol{
		Normalized: normalized,
		Raw:        raw,
		Source:     source,
	}
}

func (b *coalesceBuffer) addThread(t string) {
	if t == "" {
		return
	}
	b.threads[t] = struct{}{}
}

// reset clears the accumulator. Called at the start of every Run.
func (b *coalesceBuffer) reset() {
	b.symbols = map[string]coalescedSymbol{}
	b.threads = map[string]struct{}{}
}

// symbolList returns the buffered symbols as normalized strings. Order
// is map iteration order — callers that care about deterministic order
// must sort. Used by anchor selection on new-topic creation.
func (b *coalesceBuffer) symbolList() []string {
	out := make([]string, 0, len(b.symbols))
	for s := range b.symbols {
		out = append(out, s)
	}
	return out
}

// coalescedList returns full per-symbol records (raw + normalized +
// source). Used by closeTurnAndUpdateEngagement to merge into a thread's
// history_symbols.
func (b *coalesceBuffer) coalescedList() []coalescedSymbol {
	out := make([]coalescedSymbol, 0, len(b.symbols))
	for _, sym := range b.symbols {
		out = append(out, sym)
	}
	return out
}

func (b *coalesceBuffer) threadList() []string {
	out := make([]string, 0, len(b.threads))
	for t := range b.threads {
		out = append(out, t)
	}
	return out
}
