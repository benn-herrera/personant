### User project git

| Site | Kind | What happens | Ev | WP |
|---|---|---|---|---|
| `store/gitworkspace.go` (`FindGitRoot`, `GetGitOriginURL`, `readGitConfig`, `firstNonEmptyLine`) and `gitworkspace_test.go` (with the `gitInit`/`gitRun` fixtures) | remove | Personant's only git is the home's | E14 | 9 |
| `store/project_ops.go` `FindProjectByRemote`, `NormalizeRemoteURL`, `splitSCPSSH` and their tests; `MemoryOps.FindProjectByRemote`, `FileAdapter.FindProjectByRemote` | remove | no remote identity | E14 | 9 |
| `store.FindProjectByPath` | reshape | matches `current_root_path` only | E14 | 9 |
| `memops.ProjectMeta` `RemoteURLs`, `HistoricalRemoteURLs`, `HistoricalRootPaths` | remove | `CurrentHomeFormat` 3 → 4, registry gains `{From: 3, To: 4}` (restamp only) | E14, E15 | 9 |
| `harness_run.go` `gitHead` | keep | records this repository's HEAD in the run manifest | E14 | — |

