### 5.5 Mid-turn thread fetch (system-injected)

**Open:** U3 — under opencode the mechanism below does not fire; a recall tool the model invokes is
the alternative. Until decided, the text below binds.

When a topic tag references one or more `thr_<n>` not currently in the active set, the memory service fetches
those threads' contents and injects them into context **before the model generates its response
body**. The model does not invoke a tool to request the fetch.

The fetched thread enters as a `thread.fetched` delta ([§3.0](../../3-core-algorithms/3.0-all-content-enters-through-one-pipeline-deltas/index.md)), within its active-threads slot budget
([§3.1](../../3-core-algorithms/3.1-working-set-composition/index.md)).

Contents:

- [§5.5 Combined mid-turn re-prompt bound (with §3.3 fallback)](combined-mid-turn-re-prompt-bound-with-3-3-fallback.md)
