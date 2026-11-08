## Storage non-negotiables

Standard pattern: THESIS "The building blocks", row "The memory store".

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`config.toml` under `~/.config/personant/`) + markdown directives | hand-editable; no secret-bearing file (SPEC §2.1) |
| Derived files | regenerable from authoritative files | drift cannot accumulate; `autogit.CheckDerivedFresh` fails a day rollover or archival batch that leaves a derived file stale |
| Git management | automatic on the store directory, never on user's workspace | the service owns the store's git; the user owns the workspace's |

Contents:

- [Port abstraction policy](port-abstraction-policy.md)
