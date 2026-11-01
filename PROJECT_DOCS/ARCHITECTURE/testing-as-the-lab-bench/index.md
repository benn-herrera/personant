## Testing as the lab bench

SPEC §9 is the measurement instrument by which the architectural thesis gets proven empirically,
and the substrate by which the techniques evolve iteratively.

The simulation harness (`internal/scenarios/`, including `internal/scenarios/sim/`) is a
load-bearing deliverable under production discipline: one owner per value, key and contract; named
constants; designed in these
documents before coding; changed through CONVENTIONS.md review. A seam that silently fails (renamed
metric key, drifted log marker, second clock path) passes its gate vacuously, and the v0.1 gate is
only as sound as the harness.

Contents:

- [Harness ↔ system-under-test coupling rule](harness-system-under-test-coupling-rule.md)
- [Simulation-harness structure](simulation-harness-structure/index.md)
- [Event log format](event-log-format.md)
- [opencode-in-loop harness](opencode-in-loop-harness.md)
