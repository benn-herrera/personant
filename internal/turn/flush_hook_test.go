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
// EmbeddingDebtCap, carrying the dispatch turncount (state.TurnNumber),
// then resets. A second cap's worth enqueues a second flush.
func TestRecordExcerptScrollOut_DebtCapEnqueues(t *testing.T) {
	state, rec := newHookState(t)
	state.TurnNumber = 1000

	// One short of the cap → no enqueue yet.
	for i := 0; i < EmbeddingDebtCap-1; i++ {
		recordExcerptScrollOut(state, "thr_1")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("enqueued before reaching cap: %d calls", len(rec.calls))
	}
	if got := state.embeddingDebt["thr_1"]; got != EmbeddingDebtCap-1 {
		t.Fatalf("debt = %d, want %d", got, EmbeddingDebtCap-1)
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
	for i := 0; i < EmbeddingDebtCap; i++ {
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
	for i := 0; i < EmbeddingDebtCap; i++ {
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
	state.embeddingDebt = map[string]int{"thr_1": EmbeddingDebtCap - 3}

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

// TestFlushHooks_SymbolicOnlyCountsButNoDispatch: with a Recaller that does
// NOT satisfy flushEnqueuer (the symbolic-only path), the §6.5 flush COST is
// still observed — the debt accrues and the flushCalls/flushChunks counters
// bump on a debt-cap fire — but no index DISPATCH happens (EnqueueFlush is not
// satisfied). This is the #126 contract: the flush cost gauge reports what the
// policy actually did, which is the same cost an embedding run would pay for
// this workload; only the embed work below the turn layer is gated on the
// embedder (I7, now narrowed from "no debt tracking" to "no dispatch").
func TestFlushHooks_SymbolicOnlyCountsButNoDispatch(t *testing.T) {
	paths, meta := newTestHome(t)
	ops := fileadapter.NewFileAdapter(paths)
	state := NewState(ops, meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	state.Recaller = symbolicOnlyRecaller{} // a Recaller, deliberately NOT a flushEnqueuer
	state.TurnNumber = 3
	state.Budget = memops.Budget{BTopK: 1}

	// EmbeddingDebtCap+5 scroll-outs on one thread → exactly one cap fire (the
	// remaining 5 sit below the next cap and do not flush yet).
	for i := 0; i < EmbeddingDebtCap+5; i++ {
		recordExcerptScrollOut(state, "thr_1")
	}
	if state.FlushCalls() != 1 {
		t.Errorf("symbolic-only debt-cap flush count = %d, want 1 (cost observed without an embedder)", state.FlushCalls())
	}
	if state.FlushChunks() != EmbeddingDebtCap {
		t.Errorf("symbolic-only flush chunks = %d, want %d (one cap's worth)", state.FlushChunks(), EmbeddingDebtCap)
	}

	// A dormancy demotion ALWAYS fires a flush (the coarse-body re-embed cost),
	// even when the demoted thread carries no excerpt debt — so a no-debt
	// demotion counts a call but adds no chunks.
	touchActiveLRU(state, "thr_a")
	touchActiveLRU(state, "thr_b") // demotes thr_a (no debt) → +1 call, +0 chunks
	if state.FlushCalls() != 2 {
		t.Errorf("a no-debt dormancy demotion must still count one flush call; calls=%d, want 2", state.FlushCalls())
	}
	if state.FlushChunks() != EmbeddingDebtCap {
		t.Errorf("a no-debt demotion must add no chunks; chunks=%d, want %d", state.FlushChunks(), EmbeddingDebtCap)
	}

	// A dormancy flush of thr_1 carries its remaining +5 debt tail.
	flushOnDormancy(state, "thr_1")
	if state.FlushCalls() != 3 {
		t.Errorf("dormancy flush of a debt-carrying thread must count; calls=%d, want 3", state.FlushCalls())
	}
	if state.FlushChunks() != EmbeddingDebtCap+5 {
		t.Errorf("flush chunks after dormancy = %d, want %d", state.FlushChunks(), EmbeddingDebtCap+5)
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
