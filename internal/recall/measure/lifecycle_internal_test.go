package measure

import (
	"context"
	"sync"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// newLifecycleService builds a Service over an isolated home with one seeded
// thread and a mock embedder (so Prepare starts the indexer goroutine).
func newLifecycleService(t *testing.T) *Service {
	t.Helper()
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	ts := "2026-04-01T00:00:00Z"
	rec := memops.SpineRecord{
		ID: "thr_1", Project: "prj_1", Anchors: []string{"thr_1"}, Summary: "thr_1",
		State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine: %v", err)
	}
	if err := store.SeedThread(paths, memops.Thread{Meta: memops.ThreadMeta{
		ID: "thr_1", Project: "prj_1", Anchors: []string{"thr_1"}, Summary: "thr_1",
		State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
	}}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	return NewService(fileadapter.NewFileAdapter(paths), model.NewMockEmbedder())
}

// TestEnqueueFlush_AfterClose is the burn-down Wave 3 guard that an enqueue
// arriving after Close is refused cleanly: no panic (jobs is never closed) and
// no pendingFlush increment for the dropped job (a refused enqueue must not
// widen the §3.4 lexical band).
func TestEnqueueFlush_AfterClose(t *testing.T) {
	s := newLifecycleService(t)
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Must not panic on send-after-close, and must drop without touching pending.
	s.EnqueueFlush("thr_1", 7)
	if d := s.pendingFlushDepth("thr_1"); d != 0 {
		t.Errorf("refused post-close enqueue incremented pendingFlush: got %d, want 0", d)
	}

	// Close is idempotent.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestEnqueueFlush_ConcurrentClose drives many concurrent EnqueueFlush calls
// against a racing Close. Before the fix this panicked (send on a closed jobs
// channel) or wedged a caller on a send no exited indexer would receive. It
// must now complete cleanly: no panic, no deadlock, and every goroutine
// returns. Runs meaningfully under `GOFLAGS=-race`.
func TestEnqueueFlush_ConcurrentClose(t *testing.T) {
	s := newLifecycleService(t)
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	const goroutines = 16
	const perGoroutine = 40
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				s.EnqueueFlush("thr_1", i+1)
			}
		}()
	}

	// Close concurrently with the in-flight enqueues.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wg.Wait() // no goroutine may block forever on a post-close send

	// The property under test is liveness/safety: reaching here means no send
	// panicked and no caller wedged. Pending accounting after a concurrent
	// close is intentionally NOT asserted — a job that wins the send/stop race
	// into the buffer after the indexer has already drained leaves a harmless
	// count on the torn-down Service; the deterministic clean-drop accounting
	// is covered by TestEnqueueFlush_AfterClose.
}
