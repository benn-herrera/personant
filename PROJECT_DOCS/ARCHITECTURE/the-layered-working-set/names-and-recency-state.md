### Names and recency state

Layer B order and the dormant set are the runtime's `ActiveThreads` and
`DormantThreads` lists (most-recently-engaged first), persisted each turn to
`<home>/working-set.json` (gitignored) so recency survives a relaunch. A second excerpt write for
one turn is refused with `ErrTurnAlreadyOwned`. The phantom-resolution owner fallback ranks tagged
ids by `last_engaged_turn`, a session-scoped counter that is not in the spine record; it is
deliberately not used for the no-tag owner-default, which ranks by Layer B order because the counter
inverts recency across a relaunch. Layer B order cannot rank the fallback's candidates: a tagged id
may sit outside Layer B, and a same-turn §5.5 fetch reorders the list by fetch order.

---

