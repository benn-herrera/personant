package turn

import (
	"personant/internal/memops"
)

// stagedSymbol is one entry in the per-State staging buffer. Bytes
// from task-class deltas are not preserved — only the extracted symbol
// set, tagged with the turn during which it was staged.
//
// The principle (per the transient-data lifecycle design): raw task
// bytes are always discardable after the citation window closes; only
// symbols that cross over into a subsequent decision delta get
// promoted into the persistent symbol index. The staging buffer holds
// the candidates during their window.
type stagedSymbol struct {
	// Normalized is the canonical normalized form (per §2.7.2). The
	// map key.
	Normalized string

	// Raw is the original surface form. Preserved so a future
	// promotion can reflect the user-facing spelling.
	Raw string

	// Source is the provenance (§2.7.3).
	Source memops.SymbolSource

	// StagedAt is the State.TurnNumber when this symbol entered
	// staging. Window-close GC evicts entries where
	// (currentTurn - StagedAt) > stagingWindowTurns.
	StagedAt int
}

// stagingWindowTurns is the citation window K. Decision deltas
// within this many turns of a task delta can cite (and thereby
// promote) its staged symbols. Per the locked-in design, K=3 is the
// v0.1 default; future directive plumbing may parameterize it as
// `staging.window-turns`. Calibration is driven by the
// recall-fidelity simulation.
const stagingWindowTurns = 3

// stagingBuffer is the cross-turn buffer for task-class symbols
// awaiting cross-reference promotion. Unlike coalesceBuffer (which
// resets every turn), the staging buffer persists across turns until
// either (a) a decision delta cites a staged symbol (promotion) or
// (b) the symbol's window closes (eviction).
//
// Keyed by normalized form so a repeated task-side observation
// (e.g., the same file path appears in two tool.result deltas)
// dedupes cleanly. Newer entries overwrite older entries with the
// same normalized key; the StagedAt is bumped to the newer turn so
// the window effectively refreshes when a task-side observation
// recurs.
type stagingBuffer struct {
	entries map[string]stagedSymbol
}

func newStagingBuffer() *stagingBuffer {
	return &stagingBuffer{entries: map[string]stagedSymbol{}}
}

// add records (or refreshes) a staged symbol. Caller is responsible
// for not calling this with an empty normalized form.
func (b *stagingBuffer) add(s stagedSymbol) {
	b.entries[s.Normalized] = s
}

// lookup returns the staged entry for a normalized symbol, if any.
func (b *stagingBuffer) lookup(normalized string) (stagedSymbol, bool) {
	s, ok := b.entries[normalized]
	return s, ok
}

// remove drops the staged entry for normalized (used by promotion).
// No-op if absent.
func (b *stagingBuffer) remove(normalized string) {
	delete(b.entries, normalized)
}

// evictBefore drops every staged entry whose StagedAt < cutoffTurn.
// Returns the count of evicted entries (for logging / metrics).
func (b *stagingBuffer) evictBefore(cutoffTurn int) int {
	n := 0
	for k, s := range b.entries {
		if s.StagedAt < cutoffTurn {
			delete(b.entries, k)
			n++
		}
	}
	return n
}

// len returns the current staging buffer size (for metrics).
func (b *stagingBuffer) len() int { return len(b.entries) }

// pruneStaging evicts staging entries whose citation window has
// closed. An entry staged in turn N is eligible for citation in turns
// N through N+K-1 inclusive (K = stagingWindowTurns); at the start of
// turn N+K it is no longer eligible and must be evicted before any
// chain step in the new turn can observe it.
//
// Returns the count of evicted entries (callers may use this for
// instrumentation; the chain emits a `staging.evicted` event on
// nonzero counts).
//
// Safe to call when state.staging is nil (no-op, returns 0).
func pruneStaging(state *State) int {
	if state.staging == nil {
		return 0
	}
	if state.TurnNumber <= 0 {
		// Pre-Run state: nothing has had a chance to stage yet.
		return 0
	}
	cutoff := state.TurnNumber - stagingWindowTurns + 1
	if cutoff <= 0 {
		// Early turns (TurnNumber < K): nothing has aged out yet.
		return 0
	}
	return state.staging.evictBefore(cutoff)
}
