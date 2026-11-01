### Module and symbol placement

| Where | What | Must not |
|---|---|---|
| `internal/memops` | `EmbeddingDebtCap = 16` moves here; `turn.EmbeddingDebtCap` becomes `= memops.EmbeddingDebtCap`, so its 14 referencing files are untouched | gain any folio or tier term: tiers are a file-substrate mechanism (`PROJECT_DOCS/ARCHITECTURE/substrate-non-negotiables/port-abstraction-policy.md` rule 1) |
| `internal/store` (new `folio.go`) | folio codec, exported `ReadFolio` and `ListExcerptEntries`, unexported bind/unbind, placement, `SettleExcerptTiers`, `HotExcerptTail`, `ThreadFolioPath`/`ThreadFolioRel`, the four `folio.settle.*` crashpoints | log events (it returns outcomes), or decide retirement itself (the caller passes `retired bool`) |
| `internal/store/thread_io.go` | the three readers become tier-transparent; `turnFileNumbers` replaced | unbind on read |
| `internal/memops/fileadapter` | B1b `settleExcerptTiers` in `fileadapter_barrier.go`; logs `barrier.folio-settled` / `barrier.folio-skip` | read or write folio lines itself |
| `internal/verify` | folio structure checks through `store.ReadFolio` / `store.ListExcerptEntries` | read every loose excerpt; parse folio lines itself |
| `internal/memops/homeformat.go`, `cmd/main.go`, `internal/version` | format 2; the migration registry and its 1→2 step | run before the gate, or after Reconcile |

Incumbent search: `grep -n '^func ' internal/store/jsonl.go` returns the generic
`ReadJSONL[T]` (line 22) and `WriteJSONL[T]` (line 63, "sorted by keyFn ascending … Atomic …
Duplicate keys are detected before any write"), plus spine and symbol wrappers. The folio reuses
both. Its only new code is the line type, the sort key, the line-cap guard and the two-tier
logic. `ReadJSONL`'s one limit is its 1 MiB line cap, which the too-large rule handles. Looked for
an index-entry counter in `internal/autogit` (`grep -n '^func ' internal/autogit/*.go`). The only
counter is `LooseObjectCount` (`autogit.go:306`), which counts loose objects under `objects/`,
not index entries, so WP0 adds `IndexEntryCount`. Looked for an existing migration mechanism
(`git grep -n -i 'migrat' -- '*.go' ':!*_test.go'`): only `ErrHomeFormatNoMigration` and comments
naming a future migration (`homeformat.go:30-32, 66-76`, `memops.go:628, 636`), so WP2 adds the
registry.

---

