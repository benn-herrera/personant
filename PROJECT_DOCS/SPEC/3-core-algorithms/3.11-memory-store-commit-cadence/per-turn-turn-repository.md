#### Per-turn (turn repository)

Every turn commits its writes to the turn repository at turn close (the ≤1-turn durability
boundary of [§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)), at a cost that does not grow with the store.

