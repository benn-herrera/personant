# opencode integration options for Personant's memory system

**Question.** What is the least invasive way to put Personant's memory system (THESIS.md; ACTIVE_DESIGN.md
"Boundary") under opencode? The candidates are compared against what that memory system has to do at runtime.

**Sources.** opencode release tag **v2.2.2**, commit `098aa7fd6e02b03d6fc410f0b052038c7d4c5e81`, at
`../opencode`. opencode paths are repo-relative to that clone. Personant documents are cited by file and section.
OPENCODE_SERVER_SURVEY.md was used only to find things; every opencode claim below was checked in source at the
cited lines.

**Conventions.** Lines without a label are facts with a citation. Lines starting **Judgment:** are reasoning, not
established fact. "Unverified" means the source did not settle it.

---

## 0. Verdict

- **Recommended: B, an opencode server plugin plus a separate memory service.** opencode stays unmodified.
  Ten of the thirteen requirements are fully met. R4 (turn close) is met through a hook marked experimental.
  Two requirements are partially met:
  - **R6, the "ask" recall offer.** A plugin can show the user a yes/no approval prompt, but it cannot open
    opencode's pick-list forms.
  - **R7, the topic tag the model writes in-band.** This means aborting the model's reply mid-stream and
    re-issuing the request. A plugin can only do that at the wire level through the `http.response` hook, and
    nobody has tried it in practice.
- **No requirement forces the full Go port (D).** One upstreamable change, exposing opencode's existing form
  service to plugins, would close R6. R7 needs no opencode change if mid-turn fetch becomes a tool call, which
  is opencode's own mechanism (**Judgment**, §4).
- **A, the model-endpoint proxy alone, falls short.** It has no turn-end signal (R4). It has no user channel for
  the ask offer (R6). It cannot see forks or reverts (R13). It can replace compaction only by recognising
  compaction requests from their text (R2).
- **C (A + B together) adds nothing over B** except moving the wire-level stream code into a separate process.

---

## 1. Requirements: what the memory system must do at runtime

Most of these come from designs written before the move to opencode, when Personant owned the whole harness. The
table keeps the memory and context behaviours and drops the harness mechanics. R13 is new: it follows from
opencode owning session storage.

| ID | Capability | Source |
|---|---|---|
| R1 | **Decide what enters every model request**: the working-set layers, the live window and any recalled content. The context can differ from the stored transcript. | ACTIVE_DESIGN.md "Boundary"; SPEC.md §3.1 (layers rendered into the system prompt; whole request bounded); intra-thread-recall-design.md §3, I6 (live window vs indexed body) |
| R2 | **Own compaction and summarization**: suppress opencode's lossy compaction or supply its result; build summaries offline. | ACTIVE_DESIGN.md "Boundary" (compaction and summarization are ours); THESIS.md ("compacted until what mattered is gone"); SPEC.md §3.4 recall-completeness invariant; within-thread-summary-hierarchy.md W5, W6 |
| R3 | **See every context change at a semantic boundary**: user prompt, model response, tool result and shell capture, with nothing bypassing the chain. | SPEC.md §3.0.1, §3.0.5; intra-thread-recall-design.md §6.2 (debt from scrolled-out turns); within-thread-summary-hierarchy.md §5.1 (dirty marks on leaf add) |
| R4 | **Act at turn close**: coalesce symbols per turn, assign the owner thread, write the excerpt, update the LRU and dormancy, queue embedding flushes. | SPEC.md §3.0.4, §3.2; intra-thread-recall-design.md §6.2, §11.3 |
| R5 | **Keep durable memory outside sessions**: it survives restarts, starts in time proportional to what changed, and loses at most one turn. | THESIS.md; intra-thread-recall-design.md I5, §5.3, AC4; crash-stability-implementation.md "Durability bar"; dual-repo-barrier.md §0 |
| R6 | **Surface recall within the turn**: AUTO band = injected into context with no prompt; ASK band = one offer to the user (yes/no, or a pick-list). | SPEC.md §3.4 "Recall surface", "Banding"; intra-thread-recall-design.md §7 |
| R7 | **Model-initiated mid-turn fetch and in-band metadata**: the topic tag at the start of a response is parsed before the body; a missing thread aborts the stream and re-issues the request with that thread loaded (at most once per cause); the tag is stripped from what the user sees; the `lifetime:` marker is parsed. | SPEC.md §5.1, §5.5, §3.1 "§5.5 mid-turn fetch", §3.10.8 |
| R8 | **Do background work off the critical path**: the async indexer, embedding calls, and sleep-cycle LLM summarization. | intra-thread-recall-design.md I1, §6; within-thread-summary-hierarchy.md W6, W7, §5; ARCHITECTURE.md "Memory consolidation — the 'sleep' cycle" |
| R9 | **Split off a sub-mind and re-merge it**: spawn a context the memory system seeds, observe it, and merge its results into main memory and, optionally, into the parent's context. | ACTIVE_DESIGN.md "Boundary", "Main session memory and agent memory"; ARCHITECTURE.md tier table (E4B "submind"); multicameral-runtime-intelligence.md §3, §10 |
| R10 | **Run several models by role, outside the main loop**: curator, recall judgment, summarizer, embedder, sensors. | multicameral-runtime-intelligence.md §3 ("model set as platform"), §10 decisions 2–3; SPEC.md §3.4 layer 3; within-thread-summary-hierarchy.md §9 (summarizer port) |
| R11 | **Keep raw tool output transient**: it stays in context for a window, is then evicted, and never enters durable memory. | SPEC.md §3.10.5, §3.10.6, §3.10.8; intra-thread-recall-design.md §13 |
| R12 | **Stay within the token budget**: bound the whole request against the model window before sending; read provider usage after. | SPEC.md §3.1 |
| R13 | **Stay consistent with opencode's transcript operations**: fork, revert, delete, and resume after restart. | Derived from ACTIVE_DESIGN.md "Conform to opencode" (opencode owns session storage) |

