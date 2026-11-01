### 3.0 Context-modification events (the architectural primitive)

The working window is a sequence of **context-modification events**; a "turn" only groups events
into human-readable spans. **No gaps:** every path by which content enters the working window runs
the per-event hook chain, so no content escapes extraction, dedup, or the budget check.

Contents:

- [§3.0.1 The event taxonomy](3.0.1-the-event-taxonomy.md)
- [§3.0.2 The hook chain](3.0.2-the-hook-chain.md)
- [§3.0.3 Hook-firing order matters](3.0.3-hook-firing-order-matters.md)
- [§3.0.4 Per-turn coalescing for engagement](3.0.4-per-turn-coalescing-for-engagement.md)
- [§3.0.5 Implementation contract](3.0.5-implementation-contract.md)
