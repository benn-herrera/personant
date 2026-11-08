### Active-set and dormant-set order

The active-set order and the dormant set are the memory service's `ActiveThreads` and
`DormantThreads` lists (most-recently-engaged first), persisted each turn to `working-set.json` in
the store directory (gitignored) so recency survives a relaunch. At turn close, after the turn's
engagements commit, each engaged thread id is removed from both lists if present and placed at the
front of `ActiveThreads`; if `ActiveThreads` then exceeds `layer.b-top-k`, its tail is demoted to
the front of `DormantThreads`. A second excerpt write for
one turn is refused with `ErrTurnAlreadyOwned`. The unknown-thread-id owner fallback ranks tagged
ids by `last_engaged_turn`, which is stored in the thread registry record but counted in the service run's turn counter;
it is deliberately not used for the no-tag owner-default, which ranks by active-set order because
the counter inverts recency across a relaunch. Active-set order cannot rank the fallback's
candidates: a tagged id may sit outside the active set, and a same-turn §5.5 fetch reorders the
list by fetch order.

---