---

## 2. opencode v2.2.2 capabilities (facts)

### 2.1 Installing and loading plugins; their runtime

- **How plugins are found:**
  - Any file or directory under `plugin/` or `plugins/` in a config directory
    (`packages/core/src/plugin/source-directory.ts:7-32`).
  - The config key `plugins`, whose entries are an npm spec, a path, or `{package, options}`
    (`packages/schema/src/config.ts:106`; `packages/schema/src/config/plugin.ts:6-13`; parsing at
    `packages/core/src/config/plugin/source.ts:113-120`).
  - npm targets are installed on demand (`packages/core/src/plugin/module.ts:109-113`).
  - The managed policy `integration.use` with `plugin:<target>` can block a plugin
    (`module.ts:52-60`).
  - Local plugins are watched and reloaded when they change (`module.ts:27-38`;
    `packages/plugin/src/source.ts:8-40`).
- **Entrypoints:** `server`, `tui`, `rpc` and `gui-experimental`, resolved per package
  (`packages/plugin/src/host.ts:13-65`). Only `server` runs in the server process (`module.ts:120-133`).
- **Module shape:** the default export is `{id, effect}` (Effect API) or `{id, setup}` (Promise API)
  (`module.ts:80-93, 173-193`). Promise-API hook callbacks may be `async`
  (`packages/plugin/src/promise/registration.ts:15-19`).
- **Runtime:**
  - The CLI is a Bun-compiled executable (`packages/cli/script/build.ts:160-190`), and plugins are imported
    into that process.
  - The plugin loader intercepts exactly two packages, `effect` and `@opencode/plugin`, which plugins must take
    from the host (`packages/plugin/src/runtime.ts:12-35`; `packages/plugin/src/runtime-modules.ts:7`). There
    is no other sandboxing in the loader.
  - Unverified: whether Bun's `node:net` and `node:child_process` (or `Bun.spawn`) work from plugin code. They
    are Bun capabilities and were not exercised against opencode.

### 2.2 How hooks work

- Hooks run sequentially in registration order and mutate the event object in place. `trigger` returns the
  mutated event (`packages/core/src/plugin/hooks.ts:88-95`). There is no timeout.
- Only `tool.execute.before` may fail; every other hook's failure channel is `never` (`hooks.ts:23-30`).
- A Promise-API session hook is wrapped in `Effect.promise` (`packages/plugin/src/promise/adapter.ts:600-602`).
  Under Effect semantics a rejected promise becomes a defect, so a plugin must catch its own RPC failures. What
  a defect does to the session step is unverified.
