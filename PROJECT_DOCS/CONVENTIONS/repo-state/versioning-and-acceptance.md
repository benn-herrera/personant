### Versioning and acceptance

The memory service's semver line and the store's format revision are defined in SPEC §9.1. Their
current values live in `internal/version` and are never restated in prose; `personant version`
prints them. MINOR bumps of the semver line are developer fiat at the milestones.
The semver line's PATCH is bumped once per landed change that alters the memory service's
behaviour or the `MemoryOps` port — once per plan, in the plan's closing package, when the change
lands through a plan. Changes confined to tests, the harness, recipes or documents leave it
unchanged.

An acceptance claim states its coverage (simulated, modeled, parked) as SPEC §9.1's honesty clause
requires.

