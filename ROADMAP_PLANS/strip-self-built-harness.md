# Strip the self-built harness

Parked plan. It carries out ROADMAP.md B1 ("Retire the self-built harness") and the config move in
A12. When it is activated it becomes the active plan and goes through the full dispatchability
pass (CONVENTIONS "Plan dispatchability"); the work packages below are at partly-planned depth.

## What it does

opencode is the harness (SPEC §1). Personant's self-built chat REPL, terminal, shell escape, tool
surface and provider handling are outside the contract: SPEC §4.4, §4.6 and §6 already read
"Unused", and ARCHITECTURE "Superseded code" names the packages. This plan removes that code with
its tests and dependencies, reshapes Personant's configuration into a Personant-only file under
`~/.config/personant/` (SPEC §8.2.2), and resolves the version line that served the REPL.

What it does not do: build the memory service or plugin (A3, A5), install opencode-side settings
or the memory-native commands (A12, U4), or decide U5 and U6. It states where each package depends
on U5 or U6 and gates those packages on the decision.

## Activation preconditions

- **The excerpt-folio plan has landed.** It edits `cmd/main.go` (`openHome`), `internal/memops`,
  `internal/store` and `internal/turn` (`lru.go`), all of which this plan also touches.
- **U5 is decided before WP5 starts, and U6 before WP7** ("Decisions needed"). WP1 to WP4 hold
  under every option.
- **The operator can run the live sim rungs** (`reaper.local` reachable) for WP6's done-line, or
  accepts that line as an operator step.

## Outcome (acceptance criteria)

1. `go list ./cmd/... ./internal/...` lists none of `internal/chat`, `internal/term`,
   `internal/term/screentest`, `internal/shell`, `internal/tools`, `internal/tools/web`; under U5
   option (a), none of `internal/ping`, `internal/modellist` either.
2. `go.mod` requires neither `codeberg.org/readeck/go-readability/v2` nor
   `github.com/JohannesKaufmann/html-to-markdown/v2`; `golang.org/x/net` and `golang.org/x/sys`
   appear only as `// indirect`.
3. Bare `personant` prints help and exits 0. The verbs are `init`, `index`, `verify`, `archive`,
   `version` (plus `ping`/`models` only under U5 option (b)).
4. Personant reads its settings from `$XDG_CONFIG_HOME/personant/config.toml`, else
   `$HOME/.config/personant/config.toml`, never from `$PERSONANT_HOME`. `personant init` writes
   the template there and writes no `config.toml` or `providers.toml` into the home.
5. The `MemoryOps` port has no configuration methods; no package under the memory runtime
   (`turn`, `memops`, `store`, `recall/*`, `curator`, `workset`, `autogit`, `recovery`, `index`,
   `verify`) imports the config package.
6. A retired or unknown key in `config.toml` produces a warning naming it; it never refuses.
7. Every `tool.result`, `fs.read`, `fs.write` and `user.shell-capture` delta passes the SPEC
   §8.2.1 redactor before it enters the §3.0 chain.
8. `just test` and the mock `just sim` are green after every package; the live rungs run from the
   reshaped live config.
9. No contract document or README names a removed package, verb, recipe or dependency except as
   history; the U5 and U6 `Open:` clauses are resolved per the decisions.

## Invariants (the failure modes they prevent)

1. **Remove an import edge before the package it points at.** `internal/turn` imports
   `internal/tools` in production code (`toolloop.go`, `turn.go`); deleting `internal/tools`
   first breaks the memory runtime. Each package leaves `just build` and `just test` green.
2. **Move the only implementation of a SPEC requirement before deleting its package.** The SPEC
   §8.2.1 redactor lives in `internal/shell/redact.go`; it moves to `internal/redact` in the same
   change that deletes `internal/shell`, and WP2 makes the chain use it.
