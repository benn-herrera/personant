# opencode v2.2.3: what reaches the model, and how Personant can tell

Scope: everything opencode puts in a primary model request, as the plugin's `session.context` hook sees it, for
an unmodified opencode v2.2.3 (clone at tag `v2.2.3`). Citations are repo-relative to `packages/` of that clone.
Sections 1–4 are facts from source. Section 5 is judgment. Items not confirmed in source say **unverified**.

## 0. How the hook input is assembled

- Every step rebuilds the request from stored history: `context.load` → `SessionHistory.entriesForRunner`
  (`core/src/session/context.ts:168-184`), then `baseTranscript` (`core/src/session/model-request.ts:100-123`),
  then the `context` hook (`model-request.ts:262-276, 421-426`). The mutated event is discarded after the step
  (`core/src/session/runner/llm.ts:272-282`).
- Hooks run in registration order and mutate in place (`core/src/plugin/hooks.ts:88-95`). Boot order is internal
  `pre` plugins, then package plugins, then internal `post` plugins (`core/src/plugin/supervisor.ts:98-102`;
  `core/src/plugin/internal.ts:224-284`). No `post` plugin registers a `context` hook (source search). So Personant's
  hook sees the output of every built-in `context` hook. That activation is sequential in list order is
  **unverified** in code; OPENCODE_PROBE_RESULTS.md §6 (one composed system message, hash-exact, at the mock) agrees.
- After the hook, opencode still rewrites the messages (`model-request.ts:309`; see P1–P2), and the wire adapter
  lowers them (P3–P4).
- History read: from the latest completed compaction on (`core/src/session/history.ts:183-224`). A committed revert
  deletes rows from the boundary on (`core/src/session/projector.ts:877-894`).
- Inbox items (`user`, `synthetic`, `compaction`, `move`) are promoted at step boundaries. `steer` delivery lands
  between steps of a running turn (`llm.ts:92-121, 184-200`; `projector.ts:731-758`). A user prompt can therefore
  appear after a tool result in the middle of a turn.

## 1. Inventory

Lifetime key: **req** = rebuilt on every request; **hist** = stored, visible until compaction or revert;
**once** = stored once, never re-sent by opencode after it leaves stored history; **step** = only this request,
never stored.

### 1.1 System parts (`event.system`), in order

| # | Content | Trigger | Built at | Lifetime |
|---|---|---|---|---|
| S1 | Agent `system` if set, else `system.txt` with tool guidance lines for shell/write/edit | always | `model-request.ts:110-115`; `core/src/session/system-prompt.ts:5-28`; `core/src/session/runner/prompt/system.txt` | req |
| S2 | Model-family prompt: `gpt` replaces S1, `claude` appends `anthropic.txt`, plus kimi/trinity/muse. Only when the agent has no `system` | model id match | `core/src/plugin/optimize.ts:16-60, 86-98` | req |
| S3 | `# Your Model` block (name, provider ID, model ID), spliced at index 1 | always | `core/src/plugin/identity.ts:10-28` | req |
| S4 | Instruction baseline: sources I1–I8 joined with `\n\n`, part `metadata.experimental.instructions = [{key, text}]` | always (dropped if empty) | `model-request.ts:116-120`; `core/src/instructions/index.ts:162-178, 238-240` | req, frozen per epoch (below) |
| S5 | "When you create a worktree … `tools.opencode.session_move` …" hint, pushed last | always, unconditional | `core/src/tool/plugin/opencode.ts:99-106` | req |

S4 metadata is not updated if a hook edits the part's text (`plugin/src/effect/session.ts:26-31`).

**Epoch baseline.** S4 renders the values frozen at session start or at the last completed compaction, not the
current values (`core/src/session/instruction-state.ts:172-182`; epoch advance on `Compaction.Ended`,
`projector.ts:848-853`). Later changes arrive as system-role update messages (M6).

Instruction sources inside S4, in order (`context.ts:152-163`):

