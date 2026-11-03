### WP5: Remove the provider pool and home configuration (expert)

- Scope: E6's lists (re-run each `git grep -l -w`). Edit every file listed.
- `internal/model`: `NewHTTPClient(baseURL, apiKey string) Client`;
  `NewHTTPEmbedder(baseURL, apiKey, model string, dimensions int) Embedder`. `HTTPClient` holds
  `baseURL`/`apiKey` instead of `memops.Provider`. Update `http_client_test.go`'s
  `newTestProvider` and `embed_integration_test.go`. Comments `embed.go:24` and
  `http_client.go:32` ("a config.toml choice") → "a caller's choice".
- `internal/testsupport`: `ReaperURL() string` (same `PERSONANT_REAPER_URL` override),
  `ReaperAPIKey = "dummy"`, `ReaperChatModel = "gemma-4-main"`; drop the `memops` import and fix
  the package comment. Re-run `git grep -l 'ReaperProvider()' -- '*.go'` (today
  `prompt/{elicitation,excerptgenre,history}_probe_test.go`,
  `turn/embed_recall_integration_test.go`, `model/embed_integration_test.go`) and edit each.
  `probeChatModel` uses `testsupport.ReaperChatModel`.
- `internal/scenarios/sim/sim_live_test.go`: endpoints from `testsupport`: chat model
  `ReaperChatModel`, embedding model `ReaperEmbeddingModel`, native dimensions. Delete
  `liveProvidersFile`, `liveConfigFile`, `liveConfigMissingMsg`, `rundataDir`,
  `loadLiveEndpoints`, `resolveEndpoint`. Keep fail-not-skip for an unreachable endpoint under a
  live toggle, the 1-day inference cap, the non-fatal grader embedder, and the rule that no key
  appears in a log line. The file comment and messages name `PERSONANT_REAPER_URL`.
- `internal/memops`: delete the "Provider pool" and "Config" sections of `memops_model.go`,
  `MemoryOps.LoadProviders`/`LoadConfig`, `config_test.go`; `memops_types.go:9` drops
  "ParseModelRef/ValidateConfig" from its list. `internal/memops/fileadapter`: delete the two
  methods; `fileadapter_test.go:39` drops "providers.toml template,".
- `internal/store`: delete `config.go`, `providers.go`, `userconfig.go` and their tests, and
  `PersonantPaths.Providers`/`.Config`. In `init.go` delete the template writes,
  `ensureUserIdentity`, the preservation of `providers.toml`, and the seed `README.md` and
  `.gitignore` lines naming `providers.toml`, `config.toml` or `api_keys`. Update `init_test.go`
  and `internal/autogit/turn_commit_bench_test.go`'s file list.
- `cmd/init.go`: `Long` text drops "providers.toml stub".
- `test/`: delete `config.toml`, `providers.toml`, `broken-config.toml`, `api_keys/`.
- justfile:
  - `GOPKGS` comment (lines 10–17) becomes: "GOPKGS is the set of Go package roots. All packages
    live under cmd/ and internal/; test/ holds no Go code of its own, but the gitignored sim run
    directories under test/rundata/ hold Go files that make a bare `./...` fail. Scoping to the
    real roots covers 100% of the code and never descends into test/. Use GOPKGS, not ./..., in
    every vet/test/cover recipe."
  - `GOSRCDIRS` comment: "so pointing either at test/ would descend into test/api_keys (mode 0700,
    benn-owned) and fail the walk for a non-owner." → "so pointing either at test/ would descend
    into the sim run directories under test/rundata/, which hold Go files of their own."
  - Delete the `RACEPKGS` sentence "Never point this at test/ — test/api_keys is mode 0700 and
    benn-owned." Fix the `sim` comment (`test.{providers,config}.toml`) and the `test-changed`
    comment (`test/config.toml`).
- Contract rows marked 5.
- Done-line: `just build`; `just test-run ./internal/store 'Init'`;
  `just test-run ./internal/model`; `just test-run ./cmd`;
  `just test-run ./internal/scenarios/sim 'Live'` (runs only `TestLiveCosineOpsPerQuery`: it
  proves the live file compiles, not the live path);
  `git grep -n -E 'providers\.toml|config\.toml|api_keys|apiKeyFile|LoadProviders|LoadConfig|ParseModelRef|ReaperProvider' -- '*.go' justfile`
  → empty;
  `git grep -n -E 'providers\.toml|api_keys|test\.(providers|config)' -- PROJECT_DOCS README.md ROADMAP_PLANS`
  → only this plan. Checkpoint; `just sim`; the dispatching session records the run directory
  (`command ls -d test/rundata/sim-workload-seed*-dur168h0m0s.* | tail -1`) as WP6's baseline.
  Operator step, needs `reaper.local`: `just LIVE_EMBEDDING=true DURATION=1d sim` and
  `just LIVE_INFERENCE=true DURATION=1d sim`.

