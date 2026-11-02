## 2. Design in one paragraph

Terms used throughout:
- **N** is `dedup.live-diff-window`, default 3.
- **`<id12>`** is the first 12 hex characters of a content id, the 40-hex git blob id of the bytes.
- **Share** is the per-thread Layer B share, `workset.PerThreadBudget(LayerB, k)`, where `k` is the
  number of Layer B threads composed (at most `layer.b-top-k`, default 3).

**Write side.** At turn close, the owner's excerpt gains one identifier line per file event of the
turn, written once and never rewritten. Its form is `[file write #<id12>] <path>`, placed before
`**user:**`. The thread's tracked-file store holds, per file:

- one content blob, the current bytes;
- up to N diff files, each written once when its change lands;
- one JSONL index line naming them, with their sizes and the turn of the file's latest event.

**Compose side.** For each Layer B thread, composition reads that small index. One pure plan,
computed across all Layer B threads, picks which (thread, turn) positions render the literal or a
diff. A file whose current content is at or above `dedup.literal-share-threshold` (default 0.25) of
the share gets neither: it is never expanded. Excerpts are then read newest-first, each charged
together with its planned expansions, sized from the index, until the share is spent (the overflow
rule is in [§6](6-composition.md)). Expansions render inside their turn's excerpt. Every other file
event stays the identifier line already in the excerpt text.

**Retention and recovery.** A new idempotent barrier step, B1c, removes superseded blobs and
diffs, and entries whose latest event has left the assembly window. Nothing on the dedup, retention
or compose path calls git against the workspace. The identifier is the git blob id of the bytes, so the user's own VCS
resolves it wherever that version was ever added there. Personant has no recovery operation of its
own.

---
