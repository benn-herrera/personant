### Symbol lifecycle and anchor projection

A thread's evolving identity lives in `history_symbols`; `anchors` is a deterministic re-derived
projection of it, with no LLM involved.

- `active` — currently in a top-`anchor.projection-max` slot (the zero value).
- `superseded` — once central, now ranked out. A label, not a candidacy gate: it competes on equal
  footing every turn. Retained, not evicted, so an abandoned premise remains a findable handle.
- *evicted* — the absence of the entry (capacity eviction above).

Contents:

- [Projection](projection.md)
