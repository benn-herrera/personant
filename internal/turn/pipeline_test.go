package turn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/autogit"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recovery"
	"personant/internal/store"
)

// Crash-stability turn-pipeline tests (#94, Wave R3): journal-before-
// model-call ordering, pre-canonical abort semantics, CommitTurn-failure
// marker semantics + the first end-to-end crash→Reconcile→continue
// cycle, the per-turn commit trailer, and the archival re-sequencing.

// opsRecorder wraps the real adapter, appending one token per observed
// call to a shared sequence log so tests can assert cross-boundary
// ordering (journal vs model consult vs canonical write vs commit). It
// can also inject failures at the two seams the R3 contract defines
// distinct behavior for: a JournalTurn append and CommitTurn.
type opsRecorder struct {
	memops.MemoryOps
	calls *[]string

	failJournalKind memops.TurnContentKind // fail JournalTurn for this kind (without delegating)
	failCommit      bool                   // fail CommitTurn (without delegating)
}

func (r *opsRecorder) JournalTurn(ctx context.Context, turnID string, kind memops.TurnContentKind, content []byte) error {
	*r.calls = append(*r.calls, "journal:"+string(kind))
	if r.failJournalKind != "" && r.failJournalKind == kind {
		return errors.New("injected journal failure")
	}
	return r.MemoryOps.JournalTurn(ctx, turnID, kind, content)
}

func (r *opsRecorder) CommitTurn(ctx context.Context, turnID, reason string) error {
	*r.calls = append(*r.calls, "commit-turn")
	if r.failCommit {
		return errors.New("injected commit failure")
	}
	return r.MemoryOps.CommitTurn(ctx, turnID, reason)
}

func (r *opsRecorder) CreateThread(ctx context.Context, w memops.ThreadWrite) error {
	*r.calls = append(*r.calls, "canonical:create")
	return r.MemoryOps.CreateThread(ctx, w)
}

func (r *opsRecorder) EngageThread(ctx context.Context, w memops.ThreadWrite) error {
	*r.calls = append(*r.calls, "canonical:engage")
	return r.MemoryOps.EngageThread(ctx, w)
}

func (r *opsRecorder) ArchiveThreads(ctx context.Context, ids []string) (memops.ArchiveResult, error) {
	*r.calls = append(*r.calls, "archive-threads")
	return r.MemoryOps.ArchiveThreads(ctx, ids)
}

// consultRecorder wraps a model client, logging each ConsultStream into
// the same shared sequence log the opsRecorder writes.
type consultRecorder struct {
	model.Client
	calls *[]string
}

func (c consultRecorder) ConsultStream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	*c.calls = append(*c.calls, "consult")
	return c.Client.ConsultStream(ctx, req)
}

// indexOf returns the first index of tok in calls, or -1.
func indexOf(calls []string, tok string) int {
	for i, c := range calls {
		if c == tok {
			return i
		}
	}
	return -1
}

// assertOrdered fails unless every token appears in calls in the given
// relative order.
func assertOrdered(t *testing.T, calls []string, tokens ...string) {
	t.Helper()
	prev := -1
	for _, tok := range tokens {
		i := indexOf(calls, tok)
		if i < 0 {
			t.Fatalf("call sequence missing %q: %v", tok, calls)
		}
		if i <= prev {
			t.Fatalf("call %q out of order (index %d, want after %d): %v", tok, i, prev, calls)
		}
		prev = i
	}
}

// TestPipeline_JournalModelCommitOrdering asserts the R3 write ordering:
// the prompt is journaled BEFORE the model call, the response is
// journaled BEFORE any canonical write, and CommitTurn lands after the
// canonical writes.
func TestPipeline_JournalModelCommitOrdering(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	var calls []string
	rec := &opsRecorder{MemoryOps: fileadapter.NewFileAdapter(paths), calls: &calls}
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello."},
	}, nil)
	state := NewState(rec, meta, memops.Provider{}, consultRecorder{Client: mock, calls: &calls})

	if _, err := Run(context.Background(), state, "prompt one", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertOrdered(t, calls,
		"journal:prompt", "consult", "journal:response", "canonical:create", "commit-turn")

	// Post-conditions of a committed turn: marker cleared, journal empty.
	if _, present, err := store.ReadMarker(paths); err != nil || present {
		t.Fatalf("marker after committed turn: present=%v err=%v", present, err)
	}
	if recs, torn, err := store.ScanJournal(paths); err != nil || torn != 0 || len(recs) != 0 {
		t.Fatalf("journal after committed turn: records=%d torn=%d err=%v", len(recs), torn, err)
	}
}

