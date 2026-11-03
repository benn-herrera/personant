## Evidence

Commands run at `1d1c951` (branch `plugin-route`). Every package re-runs the ones it cites at
dispatch, because earlier packages change the answers. In `git grep -E`, `\b` matches nothing on
this platform: use `-w`. Use package patterns, not `./...`: gitignored sim run directories under
`test/rundata/` hold Go files that break `./...` ("found packages wiki_adjacency_matrix … in
…/opencode/workspace").

- **E1 removal set and importers.**
  `for p in chat term term/screentest shell tools tools/web ping modellist; do echo "$p: $(git grep -l "\"personant/internal/$p\"" -- '*.go' | grep -v "^internal/$p/" | tr '\n' ' ')"; done`
  → `chat`: `cmd/chat.go cmd/main.go`; `term`: `internal/chat/*` only; `term/screentest`:
  `internal/chat/render_test.go internal/term/editor_test.go`; `shell`: `internal/chat/*` only;
  `tools`: `internal/chat/*`, `internal/turn/toolloop.go`, `toolloop_test.go`, `turn.go`,
  `webtools_test.go`; `tools/web`: `internal/chat/*`, `internal/turn/webtools_test.go`; `ping`:
  `cmd/ping.go`; `modellist`: `cmd/models.go`. `git ls-files internal/chat internal/term internal/shell internal/ping internal/modellist | wc -l`
  → 60.
- **E2 what `./cmd` links.**
  `git grep -h -o -E '"personant/internal/[a-z/]+"' -- 'cmd/*.go' ':!cmd/chat.go' ':!cmd/ping.go' ':!cmd/models.go' ':!*_test.go' | sort -u`
  → `chat eventlog log memops memops/fileadapter store version` (`chat` goes in WP1). Their
  closure imports none of the model client:
  `git grep -l -E '"personant/internal/(model|turn|curator|recall/measure)"' -- internal/eventlog internal/log internal/memops internal/store internal/version internal/index internal/verify internal/autogit internal/recovery internal/crashpoint internal/dedup internal/workset internal/prompt internal/recall/scoring internal/clock ':!*_test.go'`
  → empty.
- **E3 dependencies.** `git grep -l -E '"golang.org/x/(sys|net)' -- '*.go'` →
  `internal/term/{device_unix,gate_test,ioctl_bsd,ioctl_linux}.go`,
  `internal/tools/web/{extract,render}.go`. `git grep -l -E 'go-readability|html-to-markdown' -- '*.go'`
  → `internal/tools/web/{extract,render}.go`. `go mod graph | grep ' golang.org/x/sys@'` and
  `… ' golang.org/x/net@'` → requirers include `go-git/v5@v5.19.3` and `go-billy/v5@v5.9.2`, so
  both stay as `// indirect`; `golang.org/x/text` likewise. `grep -c langchaingo go.mod` → `0`.
- **E4 no tidy-only recipe.** `grep -n tidy justfile` → only inside `update-dependencies`, which
  also runs `go get -u ./...`, and `add-dependency`, which pins one module. WP1 adds
  `tidy-dependencies`.
- **E5 provider field is dead.** `git grep -n -w -E 'Provider' -- 'internal/turn/*.go' ':!*_test.go'`
  → `turn.go:93` (field), `:479` and `:515` (parameters), `:483` (assignment): no read.
  `git grep -l -E 'NewState\(|LoadSession\(' -- '*.go' | grep -v '^internal/chat/'` → 33 files
  (`internal/scenarios/harness_run.go`, `harness_setup.go`, `internal/turn/turn.go` and 30
  `internal/turn/*_test.go`), 149 matching lines. The harness carries it too:
  `git grep -n -w 'memops\.Provider' -- internal/scenarios ':!internal/scenarios/sim'` →
  `harness.go:731` (`Provider memops.Provider`), `harness_setup.go:59` (literal); its uses are
  `harness_setup.go:111` (`Provider: provider`) and `harness_run.go:705` (`h.Provider` passed to
  `LoadSession`).
- **E6 config types and readers.** Each `git grep -l -w -E '<name>' -- '*.go'`, outside the
  packages WP1 and WP3 delete:
  `memops\.Provider` → E5's turn and harness files plus `fileadapter/fileadapter.go`,
  `model/{embed,http_client,http_client_test}.go`, `sim/sim_live_test.go`,
  `store/providers.go`, `testsupport/livetests.go`; `Providers` →
  `memops/{config_test,memops_model}.go`, `fileadapter/fileadapter.go`,
  `scenarios/harness_setup.go`, `sim/sim_live_test.go`,
  `store/{init,init_test,paths,providers,providers_test}.go` (plus two tool-call comments in
  `model/http_stream.go`, gone in WP3); `ProviderFault` → `memops/{memops,memops_model}.go`,
  `fileadapter/fileadapter.go`, `store/{providers,providers_test}.go`; `memops\.Config` →
  `memops/config_test.go`, `fileadapter/fileadapter.go`, `store/{config,config_test}.go`;
  `ParseModelRef` → `memops/{config_test,memops_model,memops_types}.go`, `sim/sim_live_test.go`.
  `git grep -l 'BurntSushi/toml' -- '*.go'` → `store/{config,homeformat,providers}.go`, so
  `toml` stays for `version.toml`. `command ls test/` → `api_keys`, `broken-config.toml`,
  `config.toml`, `providers.toml`, `rundata`.
- **E7 live sim endpoint.** `sim_live_test.go` loads `test/rundata/test.{providers,config}.toml`
  through `store.LoadProviders`/`LoadConfig`. README "Sim Knobs": "Both live toggles … need a
  reachable `reaper.local` endpoint". `testsupport` holds `ReaperProvider()` (URL from
  `PERSONANT_REAPER_URL`, key `"dummy"`) and `ReaperEmbeddingModel`.
  `git grep -n '"gemma-4-main"' -- '*.go'` → `prompt/elicitation_probe_test.go`
  (`probeChatModel`), `model/toolstream_live_test.go` (`liveToolChatModel`). The HTTP client
  reads only `BaseURL` and `APIKey`: `grep -n 'provider\.' internal/model/*.go | grep -v _test`.
