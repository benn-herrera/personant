### Concurrent sessions

Designed, not built: multiple live opencode sessions open against one shared memory — multitasking
one career. The plugin route makes it reachable (the service is already the single writer).
Distinguished from multi-user (v2.0, locked): concurrent sessions interleave on *shared* canonical
state. Full design capture: **ROADMAP.md → "Concurrent sessions."**

**Open:** two concurrent opencode sessions against one memory (SPEC §1.1). One consequence already
visible: `session.started active=` is logged per service run, but the active project is per
opencode session.

---

