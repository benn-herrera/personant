### Automatic vs model-controlled

The service performs many operations the model cannot. Most notably **git**:

- The service owns the store directory's git repositories through `internal/autogit` (in-process
  go-git) at the cadence of the turn and history repositories (see "Crash recovery and the turn
  and history repositories"). No git hook: systemic checks `CheckDerivedFresh` (wraps
  `personant index check`) and `CheckSpineIntegrity` (wraps `personant verify`), parameterized
  only by paths, are passed per call as `GitCheckFlags` (`preFlags`/`postFlags`, 64 slots: a `uint64` bitmask of check functions; `internal/autogit/autogit.go:60`) and are
  applied to history-repository writes; turn-repository commits run none. Operation-tied
  verifications live in purpose-specific wrappers composing `autogit.Checkout`.
- Commits take their author and committer identity from the memory store's git config, falling back to `personant <personant@localhost>`; they are not cryptographically signed
  (`store.CommitSignature`).
- The service reads the user's *workspace* repository only to identify the project: its root
  (`store.FindGitRoot`) and origin URL (`store.GetGitOriginURL`).
- The memory service exposes no git tool for the store directory. The agent has opencode's shell,
  which can reach it, and nothing blocks it; crash recovery treats a broken hand-edit as legitimate
  (quarantine, never delete). That the service is the only writer of the store directory is the
  intent.

