package measure

import (
	"context"
	"sync"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall/scoring"
	"personant/internal/store"
)

// countingMock wraps the deterministic MockEmbedder and tallies how many
// individual texts it was asked to embed. An embed call over a batch of N
// texts costs N, so the tally is the AC4 "only the delta" metric: an
// incremental flush of K new chunks costs 1 (coarse) + K, never the whole
// thread. It also delegates to the inner mock so reused vs freshly embedded
// vectors are bit-comparable (the inner mock is a pure function of the text).
type countingMock struct {
	inner *model.MockEmbedder
	mu    sync.Mutex
	texts int
}

func newCountingMock() *countingMock {
	return &countingMock{inner: model.NewMockEmbedder()}
}

func (c *countingMock) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	c.mu.Lock()
	c.texts += len(texts)
	c.mu.Unlock()
	return c.inner.Embed(ctx, texts)
}

func (c *countingMock) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.texts
}

// flushHome builds an isolated home with one thread carrying the given
// per-turn excerpts, plus a Service over it whose indexer goroutine is NOT
// started — the test drives processJob synchronously for deterministic
// embed-count assertions (no goroutine timing, no cache). The Service has no
// recall-cache dir (the in-memory fileadapter via NewService picks one up; we
// bypass NewService's cache wiring by constructing the Service directly with
// just ops+embedder), so processJob exercises only the snapshot path the bug
// lives in.
func flushHome(t *testing.T, emb model.Embedder, body string, excerpts map[int]string) (*Service, store.PersonantPaths) {
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
	if err := store.SeedThread(paths, memops.Thread{
		Meta: memops.ThreadMeta{
			ID: "thr_1", Project: "prj_1", Anchors: []string{"thr_1"}, Summary: "thr_1",
			State: memops.ThreadActive, Created: ts, LastEngaged: ts, StateChanged: ts, TurnCount: 1,
		},
		Body: body,
	}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	appendExcerpts(t, paths, excerpts)

	// Construct the Service directly (not NewService) so no vector cache is
	// wired in: the test isolates the snapshot reuse path, and processJob is
	// driven by hand rather than the goroutine. fileadapter is the only
	// MemoryOps implementation.
	s := &Service{ops: fileadapter.NewFileAdapter(paths), embedder: emb}
	s.cur.Store(emptySnapshot())
	return s, paths
}

func appendExcerpts(t *testing.T, paths store.PersonantPaths, excerpts map[int]string) {
	t.Helper()
	for turn, text := range excerpts {
		if err := store.AppendThreadTurn(paths, "thr_1", turn, text); err != nil {
			t.Fatalf("append turn %d: %v", turn, err)
		}
	}
}

// TestProcessJob_FirstFlushEmbedsAll: the first flush of a thread (no prior
// snapshot entry) embeds the body + every chunk — the from-scratch path.
func TestProcessJob_FirstFlushEmbedsAll(t *testing.T) {
	emb := newCountingMock()
	body := "## Turn 1\nquasar redshift\n\n## Turn 2\nglacier moraine\n\n## Turn 3\nenzyme catalysis\n"
	s, _ := flushHome(t, emb, body, map[int]string{
		1: "quasar redshift",
		2: "glacier moraine",
		3: "enzyme catalysis",
	})

	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 3})

	// 1 coarse body + 3 chunks = 4 texts.
	if got := emb.count(); got != 4 {
		t.Errorf("first flush embedded %d texts, want 4 (1 body + 3 chunks)", got)
	}
	snap := s.cur.Load()
	if len(snap.fine["thr_1"]) != 3 {
		t.Fatalf("fine tier = %d chunks, want 3", len(snap.fine["thr_1"]))
	}
}

// TestProcessJob_SecondFlushEmbedsOnlyDelta is the headline #102 check: after
// a first flush indexes the whole thread, a second flush that appends K new
// turns embeds ONLY the body + K new chunks — NOT the whole grown thread. The
// embed-count delta is the O(Δ), not O(thread), proof.
func TestProcessJob_SecondFlushEmbedsOnlyDelta(t *testing.T) {
	emb := newCountingMock()
	body1 := "## Turn 1\nquasar redshift\n\n## Turn 2\nglacier moraine\n\n## Turn 3\nenzyme catalysis\n"
	s, paths := flushHome(t, emb, body1, map[int]string{
		1: "quasar redshift",
		2: "glacier moraine",
		3: "enzyme catalysis",
	})

	// First flush: body + 3 chunks = 4.
	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 3})
	afterFirst := emb.count()
	if afterFirst != 4 {
		t.Fatalf("first flush embedded %d, want 4", afterFirst)
	}

	// Append K=2 new turns (4, 5) and bump the watermark.
	appendExcerpts(t, paths, map[int]string{
		4: "plasma tokamak confinement",
		5: "ledger accrual depreciation",
	})

	// Second flush.
	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 5})

	// Delta cost: body (always) + 2 new chunks = 3. The 3 unchanged chunks are
	// REUSED from the snapshot, never re-embedded — that is the O(Δ) guarantee.
	delta := emb.count() - afterFirst
	if delta != 3 {
		t.Errorf("second flush embedded %d delta texts, want 3 (1 body + 2 new chunks), NOT the whole 5-chunk thread", delta)
	}
	snap := s.cur.Load()
	if len(snap.fine["thr_1"]) != 5 {
		t.Fatalf("fine tier after second flush = %d chunks, want 5", len(snap.fine["thr_1"]))
	}
}