3. **Configuration is not memory.** The config package depends on nothing in the memory runtime
   and nothing in the memory runtime imports it; values reach the runtime as constructor
   arguments at the wiring site (the sim's live harness today, the A5 service later).
4. **One resolver for key material.** API-key file resolution exists once, in the config package;
   the sim's live harness uses it rather than reading key files itself. The `[user]`, `[chat]`
   and `[search]` sections leave the product type; a sim-only key never enters it.
5. **The config directory is resolved by rule, not by `os.UserConfigDir`.** On macOS
   `os.UserConfigDir` returns `~/Library/Application Support`, which contradicts SPEC §8.2.2.
6. **No test writes the operator's real config directory.** `store.Init` does not write config;
   `cmd/init` does, and every test that runs it sets `XDG_CONFIG_HOME` to a temp directory.
7. **Prune dependencies with a tidy that does not upgrade.** `just update-dependencies` runs
   `go get -u ./...` (whole-graph upgrade); WP1 adds a tidy-only recipe and every package that
   drops a dependency runs it.

Left to the implementer: the config package's internal layout, warning wording, and whether
`model` takes the config package's endpoint type or plain arguments (either way `model` may import
the config package; the reverse is not allowed).

## 1. Inventory

Import graph, re-runnable:

```
go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./cmd/... ./internal/...
for p in chat term term/screentest shell tools tools/web ping modellist; do
  echo "$p: $(git grep -l "\"personant/internal/$p\"" -- '*.go' | grep -v "^internal/$p/")"; done
```

Answers (branch `plugin-route`, after `1cecf0c`): `chat` ← `cmd/chat.go`, `cmd/main.go`;
`term` ← `internal/chat` only; `term/screentest` ← `internal/chat/render_test.go`,
`internal/term/editor_test.go`; `shell` ← `internal/chat` only; `tools` ← `internal/chat`,
`internal/turn/toolloop.go`, `internal/turn/turn.go`, `internal/turn/toolloop_test.go`,
`internal/turn/webtools_test.go`; `tools/web` ← `internal/chat`, `internal/turn/webtools_test.go`;
`ping` ← `cmd/ping.go`; `modellist` ← `cmd/models.go`. Run `go list` with package patterns, not
`./...`: gitignored sim run directories contain Go files that break the `./...` pattern.

| Package | Fate | Why / what changes |
|---|---|---|
| `internal/chat` | remove | REPL; only `cmd` imports it |
| `internal/term`, `internal/term/screentest` | remove | terminal for the REPL; only `chat` imports it |
| `internal/shell` | remove, except `redact.go` + `redact_test.go` → new `internal/redact` | shell escape for the REPL; the redactor is a SPEC §8.2.1 requirement with no other implementation |
| `internal/tools`, `internal/tools/web` | remove | self-built tool surface; SPEC §6 "Unused" |
| `internal/turn` | reshape | delete the in-process tool loop (`toolloop.go`, `State.Tools`, `State.OnToolReceipt`, the tool-round branch of `Run`); drop the dead `State.Provider` field; install the redactor at the chain intake |
| `internal/ping`, `internal/modellist`, `cmd/ping.go`, `cmd/models.go` | remove under U5 (a); reshape onto the config package under U5 (b) | the `ping`/`models` verbs (SPEC §4.1 Open U5) |
| `cmd/chat.go`, bare-invocation `RunE` in `cmd/main.go` | remove | bare `personant` prints help |
| `internal/store` `config.go`, `providers.go`, `userconfig.go` | move/remove | loading moves to the config package; `[user]` exists only for `tools/web` |
| `internal/memops` provider and config types, `MemoryOps.LoadProviders`/`LoadConfig`, `fileadapter` methods | move/remove | config is not memory; the only callers are `chat`, `ping`, `modellist` |
| `internal/model` | keep; trim request-side tool encoding | see section 2 |
| `internal/version` | reshape per U6 | `FrontEnd` |
| `internal/testsupport` | keep; endpoint type follows the config package | live-endpoint helpers |
| `internal/scenarios`, `internal/scenarios/sim` | keep; reshape provider and live-config plumbing | section 6 |
| `turn`, `memops`, `store`, `recall/*`, `curator`, `autogit`, `recovery`, `index`, `verify`, `workset`, `prompt`, `dedup`, `eventlog`, `crashpoint`, `clock`, `log`, `metrics` | keep | none imports a removed package except `turn` (above) |

