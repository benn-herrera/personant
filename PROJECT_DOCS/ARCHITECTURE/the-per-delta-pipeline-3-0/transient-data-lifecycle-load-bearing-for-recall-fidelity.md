### Transient-data lifecycle (load-bearing for recall fidelity)

Symbols from one-moment-of-value content (e.g., `# git status` output, search-result text) would
silently degrade Jaccard recall: they inflate the union, dilute meaningful symbols and drive
spurious matches. The lifecycle prevents this at the source:

1. **Provisional class at delta time** (mechanical, by source: SPEC §3.10.1). `task` → symbols
   extracted into a cross-turn **staging buffer** keyed by normalized form. `decision` → symbols
   extracted directly into the turn buffer (the existing engagement path).
2. **Confirmation via decision-citation.** When a decision-class delta extracts a symbol whose
   normalized form matches a staged entry, **promote**: the staged entry migrates from the staging
   buffer into the turn buffer (where source-dominance merge folds it with the citing delta's
   contribution) and is removed from the staging buffer.
3. **Expiry.** At the top of each turn (after `TurnNumber++`, before any pipeline step), staged
   entries staged before turn `currentTurn − K + 1` expire (`pruneStaging`: `cutoff :=
   TurnNumber − stagingWindowTurns + 1`, with `stagingWindowTurns` = K, default 3). Their symbols
   never reach the persistent symbol index. The window is a compiled-in constant; a staging entry
   holds the normalized form, raw form, source, and the turn it was staged. A later task-class
   delta carrying the same normalized form overwrites the entry and refreshes its staged turn,
   which restarts the window.

The principle: **raw task-class bytes are always discardable; only the symbols that crossed over
into a decision delta survive.** The persistent symbol index never sees transient symbols
because they are filtered at insert time, not removed retroactively. File content is kept
separately, in the per-thread tracked-file store, only as long as composition needs it (SPEC
§3.9.1); this lifecycle does not govern it.

---

