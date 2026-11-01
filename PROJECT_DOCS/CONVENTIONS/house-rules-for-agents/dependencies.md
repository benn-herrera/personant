### Dependencies

General dependency discipline: ARCHITECTURE.md "Approved-when-earned
dependencies". `go.mod` is the catalog.
- **Permanently out** (architectural conflict): `langchaingo/memory`, `langchaingo/chains`,
  `mattn/go-sqlite3`. Do not reintroduce.
- **SQLite:** any Go SQLite need takes `modernc.org/sqlite` first.
- **Open:** U5 — how the service reaches its own models decides whether `langchaingo/llms` is
  needed. Until then `langchaingo/llms` stays out; the OpenAI-compatible client in
  `internal/model` covers the need.
- **Vet adoption and maintenance before proposing a dependency — from the registry, not the
  README.** Record, measured not asserted: (1) last release date; (2) `Imported by` count on
  pkg.go.dev; (3) deprecation status; (4) transitive dep count; (5) license. For a port, verify
  the port's activity, not its upstream's. Default to the well-trodden option unless the
  off-standard gain is substantial. The record goes in the message of the commit that pins the
  module with `just add-dependency`, and is written before that recipe runs. (Origin: commit
  5692c75.)
- **Pending removal with their consumers** (ROADMAP.md B1, "Retire the self-built harness"):
  `codeberg.org/readeck/go-readability/v2` and `github.com/JohannesKaufmann/html-to-markdown/v2`
  serve only `internal/tools/web/`. Do not reintroduce `github.com/peterh/liner` or any other
  library that takes the process's terminal fds, termios or `SIGWINCH` handling: `internal/term/`
  owns the terminal (origin: commit c1ddea2). Both entries go in the change that removes their
  consumer.
- **Application code talks to `MemoryOps`, not to the substrate directly.** Package-level detail
  and the substrate-free exceptions: ARCHITECTURE.md "Port-and-adapter at the substrate boundary".
