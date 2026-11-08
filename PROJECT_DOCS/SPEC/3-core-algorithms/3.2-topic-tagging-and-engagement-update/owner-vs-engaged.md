#### Owner vs engaged

For each turn:
- the **owner** gets the turn excerpt, `turn_count++`, recency (`last_engaged`), `history_symbols`
  merge, and state→active resurrection ([§2.2.1](../../2-data-model/2.2-thread-registry-record-schema-spine-jsonl/2.2.1-thread-states.md)). A new-thread owner is created with the excerpt,
  `turn_count = 1`, and its `description` ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)).
- an **engaged-but-not-owner** thread gets recency, `history_symbols` merge, and state→active
  resurrection — but **no excerpt** and **no `turn_count++`**.

The active-set and dormant-set recency order ([§3.1](../3.1-working-set-composition/index.md)) and recall surfacing operate over all engaged
threads; the [§3.9](../3.9-working-set-content-dedup/index.md) file-edit application binds to the owner.