- Model hooks can be limited to one provider (`packages/plugin/src/effect/registration.ts:7-25`).
- Plugin activation is awaited before every drain and every step's context preparation. Sessions resumed after a
  restart are covered too (`packages/core/src/session/runner/llm.ts:75, 222`).

### 2.3 Server plugin hooks

Session hooks are declared at `packages/plugin/src/effect/session.ts:181-195`. "Replace" means the hook can
substitute the behaviour, not just watch it.

| Hook | Fires | Input; what the hook may change | Replace? | Where it fires |
|---|---|---|---|---|
| `session.prompt` | When a user prompt is admitted, before it is stored | `prompt{text, files, agents, skills}`, `metadata` and `delivery` are mutable (`session.ts:14-20`). The stored inbox item is built from the mutated event. | Yes: rewrites what is stored | `packages/core/src/session/prompt.ts:42-90` |
| `session.context` | Every primary step and every retry of a step | `system: SystemPart[]`, `messages: Message[]` (provider-neutral `@opencode/ai` messages), `tools` (description and input schema) and `options` (generation and provider options; defaults to `maxTokens`) are all mutable (`session.ts:25-36`). Tools may be renamed or removed; tools the hook invents are dropped (`model-request.ts:273-281`). The mutated event is not stored (`llm.ts:273-281`). | Yes: the whole request | `packages/core/src/session/model-request.ts:258-310, 417-422`; `llm.ts:239-271` |
| `session.compaction` | Every compaction request (manual, auto, overflow, background precompute; native and summary paths) | Same fields as `context`, plus `result?: {summary, native?, metadata?, tokens?}` (`session.ts:38-49`). Setting `result` skips the model call (`compaction.ts:605, 852-866`). `result.metadata.keepID` overrides the kept range (`compaction.ts:523-525`). | Yes: supply the summary | `compaction.ts:826-850` (request built from the stored transcript) |
| `session.generate` | `session.generate`, i.e. transient text from the session context, with nothing written to history | Same fields as `context` | Yes: the request | `model-request.ts:424`; `packages/core/src/session/generate.ts:47` |
| `session.title` | Title generation | Request fields plus `result?`, which skips the model call (`session.ts:53-56`) | Yes | `model-request.ts:425`; `packages/core/src/session/title.ts:71` |
| `session.model.request` | Every model request of every kind, after the request is built | `baseURL` and `headers` are mutable; `kind` is one of `primary`, `compaction`, `title`, `generate` (`session.ts:62-71`) | Redirects the endpoint | `model-request.ts:312-328` |
| `session.http.request` / `session.http.response` | Wrap every HTTP model call (only when registered) | The web `Request` or `Response` can be replaced. A replaced request body is sent; a replaced response is what opencode parses (`model-request.ts:329-360`). | Yes: at the wire level | same |
| `session.experimental.ws.handshake` / `.send` / `.receive` | Every model call over WebSocket transport | URL and headers, or a frame string; all replaceable (`session.ts:90-126`) | Yes: wire frames | `model-request.ts:363-387` |
| `session.retry` | When a model call fails | `decision` is mutable (`session.ts:128-137`) | Yes: retry policy | `model-request.ts:393-394` |
| `session.experimental.idle` | Model work has finished, no input is pending, and the session is about to go idle. It does not fire after an interrupt, a failure, or control-only work. | `background` lists running shells and subagents. Input the hook admits runs in the same execution (`session.ts:164-179`). | Can extend the turn | `llm.ts:172-182` |
| `tool.execute.before` | Before any tool runs | `tool` and `input` are mutable; may fail with `Tool.Error` to reject the call (`packages/plugin/src/effect/tool.ts:20-28, 48-52`) | Yes | `packages/core/src/tool.ts:105-112` |
| `tool.execute.after` | After any tool completes or fails | `result{output, content, metadata}` or `error` is mutable; the mutated result is what is returned and stored (`tool.ts:36-45`) | Yes: rewrites the stored tool result | `packages/core/src/tool.ts:115-157` |
| `permission.evaluate` | Each permission evaluation | `effect` and `message` are mutable (`packages/plugin/src/effect/permission.ts:10-23`) | Yes | `packages/core/src/permission.ts:180` |

Plugin context outside the hooks (`packages/plugin/src/effect/plugin.ts:26-58`):

