### Port abstraction policy

Design rules for the `memops` port surface:

1. **Default: substrate-agnostic naming and types.** Port types describe abstract concepts
   (per-thread metadata, project record, spine entry), not storage-format details. Names that would
   not make sense for an alternate substrate (SQL, bbolt, in-memory, etc.) are leaks.

2. **No ceremony for hypothetical substrates.** Don't pre-engineer abstractions, defensive
   interfaces, or migration scaffolds for substrates that don't exist.

3. **API surface is not frozen.** The port evolves when concrete evidence — a real second substrate,
   a discovered constraint — shows the current shape needs to change.

4. **Concessions are explicit, case-by-case, user-approved.** When a substrate-specific concept
   legitimately needs to live at the port (performance, debuggability, no abstract framing exists
   yet), it's an explicit exception: name the concession, state the rationale, state what would
   cause it to be revisited. Implicit concessions — a substrate term creeping in without
   deliberation — get cleaned up to the default.

---

