### Concurrent sessions

Not built. Two live opencode sessions against one memory is Open (SPEC §1.1; ROADMAP B5). The memory service is the single writer either way. One consequence of today's design: `session.started active=` is logged once per service run, while the active project is per opencode session.

---

