package measure

import (
	"context"
	"fmt"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// TestRankTier pins the three-tier assignment the Recall ordering relies on
// (burn-down Wave 3, Score-0 lexical ranking): a lexical-floor completeness
// hit (intra with Score 0, no other signal) must be tier 2 — below every
// scored candidate — while an intra hit carrying a real cosine, an embedding
// hit, and a symbolic hit are all scored candidates.
func TestRankTier(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want int
	}{
		{"embedding", Result{Embedding: &EmbeddingHit{Score: 0.8}}, 0},
		{"intra-scored", Result{IntraThread: &IntraThreadHit{Score: 0.7}}, 0},
		{"embedding+intra-lexical", Result{Embedding: &EmbeddingHit{Score: 0.8}, IntraThread: &IntraThreadHit{Score: 0}}, 0},
		{"symbolic-only", Result{Symbolic: &SymbolicHit{Score: 0.5}}, 1},
		{"symbolic+intra-lexical", Result{Symbolic: &SymbolicHit{Score: 0.5}, IntraThread: &IntraThreadHit{Score: 0}}, 1},
		{"lexical-floor-only", Result{IntraThread: &IntraThreadHit{Score: 0}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rankTier(tc.r); got != tc.want {
				t.Errorf("rankTier(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestRecall_LexicalFloorRanksAfterScored is the end-to-end guard for the
// Score-0 lexical ranking fix: a lexical-floor debt-window hit (a §3.4
// completeness guarantee, not a relevance signal) must rank AFTER every scored
// candidate so it cannot displace one out of the offered top-recallOfferK — yet
// it must stay IN the result set (completeness preserved). With three symbolic
// candidates and one engaged lexical-floor hit, the three scored candidates
// take the first three slots and the lexical hit lands last.
//
// Pre-fix the lexical hit sorted into the intra-flagged-first group and
// outranked the scored symbolic candidates, bumping one of them out of the
// offer's top-3 cut.
func TestRecall_LexicalFloorRanksAfterScored(t *testing.T) {
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	ts := "2026-04-01T00:00:00Z"
	const probe = "quasarxyz"
	// A multi-symbol query so the scored symbolic candidates clear any Jaccard
	// floor comfortably; probe is the one token that also lands in thr_1's
	// debt-window excerpt (its only path to surface, as a lexical-floor hit).
	querySyms := []string{probe, "pulsar", "nebula", "redshift"}

	seed := func(id string, anchors []string) {
		rec := memops.SpineRecord{
			ID: id, Project: "prj_1", Anchors: anchors, Summary: id,
			State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
		}
		if err := store.AppendSpineRecord(paths, rec); err != nil {
			t.Fatalf("seed spine %s: %v", id, err)
		}
		if err := store.SeedThread(paths, memops.Thread{Meta: memops.ThreadMeta{
			ID: id, Project: "prj_1", Anchors: anchors, Summary: id,
			State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
		}}); err != nil {
			t.Fatalf("seed thread %s: %v", id, err)
		}
	}

	// Three scored symbolic candidates: each carries the probe symbol as an
	// anchor, so ProposeRecall returns a positive Jaccard hit for each.
	for _, id := range []string{"thr_2", "thr_3", "thr_4"} {
		seed(id, querySyms)
	}

	// The engaged thread: probe appears ONLY in a scrolled-out debt-window
	// excerpt (no anchor), so it surfaces only via the lexical completeness
	// floor as a Score-0 intra hit.
	seed("thr_1", []string{"thr_1"})
	const total = store.ThreadTurnWindow + 16
	const debtTurn = 8
	for turn := 1; turn <= total; turn++ {
		text := fmt.Sprintf("turn %d generic filler content %d", turn, turn)
		if turn == debtTurn {
			text = fmt.Sprintf("turn %d %s deep content", turn, probe)
		}
		if err := store.AppendThreadTurn(paths, "thr_1", turn, text); err != nil {
			t.Fatalf("append turn %d: %v", turn, err)
		}
	}

	// Nil embedder: no embedding tier — the ordering under test is purely the
	// scored-symbolic (tier 1) vs. lexical-floor (tier 2) boundary, kept
	// deterministic and free of mock-cosine noise.
	s := &Service{ops: fileadapter.NewFileAdapter(paths)}
	s.cur.Store(emptySnapshot())

	results, err := s.Recall(context.Background(), Request{
		QuerySymbols:      querySyms,
		Engaged:           "thr_1",
		Exclude:           map[string]struct{}{"thr_1": {}},
		EngagedDebtWindow: 16,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("want 4 results (3 symbolic + 1 lexical floor), got %d: %+v", len(results), results)
	}

	// The lexical-floor hit must be LAST, behind all three scored candidates.
	last := results[len(results)-1]
	if last.ThreadID != "thr_1" {
		t.Errorf("lexical-floor hit thr_1 should rank last; got last=%s order=%v", last.ThreadID, ids(results))
	}
	if last.IntraThread == nil || last.Symbolic != nil || last.Embedding != nil {
		t.Errorf("thr_1 should be lexical-floor-only (intra Score 0, no symbolic/embedding); got %+v", last)
	}

	// The offered top-K cut must be exactly the three scored candidates — the
	// lexical hit does not displace any of them. offerK mirrors turn.recallOfferK
	// (the consumer's offer cut, out of this package); the ordering contract is
	// what makes that cut safe.
	const offerK = 3
	offer := results[:min(len(results), offerK)]
	for _, r := range offer {
		if r.ThreadID == "thr_1" {
			t.Errorf("lexical-floor hit displaced a scored candidate into the top-%d offer: %v", offerK, ids(results))
		}
	}

	// Completeness: the lexical hit is still present and carries the debt turn.
	if last.IntraThread == nil || !containsInt(last.IntraThread.Turns, debtTurn) {
		t.Errorf("lexical-floor completeness lost: debt turn %d not surfaced (%+v)", debtTurn, last.IntraThread)
	}
}

func ids(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ThreadID
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
