package turn

import (
	"maps"
	"slices"

	"personant/internal/model"
)

// sessionState is a snapshot of State's SESSION-VOLATILE fields — the
// ones that deliberately survive a turn boundary, as opposed to the
// turn-scoped fields (coalesce, fileEdits, turnOwner, the re-prompt
// flags, the §3.11 structural counters) that RunWithInfo resets at the
// top of every turn.
//
// It exists for ONE purpose: a turn that aborts BEFORE its first
// canonical write must leave the session exactly as it found it. That is
// the substrate half of the §4.3.3 Esc retraction rule — a retracted
// prompt is one the user is declaring a mistake, so it must not shape the
// next turn's symbols, working set, or recall. Without it the abort still
// left marks: the `user.prompt` delta fires at step 1 of the chain, well
// before the model call, and its symbol pass PROMOTES any matching
// task-class symbol out of the cross-turn staging buffer (see
// addExtractedSymbol) — a promotion that then evaporates with the
// turn-scoped coalesce buffer, silently consuming a staged symbol a later
// real prompt could have cited. The turn counter and the window-close
// staging GC move too, and a §5.5 mid-turn fetch promotes a thread into
// Layer B.
//
// The field list is deliberately EXHAUSTIVE over session-volatile state
// rather than narrowed to the fields today's pre-canonical window happens
// to touch. Narrowing would make correctness depend on a reading of the
// happy path, and a future pre-canonical mutation would silently escape
// the rollback; restoring a field that never changed costs nothing.
//
// What is deliberately NOT captured: anything the day barrier did. The
// snapshot is taken AFTER the barrier's archived-thread eviction, because
// those threads are gone from disk — restoring them would resurrect
// phantom ids into the working set and SaveWorkingSet would persist
// pointers to threads that no longer exist.
//
// maps.Clone / slices.Clone are nil-PRESERVING, which matters: several of
// these fields use nil as the well-formed empty case, so restoring an
// empty-but-non-nil container would not be a faithful rollback.
type sessionState struct {
	turnNumber        int
	staging           map[string]stagedSymbol
	activeThreads     []string
	dormantThreads    []string
	history           []model.Message
	recallSurfaced    map[string]struct{}
	embeddingDebt     map[string]int
	closureDeferUntil map[string]int
	flushCalls        int
	flushChunks       int
}

// snapshotSession captures the session-volatile state. Every value in
// these containers is an int, a string, or a struct of those, so one
// level of copying is a full separation.
func snapshotSession(state *State) sessionState {
	s := sessionState{
		turnNumber:        state.TurnNumber,
		flushCalls:        state.flushCalls,
		flushChunks:       state.flushChunks,
		activeThreads:     slices.Clone(state.ActiveThreads),
		dormantThreads:    slices.Clone(state.DormantThreads),
		history:           slices.Clone(state.History),
		recallSurfaced:    maps.Clone(state.recallSurfaced),
		embeddingDebt:     maps.Clone(state.embeddingDebt),
		closureDeferUntil: maps.Clone(state.closureDeferUntil),
	}
	if state.staging != nil {
		s.staging = maps.Clone(state.staging.entries)
	}
	return s
}

// restore puts the session back to the snapshot. Pure in-memory work: it
// touches no file, no journal and no git handle, so it cannot perturb the
// #94 crash-stability ordering it runs alongside.
func (s sessionState) restore(state *State) {
	state.TurnNumber = s.turnNumber
	state.flushCalls = s.flushCalls
	state.flushChunks = s.flushChunks
	state.ActiveThreads = slices.Clone(s.activeThreads)
	state.DormantThreads = slices.Clone(s.dormantThreads)
	state.History = slices.Clone(s.history)
	state.recallSurfaced = maps.Clone(s.recallSurfaced)
	state.embeddingDebt = maps.Clone(s.embeddingDebt)
	state.closureDeferUntil = maps.Clone(s.closureDeferUntil)
	if state.staging != nil {
		entries := maps.Clone(s.staging)
		if entries == nil {
			// The buffer existed but held nothing; keep the invariant that a
			// live stagingBuffer always has a usable map.
			entries = map[string]stagedSymbol{}
		}
		state.staging.entries = entries
	}
}
