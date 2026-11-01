### 8.3 Substrate validation in autonomic git operations

The runtime owns the git tree of `$PERSONANT_HOME`: it initializes it on first run and commits
canonical mutations with structured messages, in-process, with no user ack and no LLM involvement.

Before and after each state-changing git operation on `$PERSONANT_HOME/.git/`, the runtime runs
systemic validation inside its own logic: derived files are fresh (what `personant index check`
verifies — `symbols.jsonl` and per-project `digest.json` match their canonical sources) and the
substrate is structurally sound (what `personant verify` performs).

Operation-specific verifications (e.g. that a particular archive entry resolves after a recovery
checkout) live inside the operation that needs them.

Contents:

- [§8.3 No git pre-commit hook is installed](no-git-pre-commit-hook-is-installed.md)
