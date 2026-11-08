### Port-and-adapter at the memory store boundary

Application code (`internal/turn`, `internal/recall/measure`, `cmd/*`) depends on `memops.MemoryOps` — the conceptual operations port — not on the
memory store directly. One adapter ships (`internal/memops/fileadapter`: JSONL + markdown +
YAML frontmatter + TOML + go-git on the store directory); callers and tests depend only on the interface.

Pure renderers (`internal/workset.Compose`, `internal/recall/scoring`) are **store-independent**:
they import `memops`'s plain domain types (`SpineRecord`, `Budget`, `ThreadMeta`, ...) but never
the `MemoryOps` port itself, operating only on data the caller already fetched. They import nothing from `internal/store`; the pure helpers they use (`Normalize`, `DominantSource`) are in `memops`. The scenario harness and its invariants read the memory store directly (`h.Paths`; `store.LoadArchiveIndex` for the archived thread set) — the test-side validator pattern, not an application-code exception.

---

