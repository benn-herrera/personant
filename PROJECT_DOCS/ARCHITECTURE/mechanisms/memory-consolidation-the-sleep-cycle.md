### Memory consolidation — the "sleep" cycle

Designed, partially built. The day-off idle window already fires `MemoryOps.Consolidate` (git
gc/repack on both git DBs, SPEC §3.11) and the within-thread summary-tree rebuild
(`measure.RebuildTrees`, #111). The fuller offline-reorganization vision — re-packing fragmented
structures, compacting the spine, advancing archival, and the SPEC §3.10.8 content-retention
keep/toss precision layer, which cites this section by name — is designed, not built. Full design
capture: **ROADMAP.md → Far-horizon, "Memory consolidation."**

