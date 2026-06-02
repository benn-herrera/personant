package turn

import (
	"context"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/measure"
)

// flushCall records one EnqueueFlush invocation.
type flushCall struct {
	threadID          string
	dispatchTurncount int
}

// recordingRecaller is a Recaller that also satisfies the flushEnqueuer
// optional interface (like the embedding measure.Service), recording every
// EnqueueFlush. The Recall stack itself is a no-op — these tests exercise
// only the §6.2 debt/dormancy enqueue wiring in turn, not recall scoring.
type recordingRecaller struct {
	calls []flushCall
}

func (r *recordingRecaller) Prepare(context.Context) error { return nil }
func (r *recordingRecaller) Recall(context.Context, measure.Request) ([]measure.Result, error) {
	return nil, nil
}
func (r *recordingRecaller) Close() error { return nil }
func (r *recordingRecaller) EnqueueFlush(threadID string, dispatchTurncount int) {
	r.calls = append(r.calls, flushCall{threadID: threadID, dispatchTurncount: dispatchTurncount})
}

// compile-time guards: recordingRecaller is a Recaller AND a flushEnqueuer.
var (
	_ measure.Recaller = (*recordingRecaller)(nil)
	_ flushEnqueuer    = (*recordingRecaller)(nil)
)

func newHookState(t *testing.T) (*State, *recordingRecaller) {
	t.Helper()
	paths, meta := newTestHome(t)
	ops := fileadapter.NewFileAdapter(paths)
	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	rec := &recordingRecaller{}
	state.Recaller = rec
	return state, rec
}

// TestRecordExcerptScrollOut_DebtCapEnqueues: the debt counter accrues one
// per scrolled-out excerpt and enqueues exactly one flush when it reaches
// embeddingDebtCap, carrying the dispatch turncount (state.TurnNumber),
// then resets. A second cap's worth enqueues a second flush.
func TestRecordExcerptScrollOut_DebtCapEnqueues(t *testing.T) {
	state, rec := newHookState(t)
	state.TurnNumber = 1000

	// One short of the cap → no enqueue yet.
	for i := 0; i < embeddingDebtCap-1; i++ {
		recordExcerptScrollOut(state, "thr_1")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("enqueued before reaching cap: %d calls", len(rec.calls))
	}
	if got := state.embeddingDebt["thr_1"]; got != embeddingDebtCap-1 {
		t.Fatalf("debt = %d, want %d", got, embeddingDebtCap-1)
	}

	// The cap-th scroll-out fires exactly one flush and resets the counter.
	recordExcerptScrollOut(state, "thr_1")
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 flush at cap; got %d", len(rec.calls))
	}
	if rec.calls[0] != (flushCall{threadID: "thr_1", dispatchTurncount: 1000}) {
		t.Errorf("flush call = %+v, want thr_1 @ 1000", rec.calls[0])
	}
	if got := state.embeddingDebt["thr_1"]; got != 0 {
		t.Errorf("debt not reset after flush: %d", got)
	}

	// A second full cap enqueues again, at the (possibly newer) turncount.
	state.TurnNumber = 1500
	for i := 0; i < embeddingDebtCap; i++ {
		recordExcerptScrollOut(state, "thr_1")
	}
	if len(rec.calls) != 2 {
		t.Fatalf("expected 2 flushes after second cap; got %d", len(rec.calls))
	}
	if rec.calls[1].dispatchTurncount != 1500 {
		t.Errorf("second flush turncount = %d, want 1500", rec.calls[1].dispatchTurncount)
	}
}

// TestRecordExcerptScrollOut_PerThreadDebt: debt is tracked per thread, so
// interleaved scroll-outs on two threads each reach their own cap
// independently.
func TestRecordExcerptScrollOut_PerThreadDebt(t *testing.T) {
	state, rec := newHookState(t)
	state.TurnNumber = 1
	for i := 0; i < embeddingDebtCap; i++ {
		recordExcerptScrollOut(state, "thr_1")
		recordExcerptScrollOut(state, "thr_2")
	}
	if len(rec.calls) != 2 {
		t.Fatalf("expected one flush per thread (2 total); got %d", len(rec.calls))
	}
	seen := map[string]bool{}
	for _, c := range rec.calls {
		seen[c.threadID] = true
	}
	if !seen["thr_1"] || !seen["thr_2"] {
		t.Errorf("both threads must flush; calls=%+v", rec.calls)
	}
}

