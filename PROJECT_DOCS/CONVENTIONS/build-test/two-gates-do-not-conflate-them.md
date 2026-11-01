### Two gates — do not conflate them

`just test` (`fmt-check`, `go vet` and `go test` across the
Go packages) is the **CHECKPOINT GATE**: run it **once, before committing a checkpoint**. Go's test
cache scopes it: a package reruns only when its sources, dependencies, the files its tests read,
the env vars they read or the test flags change, so the sim package (tens of minutes) runs only
when a change could alter its outcome. The cache keys what tests read by stat info (size, mode,
mtime), not content, and covers every directory a test stats, opens or path-resolves. So a
generator that rewrites a file tests read leaves identical content untouched; a test never
creates or removes entries in a directory another package's tests read (run homes under
`test/rundata/` are emptied, never replaced); and a miss is diagnosed with
`just test-cache-why <pkg>`, which names the changed input. It is **not** an edit gate; never re-run it
between edits. To verify an edit landed, use the **EDIT GATE**:
`just build` plus `just test-run <pkg> <regexp>` for only the touched tests. **Commit at meaningful
checkpoints** — a complete, self-consistent change — not per edit, and run the checkpoint gate once
at that point. Probe plans gate as "Probe work" states.

