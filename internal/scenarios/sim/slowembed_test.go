package sim

// B2 — sim slow-embedder mode + the first observation of the BD-8 (1+P)×cap
// dead-zone bound OUTSIDE measure's unit tests.
//
// slowEmbedder is the deterministic embed-blocking injection B2 asks for: a
// wrapping model.Embedder that gates multi-text (flush) batches on a release
// channel so a test can hold P flush jobs provably in-flight while it drives
// scroll-outs and a recall query. It mirrors the gatedEmbedder shape in
// measure's deadzone_inflight_internal_test.go, lifted to the sim package where
// it can wrap any mock rung's embedder.
//
// TestSimSlowEmbedder_DeadZoneWidenedBand then drives measure.Service through its
// PUBLIC API (NewService → Prepare → EnqueueFlush → Recall) with the slow
// embedder installed, sustains P=1, and asserts a probe target in the
// (1×cap, 2×cap] widened band is surfaced by the runtime's in-flight-widened
// lexical floor — and that the sim oracle's inDebtWindowDepth classifier, given
// the SAME observed pending, classifies exactly that band (and would NOT at
// pending=0). Prepare on the empty store starts the indexer without a warm build
// that would embed the tail and erase the lag — the same lag-preserving setup
// the measure unit test wires by hand, achieved here purely through public
// entry points.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
	"personant/internal/turn"
)

// slowEmbedder wraps a model.Embedder and blocks multi-text (flush) batches on a
// gate channel, tracking how many are currently blocked (the OBSERVED per-service
// pending-flush count — the sim-side ground truth for the runtime's P). Single-
// text batches (the recall query embed) pass straight through so Recall never
// blocks. Deterministic: no timers, no sleeps — the test decides when to release.
type slowEmbedder struct {
	inner    model.Embedder
	entered  chan struct{} // non-blocking signal when a flush batch reaches Embed
	gate     chan struct{} // closed by release() to unblock all held flush embeds
	inFlight atomic.Int64  // flush batches currently blocked in Embed
}

func newSlowEmbedder(inner model.Embedder) *slowEmbedder {
	return &slowEmbedder{
		inner:   inner,
		entered: make(chan struct{}, 1),
		gate:    make(chan struct{}),
	}
}

func (s *slowEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 1 { // a fine-tier flush embeds coarse+chunks (>=2 texts); a query embed is 1
		s.inFlight.Add(1)
		select {
		case s.entered <- struct{}{}:
		default:
		}
		<-s.gate
		s.inFlight.Add(-1)
	}
	return s.inner.Embed(ctx, texts)
}

// InFlight reports the OBSERVED number of flush batches currently blocked in
// Embed — the sim-side pending-flush count P the widened dead-zone band keys on.
func (s *slowEmbedder) InFlight() int { return int(s.inFlight.Load()) }

// release unblocks all held flush embeds so the indexer can drain on Close.
func (s *slowEmbedder) release() { close(s.gate) }

