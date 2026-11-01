### Port-and-adapter at the substrate boundary

Application code (`internal/turn`, `internal/opencodehook`, `internal/recall/measure`, `cmd/*`,
scenarios harness) depends on `memops.MemoryOps` — the conceptual operations port — not on the
substrate directly. One adapter ships today (`internal/memops/fileadapter`: JSONL + markdown + YAML
frontmatter + TOML + go-git on `~/.personant/`); future adapters implement the same interface
without rewriting callers or tests.

Pure renderers (`internal/workset.Compose`, `internal/recall/scoring`) are **substrate-free**: they
import `memops`'s plain domain types (`SpineRecord`, `Budget`, `ThreadMeta`, ...) but never the
`MemoryOps` port itself, operating only on data the caller already fetched. What stays direct from
`internal/store` for the same reason: pure helpers (`Normalize`, `DominantSource`) and data-type
aliases (`ThreadFrontmatter`, ...). Tests inspect the substrate directly via `h.Paths` in invariants
— that's the test-side substrate validator pattern, not an application-code exception.

---