| # | Key | Content | Built at |
|---|---|---|---|
| I1 | `core/codemode` | Code Mode tool catalog | `core/src/codemode/instructions.ts:126-140` |
| I2 | `core/mcp-guidance` | MCP server instructions, only for servers with a reachable tool | `core/src/mcp/instructions.ts:70-110` |
| I3 | `core/reference-guidance` | `<available_references>` project reference dirs | `core/src/reference/instructions.ts:15-30, 84-95` |
| I4 | `core/skill-guidance` | Skill guidance and `<available_skills>` (with a description, not autoinvoke-disabled, not denied) | `core/src/skill/instructions.ts:25-35, 76-95` |
| I5 | `core/instructions` | `Instructions from: <path>\n<content>` for global `<config>/AGENTS.md`, project `AGENTS.md` walking up, and config `instructions` (paths, globs, URLs) | `core/src/instruction-discovery.ts:17, 123-125`; `core/src/config/plugin/instruction.ts:38-120` |
| I6 | `core/date` | `Today's date: <date>` | `core/src/instructions/builtins.ts:24-32` |
| I7 | `core/environment` | `<env>` block: cwd, workspace root, git yes/no, platform, preferred tmp dir | `builtins.ts:33-53` |
| I8 | `api/<key>` | `<context key="…">value</context>` entries set through the instructions API | `core/src/session/instruction-entry.ts:84-103` |

### 1.2 Messages (`event.messages`) from stored history

The conversion is `toLLMMessage` (`core/src/session/runner/to-llm-message.ts:240-315`).

| # | Content | Trigger | Built at | Lifetime |
|---|---|---|---|---|
| M1 | User prompt text. Includes the expansion of `/init`, `/review`, config commands and MCP prompts, and the child prefix `You are a subagent spawned by another session.` | prompt / command / subagent dispatch | `to-llm-message.ts:256-273`; `core/src/plugin/command.ts:29-80`; `core/src/config/plugin/command.ts:86-126`; `core/src/tool/plugin/subagent.ts:211-218` | hist |
| M2 | Prompt-attached skills: `<skill_content name=…>` text **before** the prompt text in the same user message | prompt `skills` | `core/src/session/prompt.ts:58-78`; `core/src/skill.ts:35-53`; `to-llm-message.ts:258` | hist |
| M3 | Attachments: text files as `\n\nAttached file: <name>\n…`, directories as `Attached directory: …`, images/PDF as `Attached file: <path>` + media part | prompt `files` (TUI `@file`, paste) | `to-llm-message.ts:38-103`; `prompt.ts:92-145` | hist |
| M4 | `@agent` mentions: only `metadata.agents` on the user message, no text | prompt `agents` | `to-llm-message.ts:268-271` | hist |
| M5 | User-invoked skill via `POST …/skill`: a `skill` message whose text is the raw skill body (`skill.content`), not wrapped | experimental skill route | `core/src/session/session.ts:236-258`; `core/src/session/message-updater.ts:176-187`; `to-llm-message.ts:276-277` | hist |
| M6 | Instruction update: system-role message. Texts such as `Today's date is now:`, `The environment you are running in is now:`, `The instructions from <path> changed. Here's the diff:`, `New instructions apply from:`, `… no longer apply.`, skill/MCP/reference/code-mode/`api/` deltas | an instruction source value changes between steps | `instruction-state.ts:38-87`; `message-updater.ts:151-163`; `to-llm-message.ts:278-279`; renderers in the I1–I8 files | hist; ones before a native compaction are dropped (`history.ts:218-223`) |
| M7 | Nested `AGENTS.md`: synthetic `Instructions from: <path>\n<content>` for AGENTS.md files between a read target and the location root | successful `read` below the location | `core/src/tool/plugin/read.ts:82-107`; `core/src/session/instructions.ts:47-94` | once (dedup ledger is synthetic metadata in stored history since the last compaction, `instructions.ts:40-45, 100-113`) |
| M8 | Editor context: synthetic `<system-reminder>Note: The user opened the file "…"` or `… selected #a-b from "…" ```…```` | TUI prompt with a pending editor selection | `tui/src/component/prompt/index.tsx:161-172, 1347-1360` | hist |
| M9 | User shell capture (`!cmd`): synthetic `The following shell command was executed by the user:\n<shell id=… state=… command=…>\n…</shell>`; start failure: `User shell command failed to start:` | `POST …/shell` | `session.ts:192-235`; `core/src/shell/result.ts:45-77` | hist. The `shell` row itself is `background:true` and emits nothing (`core/src/session/shell.ts:17`; `to-llm-message.ts:281-282`) |
| M10 | Background shell completion: synthetic `<shell id=… state=… command=…>\n…</shell>` | model `shell` call with `background:true` finishes | `core/src/tool/plugin/shell.ts:150-180`; `result.ts:45-63` | hist |
| M11 | Background subagent completion: synthetic `<subagent sessionID=… state=… description=…>\n…</subagent>` | background child finishes | `core/src/session/subagent-completion.ts:21-46` | hist |
| M12 | Background image completion: synthetic `<image id=… state=…>…</image>` | image job finishes | `core/src/tool/plugin/image-generate.ts:203-220`; `core/src/image/notice.ts` | hist |
| M13 | `User requested that active blocking work be moved to the background. …` | `POST …/background` | `session.ts:508-525` | hist |
| M14 | Plan reminders: `<system-reminder>\nYou are in Plan mode. …` / `<system-reminder>\nYou are NO LONGER in Plan mode. …` | session created as `plan`, or agent switched to or from `plan` | `core/src/plugin/plan.ts:14-25, 75-108` | hist |
| M15 | Goal kickoff and continuation (`A goal is now active …`, `Continue working toward the active goal.` with `<untrusted_objective>`) | `/goal <objective>`, `/goal resume`, goal judge says continue | `core/src/plugin/goal.ts:198-202, 377-381, 411-432`; `plugin/goal/{kickoff,continuation}.txt` | hist |
| M16 | Continue notices: `The previous response was interrupted. …`, `Your last response hit the output token limit …`, `The server restarted while you were working. …` | incomplete stream; `finish:"length"`; restart resume | `llm.ts:37-41, 333, 364`; `core/src/session/execution/restart.ts:16-17, 93-98` | hist |
| M17 | `The working directory has been changed to <dir>.` (user role) | session move | `message-updater.ts:121-130`; `to-llm-message.ts:247-255` | hist |
| M18 | Reasoning-effort marker: system role with one `effort` part | model switch that only changes the effort variant | `to-llm-message.ts:229-238`; `ai/src/schema/messages.ts:280-281` | hist; stripped on routes without support (`ai/src/effort-updates.ts:16-17`) |
| M19 | Compaction checkpoint: one user message `<conversation-checkpoint>…<summary>…</summary>[<recent-context>…</recent-context>]</conversation-checkpoint>`, or a native window replayed as messages | completed compaction | `to-llm-message.ts:293-312`; recent text rebuilt from `metadata.keepID` (`history.ts:231-265`) | first message until the next compaction |
| M20 | Assistant text, reasoning (downgraded to text after a model/provider switch or error), tool calls; provider-executed tool results inline | model output | `to-llm-message.ts:155-220` | hist |
| M21 | Tool results (tool role, see 1.4) | tool settles | `to-llm-message.ts:127-153, 208-219` | hist |

