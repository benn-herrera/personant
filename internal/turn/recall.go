package turn

import (
	"context"
	"fmt"
	"strings"

	"personant/internal/memops"
	"personant/internal/recall"
)

// maxEmbedChars caps the text sent to the embedder for one thread or
// query. Thread bodies rarely approach this; it keeps any one
// /embeddings request within a typical model context window.
const maxEmbedChars = 28000

// BuildEmbeddingIndex populates the in-memory §3.4 layer-2 thread
// embedding index: every thread's body is embedded and held as a
// recall.ThreadVector. A no-op when no Embedder is configured.
//
// Session-scoped: built once at session setup. Threads created within
// the session are not re-embedded until the next build — acceptable
// while recall is log-only; incremental refresh is a later increment.
//
// Embedding failure is returned, not swallowed: the caller decides
// whether to proceed without embedding recall (chat does — it logs a
// warning and continues with symbolic recall only).
func (s *State) BuildEmbeddingIndex(ctx context.Context) error {
	s.embedIndex = nil
	if s.Embedder == nil {
		return nil
	}
	recs, err := s.Ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return fmt.Errorf("embedding index: list threads: %w", err)
	}
	if len(recs) == 0 {
		return nil
	}
	texts := make([]string, len(recs))
	for i, r := range recs {
		thr, err := s.Ops.LoadThread(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("embedding index: load %s: %w", r.ID, err)
		}
		texts[i] = truncateForEmbed(thr.Body)
	}
	vecs, err := s.Embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embedding index: embed: %w", err)
	}
	if len(vecs) != len(recs) {
		return fmt.Errorf("embedding index: %d vectors for %d threads", len(vecs), len(recs))
	}
	idx := make([]recall.ThreadVector, len(recs))
	for i := range recs {
		idx[i] = recall.ThreadVector{ThreadID: recs[i].ID, Vector: vecs[i]}
	}
	s.embedIndex = idx
	return nil
}

// surfaceRecallCandidates runs the §3.4 recall layers over this turn
// and logs the matches. v0.1 only logs; there is no UI surface and no
// SpineRecord.RecallFires increment (that field reserves "matches that
// resulted in fetch" per §2.2; until a UI surface lands a match isn't
// a fetch).
//
// Two layers run, independently:
//
//   - Symbolic Jaccard (layer 1) over the turn's coalesced symbol set
//     — high precision, low recall under vocabulary drift.
//   - Embedding cosine (layer 2) over the user input — drift-robust;
//     the primary recall scan when an Embedder is configured.
//
// They are parallel signals, not a cascade: the C.6 calibration showed
// symbolic recall collapses under drift, so it must not gate the
// embedding scan. engagedSet (threads engaged this turn) is excluded
// from both — an engaged thread doesn't shadow the turn's own
// engagement signal.
func surfaceRecallCandidates(ctx context.Context, state *State, userInput string, engagedSet map[string]struct{}) error {
	if err := surfaceSymbolicRecall(ctx, state, engagedSet); err != nil {
		return err
	}
	surfaceEmbeddingRecall(ctx, state, userInput, engagedSet)
	return nil
}

// surfaceSymbolicRecall runs the §3.4 layer-1 symbolic Jaccard
// pre-filter and logs each candidate as a spine.match-fire event.
func surfaceSymbolicRecall(ctx context.Context, state *State, engagedSet map[string]struct{}) error {
	query := state.coalesce.symbolList()
	candidates, err := state.Ops.ProposeRecall(ctx, query, memops.RecallOptions{
		Project: state.ActiveProject.ID,
		Exclude: engagedSet,
	})
	if err != nil {
		return fmt.Errorf("recall propose: %w", err)
	}
	querySize := len(state.coalesce.symbols)
	for _, c := range candidates {
		details := fmt.Sprintf("%s score=%.2f matched=%s query_size=%d",
			c.ThreadID,
			c.Score,
			strings.Join(c.MatchedSymbols, ","),
			querySize,
		)
		if err := state.Ops.Log(ctx, "spine", "match-fire", details); err != nil {
			return fmt.Errorf("log spine.match-fire: %w", err)
		}
	}
	return nil
}

// surfaceEmbeddingRecall runs the §3.4 layer-2 embedding cosine match:
// the user input is embedded and cosine-compared against the in-memory
// thread embedding index; each candidate is logged as a
// spine.embed-match-fire event.
//
// Best-effort: a no-op when no Embedder or empty index; an embedding
// call failure is logged and swallowed (recall is opportunistic and
// must never abort the turn).
func surfaceEmbeddingRecall(ctx context.Context, state *State, userInput string, engagedSet map[string]struct{}) {
	if state.Embedder == nil || len(state.embedIndex) == 0 {
		return
	}
	q := strings.TrimSpace(userInput)
	if q == "" {
		return
	}
	vecs, err := state.Embedder.Embed(ctx, []string{truncateForEmbed(q)})
	if err != nil || len(vecs) != 1 {
		if err == nil {
			err = fmt.Errorf("embedder returned %d vectors for 1 input", len(vecs))
		}
		_ = state.Ops.Log(ctx, "recall", "embed-error", sanitizeDetail(err.Error()))
		return
	}
	candidates := recall.ProposeEmbedding(vecs[0], state.embedIndex, recall.EmbeddingOptions{
		Exclude: engagedSet,
	})
	for _, c := range candidates {
		details := fmt.Sprintf("%s score=%.3f query_chars=%d", c.ThreadID, c.Score, len(q))
		if err := state.Ops.Log(ctx, "spine", "embed-match-fire", details); err != nil {
			_ = state.Ops.Log(ctx, "recall", "embed-error", sanitizeDetail(err.Error()))
			return
		}
	}
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
