## 9. Proposed SPEC text (replaces the leaves under `PROJECT_DOCS/SPEC/3-core-algorithms/3.9-working-set-content-dedup/`)

**index.md**

> ### 3.9 Working-set content dedup
>
> This section covers the layers Personant composes into the working set ([§3.1]). opencode's
> stored transcript is never rewritten and its tool-result rows are never edited: a history row is
> dropped from the request once memory has captured it, so repeated file copies there never reach
> the model.
>
> File content the agent reads or writes appears in the composed context at most once as a
> literal, at its most recent position. Every other place it appeared carries a content-addressed
> identifier in place, so the context still shows that the file was read or modified there, and in
> what order. Per-turn composition cost and context size scale with what the context holds, never
> with how many times a file has been read or edited; this is what lets long-running work fit a
> fixed budget. Persistent storage ([§3.9.1]) holds only what composition can render; composition
> ([§3.9.2]) decides each turn which positions render content.
>
> Contents: §3.9.1 Persistent storage and retention · §3.9.2 Live context composition · §3.9.3 Diff
> format · §3.9.4 Identifier-only references

**3.9.1-persistent-storage-and-retention.md** (renamed from `…-thread-file-body-log.md`)

> #### 3.9.1 Persistent storage and retention
>
> What is stored is **file bytes**. opencode's `read` returns formatted output (for example with
> line numbers); the tracked-file store holds the file's bytes, not that output. The content of a
> write comes from the `write` tool's input, because the tool's output is only a confirmation line.
>
> **Identifier.** A file content's identifier is computed from its bytes alone, in the form a git
> object id for those bytes takes, so a version control system that holds that version resolves it.
>
> **Positions.** Each `fs.read`, `fs.write` and `fs.commit` of a turn is recorded in the owning
> thread's excerpt for that turn as one identifier line — operation, identifier (a commit hash for
> `fs.commit`) and path — in event order. An identifier line is written once and never rewritten;
> an excerpt never holds file bytes.
>
> **What is kept**, per thread and file: the current content (the bytes of the file's most recent
> read or write) and its `dedup.live-diff-window` most recent changes, each as a diff from the prior
> content to the new. A change whose diff is at or above `dedup.diff-literal-threshold` of the new
> content's size keeps no diff. Nothing else is kept: superseded content, and files whose most
> recent read or write has left the thread's assembly window ([§2.3]), leave storage at the next day
> barrier.
>
> **Bounds are Personant's own.** Storage per thread is bounded by the files read or written within
> its assembly window times (current size + the diff window), whatever the user does with version
> control — commit, rebase, or no repository at all. Retention never consults the workspace
> repository; Personant runs no git operation against it.
>
> **Earlier content.** Content no longer kept is recovered, where it can be, through the user's own
> version control by its identifier; Personant guarantees nothing about it. Superseded content is
> not evicted content in the [§1.4] sense: a file's current content is kept and is what an evicted
> entry brings back.
>
> ##### Symbol lifecycle for file-edit events
>
> `fs.read`, `fs.write`, and `fs.commit` deltas are task-class ([§3.10.1]): their symbols enter the
> staging buffer and reach `symbols.jsonl` only if a decision delta cites them within the citation
> window (K = 3, [§3.10.3]).

**3.9.2-live-context-composition-working-window.md**

> #### 3.9.2 Live context composition (working window)
>
> The positions a composed context holds are the identifier lines of the excerpts it assembles
> ([§2.3]). Replacement happens there, in place, and only for positions the context holds:
>
> - **Literal current content** at each file's most recent read or write position, when the context
>   holds that position. One file, and one content, render as a literal at most once across the whole
>   composed context, Layer B threads included; the most recent position wins.
> - **The N most recent changes** of each file (`dedup.live-diff-window`, default N = 3) render as
>   literal diffs at their positions, when the context holds them.
> - **Every other position** renders as its identifier line alone.
> - **A file too large for its thread's share is never rendered as content.** When a file's current
>   content is at or above `dedup.literal-share-threshold` (default 0.25) of its thread's Layer B
>   share ([§3.1]), no literal and no diff of it renders anywhere in the composed context; every
>   position of it is its identifier line alone.
>
> Positions are never rewritten in storage; replacement is a composition decision each turn. Content
> rendered at a position counts against the budget of the layer holding it, together with its
> excerpt ([§3.1]); content that cannot fit renders as its identifier with its size and stays
> retrievable ([§1.4]). Composition reads only the content it renders, plus one record per file kept
> for the thread.

**3.9.3-diff-format.md**

> #### 3.9.3 Diff format
>
> ##### Default: unified diff, from the prior content to the new
>
> **Fallback: natural-language descriptive diff** ("added a line `foo` after line 12; removed lines
> 20–22"), which the runtime may switch to per-diff where unified diff is hard for the model to
> apply (complex multi-region changes, very long files). The directive `dedup.diff-format` sets the
> global default; per-thread override is possible.

**3.9.4-identifier-only-references.md**

> #### 3.9.4 Identifier-only references
>
> An identifier line alone is the normal form of every earlier read or write of a file, and of every
> commit: it says *that* the file was touched there, and in what order. When cognition needs the
> content of a version no longer kept, recovery is the user's path, through their version control.
>
> ##### For active reasoning over content, identifier-only is insufficient
>
> A file recently the subject of `read`, `write` or `edit` in an engaged thread keeps its literal
> at its most recent position in Layer B, however many positions precede it, provided it is under
> `dedup.literal-share-threshold` ([§3.9.2]). A file at or above it is shown by identifier only;
> work that needs its content is not carried by the composed context.

**Other SPEC leaves (same change):**

- `2.3`:
  - "and the [§3.9] tracked-file sidecar" → "and the [§3.9] tracked-file store".
  - Add to the decision-class bullet: "a file event appears in an excerpt only as its identifier line
    ([§3.9.1])".
- `2.6.1`:
  - Delete `dedup.anchor-cadence`, `dedup.chain-retention-turns` and `dedup.chain-retention-days`.
  - `dedup.diff-literal-threshold: 0.7  # a change's diff at or above this fraction of the new content's size is not kept (§3.9.1)`.
  - `dedup.live-diff-window: 3  # N most-recent changes per file kept (§3.9.1) and rendered as diffs (§3.9.2)`.
  - Add `dedup.literal-share-threshold: 0.25  # a file whose current content is at or above this fraction of its thread's Layer B share is never expanded; identifier only (§3.9.2)`.
- `3.0.2` step 3: "Dedup decision ([§3.9]): a file-edit delta is buffered for the turn; at turn
  close it becomes an identifier line in the owner's excerpt and updates the tracked-file store
  ([§3.9.1]). Which positions render content is decided at composition ([§3.9.2])."
- `3.8.1`: "`files.json`" → "the tracked-file store".
- `3.8.3` "Two recovery surfaces, different strength" → "Archived *threads* recover from
  Personant's own home git, which it never rewrites: an unconditional guarantee. File content a
  thread no longer keeps ([§3.9.1]) is not Personant's to recover; its identifier names it in the
  user's version control."
- `3.10.6` last paragraph: "([§3.9.1], `files.json`)" → "([§3.9.1])".

---

