### Versioning & acceptance (read before judging "done")

The version lines and the home's on-disk
format are defined in SPEC §9.1. Their current values live in `internal/version` and are never
restated in prose; `personant version` prints them. The substrate line is semver; its MINOR bumps
are developer fiat at the milestone gates.

**Open:** U6 — whether `version.FrontEnd`, its bump contract and `just test-fe` are retired or
repurposed for the plugin.

Never report acceptance from a green run alone: an acceptance claim states its coverage (simulated,
modeled, parked) as SPEC §9.1's honesty clause requires.

