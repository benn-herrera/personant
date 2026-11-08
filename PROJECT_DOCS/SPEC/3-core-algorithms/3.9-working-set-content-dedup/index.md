### 3.9 Working-set content dedup

This section covers the slots Personant composes into the working set ([§3.1](../3.1-working-set-composition/index.md)). Dedup never
rewrites opencode's stored transcript or edits its tool-result rows (the [§1.7](../../1-operating-model/1.7-opencode-storage.md) database cleanup is separate): a transcript row is
dropped from the request once memory has captured it, so repeated file copies there never reach
the model.

File content the assistant reads or writes appears in the composed context at most once as full
content, at its most recent position. Every other place it appeared carries a content-addressed
content id in place, so the context still shows that the file was read or modified there, and in
what order. Per-turn composition cost and context size scale with what the context holds, never
with how many times a file has been read or edited; this is what lets long-running work fit a
fixed budget. Persistent storage ([§3.9.1](3.9.1-persistent-storage-and-retention.md)) holds only what composition can render; composition
([§3.9.2](3.9.2-live-context-composition-working-window.md)) decides each turn which positions render content.

Contents:

- [§3.9.1 Persistent storage and retention](3.9.1-persistent-storage-and-retention.md)
- [§3.9.2 Live context composition (working window)](3.9.2-live-context-composition-working-window.md)
- [§3.9.3 Diff format](3.9.3-diff-format.md)
- [§3.9.4 Content-id-only references](3.9.4-content-id-only-references.md)
