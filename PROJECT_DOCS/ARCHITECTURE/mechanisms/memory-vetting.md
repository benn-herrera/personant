### Memory vetting

Not built; outcomes and open items: SPEC §1.8.

Mechanism: behavior- and process-memory writes pass through the `MemoryOps` port, so the review stage sits at
one port operation. The port has no directive-write operation today (`DirectiveParam` only reads).
Career-memory protection uses the slots, the dual-repo store and the consolidation cycle (see
"The layered working set", "Crash stability and the dual-repo substrate").

**Open:** which package holds the review stage and its provenance record.

