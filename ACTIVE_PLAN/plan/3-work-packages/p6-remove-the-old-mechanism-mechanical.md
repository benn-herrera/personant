### P6 — Remove the old mechanism (mechanical)

**Precondition.** P4's evidence line (`dedup-expansions: literal=<n> diff=<n>`, `literal` above
zero), quoted in P4's report, and P5's done-line.

**Scope.** Every edit is listed below; nothing else changes. Each "delete" removes the symbol and
the tests that exercise only it.

| File | Edit |
|---|---|
| `internal/dedup/chain.go`, `internal/dedup/chain_test.go`, `internal/dedup/livewindow.go`, `internal/dedup/livewindow_test.go` | delete the files |
| `internal/dedup/diff.go` | move `applyDiff` and `parseHunkSourceStart` into `internal/dedup/diff_test.go` (the round-trip oracle for `Diff`) |
| `internal/store/threadfiles.go`, `internal/store/threadfiles_test.go` | delete the files |
| `internal/store/relpaths.go` | delete `ThreadFilesRel`; keep `threadFilesFileName` (B1c deletes the legacy file by it) |
| `internal/store/gitworkspace.go`, `internal/store/gitworkspace_test.go` | delete `BlobReachable`, `ShowFileAtCommit` and their tests (`TestBlobReachable_*`, `TestShowFileAtCommit_*`); keep `FindGitRoot`, `GetGitOriginURL`, `readGitConfig`, `firstNonEmptyLine` |
| `internal/memops/memops.go` | delete port methods `RecordFileWrite`, `RecordFileCommit`, `AgeFileChains`, `GetFileVersion` and their comments |
| `internal/memops/memops_model.go` | delete `ErrFileVersionUnreachable` |
| `internal/memops/fileadapter/fileadapter.go` | delete `RecordFileWrite`, `RecordFileCommit`, `workspaceGitRoot`, `gitPathForShow`, `AgeFileChains`, `GetFileVersion` |
| `internal/memops/fileadapter/fileadapter_m1_recovery_test.go` | delete the file |
| `internal/turn/engage.go` | delete the `AgeFileChains` loop (lines 313–324) and its comment |
| `internal/turn/turn.go` | line 1398 comment: drop `AgeFileChains` from the list |
| `internal/store/init.go` | `seedReadmeMD`, line 686: `` `files.json` `` → `` `files.jsonl` + `files/` (tracked-file store) `` |
| `internal/autogit/autogit.go` | line 564 comment: `files.json` → `files.jsonl` + `files/` |