- **`tool.transform`** can add, update or remove tools. This is how a plugin gives the model a tool
  (`effect/tool.ts:8-18, 54-60`).
- **`event.subscribe`** streams all public bus events, or selected types (`packages/core/src/plugin/host.ts:264-280`).
  The useful ones are:

  | Events | Defined at |
  |---|---|
  | `session.created`, `.deleted` | `packages/schema/src/session-event.ts` |
  | `session.forked {parentID, boundary}` | `:192-205` |
  | `session.revert.committed {to}` | `:647-651` |
  | `session.text.ended`, `session.tool.success`, `session.shell.ended`, `session.step.ended`, `session.usage.recorded`, `session.compaction.ended`, `session.execution.succeeded` | `packages/schema/src/session-event.ts` |

- **`session`**: `list`, `create`, `get`, `remove`, `switchAgent`, `switchModel`, `prompt`, `generate`,
  `command`, `compact`, `synthetic`, `interrupt`, `update`, `move`, `context`, `wait` and `instructions`
  (`effect/session.ts:197-217`; host wiring at `host.ts:589-591`).
  - `create` accepts `parentID` (`packages/protocol/src/groups/session.ts:168, 183`).
  - `synthetic` durably admits input and schedules execution unless `resume:false` (`protocol/.../session.ts:398-417`).
  - **There is no `fork`** in the plugin session domain.
- **`experimental.generate`**: stateless generation with any configured model; no session, history or tools
  (`plugin.ts:35-39`).
- **`permission.assert`**: prompts the user when approval is required and waits for the reply
  (`effect/permission.ts:26-33`). The input carries `action`, `resources` and `metadata`
  (`packages/plugin/src/permission.ts:5-13`).
- **Forms are not exposed to plugins.** The server's `Form.Service` has `create` and `ask`
  (`packages/core/src/form.ts:87-88`) and the built-in question tool uses it (`core/src/tool/plugin/question.ts:51`),
  but the plugin `Context` has no form domain (`plugin.ts:26-58`).

### 2.4 Compaction: disable, replace, reconfigure

- **Settings:** `{auto, buffer?, keep, precompute}`. The default is `auto: true, keep: 15000, precompute: false`
  (`packages/core/src/session/compaction.ts:41-49, 261`).
- **Config keys:** `compaction.auto`, `compaction.buffer` and `compaction.keep.tokens`, plus
  `experimental.background_compaction` (`packages/core/src/config/plugin/compaction.ts:15-24`;
  `packages/schema/src/config/compaction.ts`).
- **Disabling it:** `compaction.auto: false` turns off automatic and overflow compaction. Only manual compaction
  still runs (`compaction.ts:294-295`). That service's transform is used by an internal config plugin; an
  external plugin cannot call it, but config reaches the same setting.
- **When it triggers:** before each step (`llm.ts:241-248`), using `estimatePrompt`. That is the provider-reported
  token count of the latest assistant message, plus a character estimate for the stored messages since then
  (`compaction.ts:1102-1127`). It also triggers once on a provider overflow error (`llm.ts:304-310`).
- **Replacing the content:** the `session.compaction` hook's `result` (§2.3). Both the summary path and the native
  path go through `deliver`, which honours `result` (`compaction.ts:515, 577, 605`).
- **Effect on later requests:**
  - A completed compaction bounds every later history read; rows are read from the latest compaction on
    (`packages/core/src/session/history.ts:192-199`).
  - It replays as one `<conversation-checkpoint>` user message (`packages/core/src/session/runner/to-llm-message.ts:293-311`).
- **Manual compaction** is an inbox control item. A plugin can enqueue one with `session.compact`, and it runs
  even when `auto` is false (`llm.ts:127-171`; `compaction.ts:294`).

### 2.5 Subagents

- **Spawning:**
  - The `subagent` tool is itself a plugin and uses the same `ctx.tool` API.
  - Depth limit: `experimental.subagent_depth`, default 1 (`packages/core/src/tool/plugin/subagent.ts:125-139`).
  - The agent named must not be `primary` (`:142`).
  - The child is created with `parentID` set to the caller (`:192-203`).
  - The child's first prompt is prefixed "You are a subagent spawned by another session." (`:211-224`).
- **The child is an ordinary session.** It is driven by the same runner (`sessions.prompt`, then
  `llm.ts` drain), so every session hook fires for it with the child's `sessionID`.
