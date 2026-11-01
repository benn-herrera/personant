### Boundary

Outside context management, Personant uses what opencode has chosen, as it is. Where
opencode's machinery would decide what the model sees (compaction, history replay), the plugin
takes that decision over through a hook; it does not reimplement the machinery. The host is
opencode, not Charm's Crush: Crush reads provider API keys from environment variables and runs
its TUI in-process with its backend, so there is no protocol to sit under. The fallback, if
this route cannot give the memory system the control it needs, is a full Go port of opencode's
server (`ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md`).

