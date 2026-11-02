### P4 — Composition switch (expert; the evidence step)

**Scope.**
- `internal/workset/workset.go`:
  - `ThreadData` becomes `{Meta, Excerpts}`, each excerpt carrying its `Expansions`;
  - exported per-excerpt and thread-header renderers, used by both the adapter (sizing) and
    `renderLayerB` (output);
  - an exported function giving an expansion's rendered length from its content size and header
    fields, and the size note's rendered length, so the adapter sizes expansions and notes without
    loading bytes;
  - each excerpt's `Expansions` entry is either a taken expansion (with its bytes) or a not-taken
    one marked as such (no bytes, with its byte size), for which `workset` renders the size note.
    The Go shape of that mark is the coder's choice (design §14);
  - `TrackedFile`, `appendTrackedFiles`, `writeTrackedFile` and the `dedup` import removed.
- `internal/workset/workset_test.go`.
- `internal/memops/fileadapter/workset_compose.go`: `loadActiveThreadData`, `loadOneThreadData`,
  `loadTrackedFiles`.
- `internal/memops/fileadapter/workset_compose_test.go`.
- `internal/memops/memops_types.go`: the expansion report on `WorksetLayers` (Work 3).
- `internal/scenarios/invariants.go` and `invariants_test.go`: the per-step expansion check and its
  unit tests.
- `internal/scenarios/metric_keys.go`: two counter keys.
- `internal/scenarios/harness.go`: only if the counters need plumbing beyond the existing
  `Metrics *metrics.Run` field (`harness.go:734`).
- `internal/scenarios/sim/sim_rung_test.go`: the summary line in `logRungSummary`.
- The contract leaves listed under "Contract".

**Design to read:** [design §2](../../design/2-design-in-one-paragraph.md) (terms);
[design §3](../../design/3-invariants.md) invariants 1, 3, 6, 9, 10, 11;
[design §4](../../design/4-module-skeleton.md) (adapter, `workset` and port rows);
[design §6](../../design/6-composition.md) (all steps);
[design §12](../../design/12-acceptance-criteria.md) criteria 1, 5 and 10.

**Facts.**
- `ThreadData` references: `git grep -l 'ThreadData' -- '*.go'` →
  `internal/memops/fileadapter/workset_compose.go`, `internal/workset/workset.go`,
  `internal/workset/workset_test.go`. `grep -c "ThreadData{" internal/workset/workset_test.go` → 8
  literal sites.
- `TrackedFile` references: `git grep -l 'TrackedFile' -- '*.go'` → `internal/workset/workset.go`,
  `workset_test.go`, `internal/memops/fileadapter/workset_compose.go`, `workset_compose_test.go`,
  and `internal/autogit/reset_test.go`. The last is an unrelated helper, `seedTrackedFile`, and is
  not touched.
- `WindowEntry` references: `git grep -l 'WindowEntry' -- '*.go'` → `internal/dedup/livewindow.go`,
  `internal/workset/workset.go`, `workset_test.go`. After P4 only `livewindow.go` remains (P6
  deletes it).
- Tests replaced: `internal/workset/workset_test.go` `TestLayerBTrackedFilesSection` and
  `TestLayerBNoTrackedFilesSection`; `internal/memops/fileadapter/workset_compose_test.go`
  `TestComposeWorkingSet_LayerB_TrackedFiles`, which seeds through `RecordFileWrite` and asserts
  `=== tracked files ===`.
- The share. `workset.PerThreadBudget(layerB, n)` is `layerB / n`, floored at 4096
  (`minPerThreadBudget`, `internal/workset/workset.go`). `loadActiveThreadData` already computes
  `perThread := workset.PerThreadBudget(budget.LayerB, limit)`. The literal limit is
  `int(dedup.LiteralShareThreshold × perThread)`. At the default budget,
  `200000 × 2.5 × 0.85 × 0.55 / 3` = 77,916 B (design E1), so the limit is 19,479 B.
- Today `loadOneThreadData` reads the frontmatter, then `ReadThreadBody(a.paths, id, perThread)`,
  then `loadTrackedFiles`, which runs `Chain.LiveWindow` over every tracked file. `Plan` needs every
  Layer B thread's index before any thread is walked, so loading becomes two passes: indexes and
  frontmatter for all threads, then one `Plan`, then the per-thread walks.
