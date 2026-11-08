### 3.0 All content enters through one pipeline (deltas)

The **working window** — everything the next model request will carry — is built up as a sequence
of **deltas**, each one content addition to the window, with a
source ([§3.0.1](3.0.1-the-delta-taxonomy.md)); a turn groups deltas, from the user message (or resumed request) that opens it to its close ([§3.0.4](3.0.4-turn-boundaries-and-per-turn-engagement-coalescing.md)). **No gaps:** every delta — every content addition from the session ([§3.0.1](3.0.1-the-delta-taxonomy.md)) — runs the **per-delta pipeline** ([§3.0.2](3.0.2-the-per-delta-pipeline.md): extraction,
engagement, dedup, budget check, logging), so no content escapes extraction, dedup, or the budget
check.

Contents:

- [§3.0.1 The delta taxonomy](3.0.1-the-delta-taxonomy.md)
- [§3.0.2 The per-delta pipeline](3.0.2-the-per-delta-pipeline.md)
- [§3.0.3 Pipeline order matters](3.0.3-pipeline-order-matters.md)
- [§3.0.4 Turn boundaries and per-turn engagement coalescing](3.0.4-turn-boundaries-and-per-turn-engagement-coalescing.md)
- [§3.0.5 Implementation contract](3.0.5-implementation-contract.md)
