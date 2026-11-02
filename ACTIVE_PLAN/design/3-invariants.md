## 3. Invariants

1. **One literal per file, at its most recent position.** In one composed context, each (project,
   path) renders as a literal at most once, and each content id renders as a literal at most once.
   The literal sits at the file's most recent read/write position across all Layer B threads. If
   the walk does not reach that position, or the literal does not fit there
   ([§6](6-composition.md) step 3), no literal of that file renders; it is never moved to an
   earlier position.
2. **Positions are immutable text.** Identifier lines are written into an excerpt once, at turn
   close, by the turn writer. Nothing rewrites an excerpt or a folio line to place, swap or remove
   file content. Personant never writes file bytes into an excerpt. This keeps the decision-class
   rule of SPEC §2.3 and SPEC §3.10.6 intact.
3. **Composition never parses excerpt text.** Which positions expand comes from the store index plus
   excerpt turn numbers. Identifier lines are written for the model and humans only. This keeps the
   guarantee on machines: no parser depends on byte-exact prose that a model or user could imitate.
4. **No structure grows with edit count.** No stored or rendered tracked-file structure has a size
   proportional to the number of reads or edits of a path. Per path a thread holds one current blob,
   at most N diffs (plus superseded files until the next barrier) and one index line. The only
   per-event record is the identifier line in that turn's excerpt.
5. **Retention is our own policy.** No code on the dedup, retention or compose path runs git against
   the user's workspace, or reads its state. A missing, uncommitted, rebased or absent workspace repo
   changes nothing in what Personant keeps.
6. **Compose cost scales with what is rendered.** Per turn, compose is O(index lines of the Layer B
   threads) plus O(bytes of the blobs and diffs it renders) plus the excerpt reads it already does.
   Every fit decision is made from sizes recorded in the index (a blob's `size`, a change's
   diff `size`), so compose never reads a blob or diff it does not render. Diffs are computed once,
   at write time; compose never runs LCS.
7. **The turn path only adds.** The per-turn store write only adds or overwrites. Content-addressed
   blob and diff files are written before the index line that names them, so the index never names
   an absent file. Deletion happens only at barrier step B1c.
8. **One content-id definition.** A single `dedup` function defines content ids, and both the
   excerpt writer and the store use it.
9. **Expansions are charged to the budget.** The unit of charge is one excerpt: its text, the
   separator before it, and the expansions planned at its position. Units are charged against the
   share, and the overflow rule of [§6](6-composition.md) step 3 decides what fits. An expansion
   that does not fit renders as its identifier with a byte-size note. Render-time truncation
   remains only a backstop.
10. **Boundary checks.** `RecordFileEvents` validates its inputs:
    - thread id;
    - turn > 0;
    - `at` parses as RFC3339;
    - kind ∈ {read, write};
    - path non-empty and free of `\n`.

    Loading the index validates sort order and uniqueness of `path`. Loading a blob for render
    checks that `ContentID(bytes)` equals its name; on mismatch it renders the identifier and logs
    `dedup.error`. All violations go through the event log.
11. **A file too large for its share never expands, and one file takes at most 0.775 of the
    share.**
    - A (project, path) whose current content, at its most recent position, is at or above
      `dedup.literal-share-threshold` × share renders no literal and no diff anywhere in the
      composed context. Every one of its positions stays its identifier line.
    - A file under the threshold is expanded and charged as invariants 1 and 9 state. A diff of
      it is planned only if its recorded size is below `dedup.diff-literal-threshold` × the file's
      **current** size. The write-time rule measures a diff against the content it produced,
      which is larger than the current content when the file has since shrunk; the compose-time
      rule closes that gap. A diff that fails it stays its identifier line.
    - Both decisions read sizes from the index line, never the blob.
    - **Default 0.25.** With both rules, one file's literal plus its N diffs is below
      0.25 × (1 + 3 × 0.7) = 0.775 of the share, which leaves at least 22% of the share for the
      thread's own excerpts, the conversation that gives the literal its meaning. At the default
      budget the threshold is 0.25 × 77,916 B = 19,479 B (≈ 7.8k tokens).
    - The name follows the `dedup.*` directive form of SPEC §2.6.1 and, like the other `dedup.*`
      keys, is fixed at its default in v0.1 (a `dedup` package constant).

Out of scope, deliberately:

- opencode's transcript. SPEC §3.9 covers the memory-composed layers only; see the
  [scope note](entry-point.md).
- Work that needs the content of a file over the threshold: a delegated sub-mind that does the
  whole job and returns a summary (ROADMAP B6a). This design guarantees only that such a file never
  enters the composed context as a literal.
- The SPEC §3.9.3 natural-language diff fallback (unchanged).
- SPEC §3.0.2 step-4 turn-time budget enforcement (still a v0.1 non-goal).
- Filtering identifier lines out of recall and curator text. Those inputs include the lines
  unfiltered; nothing in SPEC §3.4 depends on excerpt purity at the line level.

---
