#### Projection

A pure function of current salience over all retained history symbols, active and
superseded alike. Rank by (1) class: a high-specificity identifier above an ordinary symbol of
equal count; (2) `count` descending; (3) recency: `last_active_turn` then `first_seen_turn`, newest
first; (4) `normalized` ascending. The top `anchor.projection-max` become `anchors`, in rank order;
none gives an empty list. `ever_central` is not a ranking tier; it only protects from eviction.

**Transitions** are labels read off the re-ranked top slice each turn, with no TTL. *Entry*: a
symbol in a slot this turn latches `ever_central`, sets `last_active_turn = turn` and is `active`
(also for a superseded symbol that ranks back in). *Exit*: a once-central symbol outside the slice
flips to `superseded`. A symbol is never evicted while `ever_central` as long as the protected set
fits the cap.

**Ordering invariant** (per turn, before the spine/frontmatter write): merge → project → evict, so
a symbol entering the projection has `ever_central` latched before the eviction predicate reads it.
`history_len` and `ever_central_count` staying flat over a run is a SPEC §9 sim obligation.

---