Emit nothing: `agent-switched` and `idle` rows (`to-llm-message.ts:242-244`); retry state (`assistant.retry` is not
lowered, `to-llm-message.ts:155-220`); snapshots; todos (no injection exists, see A3).

### 1.3 Injected by built-in hooks, or appended per request (not from a stored row)

| # | Content | Trigger | Built at | Lifetime |
|---|---|---|---|---|
| H1 | Plan enter/leave reminder spliced before the last user message when the request lacks the right one; also persisted as M14 | agent is `plan` and the last reminder seen is not "enter", or the reverse | `plan.ts:55-73, 110-115` | step (+ hist copy) |
| H2 | Goal reminder `<system-reminder>\nA goal is active …<untrusted_objective>…` spliced before the last user message when no user message holds the objective | active goal | `goal.ts:218-231`; `plugin/goal/reminder.txt` | step, never stored |
| H3 | `CRITICAL - MAXIMUM STEPS REACHED …` as a trailing **assistant** message, with `toolChoice:"none"` | agent `steps` reached | `llm.ts:249-268`; `core/src/session/runner/max-steps.ts` | step |

### 1.4 Tool-result content conventions (inside M21)

| # | Content | Built at |
|---|---|---|
| R1 | Generic truncation over 2000 lines / 50 KiB: head kept, then `[showing lines 1-N of M; full output saved to <data>/tool-output/tool_…]`; file kept 7 days | `core/src/tool-output.ts:13-16, 69-120` |
| R2 | `read`: `Read file <path>, lines a-b` + `n: <line>` numbering; `[Output truncated. Continue reading with offset: N]`; images/PDF as file content | `read.ts:170-207` |
| R3 | `edit`: `Edited <file> (N replacements)`; `write`: `Wrote/Created file successfully: <path>` (no content) | `core/src/tool/plugin/edit.ts:316-319`; `core/src/tool/plugin/write.ts:39-40` |
| R4 | `shell`: output + exit notice; background start: `Command moved to the background (shell ID: …).\nOutput is streaming to: <file>` plus "DO NOT poll" text | `shell.ts:24-25, 77-97` |
| R5 | `subagent` foreground: `<subagent sessionID="…" state="completed">…</subagent>`; background: `The subagent is working in the background (sessionID: …) …` | `subagent.ts:19-28, 255-266` |
| R6 | `question`: `User has answered your questions: "q"="a", …` | `core/src/tool/plugin/question.ts:38-46` |
| R7 | `todowrite`: `Todos updated: k/n completed …` (list itself is only in the call input) | `core/src/tool/plugin/todo.ts:30-41, 73-77` |
| R8 | `skill`: `<skill_content name=…>` with base dir and sampled `<skill_files>` | `core/src/tool/plugin/skill.ts:46-66`; `skill.ts:35-53` |
| R9 | Errors (tool role, `result.type:"error"`): tool failure text; `The user declined this tool call`; `The user dismissed this question`; reject-with-message feedback text (built-in and MCP tools); `Tool execution interrupted[: name (sessionID: …)]`; `Tools are disabled after the maximum agent steps` | `core/src/session/runner/step.ts:93, 207-215`; `core/src/session/to-session-error.ts:75-81`; `core/src/permission.ts:61-67, 302-322`; `core/src/tool/mcp.ts:108-114`; `llm.ts:409-432` |
| R10 | Media the model cannot take: `[N image(s) omitted: …]` | `core/src/tool.ts:109-118` |

