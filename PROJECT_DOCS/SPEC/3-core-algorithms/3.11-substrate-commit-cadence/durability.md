#### Durability

Content is durable to ≤1 turn at all times: the journal ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)) protects the
in-flight turn's raw bytes before any canonical write, and the scoped daily commit lands the turn at
close. [§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) governs the crash protocol and mid-run recovery.

---

