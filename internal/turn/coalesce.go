package turn

// coalesceBuffer is a per-turn accumulator for symbols and thread-IDs
// observed across the deltas of a single user turn. It is the data
// structure behind the §3.0.4 "engagement updates fire once per
// affected thread, with the union as input" contract.
//
// Buffer is owned by State; access is single-goroutine within Run.
// Methods are inlined trivially but kept on a named type so the code
// reading them telegraphs intent.
type coalesceBuffer struct {
	symbols map[string]struct{}
	threads map[string]struct{}
}

func newCoalesceBuffer() *coalesceBuffer {
	return &coalesceBuffer{
		symbols: map[string]struct{}{},
		threads: map[string]struct{}{},
	}
}

func (b *coalesceBuffer) addSymbol(s string) {
	if s == "" {
		return
	}
	b.symbols[s] = struct{}{}
}

func (b *coalesceBuffer) addThread(t string) {
	if t == "" {
		return
	}
	b.threads[t] = struct{}{}
}

// reset clears the accumulator. Called at the start of every Run.
func (b *coalesceBuffer) reset() {
	b.symbols = map[string]struct{}{}
	b.threads = map[string]struct{}{}
}

// symbolList returns the buffered symbols as a slice. Order is map
// iteration order — callers that care about deterministic order must
// sort. v0.1 callers (engagement update) do not need ordering.
func (b *coalesceBuffer) symbolList() []string {
	out := make([]string, 0, len(b.symbols))
	for s := range b.symbols {
		out = append(out, s)
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
