#### Version lines

The memory service this spec defines carries a semver
line; `personant version` prints the current value, `personant --version` prints it in one-line
form, and `system.bootstrap` records it once per process open ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)). CONVENTIONS, "Versioning and
acceptance", carries the bump contract. The four-month acceptance criterion converges the memory
service. Milestone:

- Memory service **v0.5.0 — realism convergence.** The acceptance criterion above, walked to the top
  span with every identified realism element accounted for.

**A further line, not semver: the store's on-disk format revision.** The **format revision** is the
store's on-disk layout number. `<store-dir>/version.toml` (ARCHITECTURE "Store directory layout"; its authoritative class, [§2.1](../../2-data-model/2.1-storage-layout/index.md)) carries it as one integer,
`format = N`, recording the layout the store was written in; this binary writes one revision and
may find another in a store. It advances independently of the semver line.

The **format check** runs at the process boundary, **before** startup recovery ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)): recovery
resets the store working tree to a recovery point and must never run against a layout this binary
cannot interpret. The open sequence is format check → startup recovery → service-run load.

- **Equal** → proceed.
- **Newer on disk → refuse.** `--allow-newer-home` overrides the refusal for one invocation; a
  waved-through open emits a stderr warning and a `system.home-format-override` event ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)).
- **Older on disk → migrate forward** when this binary can migrate from
  that format revision to the current one: the store is brought to the current format revision and
  its `version.toml` rewritten, and the open emits a `system.home-format-migrated` event ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md))
  naming both revisions. An older revision this binary cannot migrate from is refused. `personant init`
  runs the format check and writes `version.toml` at the current format revision only when the store has none; it never rewrites one, so it leaves an older store's revision for
  the next open to migrate.
- **No revision → treated as revision 1.** A store predating versioning is taken
  to be format revision 1 — always 1, even after the current revision advances — and then takes the
  older-on-disk path, so it is migrated and its revision written at the current one.

`personant version` is **exempt from the format check**: it reports the binary's identity, the
resolved store directory, and the store's format revision (a concrete number, *absent*, or
*unreadable*) and exits 0 either way. It never runs the format check or startup recovery and never
fails on a store it cannot read.

