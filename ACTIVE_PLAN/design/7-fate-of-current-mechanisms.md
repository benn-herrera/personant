## 7. Fate of current mechanisms

Removed: reachability-gated aging and the file-version recovery op. Retention does not wait on the
workspace repository, and Personant offers no recovery of
content it no longer keeps (invariant 5, [§5.3](5-store-format-and-retention/5.3-retention-b1c-8.md)). The table records what goes.

| Mechanism | Fate |
|---|---|
| `files.json`, `store.ThreadFiles` / `FileEntry` / `RecordWrite` / `RecordCommit` / `AgeOut` / `DropChain`, `FileChainRetention*` | removed; replaced by [§5](5-store-format-and-retention/index.md) |
| `dedup.Chain`, `Append`, `Reconstruct`, `encodeDemoted`, `AnchorCadence` | removed (no reverse chains) |
| `applyDiff` and its parser `parseHunkSourceStart` | moved to `diff_test.go` as the round-trip oracle for `Diff`; production never applies a diff |
| `dedup.LiveWindow`, `WindowEntry`, `identifierText` (SHA-256) | removed; replaced by `dedup.Plan` and `ContentID` |
| `diff.go` `makeDiff`, `lcs`, `lcsLineProductMax`, `DiffLiteralThreshold` | kept as the write-time forward diff and guard |
| `workset` `TrackedFile`, `appendTrackedFiles`, `writeTrackedFile`, `=== tracked files ===` | removed; expansions render inside excerpts |
| chain aging: `AgeFileChains` (port, adapter, `engage.go:313–324` loop), `dedup.chain-aged`, `dedup.chain-age-refused` | removed; B1c retention emits `dedup.pruned` (thr, files, bytes) and `dedup.prune-skip` (thr, reason) |
| file-version recovery: `GetFileVersion`, `ErrFileVersionUnreachable`, `workspaceGitRoot`, `gitPathForShow`, `store.BlobReachable`, `store.ShowFileAtCommit` | removed. Recovery of content Personant no longer holds goes through the user's VCS by identifier (`git cat-file -p <id>`). `FindGitRoot` / `GetGitOriginURL` stay (project identity). |
| `RecordFileWrite` / `RecordFileCommit` / `applyFileEdits` | replaced by one `RecordFileEvents` call for the owner |
| `fs.commit-untracked` event | retired (commit events no longer touch the store) |
| sim `VerifyDedupConsistency` | removed. `personant verify` checks the store (criterion 8: index decodes sorted and unique; every named blob and `diff:true` file exists; each blob hashes to its name), and the harness already runs `verify.Verify` through `VerifySpineIntegrity` in the same invariant lists (`internal/scenarios/invariants.go` `heavyDefaultInvariants`, `internal/scenarios/restart.go`), so a separate check would duplicate it |
| sim `buildWorkPreEvents` comment at `workload.go:3332` ("inflated bytes ride into THIS turn's assembled request") | false per E1. The comment says instead that the filler becomes a tracked path and renders from the next turn's composition. |

---

