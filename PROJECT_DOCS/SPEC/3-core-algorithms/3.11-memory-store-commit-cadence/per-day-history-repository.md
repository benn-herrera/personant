#### Per-day (history repository)

The day rollover writes exactly one day-grain commit to the history repository (an empty day still
yields one) plus the day's archive commits ([§3.8](../3.8-cold-storage/index.md)). The history repository grows by at most one day-commit a day (~365 a year) plus archival's bounded commits,
and integrity checks run once a day: the day's commit passes thread registry
integrity validation, and the rollover leaves derived files fresh. The turn repository is deleted and recreated
at each rollover.

**Checkpoint (clean service-run stop + project creation) → turn repository.** A Personant
**checkpoint** is a commit of the whole store working tree, hand-edits included, to the turn
repository, made at a clean service-run stop and after a project is created. Creating or closing a
thread lands in its turn's own commit. The history
repository is written only at day rollover, apart from crash recovery ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)).

