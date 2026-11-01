## Project identity (dual-layered)

| Layer | Form | Mutable? | Purpose |
|---|---|---|---|
| Internal handle | `prj_<n>` | no — assigned at creation | storage key (`projects/prj_<n>/`); spine reference |
| External canonical identity | normalized git remote URL | yes (rare) | "is this the same project I've seen before?" — survives file moves and renames |
| Display name | user-chosen string | yes (`/project rename`) | UI rendering, command targeting |

**Bootstrap waterfall** when an opencode session starts (SPEC §4.5.7). Its input is opencode's
session directory:

1. Git remote of the session directory (or an ancestor) → known project's `remote_urls` /
   `historical_remote_urls`.
2. Session directory path → known project's `current_root_path` / `historical_root_paths`.
3. `last-active` confirmation.
4. Final fallback: create / switch / no project (`prj_default`).

**Open:** U4 — the channel for the interactive steps (the last-active confirmation, the final
fallback, the remote-adoption and collision prompts, the file-move prompt) and the command for an
explicit selection.

When a project gains a remote URL after creation (local-only → published), the service detects it
and adopts it as canonical identity (SPEC §4.5.4; the adoption prompt waits on U4).

---

