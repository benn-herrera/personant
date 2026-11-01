#### Slots for opencode content

Three further slots carry content opencode supplies ([§1.2](../../1-operating-model/1.2-mcp.md), [§1.3](../../1-operating-model/1.3-opencode-content-in-composed-requests.md)):

| Slot | Content |
|---|---|
| Instructions | The full text of each instruction source opencode loads (for example `AGENTS.md`), per source, including a nested `AGENTS.md` captured when opencode injects it. A source whose content is unchanged does not change the slot. |
| opencode expectations | The todo list, the agent mode, an open question or permission request, reminders still in force, child session IDs, background-job results — rendered as current state. |
| Tool definitions | The built-in and MCP tool definitions sent with each request. They count against the token ceiling. |

