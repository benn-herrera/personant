# opencode pre-probe: unmodified opencode in a loop with a mocked model

**What this is.** A cheap, throwaway measurement taken before the probe in ACTIVE_PLAN.md is built. Real,
unmodified opencode (`/opt/homebrew/bin/opencode`, `opencode v2.2.1`) runs headless under this account. A
deterministic OpenAI-compatible mock serves the model, a logging-only server plugin records hooks and events, and a
Python driver sends prompts over the HTTP API. All code, data and logs are under
`.claude-temp/preprobe/` (untracked). Source citations are to the v2.2.2 clone at `../opencode`.

**Conventions.** Unlabelled lines are measured facts, with the run directory under `.claude-temp/preprobe/runs/`.
Lines starting **Judgment:** are interpretation.

## 1. Setup

| Piece | File | What it does |
|---|---|---|
| Mock model | `mock_model.py` | Python stdlib. Serves `POST */chat/completions` (SSE stream and non-stream) and `GET */models`. Picks its reply from the `[turn N]` tag in the latest tagged user message and the number of assistant messages after it. Turn kinds cycle by `N % 5`: plain text, `read`, `shell`, `read`+`shell` in one response, a 2 KB text. Tool-call arguments are split across two SSE chunks; usage arrives on a final `choices: []` chunk (`prompt_tokens` = request bytes / 4). Logs per request: arrival, done, duration, message count, request bytes, raw and ID-normalized body hash. |
| Plugin | `plugin/index.ts` | Promise-API server plugin (`{id, setup}`), loaded from a directory path in `plugins`. Appends one JSON line per hook call and per subscribed event, with `performance.timeOrigin + performance.now()` and a sequence number. In checkpoint mode it calls `ctx.session.compact` from `experimental.idle` after every Nth prompt and answers `session.compaction` with `result: {summary: "PREPROBE CHECKPOINT …"}`. In fidelity mode, see §5. |
| Driver | `driver.py` | Python stdlib. Builds a run home, fixture workspace (own `git init`) and config. Starts the mock, then `opencode serve --stdio --port 0`, reads `{url}` from stdout line 1, and opens `GET /api/event`. Creates one session, then for each turn posts `POST /api/session/{id}/prompt` and waits for `session.execution.succeeded` on the SSE stream. Shuts down by closing opencode's stdin, then reaps every process. |
| Analysis | `analyze.py`, `fidelity.py`, `userdb_check.py` | Per-run stats, cross-run repeatability, growth buckets; prompt-text fidelity; read-only check of the user DB (queries a copy). |

Environment for opencode: `HOME`, `TMPDIR`, `XDG_{CONFIG,DATA,STATE,CACHE}_HOME` all under the run home;
`OPENCODE_CONFIG=<run>/opencode.json`; `OPENCODE_DISABLE_PROJECT_CONFIG=1`, `OPENCODE_DISABLE_CLAUDE_CODE=1`,
`OPENCODE_DISABLE_MODELS_FETCH=1`; random `OPENCODE_PASSWORD`. Config: `providers.mock` with
`package: "@ai-sdk/openai-compatible"` and `settings.baseURL`, `model: "mock/mock-model"`, `compaction.auto: false`,
`permissions: [{action:"*", resource:"*", effect:"allow"}]`, the plugin entry, `update: "disable"`,
`share: "disabled"`.

Host: macOS, M3 Max. Everything runs on one machine. A user-owned `opencode serve --service` (pid 50802, started
2026-10-29 16:15, `HOME=/Users/agent-user`) was running throughout and holds the user DB open. It is not part of
this work and was left alone.

## 2. Mechanics verdicts

