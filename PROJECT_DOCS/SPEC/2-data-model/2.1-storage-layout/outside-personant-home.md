#### Outside `$PERSONANT_HOME`

- Personant-only settings live under `~/.config/personant/` (`config.toml`, [§8.2.2](../../8-bootstrap-and-lifecycle/8.2-configuration-sources-and-precedence/8.2.2-personant-settings-config-personant-config-toml.md)).
- opencode's data home is never under `$PERSONANT_HOME`: crash recovery quarantines untracked files
  ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)), which would include opencode's database.

**Open:** U5 — whether `providers.toml` (a provider pool) and `api_keys/` exist, and where they
are placed ([§8.2.1](../../8-bootstrap-and-lifecycle/8.2-configuration-sources-and-precedence/8.2.1-reaching-personant-s-own-models.md)).