Exported symbols whose only production caller was the REPL, and stay: the U4 command and ack
surface in `internal/turn` (`PauseThread`, `ResumeThread`, `BackToThread`, `CreateTopic`,
`RenameTopic`, `ManualClosure`, `DrainClosures`, `PendingClosures`, the `Phase*` labels, the ack-mode
parsers), `curator.NewHTTPCurator` (the production curator; A5 constructs it), the
`measure.*Hit` types and `store.DirectiveUserFile`. They wait for U4 and A5. Those that go with
their consumer: `memops.ChatConfig`, `SearchConfig`, `UserConfig`, `ProviderAPIExa`,
`ProviderTypeSearch`, `LocalProviderName`, and `turn.ToolReceipt`, `IsToolPhase`,
`PhaseRunningTool`. Re-run after each removal to find the next orphans: for each symbol,
`git grep -l -w <Symbol> -- '*.go'` showing only its defining package.

What the REPL is the only production caller of, so the binary loses it until A5 builds the
service: wiring the curator, the embedder and the recall service; loading config and providers;
the interactive project-bootstrap prompts (the resolver itself, `store.ResolveActiveProject`,
stays); the day barrier at session open. The REPL's open sequence (`internal/chat/chat.go`
`Run`: Init, Reconcile, day barrier, providers and config, project bootstrap, `LoadSession`,
curator and recaller install, turn loop, closure drain) remains readable for A5 at
`git show <WP1 commit>^:internal/chat/chat.go`.

## 2. Model-access boundary (U5)

| Code | Memory runtime use | REPL/harness-only use | Sim use |
|---|---|---|---|
| `model.Client.Consult` | `curator.HTTPCurator` (closure summary, SPEC §5.2); constructed in production only by `chat` | — | mock gate uses `scriptedCurator` (`internal/scenarios/harness_setup.go`); no rung constructs a real curator |
| `model.Client.ConsultStream`, SSE parse | `turn.Run`'s main request: the in-process path, which A3/A5 replace in production (opencode makes the call) | `chat` via `turn.Run` | mock gate drives `turn.Run` with `MockClient`; live-inference rung with `HTTPClient` |
| `model.Client.ListModels` | none | `chat` model resolution, `ping`, `modellist` | live-rung reachability probes (`sim_live_test.go`), `testsupport.RequireModelPresent`, `internal/prompt` probe tests |
| `model.Embedder`, `NewHTTPEmbedder`, `MockEmbedder` | `recall/measure.Service`; wired in production only by `chat` | — | live-embedding rung, tag-fidelity grader embedder |
| `Request.Tools`, `ToolSpec`, tool-message encoding | none once WP3 lands | the `turn` tool loop | none |
| provider pool, `LoadProviders`, `LoadConfig`, `ValidateConfig` | none | `chat`, `ping`, `modellist` | live-rung endpoint loading |
| dissector (SPEC §5.3), recall judgment, memory vetting reviewer (B10) | not built | — | — |

Commands: `git grep -l '"personant/internal/model"' -- '*.go' ':!*_test.go'` → `chat`, `curator`,
`modellist`, `ping`, `recall/measure`, `scenarios`, `scenarios/sim`, `tools`, `tools/web`, `turn`;
`git grep -n 'NewHTTPCurator(' -- '*.go'` → `internal/chat/chat.go:394`, `internal/chat/model.go:145`
and curator tests; `git grep -n 'NewService(' -- internal/chat` → `chat.go:351, 358`.

Consequences that hold under every U5 option:

- The embedder needs a Personant-held endpoint and key (opencode has no embedding provider, SPEC
  §8.2.1), so a config loader, key resolution, the HTTP embedder and the redactor's key set all
  survive.
- The in-process sim has no opencode. Its live-inference rung needs a direct chat endpoint, so
  `HTTPClient`'s chat path and an endpoint loader survive as test infrastructure even under (a).