**Facts** (each delete's full reference list; after the deletes, each must return nothing):

Abbreviations in this table only: `FA/` = `internal/memops/fileadapter/`; every other path is
given as `git grep -l` prints it.

| Command | Answer at `c23b036` |
|---|---|
| `git grep -l 'RecordFileWrite' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `FA/workset_compose_test.go` (P4), `internal/memops/memops.go`, `internal/turn/engage.go` (P3) |
| `git grep -l 'RecordFileCommit' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `internal/memops/memops.go`, `internal/turn/engage.go`, `internal/turn/turn_test.go` (P3) |
| `git grep -l 'AgeFileChains' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `internal/memops/memops.go`, `internal/turn/engage.go`, `internal/turn/turn.go` |
| `git grep -l 'GetFileVersion' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `internal/memops/memops.go`, `internal/memops/memops_model.go` |
| `git grep -l 'ErrFileVersionUnreachable' -- '*.go'` | the four above, plus `internal/store/gitworkspace.go` (comment in `ShowFileAtCommit`) |
| `git grep -l 'BlobReachable' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `internal/memops/memops.go`, `internal/store/gitworkspace.go`, `internal/store/gitworkspace_test.go` |
| `git grep -l 'ShowFileAtCommit' -- '*.go'` | the same five files |
| `git grep -l 'LoadThreadFiles' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `FA/workset_compose.go` (P4), `internal/memops/memops.go`, `internal/scenarios/invariants.go` (P3), `internal/scenarios/scenarios_test.go` (P3), `internal/store/threadfiles.go`, `internal/store/threadfiles_test.go`, `internal/turn/emptyresponse_test.go` (P3), `internal/turn/tagrecovery_test.go` (P3), `internal/turn/turn_test.go` (P3) |
| `git grep -l 'SaveThreadFiles' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `internal/memops/memops.go`, `internal/scenarios/invariants_test.go` (P3), `internal/store/threadfiles.go`, `internal/store/threadfiles_test.go` |
| `git grep -l 'ThreadFilesRel' -- '*.go'` | `FA/fileadapter.go`, `internal/store/relpaths.go` |
| `git grep -l 'FileChainRetention' -- '*.go'` | `FA/fileadapter_m1_recovery_test.go`, `internal/store/threadfiles.go`, `internal/store/threadfiles_test.go` |
| `git grep -l 'AnchorCadence' -- '*.go'` | `internal/dedup/chain.go`, `internal/dedup/chain_test.go`, `internal/dedup/livewindow.go`, `internal/store/threadfiles.go` |
| `git grep -l 'dedup.Chain' -- '*.go'` | `internal/store/threadfiles.go` |
| `git grep -l 'dedup.New(' -- '*.go'` | `internal/scenarios/invariants_test.go` (P3) |
| `git grep -l 'chain-age' -- '*.go'` | `FA/fileadapter.go`, `FA/fileadapter_m1_recovery_test.go`, `internal/memops/memops.go`, `internal/turn/engage.go` |
| `git grep -l 'identifierText' -- '*.go'` | `internal/dedup/livewindow.go`, `internal/dedup/livewindow_test.go` |

Files marked (P3) or (P4) no longer carry the name by the time P6 runs; any that still does is a
P3 or P4 defect to report, not to fix here.

**Contract.**
- SPEC `2.6.1`: delete `dedup.anchor-cadence`, `dedup.chain-retention-turns` and
  `dedup.chain-retention-days`.
- SPEC `3.8.3-recovery.md`: "Two recovery surfaces, different strength" becomes the design's text.
- ARCHITECTURE `recurring-patterns/autonomic-vs-llm-controlled.md`: the workspace-git bullet
  becomes the design's text.
- ARCHITECTURE `mechanisms/closure-and-retirement.md`: delete the `GetFileVersion` /
  `BlobReachable` / retention-constants sentence (lines 32–35).
- ARCHITECTURE `testing-as-the-lab-bench/event-log-format.md`: remove `chain-aged` and
  `chain-age-refused` from the `dedup` row.

**Done-line.**
- `just build`;
- the design's criterion 9 command returns nothing:
  `git grep -n -e LiveWindow -e "dedup.Chain" -e AgeFileChains -e GetFileVersion -e BlobReachable -e ShowFileAtCommit -e chain-age -e "tracked files ===" -- '*.go'`;
- `grep -rn -E "anchor-cadence|chain-retention|chain-age|BlobReachable|GetFileVersion|reverse-delta" PROJECT_DOCS`
  returns nothing;
- criterion 3, no git on the dedup, retention or compose path. Both commands return nothing:
  - `git grep -n -e '"os/exec"' -e '"personant/internal/autogit"' -e 'exec\.Command' -- internal/dedup internal/store/trackedfiles.go internal/memops/fileadapter/workset_compose.go`;
  - `git grep -W -n 'func (a \*FileAdapter) pruneTrackedFiles' -- internal/memops/fileadapter/fileadapter_barrier.go | grep -e 'autogit\.' -e 'exec\.'`
    (`-W` prints the whole function; B1c's adapter step must carry the name `pruneTrackedFiles`,
    design §8);
- `just test-run ./internal/dedup '.'`, `just test-run ./internal/store 'TestFileStore|TestPruneTrackedFiles|TestFindGitRoot|TestGetGitOriginURL'`;
- test files compile in the other touched packages. `just build` does not compile `_test.go`
  files, and `-run '^$'` compiles the test binary and runs nothing:
  `just test-run ./internal/memops '^$'`, `just test-run ./internal/memops/fileadapter '^$'`,
  `just test-run ./internal/turn '^$'`;
- `just check-links`.

**Checkpoint C2.** `just test`, then one commit covering P3–P6. The commit message quotes P4's
evidence line, `dedup-expansions: literal=<n> diff=<n>`, with its numbers.

---

