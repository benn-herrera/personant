#### The retraction rule

A retracted prompt does **not enter memory**; only a deliberate
re-submission puts its text there. No partial response is kept, at any point relative to the
model's first token. Before the first authoritative write, an interrupt leaves memory exactly as if the
turn never happened — turn count, staged symbols, active and dormant sets, symbol history and recall
marks included — except that the event log keeps the lines the turn already wrote (sources and byte counts, never text) and gains its `system.turn-aborted` line; the next start finds nothing to recover. After it, recovery
owns the turn ([§4.5.8](../4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)). Consequences:

1. **Rollback.** An abort before any authoritative write restores that state to its pre-turn value
   (otherwise, e.g., a staged task-class symbol cited by the retracted prompt would be permanently
   consumed). Failures after an authoritative write do **not** roll back; recovery owns them.
2. **No durable full-text artifact.** The abort record is one [§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md) event line —
   `system.turn-aborted` with `turn=` and `bytes=` (the prompt's byte count). Not the text.

An interrupt during a tool round counts as before the first authoritative write.