| Mechanic | Verdict | Evidence |
|---|---|---|
| Headless serve under this account | **Works.** `{url}` on stdout line 1; `/api/info` 200 after 0.30–0.34 s; Basic auth `opencode:<OPENCODE_PASSWORD>` accepted. Closing stdin ends the server in about 0.04 s with exit code 0. | `run.json` `ready_s`, `shutdown_s`, `procs` in every run |
| Isolation (user DB untouched) | **Holds.** Size and mtime of `~/.local/share/opencode/opencode.db` and `-wal` are identical before and after every run. A query of a copy finds 0 `session_v2` rows and 0 `project_directory` rows under the preprobe directory, before and after (66 `session_v2` rows both times). Each run's own DB lives in `<run>/home/data/opencode/`. | `userdb/checks.jsonl`; `run.json` `user_db_before/after` |
| Mock provider, streamed tool calls | **Works.** opencode always streams (`stream: true` plus `stream_options.include_usage`; `packages/ai/src/protocols/openai-chat.ts:909-910`), so the mock's non-streaming path never ran. Tool calls whose arguments are split across chunks parse correctly, including two parallel calls in one response. Usage from the final chunk reaches `session.step.ended.tokens` unchanged. | `rep*/plugin.jsonl` `hook.tool.execute.after.input`; `step.ended.tokens` |
| Plugin loading | **Works** from a directory path in `plugins` (`index.ts` resolved; `loading plugin` in the isolated opencode log). No `@opencode/plugin` import is needed. The file watcher subscribes to the plugin directory. | `<run>/home/data/opencode/log/opencode.log` |
| `session.prompt` | Fires once per prompt, before `session.inbox.enqueued`. | all runs |
| `session.context` | Fires once per primary model step: 800 per 500-turn run (500 turns + 300 tool follow-up steps). No title or compaction requests pass through it. | `analyze.py run` record counts |
| `tool.execute.after` | Fires once per tool call (400 per run), with `input`, `status`, `result.content`. | same |
| `session.model.request` | Fires for every model request with `kind`: `primary` 800 and `title` 1 per 500-turn run. **It also fires with `kind: "compaction"` for every plugin-answered compaction (5000 in `growth-ckpt8`), although no request is sent.** The compaction request is built through the request hooks (`compaction.ts` `prepare` → `requests.compaction`) before the supplied `result` short-circuits the call (`fromHook`). A `model.request` hook call is therefore not evidence of a model call. | `analyze.py run growth-ckpt8`; mock log |
| `session.compaction` + plugin `result` | **Works; no model call.** 5000/5000 checkpoints in `growth-ckpt8`, 0 errors. `session.compact` called from inside `experimental.idle` returns in 0.47 ms median (max 13.7 ms). The compaction runs **in the same execution**, before `session.execution.succeeded`, in 5000/5000 cases. The hook's `result.summary` is used verbatim (`session.compaction.ended.text`). The mock receives no compaction request. | `growth-ckpt8/plugin.jsonl`, `turns.jsonl` |
| `experimental.idle` | Fires once per turn, **and once more after a compaction the hook itself enqueued**: the drain loops back, runs the compaction, and reaches the idle boundary again. In `growth-ckpt8` that is 45,000 idle calls for 40,000 prompts and 5000 checkpoints. A plugin that counts idles as turns must deduplicate; this one keys on its own prompt count. | `growth-ckpt8/plugin.jsonl` |
| Event subscription (`ctx.event.subscribe`) | Step, text, tool, execution, inbox and compaction events all delivered. **`session.usage.recorded` is not subscribable**: the server logs `ignoring unknown event types`. It is published (`core/src/session/title.ts:64`, `compaction.ts:885`) but is not in the public event manifest. The public event is `session.usage.updated`. `session.status` and `session.idle` are accepted but never published in these runs; core has no publisher for them. | isolated opencode log; `rep*/plugin.jsonl` |
| Can `experimental.idle` run before the turn's last text/step event? | **Yes for `step.ended`, every time: 48,158/48,158 turns** across `rep1`–`rep5`, `growth-nockpt` and `growth-ckpt8`. In the plugin's own log, the idle hook comes before the final `session.step.ended` (and its `session.usage.updated`) and before `session.execution.succeeded`. In source, `step.ended` is published inside `runStep` (`core/src/session/runner/step.ts:249`) before the runner reaches the hook (`runner/llm.ts:172-182`); the subscription fiber has not delivered it yet. The same lag shows for compaction: the `session.compaction` hook runs before the plugin receives `session.compaction.started`. `session.text.ended` arrived before the idle hook in 48,158/48,158 turns, but nothing in source orders that delivery. | `analyze.py run …` "ordering" line |

