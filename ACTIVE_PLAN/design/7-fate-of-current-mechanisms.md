## 7. Fate of current mechanisms

Removed: reachability-gated aging and the file-version recovery op. Retention does not wait on the
workspace repository, and Personant offers no recovery of content it no longer keeps (invariant 5,
[§5.3](5-store-format-and-retention/5.3-retention.md)). The table records what goes.

| Mechanism | Fate |
|---|---|
| `files.json`, `store.ThreadFiles` / `FileEntry` / `RecordWrite` / `RecordCommit` / `AgeOut` / `DropChain`, `FileChainRetention*` (`internal/store/threadfiles.go`) | removed; replaced by [§5](5-store-format-and-retention/index.md) |
| `dedup.Chain`, `Append`, `Reconstruct`, `encodeDemoted`, `AnchorCadence` (`internal/dedup/chain.go`) | removed (no reverse chains) |
| `applyDiff` and its parser `parseHunkSourceStart` (`internal/dedup/diff.go`) | moved to `internal/dedup/diff_test.go` as the round-trip oracle for `Diff`; production never applies a diff |
| `dedup.LiveWindow`, `WindowEntry`, `identifierText` (SHA-256) (`internal/dedup/livewindow.go`) | removed; replaced by `dedup.Plan` and `ContentID` |
| `dedup.LiveDiffWindow` (`internal/dedup/livewindow.go`) | moved to `internal/dedup/diff.go`; it is N |
| `makeDiff`, `lcs`, `lcsLineProductMax`, `DiffLiteralThreshold` (`internal/dedup/diff.go`; the constant moves there from `internal/dedup/chain.go`) | kept as the write-time forward diff and guard |
| `workset` `TrackedFile`, `appendTrackedFiles`, `writeTrackedFile`, `=== tracked files ===` (`internal/workset/workset.go`) | removed; expansions render inside excerpts |
| chain aging: `AgeFileChains` (port, adapter, the `internal/turn/engage.go:313–324` loop), `dedup.chain-aged`, `dedup.chain-age-refused` | removed; B1c retention emits `dedup.pruned` (thr, files, bytes) and `dedup.prune-skip` (thr, reason) |
| file-version recovery: `GetFileVersion`, `ErrFileVersionUnreachable`, `workspaceGitRoot`, `gitPathForShow`, `store.BlobReachable`, `store.ShowFileAtCommit` | removed. Content Personant no longer holds is found in the user's VCS by identifier (`git cat-file -p <id>`). `FindGitRoot` / `GetGitOriginURL` stay (project identity). |
| `RecordFileWrite` / `RecordFileCommit` / `applyFileEdits` | replaced by one `RecordFileEvents` call for the owner |
| `fs.commit-untracked` event | retired (commit events no longer touch the store) |
| sim `VerifyDedupConsistency` | removed. `personant verify` checks the store ([§12](12-acceptance-criteria.md) criterion 8), and the harness already runs `verify.Verify` through `VerifySpineIntegrity` in the same invariant lists (`internal/scenarios/invariants.go` `heavyDefaultInvariants`, `internal/scenarios/restart.go`), so a separate check would duplicate it |
| sim `buildWorkPreEvents` comment at `internal/scenarios/sim/workload.go:3331–3333` ("inflated bytes ride into THIS turn's assembled request") | false per [E1](1-established-facts/e1-where-file-content-gets-into-the-model-s-context.md). It is corrected to say that the filler becomes a tracked path and renders from the next turn's composition. |

---
