#### Version lines

The substrate (the runtime and memory model this spec defines) carries a semver
line; `personant version` prints the current value, `personant --version` prints it in one-line
form, and `system.bootstrap` records it once per process open ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)). CONVENTIONS, "Versioning &
acceptance", carries the bump contract. The four-month gate converges the substrate. Milestone
gate:

- Substrate **v0.5.0 — realism convergence.** The gate above, walked to the top rung with every
  identified realism element accounted for.

**A further line, not semver: the home's on-disk `format`.** `<home>/version.toml` ([§2.1](../../2-data-model/2.1-storage-layout/index.md)) carries one
integer, `format = N`, recording the layout revision the home was written in. It advances
independently of the semver line.

**Gating** runs at the process boundary, **before** startup recovery ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)): recovery resets the
worktree to a recovery point and must never run against a layout this binary cannot interpret. The
open sequence is format gate → startup recovery → service-run load.

- **Equal** → proceed.
- **Newer on disk → refuse.** `--allow-newer-home` overrides the refusal for one invocation; a
  waved-through open emits a stderr warning and a `system.home-format-override` event ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)).
- **Older on disk → migrate forward** when this binary registers a chain of migration steps from
  that format to the current one: the home is brought to the current format and restamped, and
  the open emits a `system.home-format-migrated` event ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)) naming both formats. An older
  format with no registered chain is refused. `personant init` gates but never stamps, so it
  leaves an older home's stamp for the next open to migrate.
- **No stamp → adopt forward.** A home predating versioning is taken to be format 1 — always 1,
  even after the current format advances — and then takes the older-on-disk path, so it is
  migrated and stamped at the current format.

`personant version` is **ungated**: it reports the binary's identity, the resolved home path, and
the home's stamp (a concrete revision, *absent*, or *unreadable*) and exits 0 either way. It never
runs the gate or recovery and never fails on a home it cannot read.

