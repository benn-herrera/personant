## 2. Design in one paragraph

**Write side.** At turn close the owner's excerpt gains one identifier line per file event of the
turn, written once and never rewritten. Its form is `[file write #<id12>] <path>`, placed before
`**user:**`. The thread's tracked-file store holds, per file:

- one content blob, the current bytes;
- up to N diff files, each written once when its change lands;
- one JSONL index line naming them and the turn of the file's latest event.

**Compose side.** For each Layer B thread, composition reads that small index. One pure plan,
computed across all Layer B threads, picks which (thread, turn) positions render the literal or a
diff. A file whose current content is at or above `dedup.literal-share-threshold` (default 0.25) of
the per-thread Layer B share gets neither: it is never expanded. Excerpts are then read
newest-first, each excerpt costed together with its planned expansions, until the per-thread share
is spent. Expansions render inside their turn's excerpt. Every other file event stays the
identifier line already in the excerpt text.

**Retention and recovery.** Superseded blobs and diffs, and entries whose latest event has left the
assembly window, are removed by a new idempotent barrier step (B1c). No git call is made against the
workspace anywhere. The identifier is the git blob object id of the bytes, so the user's own VCS
resolves it whenever that version was ever added there. Personant needs no recovery operation.

---

