## Project identity

| Level | Form | Mutable? | Purpose |
|---|---|---|---|
| Internal handle | `prj_<n>` | no — assigned at creation | storage key (`projects/prj_<n>/`); thread registry reference |
| Remote identity | normalized git remote URL | yes (rare) | "is this the same project I've seen before?" — survives file moves and renames |
| Display name | user-chosen string | yes (`/project rename`) | UI rendering, command targeting |

**Project resolution** when an opencode session starts (SPEC §4.5.7). Its input is opencode's
session directory:

1. Git remote of the session directory (or an ancestor) → known project's `remote_urls` /
   `historical_remote_urls`.
2. Session directory path → known project's `current_root_path` / `historical_root_paths`.
3. `last-active` confirmation.

**Open:** U4 — the channel for the interactive steps (the last-active confirmation, the remote-adoption and collision questions, the file-move question) and the command for
an explicit selection.

When a project gains a remote URL after creation (local-only → published), the memory service
detects it and adopts it as the project's remote identity (SPEC §4.5.4; the adoption question waits
on U4).

---

