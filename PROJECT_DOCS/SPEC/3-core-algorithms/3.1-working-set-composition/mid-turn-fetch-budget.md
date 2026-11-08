#### Mid-turn fetch budget

**Open:** U3 — whether the in-band tag and its stream abort-and-re-issue
are built under opencode or replaced by a recall tool. Until decided, the text below binds: when
the model's topic tag at response start names a `thr_<n>` not in the active set, that thread joins
the front of the active set and the response is regenerated with it in context; the user sees only
the regenerated response. At most one such re-prompt per turn ([§5.5](../../5-model-interaction/5.5-mid-turn-thread-fetch-system-injected/index.md)). A thread of another project
is declined (`thread.fetch-cross-project`) and neither loaded nor re-prompted for.

The close-time LRU update still picks up any thread the mid-turn fetch could not service (an
unloadable thread file logs `thread.fetch-miss` and is skipped) or did not trigger (e.g. the
second-stream tag introduces another missing `thr_<n>`; with the cap reached, that thread enters the
active set at close).

