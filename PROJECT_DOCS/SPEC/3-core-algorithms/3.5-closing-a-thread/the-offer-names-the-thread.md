#### The offer names the thread

Wherever the interactive closing dialog runs — a drain point, `/done`,
or `always` mode — each offer shows `idle topic thr_N: <display name> — <gist>` above the
summarizer's draft summary.

**`/done` is always fully interactive** in both modes. A draft that **fails or comes back empty is
never auto-accepted**: it logs `retire.curator-error` (the summarizer-failure event) and is retried at the next scan. (A
persistently failing summarizer therefore leaves its thread un-closed rather than queued.)

