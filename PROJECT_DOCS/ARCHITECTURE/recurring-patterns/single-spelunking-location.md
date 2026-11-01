### Single spelunking location

Personant's own state lives in three places:

- `~/.personant/` — memory: the canonical files and both git DBs. Forensic inspection is
  `cd ~/.personant && grep -r ...`.
- `~/.config/personant/` — Personant-only settings (`config.toml`, SPEC §8.2.2). Nothing here
  overlaps opencode's configuration. `internal/store/paths.go` still places `config.toml` and
  `providers.toml` under the home; moving them is ROADMAP.md work (install and config).
- opencode's data home — never inside `~/.personant/` (see "Start order and resume"); no Personant
  state, read by the database cleanup and reconcile.

**Open:** U5 — whether `providers.toml` and `api_keys/` exist and where they are placed (SPEC
§2.1, §8.2.1).