### 1.5 Tool definitions (`event.tools`: name → `{description, input}`)

| # | Content | Built at |
|---|---|---|
| T1 | Built-in tools (read, edit, write, patch, glob, grep, shell, webfetch, websearch, question, todowrite if `experimental.todo`, skill, subagent, image, MCP resource tools, `execute`), filtered by permissions | `internal.ts:245-261`; `core/src/tool.ts` |
| T2 | Descriptions rewritten per request: subagent list appended; shell description for the detected shell | `subagent.ts:288-312`; `shell.ts:277-283` |
| T3 | Set curated per request: `patch` for `gpt-*` models, else `edit`/`write`; `websearch` removed when disabled | `core/src/tool/plugin/patch.ts:319-332`; `core/src/tool/plugin/websearch.ts:184-192` |
| T4 | MCP tools `<server>_<tool>`; plugin tools via `tool.transform` | `tool/mcp.ts`; `plugin/src/effect/tool.ts` |

The hook may rename or remove tools. Tools it invents are dropped (`model-request.ts:277-285`).

### 1.6 Rewrites after the hook (Personant's output is still changed)

| # | Content | Built at |
|---|---|---|
| P1 | Media the model does not support → `ERROR: Cannot read "…" (this model does not support … input). Inform the user.` | `model-request.ts:135-150, 187-191, 309` |
| P2 | Past 25 MiB of inline images, oldest → `[This image was removed to reduce the request size …]` | `model-request.ts:43-46, 193-221, 309` |
| P3 | Effort markers stripped unless the route supports them | `effort-updates.ts:16-25` |
| P4 | openai-chat: system parts joined with `\n`; system-role messages → user `<system-update>` with `&<>` escaped | `ai/src/protocols/shared.ts:186-199`; OPENCODE_PREPROBE_RESULTS.md §5 |

**Requests that do not go through `session.context`:** title (`session.title`), `session.generate` (the goal judge,
`goal.ts:123`; its own `generate` hook, `model-request.ts:428`), and the compaction request (`session.compaction`).
The compaction summary prompt and nudge are appended after that hook (`compaction.ts:477-495`).

## 2. Detection in the `session.context` input

**Id join.** Messages built from stored rows carry `id` = the stored message ID: user, synthetic, skill, assistant,
location-switched, compaction (`to-llm-message.ts:250, 264, 275, 277, 299`). Messages with no `id` come from H1–H3,
M6, M18, M21 and native windows. The plugin can read the stored messages, with `type` and `metadata`, through
`session.context` on its session API (`plugin/src/effect/session.ts:201-217`; `plugin/src/promise/adapter.ts:619`;
used the same way at `goal.ts:92`). Joining on `id` recovers what the hook input loses. Synthetic messages lose
their metadata in the conversion (`to-llm-message.ts:275`).

