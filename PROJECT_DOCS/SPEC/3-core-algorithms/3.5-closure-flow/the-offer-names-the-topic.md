#### The offer names the topic

Wherever the interactive resolver runs — the boundary drain, `/done`,
or `always` mode — each closure offer shows `idle topic thr_N: <display name> — <gist>` above the
curator's draft summary, resolved by the [§2.2.2](../../2-data-model/2.2-spine-record-schema-spine-jsonl/2.2.2-display-form.md) fallback chain (description → summary → projected
anchors).

**`/done` is always fully interactive** in both modes. A draft that **fails or comes back empty is
never auto-accepted**: it logs `retire.curator-error` and is retried at the next scan. (A
persistently failing curator therefore leaves its thread un-closed rather than queued.)

