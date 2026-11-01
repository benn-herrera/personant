### 3.11 Substrate commit cadence

The substrate is backed by **two** git DBs ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)): a **daily** DB (`.git-daily/`, disposable
per-turn) and a **primary** DB (`.git/`, permanent day-grain career history). Policy: **per-turn
commit to daily, day-grain commit to primary**.

Contents:

- [§3.11 Per-turn (daily)](per-turn-daily.md)
- [§3.11 Per-day (primary)](per-day-primary.md)
- [§3.11 gc cadence](gc-cadence.md)
- [§3.11 Durability](durability.md)
