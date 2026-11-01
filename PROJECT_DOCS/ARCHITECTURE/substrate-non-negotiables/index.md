## Substrate non-negotiables

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`config.toml` under `~/.config/personant/`; `providers.toml`: Open, U5) + markdown directives | hand-editable; secret-bearing files separated |
| Derived files | regenerable from canonical | drift cannot accumulate; `autogit.CheckDerivedFresh` fails any state-changing git op on stale |
| Git management | autonomic on `~/.personant/`, never on user's workspace | the service owns its house; the user owns theirs |

Contents:

- [Substrate evolution: if/when scale forces it](substrate-evolution-if-when-scale-forces-it.md)
- [Port abstraction policy](port-abstraction-policy.md)
