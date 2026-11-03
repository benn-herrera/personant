### WP1: Remove the REPL, terminal, shell and the `ping`/`models` verbs (mechanical)

- Delete `internal/chat/`, `internal/term/`, `internal/shell/`, `internal/ping/`,
  `internal/modellist/`, `cmd/chat.go`, `cmd/ping.go`, `cmd/models.go`.
- `cmd/main.go`: drop the `internal/chat` import, the root command's `RunE`, `withStdStreams`
  with its comment, the `Long` text naming the chat session, the comments naming the chat entry
  points (`grep -n chat cmd/main.go`), and the `ping`/`models` `AddCommand` calls.
- `memops.LocalProviderName`: delete if `git grep -l -w LocalProviderName -- '*.go'` lists only
  `internal/memops/memops_model.go`.
- Comments naming `internal/chat`: in `internal/memops/memops.go` (line 7) and
  `internal/memops/fileadapter/fileadapter.go` (line 16), drop `internal/chat` from the listed
  packages.
- justfile:
  - Delete `FE_SCOPE_PREFIXES`, `FE_TESTPKGS`, `fe-scope-check`, `test-fe`, `test-be` and their
    comment blocks.
  - Drop `./internal/chat/...`, `./internal/shell/...` and `./internal/term/...` from
    `RACEPKGS`, with their rationale rows.
  - `GOSRCDIRS` comment: "Used by fmt/fmt-check and by the fe-scope-check untracked-file
    enumeration." → "Used by fmt/fmt-check and by test-changed's untracked-file scan."
  - `GOTESTCOUNT` comment: "(`test`, `test-be`, `test-fe`, `test-changed`)" → "(`test`,
    `test-changed`)".
  - `GOTESTTIMEOUT` comment: "`test` (and test-be/test-nocache)" → "`test` (and test-nocache)".
  - `build` comment: delete "(internal/term was written and "gated" that way once)".
  - Add `tidy-dependencies:` running `go mod tidy` only, with a one-line `#` description in the
    style of `add-dependency`. Run `just tidy-dependencies`.
- Contract rows marked 1 (README rows marked 1 included).
- Done-line: `just build`; `just test-run ./cmd`;
  `git ls-files internal/chat internal/term internal/shell internal/ping internal/modellist` →
  empty; `grep -n 'golang.org/x/sys' go.mod` → one `// indirect` line;
  `git grep -n -E 'internal/(chat|term|shell|ping|modellist)|test-fe|fe-scope|test-be' -- justfile '*.go' PROJECT_DOCS README.md ':!internal/tools' ':!internal/turn/commands_vocab_test.go' ':!internal/memops/memops_model.go'`
  → empty. The three excluded paths still name `internal/chat` until WP3, WP8 and WP5 delete them.
  Checkpoint.