// TestSimSlowEmbedder_DeadZoneWidenedBand is the B2 first-observation test. It
// reproduces the measure BD-8 single-in-flight timeline at the sim level via the
// public measure API, then ties the runtime's widened lexical floor to the sim
// oracle's widened inDebtWindowDepth classifier.
func TestSimSlowEmbedder_DeadZoneWidenedBand(t *testing.T) {
	const probeTurn = 3 // inside the oldest in-flight batch, below the 1×cap band
	const probeToken = "pulsarqrst"
	dcap := turn.EmbeddingDebtCap

	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}

	slow := newSlowEmbedder(model.NewMockEmbedder())
	s := measure.NewService(fileadapter.NewFileAdapter(paths), slow)
	// Prepare on the EMPTY store: no threads to embed, so no gated batch — the
	// warm build is a no-op and the indexer starts clean. This is what lets the
	// lag survive: the thread is seeded AFTER Prepare, so its scrolled-out tail is
	// never synchronously embedded.
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = s.Close() }()
	defer slow.release() // release the held flush so Close can drain the indexer

	// Seed thr_1 with exactly a full assembly window + one debt cap of excerpts —
	// the state at the instant a cap-flush fires. probeToken lives only in
	// probeTurn; every other turn is generic filler.
	total := store.ThreadTurnWindow + dcap
	texts := seedSlowEmbedThread(t, paths, "thr_1", total, probeTurn, probeToken)

	// (1) The cap-flush the runtime fires at the debt cap. The indexer picks it up,
	//     loads the excerpts, and blocks inside the gated flush embed — P=1.
	s.EnqueueFlush("thr_1", total)
	select {
	case <-slow.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("flush embed never entered — indexer not in flight")
	}
	if p := slow.InFlight(); p < 1 {
		t.Fatalf("slow embedder InFlight=%d, want >=1 (a flush must be held in flight)", p)
	}

	// (2) Scroll-outs continue on top of the in-flight batch (partial accrual,
	//     below the next cap) — this is what pushes probeTurn's oldest excerpts
	//     below the newest-1×cap band the plain floor scans.
	next := total
	for j := 0; j < dcap/2; j++ {
		next++
		if err := store.AppendThreadTurn(paths, "thr_1", next,
			fmt.Sprintf("turn %d generic filler content body number %d", next, next)); err != nil {
			t.Fatalf("append turn %d: %v", next, err)
		}
	}

	// The probe target's turn-depth at query time, and the pending we OBSERVED.
	pending := slow.InFlight()
	depth := next - probeTurn

	// The classifier and the runtime must agree on the band. With the observed
	// pending the target is in the widened dead zone; without the widening (the
	// old 1×cap band) it would be classified out — the exact defect B2 fixes.
	if !inDebtWindowDepth(depth, pending) {
		t.Fatalf("inDebtWindowDepth(%d, pending=%d) = false — the probe target is not in the widened dead zone; "+
			"test setup is wrong (window=%d cap=%d)", depth, pending, store.ThreadTurnWindow, dcap)
	}
	if inDebtWindowDepth(depth, 0) {
		t.Fatalf("inDebtWindowDepth(%d, pending=0) = true — the target is inside the plain 1×cap band, so the "+
			"widening is not being exercised; pick a deeper probe turn", depth)
	}

	// (3) The recall query the runtime issues: EngagedDebtWindow is the plain debt
	//     CAP; Recall widens it internally to (1+P)×cap. The target has no vector
	//     (flush unpublished) and sits below the 1×cap band, so ONLY the widened
	//     lexical floor can surface it — the BD-8 bound, observed at the sim level.
	results, err := s.Recall(context.Background(), measure.Request{
		QuerySymbols:      []string{probeToken},
		QueryText:         texts[probeTurn],
		Engaged:           "thr_1",
		Exclude:           map[string]struct{}{"thr_1": {}},
		EngagedDebtWindow: dcap,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !intraHitContainsTurn(results, "thr_1", probeTurn) {
		t.Fatalf("turn %d (depth %d, pending %d, in the (1×cap,%d×cap] widened band) is unfindable while a flush is "+
			"in flight — the runtime's in-flight-widened lexical floor did not surface it (SPEC §3.4 / BD-8). results=%+v",
			probeTurn, depth, pending, 1+pending, results)
	}
}

// seedSlowEmbedThread lays down one active thread with `turns` retained excerpts
// (turns 1..turns), planting probeToken in probeTurn only, and returns the
// excerpt text by turn. Mirrors measure's seedDeadZoneThread using the public
// store API (that helper is package-private to measure's test).
func seedSlowEmbedThread(t *testing.T, paths store.PersonantPaths, threadID string, turns, probeTurn int, probeToken string) map[int]string {
	t.Helper()
	ts := "2026-07-01T00:00:00Z"
	meta := memops.ThreadMeta{
		ID: threadID, Project: "prj_1", Anchors: []string{threadID}, Summary: threadID,
		State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
	}
	if err := store.AppendSpineRecord(paths, memops.SpineRecord{
		ID: threadID, Project: "prj_1", Anchors: []string{threadID}, Summary: threadID,
		State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
	}); err != nil {
		t.Fatalf("seed spine: %v", err)
	}
	if err := store.SeedThread(paths, memops.Thread{Meta: meta}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	byTurn := make(map[int]string, turns)
	for tn := 1; tn <= turns; tn++ {
		text := fmt.Sprintf("turn %d generic filler content body number %d", tn, tn)
		if tn == probeTurn {
			text = fmt.Sprintf("turn %d %s spectroscopy luminosity", tn, probeToken)
		}
		byTurn[tn] = text
		if err := store.AppendThreadTurn(paths, threadID, tn, text); err != nil {
			t.Fatalf("append turn %d: %v", tn, err)
		}
	}
	return byTurn
}

// intraHitContainsTurn reports whether the recall results carry an intra-thread
// hit for threadID that includes the given turn number.
func intraHitContainsTurn(results []measure.Result, threadID string, turnNumber int) bool {
	for _, r := range results {
		if r.ThreadID != threadID || r.IntraThread == nil {
			continue
		}
		for _, tn := range r.IntraThread.Turns {
			if tn == turnNumber {
				return true
			}
		}
	}
	return false
}
