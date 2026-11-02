## 6. Composition

1. **Load.** For each Layer B thread (up to `BTopK`), load `files.jsonl`. A missing file means no
   files. A corrupt one logs `workset.warning`, and that thread then gets no expansions; its
   identifier lines still render as text.
2. **Plan (`dedup.Plan`, pure).** The input is (thread id, project, entries) in Layer B order, plus
   the literal size limit `dedup.literal-share-threshold` × per-thread share, computed by the
   adapter.
   - **Size gate:** for each (project, path), the entry with max `at` (ties go to Layer B order) is
     its most recent position. If that entry's `size` is at or above the limit, the path gets no
     literal and no diffs; an older, smaller version elsewhere is not substituted.
   - **Literal:** otherwise that entry gets a literal at (thread, `turn`). An id already granted a
     literal is not granted again.
   - **Diffs:** for a path under the limit, across all threads' `changes` for that (project, path),
     the N most recent with `diff:true` get diffs at their (thread, turn).
3. **Walk.** Read each thread's excerpts newest-first. For each excerpt:
   - load its planned expansions;
   - render the excerpt and its expansions with `workset`'s per-excerpt renderer, and charge the
     result's length against the share remaining after the thread header;
   - stop before the excerpt that does not fit, except that the newest excerpt is always taken
     (alone over the share, it is cut by the render-time backstop, as today);
   - the separator between excerpts is charged with the excerpt it precedes;
   - if a single expansion does not fit even alone, render the identifier with a size note.
   - A planned position outside the walked excerpts never loads its bytes.
4. **Render.** `workset` receives `ThreadData{Meta, Excerpts}`, where each excerpt carries
   `Expansions`, and renders them chronologically. The literal goes after a diff at the same
   position.
5. **Report.** `ComposeWorkingSet` returns, beside the layer strings in `memops.WorksetLayers`, a
   structured report of the expansions it actually rendered. The report gives the literal limit
   used and, per expansion, the thread, turn, project, path, id, kind and the path's current size.
   Checks and counters read that report; nothing parses the composed text (invariant 3).

---

