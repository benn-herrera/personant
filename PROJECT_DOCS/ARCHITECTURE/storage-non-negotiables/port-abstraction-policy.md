### Port abstraction policy

Design rules for the `memops` port surface:

1. **Default: backend-agnostic naming and types.** Port types describe abstract concepts
   (per-thread metadata, project record, thread registry record), not storage-format details.
   Names that would not make sense for an alternate storage backend (SQL, bbolt, in-memory, etc.)
   are leaks.

2. **No ceremony for hypothetical backends.** Don't pre-engineer abstractions, defensive
   interfaces, or migration scaffolds for storage backends that don't exist.

3. **API surface is not frozen.** The port evolves when concrete evidence — a real second storage
   backend, a discovered constraint — shows the current shape needs to change.

4. **Concessions are explicit and case-by-case.** When a backend-specific concept
   legitimately needs to live at the port (performance, debuggability, no abstract framing exists
   yet), it's an explicit exception: name the concession, state the rationale, state what would
   cause it to be revisited. Implicit concessions — a backend term creeping in without
   deliberation — get cleaned up to the default.

---