// TestPipeline_JournalFailureAbortsPreCanonical: a journal-append failure
// (prompt or response) aborts the turn BEFORE any canonical write, with a
// clean error to the caller and the turn scope released (no leftover
// marker to wedge the next turn).
func TestPipeline_JournalFailureAbortsPreCanonical(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind memops.TurnContentKind
		want string
	}{
		{"prompt", memops.TurnContentPrompt, "journal prompt"},
		{"response", memops.TurnContentResponse, "journal response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, meta := newTestHome(t)
			pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

			var calls []string
			rec := &opsRecorder{
				MemoryOps:       fileadapter.NewFileAdapter(paths),
				calls:           &calls,
				failJournalKind: tc.kind,
			}
			mock := model.NewScriptedMock([]model.Response{
				{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello."},
			}, nil)
			state := NewState(rec, meta, memops.Provider{}, mock)

			headBefore, herr := autogit.HeadHash(context.Background(), paths)
			if herr != nil {
				t.Fatalf("HeadHash: %v", herr)
			}

			_, err := Run(context.Background(), state, "doomed prompt", io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run error = %v, want %q", err, tc.want)
			}

			// Nothing canonical happened.
			if i := indexOf(calls, "canonical:create"); i >= 0 {
				t.Fatalf("canonical write after journal failure: %v", calls)
			}
			recs, rerr := store.ReadSpine(paths.Spine)
			if rerr != nil {
				t.Fatalf("ReadSpine: %v", rerr)
			}
			if len(recs) != 0 {
				t.Fatalf("spine has %d records after aborted turn, want 0", len(recs))
			}
			// The scope was released: no marker survives to wedge the next turn.
			if _, present, merr := store.ReadMarker(paths); merr != nil || present {
				t.Fatalf("marker after pre-canonical abort: present=%v err=%v", present, merr)
			}
			// And released WITHOUT a commit (R3 review F3): the tree is at
			// most logs-dirty, so a commit-per-abort would be pure churn
			// under a provider-outage retry loop.
			headAfter, herr := autogit.HeadHash(context.Background(), paths)
			if herr != nil {
				t.Fatalf("HeadHash: %v", herr)
			}
			if headAfter != headBefore {
				t.Fatalf("aborted-turn release minted a commit: %s → %s", headBefore, headAfter)
			}
		})
	}
}