## 3. Repeatability (run A)

Five identical runs `rep1`…`rep5`: 500 turns each, fresh run home each time, same script and same-length run names.
Compared with `python3 analyze.py repeat rep1 rep2 rep3 rep4 rep5`.

**Identical in every aligned record across all five runs:**
- Mock side (801 requests per run: 800 primary + 1 title): message count, role sequence, request bytes, response
  bytes, usage, and the request-body hash after normalization. Normalization replaces opencode IDs and the
  `/runs/<name>` path segment.
- `session.context` input (800 calls): message count, roles, message bytes, ID-normalized message hash, system part
  count and bytes, normalized system hash, tool list, options.
- `tool.execute.after` (400 calls): status, input, result bytes, and the full output text (raw hash identical, not
  just normalized).
- Token counts in `session.step.ended`.
- Order of every hook call and session-scoped event per turn, as the plugin sees it: 501/501 turn slices identical.
  The driver's SSE stream, filtered to `session.*`, is identical too.

**Varies across runs:**
- IDs: session, message, event (`evt_…`, time-ordered) and inbox IDs. These make the raw `session.context` message
  hash differ in 800/800 calls.
- The run directory path. The system prompt contains the workspace path three times, so the raw request body hash
  differs in 800/801 requests. The title request is the exception. Byte length is equal because the names are the
  same length.
- Mock port, and therefore `baseURL` in `session.model.request`.
- Timestamps.
- **Ordering of non-session events on the SSE stream only.** `shell.exited` and `shell.deleted` swap with the
  adjacent `session.tool.success` in 30–38 of 500 turns per run, all of them turns with a `shell` call. The multiset
  of events per turn is identical.

**Timing** (turns ≥ 2; turn 1 is 640–670 ms because it also runs the title request and first-use setup):

| Run | wall median / p90 / CV (ms) | overhead median / p90 / CV (ms) |
|---|---|---|
| rep1 | 63.6 / 99.6 / 0.37 | 62.5 / 96.1 / 0.37 |
| rep2 | 62.8 / 103.6 / 0.38 | 61.7 / 100.5 / 0.37 |
| rep3 | 62.4 / 100.2 / 0.37 | 61.1 / 96.9 / 0.37 |
| rep4 | 64.0 / 101.3 / 0.37 | 62.9 / 98.3 / 0.36 |
| rep5 | 63.3 / 100.3 / 0.36 | 61.9 / 97.3 / 0.36 |

- Across runs: the run medians span 1.6 ms (CV 0.009). The per-turn CV across the five runs has median 0.067 and
  p90 0.111.
- Within a run, CV is about 0.37. Most of it is structure, not noise: median wall by turn kind is 42 ms for text,
  77 for `read`, 81 for `shell`, 84 for `read`+`shell`, and 42 for long text. The other part is growth over the 500
  turns (§4).
- Mock time is 1.5 ms median per turn, so overhead ≈ wall.

**Judgment:** with a deterministic model, opencode is deterministic in everything that matters for memory-system
measurement: context content, tool output, hook order and token accounting. Only IDs, paths, ports and timestamps
vary, plus one benign non-session event interleave. A probe can compare runs byte-for-byte after normalizing IDs and
the run path. Timing is stable to about 1–3% at the run-median level and about 7% per turn.

## 4. Growth (run B)

Two runs, each capped at 30 minutes of driving, same script as run A:
- `growth-nockpt`: no checkpoints. Reached 5,658 turns in 1801 s.
- `growth-ckpt8`: checkpoint every 8 prompts. Hit the 40,000-turn cap in 1770 s.

"Overhead" = driver turn wall time minus the mock's own time for that turn's requests (parsing included).
"ctx" = the first `session.context` call of each turn. All values are medians per bucket.

