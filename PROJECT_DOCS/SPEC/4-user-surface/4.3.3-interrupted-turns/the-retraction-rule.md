#### The retraction rule

A retracted prompt does **not enter memory**; only a deliberate
re-submission puts its text there. No partial response is kept, at any point relative to the
model's first token. Before the first canonical write, an interrupt leaves memory exactly as if the
turn never happened: no open [§4.5.8](../4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) turn scope, so the next start opens quiet. After it, recovery
owns the turn ([§4.5.8](../4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)). Consequences:

1. **Rollback.** The `user.prompt` delta has already run ([§3.0](../../3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/index.md) chain step 1) by the time of
   the retraction. A pre-canonical abort restores the run-volatile runtime state — turn counter, the
   [§3.10](../../3-core-algorithms/3.10-transient-data-lifecycle/index.md) cross-turn staging buffer, Layer B/C membership, history, recall-surfaced marks, embedding
   debt — to its pre-turn value (otherwise, e.g., a staged task-class symbol cited by the retracted
   prompt would be permanently consumed). The rollback is in-memory and must not perturb [§4.5.8](../4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)
   write ordering. Post-canonical failures do **not** roll back; recovery owns them.
2. **No durable full-text artifact.** The abort record is one [§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md) event line —
   `system.turn-aborted` with turn id, phase and byte count. Not the text.

A tool round writes nothing canonical, so an interrupt during one is pre-canonical.