- The memory runtime reaches models only through interfaces: `curator.Curator` over
  `model.Client`, and `model.Embedder`. U5 changes what the wiring site installs behind them, not
  the runtime. Under (a) the production adapter implements `Consult` only, so `curator` should
  then depend on a Consult-only interface (A5's change).

Packages that depend on U5 and say so: WP5 (`ping`, `modellist`, their verbs), WP6's role-model
keys, the CONVENTIONS `langchaingo/llms` line, SPEC §4.1, §8.1 "opencode settings", §8.2.1, §8.2.2
and §2.1 "Outside `$PERSONANT_HOME`".

## 3. Configuration

Today (`internal/memops/memops_model.go` `Config`, `Provider`; loaders in `internal/store`;
fixtures `test/config.toml`, `test/providers.toml`, `test/broken-config.toml`, `test/api_keys/`):

| File / key | Read by | Fate |
|---|---|---|
| `<home>/config.toml` `[user] name`, `email` | `tools/web` User-Agent; written by `store.Init` via `EnsureUserSection` | drop (WP3) |
| `[chat] defaultModel` | `chat`; the sim's live-inference rung | drop from the product; sim-only key (section 6) |
| `[chat] showThinking` | `chat` | drop |
| `[search] provider`, `maxPerTurn`, `maxPerDay` | `tools/web` | drop (WP3) |
| `[embedding] model`, `vectorLength` | `chat`; sim live rungs | keep (SPEC §8.2.2) |
| `<home>/providers.toml` `[<name>] baseUrl` | `chat`, `ping`, `modellist`, sim live | keep as `[providers.<name>] baseUrl` in `config.toml` |
| `apiKeyFile` | same | keep; relative paths resolve against the config file's directory |
| `apiKeyUnsafe` (inline key) | same | drop: SPEC §8.2.2 makes `config.toml` not secret-bearing |
| `type`, `api` | pool validation (`inference`/`openai`, `search`/`exa`) | drop: one kind and one protocol remain |

Proposed file, valid under both U5 options:

```toml
# $XDG_CONFIG_HOME/personant/config.toml, else ~/.config/personant/config.toml
[embedding]
model        = "reaper/nomicai-embed"   # "provider/model", split on the first "/"
vectorLength = 768                      # optional

[providers.reaper]
baseUrl    = "http://reaper.local:4000/v1"
apiKeyFile = "keys/reaper.key"          # optional; relative to this file's directory
```

Under U5 (b) only, a `[models]` table adds role references (`curator`, `dissector`, `judgment`,
`reviewer`), each `"provider/model"` into `[providers.*]`; its exact keys are set when (b) is
chosen. Under (a) the file stays as above. Load-time validation keeps today's rule, narrowed: every
model reference is well formed and names a `[providers.*]` table in the same file. That closes the
SPEC §8.2.2 Open on where the embedding provider is defined.

Location rule: `$XDG_CONFIG_HOME/personant/` when the variable is set and non-empty, else
`$HOME/.config/personant/`. `$PERSONANT_HOME` does not move it. Incumbent search: `git grep -n -E
'UserConfigDir|XDG_CONFIG_HOME' -- '*.go'` finds only `zedKeymapPath` in
`internal/chat/terminalsetup.go`, the same rule for another program's file, deleted with `chat`.

Existing homes: **a clean break, no migration** (decision D3). The binary stops reading
`<home>/config.toml` and `<home>/providers.toml`. `openHome` warns once per process, through
`internal/log`, when either file exists, naming the new location. Retired keys found in the new
file warn by name (TOML undecoded keys). No home-format bump and no migration-registry step:
the home format guards the readers of memory layout (SPEC §9.1), these files are read by none of
them, and ARCHITECTURE "Home layout" already omits them. The home's managed `.gitignore` block and
seeded `README.md` stop naming the two files.

## 4. Version lines (U6)

`version.FrontEnd` is printed by `personant version` and `--version`
(`internal/version/version.go`), recorded as `frontend=` in `system.bootstrap`
(`internal/eventlog/bootstrap.go:38-40`), and pinned by `internal/version/version_test.go` and
`internal/eventlog/bootstrap_test.go:39`. `just test-fe`, `fe-scope-check`, `FE_SCOPE_PREFIXES`,
`FE_TESTPKGS` and the `test-be` alias exist for the REPL.

`test-fe` and its guard go in WP1 under either U6 option: their scope prefixes are `cmd/`,
`internal/chat/`, `internal/version/`, `internal/shell/`, `internal/term/`, three of which WP1
deletes, and a repurposed line would version the TypeScript plugin, whose tests are not Go. U6
then decides only the constant, the bootstrap field and the version output (WP7). Options and
recommendation: "Decisions needed", D2.

## 5. Contract documents

Each change lands in the package that motivates it:

| Document | Change | WP |
|---|---|---|
| ARCHITECTURE entry point, "Superseded code" | trim per package; delete the paragraph when the last package goes | 1, 3, 5 |
| ARCHITECTURE `module-map.md` | `cmd/` row: drop `chat` (and `ping`, `models` under U5 (a)); `internal/model` row: drop "SSE streaming" only if WP3 removes it, add that the chat path serves the sim's live rungs; add rows for `internal/redact` and the config package | 1, 2, 6 |
| ARCHITECTURE "Single spelunking location", substrate non-negotiables table | drop "`paths.go` still places…"; resolve the U5 Open on `providers.toml` and `api_keys/` (they become `[providers.*]` and key files under the config directory) | 6 |
| ARCHITECTURE event-log format | add the `permissions` `redaction-fire` row (`occurrences=`, `bytes=`, never the matched text); `frontend=` per U6 | 2, 7 |
| SPEC §4.1 | drop the U5 Open on `ping`/`models` (remove or keep them); U6 Open per D2 | 5, 7 |
| SPEC §8.1 and "opencode settings" | init writes the template under the config directory; the provider-pool-template Open resolves per U5 | 5, 6 |
| SPEC §8.2.1, §8.2.2, §8.2.3, §2.1 | provider tables in `config.toml`; resolution of the embedding provider; `XDG_CONFIG_HOME` (D5); key-file placement | 5, 6 |
| SPEC §9.1 "Version lines" | U6 Open per D2 | 7 |
| CONVENTIONS `dependencies.md` | delete the `liner`/terminal sentence (WP1) and the "Pending removal" bullet (WP3); the `langchaingo/llms` line per U5 | 1, 3, 5 |
| CONVENTIONS `opencode-clone-referent-only.md` | delete "Free-API citizenship" | 3 |
| CONVENTIONS "Versioning & acceptance" | U6 Open per D2 | 7 |
| README | "What the current tree builds"; delete "Chat REPL"; Build Targets drops `test-fe`; acknowledgements drop `x/sys`, `x/net`, readability, html-to-markdown and change the `toml` row to `config.toml`; "Sim Knobs" names the reshaped live config | 1, 3, 6 |
| ROADMAP | B1 checked off; A12 loses the config move; A5 gains "install the redactor with the resolved key set" and "wire curator, embedder and config per U5"; U5 and U6 checked when decided | 8 |
| `ROADMAP_PLANS/inference-in-loop.md` | names the old live-config pair; update or note it | 6 |

## 6. Sim and scenario harness

Nothing in `internal/scenarios` drives the REPL, offers tools, or reads a home's config:
`git grep -n -E 'internal/chat|\.Tools\b|LoadProviders\(ctx|LoadConfig\(ctx' --
'internal/scenarios/*.go' 'internal/scenarios/sim/*.go'` returns nothing. What touches the
removal set:

- `harness_setup.go` writes a sentinel `providers.toml` into the run home that nothing reads, and
  builds a `memops.Provider` only to pass to `turn.NewState`/`LoadSession`, which store it in
  `State.Provider`, a field nothing reads (`git grep -n -F '.Provider' -- 'internal/turn/*.go'`
  shows only the declaration). Both go in WP4, with `Harness.Provider` (`harness.go:731`).
- `sim_live_test.go` loads the user-provided live pair (constants `liveProvidersFile`,
  `liveConfigFile`) with `store.LoadProviders`/`store.LoadConfig`, reads `[chat] defaultModel` for
  the live-inference rung and `[embedding]` for the live-embedding rung and the grader embedder.
  WP6 moves it to one live file in the product format plus a sim-only chat-endpoint table that
  the sim decodes itself through the config package's endpoint and key-resolution code, and
  updates `liveConfigMissingMsg`. The operator rewrites their live file once (README "Sim Knobs").
- `internal/turn` tests drive the chain with `MockClient`; `toolloop_test.go` and
  `webtools_test.go` go with the tool loop.

The mock gate (`just test` runs the sim package at `1d`; `just sim` at `1w`) uses only
`MockClient`, `scriptedCurator` and a nil embedder, none of which changes. WP3, WP4 and WP6 add
`just sim` to their done-lines.

## 7. Work packages

Every package that touches Go shares `just build` and runs alone in the shared tree. Done-lines end
in a checkpoint `just test`, then commit.

### WP1: Remove the REPL, terminal and shell (mechanical)

- Move `internal/shell/redact.go` and `redact_test.go` to `internal/redact/` (package `redact`;
  symbols unchanged); rewrite its doc comment, which describes `#` captures, to state the SPEC
  §8.2.1 rule it enforces.
- Delete `internal/chat/` (with `testdata/`), `internal/term/` (with `screentest/`), the rest of
  `internal/shell/`, and `cmd/chat.go`.
- `cmd/main.go`: drop the `internal/chat` import, the root command's `RunE` and `withStdStreams`,
  and the comments that name the chat entry points. Without `RunE`, cobra prints help.
- `justfile`: delete `FE_SCOPE_PREFIXES`, `FE_TESTPKGS`, `fe-scope-check`, `test-fe`, `test-be`;
  drop `chat`, `shell`, `term` from `RACEPKGS` and its rationale rows; fix comments naming those
  recipes or packages. Add `tidy-dependencies` (`go mod tidy`, nothing else) and run it:
  `golang.org/x/sys` becomes `// indirect`. `go mod graph | grep ' golang.org/x/sys@'` shows
  `go-git/v5` and `go-billy/v5` requiring it.
- Docs per section 5 (rows marked 1).
- Done-line: `just build`; `just test-run ./internal/redact`; `just test-run ./cmd`; checkpoint.

### WP2: The redactor on the chain intake (expert)

- `internal/turn`: a `State` field holding a `*redact.Redactor` (nil is inert, as
  `Redactor.Redact` already treats it). `onContextDelta` (`chain.go:70`), the single intake, redacts
  `Content` for `memops.SourceToolResult`, `SourceFSRead`, `SourceFSWrite` and
  `SourceUserShellCapture` before anything stages or extracts from it, and on a fire logs
  `permissions.redaction-fire` with occurrence and byte counts, never the match.
- No production wiring supplies keys yet (A5 does); the sim leaves the field nil.
- Test: a delta carrying a key reaches staging redacted; other sources pass untouched; a nil
  redactor changes nothing.
- Done-line: `just build`; `just test-run ./internal/turn 'Redact'`; checkpoint.

### WP3: Remove the self-built tool surface (expert)

- `internal/turn`: delete `toolloop.go`, `toolloop_test.go`, `webtools_test.go`; in `turn.go`
  delete `State.Tools`, `State.OnToolReceipt`, `req.Tools = state.Tools.Specs()`, the
  `toolMessages`/`toolRounds` state and the tool-round branch after the stream drains. A response
  that carries tool calls then ends the request loop as one without them does; the re-issue paths
  for the SPEC §5.5 fetch and the reminders stay. Keep `bodyParts`/`joinRoundBodies` only if a
  remaining re-issue path still produces several segments.
- Delete `internal/tools/` and `internal/tools/web/`.
- `internal/model`: delete `Request.Tools`, `ToolSpec` and the request-side tool encoding, and any
  tool-call field that only `model`'s own tests then reference (check with
  `git grep -n -w -e ToolSpec -e ToolCalls -e ToolCallID -- '*.go'`), with
  `toolstream_test.go` and `toolstream_live_test.go` to match.
