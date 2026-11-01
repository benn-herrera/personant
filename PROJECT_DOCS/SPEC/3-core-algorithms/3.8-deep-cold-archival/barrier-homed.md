#### Barrier-homed

The archival *drain* fires **only at the day barrier** ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) B1), against the
**primary** git DB, so primary stays barrier-exclusive ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) INV-5). Standalone archival is
crash-recoverable via the same roll-forward completion ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) (6)).

