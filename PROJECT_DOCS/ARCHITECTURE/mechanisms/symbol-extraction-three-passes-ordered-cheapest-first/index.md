### Symbol extraction (three passes ordered cheapest-first)

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file
   paths, URLs, code symbols, project-configured patterns, user `#tags`). Free, reliable, narrow.
2. **Model-emitted** (every turn) — the topic tag's anchor list provides named-entity coverage the
   deterministic pass can't reach.
3. **Curator** (at retirement) — LLM drafts the closure summary (§3.5). A routine decayed thread
   takes the draft as written; an anchor-rich or long-engaged thread, or one whose draft failed, is
   queued as an exception for review (see "Closure and retirement"). Anchors are not curator-picked
   (below).

Contents:

- [Anchors vs. history (the evolving-anchor model)](anchors-vs-history-the-evolving-anchor-model.md)
