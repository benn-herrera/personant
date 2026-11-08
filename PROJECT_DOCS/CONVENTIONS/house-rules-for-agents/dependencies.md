### Dependencies

- **A dependency lands with its first consumer, never before**; no hand-built complex substitute
  for one that would just work. `go.mod` is the catalog.
- **Permanently out** (architectural conflict): `langchaingo/memory`, `langchaingo/chains`,
  `mattn/go-sqlite3`. Do not reintroduce.
- **SQLite:** any Go SQLite need takes `modernc.org/sqlite` first.
- `langchaingo/llms` stays out: the memory service makes no model calls (SPEC §8.2.1).
- **Vet adoption and maintenance before proposing a dependency — from the registry, not the
  README.** Record, measured not asserted: (1) last release date; (2) `Imported by` count on
  pkg.go.dev; (3) deprecation status; (4) transitive dep count; (5) license. For a port, verify
  the port's activity, not its upstream's. Default to the well-trodden option unless the
  off-standard gain is substantial. The record goes in the message of the commit that pins the
  module with `just add-dependency`, and is written before that recipe runs. (Origin: commit
  5692c75.)
- **Application code talks to `MemoryOps`, not to the memory store directly.** Package-level
  detail and the store-independent exceptions: ARCHITECTURE "Port-and-adapter at the memory store
  boundary".
