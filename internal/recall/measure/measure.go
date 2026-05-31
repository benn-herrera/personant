// Package measure is the application-side §3.4 recall stack — the
// substrate-coupled glue that the experience layer uses to drive
// recall during a turn.
//
// It depends on the memops.MemoryOps port for substrate access (thread
// listing, body loading, log writes) and on the substrate-free
// scoring primitives in recall/scoring for the actual Jaccard and
// cosine math. The split is the architectural boundary called out in
// the MAD architecture-and-standards review: scoring is pure / testable
// without any substrate; measure is the substrate-aware composition.
package measure

import (
	"context"
	"fmt"
	"sort"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/scoring"
)

// maxEmbedChars caps the text sent to the embedder for one thread or
// query. Thread bodies rarely approach this; it keeps an /embeddings
// request within a typical model context window.
const maxEmbedChars = 28000

// Recaller is the §3.4 recall stack behind one contract. Experience-
// layer code (the recall UI surface) depends only on this interface
// and the Result type; the substrate port, the embedding model, and
// the embedding index are held inside the implementation and never
// exposed. That is the insulation boundary between experience logic
// and data/substrate logic — recall internals (layer composition,
// models, indexes, eventually layer-3 model judgment) can change
// without touching any caller.
type Recaller interface {
	// Prepare warms the recaller — e.g. builds the embedding index.
	// Called once after construction. A symbolic-only recaller no-ops.
	Prepare(ctx context.Context) error

	// Recall returns merged, ranked recall candidates for one turn.
	Recall(ctx context.Context, req Request) ([]Result, error)
}

// Request is the per-turn input to recall.
type Request struct {
	// QuerySymbols is the turn's coalesced symbol set — layer 1 input.
	QuerySymbols []string
	// QueryText is the turn's user input — layer 2 (embedding) input.
	QueryText string
	// Project scopes the layer-1 symbolic match. Empty → no filter.
	Project string
	// Exclude is the set of thread IDs to omit — typically threads
	// engaged this turn.
	Exclude map[string]struct{}
}

// SymbolicHit is the layer-1 detail for a recalled thread.
type SymbolicHit struct {
	Score          float64
	MatchedSymbols []string
}

// EmbeddingHit is the layer-2 detail for a recalled thread.
type EmbeddingHit struct {
	Score float64
}

// Result is one merged recall candidate. Symbolic / Embedding are nil
// when that layer did not fire for the thread.
type Result struct {
	ThreadID  string
	Score     float64 // unified ranking score (see Service.Recall)
	Symbolic  *SymbolicHit
	Embedding *EmbeddingHit
}

// Layers reports which recall layers fired for this result.
func (r Result) Layers() []string {
	var out []string
	if r.Symbolic != nil {
		out = append(out, "symbolic")
	}
	if r.Embedding != nil {
		out = append(out, "embedding")
	}
	return out
}

// Service is the default Recaller. It composes §3.4 layer 1 (symbolic
// Jaccard, via the MemoryOps port) and layer 2 (embedding cosine, over
// an in-memory thread-embedding index it owns and builds). Layer 3
// (model judgment) will slot in here later with no interface change.
type Service struct {
	ops      memops.MemoryOps
	embedder model.Embedder // nil → symbolic-only recall
	index    []scoring.ThreadVector
}

// NewService constructs a Service. A nil embedder yields a
// symbolic-only recaller — embedding recall is opt-in per provider.
func NewService(ops memops.MemoryOps, embedder model.Embedder) *Service {
	return &Service{ops: ops, embedder: embedder}
}

// AddThread incrementally embeds one thread's current body and appends
// (or replaces, if already present) its vector in the layer-2 index. It
// is the incremental counterpart to Prepare's batch build: a long-lived
// session creates threads continuously, and without per-thread index
// upkeep the embedding layer can never recall any thread created after
// the one-shot Prepare ran (it is structurally absent from the index).
//
// A no-op when no embedder is configured. Safe to call before Prepare;
// the index simply starts as this one entry.
//
// PRODUCTION RELEVANCE: the embedding index for a single-career session
// spanning thousands of threads cannot rely on session-start Prepare
// alone — every thread born mid-session would be invisible to embedding
// recall until the next process restart. AddThread is the seam a
// long-lived runtime calls at thread-create (and at body-grow, if it
// wants the index to track edits) to keep the index current.
func (s *Service) AddThread(ctx context.Context, threadID string) error {
	if s.embedder == nil {
		return nil
	}
	thr, err := s.ops.LoadThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("recall: add-to-index load %s: %w", threadID, err)
	}
	vecs, err := s.embedder.Embed(ctx, []string{truncateForEmbed(thr.Body)})
	if err != nil {
		return fmt.Errorf("recall: add-to-index embed %s: %w", threadID, err)
	}
	if len(vecs) != 1 {
		return fmt.Errorf("recall: add-to-index %s: embedder returned %d vectors for 1 input", threadID, len(vecs))
	}
	tv := scoring.ThreadVector{ThreadID: threadID, Vector: vecs[0]}
	for i := range s.index {
		if s.index[i].ThreadID == threadID {
			s.index[i] = tv // replace an existing entry (body grew)
			return nil
		}
	}
	s.index = append(s.index, tv)
	return nil
}

