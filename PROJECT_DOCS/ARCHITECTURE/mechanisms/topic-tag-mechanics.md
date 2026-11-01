### Topic-tag mechanics

**Open:** U3 — the in-band topic tag under opencode, including stripping it from the stored
transcript (see "No speculative prefetch"). Until decided, the mechanics below bind.

The tag prompt is a compiled constant in `internal/prompt/template.go` (`TopicTagDirective`); the
two recovery reminders (`TopicTagReminder`, `EmptyResponseReminder`) live in the same file, and
changing any of them is a rebuild. The owner of a multi-existing tag is the lowest `thr_N` because
the coalesce buffer is a map and does not preserve tag order. The bare `*new-topic* [...]` alias is
accepted by the bounded preamble scan (so the §5.5 fetch timing, the missing-tag trigger and the
stream-filter suppression agree) and by the stream filter's Close-time trailing-line fallback; an
all-whitespace response longer than that scan resolves as tag-less rather than empty, so it fires
the missing-tag reminder. `fs.read` deltas buffer a write entry like `fs.write`, so a tag-less
read-only turn pays the full missing-tag recovery; whether such a turn should count as
conversational is an open calibration question.

