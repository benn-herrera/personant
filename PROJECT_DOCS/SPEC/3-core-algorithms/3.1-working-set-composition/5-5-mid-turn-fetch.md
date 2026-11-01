#### [§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md) mid-turn fetch

**Open:** U3 — whether the in-band tag and its stream abort-and-re-issue
are built under opencode or replaced by a recall tool. Until decided, the text below binds: when
the model's topic tag at response start references a `thr_<n>` not in the Layer B active set, the
runtime aborts the in-flight stream, loads the thread, fires a
`thread.fetched` context delta ([§3.0.1](../3.0-context-modification-events-the-architectural-primitive/3.0.1-the-event-taxonomy.md)), promotes the thread into the Layer B active set (at the
front, capped at `layer.b-top-k` — overflow demotes the tail to the dormant set), and re-issues the
request with the recomposed system prompt. The user-visible response is the second stream's body.
The re-prompt is capped at 1 per turn. A fetched thread belonging to another project is declined at
the fetch seam (`thread.fetch-cross-project`) — the [§3.2](../3.2-topic-tagging-and-engagement-update/index.md) cross-project policy — and is neither
promoted nor re-prompted for.

The close-time LRU update still picks up any thread the mid-turn fetch could not service (an
unloadable thread file logs `thread.fetch-miss` and is skipped) or did not trigger (e.g. the
second-stream tag introduces another missing `thr_<n>`; with the cap reached, that thread enters the
Layer B active set at close).