| # | Hook-input signature | Stored-row signal (via id join) | Reliability |
|---|---|---|---|
| S1/S2 | `system[0]` | — | reliable by position. Content varies by agent and model family |
| S3 | `system[1]`, text starts `# Your Model\n- Name:` | — | reliable |
| S4 | the part with `metadata.experimental.instructions`; per-source text by `key` | — | reliable. Keys `core/*`, `api/*` as in 1.1 |
| S5 | last part, exact text (`opencode.ts:101-103`) | — | reliable (exact match) |
| M1 | role user, id, text part(s), no attachment metadata | `type:"user"` | typed text vs command expansion: **not distinguishable** (no marker in either the hook or the stored row) |
| M2 | role user, leading text part starting `<skill_content name=` | `user.skills[]` | reliable via stored row; by text, spoofable |
| M3 | text part with `metadata.attachment {source,name,description}`; `media` parts; `Attached file:` text before media (no metadata) | `user.files[]` | text/dir reliable by metadata; image/PDF label text by prefix only |
| M4 | user `metadata.agents` | same | reliable |
| M5 | role user, id, raw skill body | `type:"skill"`, `skill`, `name` | **id join needed**: by text, indistinguishable from a prompt |
| M6 | role system, single text part, no id | `type:"system"`, `metadata.notice:"instructions"`, `instructionSources` (`message-updater.ts:159`) | role is reliable (only M6 and M18 are system role). Which source changed: **text pattern only**, no id to join |
| M7 | role user, id, starts `Instructions from: ` | synthetic `metadata.instruction.paths` | reliable via id join |
| M8 | role user, id, starts `<system-reminder>Note: The user opened the file` / `…The user selected` | synthetic, **no metadata** | text prefix only |
| M9 | starts `The following shell command was executed by the user:\n<shell ` / `User shell command failed to start:` | `metadata.source:"shell"`, `shellID`, `state`, `exit`, `truncated` | reliable via id join |
| M10 | starts `<shell id="` | `metadata.source:"shell"`, `jobID` | reliable via id join |
| M11 | starts `<subagent sessionID="` | `metadata.source:"subagent"`, `childID`, `agent`, `state` | reliable via id join (hook text alone: spoofable) |
| M12 | starts `<image id="` | `metadata.source:"experimental_image_generate"`, `jobID`, `path` | reliable via id join |
| M13 | exact prefix `User requested that active blocking work be moved to the background.` | synthetic, no metadata | text prefix only |
| M14 | exact enter/leave strings (`plan.ts:14-25`; enter embeds `~/.opencode/plan`) | synthetic, no metadata | exact match, reliable |
| M15 | contains `<untrusted_objective>` | `metadata.source:"goal"`, `goalID` | reliable via id join |
| M16 | exact strings | interrupt/length: no metadata; restart: `metadata.notice:"restart"` | exact match, reliable |
| M17 | exact prefix `The working directory has been changed to ` | `type:"location-switched"` | reliable |
| M18 | role system, content `[{type:"effort"}]` | `type:"model-switched"` | reliable |
| M19 | role user, id, starts `<conversation-checkpoint>` | `type:"compaction"`, `summary`, `metadata.keepID` | reliable |
| M20 | role assistant, id; `text`/`reasoning`/`tool-call` parts | `type:"assistant"` | reliable |
| M21 | role tool, `tool-result` part `{id: callID, name, result.type}` | tool part in the assistant row (`state.metadata`: `truncated`, `outputPath`, `todos`, `answers`, `sessionID`) | reliable by `name` and call id; R1/R2 markers by text |
| H1 | role user, **no id**, exact plan string | — | exact match |
| H2 | role user, **no id**, starts `<system-reminder>\nA goal is active` | — | exact match |
| H3 | role assistant, **no id**, last, starts `CRITICAL - MAXIMUM STEPS REACHED` | — | exact match |
| T* | `event.tools` keys; MCP by `<server>_` prefix; Code Mode `execute` | — | reliable |

**Not reliably distinguishable:**
1. Slash-command and MCP-prompt expansions vs typed prompts (M1). Neither the `session.prompt` hook nor the stored row
   carries a marker (`config/plugin/command.ts:121-126`; `plugin/command.ts:34-41, 74-80`).
2. User-invoked skill bodies (M5) without the id join.
3. Which instruction source an M6 update is about. The hook message has no id and no metadata. The stored row has
   `instructionSources`, but nothing in the hook input links to it.
