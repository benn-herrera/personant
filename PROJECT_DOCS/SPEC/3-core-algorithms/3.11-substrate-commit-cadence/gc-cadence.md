#### gc cadence

Packing is **count-triggered** by the daily DB's loose-object count, plus the
offline sleep cycle, which packs both repos (on the [§9](../../9-measurement-and-validation-regime/index.md) day-off idle window). A default-off mid-day
re-baseline knob ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) (5)) handles a long single service run in which no barrier fires.