- **E8 flow-control seams.** `git grep -n -E 'ClosureResolver|RecallResolver|ClosureAckMode|RecallAckMode' -- '*.go' | grep -v '^internal/chat/' | grep -v _test.go`
  → production sites in `internal/turn/{closure,commands,recall,turn}.go`. `installAckPolicy`
  (`harness_setup.go:222–226`, called at `harness_setup.go:177` and `harness_run.go:720`) sets
  `Curator`, `ClosureAckMode = AckModeAlways` and `RecallAckMode = RecallAckAlways`. Every sim
  step scripts `ClosureAck{Outcome: ClosureResolved}` and decline-all `RecallAck`
  (`git grep -n -E 'RecallAck|ClosureAck' -- internal/scenarios/sim/*.go`), so the mock gate
  today retires every decayed thread as `resolved` and never fetches on recall.
- **E9 runtime mirror.** `workload.go` `advanceRuntimeMirror` models Layer B from engagement and
  decay only; the BD-4 cross-check compares it with the runtime's post-turn `ActiveThreads`.
  `StepFeedback` carries `RecallMatchFireIDs`, `EmbedMatchFireIDs` and `IntraMatchFireIDs`,
  scraped from the event log. A `just sim` run writes
  `test/rundata/sim-workload-seed<seed>-dur<duration>.<stamp>/<same name>.metrics.json`; the
  default `1w` run is `dur168h0m0s`, and each run gets a new stamped directory.
- **E10 directive parameters.** `git grep -n -E 'DirectiveParam\(|ReadParameter\(' -- '*.go' | grep -v _test.go`
  → callers only in `internal/chat/chat.go` (the two ack modes).
- **E11 project bootstrap.** `git grep -n -w -E 'ExplicitProject|StepNeedsConfirmation|StepNeedsFallback|ResolveActiveProject' -- '*.go' | grep -v '^internal/chat/' | cut -d: -f1 | sort -u`
  → `cmd/chat.go`, `memops/{memops,memops_types}.go`, `fileadapter/fileadapter.go`,
  `store/bootstrap{,_test}.go`. The only reader of `last-active` outside tests is
  `store.ResolveActiveProject` step 3; `GetLastActiveProject`/`SetLastActiveProject` are called
  only from `internal/chat/chat.go`. The REPL's project creation
  (`internal/chat/chat.go` `createNewProject`, lines 1388–1418) is: `ops.NextProjectID`; build
  `memops.ProjectMeta{ID, Name, CurrentRootPath, Created: now, LastActive: now}` with
  `now := clock.Timeline().UTC().Format(time.RFC3339)`; `ops.CreateProject`;
  `ops.Log(ctx, memops.LogCategoryProject, "created", "id="+id+" name="+name)`; a structural
  checkpoint.
