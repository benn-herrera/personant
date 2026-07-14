package measure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// gatedEmbedder wraps the deterministic mock embedder with a gate on
// flush-batch embeds, so a test can hold an indexer flush job provably
// in-flight (loaded its excerpts, blocked inside the embed call) while the
// test mutates on-disk state around it. Only multi-text batches gate: a
// fine-tier flush always embeds coarse+chunks (≥2 texts), while the recall
// query embed is a single text and must pass through so Recall itself never
// blocks.
type gatedEmbedder struct {
	inner   model.Embedder
	entered chan struct{} // signalled (non-blocking) when a flush batch reaches Embed
	gate    chan struct{} // closed to release blocked flush embeds
}

func (g *gatedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 1 {
		select {
		case g.entered <- struct{}{}:
		default:
		}
		<-g.gate
	}
	return g.inner.Embed(ctx, texts)
}

// seedDeadZoneThread lays down one active thread with `turns` retained
// excerpts (turn 1..turns), planting probeToken in probeTurn only. Returns
// the excerpt text by turn so the test can build queries from it.
func seedDeadZoneThread(t *testing.T, paths store.PersonantPaths, threadID string, turns, probeTurn int, probeToken string) map[int]string {
	t.Helper()
	ts := "2026-07-01T00:00:00Z"
	rec := memops.SpineRecord{
		ID: threadID, Project: "prj_1", Anchors: []string{threadID}, Summary: threadID,
		State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("seed spine: %v", err)
	}
	if err := store.SeedThread(paths, memops.Thread{
		Meta: memops.ThreadMeta{
			ID: threadID, Project: "prj_1", Anchors: []string{threadID}, Summary: threadID,
			State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
		},
	}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	texts := make(map[int]string, turns)
	for turn := 1; turn <= turns; turn++ {
		text := fmt.Sprintf("turn %d generic filler content body number %d", turn, turn)
		if turn == probeTurn {
			text = fmt.Sprintf("turn %d %s spectroscopy luminosity", turn, probeToken)
		}
		texts[turn] = text
		if err := store.AppendThreadTurn(paths, threadID, turn, text); err != nil {
			t.Fatalf("append turn %d: %v", turn, err)
		}
	}
	return texts
}

// TestRecall_CompletenessContinuousAcrossInFlightFlush is the BD-8 (§3.4)
// dead-zone proof. INVARIANT UNDER TEST (SPEC §3.4): recall completeness
// holds CONTINUOUSLY — durable on-disk content must be findable at every
// instant, including while an asynchronous fine-tier flush is in flight.
//
// The adversarial timeline it drives is the one the July recall review
// claimed re-opens the dead zone the #123 lexical floor closed:
//
//  1. A thread accrues exactly the §6.5 debt cap of scrolled-out excerpts;
//     the runtime fires a cap-flush (EnqueueFlush) and RESETS the per-thread
//     debt counter (turn.recordExcerptScrollOut).
//  2. The flush job is held in-flight (embedder gated): it has loaded the
//     excerpts but published NOTHING — the fine tier is still empty.
//  3. New excerpts keep scrolling out on top of the in-flight batch
//     (optionally enough to fire further cap-flushes, which queue behind
//     the stalled job — the indexer is a single goroutine).
//
// At that instant the truly-unflushed scrolled-out depth exceeds one cap.
// The oldest in-flight excerpts have no vectors (flush unpublished) AND sit
// below the newest-cap band the lexical completeness floor scans — so a
// query targeting one of them probes exactly the transient window the
// invariant forbids. The test asserts the probe turn is findable through
// the public Recall surface; HOW it is found (embedding tier, lexical
// floor, or any future mechanism) is deliberately unasserted — §3.4
// completeness is mechanism-agnostic.
func TestRecall_CompletenessContinuousAcrossInFlightFlush(t *testing.T) {
	// The runtime's §6.5 debt cap (turn.EmbeddingDebtCap). Mirrored as a
	// literal because package turn imports measure — the real constant is
	// unreachable from this internal test without an import cycle. The
	// companion TestRecall_DebtWindowCompleteness pins the same value.
	const debtCap = 16
	const probeTurn = 3 // inside the OLDEST in-flight batch (turns 1..debtCap)
	const probeToken = "pulsarqrst"

	cases := []struct {
		name string
		// queuedBatches: additional cap-sized scroll-out batches accrued —
		// and cap-flushes enqueued — behind the stalled in-flight flush.
		// 0 = the minimal single-in-flight case; 1 = indexer backlog.
		queuedBatches int
	}{
		{name: "one flush in flight", queuedBatches: 0},
		{name: "second batch queued behind stalled flush", queuedBatches: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths := store.PathsForHome(t.TempDir())
			if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
				t.Fatalf("store.Init: %v", err)
			}
			// debtCap scrolled-out excerpts (turns 1..debtCap) below a full
			// assembly window — the state at the instant the cap-flush fires.
			total := store.ThreadTurnWindow + debtCap
			texts := seedDeadZoneThread(t, paths, "thr_1", total, probeTurn, probeToken)

			ge := &gatedEmbedder{
				inner:   model.NewMockEmbedder(),
				entered: make(chan struct{}, 1),
				gate:    make(chan struct{}),
			}
			// Wire the Service exactly as Prepare does, minus the synchronous
			// warm build (which would embed the scrolled-out tail before the
			// scenario starts and erase the lag under test).
			s := &Service{ops: fileadapter.NewFileAdapter(paths), embedder: ge}
			s.cur.Store(emptySnapshot())
			s.jobs = make(chan indexJob, jobQueueDepth)
			s.stop = make(chan struct{})
			s.indexerDone = make(chan struct{})
			go s.runIndexer()
			defer func() { _ = s.Close() }()
			defer close(ge.gate) // release the stalled flush so Close can drain

			// (1) The cap-flush the runtime fires when debt hits the cap.
			s.EnqueueFlush("thr_1", total)
			// (2) Provably in-flight: the indexer has loaded the thread's
			// excerpts and is blocked inside the embed call.
			select {
			case <-ge.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("flush embed never entered — indexer not in flight")
			}

			// (3) Scroll-outs continue on top of the in-flight batch.
			next := total
			appendTurn := func() {
				next++
				if err := store.AppendThreadTurn(paths, "thr_1", next,
					fmt.Sprintf("turn %d generic filler content body number %d", next, next)); err != nil {
					t.Fatalf("append turn %d: %v", next, err)
				}
			}
			for i := 0; i < tc.queuedBatches; i++ {
				for j := 0; j < debtCap; j++ {
					appendTurn()
				}
				s.EnqueueFlush("thr_1", next) // the next cap-flush; queues behind the stalled job
			}
			for j := 0; j < debtCap/2; j++ {
				appendTurn() // partial accrual: live debt below the next cap
			}

			// Premise: nothing has been published for the thread — the flush
			// is genuinely unfinished, so the probe turn has no vector.
			if got := len(s.cur.Load().fine["thr_1"]); got != 0 {
				t.Fatalf("test premise broken: fine tier has %d chunks while flush is in flight", got)
			}

			// The recall query the runtime would issue: EngagedDebtWindow is
			// the debt CAP, exactly what turn.debtWindowBound passes.
			results, err := s.Recall(context.Background(), Request{
				QuerySymbols:      []string{probeToken},
				QueryText:         texts[probeTurn],
				Engaged:           "thr_1",
				Exclude:           map[string]struct{}{"thr_1": {}},
				EngagedDebtWindow: debtCap,
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
						return // findable — completeness held through the in-flight window
					}
				}
			}
			t.Fatalf("turn %d (in-flight, unembedded, below the newest-cap lexical band) is unfindable while %d excerpts are unflushed — SPEC §3.4 forbids this dead zone (results=%+v)",
				probeTurn, next-store.ThreadTurnWindow, results)
		})
	}
}
