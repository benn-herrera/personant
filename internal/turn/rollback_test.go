package turn

import (
	"context"
	"io"
	"maps"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// cancelOnConsult cancels the turn's context at the moment the model
// round-trip begins, which is exactly the shape of a §4.3.3 Esc abort:
// the `user.prompt` delta has already run through the §3.0 chain, and the
// turn then dies inside the PRE-CANONICAL window.
type cancelOnConsult struct {
	model.Client
	cancel context.CancelFunc
}

func (c cancelOnConsult) ConsultStream(ctx context.Context, _ model.Request) (model.StreamReader, error) {
	c.cancel()
	return nil, ctx.Err()
}

// stageSymbolTurn runs one turn whose tool.result pre-event stages a
// task-class symbol, and returns the live state with that symbol sitting
// in the cross-turn staging buffer.
func stageSymbolTurn(t *testing.T, stagedPath string) *State {
	t.Helper()
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nSeen it."},
		{Content: "*topic: thr_1*\nStill here."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)

	_, err := RunWithDeltas(context.Background(), state,
		[]Delta{{Source: memops.SourceToolResult, Content: "ran the linter over " + stagedPath}},
		"take a look at that", io.Discard)
	if err != nil {
		t.Fatalf("staging turn: %v", err)
	}
	if state.staging.len() == 0 {
		t.Fatalf("precondition failed: the tool.result pre-event staged nothing, so this test would be vacuous")
	}
	return state
}

// TestPreCanonicalAbort_NoSessionResidue is the §4.3.3 retraction
// invariant: a turn the user abandons must leave the session EXACTLY as
// it found it. The mechanism under test is the one that can silently
// violate it — the `user.prompt` delta fires at step 1 of the §3.0 chain,
// long before the model call, and citing a staged task-class symbol
// PROMOTES it out of the cross-turn staging buffer into the turn-scoped
// coalesce buffer, which is then discarded. Without a rollback, a
// retracted prompt would permanently consume a staged symbol that a
// later, real prompt could have cited.
//
// The "promotes" subtest is the non-vacuity guard: it proves the citation
// really does move the symbol, so the "abort" subtest's assertion of an
// unchanged buffer means the rollback worked rather than that nothing
// ever happened.
func TestPreCanonicalAbort_NoSessionResidue(t *testing.T) {
	const stagedPath = "internal/widget/gadget.go"
	cite := "what changed in " + stagedPath + "?"

	t.Run("a completed turn promotes the staged symbol", func(t *testing.T) {
		state := stageSymbolTurn(t, stagedPath)
		before := state.staging.len()
		if _, err := Run(context.Background(), state, cite, io.Discard); err != nil {
			t.Fatalf("citing turn: %v", err)
		}
		if state.staging.len() >= before {
			t.Fatalf("staging len %d (was %d): the citation did not promote anything, so the abort case below proves nothing",
				state.staging.len(), before)
		}
	})

	t.Run("an aborted turn leaves no residue", func(t *testing.T) {
		state := stageSymbolTurn(t, stagedPath)

		// Snapshot every session-volatile field BEFORE the doomed turn.
		want := snapshotSession(state)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		state.Client = cancelOnConsult{Client: state.Client, cancel: cancel}
		if _, err := Run(ctx, state, cite, io.Discard); err == nil {
			t.Fatal("the cancelled turn reported success")
		}

		got := snapshotSession(state)
		if got.turnNumber != want.turnNumber {
			t.Errorf("TurnNumber = %d, want %d (an abandoned turn must not advance the counter)",
				got.turnNumber, want.turnNumber)
		}
		if !maps.Equal(got.staging, want.staging) {
			t.Errorf("staging buffer changed:\n got %v\nwant %v", got.staging, want.staging)
		}
		assertStringsEqual(t, "ActiveThreads", got.activeThreads, want.activeThreads)
		assertStringsEqual(t, "DormantThreads", got.dormantThreads, want.dormantThreads)
		if len(got.history) != len(want.history) {
			t.Errorf("History length = %d, want %d", len(got.history), len(want.history))
		}
		if !maps.Equal(got.embeddingDebt, want.embeddingDebt) {
			t.Errorf("embeddingDebt = %v, want %v", got.embeddingDebt, want.embeddingDebt)
		}
		if !maps.Equal(got.closureDeferUntil, want.closureDeferUntil) {
			t.Errorf("closureDeferUntil = %v, want %v", got.closureDeferUntil, want.closureDeferUntil)
		}
		if len(got.recallSurfaced) != len(want.recallSurfaced) {
			t.Errorf("recallSurfaced = %v, want %v", got.recallSurfaced, want.recallSurfaced)
		}
		if got.flushCalls != want.flushCalls || got.flushChunks != want.flushChunks {
			t.Errorf("flush counters = (%d,%d), want (%d,%d)",
				got.flushCalls, got.flushChunks, want.flushCalls, want.flushChunks)
		}
	})
}

func assertStringsEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", name, got, want)
			return
		}
	}
}
