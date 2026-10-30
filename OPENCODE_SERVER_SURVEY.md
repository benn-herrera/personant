# opencode server survey (reference for a Go re-implementation)

**Surveyed:** opencode release tag **v2.2.2**, commit `098aa7fd6e02b03d6fc410f0b052038c7d4c5e81`
(local clone `../opencode`). All paths are repo-relative to that clone unless prefixed `personant:`.
Claims not confirmed in source are marked *unverified*.

**Provenance.** Section 1 was read directly. Sections 2 to 4 and the personant inventory were
compiled by research sub-agents. For files that changed between an earlier commit and v2.2.2, they
re-checked their claims against v2.2.2. Their key citations were spot-checked against the code:
subagent depth, permission `evaluate`, `Permission.Reply`, agent defaults, ID format, retry
schedule, `session_part`, event persistence, personant `Client` and tool loop. Line numbers in
sections 2 to 4 that were not spot-checked carry the sub-agents' readings.

**Package map (v2 architecture).**

| Package | Holds |
|---|---|
| `packages/schema` | Domain shapes and event definitions (Effect Schema) |
| `packages/protocol` | HTTP endpoint definitions (Effect `HttpApi` groups) and the generated `openapi.json` |
| `packages/core` | Runtime behaviour and persistence |
| `packages/server` | Implements the protocol |
| `packages/client` | Generated clients plus Solid data layer |
| `packages/tui` | The terminal UI |
| `packages/cli` | The `opencode` binary that launches the TUI and/or server |

Dependency direction is stated in `AGENTS.md:2`. The authority table is in
`specs/v2/README.md`: Protocol owns HTTP, Schema owns shapes, Core owns behaviour.

`packages/core/src/v1` and `packages/schema/src/v1` are **not** a live API. They migrate legacy
config files at load time (`packages/core/src/config/normalize.ts:21-27`) and import the legacy
database (`packages/core/src/database/v1-migration.bun.ts`). Every served route is under `/api/*`
or `/auth/*`.

---

## 1. Server contract the TUI depends on

### 1.1 How the TUI gets a server

The TUI is a library (`@opencode/tui` `run`). The `opencode` CLI process hosts it in-process and
hands it an endpoint `{url, auth?}` (`packages/cli/src/commands/handlers/default.ts:35-52,96-120`;
`packages/client/src/service.ts:2-14`). The server is always a separate process or remote URL.
`ServerConnection.resolve` (`packages/cli/src/services/server-connection.ts:23-57`) picks one of
three modes.

**1. `--server <url>`** (`server-connection.ts:30-46`)
- Password comes from `OPENCODE_PASSWORD` or `OPENCODE_SERVER_PASSWORD` (`packages/cli/src/env.ts:10-11`). Username is fixed to `opencode`.
- The CLI calls `GET /api/info` with a 5 s timeout.
- A version mismatch only prints a warning (`server-connection.ts:41-44`).
- No managed-service reconnect is offered (`default.ts:107`).
- **This is the attach point for an external Go back end: no TUI modification is needed.**

**2. `--standalone`, or service config `disabled:true`** (`server-connection.ts:47-49`;
`packages/cli/src/services/standalone.ts:17-56`)
- Spawns `<self> serve --stdio --port 0` with a random password in `OPENCODE_PASSWORD`.
- Reads one stdout line of JSON `{"url": ...}` (`standalone.ts:8,40-50`; emitted at `packages/cli/src/server-process.ts:208`).
- The stdin pipe is an ownership lease: the server exits on stdin EOF (`standalone.ts:26-28`; `server-process.ts:213,233-246`).

**3. Managed background service (default)**
- **Discovery:** the registration file `$XDG_STATE_HOME/opencode/service.json` (default `~/.local/state/opencode/service.json`) holds `{id, version, url, pid, password}` (`packages/client/src/service-probe.ts:39-41`; `packages/client/src/service.ts:46-57`).
- The file is written atomically, mode 0600 (`packages/cli/src/services/service-registration.ts:24-46`). The service shuts down if the file stops naming it, re-checked every 5 s (`:47-74`).
- **Probe:** `GET /api/info` with Basic auth. `pid` and `version` must match the registration; 404 means an incompatible protocol (`service-probe.ts:49-90`).
- **Start:** spawned as `<self> serve --service` (`packages/cli/src/services/service-config.ts:144-152`).
- **Default port** is `0xC0DE` (49374) for the latest/dev/beta/next channels (`service-config.ts:46-60`). Hostname defaults to `127.0.0.1` (`server-process.ts:72-73`).
- On a version mismatch the TUI's `ensure` replaces the service (`default.ts:38`; `service-probe.ts:22-32`).

**`opencode serve` with no flags** (`server-process.ts:72-96,208-209`)
- Binds `127.0.0.1`.
- With no port given, it scans upward from 4096 (`packages/server/src/process.ts:146-153`).
- The password is `OPENCODE_PASSWORD`, or a random one printed to stdout.
- A password is mandatory for the Node server process (`process.ts:55-56`).

**Client-side state is not on the server.**
- TUI settings live in `<XDG_CONFIG>/opencode/cli.json`, read by the CLI, not the server (`packages/cli/src/config/config.ts:31`; key list in `packages/tui/src/config/index.tsx:69-256`).
- The recent/favourite model list lives in `<XDG_STATE>/opencode/model.json` (`packages/tui/src/context/local.tsx:157`; sub-agent reading).
- TUI plugins and themes load in the TUI process (`packages/tui/src/app.tsx:232`).

**TUI startup calls** (`packages/tui/src/app.tsx:214-231`):
1. `GET /api/fs/list?location[directory]=<cwd>`, falling back to `GET /api/location`. This resolves the working location.
2. Open the SSE stream (§1.5).
3. The Solid data layer then lazily fetches catalogs (agents, providers, models, commands, skills, config, mcp, vcs, …) (`packages/client/src/solid/data.ts:2048-2076`).

### 1.2 Transport and auth

**HTTP**
- Plain HTTP/1.1 over loopback, with compression and CORS middleware (`process.ts:72-80`).
- **CORS allow-list:** no `Origin` header; `http://localhost:*`; `http://127.0.0.1:*`; `oc://renderer`; tauri origins; `*.opencode.ai`; plus configured extras (`packages/server/src/cors.ts:11-20`).
- Browser `fetch`/XHR from an unlisted origin is blocked. A non-browser client sends no `Origin` and passes.

**Auth: HTTP Basic, username `opencode`** (`packages/server/src/auth.ts:18-37`). Accepted credentials:
- `Authorization: Basic …` header (`packages/server/src/middleware/authorization.ts:33-40`).
- `?auth_token=<base64(user:pass)>` query parameter (same lines).
- An HMAC session token in place of the password, from pairing (`auth.ts:39-55`). Pairing uses `POST /api/pair` → `{code, expires_in}` and `GET /auth/connect/{code}` (`packages/server/src/pairing.ts:7-32`).
- Cookie `opencode_session_<port>`, same-origin only (`authorization.ts:65-72`; `auth.ts:57-61`).

**Unauthorized responses**
- 401 with `{"_tag":"UnauthorizedError","message":"Authentication required"}` (`authorization.ts:52-57`).
- `WWW-Authenticate` is sent only to non-browser clients or navigations (`authorization.ts:44-49`).

**Auth exemptions**
- WebSocket upgrade routes (`/api/pty/{id}/connect`, `/api/experimental/persistent-pty/{id}/connect`, `/api/experimental/client-mcp`) skip auth when a `ticket` query parameter is present; the handler validates the ticket.
- The pairing link also skips auth (`authorization.ts:83-94`; `process.ts:196-202`).

**Readiness**
- Before the application layer is ready, `GET /api/info` answers `{version, pid, urls, paths:{tmp}}`: 503 with `retry-after: 1` while starting or stopping, 500 if failed.
- All other routes return 503 `{code:"service_starting"|"service_stopping"|"service_failed"}` (`process.ts:186-238`).

**Location scoping (multi-project server)**
- A location-scoped request names its project directory via query `location[directory]=<abs path>` (OpenAPI `deepObject` style), or header `x-opencode-directory` (URI-encoded). Otherwise the server's cwd is used (`packages/server/src/location.ts:71-79`; `packages/protocol/src/groups/location.ts:6-27`).
- Session-scoped routes derive the location from the stored session instead (`packages/server/src/middleware/session-location.ts:16-29`).
- Location-scoped list responses are wrapped `{location:{directory, project}, data}` (`location.ts:49-60`). Most others are `{data: …}`, for example `packages/protocol/src/groups/session.ts:176,234`.

**Errors**
- Tagged JSON `{"_tag": "<Name>Error", message, …}` with fixed statuses: 400 invalid request/cursor, 401, 403, 404 not-found family, 409 conflict/busy/form-settled, 500 unknown/command-execution, 503 unavailable (`packages/protocol/src/errors.ts:5-265`).
- Schema decode failures are mapped by `SchemaErrorMiddleware` (`packages/protocol/src/api.ts:207-208`).

**Other headers**
- `x-opencode-capabilities: experimental-media-uri` opts a client into `media:` URIs (`packages/server/src/media-uri.ts:3-4`). The v2.2.2 TUI does not send it (no reference in `packages/tui/src` or `packages/client/src`).

### 1.3 Is there a machine-readable API definition?

**Yes, and it is generated from the server's own route definitions.**

