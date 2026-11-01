#### Per-turn (daily)

Every turn commits its **scoped** write set to **daily** at turn close
(`Personant-Turn: <id>` trailer) — the ≤1-turn durability boundary of [§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md). Staging is scoped to
the turn's recorded write set (O(write-set), no tree walk); daily commits carry **no integrity
validation**, so per-turn cost stays flat. Structural changes (create/close a thread, create a
project) ride the per-turn daily commit and are folded into the day at the barrier.

