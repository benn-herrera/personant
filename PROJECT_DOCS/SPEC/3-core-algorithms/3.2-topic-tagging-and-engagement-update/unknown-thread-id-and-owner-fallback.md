#### Unknown thread id and owner fallback

Every referenced `thr_<n>` is resolved against the thread registry
at turn close. An id that does not resolve is an **unknown thread id**: not in the thread registry
(`thread.engaged-miss`) or belonging to another project (`thread.engaged-cross-project` —
cross-project engagement is declined). An unknown thread id is logged and
dropped: it never enters the engaged set, the active set, or the persisted working set; never owns
the turn or receives the [§3.9](../3.9-working-set-content-dedup/index.md) file-edit binding; and a declined cross-project record is never
mutated. When the tag's would-be owner (the lowest-id existing referenced thread) is an unknown
thread id, ownership falls back to the most recently engaged **valid** referenced thread, ties
broken by lowest `thr_<n>` id. When every referenced thread id is unknown: a tagged `*new-topic*`
thread owns the turn; with no `*new-topic*` either, a new thread is created from the turn as for a
pure `*new-topic*` tag. The turn's excerpt is never dropped. A turn with **no tag at all** whose
deltas require owner binding follows the [§3.3](../3.3-symbol-extraction-two-passes/index.md) missing-tag fallback (re-prompt once → owner-default
by active-set order).

