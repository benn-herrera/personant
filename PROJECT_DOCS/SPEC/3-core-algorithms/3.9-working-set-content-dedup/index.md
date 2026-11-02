### 3.9 Working-set content dedup

This section covers the layers Personant composes into the working set ([§3.1](../3.1-working-set-composition/index.md)). opencode's
stored transcript is never rewritten and its tool-result rows are never edited: a history row is
dropped from the request once memory has captured it, so repeated file copies there never reach
the model.

File content the agent reads or writes appears in the composed context at most once as a
literal, at its most recent position. Every other place it appeared carries a content-addressed
identifier in place, so the context still shows that the file was read or modified there, and in
what order. Per-turn composition cost and context size scale with what the context holds, never
with how many times a file has been read or edited; this is what lets long-running work fit a
fixed budget. Persistent storage ([§3.9.1](3.9.1-persistent-storage-and-retention.md)) holds only what composition can render; composition
([§3.9.2](3.9.2-live-context-composition-working-window.md)) decides each turn which positions render content.

Contents: [§3.9.1 Persistent storage and retention](3.9.1-persistent-storage-and-retention.md) ·
[§3.9.2 Live context composition](3.9.2-live-context-composition-working-window.md) · [§3.9.3 Diff format](3.9.3-diff-format.md) ·
[§3.9.4 Identifier-only references](3.9.4-identifier-only-references.md)
