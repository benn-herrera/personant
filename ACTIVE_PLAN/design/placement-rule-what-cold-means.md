### Placement rule: what "cold" means

Two signals the code already uses decide placement:

- **A thread is wholly cold exactly when it is in a retired state.** `memops.IsRetiredState`
  (`internal/memops/memops_barrier.go:90-95`): "Only resolved / decided / abandoned threads — the
  §3.5 retire-class states — may be archived; active/wip/paused/blocked are live". This is
  archival's coldness rule (SPEC §3.8.1,
  `PROJECT_DOCS/SPEC/3-core-algorithms/3.8-deep-cold-archival/3.8.1-mechanism.md` line 4:
  "Archival drains a **batch** of the coldest retired threads"), and it is canonical (spine),
  which keeps placement deterministic. Idleness is not a coldness signal: a parked
  wip/paused/blocked thread keeps its newest H loose. Layer B/C membership (`working-set.json`) is
  not used: it is operational, gitignored state.
- **Warm window = assembly window + steady-state debt window.** `store.ThreadTurnWindow = 512`
  (`internal/store/thread_io.go:35`) bounds what `ReadThreadBody` assembles.
  `ReadDebtWindowExcerpts` reads the `maxN` excerpts just below that window; recall passes
  `(1 + pending) × EmbeddingDebtCap`, where `const EmbeddingDebtCap = 16`
  (`internal/turn/lru.go:26`) and pending is 0 in steady state (`internal/recall/measure/measure.go`
  :1337-1338: "P is 0 in the steady state").

Rule: for a retired thread, every excerpt goes to the folio. For any other thread, the newest H
entries by (number, name) stay loose and the rest go to the folio. Exceptions: too-large excerpts
always stay loose, and conflicting names do not move. A thread active across days
keeps its newest 528 loose, so each barrier binds only that day's overflow and unbinds nothing.
Unbinding happens only when a retired thread is re-engaged (`internal/turn/engage.go:596-599`
promotes it to `active`) or recovered from archive: at the next barrier its newest H come back
loose. A backlogged indexer (pending > 0) reads below H from the folio, which is correct but slower.

