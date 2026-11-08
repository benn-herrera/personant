### Thread lifecycle

Standard pattern: THESIS "The building blocks", row "Thread lifecycle".

A thread's life moves through five stages (SPEC §2.2.1 holds the states):

1. **Created** when the model emits `*new-topic*` in a topic tag, or by an explicit user act
   (**Open:** U4 — the command).
2. **Active**, in the active set, while engaged (the model tags it in the per-turn topic list).
3. **Dormant**, in the dormant set, once more recently engaged threads push it out of the active set (SPEC §3.1 LRU update rule).
4. **Closed** at a resting point: it leaves the active and dormant sets and remains in the registry
   slot as a display line, with full content preserved in `threads/thr_<n>/`.
5. **Archived**, in cold storage, only if thread registry size pressure builds — restorable by
   explicit fetch through git.

A thread is closed when the work reaches a resting point, not under "context window pressure."
Resting points are recognized by engagement decay (Spec §3.5). Fallback dissection is the one path that closes threads under budget pressure; it is not built (see "Fallback dissection").