- **E12 version line.** `git grep -n -i -E 'FrontEnd|frontend=|front[ -]end' -- internal/version internal/eventlog cmd`
  → `internal/version/version.go` (lines 8–9, 34–35, 123, 138, 160), `version_test.go`,
  `internal/eventlog/bootstrap.go` (18, 38, 40), `bootstrap_test.go:39`, `cmd/version.go:16`
  (`Long` text), plus `cmd/chat.go`.
- **E13 contract Opens.** `git grep -n -E 'U4|U5|U6' -- PROJECT_DOCS | wc -l` → 38.
  `git grep -l 'U8' -- PROJECT_DOCS` → SPEC §4.3.3 index, §4.5.8, ARCHITECTURE "Invariant for the
  production inversion", "Start order and resume", "Event log format".
- **E14 user-repository reads.**
  `for s in FindGitRoot GetGitOriginURL NormalizeRemoteURL FindProjectByRemote splitSCPSSH RemoteURLs HistoricalRemoteURLs HistoricalRootPaths; do echo "$s: $(git grep -l -w $s -- '*.go' | grep -v '^internal/chat/' | tr '\n' ' ')"; done`
  → `FindGitRoot`: `store/{bootstrap,bootstrap_test,gitworkspace,gitworkspace_test}.go`;
  `GetGitOriginURL`: `store/{bootstrap,gitworkspace,gitworkspace_test}.go`;
  `NormalizeRemoteURL`: `store/{gitworkspace,project_ops,project_ops_test}.go`;
  `FindProjectByRemote`: `memops/memops.go`, `fileadapter/fileadapter.go`,
  `store/{bootstrap,project_ops,project_ops_test}.go`; `splitSCPSSH`: `store/project_ops.go`;
  `RemoteURLs`, `HistoricalRemoteURLs`: `memops/memops_model.go`,
  `store/{bootstrap_test,project_ops,project_ops_test}.go`; `HistoricalRootPaths`: the same plus
  `memops/memops.go`, `store/bootstrap.go`. `gitInit`/`gitRun` are defined in
  `store/gitworkspace_test.go` and used only by `store/bootstrap_test.go`
  (`git grep -l -w -E 'gitInit|gitRun' -- '*.go'`). No scenario or sim fixture builds a user
  repository. `git grep -l -E 'exec\.Command\("git"' -- '*.go' | grep -v '^internal/chat/'` →
  `scenarios/harness_run.go` (`gitHead(repoRoot(h.T))`, this repository's HEAD for the run
  manifest), `store/gitworkspace.go`, `store/gitworkspace_test.go`, `store/init_test.go` (on a
  home). SPEC §3.9.1 already reads "Storage, retention and composition never consult the
  workspace repository".
- **E15 home-format registry.** `internal/memops/homeformat.go`:
  `var homeFormatMigrations = []HomeFormatMigration{{From: 1, To: 2}, {From: 2, To: 3}}`, "The
  1→2 and 2→3 steps are restamps"; `internal/version/version.go` `CurrentHomeFormat = 3`. Tests
  naming the constant or the registry: `git grep -l -E 'CurrentHomeFormat|To: 3' -- '*_test.go'`
  → `cmd/version_test.go`, `eventlog/bootstrap_test.go`,
  `fileadapter/fileadapter_homeformat_test.go`, `memops/homeformat_test.go`,
  `store/init_test.go`, `version/version_test.go`, `chat/commands_test.go`.
- **E16 substrate bumps.**
  `git log --oneline -L '/^const Substrate/,+1:internal/version/version.go' | grep -E '^[0-9a-f]{7} '`
  → after `0.2.0` (`9d9bf23`), one PATCH bump per runtime- or port-changing feature commit
  through `0.2.7` (`52f996b`). The plan-driven sprints since (excerpt folios, content dedup)
  landed with no bump.

