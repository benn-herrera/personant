### Read path, per reader

Every excerpt reader goes through three functions in `internal/store/thread_io.go`. Command and
answer (non-test call sites):

```
$ git grep -n 'ReadThreadBody(\|ReadThreadExcerpts(\|ReadDebtWindowExcerpts(\|store\.LoadThread(' \
    -- '*.go' ':!*_test.go'
internal/memops/fileadapter/fileadapter.go:236:  store.LoadThread(a.paths, threadID)
internal/memops/fileadapter/fileadapter.go:263:  store.ReadThreadExcerpts(a.paths, threadID)
internal/memops/fileadapter/fileadapter.go:278:  store.ReadDebtWindowExcerpts(a.paths, threadID, maxN)
internal/memops/fileadapter/fileadapter_archive.go:340:  store.ReadThreadBody(a.paths, id, 0)
internal/memops/fileadapter/workset_compose.go:274:  store.ReadThreadBody(a.paths, id, perThread)
internal/store/thread_io.go:351:  ReadThreadBody(paths, threadID, 0)   (inside LoadThread)
(plus the three definitions, thread_io.go:188, 251, 300)
$ git grep -n '\.LoadThread(ctx\|\.LoadThreadExcerpts(ctx\|\.LoadDebtWindowExcerpts(ctx' \
    -- '*.go' ':!*_test.go'
internal/recall/exact/exact.go:146            LoadThreadExcerpts (GrepThreads)
internal/recall/measure/measure.go:534        LoadThread (coarse index build)
internal/recall/measure/measure.go:734,739    LoadThread + LoadThreadExcerpts (flush job)
internal/recall/measure/measure.go:1363       LoadDebtWindowExcerpts (completeness floor)
internal/recall/measure/veccache.go:241,245   LoadThreadExcerpts + LoadThread (cache reconcile)
internal/turn/closure.go:442                  LoadThread (closure curator)
internal/turn/commands.go:260                 LoadThread (command fetch)
internal/turn/lru.go:181                      LoadThread (Layer B fetch)
$ git grep -n 'os.ReadDir\|filepath.WalkDir\|filepath.Walk(' -- '*.go' ':!*_test.go'
(the only reader of turns/ is internal/store/thread_io.go:576, inside turnFileNumbers; the others
 list threads/, objects/, logs/, projects/, the home root for tmp- residue, or hash a whole
 directory)
```

Decision for every reader: **read the folio directly; never unbind on read.** A read that changed
the tree would dirty the worktree outside the turn's scoped write set, add crash cells to the read
path, and make a recall fetch a canonical write. The port interface (`memops.MemoryOps`) does not
change, so no caller or test double changes.

| Reader | Needs | Folio access |
|---|---|---|
| `ReadThreadBody` (Layer B, `LoadThread`: closure, fetch, coarse index, archive size) | newest ≤ 512 within a byte budget | only when the read needs more than min(loose count, H) excerpts (invariant 8): warm threads after a barrier never decode it |
| `ReadDebtWindowExcerpts` (completeness floor) | the `maxN` below the newest 512 | only when loose count < 512 + `maxN` |
| `ReadThreadExcerpts` (flush job, cache reconcile, `GrepThreads`) | all, oldest first | always when a folio exists: one file replaces many |
| archival capture / recovery (`autogit.CheckoutTree`, `TreeHashAt`, `WorktreeTreeHash`) | the directory as bytes | the folio is one more file in the subtree; tree hash and byte-exact restore are unchanged |
| `personant verify` | structure | WP3 |
| `personant index` | none | derived files come from spine and frontmatter only |

A warm thread whose window reaches into its folio exists only between a retired thread's
re-engagement (or recovery) and the next barrier, which moves its newest H back to loose files.
Until then such a read decodes the whole folio once per read and takes entries newest-first from
memory. Readers return the warmed thread's bound excerpts byte-exact from the moment it warms; only
the file location lags.

**Latent defect folded in.** `turnFileNumbers` accepts any `<digits>.md` stem, but every reader
re-derives the file name with `turnFileName(n)` (`thread_io.go:216, 265, 329`). A loose `2.md` is
therefore listed and then read from a nonexistent `0000002.md`, which is an error.
`TestBarrier_OvernightHandEdit` (`fileadapter_barrier_test.go:693-735`) writes exactly such a file
and never reads it back. The new listing carries each entry's actual name.

