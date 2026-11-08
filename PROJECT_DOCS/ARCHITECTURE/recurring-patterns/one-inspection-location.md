### One inspection location

Personant's own state lives in two places, and it reads a third:

- The store directory — memory: the authoritative files and both git repositories.
- `~/.config/personant/` — Personant-only settings (`config.toml`, SPEC §8.2.2). Nothing here
  overlaps opencode's configuration.
- opencode's data home — never inside the store directory (see "Start order and resume"); no
  Personant state, read by the database cleanup and reconcile.

