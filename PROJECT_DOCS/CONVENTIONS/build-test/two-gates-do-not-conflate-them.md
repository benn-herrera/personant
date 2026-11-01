### Two gates — do not conflate them

`just test` (`fmt-check`, `go vet` and `go test` across the
Go packages) is the **CHECKPOINT GATE**: run it **once, before committing a checkpoint**. Go's test
cache scopes it: a package reruns only when its sources, dependencies, the files its tests read,
the env vars they read or the test flags change, so the sim package (tens of minutes) runs only
when a change could alter its outcome. A generator that rewrites a file tests read must leave
identical content untouched, or it defeats the cache. It is **not** an edit gate; never re-run it
between edits. To verify an edit landed, use the **EDIT GATE**:
`just build` plus `just test-run <pkg> <regexp>` for only the touched tests. **Commit at meaningful
checkpoints** — a complete, self-consistent change — not per edit, and run the checkpoint gate once
at that point. Probe plans gate as "Probe work" states.