- **Single source.** Endpoint definitions are Effect `HttpApi` groups in `packages/protocol/src/groups/*.ts`, assembled by `makeApiFromGroup` (`packages/protocol/src/api.ts:145-208`). The server builds its router from the same assembly (`packages/server/src/api.ts:6-12`; `packages/server/src/routes.ts:185`).
- **`packages/protocol/openapi.json`** (OpenAPI 3.1.0, 162 operations, 351 component schemas) is produced by `OpenApi.fromApi(ClientApi)` (`packages/protocol/script/generate-openapi.ts:7`). `ClientApi` is the same `makeDefaultApi` assembly with placeholder middleware identities (`packages/protocol/src/client.ts:28-34`). A `--check` mode fails CI when the file is stale (`generate-openapi.ts:13-18`).
- **Live copy.** The running server also serves the document at `GET /openapi.json` (`routes.ts:185`).
- **Generated clients.** `packages/client/src/{promise,effect}/generated/*` are generated from `ClientApi` by `@opencode/httpapi-codegen` (`packages/client/script/generate.ts:54-65`). Contributors must regenerate after any protocol change and never hand-edit (`AGENTS.md:1`). The promise client omits the websocket and legacy endpoints `pty.connect`, `persistentPty.connect`, `mcp.client` and `generate.textLegacy` (`packages/protocol/src/client.ts:77-91`).
- **Gap: event payloads are opaque in OpenAPI.** The SSE response schema declares `data` as `V2EventEncoded`, which is only `{"type":"string","contentMediaType":"application/json"}`. The 94 event type names appear only as the `types` query enum.
- **Where full event payload types live.**
  - TypeScript: `packages/client/src/promise/generated/types.ts` (for example `SessionTextDelta`).
  - Source of truth: Effect schemas, assembled from `packages/schema/src/event-manifest.ts:40-83` (`ServerDefinitions`).
  - A Go port needs those event shapes from `types.ts` or the schema sources, or by exporting JSON Schema from the Effect schemas (not done here; it would require running Bun).
- **`specs/v2/*.md`** are design contracts and decision records, explicitly "not API reference" (`specs/v2/README.md`).
- **`sdks/`** contains only `vscode`. `packages/sdk` composes Client + Core + Server for embedding (`AGENTS.md:2`).

### 1.4 Routes, and which the TUI calls

