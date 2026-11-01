### Boundary

Outside context management, Personant uses what opencode has chosen, as it is. Where
opencode's machinery would decide what the model sees (compaction, history replay), the plugin
takes that decision over through a hook; it does not reimplement the machinery. The fallback, if
this route cannot give the memory system the control it needs, is a full Go port of opencode's
server (`ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md`).

