### Transient-data lifecycle (load-bearing for recall fidelity)

Symbol pollution by one-moment-of-value content (e.g., `# git status` output, search-result noise)
would silently degrade Jaccard recall: noise inflates the union, dilutes meaningful symbols, drives
spurious matches. The lifecycle prevents this at the source:

1. **Provisional class at delta time** (mechanical, by source: SPEC §3.10.1). `task` → symbols
   extracted into a cross-turn **staging buffer** keyed by normalized form. `decision` → symbols
   extracted directly into the per-turn coalesce buffer (the existing engagement path).
2. **Confirmation via decision-citation.** When a decision-class delta extracts a symbol whose
   normalized form matches a staged entry, **promote**: the staged entry migrates from staging into
   coalesce (where source-dominance merge folds it with the citing delta's contribution), removed
   from staging.
3. **Window-close GC.** At the top of each turn (after `TurnNumber++`, before any chain step),
   staged entries older than `stagingWindowTurns` (default 3) evict (`pruneStaging`). Their symbols
   never reach the persistent symbol index. The window is a compiled-in constant; a staging entry
   holds the normalized form, raw form, source, and the turn it was staged.

The principle: **raw task-class bytes are always discardable; only the symbols that crossed over
into a decision delta survive.** The canonical symbol index never sees transient pollution because
pollution is filtered at insert time, not removed retroactively. File content has a separate
persistent home in the per-thread tracked-file store (SPEC §3.9.1), which this lifecycle does not
govern.

---

