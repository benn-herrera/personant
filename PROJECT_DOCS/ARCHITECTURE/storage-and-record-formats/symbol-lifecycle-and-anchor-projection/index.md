### Symbol lifecycle and anchor projection

A thread's evolving identity lives in `history_symbols`; `anchors` is a deterministic re-derived
projection of it, with no model involved.

- `active` — not superseded: in the projection now, or never yet central (the zero value).
- `superseded` — once central, now ranked out. A label, not a candidacy gate: it competes on equal
  footing every turn. Retained, and protected from trimming while the protected set fits the cap, so an abandoned premise remains a findable handle.
- *trimmed* — the absence of the entry (capacity trimming above).

Contents:

- [Projection](projection.md)