- **How the result returns:**
  - **Foreground:** the parent's tool result is `<subagent sessionID=… state="completed">…</subagent>`
    (`subagent.ts:240-276`). It passes through `tool.execute.after` and can be mutated there (`tool.ts:141-157`).
  - **Background:** completion arrives as a synthetic message in the parent with `metadata.source:"subagent"`
    (`packages/core/src/session/subagent-completion.ts:21-46`). No tool hook sees it; the `context` hook and the
    event stream do.
  - **Headers:** model requests from a child carry `x-opencode-parent-session-id` and `x-parent-session-id`
    (`model-request.ts:289-294`).

### 2.6 Custom provider: what a proxy would receive

- **Configuration:**
  - `provider.<id>` takes `package`, `settings` (open record), `headers`, `body` and `models{… limit{context,
    input, output} …}` (`packages/schema/src/config/provider.ts:112-121, 54-58`).
  - `@ai-sdk/openai-compatible` maps to opencode's native openai-compatible chat route
    (`packages/core/src/aisdk-native.ts:70`).
  - `settings.baseURL` becomes the route endpoint (`packages/ai/src/providers/openai-compatible.ts:19-37, 70-77`).
- **What each request carries:** the full assembled request — system parts, every message from the latest
  compaction onward (after the `context` hook), tool definitions and generation options
  (`model-request.ts:286-310`).
- **Headers sent with it:** `x-opencode-session-id`, the parent session ID when there is one,
  `x-session-affinity`, `X-Session-Id`, `x-opencode-project`, `x-opencode-session` and `x-opencode-client`
  (`model-request.ts:288-299`). They go out on the wire (`packages/ai/src/route/transport/http.ts:60`).
- **What is missing:** no header names the request kind (primary, compaction, title or generate). Title,
  compaction and generate requests reach the same endpoint when they use the same model.
- **Forks:** a forked session is a new root session, so its requests carry no parent header. The proxy cannot
  tell a fork from a new session (fork projection: `packages/core/src/session/projector.ts:113-160`).

### 2.7 When the stored session differs from what the model saw

| Operation | Behaviour | Consequence for a context rewriter |
|---|---|---|
| Each step | History is reloaded from storage and the hook runs again; the hooked event is thrown away (`llm.ts:239-281`) | The rewriting must be repeatable per step, or the memory service must record its own decisions. **Judgment:** changing the prefix from step to step costs the provider's prompt cache. |
| Token accounting and auto-compaction | `measured` is the provider's usage for what the model actually received (`compaction.ts:1121-1125`) | A small hooked context keeps auto-compaction from firing. The stored transcript still grows, and each step reads every row since the latest compaction (`history.ts:192-199`). |
| Compaction | The summary request is built from the stored transcript, not the hooked context (`compaction.ts:838-850`) | Not an issue when the hook supplies `result`. |
| Resume after restart | Stale compactions and tool calls are settled, then the drain resumes after plugin activation (`llm.ts:75-77, 379-432`) | The hooks are in place before any resumed step. |
| Fork | Settled stored messages are copied into a new root session, and `session.forked` is published (`projector.ts:113-257`; `session-event.ts:192-205`) | Memory must map the fork. A plugin sees the event; a proxy does not. |
| Revert | `commit` publishes `session.revert.committed{to}` (`packages/core/src/session/revert.ts:75-80`), and the projector deletes the rows from the boundary on (`projector.ts:880-889`) | Memory derived from reverted turns stays unless the memory service retracts it on that event. |
| TUI display | The TUI shows stored messages | Users see the full transcript, not the curated context. Not a breakage. |

---

## 3. Mapping: requirement × candidate

- **A** = model-endpoint proxy. **B** = server plugin + memory service. **C** = A + B. **D** = full Go port.
- **E** = an MCP server or custom tools plus config only (agents, instructions, `compaction.auto`): another
  unmodified-opencode route.
- "Met" means met with opencode unmodified, except in column D.

