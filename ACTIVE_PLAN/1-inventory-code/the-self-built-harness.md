### The self-built harness

| Site | Kind | What happens | Ev | WP |
|---|---|---|---|---|
| `internal/chat/` (with `testdata/`), `cmd/chat.go`, root `RunE` and `withStdStreams` in `cmd/main.go` | remove | bare `personant` prints help (cobra default without `RunE`) | E1, E2 | 1 |
| `internal/term/`, `internal/term/screentest/` | remove | only `chat` imports them | E1 | 1 |
| `internal/shell/` including `redact.go` | remove | SPEC §8.2.1 sets the redactor's key set to "the keys Personant has already resolved", and Personant now resolves none | E1 | 1 |
| `internal/tools/`, `internal/tools/web/` | remove | after the tool loop leaves `turn` | E1 | 3 |
| `turn`: `toolloop.go`, `toolloop_test.go`, `webtools_test.go`; `turn.go` tool fields and the tool-round branch; `stream.go` tool identities | remove | a response with tool calls ends the request loop like one without them; the §5.5 and §3.3 re-issue paths stay | E1 | 3 |
| `model`: `Request.Tools`, `ToolSpec`, `ToolCall`, `Response.ToolCalls`, tool encoding and stream accumulation, `toolstream_test.go`, `toolstream_live_test.go`, the mock's tool calls | remove | opencode owns tool calls | — | 3 |
| `go.mod`: readability, html-to-markdown and their transitive set | remove | via `just tidy-dependencies`; `x/sys` goes indirect in WP1 and `x/net` in WP3 | E3 | 1, 3 |
| justfile `RACEPKGS` entries and rationale rows for `chat`, `term`, `shell` | reshape | list keeps `recall/measure model eventlog metrics log scenarios` | — | 1 |
| justfile `tidy-dependencies` (`go mod tidy` only) | new | the only new recipe | E4 | 1 |