- `renderLayerB` truncates each thread to `perThread` and the layer to `LayerB`
  (`internal/workset/workset.go`, `renderLayerB`). That stays as the backstop (invariant 9).
- Per-step compose in the harness. `VerifyNoBudgetOverflow` (`internal/scenarios/invariants.go:561`)
  calls `h.Ops.ComposeWorkingSet` on mock rungs and is in `cheapDefaultInvariants`. The AC1 / AC10
  assertion and the expansion count ride on that compose; no second `ComposeWorkingSet` call is
  added per step.
- The channel. `memops.WorksetLayers` holds only the five layer strings
  (`internal/memops/memops_types.go:216–228`), and invariant 3 forbids parsing composed text, so
  the check cannot read the expansions out of `LayerB`. The adapter returns a structured report
  in a new `WorksetLayers` field. Adding a field changes no existing literal:
  `git grep -l 'WorksetLayers' -- '*.go'` → `internal/memops/fileadapter/workset_compose.go`,
  `internal/memops/memops.go`, `internal/memops/memops_types.go`, and
  `git grep -n "WorksetLayers{" -- '*.go'` → `workset_compose.go:38, 67, 69` only (two empty
  returns and one keyed literal). `turn` copies the layer fields one by one into
  `prompt.SystemPromptElements` (comment above the type), so the new field never reaches the
  prompt.
- Metric keys that cross the harness ↔ sim seam live in `internal/scenarios/metric_keys.go` (its
  header comment: "the single source of truth for the metric KEY strings"). The harness writes
  them with `h.Metrics.Counter(name, delta)` (for example `internal/scenarios/harness_run.go:347`).
  The rung summary lines are printed by `logRungSummary` (`internal/scenarios/sim/sim_rung_test.go`,
  after line 406, for example `t.Logf("turns:            %d", rep.turns)`).
- The sim's tracked file stays under the limit early in a run. Its current content was 3,403 bytes
  at day 5 and 6,962 bytes at day 10 (pre-change `files.json` snapshots of the same seed,
  `wiki_adjacency_matrix.go`), so a 1d mock run expands it.
- A thread's project is its frontmatter `project` (`memops.ThreadMeta.Project`,
  `internal/memops/memops_model.go:178`); `loadOneThreadData` already loads the frontmatter
  (`store.LoadThreadFrontmatter`).

**Work.**
1. `workset` renders each excerpt with its expansions inside it, under delimiters of the coder's
   wording (design §14; the text is model-facing, so it names what the block is). It renders
   chronologically, and at one position a diff renders before the literal.
2. The adapter implements [design §6](../../design/6-composition.md) steps 1–3:
   - load frontmatter and indexes for every Layer B thread; a corrupt index logs
     `workset.warning`, and that thread gets no expansions;
   - run `Plan` once;
   - walk each thread newest-first under design §6 step 3's overflow rule. The unit is an excerpt
     plus the separator before it plus its planned expansions. A bare excerpt that does not fit
     ends the walk, except the newest, which is always taken. Each expansion is taken only if it
     fits; one that does not renders its identifier with a charged size note;
   - size from the index (`size` fields) and `workset`'s expansion-length function; load bytes
     only for expansions taken;
   - log a blob hash mismatch as `dedup.error` and render the identifier.
3. The expansion report:
   - `WorksetLayers` gains a field, for example `FileExpansions memops.FileExpansionReport`. It
     carries `LiteralMax` (the byte limit used) and one record per expansion **actually rendered**
     (after the walk, not as planned). The record carries P1's `Plan` record fields: thread id,
     excerpt turn, project, path, kind (`literal` or `diff`), `id` or `from`/`to`, the
     expansion's own byte size, and the path's current size. A size note is not an expansion; it
     adds no report record.
   - The harness check is a pure function over that report. It fails when:
     - a (project, path) has two literal records;
     - an id has two literal records;
     - any record's path size is at or above `LiteralMax`;
     - a diff record's own size is at or above `DiffLiteralThreshold` × its path size (the
       compose-time diff gate, invariant 11).

     `VerifyNoBudgetOverflow` calls it on the compose it already does.
   - It adds the step's counts to two counters registered in `metric_keys.go`:
     `MetricDedupExpansionsLiteral = "dedup_expansions_literal"` and
     `MetricDedupExpansionsDiff = "dedup_expansions_diff"`.
   - `logRungSummary` prints exactly `dedup-expansions: literal=<n> diff=<n>` (format
     `"dedup-expansions: literal=%d diff=%d"`). That is the line P5, P6 and C2 quote.

