### P4 — Composition switch (expert; the evidence step)

**Scope.**
- `internal/workset/workset.go`:
  - `ThreadData` becomes `{Meta, Excerpts}`, each excerpt carrying its `Expansions`;
  - exported per-excerpt and thread-header renderers, used by both the adapter (sizing) and
    `renderLayerB` (output);
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

**Design to read:** [§3](index.md) invariants 1, 3, 6, 9, 10, 11, [§4](../4-acceptance-criteria.md) (adapter and `workset` rows), [§6](../6-post-sprint-review.md), §12
criteria 1 and 10.

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
- The sim's tracked file stays under the limit early in a run. Current content was 3,377 B at day 5
  and 6,908 B at day 10 (pre-change `files.json` snapshots, `wiki_adjacency_matrix.go`), so a 1d
  mock run expands it.

**Work.**
1. `workset` renders each excerpt with its expansions inside it, under delimiters of the coder's
   wording (design §14; the text is model-facing, so it names what the block is). It renders
   chronologically, and the literal follows a diff at the same position.
2. The adapter, per design [§6](../6-post-sprint-review.md):
   - load the indexes; a corrupt one logs `workset.warning`, and that thread gets no expansions;
   - run `Plan` once;
   - walk each thread newest-first, charging each excerpt and its expansions (and the separator)
     against the share remaining after the header;
   - always take the newest excerpt;
   - load bytes only for planned positions inside the walk; an expansion that cannot fit alone
     renders its identifier with a size note;
   - log a blob hash mismatch as `dedup.error` and render the identifier.
3. The expansion report:
   - `WorksetLayers` gains a field, for example `FileExpansions memops.FileExpansionReport`. It
     carries `LiteralMax` (the byte limit used) and one record per expansion **actually rendered**
     (after the walk, not as planned): thread id, excerpt turn, project, path, content id, kind
     (`literal` or `diff`), and the path's current content size from the index.
   - The harness check is a pure function over that report. It fails when a (project, path) has
     two literal records, when an id has two literal records, or when any record's size is at or
     above `LiteralMax`. `VerifyNoBudgetOverflow` calls it on the compose it already does.
   - It adds the step's counts to two counters registered in `metric_keys.go`:
     `MetricDedupExpansionsLiteral = "dedup_expansions_literal"` and
     `MetricDedupExpansionsDiff = "dedup_expansions_diff"`.
   - `logRungSummary` prints exactly `dedup-expansions: literal=<n> diff=<n>` (format
     `"dedup-expansions: literal=%d diff=%d"`). That is the line P5, P6 and C2 quote.

**Tests.**
- Compose: AC1 across two Layer B threads touching one path (latest wins; N diffs; others
  identifier-only); a shared id in two paths gets one literal.
- AC10: a file at the limit renders no literal or diff, and a file one byte under the limit
  renders its literal.
- A planned position outside the walk loads no bytes.
- A corrupt index logs `workset.warning` and still renders the thread.
- `workset`: expansion placement and order; the per-excerpt renderer's length equals that
  excerpt's share of the rendered thread.
- Compose: the report lists exactly the expansions rendered; a planned expansion cut by the walk
  is absent.
- Harness check, on planted reports (`internal/scenarios/invariants_test.go`), so it cannot pass
  vacuously: `TestFileExpansionCheck_FailsOnTwoLiteralsForOnePath`,
  `TestFileExpansionCheck_FailsOnSharedIDTwice`, `TestFileExpansionCheck_FailsOverLimit`,
  `TestFileExpansionCheck_PassesClean`.

**Contract.** Links to §3.9.1 target its current file name, as in P3.
- SPEC `3.9-working-set-content-dedup/index.md`: the design's §9 index text, scope paragraph
  included.
- SPEC `3.9.2-live-context-composition-working-window.md` and
  `3.9.4-identifier-only-references.md`: the design's §9 text.
- SPEC `3.9.3-diff-format.md`: the heading "Default: unified diff" becomes "Default: unified diff,
  from the prior content to the new" (design §9).
- SPEC `2.6.1-recognized-parameters.md`: the `dedup.live-diff-window` line becomes the design's
  wording. Add `dedup.literal-share-threshold: 0.25` with the design's comment.
- ARCHITECTURE `the-layered-working-set/index.md`: the "File content" bullet (design [§6](../6-post-sprint-review.md)).
- ARCHITECTURE `module-map.md`: the `internal/dedup/` row (design §10).

**Done-line and evidence.** `just build`;
`just test-run ./internal/workset '.'`;
`just test-run ./internal/memops/fileadapter 'TestComposeWorkingSet'`;
`just test-run ./internal/scenarios 'TestFileExpansionCheck|TestVerifyNoBudgetOverflow'`;
`just DURATION=1d sim` passes, and its output carries `dedup-expansions: literal=<n> diff=<n>`
with `literal` above zero. Quote that line in the package report: it is the precondition P5 and
P6 cite. Also `just check-links`.

---

