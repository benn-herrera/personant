### WP3: Remove the in-process tool loop and the tool surface (expert)

- Scope: `git grep -l -w -E 'ToolSpec|ToolCall|ToolCalls|ToolReceipt|IsToolPhase|PhaseRunningTool|appendToolIdentities|runToolRound|maxToolRoundsPerTurn|logToolRoundsExhausted|toolCallNames|OnToolReceipt' -- '*.go' | grep -v -E '^internal/(chat|tools)/'`
  → `model/{client,http_client,http_client_test,http_stream,mockllm,toolstream_live_test,toolstream_test,usage_reasoning_test}.go`,
  `turn/{stream,toolloop,toolloop_test,turn}.go`. Edit each.
- `internal/turn`: delete `toolloop.go`, `toolloop_test.go`, `webtools_test.go`. In `turn.go`
  delete `State.Tools`, `State.OnToolReceipt`, `req.Tools = state.Tools.Specs()`, the
  `toolMessages`/`toolRounds` state and the tool-round branch after the stream drains (today
  about lines 1155–1201, from the "§6.1 tool round" comment to the `buildSystemPrompt` re-issue):
  `if len(full.ToolCalls) == 0 { break }` becomes an unconditional `break`. Keep
  `bodyParts`/`joinRoundBodies` only if a remaining re-issue path (§5.5 fetch, §3.3 reminders)
  still yields more than one segment. In `stream.go` delete the `tools []model.ToolCall` result
  field, the `seen` accumulation and `appendToolIdentities` (lines 54–129).
- `internal/tools/`, `internal/tools/web/`: delete.
- `internal/model`: delete `Request.Tools`, `ToolSpec`, `ToolCall`, `Response.ToolCalls`, the
  wire tool types, request-side encoding, stream tool-call accumulation and the mock's tool-call
  support. Delete `toolstream_test.go` and `toolstream_live_test.go`; trim tool-call cases from
  `usage_reasoning_test.go` and `http_client_test.go`.
- `just tidy-dependencies`.
- Contract rows marked 3.
- Done-line: `just build`; `just test-run ./internal/turn`; `just test-run ./internal/model`;
  the scope grep → empty; `grep -n -E 'readability|html-to-markdown' go.mod` → empty;
  `grep -n 'golang.org/x/net' go.mod` → one `// indirect` line. Checkpoint; `just sim` (its
  metrics JSON is WP6's baseline only if WP4 and WP5 leave behaviour unchanged; WP5's run is the
  one WP6 uses).

