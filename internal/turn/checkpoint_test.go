package turn

import (
	"bytes"
	"context"
	"testing"
	"time"

	"personant/internal/autogit"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// headHash returns the substrate HEAD commit hash for the test home.
func headHash(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	h, err := autogit.HeadHash(context.Background(), paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	return h
}

// runTurn drives one full Run with the given scripted model content and
// returns the substrate HEAD before and after the turn.
func runTurn(t *testing.T, paths store.PersonantPaths, state *State, content, input string) (before, after string) {
	t.Helper()
	state.Client = model.NewScriptedMock([]model.Response{{Content: content}}, nil)
	before = headHash(t, paths)
	var out bytes.Buffer
	if _, err := Run(context.Background(), state, input, &out); err != nil {
		t.Fatalf("Run(%q): %v", input, err)
	}
	after = headHash(t, paths)
	return before, after
}

// TestCheckpoint_ThreadCreateCommitsOnce: a turn that creates a thread
// (*new-topic*) yields exactly one new substrate commit (HEAD advances by 1).
func TestCheckpoint_ThreadCreateCommitsOnce(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	before, after := runTurn(t, paths,
		state, "*topic: *new-topic* [foo, bar, baz, qux]*\nHello.", "new thread please")

	if after == before {
		t.Fatalf("thread-create turn did not advance HEAD (still %s)", before)
	}
	if n := commitCountBetween(t, paths, before, after); n != 1 {
		t.Fatalf("thread-create turn produced %d commits, want exactly 1", n)
	}
}

// TestCheckpoint_ContentOnlyTurnDoesNotCommit is the core policy assertion:
// a turn with NO structural change (no thread create, no closure) writes NO
// substrate commit — HEAD is unchanged.
func TestCheckpoint_ContentOnlyTurnDoesNotCommit(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	// No topic tag → no thread created, no thread engaged → content-only.
	before, after := runTurn(t, paths, state, "Just a plain reply, no topic.", "hi")

	if after != before {
		t.Fatalf("content-only turn advanced HEAD: %s -> %s (cadence must not commit)", before, after)
	}
}

// TestCheckpoint_CloseCommitsOnce: a turn that closes/retires a thread yields
// exactly one substrate commit. Closure fires in the same Run via the §3.5
// decay scan; the cadence check at turn close commits once.
func TestCheckpoint_CloseCommitsOnce(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	// Arm decay: TurnNumber jumps past the idle threshold so the seeded
	// thread is closure-eligible on this turn. Run increments TurnNumber, so
	// seed one below the firing value.
	state.TurnNumber = decayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "gist", anchors: []string{"x", "y", "z", "w"}}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)

	// Content-only model output: the structural change is the closure, not a
	// create. A content-only turn alone would not commit (prior test); here
	// the closure is what trips the cadence.
	before, after := runTurn(t, paths, state, "plain reply", "anything")

	rec, found, err := state.Ops.FindThread(context.Background(), "thr_1")
	if err != nil || !found {
		t.Fatalf("FindThread thr_1: found=%v err=%v", found, err)
	}
	if rec.State != memops.ThreadResolved {
		t.Fatalf("thr_1 state = %q, want resolved (closure did not fire)", rec.State)
	}
	if after == before {
		t.Fatalf("closure turn did not advance HEAD (still %s)", before)
	}
	if n := commitCountBetween(t, paths, before, after); n != 1 {
		t.Fatalf("closure turn produced %d commits, want exactly 1", n)
	}
}

// TestCheckpoint_CloseAndCreateCoalesceToOneCommit: a switch turn that BOTH
// closes a decayed thread AND creates a new one yields exactly ONE commit —
// the whole point of checking once at turn close rather than per mutation.
func TestCheckpoint_CloseAndCreateCoalesceToOneCommit(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	seedClosureThread(t, paths, meta.ID, "thr_1", memops.ThreadActive, 1, "")

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	state.TurnNumber = decayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "gist", anchors: []string{"x", "y", "z", "w"}}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)

	// New-topic create on the same turn the decayed thr_1 retires.
	before, after := runTurn(t, paths,
		state, "*topic: *new-topic* [foo, bar, baz, qux]*\nHello.", "new thread")

	// Both structural changes happened.
	rec, _, err := state.Ops.FindThread(context.Background(), "thr_1")
	if err != nil {
		t.Fatalf("FindThread thr_1: %v", err)
	}
	if rec.State != memops.ThreadResolved {
		t.Fatalf("thr_1 not retired (state=%q): close half of switch missing", rec.State)
	}
	if _, found, err := state.Ops.FindThread(context.Background(), "thr_2"); err != nil || !found {
		t.Fatalf("thr_2 not created (found=%v err=%v): create half of switch missing", found, err)
	}

	if n := commitCountBetween(t, paths, before, after); n != 1 {
		t.Fatalf("close+create switch produced %d commits, want exactly 1 (coalescing)", n)
	}
}

// TestCheckpoint_SessionCloseFlushesContentOnlyTurns: content-only turns do
// not commit (per-turn), accumulating uncommitted working-tree changes. The
// session-close Checkpoint (the safety net chat.go calls after loop()) must
// flush them — HEAD advances exactly once for the whole pending batch.
func TestCheckpoint_SessionCloseFlushesContentOnlyTurns(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	// Two content-only turns: each writes session/working-set + log bytes but
	// no structural commit. (Working-set state is kept out of git, but the
	// event log under logs/ is tracked, so the tree goes dirty.)
	before := headHash(t, paths)
	runTurn(t, paths, state, "plain reply one", "hi")
	runTurn(t, paths, state, "plain reply two", "again")
	if got := headHash(t, paths); got != before {
		t.Fatalf("content-only turns committed mid-session: %s -> %s", before, got)
	}

	// Session-close safety net (mirrors chat.go's post-loop() call).
	if err := state.Ops.Checkpoint(context.Background(), "session-close"); err != nil {
		t.Fatalf("session-close Checkpoint: %v", err)
	}
	after := headHash(t, paths)
	if after == before {
		t.Fatalf("session-close did not flush pending content-only turns (HEAD still %s)", before)
	}
	if n := commitCountBetween(t, paths, before, after); n != 1 {
		t.Fatalf("session-close produced %d commits, want exactly 1 batched flush", n)
	}
}

// commitCountBetween returns the number of commits on the first-parent path
// from `after` back to (but excluding) `before`.
func commitCountBetween(t *testing.T, paths store.PersonantPaths, before, after string) int {
	t.Helper()
	ctx := context.Background()
	count := 0
	cur := after
	for cur != before && cur != "" {
		count++
		parent, err := autogit.ParentCommitHash(ctx, paths, cur)
		if err != nil {
			t.Fatalf("walk parents from %s: %v", after, err)
		}
		cur = parent
		if count > 50 {
			t.Fatalf("commit walk did not reach %s within 50 commits", before)
		}
	}
	return count
}