- Remove what served only the tools: `internal/store/userconfig.go` and its test, `store.Init`'s
  `[user]` step (`ensureUserIdentity`) and its seed text; `memops.Config.User`/`.Search` with their
  types; `ProviderTypeSearch`, `ProviderAPIExa` and the `search` row of `protocolsByKind`; the
  search entry in `test/providers.toml`, `test/config.toml` `[search]`,
  `test/api_keys/dummy-search.key`, and the assertions on them in `internal/store/config_test.go`
  and `providers_test.go`.
- `just tidy-dependencies`: readability and html-to-markdown leave `go.mod`; `golang.org/x/net`
  becomes `// indirect` (required by `go-git/v5` and `go-billy/v5`). Today `go mod why -m` traces
  `cascadia`, `go-shiori/dom`, `gogs/chardet`, `itlightning/dateparse` and
  `JohannesKaufmann/dom` to `internal/tools/web` only; tidy removes them.
- Docs per section 5 (rows marked 3).
- Done-line: `just build`; `just test-run ./internal/turn`; `just test-run ./internal/model`;
  `just test-run ./internal/store 'Config|Providers|Init'`; checkpoint; `just sim`.

### WP4: Drop the dead provider parameter (mechanical)

- Remove `turn.State.Provider` and the `provider memops.Provider` parameter of `turn.NewState`
  and `turn.LoadSession` (`turn.go:93, 474, 510`).
