### Symbol source

When several sources emit a symbol, `source_dominant` is the most frequent across its history
entries; ties break `curator > user > model > deterministic`. `source` records how a symbol was
emitted; `derived_from` records which thread it came from; the two are orthogonal.

**`derived_from` population** is deterministic (no LLM, no human). At each turn's history merge, for
every symbol emitted into the engaged thread: if that normalized symbol also belongs to a thread
currently resident in the recall-surfaced working set — promoted into Layer B by recall (turn-close
acceptance or the mid-turn fetch) and not yet evicted — that thread's id is unioned into the
symbol's `derived_from`. A turn-close acceptance attributes from the next turn, a mid-turn fetch
within the same turn. Eligibility ends when the parent is evicted. The set is monotonic. Recall does
not write symbols into a thread, and `derived_from` does not take part in matching. When a thread
is started by recalling and combining two or more threads, each carried symbol acquires that
parent's id.

