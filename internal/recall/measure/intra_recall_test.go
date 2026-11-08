package measure_test

import (
	"context"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
)

// flushAndWait enqueues a re-index of threadID at the given dispatch
// turncount and blocks until the async indexer has surfaced an
// intra-thread hit for it under probeQuery, or the deadline elapses. This
// is the test sync mechanism: the indexer runs on its own goroutine, so a
// test observes its effect by polling Recall (a single atomic load) until
// the new snapshot is published. A failed wait is a real defect (a leaked
// or stuck indexer), not flakiness.
func flushAndWait(t *testing.T, svc *measure.Service, threadID string, dispatchTurncount int, probeQuery string) measure.Result {
	t.Helper()
	svc.EnqueueFlush(threadID, dispatchTurncount)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		results, err := svc.Recall(context.Background(), measure.Request{
			QueryText: probeQuery,
			Engaged:   threadID,
			Exclude:   map[string]struct{}{threadID: {}},
		})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if r, ok := findResult(results, threadID); ok && r.IntraThread != nil {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("intra-thread hit for %s never published within deadline", threadID)
	return measure.Result{}
}

// seedThreadTurns seeds a thread whose body is laid down as distinct
// per-turn excerpts (one block per "## Turn" heading), so the fine tier
// has multiple chunks to retrieve from.
func seedThreadTurns(t *testing.T, paths store.PersonantPaths, id string, anchors []string, body string) {
	t.Helper()
	seedThread(t, paths, id, anchors, body)
}

// TestService_NilEmbedder_ByteIdenticalSymbolic is AC5/I7: a nil-embedder
// Service produces exactly the symbolic-only Result stream — no embedding
// or intra-thread hits, regardless of QueryText/Engaged being set.
func TestService_NilEmbedder_ByteIdenticalSymbolic(t *testing.T) {
	paths, ops := newRecallHome(t)
	seedThread(t, paths, "thr_1", []string{"alpha", "beta", "gamma", "delta"}, "alpha beta gamma delta")

	svc := measure.NewService(ops, nil)
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	results, err := svc.Recall(context.Background(), measure.Request{
		QuerySymbols: []string{"alpha", "beta", "gamma", "delta"},
		QueryText:    "alpha beta gamma delta",
		Engaged:      "thr_1", // would trigger intra-thread with an embedder
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	r, ok := findResult(results, "thr_1")
	if !ok {
		t.Fatalf("thr_1 not recalled; results=%+v", results)
	}
	if r.Symbolic == nil {
		t.Errorf("symbolic hit missing")
	}
	if r.Embedding != nil {
		t.Errorf("embedding hit present with nil embedder: %+v", r.Embedding)
	}
	if r.IntraThread != nil {
		t.Errorf("intra-thread hit present with nil embedder: %+v", r.IntraThread)
	}
	if got := r.Layers(); len(got) != 1 || got[0] != "symbolic" {
		t.Errorf("Layers() = %v, want [symbolic]", got)
	}
}

// TestService_IndexerEnqueueSwap is the end-to-end indexer check: an
// EnqueueFlush job is consumed by the goroutine and swapped into the live
// snapshot (observable via a later Recall), and Close stops the goroutine
// cleanly.
func TestService_IndexerEnqueueSwap(t *testing.T) {
	paths, ops := newRecallHome(t)
	// Distinct per-turn content; an early turn (turn 1) is topically
	// distinct from the rest, so a probe for it retrieves that chunk.
	body := "## Turn 1\nquasar redshift spectroscopy luminosity\n\n" +
		"## Turn 2\nglacier moraine sediment erosion\n\n" +
		"## Turn 3\nenzyme catalysis substrate kinetics\n"
	seedThreadTurns(t, paths, "thr_1", []string{"thr_1"}, body)

	svc := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	r := flushAndWait(t, svc, "thr_1", 3, "quasar redshift spectroscopy luminosity")
	if r.IntraThread == nil {
		t.Fatalf("expected intra-thread hit after flush")
	}
	if len(r.IntraThread.Turns) == 0 || r.IntraThread.Turns[0] != 1 {
		t.Errorf("intra-thread best turn = %v, want turn 1 first", r.IntraThread.Turns)
	}
	if got := r.Layers(); !containsLayer(got, "intra-thread") {
		t.Errorf("Layers() = %v, want intra-thread present", got)
	}
}

// TestService_EngagedBypassBelowCoarse is the §4.1-step-3 check: the
// engaged thread surfaces an intra-thread hit for its early chunk even
// when the thread is in Exclude (so it cannot appear via the coarse/
// thread-level pass) — the bypass admits its chunks by ID.
func TestService_EngagedBypassBelowCoarse(t *testing.T) {
	paths, ops := newRecallHome(t)
	body := "## Turn 1\ntrefoil knot topology invariant chirality\n\n" +
		"## Turn 2\nmonsoon humidity precipitation tropics\n\n" +
		"## Turn 3\nledger reconciliation accrual depreciation\n"
	seedThreadTurns(t, paths, "thr_eng", []string{"thr_eng"}, body)
	// A second, unrelated thread so the coarse tier is non-trivial.
	seedThread(t, paths, "thr_2", []string{"thr_2"}, "neutrino oscillation flavor lepton")

	svc := measure.NewService(ops, model.NewMockEmbedder())
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// Probe an early slot of the engaged thread, with the engaged thread
	// excluded at the thread level. The only way it can appear is the
	// intra-thread bypass.
	r := flushAndWait(t, svc, "thr_eng", 3, "trefoil knot topology invariant chirality")
	if r.IntraThread == nil {
		t.Fatalf("engaged-thread intra-thread bypass did not fire; result=%+v", r)
	}
	if r.Embedding != nil {
		t.Errorf("excluded engaged thread should not get a thread-level embedding hit: %+v", r.Embedding)
	}
	if r.IntraThread.Turns[0] != 1 {
		t.Errorf("bypass best turn = %v, want turn 1", r.IntraThread.Turns)
	}
}

func containsLayer(layers []string, want string) bool {
	for _, l := range layers {
		if l == want {
			return true
		}
	}
	return false
}

// Ensure the additive port op compiles against the interface from the
// test package's perspective (a missing implementer would fail the build
// elsewhere; this keeps the symbol referenced).
var _ = func(ops memops.MemoryOps) {
	_, _ = ops.LoadThreadExcerpts(context.Background(), "thr_1")
}
