### Recall mechanisms (four, layered by cost)

1. **Model-native recognition** (default; cheapest). The spine is in window; the model recognizes
   prior topics directly when a turn engages them. Emits topic tag at turn start. Handles synonyms
   and paraphrases for free since the model is the recognition oracle.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols match a retired
   thread's anchors above threshold, the thread is brought forward by band (SPEC §3.4): a candidate
   at or above its tier's auto threshold is fetched with one visible line and no question; the band
   between the surface and auto thresholds is asked: *"We talked about X 2 days ago — pick up
   there?"*; nothing below surfaces (rationale: "The model drafts, deterministic code writes"). With
   no channel to the user, recall stays log-only.
   **Open:** U4 — the channel for the auto line and the ask offer. Under opencode a plugin has a
   yes/no prompt and no pick-list, so a multi-candidate offer cannot be a numbers/all/none list.
3. **Cross-project recall** (Layer A2 → fetch). Per-project anchor digests in A2 are scanned each
   turn. Same opportunistic mechanism as (2), slightly higher threshold.
4. **Intra-thread recall of scrolled-out early content** (#109/#111). For the engaged long-running
   thread, the fine-tier chunk embedding index recalls early turns that have scrolled out of the
   FIFO assembly window. The engaged thread bypasses the coarse gate and runs directly against its
   own indexed chunks. On threads long enough to have a built summary tree, an O(log n) beam descent
   (`scoring.DescendChunks`) replaces the flat O(C_main) scan — the sleep cycle builds and maintains
   the per-thread summary hierarchy (`measure.RebuildTrees`); absent or stale trees fall back to the
   flat scan with no correctness loss (W8). The fine tier is filled asynchronously, so it lags
   scroll-out by up to the embedding-debt cap; the recall-completeness invariant (SPEC §3.4) forbids
   a dead zone there, so the automatic intra path also runs a **bounded lexical pass** over exactly
   that debt window (`exact.MatchExcerptsBySymbols` over `MemoryOps.LoadDebtWindowExcerpts`, ≤ the
   cap, query-symbol match) and unions the hits (#123).

   The index is maintained by a single background indexer that swaps an `atomic.Pointer` snapshot,
   so recall reads never block; a per-thread turn-count watermark stops a stale in-flight embed from
   overwriting a fresher vector, and a flush arriving at an *equal* watermark (the same-turn
   cap-flush plus dormancy-flush double enqueue) always publishes. The lexical floor scans `(1 +
   pending) × cap` excerpts: `cap` is the embedding-debt cap (16) and `pending` counts
   enqueued-but-unpublished flush jobs — 0 at steady state and at most 258 at the v0.1 queue depth
   of 256 (queue plus one in process plus one blocked enqueue). Only a genuinely failed flush
   retires a pending job without publishing. Persisted vectors and summary trees are `.vec`/`.tree`
   sidecars under `<home>/.recall-cache/` (`internal/recall/measure/veccache.go`, `treecache.go`).

**Decline categorization** is an input to directive accrual (cross-project aggregate only; see
"Personalization through directive accrual") so that a bad match does not suppress good offers. SPEC §4.3 holds the reason enum and its effects; how a reason is captured waits on U4.

Contents:

- [Guarantee boundary — findability, not reminiscence](guarantee-boundary-findability-not-reminiscence.md)
