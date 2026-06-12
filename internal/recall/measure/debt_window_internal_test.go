package measure

import (
	"context"
	"fmt"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/scoring"
	"personant/internal/store"
)

// TestRecall_DebtWindowCompleteness is the SPEC §3.4 recall-completeness
// regression guard (#123): durable content that has scrolled out of the
// assembly window but is still pending its asynchronous fine-tier embedding
// flush MUST remain findable by the automatic recall path — there is no
// lag-window dead zone.
//
// It constructs the exact dead-zone state deterministically: a thread with
// MORE than the assembly window of retained excerpts on disk (so the oldest
// have scrolled out), where the live fine embedding tier covers the in-window
// excerpts but OMITS the recent scrolled-out tail (the embedding-debt window —
// the state during an async flush's in-flight lag). The runtime port reads the
// real on-disk excerpts; only the fine snapshot is hand-built to represent the
// lag.
//
// PRE-FIX this fails: the intra-thread pass read only snap.fine, so the
// debt-window excerpt — durable on disk but not yet embedded — was unfindable.
// POST-FIX the bounded lexical debt pass (EngagedDebtWindow > 0) surfaces it,
// closing the dead zone continuously.
func TestRecall_DebtWindowCompleteness(t *testing.T) {
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
	if err := store.SeedThread(paths, memops.Thread{
		Meta: memops.ThreadMeta{
			ID: "thr_1", Project: "prj_1", Anchors: []string{"thr_1"}, Summary: "thr_1",
			State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
		},
	}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	// Lay down more than the assembly window of excerpts so the oldest scroll
	// out. The debt window is the embeddingDebtCap=16 excerpts immediately
	// below the window; with total = window+cap, the oldest 16 (turns 1..16)
	// are exactly that debt window. Turn 8 carries a unique token that the
	// probe matches and that appears nowhere else.
	const total = store.ThreadTurnWindow + 16
	const debtTurn = 8
	const probeToken = "quasarxyz"
	excerpts := make(map[int]string, total)
	for turn := 1; turn <= total; turn++ {
		if turn == debtTurn {
			excerpts[turn] = fmt.Sprintf("turn %d %s spectroscopy luminosity", turn, probeToken)
		} else {
			excerpts[turn] = fmt.Sprintf("turn %d generic filler content body number %d", turn, turn)
		}
	}
	for turn, text := range excerpts {
		if err := store.AppendThreadTurn(paths, "thr_1", turn, text); err != nil {
			t.Fatalf("append turn %d: %v", turn, err)
		}
	}

	emb := model.NewMockEmbedder()
	s := &Service{ops: fileadapter.NewFileAdapter(paths), embedder: emb}

	// Build the fine tier to represent the async-flush lag: it covers the
	// in-window excerpts (turns 17..total) but NOT the debt window (turns
	// 1..16) — those are durable on disk yet not yet embedded.
	var fine []scoring.ChunkVector
	var hashes []string
	for turn := 17; turn <= total; turn++ {
		vecs, err := emb.Embed(context.Background(), []string{excerpts[turn]})
		if err != nil {
			t.Fatalf("embed turn %d: %v", turn, err)
		}
		fine = append(fine, scoring.ChunkVector{TurnNumber: turn, Vector: vecs[0]})
		hashes = append(hashes, contentHash(excerpts[turn]))
	}
	snap := emptySnapshot()
	snap.fine["thr_1"] = fine
	snap.fineHash["thr_1"] = hashes
	s.cur.Store(snap)

	// Sanity: the debt-window excerpt is genuinely absent from the fine tier —
	// the embedding intra-pass alone cannot find it (the dead zone).
	for _, cv := range fine {
		if cv.TurnNumber == debtTurn {
			t.Fatalf("test premise broken: debt turn %d is in the fine tier", debtTurn)
		}
	}

	req := Request{
		QuerySymbols:      []string{probeToken},
		QueryText:         excerpts[debtTurn],
		Engaged:           "thr_1",
		Exclude:           map[string]struct{}{"thr_1": {}},
		EngagedDebtWindow: 16, // the §6.5 debt cap — the bound the runtime passes
	}
	results, err := s.Recall(context.Background(), req)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	var hit *Result
	for i := range results {
		if results[i].ThreadID == "thr_1" {
			hit = &results[i]
			break
		}
	}
	if hit == nil || hit.IntraThread == nil {
		t.Fatalf("engaged thread surfaced no intra-thread hit; debt-window content is in a recall dead zone (results=%+v)", results)
	}
	found := false
	for _, turn := range hit.IntraThread.Turns {
		if turn == debtTurn {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("debt-window turn %d not in intra-thread hit %v — SPEC §3.4 completeness violated", debtTurn, hit.IntraThread.Turns)
	}
}

// TestRecall_NoDebtWindow_NoExtraPass is the byte-identical guard: with
// EngagedDebtWindow == 0 (no embedder-tracked debt, the symbolic-only or
// no-scroll-out case) the lexical debt pass does not run, so recall behaves
// exactly as before this fix. A thread whose only matching content is in the
// (here intentionally unscanned) debt window surfaces no intra hit.
func TestRecall_NoDebtWindow_NoExtraPass(t *testing.T) {
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
	if err := store.SeedThread(paths, memops.Thread{
		Meta: memops.ThreadMeta{
			ID: "thr_1", Project: "prj_1", Anchors: []string{"thr_1"}, Summary: "thr_1",
			State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
		},
	}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	const total = store.ThreadTurnWindow + 16
	const probeToken = "quasarxyz"
	for turn := 1; turn <= total; turn++ {
		text := fmt.Sprintf("turn %d generic filler %d", turn, turn)
		if turn == 8 {
			text = fmt.Sprintf("turn %d %s", turn, probeToken)
		}
		if err := store.AppendThreadTurn(paths, "thr_1", turn, text); err != nil {
			t.Fatalf("append turn %d: %v", turn, err)
		}
	}

	s := &Service{ops: fileadapter.NewFileAdapter(paths), embedder: model.NewMockEmbedder()}
	s.cur.Store(emptySnapshot()) // empty fine tier: nothing embedded

	results, err := s.Recall(context.Background(), Request{
		QuerySymbols:      []string{probeToken},
		QueryText:         probeToken,
		Engaged:           "thr_1",
		Exclude:           map[string]struct{}{"thr_1": {}},
		EngagedDebtWindow: 0, // debt pass disabled
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, r := range results {
		if r.ThreadID == "thr_1" && r.IntraThread != nil {
			t.Fatalf("intra hit fired with EngagedDebtWindow=0; debt pass must be inert: %+v", r.IntraThread)
		}
	}
}