**Without checkpoints** (`python3 analyze.py growth growth-nockpt 500`):

| Turns | overhead median / p90 (ms) | ctx messages | ctx bytes | mock request bytes |
|---|---|---|---|---|
| 2–500 | 64.1 / 99.0 | 851 | 447 K | 413 K |
| 1001–1500 | 164.8 / 202.1 | 4,250 | 2.24 M | 1.97 M |
| 2001–2500 | 274.0 / 317.9 | 7,650 | 4.03 M | 3.54 M |
| 3001–3500 | 387.1 / 433.9 | 11,050 | 5.83 M | 5.11 M |
| 4001–4500 | 497.2 / 544.4 | 14,450 | 7.63 M | 6.67 M |
| 5001–5500 | 611.5 / 662.9 | 17,850 | 9.42 M | 8.24 M |
| 5501–5658 | 659.5 / 693.7 | 18,970 | 10.01 M | 8.75 M |

- Context grows a steady 1.79 KB and 3.4 messages per turn.
- Overhead rises linearly with no knee: about +55 ms per 500 turns, OLS slope 94 ms per 1000 turns, intercept
  36 ms. That is about 62 ms per MB of `session.context` input.
- Mock time also grows, from 1.5 to 37 ms per turn, because the mock parses the larger body. That time is excluded
  from overhead.
- opencode RSS: 651 MB at turn 100, 943 MB at 500, 1.01 GB at 1000, 1.34 GB at 5000. Isolated DB: 24 MB at 5,658
  turns.

**With checkpoints every 8 prompts** (`python3 analyze.py growth growth-ckpt8 2000`):

| Turns | overhead median / p90 (ms) | ctx messages | ctx bytes | mock request bytes |
|---|---|---|---|---|
| 2–2000 | 47.2 / 57.1 | 14 | 9.5 K | 30.0 K |
| 10001–12000 | 47.7 / 56.7 | 14 | 9.6 K | 29.8 K |
| 20001–22000 | 49.8 / 59.7 | 14 | 9.6 K | 29.8 K |
| 30001–32000 | 51.3 / 61.2 | 14 | 9.6 K | 29.8 K |
| 38001–40000 | 51.5 / 61.3 | 14 | 9.6 K | 29.8 K |

- `session.context` input is flat: 14 messages and about 9.5 KB median. After a checkpoint, history starts with one
  `<conversation-checkpoint>` user message. It holds the plugin's summary plus a `<recent-context>` block with the
  kept tail serialized as text, because the plugin passed no `keepID`.
  Sample: `runs/smoke-ckpt/context-sample.json.after-compaction.json`.
- Overhead drifts slightly: +4.3 ms over 40,000 turns (47.2 → 51.5 ms; OLS 0.14 ms per 1000 turns).
- RSS: 843 MB at turn 1000, 955 MB at 10,000, 1.02 GB at 40,000.
- The isolated DB grows regardless of checkpoints: 42 MB at 10,000 turns and 169 MB at 40,000, about 4.2 KB per
  turn (the same rate as without checkpoints).

**Against run-A noise:**
- Run A's run-median spread is 1.6 ms (CV 0.009) and its per-turn cross-run CV is 0.067.
- The uncheckpointed rise (≈600 ms over 5,600 turns) is about 370× the run-median spread.
- The checkpointed drift (+4.3 ms) is about 2.7× the run-median spread and about 8% of the level. That is above noise
  but small.

**Judgment:** with checkpoints the per-turn cost is effectively flat. The residual drift tracks something that keeps
growing (stored rows; the DB grows linearly), not the request. That cause is unverified.

## 5. Prompt-text fidelity of `session.context`

**Method.** `driver.py fidelity --turns 16 --fidelity-replace 5,10 --fidelity-control 3,15`.
- On prompts 5 and 10, the first `session.context` call replaces `system` and `messages` with a fixture: 5 system
  parts; a user message with 3 text parts; an assistant text + `read` tool-call; the matching tool-result; a
  mid-conversation `system`-role message; an assistant message with a 112,507-byte single line; and a final user
  message.