4. Editor context, background-move and interrupt/length notices (M8, M13, M16): marked by text only. A user who types
   the same text produces the same input.
5. The synthetic families without the id join (M9–M12, M15): their markers are user-typeable XML prefixes.
6. Messages that arrive mid-turn by `steer` (M1) look like any user prompt. Only their position after tool results
   shows they arrived mid-turn.

The `session.prompt` hook sees only M1–M4 (`prompt.ts:42-53`). Synthetic admission has no hook
(`session.ts:286-327`), so M7–M17 are visible only in `session.context` and the event stream.

## 3. Behaviour that assumes earlier content is still visible to the model

"Code" means opencode enforces the behaviour from state outside the request, so dropping context does not change it.
"Model" means only the model's view carries it.

| # | Behaviour | Relies on | Where enforced | Without it in the next request |
|---|---|---|---|---|
| A1 | Truncated output (R1, R2) | The marker with the saved path or `offset` | file on disk 7 days: code; pointer: model | The model cannot continue reading or find the saved file. It re-runs the tool |
| A2 | Edit from read output | The edit description tells the model to copy exact text from Read output (`edit.ts:222`). **No read-before-edit check exists**: edit only needs a unique exact `oldString` (`edit.ts:233-296`) | match check: code | The model guesses `oldString`. Edits fail with `Could not find oldString …` (safe; costs round trips) |
| A3 | Todo tracking | "Send the full list on every call; it replaces the previous one" (`todowrite.txt`). The list exists only in the last `todowrite` call input; the result is a count summary (R7) | model | The list and its progress are lost. The model rebuilds it, or overwrites the user-visible list |
| A4 | Question answers | Answers exist only in R6 text (and tool metadata) | model | The user's choices are lost. The model asks again or proceeds without them |
| A5 | Plan mode | M14/H1 reminder. Edit denial outside `~/.opencode/plan` is a permission rule (`plan.ts:33-53`) | edit block: code; awareness: model | H1 re-adds the reminder only when the **pre-Personant** input lacks it. The stored M14 usually satisfies that, so if Personant drops it the model is not told it is in Plan mode. Edits still fail, now as errors |
| A6 | Leaving Plan mode | The "NO LONGER in Plan mode" reminder supersedes the earlier one | model | If the enter reminder survives in recall and the leave reminder does not, the model keeps refusing edits |
| A7 | Subagent continuation | R5/M11 carry `sessionID`; continuing a child needs that ID. The direct-child check is in code (`subagent.ts:171-174`) | model (ID); code (check) | The model cannot continue or reference the child, and may redo its work |
| A8 | Background work | R4/R5 tell the model "DO NOT poll; you will be notified". The notice (M10/M11/M12) arrives later as one synthetic message | wake-up: code (`llm.ts:172-182`); result: model | If the notice is not composed into the request it wakes, the result is lost. The model was told not to poll for it |
| A9 | Nested `AGENTS.md` (M7) | Injected once per path. Re-injected only after the synthetic leaves **stored** history (compaction or revert), not the hooked request (`instructions.ts:40-45, 100-113`) | model | **Silently lost.** opencode never re-sends it while the stored copy exists |
| A10 | Instruction updates (M6) | Deltas against the epoch baseline: diffs, "no longer apply", "in addition to those previously listed". The S4 baseline stays at the old values until the next compaction (`instruction-state.ts:172-182`) | model | Dropping M6 leaves the model on stale AGENTS.md, date, cwd, skills or MCP lists. Keeping a diff without its baseline is meaningless |
| A11 | Skill already loaded | Guidance: "A skill that is already present this way does not need to be invoked again" (`skill/instructions.ts:29`) | model | The model re-invokes the skill (another `skill` permission check and tool round trip) |
| A12 | Continue notices (M16) | "Continue from where you left off without repeating" assumes the partial assistant output is in the request | model | The model restarts the answer or repeats work |
| A13 | Max steps (H3) | Tools refused in code (`step.ts:93`) | code | No break. H3 must stay last (a trailing assistant message) |
| A14 | Permission reject with feedback (R9) | The feedback is the tool error text | model | The model retries what the user rejected |
| A15 | Images removed (P2) | opencode tells the model not to recall the removed image | model | n/a. opencode itself expects the model to forget |
| A16 | Revert | Committed revert deletes rows (`projector.ts:877-894`); files restored from snapshots | code (files, rows) | Memory derived from reverted turns stays unless the service retracts it on `session.revert.committed` (`core/src/session/revert.ts:77`). Plan reminders can be stripped (`plan.ts:55-56`) |
| A17 | `options.maxTokens` | Fitted to opencode's estimate of **its own** history (`model-request.ts:89-98, 268-273`) | code | With a smaller composed request the limit is too low. The hook may reset it |
| A18 | Auto-compaction | Triggered by provider-measured usage of the composed request plus a char estimate of stored rows since then (`compaction.ts:422-431`) | code | A small composed request keeps it from firing (consistent with OPENCODE_INTEGRATION_OPTIONS.md §2.7) |
| A19 | Goal loop | Judge and progress checks read **stored** history (`goal.ts:91-102, 123`). H2 is re-added when the objective is missing from the pre-Personant input | code | Unaffected by composition. H2 lands in Personant's input only when stored history lacks the objective |