// TestProcessJob_ReuseIsEquivalent proves the reuse path produces the EXACT
// vectors the all-embed path would: an unchanged chunk after a second flush
// keeps its bit-identical first-flush vector. Equivalence-to-all-embed is the
// non-regression requirement — only the embedding WORK is skipped, never the
// result.
func TestProcessJob_ReuseIsEquivalent(t *testing.T) {
	emb := newCountingMock()
	body := "## Turn 1\nquasar redshift\n\n## Turn 2\nglacier moraine\n\n## Turn 3\nenzyme catalysis\n"
	s, paths := flushHome(t, emb, body, map[int]string{
		1: "quasar redshift",
		2: "glacier moraine",
		3: "enzyme catalysis",
	})

	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 3})
	firstVecs := chunkVecByTurn(s.cur.Load().fine["thr_1"])

	appendExcerpts(t, paths, map[int]string{4: "plasma tokamak confinement"})
	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 4})
	secondVecs := chunkVecByTurn(s.cur.Load().fine["thr_1"])

	// The three carried-over chunks must be bit-identical (reused, not re-embedded).
	for _, turn := range []int{1, 2, 3} {
		if !vecEqual(firstVecs[turn], secondVecs[turn]) {
			t.Errorf("turn %d vector changed across flushes; reuse did not preserve the vector", turn)
		}
	}
	// And the new chunk's vector equals what the mock embeds for its text
	// (the all-embed path would produce the same — the mock is a pure fn).
	want, _ := emb.inner.Embed(context.Background(), []string{"plasma tokamak confinement"})
	if !vecEqual(secondVecs[4], want[0]) {
		t.Errorf("new chunk turn 4 vector != direct embed of its text")
	}
}

// TestProcessJob_ChangedChunkReEmbeds: a chunk whose text changed at an
// existing turn (hash mismatch) is re-embedded, not reused — the reuse key is
// (turn, content hash), so a content change forces a fresh vector.
func TestProcessJob_ChangedChunkReEmbeds(t *testing.T) {
	emb := newCountingMock()
	body := "## Turn 1\nquasar redshift\n\n## Turn 2\nglacier moraine\n"
	s, paths := flushHome(t, emb, body, map[int]string{
		1: "quasar redshift",
		2: "glacier moraine",
	})

	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 2})
	afterFirst := emb.count() // 1 body + 2 chunks = 3
	firstVecs := chunkVecByTurn(s.cur.Load().fine["thr_1"])

	// Overwrite turn 2's excerpt text (same turn number, new content).
	if err := store.AppendThreadTurn(paths, "thr_1", 2, "tropical monsoon precipitation"); err != nil {
		t.Fatalf("rewrite turn 2: %v", err)
	}
	s.processJob(context.Background(), indexJob{threadID: "thr_1", dispatchTurncount: 3})

	// Delta: body (always) + turn-2 re-embed = 2. Turn 1 is reused.
	if delta := emb.count() - afterFirst; delta != 2 {
		t.Errorf("changed-chunk flush embedded %d delta, want 2 (body + changed turn 2)", delta)
	}
	secondVecs := chunkVecByTurn(s.cur.Load().fine["thr_1"])
	if !vecEqual(firstVecs[1], secondVecs[1]) {
		t.Errorf("unchanged turn 1 was re-embedded (vector differs)")
	}
	if vecEqual(firstVecs[2], secondVecs[2]) {
		t.Errorf("changed turn 2 kept its stale vector; expected a fresh embed")
	}
}

// TestProcessJob_MissingPriorFallsBack: when the prior snapshot is missing a
// chunk the flush would otherwise reuse, it falls back to embedding it —
// safety over cleverness, never serving a stale/missing vector. Modeled here
// by a fine/fineHash length mismatch (priorChunkVectors' defensive guard) and
// the first-flush no-prior case.
func TestProcessJob_MissingPriorFallsBack(t *testing.T) {
	// Direct unit check on the reuse-lookup builder: a snapshot whose fine and
	// fineHash disagree in length cannot key reliably → empty map (embed all).
	s := &Service{}
	snap := emptySnapshot()
	snap.fine["thr_1"] = []scoring.ChunkVector{{TurnNumber: 1, Vector: vec(1, 0)}, {TurnNumber: 2, Vector: vec(0, 1)}}
	snap.fineHash["thr_1"] = []string{"only-one-hash"} // length mismatch
	s.cur.Store(snap)
	if got := s.priorChunkVectors("thr_1"); got != nil {
		t.Errorf("length-mismatch prior should yield no reuse, got %d entries", len(got))
	}

	// A thread with no prior entry at all yields an empty reuse map → first
	// flush embeds everything (covered by FirstFlushEmbedsAll; asserted here on
	// the builder directly).
	if got := s.priorChunkVectors("thr_absent"); len(got) != 0 {
		t.Errorf("absent prior should yield no reuse, got %d entries", len(got))
	}
}

// chunkVecByTurn indexes a fine-tier slice by turn number for comparison.
func chunkVecByTurn(chunks []scoring.ChunkVector) map[int][]float64 {
	out := make(map[int][]float64, len(chunks))
	for _, c := range chunks {
		out[c.TurnNumber] = c.Vector
	}
	return out
}

// vecEqual reports exact element-wise equality (the mock embedder is
// deterministic, so a reused vector is bit-identical to a re-embed).
func vecEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
