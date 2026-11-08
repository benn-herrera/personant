## 1. Operating model

Personant runs under unmodified opencode as a plugin plus the **memory service**, a local process
that holds all memory state. Personant owns what enters every model request: composition
(assembling each request, the **composed request**, from memory, [§3.1](../3-core-algorithms/3.1-working-set-composition/index.md)), which takes the
place of opencode's compaction; summarization (condensing older content,
[§5.2](../5-model-interaction/5.2-summarizer-prompt-for-closing-summary.md)); and
recall (surfacing older **threads** — each the durable record of one problem being worked on —
relevant to the current work, [§3.4](../3-core-algorithms/3.4-recall-matching/index.md)). opencode
owns everything else: the terminal UI, session storage, the message data model, the event stream,
agents, permissions, tools, providers and configuration. Where opencode has made a choice outside
context management, Personant uses it as it is ([§1.7](1.7-opencode-storage.md)'s database cleanup is the one exception). Personant does not define interaction modes, a
workflow, or how much the user takes part. opencode's plugins and Code Mode (opencode's way of
letting the model call tools by writing code) work unchanged. The
components and their wiring: ARCHITECTURE "Plugin route".

An **Open:** line marks a decision not yet made; a tag such as U7, B6a or A12a names it.

**Open:** U7 — plugin failure policy: fail open (opencode's own composition reaches the model;
the memory service misses the step) or fail closed (the request is refused).

Contents:

- [§1.1 Sessions and service runs](1.1-sessions-and-service-runs.md)
- [§1.2 MCP](1.2-mcp.md)
- [§1.3 opencode content in composed requests](1.3-opencode-content-in-composed-requests.md)
- [§1.4 Evicted content](1.4-evicted-content.md)
- [§1.5 Determinism](1.5-determinism.md)
- [§1.6 Resume](1.6-resume.md)
- [§1.7 opencode storage](1.7-opencode-storage.md)
- [§1.8 Memory vetting](1.8-memory-vetting.md)
