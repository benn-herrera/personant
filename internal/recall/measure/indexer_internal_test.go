package measure

import (
	"context"
	"testing"

	"personant/internal/recall/scoring"
)

// vec is a tiny helper to build a 2-D vector fixture; cosine math is
// exercised in scoring's own tests, so these fixtures only need to be
// distinguishable, not realistic.
func vec(a, b float64) []float64 { return []float64{a, b} }

// TestSwap_DropsStaleWatermark is the AC3 monotonicity check (I2/F5): a
// vector whose dispatch watermark is not strictly greater than the
// thread's recorded watermark is dropped on swap, so a stale in-flight
// embed never overwrites a fresher vector. Exercised directly on the
// swap method (no goroutine) for determinism.
func TestSwap_DropsStaleWatermark(t *testing.T) {
	s := &Service{}
	s.cur.Store(emptySnapshot())

	// A fresh embed at turncount 100 lands.
	fresh := scoring.ChunkVector{TurnNumber: 5, Vector: vec(1, 0)}
	s.swap("thr_1", 100, scoring.ThreadVector{ThreadID: "thr_1", Vector: vec(1, 0)}, []scoring.ChunkVector{fresh}, []string{"h5"})

	snap := s.cur.Load()
	if snap.watermark["thr_1"] != 100 {
		t.Fatalf("watermark after fresh swap = %d, want 100", snap.watermark["thr_1"])
	}
	if len(snap.fine["thr_1"]) != 1 || snap.fine["thr_1"][0].TurnNumber != 5 {
		t.Fatalf("fine tier after fresh swap = %+v, want one chunk turn 5", snap.fine["thr_1"])
	}

	// A stale embed dispatched at turncount 80 (thread re-activated and
	// re-decayed while this embed was outstanding) must be DROPPED — the
	// fresher turn-100 vector survives.
	stale := scoring.ChunkVector{TurnNumber: 2, Vector: vec(0, 1)}
	s.swap("thr_1", 80, scoring.ThreadVector{ThreadID: "thr_1", Vector: vec(0, 1)}, []scoring.ChunkVector{stale}, []string{"h2"})

	snap = s.cur.Load()
	if snap.watermark["thr_1"] != 100 {
		t.Errorf("watermark after stale swap = %d, want unchanged 100", snap.watermark["thr_1"])
	}
	if len(snap.fine["thr_1"]) != 1 || snap.fine["thr_1"][0].TurnNumber != 5 {
		t.Errorf("stale embed overwrote fresher vector: fine=%+v", snap.fine["thr_1"])
	}

	// An equal watermark is also dropped (strictly-greater rule).
	s.swap("thr_1", 100, scoring.ThreadVector{ThreadID: "thr_1", Vector: vec(0, 1)}, nil, nil)
	snap = s.cur.Load()
	if len(snap.fine["thr_1"]) != 1 || snap.fine["thr_1"][0].TurnNumber != 5 {
		t.Errorf("equal-watermark swap was not dropped: fine=%+v", snap.fine["thr_1"])
	}

	// A strictly newer embed wins and may shrink the fine tier to empty.
	s.swap("thr_1", 101, scoring.ThreadVector{ThreadID: "thr_1", Vector: vec(0, 1)}, nil, nil)
	snap = s.cur.Load()
	if snap.watermark["thr_1"] != 101 {
		t.Errorf("watermark after newer swap = %d, want 101", snap.watermark["thr_1"])
	}
	if _, present := snap.fine["thr_1"]; present {
		t.Errorf("empty-chunk swap should drop the fine entry, got %+v", snap.fine["thr_1"])
	}
}

// TestSnapshotImmutability: with replaces a thread's vectors without
// mutating the prior snapshot — a reader holding the old pointer sees the
// old whole (I1, never torn).
func TestSnapshotImmutability(t *testing.T) {
	old := emptySnapshot()
	old = old.with("thr_1", scoring.ThreadVector{ThreadID: "thr_1", Vector: vec(1, 0)},
		[]scoring.ChunkVector{{TurnNumber: 1, Vector: vec(1, 0)}}, []string{"h1"}, 10)

	next := old.with("thr_1", scoring.ThreadVector{ThreadID: "thr_1", Vector: vec(0, 1)},
		[]scoring.ChunkVector{{TurnNumber: 2, Vector: vec(0, 1)}}, []string{"h2"}, 20)

	if old.watermark["thr_1"] != 10 {
		t.Errorf("prior snapshot watermark mutated: %d", old.watermark["thr_1"])
	}
	if old.fine["thr_1"][0].TurnNumber != 1 {
		t.Errorf("prior snapshot fine tier mutated: %+v", old.fine["thr_1"])
	}
	if next.watermark["thr_1"] != 20 || next.fine["thr_1"][0].TurnNumber != 2 {
		t.Errorf("new snapshot not updated: wm=%d fine=%+v", next.watermark["thr_1"], next.fine["thr_1"])
	}
}

// TestNilEmbedder_NoIndexerGoroutine is the I7 construction check: a
// nil-embedder Service starts no goroutine (jobs/indexerDone stay nil),
// EnqueueFlush is a no-op, and Close is a clean no-op.
func TestNilEmbedder_NoIndexerGoroutine(t *testing.T) {
	s := NewService(nil, nil)
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if s.jobs != nil || s.indexerDone != nil {
		t.Fatalf("nil embedder started indexer plumbing: jobs=%v done=%v", s.jobs, s.indexerDone)
	}
	s.EnqueueFlush("thr_1", 5) // must not panic / block (no channel)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
