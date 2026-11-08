### Symbol source

When several sources emit a symbol, the highest-ranked wins, `curator > user > model > deterministic` (`memops.DominantSource`): within a turn, across turns and in `source_dominant`. In the symbol index a
thread's anchor counts as `curator`-sourced (`internal/index/symbols.go`): a stored label for
anchors, not summarizer output, since the summarizer contributes no symbols (see "Symbol extraction").
`source` records how a symbol was emitted; `derived_from` records which thread it came from; the
two are orthogonal.

**`derived_from` population** is deterministic (no model, no human). At each turn's symbol-history
merge, for every symbol emitted into the engaged thread: if that normalized symbol also belongs to
a thread currently resident among those recall has loaded into the active set (by automatic turn-close
fetch or the mid-turn fetch) and not yet demoted, that thread's id is unioned into the
symbol's `derived_from`. An automatic turn-close fetch attributes from the next turn, a mid-turn fetch
within the same turn. Eligibility ends when the parent is demoted. The set is monotonic. Recall does
not write symbols into a thread, and `derived_from` does not take part in matching. When a thread
is started by recalling and combining two or more threads, each carried symbol acquires that
parent's id.