- Callers: `git grep -l -E 'NewState\(|LoadSession\(' -- '*.go'` after WP3 → 32 files:
  `internal/scenarios/harness_run.go`, `harness_setup.go`, `internal/turn/turn.go`, and 29 test
  files in `internal/turn` (144 matching lines, definitions included).
- `internal/scenarios`: delete `Harness.Provider`, the provider literal and the sentinel
  `providers.toml` write in `harness_setup.go`.
- Done-line: `just build`; `just test-run ./internal/turn`; `just test-run ./internal/scenarios`;
  checkpoint; `just sim`.

### WP5: The `ping` and `models` verbs (gated on U5; mechanical under (a))

- Under (a): delete `cmd/ping.go`, `cmd/models.go`, `internal/ping/`, `internal/modellist/`, and
  `memops.LocalProviderName`. SPEC §4.1 drops the Open and both verbs; CONVENTIONS keeps
  `langchaingo/llms` out with the reason that opencode serves the role models.
- Under (b): skip this package; WP6 moves both verbs onto the config package and SPEC §4.1 lists
  them.
- Done-line: `just build`; `just test-run ./cmd`; checkpoint.

### WP6: Configuration under `~/.config/personant/` (expert; needs U5)

- New leaf config package (stdlib and `BurntSushi/toml` only) owning: directory resolution
  (section 3 rule), the types moved out of `memops` (`Config`, the embedding section, the endpoint
  record, `ParseModelRef`, the narrowed validation), loading with `apiKeyFile` resolution and the
  retired/unknown-key list, and the template `personant init` writes (never overwriting an
  existing file).
