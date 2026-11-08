### Recall paths (four, ordered by cost)

Standard pattern: THESIS "The building blocks", row "Recall".

1. **Model-native recognition** (default; cheapest). The registry slot is always in the composed
   request; the model recognizes prior threads directly when a turn engages them. It emits the
   topic tag at turn start. Handles synonyms and paraphrases for free, since the model does the
   recognizing.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols match, above threshold, the anchors and history symbols of a thread of the active project that this turn did not engage — or, with an embedder configured, the turn's input is close in embedding similarity to a thread's indexed body — the thread is brought forward by band (SPEC §3.4). The embedding scan (`measure.Service`, `coarseFine`) selects the top threads by body vector and scores each by its best chunk; it is not limited to the active project, and another project's candidate is declined at the fetch (SPEC §3.2).
   The symbolic scorer matches on `T(thr) = anchors ∪ {h.normalized}` over the thread's
   `history_symbols`, so a `superseded` symbol stays matchable. The Jaccard numerator is a weighted
   sum: a matched symbol counts 1.0 if `active` in that thread and `recall.superseded-weight` if
   `superseded`, and a symbol that is also a projected anchor counts as active. The denominator
   (union size) is unchanged, and at weight 1.0 the score is byte-identical to the plain-count
   scorer. The label is read from the thread's frontmatter `lifecycle`, not from the derived index:
   `symbols.jsonl` `superseded_in` is the cheap-scan filter ("which threads abandoned premise X",
   O(1) per query symbol) and the scorer does not consume it.
3. **Cross-project recall.** Not built: a cross-project fetch and cross-project engagement are declined (SPEC §3.1, §3.2; ROADMAP "Cross-project recall").
4. **Intra-thread recall of scrolled-out early content.** For the engaged long-running thread, the
   fine-level chunk embedding index recalls early turns that have scrolled out of the
   recent-excerpt window. The engaged thread bypasses the coarse level (one vector per thread body) and runs directly against
   its own indexed chunks. Every thread is an indexed body: one coarse vector for the thread body
   plus per-excerpt chunk vectors. An active thread's recent-excerpt window is in the working set
   and is not embedded; excerpts scrolling out of it become index material. On threads long enough to have a built summary tree, an O(log n) beam
   descent (`scoring.DescendChunks`) replaces the flat O(C_main) scan (`C_main` is the engaged thread's chunk count) — consolidation builds and
   maintains the per-thread summary trees (`measure.RebuildTrees`); absent or stale trees fall back
   to the flat scan with no correctness loss. The summary tree serves SPEC §3.4 *Long threads* (no performance decay as a thread's length grows into the thousands): a flat scan's per-query cost grows with the thread's chunk count (`C_main`). The descent is approximate; how often it picks a different leaf than the flat scan is measured (`recall_intra_descent_divergence`, SPEC §9.4), and exact recall stays with the lexical level (SPEC §3.4). Summary trees are built only by consolidation (see "Memory consolidation"). The fine level is filled asynchronously, so it lags
   scroll-out by up to the unembedded backlog limit; the recall-completeness invariant (SPEC §3.4)
   forbids a gap in findability there, so the automatic intra path also runs the completeness floor,
   a lexical pass over exactly that backlog (`exact.MatchExcerptsBySymbols` over
   `MemoryOps.LoadDebtWindowExcerpts`, (1 + pending) × the limit (below), query-symbol match), and unions the hits.

   The index is maintained by a single background indexer that swaps an `atomic.Pointer` index
   snapshot, so recall reads never block; an embed sequence guard (the per-thread turn count each
   flush carries) stops a stale in-flight embed from overwriting a fresher vector, and a flush
   arriving at an *equal* count (the same-turn limit-flush plus dormancy-flush double enqueue)
   always publishes. Two triggers enqueue a flush: the unembedded backlog limit (16 turns of
   accumulated scrolled-out content, a provisional value calibrated in SPEC §9) and the dormancy
   transition, which flushes a thread's remaining backlog when it decays out of the working set so
   its whole body is indexed before it becomes a pure recall target. The completeness floor scans `(1 + pending) × limit` excerpts: `limit` is the
   unembedded backlog limit (16) and `pending` counts enqueued-but-unpublished flush jobs — 0 at
   steady state and at most 258 at the queue depth of 256 (queue plus one in process plus one
   blocked enqueue). Only a genuinely failed flush drops a pending job without publishing.
   Persisted vectors and summary trees are `.vec`/`.tree` sidecars under `.recall-cache/` in the
   store directory (`internal/recall/measure/veccache.go`, `treecache.go`). Each is keyed by a
   content-staleness marker (a body or chunk hash), written through by the indexer and loaded at
   startup; a miss is rebuilt, so a service run re-embeds only what changed, O(changed) rather than
   O(all).

   In-context exclusion: when an intra-thread candidate is accepted, the service records its
   turn-excerpts as in-window for the engaged thread (`recallWindowTurns` in `internal/turn`), and
   the recall stack drops them from both the intra-thread scan and the lexical completeness floor.
   The mark ends when the thread is demoted out of the active set. It is held in memory only:
   `working-set.json` restores the active set after a restart, but the composed request then no
   longer holds the accepted excerpts, and the marks and the window are rebuilt together.

**Decline categorization** is an input to feedback directive tuning (cross-project aggregate only;
see "Personalization through feedback directive tuning") so that a bad match does not suppress good
offers. SPEC §4.3 holds the reason enum and its effects; how a reason is captured waits on U4.

Contents:

- [Guarantee boundary — findability, not reminiscence](guarantee-boundary-findability-not-reminiscence.md)
