# personant

## Build & validation
- **Two gates, never conflated (AGENTS.md "Two gates").** `make test` is the CHECKPOINT GATE — full suite, minutes, run *once before a commit*. It is NOT an edit gate: do not re-run it between edits. To verify an edit landed, use the EDIT GATE — `make build` (compile) + `make test-run PKG=<pkg> RUN=<regexp>` (only the touched test). Commit at checkpoints, not per-edit. Spawned coders inherit this via AGENTS.md; a task spec that has a coder re-run the full suite per edit (or revert→full-test→restore→full-test) is a process error — scope it to `test-run`.
