#### Per-day (primary)

The **day barrier** ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) B0–B6, fired on new-day detection) writes exactly
**one** day-grain commit to primary (`Personant-Day: <N>`; an empty day still yields one) plus the
day's archival anchors ([§3.8](../3.8-deep-cold-archival/index.md), B1). Primary is bounded to ~365 commits/year, and integrity gating
runs **once a day**: spine-integrity validation of the staged day-commit (B2) and a
derived-freshness assertion after the post-rebuild (B6). The daily DB is nuked and reborn at each
barrier (B4/B5).

**Checkpoint (clean service-run stop + mid-run structural) → daily.** Every checkpoint targets **daily**
with a full-worktree staging sweep that absorbs hand-edits and any content not yet scoped-committed
(normally a no-op). Primary is **barrier-exclusive** ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) INV-5).