// Prepare builds the in-memory layer-2 embedding index by embedding
// every thread's body. A no-op when no embedder is configured.
//
// Session-scoped batch build: it embeds every thread that exists at call
// time. Threads created AFTER Prepare are not in the index until either a
// re-Prepare or an incremental AddThread call (the long-lived-session
// path — see AddThread's PRODUCTION RELEVANCE note).
func (s *Service) Prepare(ctx context.Context) error {
	s.index = nil
	if s.embedder == nil {
		return nil
	}
	recs, err := s.ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return fmt.Errorf("recall: list threads: %w", err)
	}
	if len(recs) == 0 {
		return nil
	}
	texts := make([]string, len(recs))
	for i, r := range recs {
		thr, err := s.ops.LoadThread(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("recall: load %s: %w", r.ID, err)
		}
		texts[i] = truncateForEmbed(thr.Body)
	}
	vecs, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("recall: embed index: %w", err)
	}
	if len(vecs) != len(recs) {
		return fmt.Errorf("recall: %d vectors for %d threads", len(vecs), len(recs))
	}
	idx := make([]scoring.ThreadVector, len(recs))
	for i := range recs {
		idx[i] = scoring.ThreadVector{ThreadID: recs[i].ID, Vector: vecs[i]}
	}
	s.index = idx
	return nil
}

// Recall runs the recall layers and returns merged candidates ranked
// for the experience layer.
//
// Layers run as parallel signals, not a cascade — the C.6 calibration
// showed symbolic Jaccard recall collapses under vocabulary drift, so
// it must not gate the embedding scan. Layer 1 failure (a substrate
// error) is returned. Layer 2 failure (an embedding call) is logged
// and swallowed — recall is opportunistic and degrades to symbolic.
//
// Ranking (v0.1, provisional — calibration will tune it): results
// where the embedding layer fired rank above symbolic-only results,
// each group ordered by its layer score. Score carries the embedding
// cosine when layer 2 fired, else the symbolic Jaccard score.
func (s *Service) Recall(ctx context.Context, req Request) ([]Result, error) {
	merged := map[string]*Result{}

	symbolic, err := s.ops.ProposeRecall(ctx, req.QuerySymbols, memops.RecallOptions{
		Project: req.Project,
		Exclude: req.Exclude,
	})
	if err != nil {
		return nil, fmt.Errorf("recall: symbolic: %w", err)
	}
	for _, c := range symbolic {
		merged[c.ThreadID] = &Result{
			ThreadID: c.ThreadID,
			Score:    c.Score,
			Symbolic: &SymbolicHit{Score: c.Score, MatchedSymbols: c.MatchedSymbols},
		}
	}

	for _, c := range s.embeddingCandidates(ctx, req) {
		r := merged[c.ThreadID]
		if r == nil {
			r = &Result{ThreadID: c.ThreadID}
			merged[c.ThreadID] = r
		}
		r.Embedding = &EmbeddingHit{Score: c.Score}
		r.Score = c.Score // embedding score dominates the unified rank
	}

	out := make([]Result, 0, len(merged))
	for _, r := range merged {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ie, je := out[i].Embedding != nil, out[j].Embedding != nil
		if ie != je {
			return ie // embedding-fired results first
		}
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ThreadID < out[j].ThreadID
	})
	return out, nil
}

// embeddingCandidates runs layer 2. Returns nil (no candidates) when
// no embedder, an empty index, or an empty query — and on an embedding
// call failure, which it logs and swallows.
func (s *Service) embeddingCandidates(ctx context.Context, req Request) []scoring.EmbeddingCandidate {
	if s.embedder == nil || len(s.index) == 0 || req.QueryText == "" {
		return nil
	}
	vecs, err := s.embedder.Embed(ctx, []string{truncateForEmbed(req.QueryText)})
	if err != nil || len(vecs) != 1 {
		if err == nil {
			err = fmt.Errorf("embedder returned %d vectors for 1 input", len(vecs))
		}
		_ = s.ops.Log(ctx, memops.LogCategoryRecall, "embed-error", err.Error())
		return nil
	}
	return scoring.ProposeEmbedding(vecs[0], s.index, scoring.EmbeddingOptions{Exclude: req.Exclude})
}

// truncateForEmbed bounds text sent to the embedder. Byte truncation
// may clip a trailing multi-byte rune; embedding endpoints tolerate
// that, and thread bodies rarely reach the cap.
func truncateForEmbed(s string) string {
	if len(s) > maxEmbedChars {
		return s[:maxEmbedChars]
	}
	return s
}
