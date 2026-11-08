#### Projection

A pure function of current salience over all retained history symbols, active and
superseded alike. Rank by (1) class: a high-specificity identifier (URL, file path, git SHA) above any ordinary symbol,
whatever their counts; (2) `count` descending; (3) recency: `last_active_turn` then `first_seen_turn`, newest
first; (4) `normalized` ascending. The top `anchor.projection-max` become `anchors`, in rank order;
none gives an empty list. `ever_central` is not a ranking criterion; it only protects from trimming.

**Transitions** are labels read off the re-ranked top slice each turn, with no TTL. *Entry*: a
symbol in a slot this turn latches `ever_central`, sets `last_active_turn = turn` and is `active`
(also for a superseded symbol that ranks back in). *Exit*: a once-central symbol outside the slice
flips to `superseded`. A symbol is never trimmed while `ever_central` as long as the protected set
fits the cap.

**Ordering invariant** (per turn, before the thread registry/frontmatter write): merge → project →
trim, so a symbol entering the projection has `ever_central` latched before the trimming predicate
reads it.
`history_len` and `ever_central_count` staying flat over a run is a SPEC §9 sim obligation.

