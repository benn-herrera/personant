## 1. Outcomes this plan carries

- **Scope.** SPEC §3.9 covers the memory-composed layers only: the working set Personant composes
  in its own turn loop (SPEC §3.1). opencode's stored transcript is never rewritten and its
  tool-result rows are never edited. History rows are dropped from the request once memory has
  captured them (ARCHITECTURE "Injection handling", drop on re-injection), so repeated file copies
  there never reach the model. No package touches the plugin or opencode's storage.
- **Over-share files.** A file whose current content is at or above
  `dedup.literal-share-threshold` (default **0.25**) of the per-thread Layer B share is never
  expanded: it renders no literal and no diff anywhere in the composed context, and each position
  of it is its identifier line only. Work that needs such a file's content is out of scope.
- **No reachability-gated aging.** Retention of tracked-file content never waits on the workspace
  repository, and Personant offers no file-version recovery op. `AgeFileChains`,
  `GetFileVersion`, `store.BlobReachable` and `store.ShowFileAtCommit` are removed (P6). Recovery
  of content Personant no longer keeps goes through the user's own version control by identifier.
- **No dual-write.** The new store is built (P1–P2) and then switched to (P3–P4). The old mechanism
  is removed (P6) only after P4's evidence shows the new path doing the job. The new path is proven
  by its own acceptance criteria and the P7 measurement (design §13), not by agreement with the old
  path.
- **Gates.** Every package that touches Go code shares `just build`, so packages run one at a time
  in the shared working tree; no git worktree. Edit gate: `just build` plus
  `just test-run <pkg> <regexp>`. Checkpoint gate: `just test`, once, before the commit at C1, C2
  and C3 only (CONVENTIONS "Two gates"). `just test` reaches the sim package, which measured
  1508.8 s (justfile comment above `GOTESTTIMEOUT`).