// TestPipeline_CommitFailureLeavesMarkerThenRecovers is the first
// end-to-end crash→recover→continue cycle: a CommitTurn failure leaves
// the marker set (never half-clear) so the turn fails loudly into the
// ≤1-loss recovery path; Reconcile then rolls the torn turn back
// (cell 4), preserves the journaled bytes as a surfaced artifact, and a
// fresh session continues cleanly on the repaired substrate.
func TestPipeline_CommitFailureLeavesMarkerThenRecovers(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	adapter := fileadapter.NewFileAdapter(paths)
	var calls []string
	rec := &opsRecorder{MemoryOps: adapter, calls: &calls, failCommit: true}
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nDoomed reply body."},
	}, nil)
	state := NewState(rec, meta, memops.Provider{}, mock)

	_, err := Run(context.Background(), state, "doomed user prompt", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("Run error = %v, want commit failure", err)
	}

	// The scope is LEFT OPEN (the journal is not truncated — never
	// half-release) and the canonical writes sit uncommitted — exactly
	// the cell-4 torn-turn shape.
	owner, inFlight, jerr := store.JournalOwner(paths)
	if jerr != nil || !inFlight || owner == "" {
		t.Fatalf("scope after commit failure: owner=%q inFlight=%v err=%v (want an in-flight turn)", owner, inFlight, jerr)
	}
	if _, found, ferr := adapter.FindThread(context.Background(), "thr_1"); ferr != nil || !found {
		t.Fatalf("uncommitted canonical write missing pre-recovery: found=%v err=%v", found, ferr)
	}

	// Recover — the relaunch path.
	rep, rerr := adapter.Reconcile(context.Background())
	if rerr != nil {
		t.Fatalf("Reconcile: %v", rerr)
	}
	if !rep.ResetPerformed {
		t.Fatalf("Reconcile did not reset a torn turn; cells=%v", rep.CellsHit)
	}
	if rep.PreservedContentPath == "" {
		t.Fatalf("Reconcile preserved no journal content; report=%+v", rep)
	}
	artifact, aerr := os.ReadFile(rep.PreservedContentPath)
	if aerr != nil {
		t.Fatalf("read preserved artifact: %v", aerr)
	}
	for _, want := range []string{"doomed user prompt", "Doomed reply body."} {
		if !strings.Contains(string(artifact), want) {
			t.Errorf("preserved artifact missing %q", want)
		}
	}
	// The torn turn was rolled back: ≤1 turn lost, spine back to empty.
	if recs, serr := store.ReadSpine(paths.Spine); serr != nil || len(recs) != 0 {
		t.Fatalf("spine after recovery: %d records err=%v, want 0", len(recs), serr)
	}
	if _, inFlight, jerr := store.JournalOwner(paths); jerr != nil || inFlight {
		t.Fatalf("scope after recovery: inFlight=%v err=%v", inFlight, jerr)
	}

	// Continue: a fresh session on the repaired substrate runs a turn
	// end-to-end and commits it with the trailer.
	mock2 := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nBack in business."},
	}, nil)
	state2 := NewState(adapter, meta, memops.Provider{}, mock2)
	_, info, rerr2 := RunWithInfo(context.Background(), state2, nil, "post-recovery prompt", io.Discard)
	if rerr2 != nil {
		t.Fatalf("post-recovery Run: %v", rerr2)
	}
	headTurn, herr := autogit.HeadTurn(context.Background(), paths)
	if herr != nil {
		t.Fatalf("HeadTurn: %v", herr)
	}
	if headTurn != info.TurnID || info.TurnID == "" {
		t.Fatalf("post-recovery HEAD trailer %q != turn id %q", headTurn, info.TurnID)
	}
}

// TestPipeline_TrailerAndInfo asserts the per-turn commit carries the
// Personant-Turn trailer matching TurnInfo.TurnID, and CommitDuration is
// a real measurement.
func TestPipeline_TrailerAndInfo(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)

	_, info, err := RunWithInfo(context.Background(), state, nil, "hi", io.Discard)
	if err != nil {
		t.Fatalf("RunWithInfo: %v", err)
	}
	if info.TurnID == "" {
		t.Fatalf("TurnInfo.TurnID empty")
	}
	headTurn, herr := autogit.HeadTurn(context.Background(), paths)
	if herr != nil {
		t.Fatalf("HeadTurn: %v", herr)
	}
	if headTurn != info.TurnID {
		t.Fatalf("HEAD trailer %q != TurnInfo.TurnID %q", headTurn, info.TurnID)
	}
	if info.CommitDuration <= 0 {
		t.Errorf("CommitDuration = %v, want > 0 (a real commit was measured)", info.CommitDuration)
	}
}

// TestPipeline_ArchivalRunsAfterCommitTurn asserts the R3 re-sequencing
// (SOLUTION principle 6): the §3.8 archival drain fires strictly AFTER
// CommitTurn returns — between turns, never inside the turn's marker
// window — and releases its own marker scope by the time Run returns.
func TestPipeline_ArchivalRunsAfterCommitTurn(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	// Push the spine over the archival high-water mark with retired
	// threads so the turn-close drain fires this turn.
	for i := 0; i < archiveHighWater+5; i++ {
		seedArchivalThread(t, paths, meta.ID, fmt.Sprintf("thr_%d", i+1),
			memops.ThreadResolved, monotonicTS(i))
	}

	var calls []string
	rec := &opsRecorder{MemoryOps: fileadapter.NewFileAdapter(paths), calls: &calls}
	mock := model.NewScriptedMock([]model.Response{
		{Content: "plain reply, no topic tag"},
	}, nil)
	state := NewState(rec, meta, memops.Provider{}, mock)

	if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertOrdered(t, calls, "commit-turn", "archive-threads")

	// The archival batch ran and released its op=archival scope; no marker
	// of any kind survives the turn.
	if _, present, err := store.ReadMarker(paths); err != nil || present {
		t.Fatalf("marker after archival turn: present=%v err=%v", present, err)
	}
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("ReadSpine: %v", err)
	}
	if len(recs) > archiveHighWater {
		t.Fatalf("spine not drained: %d records (high water %d)", len(recs), archiveHighWater)
	}
}