| Req | A: proxy | B: plugin + service | C: A+B | D: Go port | E: MCP / config only |
|---|---|---|---|---|---|
| R1 context control | **Met**: rewrites the OpenAI-chat body per request (§2.6); cannot tell request kinds apart | **Met**: the `context` hook replaces system, messages, tools and options every step and retry (`model-request.ts:258-310`; `llm.ts:239-271`), before serialization and independent of provider | Met (B) | Met (owns the runner) | **Not met**: can only add content (tool results, instructions); cannot remove history |
| R2 compaction | **Partial**: `compaction.auto:false` suppresses it (`compaction.ts:294-295`); replacing it means recognising compaction requests from their template text (`compaction.ts:206`) because no kind is sent; cannot set the kept range | **Met**: `auto:false`, plus a `compaction` hook `result` with `keepID` (`compaction.ts:523-525, 605, 852-866`), plus `session.compact` to put bounded checkpoints into the stored history (`effect/session.ts:208`) | Met (B) | Met | **Partial**: suppression only |
| R3 observe all changes | **Partial**: user text and tool results are seen only when the next request carries them; a turn's last step is seen only as it streams; no shell or tool event as it happens | **Met**: `session.prompt` (`prompt.ts:42-53`), `tool.execute.after` (`tool.ts:115-157`), and events for text, tool, shell and step (`host.ts:264-280`) | Met (B) | Met | **Not met** |
| R4 turn close | **Not met**: no end-of-turn signal | **Met (experimental hook)**: `experimental.idle` (`llm.ts:172-182`), plus the `session.execution.succeeded` event | Met (B) | Met | **Not met** |
| R5 durable store, restart | **Met**: runs in its own process | **Met**: runs in its own process; activation is awaited before resumed steps (`llm.ts:75, 222`); the plugin must survive the service being down (`adapter.ts:600-602`) | Met | Met | Met (storage only) |
| R6 recall surfacing | **Partial**: AUTO band by rewriting the body; **no ASK channel** | **Partial**: AUTO band via `context`; ASK only as a yes/no `permission.assert` (`effect/permission.ts:26-33`); **no pick-list form** (`plugin.ts:26-58` lacks `Form`) | Partial (B) | **Met**: the server owns `Form.ask` (`form.ts:88`); how the stock TUI renders it is unverified | **Partial**: only through the model calling the question tool |
| R7 mid-turn fetch, in-band tag | **Met**: the proxy sits in the stream; it can read the head, abort upstream, re-issue, and strip the tag before opencode stores the text | **Partial**: the same is possible in-process by replacing the response in `http.response` (`model-request.ts:350-358`), or with `ws.receive` for WebSocket. It is protocol-specific and has not been tried (unverified). Opencode-native alternative: a recall or fetch **tool** added with `tool.transform` | Met (A part) | Met | **Partial**: tool-call alternative only |
| R8 background work | **Met** | **Met**: the service runs in its own process; the loader adds no sandbox (`runtime.ts:12-35`) | Met | Met | Met |
| R9 sub-mind split/re-merge | **Partial**: sees child requests through the parent header (`model-request.ts:291`) and can seed them; cannot spawn a sub-mind or durably inject the merged result | **Met**: the same hooks fire for children; the result can be mutated in `execute.after` or seen as a synthetic message (§2.5); the plugin can `create` a child with `parentID`, then `prompt`, `wait`, and add `synthetic` messages to the parent (`effect/session.ts:197-217`) | Met (B) | Met | **Not met** |
| R10 role models | **Met** | **Met**: the service calls its models directly; `experimental.generate` (`plugin.ts:35-39`); `model.request` can reroute a call (`model-request.ts:312-321`) | Met | Met | Met (within the server) |
| R11 transient tool output | **Met**: can drop tool results from the body | **Met**: `context` edits messages; `execute.after` trims what is stored (`tool.ts:141-157`) | Met | Met | **Not met** |
| R12 token budget | **Met**: sees upstream usage; knows the window only from its own config | **Met**: `options.maxTokens` (`model-request.ts:263-269`) and the `session.usage.recorded` and `session.step.ended` events | Met | Met | **Not met** |
| R13 fork / revert / delete | **Not met**: forks are root sessions with no parent header; reverts show only as missing messages | **Met**: the `session.forked`, `session.revert.committed` and `session.deleted` events (§2.3) | Met (B) | Met | **Not met** |

**Totals.** "Partial with an experimental API" counts as met here.

| Candidate | Met | Partial | Not met |
|---|---|---|---|
| A | 7 | 4 (R2, R3, R6, R9) | 2 (R4, R13) |
| B | 11 | 2 (R6, R7) | 0 |
| C | 12 | 1 (R6) | 0 |
| D | all | — | — |
| E | 3 | 3 | 7 |

