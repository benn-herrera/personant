### WP2: Retire the front-end version line (mechanical)

- `internal/version/version.go`: delete `FrontEnd` and the FrontEnd paragraph of the package
  comment, which then lists two lines; remove "front end" from the short form (`parts`, and the
  example at line 123) and the long form (the field table).
- `internal/eventlog/bootstrap.go`: drop `frontend=` from the format string, the argument and the
  example comment (line 18).
- `cmd/version.go` `Long`: drop "front-end version,".
- Update `internal/version/version_test.go`, `internal/eventlog/bootstrap_test.go`, and
  `cmd/version_test.go` if it asserts the front-end line.
- Contract rows marked 2.
- Done-line: `just build`; `just test-run ./internal/version`;
  `just test-run ./internal/eventlog Bootstrap`; `just test-run ./cmd Version`;
  `git grep -n -i -E 'FrontEnd|frontend=|front[ -]end' -- internal/version internal/eventlog cmd PROJECT_DOCS/SPEC/4-user-surface/4.1-cli-command-surface.md PROJECT_DOCS/SPEC/9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/version-lines.md PROJECT_DOCS/ARCHITECTURE/testing-as-the-lab-bench/event-log-format.md`
  → empty. Checkpoint.