Likely to break under budgeted composition, in order: A9, A10, A8, A3, A5/A6, A4, A7, A1. These all fail without
an error. A2 fails loudly, and A13/A16-files/A19 are held in code.

## 4. `/compact` and `/clear`

### 4.1 `/compact`, end to end

1. TUI command `session.compact`, slash `compact` (`tui/src/routes/session/index.tsx:1083-1104`), calls
   `data.session.compact`. That first calls `switchModel` with the composer's model (`client/src/solid/data.ts:1691-1714`;
   a no-op when unchanged, `core/src/session/session.ts:111-116`), then `POST /api/session/{id}/compact`
   (`protocol/src/groups/session.ts:441-458`; `server/src/handlers/session.ts:418-432`).
2. `Session.compact` commits any staged revert, admits a `compaction` inbox item and wakes the session
   (`session.ts:259-277`). No plugin hook runs at admission.
3. The runner delivers the item at the next boundary, publishes `Compaction.Started{reason:"manual", inputID}`, and
   calls `compaction.compact({reason:"manual"})` over the previewed stored history (`llm.ts:100-171`). Manual runs
   even when `compaction.auto` is false (`compaction.ts:295-296`).
4. Summary path: if there is nothing to split, it fails before any hook (`compaction.ts:461-462`). Otherwise it builds
   the request from the stored transcript and runs `session.compaction` (`compaction.ts:834-851`). If the hook sets
   `result`, the summary is taken from it and no model call is made (`compaction.ts:606, 854-866`).
   `result.metadata.keepID` overrides the kept range (`compaction.ts:524-526`).
5. `Compaction.Ended` is published (`compaction.ts:908-920`). The projector stores the completed compaction and
   advances the instruction epoch (`projector.ts:848-853`). From then on, history reads start there and replay M19
   first (`history.ts:192-214`). A control-only drain skips `experimental.idle` (`llm.ts:172-175`).

**Least intrusive no-op.** No hook can refuse admission or cancel a manual compaction, so some stored compaction
always lands (or a failure, when there is nothing to compact). The minimal footprint:

- Answer `session.compaction` with `result: {summary}`, with no model call. Personant's probe already does this
  (OPENCODE_PROBE_RESULTS.md §6).
- The model-visible effect is nil if the composer never forwards M19. What remains is opencode-side: stored history
  is bounded, a compaction row appears in the TUI, and the S4 baseline is refreshed to current values (M6 updates
  before it fold into the baseline).
- To keep opencode's own transcript text intact, set `result.metadata.keepID` to the first message ID of the
  compacted range, so the whole range is kept as `<recent-context>` text (`history.ts:231-265`). Whether that is
  useful depends on whether the composer reads opencode history at all.
- The hook event has no `reason` field (`plugin/src/effect/session.ts:43-51`). To tell a user `/compact` from
  Personant's own checkpoint compactions or auto ones, subscribe to `session.compaction.started{reason, inputID}`
  (`llm.ts:101-104`; `compaction.ts:316`), or track the IDs of compactions the plugin itself enqueued.
- `compaction.auto:false` in config removes auto and overflow compactions (`compaction.ts:296`). Manual ones remain.

### 4.2 `/clear`, end to end

- TUI command `session.clear`, slash `clear` (`tui/src/app.tsx:800-825`). It is **client-side only**: it closes
  session tabs, navigates to the home route with a new-session location, and keeps the agent and model selection. No
  server request is made at that moment. `/new` is the same without closing tabs (`app.tsx:773-797`).
