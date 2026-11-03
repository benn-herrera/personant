### Inference and provider configuration

| Site | Kind | What happens | Ev | WP |
|---|---|---|---|---|
| `internal/ping/`, `internal/modellist/`, `cmd/ping.go`, `cmd/models.go` | remove | the `ping`/`models` verbs | E1 | 1 |
| `turn.State.Provider`; `provider memops.Provider` parameter of `turn.NewState`, `turn.LoadSession`; `scenarios.Harness.Provider` and the harness's provider literal and sentinel `providers.toml` write | remove | never read | E5 | 4 |
| `memops_model.go` "Provider pool" and "Config" sections, `memops/config_test.go` | remove | config is not memory | E6 | 5 |
| `MemoryOps.LoadProviders`, `LoadConfig`; `FileAdapter` methods | remove | | E6 | 5 |
| `store/config.go`, `providers.go`, `userconfig.go` with tests; `PersonantPaths.Providers`, `.Config`; `store.Init` template writes, `ensureUserIdentity`, `providers.toml` preservation, seed lines naming them | remove | `Init` writes no settings file | E6 | 5 |
| `test/config.toml`, `test/providers.toml`, `test/broken-config.toml`, `test/api_keys/` | remove | fixtures for the removed loaders | E6 | 5 |
| `model.NewHTTPClient`, `NewHTTPEmbedder` | reshape | take `baseURL, apiKey string` (plus model and dimensions for the embedder) | E7 | 5 |
| `testsupport.ReaperProvider()` | reshape | `ReaperURL() string`, `ReaperAPIKey = "dummy"`, `ReaperChatModel = "gemma-4-main"`; no `memops` import | E7 | 5 |
| `sim/sim_live_test.go` live endpoints | reshape | from `testsupport`; the file-based loaders go; fail-not-skip probe and non-fatal grader embedder stay | E7 | 5 |
| `curator.NewHTTPCurator` | keep | wraps any `model.Client`; A5 installs it over its opencode-generation adapter | — | — |