**How the TUI column was produced.** Every generated promise-client method was mapped to its route. Call sites were then searched in `packages/tui/src`, `packages/client/src/solid` (the TUI's data layer), `packages/client/src/pty-handoff.ts` and `packages/client/src/mcp.ts`. The site shown is the first match.

**Limits of the TUI column:**
- `packages/tui/src/mini/*` is the inline "mini" front end in the same package.
- A route marked "no" might still be reached through dynamic dispatch, or through third-party TUI plugins, which receive the full client (`props.context.client`).

**Request/response shapes.** These are in `openapi.json` and `packages/protocol/src/groups/*.ts`. The core session payloads are in §1.6.

| Method | Path | operationId | TUI uses |
|---|---|---|---|
| GET | `/api/info` | server.info | **yes** packages/tui/src/context/session-terminals.tsx:69 |
| POST | `/api/pair` | server.pair | **yes** packages/tui/src/component/dialog-pair.tsx:22 |
| GET | `/auth/connect/{code}` | server.connect | no (browser pairing link) |
| GET | `/api/location` | location.get | **yes** packages/tui/src/app.tsx:229 |
| POST | `/api/location/reload` | location.reload | **yes** packages/tui/src/app.tsx:1110 |
| GET | `/api/agent` | agent.list | **yes** packages/client/src/solid/data.ts:2048 |
| GET | `/api/agent/{agentID}` | agent.get | no |
| GET | `/api/plugin` | plugin.list | **yes** packages/tui/src/feature-plugins/system/plugins.tsx:36 |
| GET | `/api/plugin/gui` | plugin.gui | no |
| POST | `/api/plugin/check` | plugin.check | **yes** packages/tui/src/feature-plugins/system/plugins.tsx:168 |
| POST | `/api/plugin/update` | plugin.update | **yes** packages/tui/src/feature-plugins/system/plugins.tsx:146 |
| GET | `/api/session` | session.list | **yes** packages/client/src/solid/data.ts:560 |
| POST | `/api/session` | session.create | **yes** packages/client/src/solid/data.ts:1625 |
| GET | `/api/session/active` | session.active | **yes** packages/client/src/solid/data.ts:671 |
| GET | `/api/session/{sessionID}` | session.get | **yes** packages/tui/src/mini/stream-v2.subagent.ts:588 |
| DELETE | `/api/session/{sessionID}` | session.remove | **yes** packages/tui/src/component/dialog-session-list.tsx:267 |
| POST | `/api/session/{sessionID}/fork` | session.fork | **yes** packages/tui/src/app.tsx:708 |
| POST | `/api/session/{sessionID}/agent` | session.switchAgent | **yes** packages/tui/src/mini/stream-v2.transport.ts:1817 |
| POST | `/api/session/{sessionID}/model` | session.switchModel | **yes** packages/tui/src/mini/stream-v2.transport.ts:1822 |
| PATCH | `/api/session/{sessionID}` | session.update | **yes** packages/tui/src/context/data.tsx:39 |
| POST | `/api/session/{sessionID}/move` | session.move | **yes** packages/tui/src/component/dialog-workspaces.tsx:318 |
| POST | `/api/session/{sessionID}/prompt` | session.prompt | **yes** packages/client/src/solid/data.ts:1723 |
| POST | `/api/session/{sessionID}/command` | session.command | **yes** packages/tui/src/feature-plugins/prompt/goal.tsx:111 |
| POST | `/api/session/{sessionID}/synthetic` | session.synthetic | **yes** packages/tui/src/component/prompt/index.tsx:1351 |
| POST | `/api/session/{sessionID}/shell` | session.shell | **yes** packages/tui/src/mini/stream-v2.transport.ts:1602 |
| POST | `/api/session/{sessionID}/compact` | session.compact | **yes** packages/tui/src/mini/runtime.ts:927 |
| POST | `/api/session/{sessionID}/revert/stage` | session.revert.stage | **yes** packages/tui/src/routes/session/index.tsx:1128 |
| DELETE | `/api/session/{sessionID}/revert` | session.revert.clear | **yes** packages/tui/src/routes/session/index.tsx:1146 |
| POST | `/api/session/{sessionID}/revert/commit` | session.revert.commit | **yes** packages/tui/src/component/prompt/index.tsx:1345 |
| GET | `/api/session/{sessionID}/context` | session.context | no |
| GET | `/api/session/{sessionID}/diff` | session.diff | **yes** packages/tui/src/feature-plugins/system/diff-viewer.tsx:151 |
| GET | `/api/session/{sessionID}/inbox` | session.inbox.list | **yes** packages/tui/src/mini/stream-v2.subagent.ts:410 |
| DELETE | `/api/session/{sessionID}/inbox/{inboxID}` | session.inbox.cancel | **yes** packages/tui/src/mini/runtime.ts:398 |
| PATCH | `/api/session/{sessionID}/inbox/{inboxID}` | session.inbox.update | **yes** packages/tui/src/mini/runtime.ts:401 |
| POST | `/api/session/{sessionID}/generate` | session.generate | **yes** packages/tui/src/feature-plugins/prompt/btw.tsx:68 |
| POST | `/api/session/{sessionID}/interrupt` | session.interrupt | **yes** packages/tui/src/routes/session/index.tsx:1126 |
| POST | `/api/session/{sessionID}/background` | session.background | **yes** packages/tui/src/routes/session/index.tsx:1387 |
| GET | `/api/session/{sessionID}/message/{messageID}` | session.message.get | **yes** packages/tui/src/mini/stream-v2.subagent.ts:472 |
| GET | `/api/session/{sessionID}/message` | session.message.list | **yes** packages/tui/src/mini/stream-v2.subagent.ts:409 |
| GET | `/api/session/{sessionID}/form` | session.form.list | **yes** packages/tui/src/mini/stream-v2.subagent.ts:544 |
| POST | `/api/session/{sessionID}/form` | session.form.create | no |
| GET | `/api/session/{sessionID}/form/{formID}` | session.form.get | no |
| POST | `/api/session/{sessionID}/form/{formID}/reply` | session.form.reply | **yes** packages/tui/src/mini/runtime.ts:278 |
| DELETE | `/api/session/{sessionID}/form/{formID}` | session.form.cancel | **yes** packages/tui/src/mini/runtime.ts:287 |
| PUT | `/api/session/{sessionID}/environment` | session.environment | **yes** packages/tui/src/app.tsx:536 |
| POST | `/api/session/{sessionID}/view` | session.view | **yes** packages/tui/src/context/session-tabs.tsx:245 |
| GET | `/api/form` | form.list | **yes** packages/tui/src/mini/stream-v2.transport.ts:980 |
| GET | `/api/permission/request` | permission.request.list | no |
| GET | `/api/permission/saved` | permission.saved.list | **yes** packages/client/src/solid/data.ts:1967 |
| PATCH | `/api/permission/saved/{id}` | permission.saved.update | no |
| DELETE | `/api/permission/saved/{id}` | permission.saved.remove | no |
| POST | `/api/session/{sessionID}/permission` | session.permission.create | no |
| GET | `/api/session/{sessionID}/permission` | session.permission.list | **yes** packages/tui/src/mini/stream-v2.subagent.ts:536 |
| GET | `/api/session/{sessionID}/permission/{requestID}` | session.permission.get | no |
| POST | `/api/session/{sessionID}/permission/{requestID}/reply` | session.permission.reply | **yes** packages/tui/src/mini/runtime.ts:273 |
| GET | `/api/event` | event.subscribe | **yes** packages/client/src/solid/connection.ts:97 |
| GET | `/api/model` | model.list | **yes** packages/tui/src/mini/catalog.shared.ts:108 |
| GET | `/api/model/default` | model.default | **yes** packages/tui/src/mini/runtime.ts:657 |
| GET | `/api/provider` | provider.list | **yes** packages/tui/src/mini/catalog.shared.ts:107 |
| GET | `/api/provider/{providerID}` | provider.get | no |
| GET | `/api/integration` | integration.list | **yes** packages/client/src/solid/data.ts:2054 |
| GET | `/api/integration/{integrationID}` | integration.get | no |
| POST | `/api/integration/{integrationID}/connect/key` | integration.connect.key | **yes** packages/tui/src/component/dialog-integration.tsx:567 |
| POST | `/api/integration/{integrationID}/connect/external` | integration.connect.external | **yes** packages/tui/src/component/dialog-integration.tsx:371 |
| POST | `/api/integration/{integrationID}/connect/oauth` | integration.oauth.connect | **yes** packages/tui/src/component/dialog-go-upsell.tsx:332 |
| GET | `/api/integration/{integrationID}/connect/oauth/{attemptID}` | integration.oauth.status | **yes** packages/tui/src/component/dialog-go-upsell.tsx:285 |
| POST | `/api/integration/{integrationID}/connect/oauth/{attemptID}/complete` | integration.oauth.complete | **yes** packages/tui/src/component/dialog-integration.tsx:779 |
| DELETE | `/api/integration/{integrationID}/connect/oauth/{attemptID}` | integration.oauth.cancel | **yes** packages/tui/src/component/dialog-go-upsell.tsx:336 |
| POST | `/api/integration/{integrationID}/connect/command` | integration.command.connect | **yes** packages/tui/src/component/dialog-integration.tsx:422 |
| GET | `/api/integration/{integrationID}/connect/command/{attemptID}` | integration.command.status | **yes** packages/tui/src/component/dialog-integration.tsx:477 |
| DELETE | `/api/integration/{integrationID}/connect/command/{attemptID}` | integration.command.cancel | **yes** packages/tui/src/component/dialog-integration.tsx:430 |
| GET | `/api/credential` | credential.list | no |
| POST | `/api/credential` | credential.create | no |
| PATCH | `/api/credential/{credentialID}` | credential.update | **yes** packages/tui/src/component/dialog-integration.tsx:253 |
| POST | `/api/credential/{credentialID}/activate` | credential.activate | **yes** packages/tui/src/component/dialog-integration.tsx:231 |
| DELETE | `/api/credential/{credentialID}` | credential.remove | **yes** packages/tui/src/component/dialog-integration.tsx:270 |
| GET | `/api/mcp` | mcp.list | **yes** packages/client/src/solid/data.ts:2056 |
| GET | `/api/mcp/resource` | mcp.resource.catalog | **yes** packages/client/src/solid/data.ts:2058 |
| GET | `/api/project` | project.list | **yes** packages/client/src/solid/data.ts:1954 |
| PATCH | `/api/project/{projectID}` | project.update | no |
| GET | `/api/fs/read/*` | fs.read | **yes** packages/tui/src/feature-plugins/system/diff-viewer.tsx:206 |
| GET | `/api/fs/list` | fs.list | **yes** packages/tui/src/app.tsx:227 |
| GET | `/api/fs/find` | fs.find | **yes** packages/tui/src/mini/runtime.ts:234 |
| GET | `/api/command` | command.list | **yes** packages/tui/src/mini/catalog.shared.ts:88 |
| GET | `/api/skill` | skill.list | **yes** packages/client/src/solid/data.ts:2076 |
| GET | `/api/reference` | reference.list | **yes** packages/tui/src/mini/catalog.shared.ts:97 |
| GET | `/api/config` | config.get | **yes** packages/client/src/solid/data.ts:2052 |
| GET | `/api/config/shell` | config.shells | no |
| GET | `/api/shell` | shell.list | **yes** packages/client/src/solid/data.ts:1447 |
| POST | `/api/shell` | shell.create | no |
| GET | `/api/shell/{id}` | shell.get | **yes** packages/tui/src/component/dialog-shell-output.tsx:41 |
| GET | `/api/shell/{id}/output` | shell.output | **yes** packages/tui/src/component/dialog-shell-output.tsx:46 |
| DELETE | `/api/shell/{id}` | shell.remove | **yes** packages/tui/src/routes/session/composer/shell-tab.tsx:104 |
| GET | `/api/pty` | pty.list | no |
| POST | `/api/pty` | pty.create | no |
| GET | `/api/pty/{ptyID}` | pty.get | no |
| PUT | `/api/pty/{ptyID}` | pty.update | no |
| DELETE | `/api/pty/{ptyID}` | pty.remove | no |
| POST | `/api/pty/{ptyID}/connect-token` | pty.connect.token | **yes** packages/client/src/solid/pty.ts:24 |
| GET (WS) | `/api/pty/{ptyID}/connect?ticket=` | pty.connect | **yes** packages/client/src/solid/pty.ts:31-37 |
| GET | `/api/worktree` | worktree.list | **yes** packages/tui/src/component/dialog-open.tsx:122 |
| POST | `/api/worktree` | worktree.create | **yes** packages/tui/src/component/dialog-open.tsx:462 |
| DELETE | `/api/worktree` | worktree.remove | **yes** packages/tui/src/component/dialog-workspaces.tsx:244 |
| POST | `/api/worktree/refresh` | worktree.refresh | **yes** packages/tui/src/component/dialog-open.tsx:133 |
| POST | `/api/vcs/init` | vcs.init | no |
| GET | `/api/vcs` | vcs.get | **yes** packages/client/src/solid/data.ts:1444 |
| GET | `/api/vcs/base` | vcs.base | **yes** packages/tui/src/feature-plugins/system/diff-viewer.tsx:127 |
| GET | `/api/vcs/status` | vcs.status | **yes** packages/tui/src/component/dialog-workspaces.tsx:257 |
| GET | `/api/vcs/branch` | vcs.branch.list | **yes** packages/tui/src/feature-plugins/system/diff-viewer.tsx:268 |
| GET | `/api/vcs/diff` | vcs.diff | **yes** packages/tui/src/feature-plugins/system/diff-viewer.tsx:163 |
| GET | `/api/websearch/provider` | websearch.providers | **yes** packages/client/src/solid/data.ts:2071 |
| POST | `/api/websearch` | websearch.query | no |
| POST | `/api/rpc/{rpcID}/{method}` | rpc.call | no (plugin RPC) |
| POST | `/api/debug/heap-dump` | debug.heapDump | no |
| GET | `/api/debug/location` | debug.location.list | no |
| DELETE | `/api/debug/location` | debug.location.evict | no |
| GET | `/api/experimental/session/stats` | experimental.session.stats | **yes** packages/tui/src/feature-plugins/system/stats.tsx:143 |
| POST | `/api/experimental/session/import` | experimental.session.import | no |
| GET | `/api/experimental/session/{sessionID}/export` | experimental.session.export | **yes** packages/tui/src/routes/session/index.tsx:1327 |
| POST | `/api/experimental/session/{sessionID}/skill` | experimental.session.skill | no |
| POST | `/api/experimental/session/{sessionID}/wait` | experimental.session.wait | **yes** packages/tui/src/mini/stream-v2.transport.ts:914 |
| GET | `/api/experimental/session/{sessionID}/log` | experimental.session.log | **yes** packages/tui/src/component/devtools-bar.tsx:165 (devtools) |
| GET/PUT/DELETE | `/api/experimental/session/{sessionID}/instructions/entries[/{key}]` | experimental.session.instructions.entry.* | no |
| POST | `/api/experimental/generate/text`, `/generate/image`, `/generate/decisions`, `/generate` (legacy) | experimental.generate.* | no |
| GET/POST | `/api/experimental/voice/models`, `/voice/session` | experimental.voice.* | no |
| POST | `/api/experimental/integration/wellknown` | experimental.integration.wellknown.add | no |
| PUT/DELETE | `/api/experimental/mcp/{server}` | experimental.mcp.add/remove | no |
| POST | `/api/experimental/mcp/{server}/connect` | experimental.mcp.connect | **yes** packages/tui/src/component/dialog-mcp.tsx:124 |
| POST | `/api/experimental/mcp/{server}/disconnect` | experimental.mcp.disconnect | **yes** packages/tui/src/component/dialog-mcp.tsx:124 |
| POST | `/api/experimental/client-mcp/token` | experimental.mcp.client.token | no |
| GET (WS) | `/api/experimental/client-mcp` | experimental.mcp.client | **yes, only if `cli.json` `experimental["client-mcp"]`** packages/tui/src/context/client-mcp.tsx:75; packages/tui/src/config/index.tsx:392 |
| POST/DELETE | `/api/experimental/fs/write`, `/fs/watch` | experimental.fs.* | no |
| GET | `/api/experimental/session/{sessionID}/terminal` | experimental.persistentPty.list | **yes** packages/tui/src/context/session-terminals.tsx:37 |
| POST | `/api/experimental/session/{sessionID}/terminal` | experimental.persistentPty.create | **yes** packages/tui/src/context/session-terminals.tsx:104 |
| GET | `/api/experimental/session/{sessionID}/terminal/read` | experimental.persistentPty.read | no |
| POST | `/api/experimental/persistent-pty/shutdown` | experimental.persistentPty.shutdown | **yes** packages/client/src/pty-handoff.ts:37 |
| POST | `/api/experimental/persistent-pty/handoff` | experimental.persistentPty.handoff | **yes** packages/client/src/pty-handoff.ts:29 |
| GET/PUT/DELETE | `/api/experimental/persistent-pty/{ptyID}` | experimental.persistentPty.get/update/remove | no |
| GET | `/api/experimental/persistent-pty/{ptyID}/snapshot` | experimental.persistentPty.snapshot | **yes** packages/tui/src/component/terminal-pane.tsx:180 |
| POST | `/api/experimental/persistent-pty/{ptyID}/connect-token` | experimental.persistentPty.connectToken | **yes** packages/client/src/solid/pty.ts:47 |
| GET (WS) | `/api/experimental/persistent-pty/{ptyID}/connect?ticket=` | experimental.persistentPty.connect | **yes** packages/client/src/solid/pty.ts:53-61 |
| GET | `/api/experimental/media/{hash}` | experimental.media.get | no |
| various | `/api/experimental/schedule[/{scheduleID}[/pause\|/resume]]` | experimental.schedule.* | no |
| GET | `/api/experimental/migration/v1` | experimental.migration.v1.status | **yes** packages/tui/src/component/migration-overlay.tsx:22 |
| GET | `/api/experimental/usage/limits` | experimental.usage.limits | **yes** packages/client/src/solid/data.ts:1445 |
| PATCH | `/api/experimental/config` | experimental.config.update | no |
| GET | `/openapi.json` | (served by HttpApiBuilder) | no |

**Coverage.** About 105 of 162 operations have a TUI call site. Experimental routes carry no
compatibility promise and are namespaced `client.experimental.*` in generated clients
(`packages/protocol/src/client.ts:71-75`).

**Client-MCP websocket direction.** On `/api/experimental/client-mcp` the *server* is the MCP
client and the TUI answers MCP JSON-RPC, exposing `open_terminal` and `write_terminal` tools
(`packages/client/src/mcp.ts:59-100`; `packages/tui/src/context/client-mcp.tsx:75-105`).

### 1.5 Event channel (SSE)

**Endpoint and framing**
- `GET /api/event[?types=a&types=b]`, `Content-Type: text/event-stream` (`packages/protocol/src/groups/event.ts:38-63`; `packages/server/src/handlers/event.ts:11-34`).
- Each frame is `data: <JSON event>\n\n`, with **no `event:` or `id:` lines** (`packages/server/src/event-feed.ts:29-31`).
- The first frame is always `{"id":"evt_…","type":"server.connected","data":{}}` (`handlers/event.ts:13-21`).
- A `: heartbeat\n\n` comment is sent every 15 s (`handlers/event.ts:23`).
- Response headers: `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no` (`handlers/event.ts:27-32`).
- `types` filters events; `server.connected` is always sent (`protocol/src/groups/event.ts:49-52`).
- **Scope:** one stream for all locations, not per project (`protocol/src/groups/event.ts:69`).

**Delivery is volatile**
- Each subscriber has a dropping queue of 4096 frames. Overflow **fails that stream** (`EventFeed.SubscriberOverflow`) rather than dropping silently (`event-feed.ts:8,66-68,77-83`).
- There is no replay of events missed while disconnected (`protocol/src/groups/event.ts:69`; `specs/v2/event-stream-architecture.md`).

**Client behaviour** (`packages/client/src/solid/connection.ts`)
- Requires `server.connected` as the first event (`:106-107`) and reconnects after 1 s (`:33`).
- Has an idle watchdog that relies on the heartbeat (`:21-24,43`).
- On every `server.connected` it re-hydrates via `GET /api/session/active` and re-syncs running sessions (`packages/client/src/solid/data.ts:666-690`).

**Envelope** (`packages/schema/src/event.ts:9-13,34,67-78`):

```
{ id: "evt_<ascending id>", type, created: <epoch ms>, data, location?: {directory,...},
  metadata?: {...}, durable?: { aggregateID, seq, version } }   // durable only on durable events
```

Plugin RPC events are typed `rpc.<name>` with required `location` (`protocol/src/groups/event.ts:14-21`).

**Event types.** There are 94 public types, from the `types` enum in `openapi.json`. Data shapes come
from `packages/client/src/promise/generated/types.ts`.

- **D/E:** durable (persisted to the session projection, carries `durable`) or ephemeral.
- **TUI column:** "✓" means a handler was found in `packages/client/src/solid/data.ts` (`handleEvent`, `:664`) or via `client.event.on(...)` in `packages/tui/src`. "–" means no consumer was found by search.

| type | D/E | data | TUI |
|---|---|---|---|
| server.connected | E | `{}` | ✓ |
| location.shutdown | E | `{}` | – |
| models-dev.refreshed, credential.updated | E | `{}` | – |
| credential.switched | E | `{integrationID, credentialID\|null}` | – |
| integration.updated, provider.updated, model.updated, agent.updated, plugin.updated, command.updated, config.updated, skill.updated, reference.updated, websearch.updated | E | `{}` (refetch signals) | ✓ |
| session.created | D | `{sessionID, projectID, location, subpath?, parentID?, slug, title?, agent?, model?, metadata?, permissions?, …}` | ✓ |
| session.agent.selected | D | `{sessionID, agent, previous?}` | ✓ |
| session.model.selected | D | `{sessionID, model:{id,providerID,variant?}, previous?}` | ✓ |
| session.moved | D | `{sessionID, location, projectID, subpath?}` | ✓ |
| session.renamed | D | `{sessionID, title}` | ✓ |
| session.metadata.updated | D | `{sessionID, metadata}` | ✓ |
| session.permissions | D | `{sessionID, permissions: Rule[]}` | ✓ |
| session.viewed | D | `{sessionID, idle}` | ✓ |
| session.archived / unarchived / deleted | D | `{sessionID}` | ✓ |
| session.forked | D | `{sessionID, parentID, boundary, instructions?, instructionEntries?}` | – |
| session.usage.updated | E | `{sessionID, cost, tokens}` | ✓ |
| session.inbox.enqueued | D | `{sessionID, inboxID, item}` | ✓ |
| session.inbox.delivered / cancelled | D | `{sessionID, inboxID}` | ✓ |
| session.inbox.delivery.changed | D | `{sessionID, inboxID, delivery:"steer"\|"queue"}` | ✓ |
| session.execution.started / succeeded | D | `{sessionID}` | ✓ |
| session.execution.failed | D | `{sessionID, error}` | ✓ |
| session.execution.interrupted | D | `{sessionID, reason:"user"\|"shutdown"\|"superseded"\|"inactivity"}` | ✓ |
| session.instructions.updated | D | `{sessionID, delta:{key: hash\|"removed"}, text?}` | ✓ |
| session.synthetic | D | `{sessionID, text, description?, metadata?}` | ✓ |
| session.skill.activated | D | `{sessionID, id, name, text}` | (not in handler list; *unverified*) |
| session.shell.started / ended | D | `{sessionID, shell}` / `{…, output:{output,cursor,size,truncated}}` | ✓ |
| session.step.started | D | `{sessionID, assistantMessageID, agent, model, snapshot?, started}` | ✓ |
| session.step.streamed | D | `{sessionID, assistantMessageID}` | ✓ |
| session.step.ended | D | `{…, finish, rawFinish?, native?, cost, tokens, …}` | ✓ |
| session.step.failed | D | `{…, error, finish?, cost?, tokens?, snapshot?, …}` | ✓ |
| session.text.started / reasoning.started | D | `{sessionID, assistantMessageID, ordinal, native?}` | ✓ |
| session.text.delta / reasoning.delta | E | `{sessionID, assistantMessageID, ordinal, delta}` | ✓ |
| session.text.ended / reasoning.ended | D | `{…, ordinal, text, native?}` | ✓ |
| session.tool.input.started | D | `{sessionID, assistantMessageID, id, name}` | ✓ |
| session.tool.input.delta | E | `{…, id, delta}` | ✓ |
| session.tool.input.ended | D | `{…, id, text}` | ✓ |
| session.tool.called | D | `{…, id, input, executed, native?}` | ✓ |
| session.tool.progress | E | `{…, id, metadata}` | ✓ |
| session.tool.success | D | `{…, id, content:[ToolContent,…], metadata?, executed, native?}` | ✓ |
| session.tool.failed | D | `{…, id, error, content?, metadata?, executed, …}` | ✓ |
| session.retry.scheduled | D | `{…, attempt, at, error}` | ✓ |
| session.compaction.started | D | `{sessionID, reason:"auto"\|"manual", inputID?}` | ✓ |
| session.compaction.delta | E | `{sessionID, text}` | ✓ |
| session.compaction.ended | D | `{sessionID, reason, model?, native?, text, cost?, tokens?}` | ✓ |
| session.compaction.failed | D | `{sessionID, reason, error, inputID?, cost?, tokens?}` | ✓ |
| session.revert.staged / cleared / committed | D | `{sessionID, revert}` / `{sessionID}` / `{sessionID, to}` | ✓ |
| session.status | E | `{sessionID, status}` | – |
| session.idle | E | `{sessionID}` | – |
| permission.asked | E | `{id, sessionID, action, resources[], save?[], metadata?, source?, message?}` | ✓ |
| permission.replied | E | `{sessionID, requestID, reply:"once"\|"always"\|"reject"}` | ✓ |
| form.created / replied / cancelled | E | `{form}` / `{id, sessionID, answer}` / `{id, sessionID}` | ✓ |
| filesystem.changed | E | `{file, event:"add"\|"change"\|"unlink"}` | – |
| project.updated | E | `{id, canonical, vcs?, name?, icon?, commands?, time, sandboxes}` | ✓ |
| worktree.updated | E | `{projectID}` | ✓ |
| worktree.resolved | D | `{projectID, directory, previous, adopted?}` | ✓ |
| pty.created / updated / exited / deleted | E | `{info}` / `{info}` / `{id, exitCode}` / `{id}` | – |
| persistent-pty.added / removed | E | `{sessionID, terminal}` / `{sessionID, ptyID}` | – |
| shell.created / exited / deleted | E | `{info}` / `{id, exit?, status}` / `{id}` | ✓ |
| tui.prompt.append | E | `{text}` | ✓ |
| tui.command.execute | E | `{command: "session.list"\|"session.new"\|…}` | ✓ |
| tui.toast.show | E | `{title?, message, variant, duration?}` | ✓ |
| tui.session.select | E | `{sessionID}` | ✓ |
| installation.updated / update-available | E | `{version}` | ✓ |
| vcs.branch.updated | E | `{branch?}` | ✓ |
| mcp.status.changed / resources.changed | E | `{server}` | ✓ |

### 1.6 Core session request shapes (`packages/protocol/src/groups/session.ts`)

| Operation | Request | Response |
|---|---|---|
| `POST /api/session` (`:165-186`) | `{id?, parentID?, title?, agent?, model?:{id,providerID,variant?}, location?:{directory}, metadata?, permissions?}`. `parentID` creates a linked child at the parent's location. | `{data: Session.Info}` |
| `POST /api/session/{id}/prompt` (`:337-357`) | `{id?, text, files?, agents?, skills?, metadata?, delivery?:"steer"\|"queue", resume?}` (`packages/schema/src/prompt-input.ts:25-30`; `packages/schema/src/session-inbox.ts:11`). Durable admission, then execution is scheduled unless `resume:false`. **No per-prompt agent or model field**: those are session state, changed by `POST …/agent {agent}` and `POST …/model {model}` (`:273-299`). | `{data: SessionInbox.User}` |
| `POST /api/session/{id}/command` (`:359-377`) | `{name, text, files?, agents?, skills?, delivery?}` | 204 |
| `GET /api/session` (`:112-143`) | query `limit?` (default 50), list filter fields (e.g. `parentID`), `cursor?` | `{data, cursor:{previous?, next?}}` |
| `GET /api/session/{id}/message` (`packages/protocol/src/groups/message.ts:23-42`) | query `limit` (1–200), `order` asc\|desc, opaque `cursor`, `type` filter | `{data: Message[], cursor}` |
| `POST /api/session/{id}/permission/{requestID}/reply` (`packages/protocol/src/groups/permission.ts:142-157`) | `{decision:"once"\|"always"\|"reject", message?}` | 204 |
| `POST /api/session/{id}/form/{formID}/reply` (`session.ts:768-781`) | `{answer}` | 204, 409 if already settled, 400 if invalid |

Message types in `message.list` are `agent-switched, model-switched, location-switched, user,
synthetic, system, skill, shell, assistant, compaction, idle` (`message.ts:9-21,54-66`).

---

## 2. Agent control

### 2.1 Agent definitions

**Shape** (`Agent.Info`, `packages/schema/src/agent.ts:23-35`):

| Field | Type |
|---|---|
| `id` | string |
| `name` | string |
| `model?` | Ref |
| `request` | `{settings, headers, body}` |
| `system?` | string |
| `description?` | string |
| `mode` | `"subagent"\|"primary"\|"all"` |
| `hidden` | bool |
| `color?` | string |
| `steps?` | positive int |
| `permissions` | Rule[] |

**Defaults** (`agent.ts:39-52`, spot-checked): mode `primary`, not hidden, permissions
`[{*,*,allow},{read,*.env,ask},{read,*.env.*,ask},{read,*.env.example,allow}]`.

**Built-ins** (`packages/core/src/plugin/agent.ts:55-116`; `packages/core/src/plugin/plan.ts:33-42`):

| id | mode | prompt | extra rules |
|---|---|---|---|
| build | primary | provider default | question allow |
| plan | primary | provider default | question allow; edit deny except `~/.opencode/plan/*` |
| general | subagent | provider default | question deny, subagent deny |
| explore | subagent | `PROMPT_EXPLORE` | `*` deny, then allow shell/grep/glob/webfetch/websearch/read; subagent deny |
| compaction | primary, hidden | — | — |
| title | primary, hidden | `PROMPT_TITLE` | `*` deny |
| summary | primary, hidden | `PROMPT_SUMMARY` | `*` deny |

**Prompt and model**
- `agent.system` replaces the default system prompt (`packages/core/src/session/model-request.ts:109-113`).
- Model precedence: session model → agent model → resolver default (`packages/core/src/session/runner/model.ts:77-99`).
- When `steps` is reached, the run appends a max-steps prompt with `toolChoice:"none"` (`packages/core/src/session/runner/llm.ts:249-268`).

**Selection is session state**
- Set via `session.create {agent}` or `POST /api/session/{id}/agent`. That publishes durable `session.agent.selected` with **no validation of existence or mode** (`packages/core/src/session/session.ts:99-105`).
- Fallback when unset: `default_agent` if selectable, else `build`, else the first selectable agent (`packages/core/src/agent.ts:53-64`).
- `GET /api/agent` lists the default agent first (`agent.ts:65-70`).

### 2.2 Modes and TUI listing

| Mode / flag | Rule |
|---|---|
| `primary` | Cannot be dispatched by the subagent tool (`packages/core/src/tool/plugin/subagent.ts:142-143`, spot-checked) |
| `subagent` | Cannot be the session default |
| `all` | Both |
| `hidden` | Excluded from the default fallback and from the subagent list given to the model |

**TUI behaviour**
- Shift+Tab cycles agents filtered by `mode !== "subagent" && !hidden` (`packages/tui/src/context/local.tsx:59-61`).
- The selection is committed with `switchAgent` on submit (`packages/tui/src/component/prompt/index.tsx:1281-1289`).
- `@`-autocomplete lists `!hidden && mode !== "primary"` (`packages/tui/src/component/prompt/autocomplete.tsx:408-409`).
- `@agent` mentions only land in message metadata; there is no server-side dispatch from mentions (`packages/core/src/session/runner/to-llm-message.ts:270`).

### 2.3 Subagent dispatch: tool `subagent` (`packages/core/src/tool/plugin/subagent.ts`)

**Input:** `{agent, description, prompt, model?:"provider/model[#variant]", sessionID?, background?}` (`:30-48`).

**Checks, in order:**
1. Ancestor depth must be below `experimental.subagent_depth` (default 1), so by default a child cannot spawn (`:123-139`, spot-checked).
2. The agent must exist and must not be `primary` (`:140-143`).
3. Permission `{action:"subagent", resources:[agentID], save:[agentID]}` (`:144-157`).
4. A `sessionID` continuation must name a direct child (`:170-173`).

**Child creation**
- `sessions.create({parentID, title: description, agent, model: arg ?? agent.model ?? parent.model})`. The child inherits location, metadata and permissions (`:190-204`; `packages/core/src/session.ts:292-314`).
- The child's first prompt is prefixed `You are a subagent spawned by another session.\n` (`:211-224`).

**Result**
- **Foreground:** blocks. The model content is `<subagent sessionID="…" state="completed">…</subagent>`, where the text is the last completed assistant text parts (`:240-271`; `packages/core/src/session/subagent-job.ts:53-61`).
- **Background:** returns `status:"running"`. Completion arrives later as a synthetic `<subagent …>` message in the parent with `metadata.source:"subagent"`, which wakes the parent (`packages/core/src/session/subagent-completion.ts:21-46`).

**Concurrency and cancellation**
- Every tool call in a step runs in its own fiber, so parallel subagents run concurrently (`packages/core/src/session/runner/step.ts:123-134`).
- Interrupting the parent tool call interrupts the child (`subagent.ts:241-245`).
- `POST …/background` moves blocking jobs to the background (`packages/core/src/session.ts:508-525`).
- Deleting a session cascades to its children (`session.ts:393-397`).

**Commands** with `subagent`/`subtask`, or whose agent is a subagent, always dispatch a background
child (`packages/core/src/config/plugin/command.ts:71-116`).

### 2.4 Permission model

**Rule matching**
- A rule is `{action, resource, effect: allow|deny|ask}` (`packages/schema/src/permission.ts:59-66`).
- **Last matching rule wins; no match means `ask`** (`packages/core/src/permission.ts:86-96`, spot-checked).
- `*` and `?` are wildcards; a trailing `" *"` also matches the bare command (`packages/core/src/util/wildcard.ts:3-13`).

**Action keys:**
- Built-in: `read`, `edit` (shared by edit/write/patch), `shell`, `glob`, `grep`, `webfetch`, `websearch`, `question`, `todowrite`, `skill`, `subagent`, `external_directory`, `experimental_image_generate`, `execute` (Code Mode).
- MCP tools: `<mcpserver>_<tool>`.
- Legacy V1 names are migrated: `bash→shell`, `task→subagent`, `write/patch→edit` (`packages/core/src/v1/config/migrate.ts:27-32`).

**Evaluation** (`packages/core/src/permission.ts:159-200`):
1. The rule list is agent rules followed by session rules. Global config `permissions` are appended to every agent.
2. If any resource evaluates to deny, the request is denied.
3. Otherwise saved project "always" allows are applied, and any remaining `ask` makes the request ask.
4. A plugin `permission.evaluate` hook may override the result. Managed policy uses it (`packages/core/src/config/plugin/policy.ts:49-63`).
5. In unattended contexts, an unanswered `ask` resolves to deny.

**Round trip through the TUI:**
1. The tool asks, and the server emits ephemeral `permission.asked` `{id:"per_…", sessionID, action, resources[], save?[], metadata?, source?:{type:"tool", messageID, id}, message?}` (`packages/schema/src/permission.ts:26-45`).
2. The TUI shows "always" only when `save` is non-empty (`packages/tui/src/routes/session/permission.tsx:232-240`). It aggregates requests across the session family (`packages/tui/src/routes/session/index.tsx:204-209`). In auto-accept mode it replies `once` automatically (`index.tsx:265-280`).
3. The reply is `POST /api/session/{sid}/permission/{rid}/reply {decision, message?}` → 204, or 404 if the request is not owned by that session (`packages/protocol/src/groups/permission.ts:142-157`). The server then emits `permission.replied`.
4. Outcomes:
   - **`reject` without a message:** rejects all pending requests in the session. The tool fails as "declined" and the run is interrupted (`packages/core/src/permission.ts:302-322`; `step.ts:207-225`).
   - **`reject` with a message:** the feedback becomes a tool failure and the model continues. **At v2.2.2, MCP tools hide this:** the model only sees `Unable to execute <server>_<tool>` (`packages/core/src/tool/mcp.ts:108-112`).
   - **`always`:** persists the `save` patterns as project-scoped allows in SQLite, then auto-approves other pending requests that now pass (`permission.ts:325-348`; `packages/core/src/permission/saved.ts:69-84`). These are managed via `GET/PATCH/DELETE /api/permission/saved…`.

**Question tool / forms** (`packages/core/src/tool/plugin/question.ts`):
- Tool input is `{questions:[{question, header, options:[{label, description}], multiple?}]}`. The server creates a form (event `form.created {form}`) with fields `q0…`.
- The TUI answers via `POST …/form/{formID}/reply {answer:{q0:"…"|["…"]}}` or cancels via `DELETE …/form/{formID}?message=`.
- A cancel without a message ends the run; a cancel with a message lets the model continue.
- Websearch provider choice also uses forms (`packages/core/src/websearch.ts`).

### 2.5 Tool registry

**Interface** (`packages/schema/src/tool.ts:14-102`):
- Definition: `{name, description, input, output?, options?:{namespace?, permission?, codemode?, pinned?}, execute(input, ctx)}`.
- Context: `{sessionID, agent, messageID, id, progress}`.
- Result: `{output?, content?: string | [{type:"text",text}|{type:"file",uri,mime,name?}], metadata?}`.
- The registered name is `<namespace>_<name>`; `execute` is reserved (`packages/core/src/tool.ts:338-355`).

**Built-in tools:**

| Tool | Notes |
|---|---|
| `read` | |
| `edit` | |
| `write` | |
| `patch` | `gpt-*` models only; edit/write hidden then |
| `glob` | |
| `grep` | |
| `shell` | `command, workdir, timeout, background` |
| `webfetch` | |
| `websearch` | |
| `question` | |
| `todowrite` | only when `experimental.todo` is true; todos live in tool metadata only |
| `skill` | |
| `subagent` | |
| `experimental_image_generate` | |
| `execute` | Code Mode: a server-side JavaScript sandbox through which further `opencode_*` tools, browser tools and, by default, MCP tools are reached |

**Filtering**
- A tool is hidden from an agent when the last rule matching its action is `{resource:"*", effect:"deny"}` (`packages/core/src/tool.ts:249-256`).
- If `execute` is wholly denied, Code-Mode-only tools are exposed directly instead (`tool.ts:258-263`).
- Plugins can transform the tool list and hook `execute.before`/`after` (`packages/plugin/src/effect/tool.ts`).

**Output truncation**
- Limits are 2000 lines or 50 KiB, keeping the head and adding a marker.
- The full output is saved at `<data>/tool-output/tool_*` for 7 days. `metadata.truncated` and `metadata.outputPath` are set (`packages/core/src/tool-output.ts:13-120`).

---

## 3. Config, agent, command, skill and instruction loading

### 3.1 Model

- **Config is an ordered entry list**, lowest priority first. It is never merged into one object (`packages/core/src/config.ts:31-32,306-316`).
- `Config.latest(entries, key)` takes the **whole value** from the last document that sets the key; there is no deep merge (`config.ts:25-28`). Other keys have per-key folds (§3.4).
- Agents, commands, skills and instructions are built by plugin "transforms" replayed in boot order. The internal `post` plugins hold the config-file loaders, so files override built-ins (`packages/core/src/plugin/internal.ts:223-281`).

### 3.2 Locations, lowest to highest priority (`packages/core/src/config/discovery.ts:59-145`; `config.ts:243-317`)

| # | Source |
|---|---|
| 1 | Managed **policies**, split off so they win (`config.ts:273-294`) |
| 2 | Console integration documents, loaded verbatim at v2.2.2 (`config.ts:308`) |
| 3 | `.well-known/opencode` documents (`config` plus `remote_config`), refreshed every 10 min (`config.ts:197,387-405`) |
| 4 | Global dir `opencode.json`, then `opencode.jsonc`, then the dir itself. The global dir is `$OPENCODE_CONFIG_DIR` (which *replaces* the default), else `$XDG_CONFIG_HOME/opencode`, else `~/.config/opencode` (`packages/util/src/global-roots.ts:7,15`; `packages/cli/src/server-process.ts:39`) |
| 5 | `OPENCODE_CONFIG` file (`config.ts:249-254`) |
| 6 | `opencode.json[c]` at **every ancestor** of the location dir up to `/`, farthest first; `.jsonc` beats `.json` (`discovery.ts:77-94,124`) |
| 7 | `.opencode/` at every ancestor: `opencode.json[c]` plus the dir (`discovery.ts:125-128`) |
| 8 | `OPENCODE_CONFIG_CONTENT` inline JSON (`config.ts:255-267`) |
| 9 | Managed settings: `/Library/Application Support/opencode` (macOS) or `/etc/opencode` (Linux), and the macOS MDM plist (`discovery.ts:28-57`) |

- `~/.claude`, `~/.agents` and the ancestor `.claude/` and `.agents/` dirs are read **only for skills** (`discovery.ts:129-143`; `server-process.ts:127-131`).
- Env toggles (`server-process.ts:124-133`): `OPENCODE_DISABLE_PROJECT_CONFIG` disables project discovery, and `OPENCODE_DISABLE_CLAUDE_CODE[_SKILLS]` disables the `.claude` roots.

### 3.3 Parsing

1. **Substitution on raw text** (`packages/core/src/config/variable.ts:26-81`):
   - `{env:VAR}`: a missing variable becomes `""`.
   - `{file:path}`: `~` is expanded; relative paths resolve against the config file; content is trimmed and JSON-escaped.
2. **JSONC parse:** any parse error rejects the document (`config.ts:133-144`).
3. **V1 to V2 normalisation** (`packages/core/src/config/normalize.ts:57-232`) renames `agent→agents`, `command→commands`, `provider→providers`, `permission/tools→permissions`, `small_model→agents.title.model`, `plugin→plugins`, and others. Unknown keys are ignored.
4. **Schema sources.** The published config JSON schema is generated by `packages/schema/script/config-schema.ts`; `packages/core/schema.json` is a drizzle DB snapshot, not the config schema.

**V2 top-level keys** (`packages/schema/src/config.ts:26-117`): `$schema, shell, model, default_agent,
update, share, enterprise, username, timezone, permissions, policies, agents, snapshots, watcher,
formatter, lsp, media, tool_output, mcp, compaction, skills, commands, instructions, references,
websearch, plugins, worktree, warming, providers, experimental`.

`policies` actions at v2.2.2 are only `provider.use`, `tool.use` and `integration.use`. MCP, skill and
plugin gating uses `integration.use` with resources `mcp:<name>`, `skill:<id>` and `plugin:<target>`
(`packages/schema/src/config/policy.ts:8-12`; `packages/core/src/config/plugin/policy.ts:37-48`).

### 3.4 Merge rules per key

| Key | Fold |
|---|---|
| Scalars and `latest` keys (`model, shell, default_agent, experimental, …`) | Last document wins, whole value |
| `agents` | Per-agent field overlay in entry order; `permissions` arrays **appended**. Top-level `permissions` are appended to every agent (`packages/core/src/config/plugin/agent.ts:82-126`) |
| `commands` | Last definition of a name wins |
| `mcp.servers` | Last document per server name |
| `instructions`, `skills` | Union, de-duplicated |
| `plugins` | Concatenated operation list; `"-name"` removes |
| `policies` | Earliest-loaded wins (managed first) (`packages/core/src/managed-policy.ts:9-36`) |

- **Reload:** watched roots are debounced 100 ms, and `config.updated` is published on change (`config.ts:344-368`).
- **Write:** `PATCH /api/experimental/config` accepts only `{shell}` (`config.ts:408-429`).
- **TUI reads:** `GET /api/config` returns the raw entry list; the TUI uses the last document with `model` as its fallback model (`packages/tui/src/context/local.tsx:173-182`).

### 3.5 Agents from files (`packages/core/src/config/plugin/agent.ts`)

**Locations:** under each config dir (global, each `.opencode/`, system), `{agent,agents}/**/*.md` (nested) and
`{mode,modes}/*.md` (forced `primary`) (`:21-26,163-174,212`). There is no `.claude/agents`.

**Name:** the relative path minus the prefix and `.md`, so `agents/review/sec.md` becomes `review/sec` (`:182-186`).

**Format:** YAML frontmatter, and the body becomes `system`.

**Frontmatter (V2):**

| Field | Type / notes |
|---|---|
| `model` | `"p/m#v"` or object |
| `request` | `{headers, body}` |
| `description` | |
| `mode` | |
| `hidden` | |
| `color` | `#RRGGBB` |
| `steps` | |
| `disabled` | removes the agent, built-ins included |
| `permissions` | Rule[]; `~` expanded |

**V1 frontmatter:** `prompt`, `temperature`, `top_p`, `tools`, `permission`, `maxSteps`, `disable`. These are migrated. A file is
treated as V1 when it has non-V2 keys and none of `permissions, request, system, disabled`
(`:31-33,188-209`).

### 3.6 Commands (`packages/core/src/config/plugin/command.ts`)

- **Sources:**
  - Files `{command,commands}/**/*.md`, plus config `commands`.
  - Built-ins: `init`, `review`, MCP prompts registered as `<server>:<prompt>`, and `goal` (`packages/core/src/plugin/command.ts:28-110`).
- **Frontmatter:** `description, agent, model, subagent, subtask` (deprecated alias). The body is the template (`packages/schema/src/config/command.ts:7-14`).
- **Template** (`command.ts:183-245`):
  - `$ARGUMENTS` is the whole input.
  - `$1…$N` are positional; the highest placeholder present swallows the remaining arguments.
  - With no placeholders, the input is appended.
  - `` !`cmd` `` runs in the shell (cwd = location, concurrency 2) and its output is inlined; a failure aborts.
  - There is **no `@file` expansion** server-side; files arrive as attachments.
- **Execution:** `POST /api/session/{id}/command`. Either a subagent child (see §2.3), or switch agent/model if set and then prompt with the given delivery (`command.ts:92-128`).
- **Listing:** `GET /api/command` returns `{name, description}`.

### 3.7 Skills

**Sources, later wins:**
1. Built-ins `opencode` and `report`.
2. `~/.claude/skills`, `~/.agents/skills`, and the ancestor `.claude/skills` and `.agents/skills`.
3. `<configdir>/skill[s]`.
4. Config `skills` paths or URLs. A URL source is fetched as `<url>/index.json` and cached at `~/.cache/opencode/skills/` (`packages/core/src/skill/discovery.ts:48-200`).

Policy can remove skills.

**Scan:**
- Breadth-first to depth 6.
- A directory with `SKILL.md` is one skill.
- Loose `*.md` files at depth 0 are also skills (`packages/core/src/config/plugin/skill-scan.ts:7-46`).

**`SKILL.md` frontmatter:** `name`, `description`, `metadata["opencode/autoinvoke"]`, `disable-model-invocation`. The ID is the
directory name (`packages/core/src/config/plugin/skill-file.ts:10-61`).

**Exposure to the model:**
- **System prompt:** an `<available_skills>` block, including only skills with a description that are not auto-invoke-disabled and not permission-denied (`packages/core/src/skill/instructions.ts:16-97`).
- **Tool:** `skill {id}`, gated by the `skill` permission, returns a `<skill_content>` block (`packages/core/src/tool/plugin/skill.ts:10-71`).
- **User-initiated:** prompt `skills` attachments and `POST /api/experimental/session/{id}/skill`.

### 3.8 Instruction files and system-prompt assembly

**Discovery**
- Only **`AGENTS.md`** is discovered. `CLAUDE.md` and `CONTEXT.md` are not read.
- Global `<config>/AGENTS.md` comes first. Then project `AGENTS.md` files from the location dir upward, nearest first, stopping at home (if under home) or at the project root (`packages/core/src/config/plugin/instruction.ts:36-94`).
- The config `instructions` key takes URLs (5 s fetch, last-good kept), absolute or `~` paths and globs, and relative paths searched upward to the project root (`instruction.ts:98-179`).
- Each is rendered as `Instructions from: <path>\n<content>`.
- After a `read` tool call, newly reached nested `AGENTS.md` files are injected once as synthetic messages (`packages/core/src/tool/plugin/read.ts:82-107`).

**System prompt assembly:**
1. The agent `system`, or `session/runner/prompt/system.txt` with tool guidance filled in (`model-request.ts:99-119`).
2. Model-family prompt plugins: `gpt` replaces, `claude` appends `anthropic.txt`, and so on. These apply only when the agent has no custom `system` (`packages/core/src/plugin/optimize.ts:16-103`).
3. An identity block spliced at index 1 (`packages/core/src/plugin/identity.ts:10-26`).
4. The instruction baseline (`packages/core/src/session/context.ts:152-163`), in this order:
   1. Code-mode catalog
   2. MCP instructions
   3. References
   4. Skill guidance
   5. `AGENTS.md` and configured instructions
   6. Date and `<env>` block (`packages/core/src/instructions/builtins.ts:23-54`)
   7. API-managed instruction entries

Later instruction changes are durable `session.instructions.updated` deltas, replayed as system-role
history messages (`packages/core/src/session/instruction-state.ts:38-61`).

### 3.9 Plugins

- **Discovery:** `{plugin,plugins}/*` under each config dir, plus config `plugins`. Npm packages are installed on demand.
- **Entrypoints:** `server`, `tui`, `rpc`, `gui-experimental` (`packages/plugin/src/host.ts:25-64`).
- **Hooks:** session prompt, context, compaction, generate, title, model.request, http request/response, retry and idle; tool `execute.before`/`after`; `permission.evaluate`; shell; aisdk (`packages/plugin/src/effect/session.ts:181-195`; `packages/core/src/plugin/hooks.ts:13-19`).
- **Policy block:** `integration.use` `plugin:<target>` (`packages/core/src/plugin/module.ts:53-60`).

---

## 4. Provider and session model

### 4.1 Providers

**Catalog**
- Source is models.dev v2 at `https://models.opencode.ai/experimental/v2.0/api.json`.
- Load order: `OPENCODE_MODELS_PATH`, then a KV cache in SQLite, then the bundled `models-dev/snapshot.txt`, then a fetch. It refreshes every 5 min with ETag (`packages/core/src/models-dev.ts:493-692`).
- Env vars: `OPENCODE_MODELS_URL`, `OPENCODE_DISABLE_MODELS_FETCH`.

**Shapes**
- `Provider.Info{id, name, activation, package, settings, headers, body, …}` (`packages/schema/src/provider.ts:96-106`).
- `Model.Info{id, modelID, providerID, family, capabilities{tools,input,output,temperature}, variants[], cost[]{input,output,cache{read,write}} (USD/M tokens), limit{context,input?,output}, status, enabled, …}` (`packages/schema/src/model.ts:114-138`).
- `Model.Ref` is `{id, providerID, variant?}`, with string form `provider/model#variant`.

**Credentials**
- Stored in SQLite table `credential`, values `{type:"key"|"oauth"|"external", …}` (`packages/core/src/credential/sql.ts:5-14`; `packages/schema/src/credential.ts:31-58`).
- Env-var keys are a fallback (`packages/core/src/integration.ts:409-423`).
- OAuth flows are plugin-provided: chatgpt/openai, github-copilot, gitlab, xai and others. There is no Anthropic OAuth plugin. Refresh happens within 5 min of expiry under a file lock (`integration.ts:751-769`).

**Wire layer**
- Requests go through opencode's own `packages/ai`, which has protocol adapters for anthropic-messages, openai-chat, openai-responses, gemini, bedrock-converse and others.
- Fallback is the Vercel AI SDK via `aisdk:<pkg>` (`packages/core/src/provider.ts:48-212`; `packages/core/src/aisdk.ts:254-366`).
- Defaults: header timeout 300 s, chunk timeout 900 s.
- **Streaming:** one `llm.stream` per attempt. Text, reasoning and tool-input deltas are batched every 100 ms into ephemeral events (`packages/core/src/session/runner/publish-llm-event.ts:98,180-200`).
- **Retries:** exponential from 2 s, capped at 10 s, up to **10** retries; `retry-after` is honoured up to 15 min; at most 3 timeout retries (`packages/core/src/model-retry.ts:22-36`, spot-checked). `specs/v2/session.md:84` says four retries; the code is newer.
- **Variants:** generated per protocol (reasoning effort, Anthropic adaptive thinking or budget) (`packages/core/src/variant.ts`).
- **Reasoning replay:** only to the same provider and model; otherwise it is downgraded to text (`to-llm-message.ts:154-238`).
- **Prompt caching:** automatic breakpoints (last tool, system first/last, conversation tail, maximum 4). The cache key is the session affinity (`packages/ai/src/cache-policy.ts:1-60`).

### 4.2 Model selection

- **Server order:** session model → agent model → config `model` → newest available text model (`packages/core/src/session/runner/model.ts:77-99`).
- **TUI order:** `--model` → config `model` → recent list (local `model.json`) → first model. The choice is sent via `session.create {model}` or `POST …/model`.
- There is **no per-prompt model override**.
- **Title model:** `title` agent model → a small model of the same provider (gpt-luna, gemini-flash(-lite), claude-haiku) → primary model (`packages/core/src/session/context.ts:94-124`; `packages/core/src/model.ts:306-318`).
- **Compaction model:** `compaction` agent model, else the session model.

### 4.3 Message model (v2)

**IDs.** 12 hex characters of `(ms*4096+counter)` (bit-inverted for descending order) plus 14 random base62 characters (`packages/schema/src/identifier.ts:16-31`, spot-checked).

| Prefix | Order |
|---|---|
| `ses_` | descending |
| `msg_` | ascending |
| `evt_` | ascending |
| `per_` | — |

**There are no part IDs.** Assistant content is positional; tools are keyed by the provider's call `id`.

**Messages** (`packages/schema/src/session-message.ts:304-316`). Every message has `{id, metadata?, time.created}`.

| type | fields |
|---|---|
| user | `text, files?, agents?, skills?` |
| synthetic, system | `text, description?` |
| skill | `skill, name, text` |
| shell | `shellID, command, status, exit?, output?` |
| agent-switched / model-switched / location-switched | the new value and `previous?` |
| assistant | `agent, model, content[], snapshot?{start,end,files}, finish?, cost?, tokens?, error?, retry?, time{created,streamed?,completed?}` |
| compaction | `status, reason, summary, model?, cost?, tokens?, error?` |
| idle | `outcome succeeded\|failed\|interrupted` (turn boundary) |

**Assistant content parts** (`session-message.ts:135-206`):
- `text{text, native?}`
- `reasoning{text, native?, time?}`
- `tool{id, name, executed?, state, time{created, ran?, completed?}}`

The tool state machine is `streaming{input:string} → running{input, metadata} → completed{input, content[], metadata?} | error{input, error, content?, metadata?}`.

**Accounting.** `tokens = {input, output, reasoning, cache{read,write}}`. Cost is provider-reported, else catalog
pricing (`packages/core/src/session/usage.ts:11-52`).

**Relation to v1.** The v1 part types `step-start/finish, snapshot, patch, agent, subtask, retry` have no v2
equivalent as parts; they are folded into the fields above.

### 4.4 Persistence

**Database**
- One global SQLite database at `$XDG_DATA_HOME/opencode/opencode.db`, or `opencode-<channel>.db`, or `OPENCODE_DB` (`packages/cli/src/database-path.ts:4-12`; `packages/util/src/global-roots.ts:5-19`).
- Opened in WAL mode with file mode 0600 (`packages/core/src/database/database.ts:40-98`).

**Tables** (`packages/core/src/session/sql.ts`; `packages/core/src/database/schema.gen.ts`):

| Table | Contents |
|---|---|
| `session_v2` | Columns include `parent_id, fork_session_id, fork_boundary, project_id, directory, title, agent, model, permission, revert, cost/tokens`, times, `time_suspended` (execution claim) |
| `session_message` | `id, session_id, type, seq, data` JSON |
| `session_part` | `(message_id, ordinal)`; the migration exists at v2.2.2: `packages/core/src/database/migration.gen.ts:57` |
| `session_inbox` | |
| `instruction_*` | |
| `blob` / `blob_ref` | |
| `event_sequence`, `event` | |
| others | `credential, kv, project, worktree, permission, schedule` |

**Event sourcing**
- Publishing a durable event assigns the next per-session `seq` and runs projectors in one transaction (`packages/core/src/bus.ts:284-445`).
- `event` rows are written only when `persist` is set (`bus.ts:208`, default false). The local CLI leaves it false; only the workerd runtime sets it (`packages/server/src/workerd.ts:69`). Projections are therefore the durable truth locally.
- Migrations live in a TS list (`migration.gen.ts`).
- **On-disk data outside the database:** snapshot git directories at `<data>/snapshot/<projectID>/…` and truncated tool output (*sub-agent reading*).

### 4.5 Lifecycle (`specs/v2/session.md`; `AGENTS.md` "Session Core")

**Prompt admission**
- Admission is durable and comes before execution. `prompt` → `session.inbox.enqueued` (inbox row) → wake.
- Inbox IDs are idempotent.
- `steer` (default) delivers at the next safe step boundary; `queue` waits.

**Runner loop**
- Each step is one `llm.stream` call that continues from reloaded history; there is no in-memory tool loop.
- Each pass promotes inbox items, starts the title on the first prompt, auto-compacts if due, then runs the step.
- Tools run concurrently once `tool.called` is durable.
- Steps emit `step.started`, then `step.streamed`, then `step.ended` or `step.failed`.
- On `finish:"length"` the runner continues, up to 3 consecutive times (`packages/core/src/session/runner/llm.ts:61-366`).

**Execution claims**
- `time_suspended` marks a process-local busy period. On restart, claimed root sessions resume, up to 10 attempts (`packages/core/src/session/execution.ts`; `packages/core/src/session/execution/restart.ts`; `packages/server/src/process.ts:245-247`).

**Interrupt, revert, title, share**
- `interrupt` emits `execution.interrupted{reason:"user"}` and an `idle` message.
- Revert: `stage` restores files from snapshots, `clear` undoes the stage, and `commit` (also implicit on the next prompt) deletes messages with `seq >=` the boundary. All three are rejected while the session is busy (`packages/core/src/session/revert.ts`).
- Title: a one-shot generation; the first line, up to 100 characters, is applied via `session.renamed` (`packages/core/src/session/title.ts`).
- Sharing has been removed.

### 4.6 Compaction (`packages/core/src/session/compaction.ts`)

**Config:** `compaction.{auto (default true), keep.tokens (15000), buffer}`.

**Trigger**
- Before each step, when estimated tokens (last provider usage plus chars/4 since then) reach `context − buffer`, or `context − max(15%, 16k)` when no buffer is set.
- Also once on a provider overflow error before any output (`compaction.ts:421-430,1102-1141`; `step.ts:155-167`).
- Manual compaction is an inbox control item (`POST …/compact`).

**Algorithm**
- Keep the newest messages within `keep` tokens, starting at a user message (`metadata.keepID`).
- Summarize the rest with a structured template: Objective, Requirements, Decisions, Work State, Next Move, Relevant Files, Important Context. An update variant handles successive checkpoints.
- Oversized transcripts are flattened and clipped, then shrunk on rejection to 85/70/50/35%.

**Replay.** Later requests begin with
`<conversation-checkpoint><summary>…</summary><recent-context>…</recent-context></conversation-checkpoint>`
(`packages/core/src/session/runner/to-llm-message.ts:293-311`).

**Pruning.** There is no v2 pruning of old tool output beyond per-call truncation.

### 4.7 Forks and children

- **Fork:** `POST …/fork {before?}` creates a **root** session (`parent_id` null) with `fork_session_id` and `fork_boundary` set. Settled messages are copied with the same `seq`, the title becomes `"<title> (fork #N)"`, and usage is reset (`packages/core/src/session.ts:339-384`; `packages/core/src/session/projector.ts:113-257`).
- **Children:** subagent sessions have `parentID`, listed via `GET /api/session?parentID=<id>`. `parentID=null` lists roots (`packages/schema/src/session.ts:79-89`).
- **Delete:** deleting a session cascades to its children.

### 4.8 Personant's existing implementation compared

**Personant facts** (sub-agent inventory; spot-checked items are marked ✓):

| Area | Personant | Citation |
|---|---|---|
| LLM wire | OpenAI-compatible `chat/completions` only (SSE streaming, `models`, `embeddings`). No Anthropic Messages, no OpenAI Responses | personant:`internal/model/http_client.go:69,164` ✓ |
| Client abstraction | `Client{Consult, ConsultStream, ListModels}`. `Message` is `{Role, Content string, ToolCalls, ToolCallID}`: text-only, no content parts | personant:`internal/model/client.go:30-34` ✓, `:120-125` |
| Streaming | `StreamReader.Next() Chunk{Content, Reasoning, ToolCalls, FinishReason, Usage}`; tool-call fragments merged by index | personant:`internal/model/http_stream.go:162-231` |
| Retries | None on LLM calls; 5-minute hard timeout | personant:`internal/model/http_client.go:41-50,129-135` |
| Usage | prompt/completion/reasoning/cached tokens plus oMLX telemetry; **no cost accounting** | personant:`internal/model/client.go:232-281` |
| Reasoning | Parsed for display only; never persisted or replayed | personant:`internal/model/http_stream.go:353-358` |
| Model catalog | None; the model is named in config and checked against `/models` | personant:`internal/chat/chat.go:607-635` |
| Config | TOML (`providers.toml`, `config.toml`) under `$PERSONANT_HOME` | personant:`internal/memops/memops_model.go:336-563` |
| Sessions | No session entity. In-memory `turn.State.History` (user/assistant text pairs, capped at 20, tool messages dropped after the turn), **not restored across launches** | personant:`internal/turn/turn.go:90-520` |
| Persistence | Memory **threads**: markdown with frontmatter plus JSONL, in a git-committed home dir. This is memory, not transcript storage | personant:`internal/store/paths.go:56-82` |
| Tool loop | In-memory loop, at most 4 tool rounds per turn | personant:`internal/turn/toolloop.go:79` ✓ |
| Tools | `Tool{Spec, Handler, Tier, Mutates}`; mutating tools refused; only web tools registered | personant:`internal/tools/registry.go:43-69,127-148` ✓ |
| Permissions, agents, subagents, forks, compaction | None (thread-closure summaries exist, but no transcript compaction) | personant:`internal/curator/curator.go:30-90` |
| Server / event bus | **None.** No `net.Listen` or `ListenAndServe` in `internal/` or `cmd/` ✓. UI is fed by in-process callbacks | personant:`internal/turn/turn.go:80-87` |

**What opencode has that personant lacks:**
- The HTTP/SSE server and its whole route surface.
- Durable session, message and inbox storage with event-sourced projections.
- A typed assistant content model (text, reasoning, tool parts with a state machine).
- Per-step durable events and restart recovery.
- Multi-protocol provider adapters (Anthropic, OpenAI Responses, Gemini, Bedrock) and a models.dev catalog with cost.
- Retry policy, variants and reasoning replay, prompt-cache breakpoints.
- Agents with modes, subagent dispatch, the permission ruleset and request round-trip, forms.
- Forks and child sessions, compaction, revert via snapshots, title generation.
- Config, agent, command, skill and instruction loading.

**Structural compatibility:**
- **Incompatible:** personant's turn model. Its history is in-memory text pairs, its tool loop runs inside one turn, and memory and transcript are coupled in `turn.State`. opencode requires each step to be one stream call over reloaded durable history, with no in-memory tool loop (`AGENTS.md` "Session Core").
- **Partly compatible:** personant's streaming client is close to one opencode protocol adapter (openai-chat). It already handles SSE parsing, tool-call fragment merging, and reasoning and usage extraction. It lacks cost, retries, content parts and other protocols.

**Recommendation (judgment, not established fact):**
- **Replace personant's session and turn layer** with a Go implementation of opencode's v2 session design: the inbox, step-per-stream runner, durable events and projections, and the message and part schema. The TUI's data layer folds those exact events and shapes, so any other model would need a translation layer as large as the port itself.
- **Keep `internal/model` as the seed of one protocol adapter** (openai-chat, which also serves oMLX), extended with retries, cost and content parts. Add other protocols only as needed.
- Personant's memory system (threads, working set, recall) has no opencode counterpart. It should attach through opencode's own extension points: instruction entries and system-prompt instructions, synthetic messages, and plugin-style hooks on prompt and context. It should not stay fused into the turn loop.

---

## Open questions (source did not settle; affect a Go re-implementation)

1. **Event payload schema source.** `openapi.json` leaves SSE `data` opaque. A Go port must derive the 94 event shapes from `packages/client/src/promise/generated/types.ts` or the Effect schemas in `packages/schema`. Whether upstream intends to publish event schemas is not stated.
2. **TUI tolerance of missing routes.** The behaviour when "yes" routes return 404 or 501 (for example integrations, worktrees, persistent PTY, plugins) was not traced per call site. This decides what a minimum viable Go server is.
3. **Version handshake.** `--server` mode only warns on a version mismatch (`server-connection.ts:41-44`). Which `version` string the Go server should report in `/api/info` (and therefore whether to mimic `2.2.2`) is a policy choice.
4. **Event persistence.** The local server does not write `event` rows (`bus.ts:208`). `GET /api/experimental/session/{id}/log` (TUI devtools) and stats may depend on them. The expected local behaviour is unverified.
5. **Unconsumed events.** `session.status`, `session.idle` and `session.forked` have no TUI consumer found by search. Whether they can be omitted is unverified (plugins may use them).
6. **Interrupted permission waits.** These emit no `permission.replied` (`packages/core/src/permission.ts:269-273`). How the TUI clears that prompt is unverified.
7. **Code Mode.** The `execute` tool is a JavaScript sandbox that fronts several tools and MCP by default. A Go server either embeds a JS runtime or denies `execute`, which exposes those tools directly. TUI rendering parity for the latter is unverified.
8. **`session_part` vs inline content.** Which one new writes should use is unsettled: the projector writes inline unless part rows exist (`packages/core/src/session/projector.ts:315-330`).
9. **Prompt texts.** Byte-identical system and model-family prompts (`packages/core/src/session/runner/prompt/*.txt`, `packages/core/src/plugin/optimize.ts` assets) are needed for prompt-cache parity. They were not reproduced here.
10. **`switchAgent` validation.** It accepts unknown or subagent IDs (`packages/core/src/session/session.ts:99-105`). Whether a Go server should validate is a choice.
11. **Retry count.** The spec says 4 and the code says 10 (`packages/core/src/model-retry.ts:22-36`). The code is newer; upstream intent is unconfirmed.
12. **Delta ordinal semantics.** `ordinal` on text and reasoning deltas is per fragment kind (`publish-llm-event.ts:159-167`). The TUI's exact reliance on it is unverified.
13. **Credential and integration HTTP API.** It changed in v2.2.2 (`packages/protocol/src/groups/credential.ts`, `packages/server/src/handlers/{credential,integration}.ts`). Its request and response semantics beyond the route table were not surveyed.
