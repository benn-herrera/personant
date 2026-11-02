## 6. Composition

1. **Load.** For each Layer B thread (up to `BTopK`), load its frontmatter and `files.jsonl`.
   - A thread's project is its frontmatter `project` (`memops.ThreadMeta.Project`,
     `internal/memops/memops_model.go:178`), which compose already loads.
   - A missing index means no files. A corrupt one logs `workset.warning`; that thread then gets
     no expansions, and its identifier lines still render as text.
2. **Plan (`dedup.Plan`, pure).** The input is (thread id, project, entries) in Layer B order, plus
   `literalMax` = `dedup.literal-share-threshold` × share, computed by the adapter.
   - **Most recent position:** for each (project, path), the entry with the latest `at`; on equal
     `at`, the earlier thread in Layer B order.
   - **Size gate:** if that entry's `size` is at or above `literalMax`, the path gets no literal
     and no diffs. An older, smaller version elsewhere is not substituted.
   - **Literal:** otherwise that entry gets a literal at (thread, `turn`).
   - **Shared id:** an id gets at most one literal. When two paths' literal candidates share an id,
     the id goes to the candidate with the later `at`; on equal `at`, to the earlier thread in
     Layer B order; within one thread, to the path that sorts first. The losing path gets no
     literal; its identifier line names the same id. Its diffs are still planned.
   - **Diffs:** for each path under the gate, take every thread's `changes` for that (project,
     path) and order them by `at`. Of those with `diff:true` and a diff `size` below
     `dedup.diff-literal-threshold` × the path's current `size` (invariant 11), the N most recent
     get diffs at their (thread, turn).
3. **Walk.** Read each thread's excerpts newest-first, against the share remaining after the
   thread header. Each excerpt is one unit: its text, the separator before it, and its planned
   expansions in render order. Sizes come from the index and from `workset`'s expansion-length
   function, so nothing is loaded to decide fit.
   - **Bare excerpt:** if the excerpt text plus separator does not fit the remaining share, the
     walk stops, and no older excerpt is taken. The exception is the newest excerpt, which is
     always taken. Alone over the share, it is cut by the render-time backstop, as today.
   - **Expansions:** after the excerpt is taken, each planned expansion, in render order, is taken
     if it fits the remaining share. One that does not fit renders as its identifier with a
     byte-size note, and the note is charged. The always-taken newest excerpt follows the same
     rule, so its expansions render only if they fit.
   - **Loading:** bytes are loaded only for expansions taken. A planned position outside the
     walked excerpts, or an expansion that did not fit, never loads its bytes.
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

   A size note is not an expansion and adds no record. Checks and counters read that report;
   nothing parses the composed text (invariant 3). The sim harness check covers invariants 1 and
   11 over every composed step, the compose-time diff gate included: a diff record's own size must
   be below `dedup.diff-literal-threshold` × its path size.

---
