## 6. Composition

1. **Load.** For each Layer B thread (up to `BTopK`), load its frontmatter and `files.jsonl`.
   - A thread's project is its frontmatter `project` (`memops.ThreadMeta.Project`,
     `internal/memops/memops_model.go:178`), which compose already loads.
   - A missing index means no files. A corrupt one logs `workset.warning`; that thread then gets
     no expansions, and its identifier lines still render as text.
2. **Plan (`dedup.Plan`, pure).** The input is (thread id, project, entries) in Layer B order, plus
   `literalMax` = ⌈`dedup.literal-share-threshold` × share⌉, computed by the adapter. Rounding up
   makes the integer test `size >= literalMax` equal to the real-valued rule "at or above the
   threshold × share".
   - **Most recent position:** for each (project, path), the entry with the latest `at`; on equal
     `at`, the earlier thread in Layer B order.
   - **Size gate:** if that entry's `size` is at or above `literalMax`, the path gets no literal
     and no diffs. An older, smaller version elsewhere is not substituted.
   - **Literal:** otherwise that entry gets a literal candidate at (thread, `turn`). Every path
     under the gate gets one, including paths whose current content shares an id with another
     path's.
   - **Diffs:** for each path under the gate, take the N most recent changes of that (project,
     path) across all threads, ordered by `at`. Of those N, a change that was not kept
     (`diff:false`) or whose diff `size` is at or above `dedup.diff-literal-threshold` × the path's
     current `size` (invariant 11) does not render. It is not replaced by an older change.
3. **Walk.** Threads are walked in Layer B order. Each thread's excerpts are read newest-first,
   against the share remaining after the thread header and `workset.ThreadSeparator`. That
   separator is charged for every thread, including the last. Sizes come from the index and from
   `workset`'s length functions, so nothing is loaded to decide fit.
   - **Admission:** an excerpt is admitted only if its text, the separator before it, and a size
     note for each of its planned expansions all fit the remaining share. The first excerpt that
     is not admitted ends the walk. The exception is the newest excerpt, which is always admitted.
     Its notes are charged only while they fit; a note that does not fit is not rendered, and the
     identifier line already in the text names the content. SPEC §3.9.2 states this exception.
   - **Upgrade:** after admission, each of the excerpt's notes, in render order, becomes its
     content if the content's extra length over the note fits the remaining share.
   - **Shared id:** a literal candidate whose id has already rendered as a literal in this
     composition stays a note. The first one taken renders: Layer B order, then newest excerpt
     first, then path order within an excerpt. A path that loses keeps its diffs; its identifier
     line names the same id.
   - **Read failure:** an expansion whose bytes fail to read, or do not match the size the index
     recorded, renders as its size note. Its charge drops back to the note's, and `dedup.error` is
     logged.
   - **Backstop:** with this accounting a thread's rendering exceeds its share only when the
     newest excerpt's text alone does. Then the render-time truncation of SPEC §3.1 cuts it, and
     nothing else can be cut.
   - **Loading:** bytes are loaded only for expansions rendered as content. A planned position
     outside the walked excerpts, or an expansion left as a note, never loads its bytes.
   - **No relocation:** an expansion that does not render is not moved to another position
     (invariant 1).
4. **Render.** `workset` receives `ThreadData{Meta, Excerpts}`, where each excerpt carries
   `Expansions`, and renders them chronologically. At the same position, a diff renders before the
   literal.
5. **Report.** `ComposeWorkingSet` returns, beside the layer strings in `memops.WorksetLayers`, a
   structured report of the expansions it actually rendered. The report gives `literalMax` and,
   per expansion:
   - the thread, turn, project, path and kind (`literal` or `diff`);
   - `id`, or `from`/`to`;
   - the expansion's own byte size;
   - the path's current size.

   A size note is not an expansion and adds no record. Composition never parses the composed text
   (invariant 3). The sim harness check is a test oracle and does read the composed text. Over
   every composed step it checks:
   - invariants 1 and 11 on the report, the compose-time diff gate included: a diff record's own
     size is below `dedup.diff-literal-threshold` × its path size;
   - at most N diffs per path;
   - each literal at its path's index entry, the one with the latest `at` across Layer B;
   - each reported expansion's end marker present in Layer B, using `workset`'s marker function.

---