---

## 4. Recommendation

**Fact:** B fully meets every requirement except R6 (ASK band) and R7 (in-band tag protocol), and both of those
are partially met (§3).

**Judgment.** B is the least invasive candidate that does the job. The reasons:

- **R1, R2 and R11 hold without any wire-format code.** The `context` and `compaction` hooks act on the
  provider-neutral request before serialization. A proxy (A) has to parse and re-emit each provider protocol it
  fronts, and only for providers pointed at it.
- **R4, R9 and R13 depend on information only inside the server**: turn end, subagent wiring, fork and revert
  events. A proxy cannot see these.
- **R7 does not force A, C or D.** SPEC.md §5.5's "the model does not invoke a tool" predates the move to opencode.
  Under opencode a fetch tool (`tool.transform`) is the native way to do the same thing: the result enters context
  at the next step, which the `context` hook controls.
  - If the in-band tag must be kept, B can do the abort and re-issue in `http.response`. It is the same code a
    proxy would need, but in-process.
  - C only moves that code out of process, at the cost of an extra process and a second protocol surface.
- **R6 is the only gap that needs opencode to change**, and the change is narrow.
  - **Upstreamable change:** add a form domain, e.g. `ctx.form.ask` wrapping `Form.Service.ask`
    (`packages/core/src/form.ts:88`), to the plugin `Context` (`packages/plugin/src/effect/plugin.ts:26-58`).
  - Until then, the ASK band works as a yes/no `permission.assert` per candidate, or as an AUTO-only policy.
- **D buys R6 at the price of re-implementing the whole server.** ACTIVE_DESIGN.md also leaves opencode's
  JavaScript plugins undecided under a Go server; B keeps them for free.

**Judgment: what B's design must hold to.**

1. **The plugin holds no memory logic.** It is a thin, stateless adapter that forwards hook events to the memory
   service over local RPC and applies the service's answers. All memory state lives in the service (R5).
2. **Every session hook catches its own RPC failures** and falls back to opencode's default: leave the event
   unmodified (`adapter.ts:600-602`).
3. **Set `compaction.auto: false` and answer every compaction from the service**, through `result` with
   `keepID`. The service also enqueues manual compactions (`session.compact`) so stored-history reads stay
   bounded (`history.ts:192-199`). Without this, each step reads a growing transcript, which conflicts with
   intra-thread-recall-design.md I3 at session scale.
4. **The rewritten `messages` must keep tool-call and tool-result pairs intact.** Providers rejecting orphaned
   tool results is general provider behaviour and was not checked in opencode source.
5. **Pin the opencode version.** `experimental.idle` and the `ws` hooks are marked experimental
   (`effect/session.ts:90-126, 164-179`).

---

## 5. Open questions (the source did not settle these)

1. **ASK prompt rendering.** How the TUI shows a `permission.assert` whose `action`, `resources` and `metadata`
   come from a plugin, and whether it reads well as a recall offer (gist, why it matched, score).
2. **Mid-stream abort in `http.response`.** Can a hook read the head of a streaming `Response`, cancel upstream,
   re-issue the request and return a second stream without breaking opencode's stream accounting (retries,
   timeouts, usage)? Only a spike settles this; it matters only if the in-band tag protocol (R7) is kept.
3. **Hook defects.** What happens to the session step, and what the user sees, when a session hook dies.
4. **Several opencode server processes on one memory service.** For example, one per TUI launch
   (OPENCODE_TUI_LAUNCH.md). Personant's crash and durability designs assume a single writer.
5. **Missed events.** Whether `event.subscribe` delivers events published while the plugin was reloading. The
   source suggests a live bus with no replay (unverified). Recovery would read stored messages instead.
6. **Bun built-ins from plugin code.** Whether `node:net`, unix sockets and child-process spawning are available,
   and whether the `--smol` exec flag (`build.ts:187`) affects a long-lived RPC client.
7. **Model window from a plugin.** Whether the plugin `model` domain exposes `limit.context` for R12; it was not
   read.
8. **Plugin API stability** beyond v2.2.2: whether the `context` and `compaction` hook shapes are stable or still
   v2-experimental. The plugin package's `effect/PLAN.md` was not reviewed.
