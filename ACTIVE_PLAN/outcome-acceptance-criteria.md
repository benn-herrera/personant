## Outcome (acceptance criteria)

1. `git ls-files internal/chat internal/term internal/shell internal/tools internal/ping internal/modellist`
   is empty.
2. `go.mod` requires neither `codeberg.org/readeck/go-readability/v2` nor
   `github.com/JohannesKaufmann/html-to-markdown/v2`; `golang.org/x/net` and `golang.org/x/sys` are
   `// indirect`.
3. Bare `personant` prints help and exits 0. The verbs are `init`, `index`, `verify`, `archive`
   and `version`. Nothing `./cmd` links imports `internal/model`, `internal/turn` or
   `internal/curator` (E2).
4. No Personant code reads or writes `providers.toml`, the home's `config.toml`, `api_keys/` or
   `last-active`, and `personant init` writes none of them. `memops` holds no provider or config
   type. `MemoryOps` has no `LoadProviders`, `LoadConfig`, `DirectiveParam`,
   `GetLastActiveProject`, `SetLastActiveProject` or `FindProjectByRemote`.
5. `turn.State` has no resolver, ack-mode or presentation-hook field. With only a curator
   installed, every decay-eligible thread that gets a non-empty draft is retired as `resolved`.
   With no resolver installed, every top-3 recall candidate at or above its tier's auto threshold
   is fetched into Layer B.
6. Project resolution always resolves from the directory alone: the project whose
   `current_root_path` is that directory, else a new project rooted there. It falls back to
   `prj_default` only when no directory is given. No Personant code runs `git` against, or opens
   with go-git, any repository outside the home.
7. `internal/version` has no `FrontEnd`, and `system.bootstrap` carries no `frontend=`. The
   justfile has no `test-fe`, `fe-scope-check`, `test-be`, `FE_SCOPE_PREFIXES` or `FE_TESTPKGS`.
8. The mock gate `just sim` (default `1w`) passes after every package that names it. The live
   toggles take their endpoint from `internal/testsupport`.
9. No `Open: U4`, `Open: U5`, `Open: U6` or `Open: U8` remains in `PROJECT_DOCS/`. No contract
   document, README or ROADMAP line names a removed package, verb, recipe, dependency, setting,
   file or `meta.json` field except as history.
10. `version.CurrentHomeFormat` is 4, reached from 3 by a restamp-only step; `version.Substrate`
    is `0.2.8`.

