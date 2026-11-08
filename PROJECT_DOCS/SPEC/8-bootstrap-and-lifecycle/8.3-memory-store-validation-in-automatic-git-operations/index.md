### 8.3 Memory store validation in automatic git operations

The memory service owns the store directory's git repositories: it initializes them on first run
and commits authoritative changes itself, with no user ack and no model. Every day-commit and
archival write to `<store-dir>/.git/` leaves the memory store structurally sound (what
`personant verify` checks), and every day rollover and archival batch leaves its derived files fresh (what `personant index check` checks).
Turn-repository commits are checked by neither.

Contents:

- [§8.3 No git pre-commit hook is installed](no-git-pre-commit-hook-is-installed.md)