- Remove `store/config.go`, `store/providers.go` with their tests (absorbed as config-package
  tests over reshaped `test/` fixtures), `PersonantPaths.Providers`/`.Config`,
  `MemoryOps.LoadProviders`/`LoadConfig` and the `fileadapter` methods, `memops/config_test.go`,
  and the provider and config types in `memops_model.go`.
- `cmd/init.go` writes the template; `store.Init` writes neither file, and the home `README.md`
  and managed `.gitignore` seeds stop naming them. `openHome` warns on a stale
  `<home>/config.toml` or `providers.toml` (section 3).
- `internal/model` (`NewHTTPClient`, `NewHTTPEmbedder`), `internal/testsupport`
  (`ReaperProvider`) and the sim's live harness take the config package's endpoint type; the live
  harness loads through the config package (section 6).
- Under U5 (b): add the `[models]` role keys; move `ping`/`modellist` here.
- Tests that run `init` set `XDG_CONFIG_HOME` to a temp directory.
- Docs per section 5 (rows marked 6).
- Done-line: `just build`; `just test-run` on the config package, `./internal/store 'Init'`,
  `./cmd 'Init|Version|HomeFormat'` and `./internal/scenarios/sim 'Live'`; checkpoint; `just sim`;
  operator: `just LIVE_EMBEDDING=true DURATION=1d sim` and
  `just LIVE_INFERENCE=true DURATION=1d sim` against the reshaped live file.

### WP7: The front-end version line (gated on U6; mechanical)

- Under D2's recommendation: delete `version.FrontEnd` and its doc comment; `personant version`
  and `--version` print the substrate line, build stamp and home format; `system.bootstrap`
  drops `frontend=`; update `internal/version/version_test.go`, `cmd/version_test.go` and
  `internal/eventlog/bootstrap_test.go`. Docs per section 5 (rows marked 7).
- Done-line: `just build`; `just test-run ./internal/version`; `just test-run ./internal/eventlog
  'Bootstrap'`; `just test-run ./cmd 'Version'`; checkpoint.

### WP8: Closing sweep (mechanical)

- ROADMAP edits (section 5). `git grep -n -E
  'internal/(chat|term|shell|tools|ping|modellist)|test-fe|fe-scope|go-readability|html-to-markdown|providers\.toml'
  -- PROJECT_DOCS README.md AGENTS.md ROADMAP.md justfile '*.go'` returns only lines that are
  history (`OPENCODE_RESEARCH/` is not searched; it is dated evidence).
