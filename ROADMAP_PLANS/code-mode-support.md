# Code Mode support

**Status:** deferred. Personant's server starts with opencode's `execute` tool denied — a configuration opencode itself
supports, in which the tools otherwise reached only through Code Mode are exposed to the model directly. This plan is
what supporting `execute` would take, if that is ever chosen. It applies only to the server-port route
([OPENCODE_SERVER_REPLACEMENT.md](OPENCODE_SERVER_REPLACEMENT.md)); under plugin integration opencode's own server runs
Code Mode unchanged.

## What Code Mode is

opencode's `execute` tool takes a JavaScript snippet written by the model and runs it in a server-side sandbox. Inside
the snippet, tools are callable as functions, so one model step can call several tools, combine their results and
return a shaped value. Some tools — MCP tools by default, browser tools and the `opencode_*` tools — are reachable only
through it while it is enabled.

The sandbox provides, as observed in generated code:
- `search({query, limit})` — a catalog lookup over available tools, returning paths, signatures and descriptions.
- `tools.<namespace>.<tool>(args)` bindings, including bracket form for hyphenated names
  (`tools.kbase["show-config"]`).
- Captured console output, returned to the model as a `Logs:` section.
- The snippet's `return` value as the tool result.

The authoritative definition is opencode's `packages/codemode` and the `execute` tool registration in
`packages/core/src/tool.ts`, at release tag v2.2.2.

## What models generate

Measured 2026-10-29 from one opencode session database: every `execute` call stored in it.

**Sample:** 129 calls across 18 sessions. Models: deepseek-v4.1-flash (57), Qwen3.8-Flash-Next (44),
minimax-m3.1-flash (19), Ornith-1.5-122B (9). The workload was dominated by a single MCP server. Larger frontier models
are not represented and may write more complex snippets.

| Feature | Share of snippets |
|---|---|
| `const`/`let`/`var` | 90% |
| `return` | 95% |
| `await` | 80% |
| `async` function declared in the snippet | 1% |
| Arrow functions | 30% |
| Ternary | 31% |
| `JSON.parse`/`JSON.stringify` | 30% |
| `Promise.all`/`allSettled`/`race` | 29% |
| `.map`/`.filter`/`.reduce`/`.forEach`/`.flatMap` | 27% |
| `for` loop | 16% |
| `if` | 9% |
| `try`/`catch` | 6% |
| Template literals | 5% |
| `while` | 3% |
| Regex use | 3% |
| `function` declaration | 2% |

| Measure | Value |
|---|---|
| Straight-line code only (no loop, branch, collection method or Promise combinator) | 44% |
| Tool invocations per snippet (`search` counted) | 0: 14 · 1: 73 · 2: 34 · 3: 6 · 4: 2 |
| Most-called function | `search` (40 calls) |
| Snippet length in lines | min 1 · median 4 · p90 15 · max 52 |
| Recorded status | `completed` for all 129, including failures |

**Failures surface only in output.** The stored status was `completed` even where the snippet failed. The real failures
seen were calls to tools not bound inside the sandbox (`tools.shell`, `tools.read`), reported as
`Unknown tool '<name>'` in the `Logs:` section.

**Shape of use:** small snippets that call one or two tools and reshape the result, or fan out a few calls
concurrently.

### Reproducing the measurement

Tool calls are stored in `session_message.data` (JSON) of opencode's database (`$XDG_DATA_HOME/opencode/opencode.db`
by default); an `execute` call is a `content[]` entry with `type: "tool"` and `name: "execute"`, its snippet in
`state.input.code` and its output in `state.content[].text`. From v2.2.2 a `session_part` table also exists and may
hold parts instead. Open the database read-only — opencode keeps it in WAL mode while running.

## Requirements for support

1. **A real JavaScript engine.** A restricted interpreter that pattern-matches tool calls would handle under half of
   observed snippets; the rest use `await`, `Promise.all`, collection methods, loops and branches.
2. **Snippet wrapping matching opencode's.** Snippets use top-level `await` and a bare `return`, so opencode wraps them
   in an async function; the port wraps them identically.
3. **Concurrent tool calls.** `Promise.all` over tool bindings must actually run the calls concurrently.
4. **Host functions mirroring opencode's sandbox exactly** — `search`, the `tools.*` binding shape, log capture, error
   reporting and result serialization — since the model is instructed by opencode's `execute` tool description.

## Approach

- **Engine: goja** (`github.com/dop251/goja`), pure Go. Its coverage of ES2015+ including `async`/`await`,
  optional chaining and `??` is unverified for the current release.
- **Concurrency:** a goja runtime is not goroutine-safe. Each tool binding returns a promise, runs the tool in a
  goroutine, and resolves the promise back on the runtime's goroutine through an event loop
  (`github.com/dop251/goja_nodejs/eventloop`, `RunOnLoop`).
- **Rejected:** a text-pattern interpreter (requirement 1), and restricting opencode's `execute` instructions to fit a
  simpler interpreter (diverges from opencode).

## Validation before committing to the approach

Run a corpus of real `execute` snippets, regenerated as above, through goja with stub tool bindings, and report the
parse and execution pass rate. Snippets that fail on engine limits, rather than stub behavior, decide whether goja is
sufficient.
