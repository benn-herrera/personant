#### Recall candidates

At turn close the merged candidates are logged per signal (`spine.match-fire` /
`spine.embed-match-fire` / `spine.intra-match-fire`), and the top 3 are taken forward. Each is
judged by its score against its OWN signal's threshold:

- **Auto-fetch band** — score ≥ `recall.symbolic-auto-threshold` (0.65) for a symbolic-scored
  candidate, or ≥ `recall.cosine-auto-threshold` (0.75) for an embedding- or intra-thread-scored
  one ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)).
  The thread is fetched automatically through the fetch path every Layer-B fetch takes, logged
  `recall.accept` ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)), and its
  `recall_fires` ([§2.2](../../2-data-model/2.2-thread-registry-record-schema-spine-jsonl/index.md)) is
  incremented.
- **Below the auto-fetch bar** — nothing beyond the match-fire record. Nothing about the candidate
  reaches the model.

A non-intra candidate already resident in the active set is skipped: it is already in context. An
intra-thread hit is exempt (its subject is the engaged thread's early content); the in-context
exclusion below governs it.
