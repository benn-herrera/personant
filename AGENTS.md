# AGENTS – Personant

## Build & validation
- **Project documents** (THESIS, SPEC, ARCHITECTURE, CONVENTIONS) live under `PROJECT_DOCS/`, one
  leaf per section; the root files only point there. Edit leaves, never the pointers
  ([CONVENTIONS, Project documents](PROJECT_DOCS/CONVENTIONS/repo-state/project-documents.md)).
- **Two gates, never conflated ([CONVENTIONS, Two gates](PROJECT_DOCS/CONVENTIONS/build-test/two-gates-do-not-conflate-them.md)).** `just test` is the CHECKPOINT GATE —
  full suite, scoped by Go's test cache, run *once before a commit*. It is NOT an edit gate: do not
  re-run it between edits. To verify an edit landed, use the EDIT GATE — `just build` (compile) + `just
  test-run <pkg> <regexp>` (only the touched test). Commit at checkpoints, not per-edit. Spawned
  coders inherit this via CONVENTIONS.md; a task spec that has a coder re-run the full suite per
  edit (or revert→full-test→restore→full-test) is a process error — scope it to `test-run`.