// TestFlushOnDormancy_Enqueues: a dormancy demotion enqueues a flush at the
// session turncount and clears the thread's remaining debt.
func TestFlushOnDormancy_Enqueues(t *testing.T) {
	state, rec := newHookState(t)
	state.TurnNumber = 42
	state.embeddingDebt = map[string]int{"thr_1": embeddingDebtCap - 3}

	flushOnDormancy(state, "thr_1")

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 dormancy flush; got %d", len(rec.calls))
	}
	if rec.calls[0] != (flushCall{threadID: "thr_1", dispatchTurncount: 42}) {
		t.Errorf("dormancy flush = %+v, want thr_1 @ 42", rec.calls[0])
	}
	if _, ok := state.embeddingDebt["thr_1"]; ok {
		t.Errorf("dormancy flush must clear debt; got %d", state.embeddingDebt["thr_1"])
	}
}

// TestTouchActiveLRU_DemotionFlushes: touchActiveLRU at the Layer-B
// overflow point enqueues a dormancy flush for the demoted thread. With
// BTopK=2, pushing a third thread demotes the oldest, which must flush.
func TestTouchActiveLRU_DemotionFlushes(t *testing.T) {
	state, rec := newHookState(t)
	state.TurnNumber = 7
	state.Budget = memops.Budget{BTopK: 2}

	touchActiveLRU(state, "thr_a")
	touchActiveLRU(state, "thr_b")
	if len(rec.calls) != 0 {
		t.Fatalf("no demotion expected under BTopK=2 yet; got %d calls", len(rec.calls))
	}
	// Third thread overflows Layer B: thr_a (oldest) demotes and flushes.
	touchActiveLRU(state, "thr_c")
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 demotion flush; got %d", len(rec.calls))
	}
	if rec.calls[0] != (flushCall{threadID: "thr_a", dispatchTurncount: 7}) {
		t.Errorf("demotion flush = %+v, want thr_a @ 7", rec.calls[0])
	}
}

// TestFlushHooks_SymbolicOnlyNoOp: with the symbolic-only default Recaller
// (no flushEnqueuer), the debt and dormancy hooks are inert — no debt map
// is allocated and no enqueue occurs. This is the I7 determinism guard:
// nil-embedder behavior is byte-identical to before Inc 4.
func TestFlushHooks_SymbolicOnlyNoOp(t *testing.T) {
	paths, meta := newTestHome(t)
	ops := fileadapter.NewFileAdapter(paths)
	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	// NewState installs a symbolic-only measure.Service (embedder nil). It is
	// a Recaller but its EnqueueFlush is a no-op; the turn-side seam guards on
	// the flushEnqueuer assertion, which measure.Service DOES satisfy — so the
	// real I7 guard for the symbolic-only path is that EnqueueFlush no-ops
	// (jobs==nil) below the turn layer. Here we additionally confirm the debt
	// map stays unallocated when a recaller does not satisfy flushEnqueuer.
	state.Recaller = symbolicOnlyRecaller{}
	state.TurnNumber = 3
	state.Budget = memops.Budget{BTopK: 1}

	for i := 0; i < embeddingDebtCap+5; i++ {
		recordExcerptScrollOut(state, "thr_1")
	}
	touchActiveLRU(state, "thr_a")
	touchActiveLRU(state, "thr_b") // demotes thr_a
	flushOnDormancy(state, "thr_z")

	if state.embeddingDebt != nil {
		t.Errorf("debt map allocated for a non-flushEnqueuer recaller: %v", state.embeddingDebt)
	}
}

// symbolicOnlyRecaller is a Recaller that deliberately does NOT implement
// flushEnqueuer — the inert-path case for I7.
type symbolicOnlyRecaller struct{}

func (symbolicOnlyRecaller) Prepare(context.Context) error { return nil }
func (symbolicOnlyRecaller) Recall(context.Context, measure.Request) ([]measure.Result, error) {
	return nil, nil
}
func (symbolicOnlyRecaller) Close() error { return nil }

var _ measure.Recaller = symbolicOnlyRecaller{}