**Tests.** Every compose test in `internal/memops/fileadapter/workset_compose_test.go` is named
`TestComposeWorkingSet_…`, so the done-line runs it.
- `TestComposeWorkingSet_InPlace…`: AC1 across two Layer B threads touching one path (latest
  wins; N diffs; others identifier-only); a shared id in two paths gets one literal.
- `TestComposeWorkingSet_ShareThreshold…`: AC10. A file at the limit renders no literal or diff,
  and a file one byte under the limit renders its literal.
- `TestComposeWorkingSet_LoadsOnlyRendered…`: a planned position outside the walk, and an
  expansion that did not fit, load no bytes.
- `TestComposeWorkingSet_Overflow…`:
  - an excerpt that fits bare but whose literal does not gets the identifier and a size note, and
    the walk continues;
  - a bare excerpt that does not fit ends the walk;
  - the newest excerpt is taken even alone over the share.
- `TestComposeWorkingSet_ShrunkFileDiffGate…`: after the file shrank, a stored diff at or above
  `DiffLiteralThreshold` × the current size does not render.
- `TestComposeWorkingSet_CorruptIndex…`: a corrupt index logs `workset.warning` and still renders
  the thread.
- `TestComposeWorkingSet_LeavesExcerptsByteIdentical`: `turns/` and `folio.jsonl` bytes are
  identical before and after compose (criterion 5).
- `TestComposeWorkingSet_Report…`: the report lists exactly the expansions rendered. A planned
  expansion cut by the walk is absent, and a size note adds no record.
- `workset` (`internal/workset/workset_test.go`, run by the done-line's `'.'`):
  - expansion placement and order;
  - the per-excerpt renderer's length equals that excerpt's share of the rendered thread;
  - the expansion-length and size-note-length functions each equal the rendered length.
- Harness check, on planted reports (`internal/scenarios/invariants_test.go`), so it cannot pass
  vacuously: `TestFileExpansionCheck_FailsOnTwoLiteralsForOnePath`,
  `TestFileExpansionCheck_FailsOnSharedIDTwice`, `TestFileExpansionCheck_FailsOverLimit`,
  `TestFileExpansionCheck_FailsOnDiffOverGate`, `TestFileExpansionCheck_PassesClean`.

**Contract.** The text to land is in
[design §9](../../design/9-proposed-spec-text-replaces-the-leaves-under-project-docs-spec-3-core-algorithms-3-9-working-set-content-dedup.md)
(SPEC) and [design §10](../../design/10-architecture-changes-same-change.md) (ARCHITECTURE). Links
to SPEC §3.9.1 target its current file name, as in P3.
- SPEC §3.9 `3.9-working-set-content-dedup/index.md`: the index text, scope paragraph included.
- SPEC §3.9.2 `3.9.2-live-context-composition-working-window.md`, including its `**Open:**` line,
  and SPEC §3.9.4 `3.9.4-identifier-only-references.md`.
- SPEC §3.9.3 `3.9.3-diff-format.md`: the heading "Default: unified diff" becomes "Default: unified
  diff, from the prior content to the new".
- SPEC §2.6.1 `2.6.1-recognized-parameters.md`: the `dedup.live-diff-window` entry, including its
  continuation comment, becomes the design's wording. Add the `dedup.literal-share-threshold`
  entry.
- ARCHITECTURE `the-layered-working-set/index.md`: the "File content" bullet.
- ARCHITECTURE `module-map.md`: the `internal/dedup/` row.

**Done-line and evidence.** `just build`;
`just test-run ./internal/workset '.'`;
`just test-run ./internal/memops/fileadapter 'TestComposeWorkingSet'`;
`just test-run ./internal/scenarios 'TestFileExpansionCheck|TestVerifyNoBudgetOverflow'`;
`just DURATION=1d sim` passes, and its output carries `dedup-expansions: literal=<n> diff=<n>`
with `literal` above zero. Quote that line in the package report: it is the precondition P5 and
P6 cite. Also `just check-links`.

---

