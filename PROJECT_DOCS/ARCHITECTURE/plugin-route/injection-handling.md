### Injection handling

opencode injects content into a request besides the user's prompt: instruction files, nested
`AGENTS.md`, reminders, MCP content, tool definitions (context-injections report §1). The service
turns each into state rather than history, so that nothing is stale and nothing is counted twice
(SPEC §1.3).

- **Instructions slot.** Rebuilt each request from the full per-source texts in the instructions
  system part's `metadata.experimental.instructions`, a list of `{key, text}`. A source's content
  hash decides whether the slot changes. opencode's instruction-update diff messages are not used,
  so no stale baseline exists to patch.
- **Nested `AGENTS.md`.** opencode injects one once, on a `read` into its directory, and never
  re-sends it (context-injections report §3). The service captures it into the instructions slot
  as its own keyed source, where it stays for as long as it applies.
- **Message rows are typed by joining** the hook input's message `id` against the plugin's session
  API, because the hook input drops metadata on synthetic messages (context-injections report §2).
- **Drop on re-injection.** Content already accounted for is dropped: by (source key, content hash)
  for instructions, by stored message `id` for message rows. Checkpoint-compaction messages are
  dropped (context-injections report §5).
- **Expectations slot.** The todo list, plan/agent mode, an open question or permission request,
  reminders still in force, the subagent session ID needed to continue a child, and background-job
  results render as current state, not history. The slot is dynamic: its content changes mid-turn
  (SPEC §3.1).
- **Tool definitions.** Sent with every request and counted against the token ceiling. opencode
  curates them per agent and passes them through, with no budget or overflow form (context-injections
  report §5). MCP tools are recognizable by the `<server>_` name prefix.
- **Stand-in line for evicted content.** File content dropped from a request for budget is replaced
  by a stand-in line that pins the version (a content hash), points at stored bytes when replay
  cannot reproduce the content, and reads as a display line the model can expand (see "Display
  lines are how the model recognizes threads"; SPEC §1.4).

**Open:** per-slot policies — all four for the instructions and tool-definition slots, and budget,
priority and overflow form for the expectations slot (SPEC §3.1). Without a tool-definition slot
the whole-request ceiling cannot be enforced (ROADMAP A7).

**Open:** a way to tell an MCP prompt expansion apart from a typed prompt; the hook input does
not distinguish them (context-injections report §2). Until found, an expansion is handled as user
input.

**Open:** injection unknowns (context-injections report §6) — whether plugin activation order
equals hook registration order after a reload, and how system-role messages are lowered on provider
routes other than openai-chat.

