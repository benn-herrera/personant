# opencode TUI launch, attach and handshake

Surveyed: opencode source at tag `v2.2.2` (commit `098aa7fd6e02b03d6fc410f0b052038c7d4c5e81`), clone at
`/Users/agent-user/projects/opencode`. Installed binary: `/opt/homebrew/bin/opencode` ->
`/opt/homebrew/Cellar/opencode/2.2.1/bin/opencode`, `opencode --version` = `opencode v2.2.1` (Mach-O arm64).

`git diff --stat v2.2.1 v2.2.2 -- packages/cli packages/tui packages/client` shows no change to any file cited
below (`tui/src/app.tsx`, `cli/src/services/*`, `cli/src/commands/handlers/default.ts`,
`client/src/solid/connection.ts`, `client/src/service-probe.ts`, `client/src/effect/service.ts`). Paths below are
relative to the clone's `packages/` directory unless stated.

## Bottom line

**Yes, no fork is needed.** Released opencode connects its TUI to an arbitrary HTTP server through an existing,
documented global flag:

```sh
OPENCODE_PASSWORD=<secret> opencode --server http://127.0.0.1:<port> [<directory>]
```

`OPENCODE_PASSWORD` is optional; omit it to send no `Authorization` header. The same flag works on `opencode run`
and `opencode mini` (see their `--help`). With `--server`, the CLI spawns no server, does not read the
service registration file, and does not stop or replace anything. It checks the server's version only to print a
warning. To bring the TUI up, the server has to answer three things: `GET /api/info`,
`GET /api/fs/list` (or the fallback `GET /api/location`), and `GET /api/event`, an SSE stream whose first event is
`server.connected`. Details are in Q3.

## Q1. Attach to an existing server

- Global flag `--server <url>`: "Connect to a server URL instead of the background service"
  (`cli/src/commands/commands.ts:20-25`); `--standalone` is its sibling (`:13-18`). Installed v2.2.1
  `opencode --help` lists both.
- Default command handler: reads `input.server` (`cli/src/commands/handlers/default.ts:22`) and passes it to
  `ServerConnection.resolve` (`:35-37`).
- `--server` together with `--standalone` is rejected: "--server and --standalone cannot be combined"
  (`cli/src/services/server-connection.ts:28-29`).
- Password env var: `OPENCODE_PASSWORD`, with legacy `OPENCODE_SERVER_PASSWORD` as fallback (`cli/src/env.ts:10-13`).
  It is sent as HTTP Basic with username `opencode` (`cli/src/services/server-connection.ts:32-35`;
  header built at `client/src/service-probe.ts:93-98`).
- Docs: `services/www/src/docs/content/cli/index.mdx:51-56` (`opencode --server http://localhost:4096`), and
  `troubleshooting.mdx:135` ("Set `OPENCODE_PASSWORD` if it has one").
- There is no `attach`/`connect` subcommand and no URL env var. Installed `opencode --help` lists none, and the
  handler table at `cli/src/index.ts:29-84` has none.
- A URL with a path prefix works: request paths are joined relative to `baseUrl`
  (`client/src/promise/generated/client.ts:342-345`).

## Q2. Plain `opencode` launch behavior

`ServerConnection.resolve` (`cli/src/services/server-connection.ts:23-57`), with `mismatch: "replace"` passed by the
default handler (`cli/src/commands/handlers/default.ts:38`):

1. If the service config has `disabled: true`, or the user passes `--standalone`, the CLI spawns a private child:
   `<self> serve --stdio --port 0` with a random `OPENCODE_PASSWORD`. It reads the URL from the child's first
   stdout line, a JSON `{url}` (`server-connection.ts:47-49`; `cli/src/services/standalone.ts:17-33,49-56`). Port 0
   means the OS picks the port, so a port collision cannot happen.