- Prompts 3 (both steps, real tool pair) and 15 (72 parts of real history) are controls, left untouched.
- The plugin logs every part as base64 of its UTF-8 bytes. The mock logs every decoded wire string the same way.
- `fidelity.py` reports, per part, where its exact bytes landed, or which transformation makes them match.
- The fixture covers: leading and trailing whitespace; CRLF; lone CR; tabs; é as U+00E9 and as e+U+0301; U+1F600 and
  U+1D11E; U+2028 and U+2029; the >100 KB line; markdown; JSON- and XML-looking text; `&amp;`, `<`, `>`, quotes; an
  empty part in both system and user. The plugin source is pure ASCII; every non-ASCII code point is built with
  `String.fromCodePoint`.
- Report: `runs/fidelity/fidelity-report.txt`.

**Result: every text part arrives byte-exact, at the level of decoded JSON strings.** Controls: 15/15, 22/22 and
72/72 parts exact. Replaced prompts: 12/15 exact. The other 3 are two empty parts and one mid-conversation system
message. Whitespace, CR, CRLF, tabs, NFC/NFD, astral characters, U+2028/U+2029, the 112 KB line, and markdown/JSON/XML
text all pass unchanged. Tool-call input (`JSON.stringify` of the hook's object) equals the wire `arguments` string
byte for byte.

**Structural transformations (OpenAI-compatible chat route), all deterministic:**

| Hook input | On the wire | Source |
|---|---|---|
| N system parts | One `system` message whose content is `"\n".join(parts)`. An empty part shows only as an extra `\n`; part boundaries are not recoverable from the wire. | `packages/ai/src/protocols/shared.ts:186`; `openai-chat.ts` `lowerMessages` |
| N text parts in one user message | One string, `"".join(parts)`. Empty parts vanish and boundaries are lost. | `openai-chat.ts` `lowerUserMessage` |
| Assistant text parts | `"".join(texts)`, with `tool_calls` alongside; `content: null` when there is no text | `lowerAssistantMessage` |
| Tool-result text items | `"\n".join(items)` in one `tool` message | `lowerToolMessages` |
| `system`-role message mid-conversation | **Moves to a `user` message**: `<system-update>\n` + text with `&`→`&amp;`, `<`→`&lt;`, `>`→`&gt;` + `\n</system-update>` | `shared.ts:188-198, 218-224` |

**Judgment:** for text the hook sets, opencode is byte-exact at the part level. A normalization step must
neutralize only (a) part merging (system parts joined with `\n`, user and assistant text parts joined with
nothing) and (b) the `<system-update>` wrapper and its `&<>` escaping for mid-conversation system messages. A
memory service that emits one system part and one text part per message, and puts no `system` role in `messages`,
gets byte-exact delivery with no normalization. Checked only for the OpenAI-compatible chat route; other provider
routes have their own lowering.

## 6. Surprises, and what they mean for the probe (all Judgment)

1. **Turn-close data is not yet delivered when `experimental.idle` runs.** In every one of 48,158 turns, the turn's
   final `session.step.ended`, which carries the step's tokens, reached the plugin's subscription after the idle hook.
   ACTIVE_PLAN.md P4 says "Take usage from the last `session.step.ended`" and "Call `IngestModelResponse` once,
   inside the `experimental.idle` handler". As written, that reads the previous step's usage, or none. Buffered
   `session.text.ended` did arrive before idle every time, but only because of scheduling. Options for P4:
   - Get the final step's data from the server inside the hook, through the plugin's session API.
   - Or take usage from the mock (P2 already records requests).
   - Or move the usage part of `CloseTurn` to `session.execution.succeeded`.
   The missed-event detector should treat "event arrived after idle" as expected, not as a miss.
2. **`experimental.idle` fires twice on checkpoint turns** when the checkpoint is enqueued from the hook. P4's
   `CloseTurn` must be idempotent per turn, or key on the prompt it closes. Otherwise every 8th turn closes twice.
3. **`session.model.request` with `kind: "compaction"` fires even when the plugin answers the compaction.** Any
   probe metric that counts model calls from that hook overcounts by one per checkpoint. Count at the mock instead.
4. **The checkpoint is cheap and synchronous within the turn.** `session.compact` from the idle hook takes about
   0.5 ms, and the compaction finishes before `session.execution.succeeded`. A driver that waits for
   `execution.succeeded` needs no separate compaction wait. P5's turn boundary can stay
   `execution.succeeded`.
5. **opencode's own overhead is about 40–85 ms per turn at a flat context** (by turn kind: about 42 ms for text,
   about 80 ms with tools). Turn 1 costs about 650 ms. That is about 20 turns/s, so P6's rung sizing will be set by
   the memory service and its RPCs, not by opencode. Without checkpoints, overhead grows about 0.1 ms per turn of
   history (62 ms per MB of context). A 1d sim run without checkpoints would make the opencode leg dominate.
   `CADENCE=8` holds it flat to within 4.3 ms over 40,000 turns.
