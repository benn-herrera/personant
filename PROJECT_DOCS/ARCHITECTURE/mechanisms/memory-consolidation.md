### Memory consolidation

Built: `MemoryOps.Consolidate` packs both git repositories (git gc and repack, SPEC §3.11), and `measure.RebuildTrees` rebuilds the within-thread summary trees. Only the simulation's day off (the harness's `SleepCycle` step) runs both. Outside the simulation, `Consolidate` runs only from the turn-repository loose-object check (see "Mechanism names"), and nothing runs `RebuildTrees` (ROADMAP "No production trigger for consolidation"). SPEC §3.10.8's content-retention keep/toss stage, which cites this section by name, is not built (ROADMAP Far-horizon, "Memory consolidation").