2. Otherwise it uses the managed background service, `Service.ensure` (`client/src/effect/service.ts:49-127`):
   - **Discovery is file-based, not a port probe.** It reads the registration file `<state>/service.json`
     (`cli/src/services/service-config.ts:121`; the name varies by channel, `:41-44`; the fallback is
     `$XDG_STATE_HOME/opencode/service.json`, else `~/.local/state/opencode/service.json`,
     `client/src/service-probe.ts:39-41`). The schema is `{id?, version?, url, pid, password?}`
     (`client/src/effect/service.ts:142-148`).
   - It probes `GET <url>/api/info` with the registered password. The answer must carry
     `{version: string, pid: int}`, and `pid` must equal the registered pid (`client/src/service-probe.ts:49-90,100-105`).
   - A version mismatch leads to **replace**: the CLI sends SIGTERM, then SIGKILL, to the registered pid and starts
     a new service (`service-probe.ts:22-24`; `effect/service.ts:98-104,182-197`). The required version is the
     client's exact `OPENCODE_VERSION` (`cli/src/services/service-config.ts:148`). A 404 on `/api/info` means
     "incompatible health protocol", which fails the launch (`service-probe.ts:25-28,65-75`).
   - If no service answers, the CLI spawns `<self> serve --service` contenders (`service-config.ts:150`;
     `effect/service.ts:60-70,108-113`).
3. Default service port: `0xC0DE` = 49374 for channels `latest`/`dev`/`beta`/`next`, and `0xC0DF` = 49375 for
   `local`. Any other channel gets `10000 + hash(channel) % 50000`. On Windows, ports ≥ 0x8000 map into 0x7000 |
   low 12 bits (`cli/src/services/service-config.ts:46-60`). The default hostname is `127.0.0.1`
   (`cli/src/server-process.ts:72-73`). `opencode service set port|hostname` overrides both.
4. Port already occupied, in service mode (`cli/src/server-process.ts:74-78,164-188`): the new service first checks
   whether the occupant is the registered, compatible incumbent (`Service.incumbent`, which probes `/api/info` at
   the expected URL using the registered pid and password). If so, it exits quietly. Otherwise it retries for 15 s
   and then fails with "Managed service port N on H is already in use by … Configure another port with
   `opencode service set port <port>`". **It never picks another port, and it never connects to an unregistered
   occupant.**

## Q3. Identity and compatibility checks a third-party server must satisfy (`--server` path)

In order:

1. **`GET /api/info`**, 5 s timeout (`cli/src/services/server-connection.ts:36-40`; route at
   `client/src/promise/generated/client.ts:461-463`).
   - It must return status 200 with `Content-Type: application/json` (or `+json`) and a non-empty JSON body
     (`client.ts:388-398`, `json()` at `:2677-2694`). The body is not schema-validated; it is cast to `ServerInfo`.
   - `version` is compared to the client's `OPENCODE_VERSION`. A mismatch only prints "Warning: Server at … has
     version …; this client is …. Continuing anyway." (`server-connection.ts:41-44`). There is no version gate.
   - On failure: 401 gives "requires a password; set OPENCODE_PASSWORD" or "rejected the password", and any other
     failure gives "did not provide a compatible V2 health response" (`server-connection.ts:92-106`). Either way
     the CLI exits.
2. **`--session <id>` only:** the CLI looks up that session before the TUI starts (`cli/src/commands/handlers/default.ts:55-61`).
3. **Initial location**, before the TUI renders (`tui/src/app.tsx:227-231`):
   `GET /api/fs/list?location[directory]=<cwd>` (`client.ts:2078-2087`, query encoding at `:2660-2675`), reading
   `response.location.directory`. If that call fails, the TUI calls `GET /api/location` (`client.ts:484-492`) and
   reads `.directory`. If both fail, `Tui.run` fails and the TUI never draws.
4. **Event stream:** `GET /api/event` (`client.ts:2147-2157`), opened automatically when the client connection is
   created (`client/src/solid/connection.ts:241`; `tui/src/context/client.tsx:26`).
   - The response must be 200 with `Content-Type: text/event-stream` (`client.ts:401-411`). Events are sent as
     `data:` lines holding JSON objects, separated by a blank line (`client.ts:426-446`).
   - **The first event must have `type: "server.connected"`** and must arrive within 2 s
     (`connection.ts:32,81,97-107`). Its type is `{id, type: "server.connected", data: {}, metadata?, location?}`
     (`client/src/promise/generated/types.ts:950-956`).
   - After that, some bytes must arrive at least every 45 s, or the client aborts as "Event stream stalled". The
     server's own keepalive comment every 15 s satisfies this (`connection.ts:21-25,35,87-92`).
   - If the stream is not connected, the TUI still renders but shows a "Reconnecting" overlay after 5 s, and
   after 1 s on a reconnect (`tui/src/app.tsx:1380-1398`). With `--server` there is no managed reconnect hook, so
   the client retries the same URL every 1 s (`connection.ts:165-181`; `default.ts:107`).
