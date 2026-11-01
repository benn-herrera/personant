### Thread lifecycle

A thread's life moves through five stages:

1. **Created** when the model emits `*new-topic*` in a topic tag, or by an explicit user act
   (**Open:** U4 — the command).
2. **Active** in Layer B while engaged (model tags it in per-turn topic list).
3. **Dormant** in Layer C as engagement decays (no longer in the model's recent topic tags).
4. **Retired** to Layer A (spine only) on closure, with full content preserved in
   `threads/thr_<n>/`.
5. **Archived** off-spine (deep cold) only if spine cardinality pressure builds — recoverable by
   explicit fetch through git.

The natural retirement boundary is "thread reached a resting point," not "context window pressure."
Resting points are recognized by engagement decay (Spec §3.5).