- The next submit creates a session (`data.session.create`, `tui/src/component/prompt/index.tsx:1239-1250`). The
  server then emits `session.created`, records a new instruction baseline, and starts title generation on the first
  promoted prompt (`llm.ts:195-198`). If the agent is `plan`, the plan plugin injects M14 (`plan.ts:100-103`).
- **No-op mechanics.** The plugin cannot intercept a TUI-only command. Treat it as no-op in the memory service:
  - Key memory on project/user, not `sessionID`.
  - On `session.created` with no `parentID` and no fork, continue the working set rather than start one. Forks arrive
    as `session.forked` (OPENCODE_INTEGRATION_OPTIONS.md §2.3).
  - Personant composes from its own working set, and the new session's opencode history is empty. So the only loss is
    opencode's live-turn tail, which the composer owns anyway.
  - Overriding the slash command from a TUI plugin is **unverified**.

## 5. Candidate Personant handling (judgment, not fact)

Classes: **E** always-on directive layer; **A1** project-scoped; **T** transient task-class (staged, GC'd unless a
decision cites it); **R** recall-backed (persisted, re-fetched by tag); **L** live-turn only; **D** drop.

| # | Injection | Class | Reasoning |
|---|---|---|---|
| S1/S2 | Agent/default/family prompt | E (replace with Personant's composed system text) | Infrastructural; THESIS "behavior is sculpted" |
| S3 | Model identity | D or E (one line) | Small. Only useful if the model must name itself |
| S4-I5 | AGENTS.md (global + project) | global → E; project → A1 | Behavior vs project conventions |
| S4-I1/I2/I4 | Code Mode catalog, MCP guidance, skill list | E (rendered only while those tools are exposed) | Tied to the tool surface |
| S4-I3 | Project references | A1 | Project-scoped |
| S4-I6/I7 | Date, env block | E (re-rendered every request) | Cheap, always current; avoids A10 staleness |
| S4-I8 | API context entries | A1 or E by key | Unknown producer |
| S5 | Worktree hint | D unless Code Mode session_move is exposed | Infrastructural tax |
| M1/M4 | User prompt / mentions | L, then decision-class delta | Primary input |
| M2/M5/R8 | Skill content | T while the task runs; R by skill ID | A11: re-loadable on demand |
| M3 | Attachments | L; text bodies T | Artifacts are not memory (THESIS) |
| M6 | Instruction updates | Fold into E/A1 re-render; D the message | Avoids A10 by keeping no stale baseline |
| M7 | Nested AGENTS.md | A1 (keyed by path), kept while that subtree is engaged | A9: opencode never re-sends it |
| M8 | Editor context | L | One-moment value |
| M9/M10/R4 | Shell output | T | Task-class source (ARCHITECTURE "Transient-data lifecycle") |
| M11/R5 | Subagent results | T body; sessionID R | A7/A8 |
| M12 | Image notice | T | — |
| M13/M16 | Background-move, continue notices | L (one step) | A12: keep the partial assistant output with them |
| M14/H1 | Plan reminder | E while agent = plan (from session state, not history) | A5/A6. Deterministic: render from `session.agent` |
| M15/H2 | Goal kickoff/continuation/reminder | L; objective E while the goal is active | A19 code-held |
| M17 | cwd change | E (env re-render) | Same as I7 |
| M18 | Effort marker | D | Cache artefact |
| M19 | Compaction checkpoint | D | Personant's memory replaces it (§4.1) |
| M20 | Assistant text | decision-class delta; L tail | — |
| M20 tool calls | — | L; `todowrite` input → R (latest list) | A3 |
| R1/R2 | Truncated outputs | T, with the marker pointer kept in L | A1 |
| R6 | Question answers | decision-class (R) | A4 |
| R9 | Errors / reject feedback | L; reject feedback decision-class | A14 |
| H3 | Max-steps prefill | L, must stay last | A13 |
| T1–T4 | Tool definitions | Pass through, curated per agent | Tool surface is a role decision |
| P1–P4 | Post-hook rewrites | n/a (outside the composer) | Emit one system part and no system role (PP §5) |

## 6. Unverified

- That plugin activation order equals hook registration order in all reload cases. The plugin dir is file-watched,
  and a reload could re-register Personant's hooks after others (OPENCODE_PREPROBE_RESULTS.md §6 item 11).
- Lowering of system-role messages and effort markers on non-openai-chat routes.
- Whether a TUI plugin can replace or disable the `/compact` or `/clear` slash commands.
