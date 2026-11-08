### One inspection location

Personant's own state lives in two places, and it reads a third:

- The store directory — memory: the authoritative files and both git repositories.
- `~/.config/personant/` — Personant-only settings (`config.toml`, SPEC §8.2.2). Nothing here
  overlaps opencode's configuration. `internal/store/paths.go` still places `config.toml` and
  `providers.toml` under the store directory; ROADMAP.md work moves `config.toml` and removes `providers.toml` (U5).
- opencode's data home — never inside the store directory (see "Start order and resume"); no
  Personant state, read by the database cleanup and reconcile.

