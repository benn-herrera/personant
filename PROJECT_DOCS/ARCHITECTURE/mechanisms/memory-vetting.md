### Memory vetting

Standard pattern: THESIS "The building blocks", row "Memory vetting".

Not built; outcomes and open items: SPEC §1.8.

Mechanism: behavior- and process-memory writes pass through the `MemoryOps` port, so the review stage sits at
one port operation. The port has no directive-write operation today (`DirectiveParam` only reads).
Threads are protected (SPEC §1.8) by the slots and the turn and history repositories; consolidation's content review is not built (see "Memory consolidation",
"The composed request and its slots", "Crash recovery and the turn and history repositories").

**Open:** which package holds the review stage and its provenance record.

