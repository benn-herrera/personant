## 6. Settled by the record

- **The redactor goes** (WP1). SPEC §8.2.1 sets its key set to "the keys Personant has already
  resolved", and ROADMAP U5 leaves Personant no provider configuration, so no keys. Redacting
  opencode's keys would be a new requirement; nothing written asks for it.
- **The live sim uses `testsupport`'s `reaper` endpoint** (WP5). README "Sim Knobs" already
  requires `reaper.local` for both toggles, and `testsupport.ReaperProvider` is the incumbent, so
  the parallel `test/rundata` endpoint files go. CONVENTIONS "Probe work" keeps the live modes.
- **One home-format bump, in WP9 only.** SPEC §9.1 "Version lines" and invariant 3: only the
  `meta.json` field removal changes a stored format. The home's `config.toml` and
  `providers.toml` stay inert where they exist, `last-active` was operational and gitignored,
  and event-log fields are not layout. The step is a restamp because the decoder ignores the
  dropped keys, as the 1→2 and 2→3 steps are (E15).
- **No stale-file warning** (WP5, WP9). CONVENTIONS "Pre-production: no transition
  scaffolding". The operator deletes `<home>/config.toml`, `<home>/providers.toml`,
  `<home>/api_keys/` and `<home>/last-active` by hand; the WP5 and WP9 commit messages say so.

