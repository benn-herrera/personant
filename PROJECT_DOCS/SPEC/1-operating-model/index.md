## 1. Operating model

Personant runs under unmodified opencode. Personant owns what enters every model request:
composition, compaction and summarization, recall, and the sub-mind split and re-merge. opencode
owns everything else: the terminal UI, session storage, the message data model, the event stream,
agents, permissions, tools, providers and configuration. Where opencode has made a choice outside
context management, Personant uses it as it is. Personant does not define interaction modes, a
workflow, or how much the user takes part. opencode's plugins and Code Mode work unchanged. The
components and their wiring: ARCHITECTURE.md "Plugin route".

**Open:** U7 — plugin failure policy: fail open (opencode's own composition reaches the model;
memory misses the step) or fail closed (the request is refused).

Contents:

- [§1.1 Sessions and service runs](1.1-sessions-and-service-runs.md)
- [§1.2 MCP](1.2-mcp.md)
- [§1.3 opencode content in composed requests](1.3-opencode-content-in-composed-requests.md)
- [§1.4 Evicted content](1.4-evicted-content.md)
- [§1.5 Determinism](1.5-determinism.md)
- [§1.6 Resume](1.6-resume.md)
- [§1.7 opencode storage](1.7-opencode-storage.md)
- [§1.8 Memory vetting](1.8-memory-vetting.md)
