## Testing as the measurement instrument

SPEC §9 is the measurement instrument that tests, by simulation, whether the design meets SPEC over a long career, and the means by which the techniques evolve iteratively.

The simulation harness (`internal/scenarios/`, including `internal/scenarios/sim/`) is a
load-bearing deliverable under production discipline: one owner per value, key and contract; named
constants. A seam failure (a renamed metric key,
a drifted log line name or a second clock path) makes a check pass vacuously, and the
acceptance criterion is only as sound as the harness.

Contents:

- [Harness ↔ system-under-test coupling rule](harness-system-under-test-coupling-rule.md)
- [Simulation-harness structure](simulation-harness-structure/index.md)
- [Event log format](event-log-format.md)
- [opencode-in-loop harness](opencode-in-loop-harness.md)
