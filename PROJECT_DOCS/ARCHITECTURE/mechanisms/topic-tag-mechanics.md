### Topic-tag mechanics

**Open:** U3 — the in-band topic tag under opencode, including stripping it from the stored
transcript (see "No speculative prefetch"). Until decided, the mechanics below bind.

The tag prompt is a compiled constant in `internal/prompt/template.go` (`TopicTagDirective`); the
two fallback reminders (`TopicTagReminder`, `EmptyResponseReminder`) live in the same file, and
changing any of them is a rebuild. The owner of a multi-existing tag is the lowest `thr_N` because
the turn buffer is a map and does not preserve tag order. The bare `*new-topic* [...]` alias is
accepted by the length-limited preamble scan (so the §5.5 fetch timing, the missing-tag trigger and the
stream-filter suppression agree) and by the stream filter's Close-time trailing-line fallback; an
all-whitespace response longer than that scan resolves as tag-less rather than empty, so it fires
the missing-tag reminder. `fs.read` deltas buffer a write entry like `fs.write`, so a tag-less
read-only turn pays the full missing-tag fallback.

The parser (`internal/prompt/parser.go`) recognizes a tag with this RE2 pattern, compiled with
`(?m)`:

```
^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$
```

Group 1 is the thread list: comma-split, whitespace-trimmed, each entry validated against
`thr_\d+|\*new-topic\*`, the unclosed `*new-topic` accepted and normalized to `*new-topic*` (SPEC §5.1.2). Group 2 is the anchor list: comma-split, whitespace-trimmed, then run
through the SPEC §2.7.2 normalization. The bare alias is the line-anchored pattern
`^\s*\*new-topic\*\s*\[([^\]]*)\]\s*$`; a bare `*new-topic*` with no bracket pair stays invalid and
is classified `bare-new-topic` (see "Event log format").

**Mid-turn fetch (SPEC §5.5).** When the tag names a `thr_<n>` outside the active set, the service
parses the tag before it consumes any response body and aborts the in-flight stream. For each such
thread it reads `threads/thr_<n>/` (frontmatter from `thread.md`; body assembled from the
recent-excerpt window and, when needed, from `folio.jsonl`), truncates it to its active-threads
allowance, fires a `thread.fetched` delta (SPEC §3.0.1) and loads the thread at the front of the
active set, where overflow past `layer.b-top-k` demotes the tail to the dormant set. It then
re-issues the request with the recomposed system prompt, and the second stream's body is the
user-visible response. The re-prompt is capped at one per turn; a thread of another project is
declined at the fetch seam (`thread.fetch-cross-project`).

