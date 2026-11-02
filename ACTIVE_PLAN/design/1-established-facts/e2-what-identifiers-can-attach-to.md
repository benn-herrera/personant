### E2. What identifiers can attach to

- **Today identifiers attach to nothing in place.** They are rendered per version into the separate
  section (`internal/dedup/livewindow.go:98–107`, `internal/workset/workset.go:449–450`).
- **The only per-turn positions are excerpts.**
  - Hot loose files sit in `turns/`. Cold lines sit in `folio.jsonl` as byte-exact copies.
  - Tiers move only at barrier step B1b (`store.SettleExcerptTiers`; barrier steps, completion and
    day-commit are described in ARCHITECTURE
    [Crash stability and the dual-repo substrate](../../../PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md)).
    The loose tail is `HotExcerptTail = ThreadTurnWindow + memops.EmbeddingDebtCap` (= 528,
    `internal/store/folio.go:27`).
  - Readers are tier-transparent and byte-exact (ARCHITECTURE "Thread directory", Excerpt tiers).
  - Therefore an identifier written into the excerpt text once, at turn close, is in place in both
    tiers forever and needs no rewrite. Any design that swaps literal text inside stored excerpts
    would have to rewrite folio lines. This design never does: literals are never written into an
    excerpt; they are added at composition.
- **The owner is `engaged[0]`.** File edits bind to `engaged[0]`, which is the turn's owner. Owner
  first: `internal/turn/engage.go:204–219`. Applied at `internal/turn/engage.go:309–311`. The
  excerpt that records the turn's file events is therefore the excerpt of the same thread whose
  store is updated.
