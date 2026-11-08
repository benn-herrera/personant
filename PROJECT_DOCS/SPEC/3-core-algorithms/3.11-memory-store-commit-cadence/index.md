### 3.11 Memory store commit cadence

The memory store is backed by **two** git repositories ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)): the **turn repository**
(`.git-daily/`, disposable, per-turn) and the **history repository** (`.git/`, permanent, day-grain
career history). Policy: **per-turn commit to the turn repository, day-grain commit to the history
repository**.

Contents:

- [§3.11 Per-turn (turn repository)](per-turn-turn-repository.md)
- [§3.11 Per-day (history repository)](per-day-history-repository.md)
- [§3.11 gc cadence](gc-cadence.md)
- [§3.11 Durability](durability.md)
