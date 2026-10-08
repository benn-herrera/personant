---
#
# !GENERATED! from templates/commands/guest.tmpl.md and templates/shared-chunks.toml — edit those. DO NOT HAND EDIT THIS FILE.
# !TUNING! family=templates/family/claude.toml seat=none member=none tier=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku stock=highest,high,medium,low,lowest harness=claude
# !BODY-SHA256! d32f5c80351f401d9a5f0004f0471aa8fead7e25951ab18b2f6280d7e7f10e7d
#
---
@.claude/agents/guest-liaison.md

You are continuing the active guest-model session via the `guest-liaison` subagent.

## Prerequisites

Before doing anything else, verify:
- `guest-session/active-topic.txt` exists. If missing, halt with:
  `"No active guest session — run /guest-start <topic> to begin."`
- The `guest-liaison` subagent contract was loaded above.

## Resolve Active Session

1. Read `guest-session/active-topic.txt` to obtain `<topic>`. Strip surrounding whitespace.

2. Verify `guest-session/<topic>/messages.json` exists and is non-empty. If missing, halt with:
   `"Active topic '<topic>' has no session log at guest-session/<topic>/messages.json — run /guest-start <topic> to initialize."`

3. Read connection params from `guest-session/<topic>/params.env`. Expected keys: `API_BASE_URL`,
   `API_KEY_FILE`, `MODEL`, optionally `SYSTEM_PROMPT_AGENT`. Treat `API_KEY_FILE` as an opaque path
   — do not read its contents. If the file is missing or any required key is absent, halt with a
   diagnostic asking the user to re-supply via `/guest-start <topic>`.

## Parsing Arguments

4. The full `$ARGUMENTS` string (all tokens, joined) is the user's message to the guest model. If
   empty, halt with: `"Usage: /guest <message>"`.

## Dispatch

5. Invoke the `guest-liaison` subagent in **continuation mode**. Pass it:
   - Session topic (`<topic>`)
   - `API_BASE_URL`, `API_KEY_FILE`, `MODEL` (from params.env)
   - User message (from `$ARGUMENTS`)

   The liaison will detect the existing `messages.json`, append the user message as a new turn, send
   to the guest model, service any file or tool requests, and return the substantive reply.

6. Relay everything the liaison returned, verbatim — the guest's response and every warning, error, refusal, round-cap report or note it emitted alongside.

**Your entire message this turn is that return and nothing else**: no preface, no closing line, no addition of your own, and no offer to supply one. That covers summarising it, evaluating it, noting what it asserted, comparing it to anything else, and supplying a fact it omitted. Volunteering a reading is not help, it is standing over the user's shoulder; offer yours when asked for it.

This holds most where it is hardest. The guest may be underpowered, out of date, or confidently wrong — still not your cue. And the reverse is the same intrusion wearing a compliment: that it held its ground, that it agrees with you, that it caught something.