5. **No other handshake.** The client sends no version header or client-identity header (`client.ts:341-368`). The
   TUI makes no pairing or token exchange on startup. The password is static Basic auth.
6. On `server.connected`, the TUI begins hydration, for example `session.active`, `location.get`, VCS and plugin
   list (`client/src/solid/data.ts:664-700`; `tui/src/plugin/context.tsx:538-572`). Plugin-list failures are
   swallowed, and plugin readiness is set on success or failure (`plugin/context.tsx:531-535,561`). The separate
   API survey covers the routes the home and session screens need.

With `--server`, the TUI is told the server is non-local (`default.ts:106`). Effects: the shell environment is not
forwarded (`default.ts:145`), the updater is flagged remote (`default.ts:128`), and no service reconnect or restart
hooks are installed (`default.ts:107-112`). The TUI's own config (`config.get/update`) is the CLI's local config
service, not a server call (`default.ts:88,121-125`).

## Q4. Process structure and release entry point

- **The TUI runs in the CLI process.** The default handler imports `run` from `@opencode/tui` and calls it in its
  own process (`cli/src/commands/handlers/default.ts:3,96`; `tui/src/app.tsx:214`). The server is a separate
  process: a `serve --service` daemon (`client/src/effect/service.ts:61`; `cli/src/services/service-config.ts:150`)
  or a `serve --stdio` child (`cli/src/services/standalone.ts:18-20`). Both are the same executable re-invoked
  (`cli/src/util/process.ts:5-11`).
- **Release build:** `cli/script/build.ts:158-185` compiles `./src/index.ts` (plus `photon-worker.ts`) with
  Bun into one executable.
  - Homebrew: the installed formula `/opt/homebrew/Cellar/opencode/2.2.1/.brew/opencode.rb:85-89` builds from the
    `v2.2.1` source tarball with `bun ./script/build.ts --single` in `packages/cli`, after patching
    `splitting: true` to `false` (`:83`). Brew therefore ships the same `packages/cli` entry point.
  - npm: `cli/bin/opencode.cjs` is a launcher that spawns the platform package binary `@opencode/cli-<os>-<arch>`
    (`:33-42,117-131`). `OPENCODE_BIN_PATH` overrides which binary it spawns (`:33,124`). That binary is built by
    the same script.

## Rejected alternative: impersonating the managed service

Plain `opencode` with no flags could be made to adopt a third-party server. Write a registration file with
`{url, pid, version, password}`, and answer `/api/info` with the same `pid` and the client's exact version. **Do
not use this.** It depends on the CLI's private registration file and channel naming. Any version drift makes the
CLI SIGTERM/SIGKILL the registered pid (`client/src/effect/service.ts:98-104,182-197`). The supported route is
`--server`.

## Unverified

- **No live run has happened.** Nobody has run `opencode --server <url>` against a non-opencode server, or the
  interactive TUI at all; the sandboxed shell has no TTY. The exact test, from a real terminal:
  - Start a stub that serves `GET /api/info` → `{"version":"x","pid":1,"urls":[],"paths":{"tmp":"/tmp"}}`
    (JSON), `GET /api/fs/list` → `{"location":{"directory":"<cwd>"}}`, and `GET /api/event` → `text/event-stream`
    with first frame `data: {"id":"e1","type":"server.connected","data":{}}` followed by a `: keepalive` comment
    every 15 s.
  - Run `opencode --server http://127.0.0.1:<port>`.
  - Expect the version warning on stderr, then the TUI drawn with no "Reconnecting" overlay. Missing hydration
    routes may produce error toasts or empty panes.
- The `ServerInfo` fields beyond `version` (`pid`, `urls`, `paths.tmp`, `hostname`, `capabilities`) are not
  checked on the `--server` path. Whether the TUI reads them later (for example `paths.tmp`, or
  `capabilities.persistentPty` for terminals) was not traced. The stub above includes them as a precaution.
- `location.directory` from `/api/fs/list` is not validated. What the TUI does if it is missing or not a string was
  not traced.
- The installed brew binary's `OPENCODE_CHANNEL`, which sets the service registration filename and default port,
  was not established. The release channel is probably `latest` (port 49374), but no permitted invocation prints it.
- The claim that the installed v2.2.1 binary behaves the same as the v2.2.2 source rests on the tag diff above
  and the matching `--help` output. No binary inspection beyond `--help` and `--version` was done.
