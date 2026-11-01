#### Phantom resolution and owner fallback

Every referenced `thr_<n>` is resolved against the spine
at turn close. An id that does not resolve is a *phantom*: not in the spine (`thread.engaged-miss`)
or belonging to another project (`thread.engaged-cross-project` — cross-project engagement is not
built in v0.1 and is declined). A phantom is logged and dropped: it never enters the engaged set,
the Layer B active set, or the persisted working set; never owns the turn or receives the [§3.9](../3.9-working-set-content-dedup/index.md)
file-edit binding; and a declined cross-project record is never mutated. When the tag's would-be
owner (the lowest-id existing referenced thread) is a phantom, ownership falls back to the most
recently engaged **valid** referenced thread, ties broken by lowest `thr_<n>` id. When every
referenced thread is a phantom: a tagged `*new-topic*` thread owns the turn; with no `*new-topic*`
either, a new thread is created from the turn as for a pure `*new-topic*` tag. The turn's excerpt is
never dropped. A turn with **no tag at all** whose deltas require owner binding follows the [§3.3](../3.3-symbol-extraction-three-passes/index.md)
missing-tag recovery (re-prompt once → owner-default by Layer B order).

