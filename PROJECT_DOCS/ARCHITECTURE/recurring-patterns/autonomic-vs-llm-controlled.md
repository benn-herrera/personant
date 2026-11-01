### Autonomic vs LLM-controlled

The service performs many operations the LLM cannot. Most notably **git**:

- The service owns `~/.personant/`'s git through `internal/autogit` (in-process go-git) at the
  dual-repo cadence (see "Crash stability and the dual-repo substrate"). No git hook: systemic
  checks `CheckDerivedFresh` (wraps `personant index check`) and `CheckSpineIntegrity` (wraps
  `personant verify`), parameterized only by paths, are passed per call as `GitCheckFlags`
  (`preFlags`/`postFlags`, 64 slots) and gate primary writes; daily commits are ungated.
  Operation-tied verifications live in purpose-specific wrappers composing `autogit.Checkout`.
- Commits are signed from the home repository's git config, falling back to a fixed identity
  (`store.CommitSignature`).
- The service issues read-only git queries against the user's *workspace* only for the §3.9
  reachability check (is a committed blob still reachable, `BlobReachable`).
- Memory exposes no git tool for the home tree. The agent has opencode's shell, which can reach
  `~/.personant/`, and nothing blocks it; recovery treats a broken hand-edit as legitimate
  (quarantine, never delete). That the service is the only writer of the home tree is the intent.