- Done-line: `just build`; checkpoint.

## Decisions needed

**D1 — U5: how the service reaches its own models.**

- (a) Role models (curator, dissector, judgment, vetting reviewer) through opencode's providers by
  `experimental.generate` (`OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md` §2.3); the embedder
  through Personant config. No setting overlaps opencode's by construction; the user configures
  chat providers once; `ping`/`models` go. Costs: a generation channel the Go service can call is
  unestablished (the plugin calls the service, not the reverse; opencode's server also exposes
  `/api/experimental/generate/text`, `OPENCODE_RESEARCH/OPENCODE_SERVER_SURVEY.md` §1.4, with
  address and auth unexamined), so a probe must establish one first; it is an `experimental`
  API, held steady by the pinned release; role-model work needs opencode running.
- (b) A Personant provider pool in `config.toml` (`[providers.*]` plus `[models]` role keys).
  The service runs its models without opencode, and production and the sim share one client path.
  Costs: SPEC §8.2.2 forbids a setting opencode also holds, and no mechanical check can enforce
  that without reading opencode's configuration, which SPEC §8.2 leaves to opencode, so the rule
  rests on the user; a key may be configured twice.
- **Recommendation: (a)**, conditional on a probe establishing the generation channel; if it
  fails, (b). It follows SPEC §1 (providers are opencode's) and keeps the no-overlap rule true
  without enforcement. WP1 to WP4 proceed either way; WP6's base file is the same under both.

**D2 — U6: the front-end version line.**

- (a) Retire `version.FrontEnd`, its bump contract and the `frontend=` field; keep the substrate
  semver and home format.
- (b) Repurpose it for the plugin. On this branch it would version nothing: the plugin is not
  built and is TypeScript with its own package version.
- **Recommendation: (a).** Plugin-to-service compatibility is A5's to design, most simply as an
  integer protocol version checked at the first RPC, like the home format, rather than a semver
  line with a bump contract. `test-fe` goes in WP1 under either option (section 4).

**D3 — Existing homes' config: clean break or migration.**

- Clean break (section 3): stop reading the home's files, warn while they remain, the user moves
  values by hand.
- Migration: a home-format step that moves and rewrites them. It would make the registry's first
  byte-rewriting step a transform of hand-edited settings, not memory, under a format that
  guards memory layout.
- **Recommendation: clean break.** The new file is small, most retired keys have no destination,
  and ROADMAP "Configuration migration on system upgrade" (far horizon) is the place for a general
  mechanism.

**D4 — Strip before the replacement works.** ROADMAP B1 sets a precondition: A5 to A12 done and an
end-to-end opencode session passing. This plan runs before that. Consequence: from WP1 until A5
ships, Personant has no interactive front end, and nothing in the binary wires the curator, the
embedder or config; memory is exercised only by the sim and the store verbs. Nothing else depends
on the REPL: the acceptance gate never drives it (section 6), SPEC already marks its surfaces
"Unused", and A3/A5 take their reference from `probe-path`, not from `internal/chat`.
**Recommendation: accept**, and replace B1's precondition with this plan.

**D5 — Honor `XDG_CONFIG_HOME`.** Adds one variable to SPEC §8.2.3; matches opencode's own
config placement and the only incumbent rule in the tree. **Recommendation: yes.**

**D6 — Substrate version bump.** Removing two `MemoryOps` methods changes the port. No written
rule covers substrate PATCH bumps (CONVENTIONS "Versioning & acceptance" states only MINOR at
milestone gates); past practice bumped PATCH per substrate change. **Recommendation:** one PATCH
bump in WP6.

## Not established

- A channel by which the Go service can request a generation through opencode (D1 (a)).
- Whether `golang.org/x/text` leaves `go.mod` or stays `// indirect` after WP3's tidy.
- Whether the executing agent can reach `reaper.local` for WP6's live done-line.
- Whether any operator workflow relies on bare `personant` starting the REPL.

## Post-sprint review

Per CONVENTIONS "Post-sprint review": an architect and a coder review the changes, tests included,
against THESIS.md, SPEC.md, ARCHITECTURE.md and CONVENTIONS.md, without the plan; the burndown goes
to `strip-self-built-harness_REVIEW_BURNDOWN.md` with a prompt-engineer pass before use.
