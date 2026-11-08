### Symbol extraction (two passes ordered cheapest-first)

Standard pattern: THESIS "The building blocks", row "Symbol extraction".

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file
   paths, URLs, code symbols, project-configured patterns, user `#tags`). Free, reliable, narrow.
2. **Model-emitted** (every turn) — the topic tag's anchor list provides named-entity coverage the
   deterministic pass can't reach.

At close the summarizer drafts the closing summary (§3.5); it contributes no symbols, and anchors are not summarizer-picked (below).

Contents:

- [Anchors and symbol history](anchors-and-symbol-history.md)
