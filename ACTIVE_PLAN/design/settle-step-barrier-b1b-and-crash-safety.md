### Settle step (barrier B1b) and crash safety

B1b runs after B1 (archival, so archived threads are gone) and before B2 (so B2's `Add(".")` stages
the moves into the day-commit). Today `runBarrier` calls `crashpoint.At(cpBarrierPostArchival)` and
then `a.barrierTail(ctx, day, res, false)` (`fileadapter_barrier.go:169-171`). B1b goes between
those two calls. The completion path re-runs the B1 drain only `if !sealed`
(`fileadapter_barrier.go:804-808`). B1b runs under the same guard, after the drain: once the
day-commit has landed, settle has already finished.

For each live spine record, ascending by id, `store.SettleExcerptTiers` does the following:

1. Write each entry being unbound as a loose file (`WriteFileAtomic`), then fsync `turns/`.
2. Write the new folio, (old − unbound − dropped duplicates) ∪ bound, sorted, with
   `WriteFileAtomic`. Fsync the thread directory. Read the folio back and check that it decodes to
   exactly the intended entries (an expectation check). If it comes out empty, remove the file.
3. Remove the loose files of the bound entries and the loose duplicates whose target is the folio.
   Remove `turns/` once it is empty.

The fsyncs exist because `WriteFileAtomic` does not fsync the parent directory
(`internal/recovery/recovery.go:213-214`: "WriteFileAtomic does not fsync the parent dir after its
rename"). Without them, a power loss could keep a loose file's unlink but lose the folio's rename.
A settle I/O error aborts the barrier like any other barrier step: the marker stays and completion
retries. Per-name anomalies (too large, conflict, unreadable folio) are logged skips, not errors.
An excerpt to be bound whose bytes fail `utf8.Valid` is an invariant violation (invariant 2):
settle checks every such excerpt of a thread before step 1 and returns an error naming its
home-relative path, having written nothing for that thread. The barrier then fails like any settle
error, and each completion retry fails the same way until the file is repaired by hand.
Settle reads a thread's folio only when it may have to move something: the folio exists
and the thread has loose excerpts or is warm. A retired thread that holds only a folio costs one
directory stat.

**Crashpoints.** Four in `internal/store/folio.go`, one in the barrier. A `folio.settle.*` point
fires only for a thread whose settle has work in that step; a step with no work fires nothing:

| Crashpoint | Fires (per thread) | Needs |
|---|---|---|
| `folio.settle.midLooseWrite` | after the first unbound loose file is written, before the second | ≥ 2 unbinds |
| `folio.settle.postLooseWrite.preFolioWrite` | after step 1, before step 2 | ≥ 1 unbind |
| `folio.settle.postFolioWrite.preLooseRemove` | after step 2's folio write or removal, before step 3 | any bind or unbind |
| `folio.settle.midLooseRemove` | after the first loose removal, before the second | ≥ 2 loose removals |
| `barrier.postSettle.preDayCommit` | once per barrier, after B1b, before B2 | always |

Every crash state converges by roll-forward under the existing `op=barrier` cell. There is no new
recovery cell and no reset (INV-1). A torn folio temp file is `tmp-folio.jsonl`, which recovery
phase 1 already sweeps unconditionally (`internal/recovery/recovery.go:212-219`).

