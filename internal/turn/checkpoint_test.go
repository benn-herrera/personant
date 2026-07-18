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
	h, err := autogit.HeadHash(context.Background(), paths, autogit.Daily)
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

// TestCheckpoint_ContentOnlyTurnCommitsOnce is the core #94 cadence
// assertion (per-turn commit supersedes the structural-only cadence): a
// turn with NO structural change still lands exactly one per-turn commit
// — the durability recovery point that bounds crash loss to ≤1 turn —
// and that commit carries the Personant-Turn trailer.
func TestCheckpoint_ContentOnlyTurnCommitsOnce(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	// No topic tag → no thread created, no thread engaged → content-only.
	before, after := runTurn(t, paths, state, "Just a plain reply, no topic.", "hi")

	if after == before {
		t.Fatalf("content-only turn did not advance HEAD (per-turn commit missing, still %s)", before)
	}
	if n := commitCountBetween(t, paths, before, after); n != 1 {
		t.Fatalf("content-only turn produced %d commits, want exactly 1", n)
	}
	headTurn, err := autogit.HeadTurn(context.Background(), paths, autogit.Daily)
	if err != nil {
		t.Fatalf("HeadTurn: %v", err)
	}
	if headTurn == "" {
		t.Fatalf("per-turn commit carries no Personant-Turn trailer")
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
	state.TurnNumber = DecayTurns
	state.ActiveThreads = []string{"thr_1"}
	state.Curator = stubCurator{summary: "gist", anchors: []string{"x", "y", "z", "w"}}
	state.ClosureResolver = fixedOutcomeResolver(ClosureResolved)

	// Content-only model output: the structural change is the closure, not
	// a create. The closure write and the turn's content ride the SAME
	// per-turn commit — still exactly one.
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
	state.TurnNumber = DecayTurns
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

// TestCheckpoint_SessionCloseBackstopIsNoOp: with the #94 per-turn commit
// every turn's writes are already durable at session close, so the
// session-close Checkpoint (chat.go's post-loop() safety net, demoted to
// a belt-and-braces backstop by SOLUTION principle 1) normally commits
// nothing — HEAD does not move.
func TestCheckpoint_SessionCloseBackstopIsNoOp(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	// newTestHome seeds projects/prj_1/meta.json OUTSIDE the adapter.
	// Under scoped per-turn staging (R3-addendum item 3) out-of-band
	// writes are absorbed only by a FULL sweep, never by a turn commit —
	// so baseline-checkpoint the fixture before measuring, exactly as a
	// real home's `personant init`/first backstop would have.
	if err := state.Ops.Checkpoint(context.Background(), "fixture-baseline"); err != nil {
		t.Fatalf("fixture-baseline Checkpoint: %v", err)
	}

	// Two content-only turns, each landing its own per-turn commit.
	runTurn(t, paths, state, "plain reply one", "hi")
	runTurn(t, paths, state, "plain reply two", "again")
	before := headHash(t, paths)

	// Session-close backstop (mirrors chat.go's post-loop() call): the
	// per-turn commits left a clean tree, so this must be a no-op.
	if err := state.Ops.Checkpoint(context.Background(), "session-close"); err != nil {
		t.Fatalf("session-close Checkpoint: %v", err)
	}
	if after := headHash(t, paths); after != before {
		t.Fatalf("session-close backstop committed on a clean tree: %s -> %s", before, after)
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
		parent, err := autogit.ParentCommitHash(ctx, paths, autogit.Daily, cur)
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