6. **Storage grows about 4.2 KB per turn whatever the checkpoint cadence** (169 MB at 40,000 turns). `oc_db_bytes`
   will rise linearly in every rung. That is expected, not a leak signal.
7. **opencode is deterministic under a deterministic model**, down to tool output text and hook and session-event
   order. P5/P7 comparisons across opencode runs can be exact after normalizing IDs and the run path. A metric that
   differs between two identical opencode runs is a probe defect. The one exception is the interleave of non-session
   `shell.*` events, which is irrelevant to the plugin.
8. **Context text is delivered byte-exact (§5).** P4's composition rule is "system text, history tail as text
   messages". To be byte-exact with no normalization, it should emit one system part and plain text messages, and
   never a `system`-role message, which becomes an escaped `<system-update>` user message.
9. **Every session costs one `title` model request**, made concurrently with turn 1. With `@ai-sdk/openai-compatible`,
   `model.small` finds no smaller model and falls back to the primary model (`core/src/session/context.ts:94-110`). P2's
   endpoint will see it unless the plugin answers `session.title`, as P4 plans to.
10. **Documentation drift found in the existing reports:**
    - `session.usage.recorded` (OPENCODE_INTEGRATION_OPTIONS.md §2.3 event table) cannot be subscribed by a plugin;
      the public event is `session.usage.updated`.
    - Its §2.6 says `provider.<id>`, but the v2 config key is `providers` (`packages/schema/src/config.ts`).
    - `session.status` and `session.idle` are listed as events in the server survey, but nothing publishes them.
11. **The plugin directory is file-watched** (`watcher subscribe … plugin`). Editing the plugin during a run reloads
    it, which is relevant to design open item 5.
12. **RSS starts at about 650 MB and passes 1 GB within a few thousand turns**, with or without checkpoints. That is
    worth a gauge in P5 if long rungs share the machine.

## 7. Reproduce

All from `.claude-temp/preprobe/`; each run directory is deleted and recreated by `driver.py`.

```sh
python3 userdb_check.py before                       # user-DB baseline (stats + query of a copy)
python3 driver.py smoke --turns 6                    # mechanics smoke
python3 driver.py smoke-ckpt --turns 10 --checkpoint 4
python3 driver.py fidelity --turns 16 --fidelity-replace 5,10 --fidelity-control 3,15
python3 fidelity.py fidelity
for i in 1 2 3 4 5; do python3 driver.py rep$i --turns 500 --dump-turns 5; done
python3 analyze.py run rep1 rep2 rep3 rep4 rep5
python3 analyze.py repeat rep1 rep2 rep3 rep4 rep5
python3 driver.py growth-nockpt --turns 40000 --max-seconds 1800 --turn-timeout 120
python3 driver.py growth-ckpt8 --turns 40000 --checkpoint 8 --max-seconds 1800 --turn-timeout 120
python3 analyze.py growth growth-nockpt 500
python3 analyze.py growth growth-ckpt8 500
python3 userdb_check.py after
```