// TestTurnID_DistinctUnderPinnedTimeline (R3 review F2): turn-id
// uniqueness must not depend on clock.Timeline — the sim pins Timeline,
// and RestartSession resets TurnNumber, so a Timeline-suffixed id
// repeated across sessions (t1-<pinned> == t1-<pinned>), defeating the
// HEADTURN==T equality predicate. The suffix is minted from the real
// clock (clock.Profiling), so ids stay distinct even when Timeline is
// frozen. The retry loop tolerates a coarse platform clock: with the
// old Timeline suffix no number of retries could ever differ.
func TestTurnID_DistinctUnderPinnedTimeline(t *testing.T) {
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	id1 := newTurnID(1)
	if !strings.HasPrefix(id1, "t1-") {
		t.Fatalf("turn id %q lost its human-readable t<N>- prefix", id1)
	}
	distinct := false
	for i := 0; i < 10000; i++ {
		if newTurnID(1) != id1 {
			distinct = true
			break
		}
	}
	if !distinct {
		t.Fatalf("newTurnID(1) repeats under a pinned Timeline: %q", id1)
	}
}

// TestTurnID_CrossSessionCrashLandsCell6NotCell5 is the reviewer's
// collision scenario, post-fix: session 1 commits its turn 1; session 2
// (fresh State, TurnNumber back at 1, Timeline still pinned) crashes
// after journaling its own turn 1's prompt. The session-2 marker id must
// NOT equal HEADTURN (session 1's trailer), so Reconcile classifies the
// crash as cell 6 — nothing landed, journal preserved — never cell 5,
// which would truncate the journal as redundant and lose the prompt.
func TestTurnID_CrossSessionCrashLandsCell6NotCell5(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	adapter := fileadapter.NewFileAdapter(paths)

	// Session 1: one committed turn; HEADTURN = its id.
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello."},
	}, nil)
	state1 := NewState(adapter, meta, memops.Provider{}, mock)
	_, info, err := RunWithInfo(context.Background(), state1, nil, "session one prompt", io.Discard)
	if err != nil {
		t.Fatalf("session 1 Run: %v", err)
	}

	// Session 2 restart: TurnNumber resets to 1. Its first turn's id must
	// not collide with session 1's committed trailer despite the pinned
	// Timeline.
	id2 := newTurnID(1)
	if id2 == info.TurnID {
		t.Fatalf("cross-session turn-id collision under pinned Timeline: %q", id2)
	}

	// Crash shape: journaled prompt for session 2's turn (the in-flight
	// signal), clean tree (killed during the model call) — the cell-6
	// observables.
	if err := store.AppendJournal(paths, id2, store.JournalPrompt, []byte("session two prompt")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	rep, err := adapter.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cells := strings.Join(rep.CellsHit, ",")
	if !strings.Contains(cells, recovery.Cell6TurnNoWrites) {
		t.Errorf("cells = %s, want %s", cells, recovery.Cell6TurnNoWrites)
	}
	if strings.Contains(cells, recovery.Cell5TurnCommitted) {
		t.Errorf("cells = %s: cell-5 misclassification would truncate an unpreserved journal", cells)
	}
	if rep.PreservedContentPath == "" {
		t.Fatal("session 2's journaled prompt was not preserved")
	}
	artifact, aerr := os.ReadFile(rep.PreservedContentPath)
	if aerr != nil {
		t.Fatalf("read preserved artifact: %v", aerr)
	}
	if !strings.Contains(string(artifact), "session two prompt") {
		t.Errorf("preserved artifact missing the in-flight prompt: %q", artifact)
	}
}
