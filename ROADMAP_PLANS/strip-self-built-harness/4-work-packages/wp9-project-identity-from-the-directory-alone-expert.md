### WP9: Project identity from the directory alone (expert)

- `internal/memops/memops_types.go`, `store/bootstrap.go`, `fileadapter/fileadapter.go`: the
  resolver row of section 1. `FileAdapter.ResolveActiveProject` creates a project with E11's
  steps (without `SetLastActiveProject`), naming it `filepath.Base` of the directory. The
  resolver reads nothing inside the directory, and takes no checkpoint: after WP1 it has no
  production caller, and A5's service takes the structural checkpoint after `StepCreated`. A
  directory not seen before is a new project, including one that was moved or renamed:
  Personant cannot tell a move from a copy, or from several copies, and people routinely copy old
  projects as templates for new ones. Nothing detects or repairs a move.
- User-repository reads: the "User project git" rows of section 1. Re-run E14's loop and edit
  every file it lists; delete `store/gitworkspace.go` and `gitworkspace_test.go`;
  `FindProjectByPath` loses its `historical_root_paths` match.
- Home format: in `internal/memops/homeformat.go` append `{From: 3, To: 4}` to
  `homeFormatMigrations` and extend its "are restamps" comment to 3→4; in
  `internal/version/version.go` set `CurrentHomeFormat = 4` with the doc line "Format 4 drops
  `remote_urls`, `historical_remote_urls` and `historical_root_paths` from `meta.json`; an older
  `meta.json` keeps them and they are ignored." Re-run E15's test grep; most tests use the
  constant and need no edit.
- `last-active`: as section 1. Re-run
  `git grep -l -E 'ReadLastActive|WriteLastActive|GetLastActiveProject|SetLastActiveProject|VerifyLastActiveValid|validLastActiveID' -- '*.go'`
  (today `memops/memops.go`, `fileadapter/fileadapter.go`,
  `scenarios/{harness,harness_setup,invariants,invariants_test}.go`,
  `store/{bootstrap,bootstrap_test,lastactive,lastactive_test,workingset_ops}.go`) and edit each,
  plus `store/paths.go`'s `LastActive` field and the seed `.gitignore` line in `store/init.go`.
  `ProjectMeta.LastActive` (`meta.json` `last_active`) stays.
- Tests:
  - `store/bootstrap_test.go` (`TestResolveActiveProject_*`) runs no git: path match; create; a
    directory holding a `.git/config` written by hand with `os.WriteFile` (`[remote "origin"]`,
    `url = git@github.com:foo/bar.git`) resolves to a new project rooted at that directory with
    no remote; empty directory → `prj_default`. `Explicit*` (including
    `DefaultProjectIDExplicit`), `RemoteMatch*`, `*DriftUpdate`, `LastActive*` and
    `FreshHomeNeedsFallback` go.
  - `store/project_ops_test.go`: `TestFindProjectByRemote`, `TestNormalizeRemoteURL` and
    `TestNormalizeRemoteURLSSHvsHTTPSCollide` go; `TestFindProjectByPath` drops its historical
    case.
  - A new `TestResolveActiveProject_*` in `internal/memops/fileadapter` asserts the
    `project.created` event on `StepCreated` (none there today:
    `grep -l ResolveActiveProject internal/memops/fileadapter/*_test.go` → nothing).
- Contract rows marked 9.
- Done-line: `just build`;
  `just test-run ./internal/store 'ResolveActiveProject|FindProject|ProjectMeta|HomeFormat|Init'`;
  `just test-run ./internal/memops 'HomeFormat'`;
  `just test-run ./internal/memops/fileadapter 'ResolveActiveProject|HomeFormat'`;
  `just test-run ./cmd`; `just test-run ./internal/scenarios`; the re-run greps → empty;
  `git grep -n -w -E 'last-active|StepNeedsConfirmation|StepNeedsFallback|StepRemoteMatch|ExplicitProject|remote_urls|historical_remote_urls|historical_root_paths|FindGitRoot|GetGitOriginURL' -- '*.go' PROJECT_DOCS`
  → only `version.go`'s format-4 doc line;
  `git grep -n -E 'survives file moves|External canonical identity' -- PROJECT_DOCS` → empty
  (today SPEC §4.5.2 lines 8 and 11, ARCHITECTURE "Project identity (dual-layered)" line 6);
  `git grep -l -E 'exec\.Command\("git"' -- '*.go'` → `internal/scenarios/harness_run.go` and
  `internal/store/init_test.go` only. Checkpoint; `just sim`. The commit message tells the
  operator that existing homes open as format 4 by restamp.

