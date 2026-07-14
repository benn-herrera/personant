package measure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// TestIndexer_WatermarkTieSecondFlushPublishes is the BD-8 watermark-tie
// regression proof: two flush jobs for the same thread enqueued with the SAME
// dispatch watermark (the structurally reachable same-turn double-enqueue —
// cap-flush at the scroll-out hook, then dormancy flush at demotion, both
// stamped with the one state.TurnNumber) must BOTH publish. Under the old
// `<=` swap guard the FIFO-later job was dropped on equality even though —
// single FIFO indexer — it loaded canonical state AFTER the first job
// published, so anything only it loaded was silently lost: the drop
// decremented pendingFlush without publishing, and once the thread went
// dormant no lexical band covered the missing batch.
//
// The test makes the drop observable as content loss: job A loads the thread,
// is held in-flight (gated embedder), the probe turn is appended (so only the
// later load sees it — the general "FIFO-later job loaded fresher state"
// property, forced deterministically), job B is enqueued at the SAME
// watermark, and the gate is released. It then asserts the probe turn is
// published into the fine tier and findable through Recall's embedding intra
// pass. EngagedDebtWindow is deliberately 0: after both jobs drain, pending
// is 0 and the newest-cap lexical band would cover the probe turn, masking
// exactly the loss under test — the lexical floor has its own proof
// (TestRecall_CompletenessContinuousAcrossInFlightFlush).
func TestIndexer_WatermarkTieSecondFlushPublishes(t *testing.T) {
	const watermark = 7 // the shared same-turn dispatch turncount
	const seedTurns = 3
	const probeTurn = seedTurns + 1
	const probeToken = "quasarwxyz"

	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	texts := seedDeadZoneThread(t, paths, "thr_1", seedTurns, 0, "")
	_ = texts

	ge := &gatedEmbedder{
		inner:   model.NewMockEmbedder(),
		entered: make(chan struct{}, 1),
		gate:    make(chan struct{}),
	}
	s := &Service{ops: fileadapter.NewFileAdapter(paths), embedder: ge}
	s.cur.Store(emptySnapshot())
	s.jobs = make(chan indexJob, jobQueueDepth)
	s.stop = make(chan struct{})
	s.indexerDone = make(chan struct{})
	go s.runIndexer()
	defer func() { _ = s.Close() }()

	// Job A (the cap-flush): loads turns 1..seedTurns, then blocks in the
	// embed call.
	s.EnqueueFlush("thr_1", watermark)
	select {
	case <-ge.entered:
	case <-time.After(10 * time.Second):
		close(ge.gate)
		t.Fatal("flush embed never entered — indexer not in flight")
	}

	// The probe turn lands AFTER job A's load — only job B's load sees it.
	probeText := fmt.Sprintf("turn %d %s spectroscopy luminosity", probeTurn, probeToken)
	if err := store.AppendThreadTurn(paths, "thr_1", probeTurn, probeText); err != nil {
		t.Fatalf("append probe turn: %v", err)
	}

	// Job B (the same-turn dormancy flush): SAME watermark.
	s.EnqueueFlush("thr_1", watermark)
	close(ge.gate)

	// Wait for both jobs to drain (pendingFlush → 0).
	deadline := time.Now().Add(10 * time.Second)
	for s.pendingFlushDepth("thr_1") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("flush jobs never drained")
		}
		time.Sleep(time.Millisecond)
	}

	// The publish assertion: job B's superset — including the probe turn —
	// is in the fine tier. Under the `<=` guard B was dropped on the tie and
	// the probe turn has no vector anywhere.
	found := false
	for _, cv := range s.cur.Load().fine["thr_1"] {
		if cv.TurnNumber == probeTurn {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("probe turn %d not published: the equal-watermark FIFO-later flush was dropped (fine tier holds %d chunks)",
			probeTurn, len(s.cur.Load().fine["thr_1"]))
	}

	// Findability through the public surface, embedding tier only (see doc
	// comment for why EngagedDebtWindow stays 0).
	results, err := s.Recall(context.Background(), Request{
		QuerySymbols:      []string{probeToken},
		QueryText:         probeText,
		Engaged:           "thr_1",
		Exclude:           map[string]struct{}{"thr_1": {}},
		EngagedDebtWindow: 0,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, r := range results {
		if r.ThreadID != "thr_1" || r.IntraThread == nil {
			continue
		}
		for _, turn := range r.IntraThread.Turns {
			if turn == probeTurn {
				return // published and findable
			}
		}
	}
	t.Fatalf("probe turn %d unfindable through Recall after both flushes completed (results=%+v)", probeTurn, results)
}
