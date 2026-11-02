### P1 — `dedup` primitives (expert)

**Scope.** `internal/dedup/` only: new file(s) of the coder's naming, plus `internal/dedup/diff.go`.
Constants move: `DiffLiteralThreshold` out of `internal/dedup/chain.go` and `LiveDiffWindow` out of
`internal/dedup/livewindow.go` into `internal/dedup/diff.go`, which survives P6. No other package
changes. `chain.go` and `livewindow.go` stay otherwise untouched until P6.

**Design to read:** [design §2](../../design/2-design-in-one-paragraph.md) (terms N, `<id12>`, share);
[design §3](../../design/3-invariants.md) invariants 1, 4, 6, 8, 11;
[design §4](../../design/4-module-skeleton.md) (`internal/dedup` row);
[design §5.1](../../design/5-store-format-and-retention/5.1-layout.md);
[design §5.3](../../design/5-store-format-and-retention/5.3-retention.md);
[design §6](../../design/6-composition.md) step 2.

**Facts.**
- Incumbent content-id primitive: `git grep -n -e ComputeHash -e "sha1\." -- '*.go'` →
  `internal/autogit/autogit.go:603: blobHash := plumbing.ComputeHash(plumbing.BlobObject, data)`
  and nothing else. go-git is already a dependency: `grep -n go-git/go-git go.mod` →
  `github.com/go-git/go-git/v5 v5.19.3`. `ContentID` wraps the same call; it is not reimplemented.
- Diff machinery to reuse: `grep -n "^const lcsLineProductMax\|^func countLines\|^func makeDiff\|^func applyDiff" internal/dedup/diff.go`
  → `68:const lcsLineProductMax = 8_000_000`, `75:func countLines`,
  `179:func makeDiff(oldContent, newContent string) string`, `283:func applyDiff`.
- Today's keep/skip rule lives in `encodeDemoted` (`internal/dedup/chain.go`). It skips when
  `countLines(oldContent)*countLines(newContent) > lcsLineProductMax`, and keeps the literal when
  `len(delta) >= DiffLiteralThreshold*len(oldContent)`. The new write-time rule (design §9, the
  SPEC §2.6.1 entry) measures the diff against the **new** content's size: "a change's diff at or
  above this fraction of the new content's size is not kept".
- `dedup` is a leaf today. `go list -deps ./internal/dedup | grep personant/` lists only
  `personant/internal/dedup`. It stays a leaf apart from go-git `plumbing`.
- Constant users, all unaffected by a move inside the package:
  `git grep -l 'DiffLiteralThreshold' -- '*.go'` → `internal/dedup/chain.go`,
  `internal/dedup/chain_test.go`, `internal/dedup/livewindow.go`, `internal/store/threadfiles.go`.
  `git grep -l 'LiveDiffWindow' -- '*.go'` → `internal/dedup/livewindow.go`,
  `internal/dedup/livewindow_test.go`, `internal/memops/fileadapter/workset_compose.go`.

**Work.**
1. `ContentID([]byte) string`: the 40-hex git blob id.
2. The identifier-line renderer: one line per event, in the form of the excerpt example in
   [design §10](../../design/10-architecture-changes-same-change.md):
   `[file read #<id12>] <path>`, `[file write #<id12>] <path>`,
   `[file commit @<first 7 of hash>] <path>`. The path is the remainder of the line. Punctuation
   beyond that is the coder's choice ([design §14](../../design/14-deliberately-unspecified-coder-s-choice.md)).
3. `Diff(old, new string) (diff string, kept bool)`: a forward unified diff via `makeDiff`.
   `kept=false` when the line-product guard trips, or when `len(diff) >= DiffLiteralThreshold ×
   len(new)`.
4. `LiteralShareThreshold = 0.25`, named after the directive key, as the existing constants are.
5. `Plan(threads, literalMax) → expansions per (thread, turn)`, exactly as
   [design §6](../../design/6-composition.md) step 2 states it:
   - the most recent position per (project, path);
   - the size gate at `literalMax`;
   - one literal per path and per id, with the shared-id winner rule (the losing path keeps its
     diffs);
   - diffs: per (project, path), changes from all threads ordered by `at`. A change qualifies when
     it has `diff:true` and a diff `size` below `DiffLiteralThreshold` × the path's current `size`;
     the N most recent that qualify are planned.

   Each planned expansion is one record with these fields, which P4 consumes for the walk and
   the expansion report:
   - thread id, turn (the excerpt number), project, path, kind (`literal` or `diff`);
   - `id` for a literal, or `from` and `to` for a diff;
   - the expansion's own byte size: the blob's `size` for a literal, the change's `size` for a
     diff;
   - the path's current `size`.

   The input entry type carries the index fields of
   [design §5.1](../../design/5-store-format-and-retention/5.1-layout.md), both `size` fields
   included. It has no JSON tags; the store maps its record to it (P2).
6. `Retain(entries, turnCount, window) → keep set` per
   [design §5.3](../../design/5-store-format-and-retention/5.3-retention.md): entries with
   `turn > turnCount − window`, their blob ids, and their `diff:true` change files.

**Tests.** `ContentID` against independently known git ids: the empty blob is
`e69de29bb2d1d6434b8b29ae775ad8c2e48c5391`, and `"hello\n"` is
`ce013625030ba8dba906f756967f9e9ca394464a` (`git hash-object`). Identifier line: each of the three
forms; the id truncated to 12 hex (commit hash to 7); a path containing spaces and `]` comes back
whole as the remainder of the line. `Diff` round-trips through `applyDiff`, and returns
`kept=false` at the guard and at the threshold boundary. `Plan`:
- latest position wins across threads;
- two paths sharing an id: the later `at` gets the literal, at equal `at` the tie-break order
  decides, and the losing path still gets its diffs;
- size exactly at `literalMax` gets nothing, size `literalMax − 1` gets the literal;
- the N most recent qualifying diffs by `at`, drawn from two threads;
- after the file shrank, a `diff:true` change whose size is at or above `DiffLiteralThreshold` ×
  the current size is not planned.

`Retain`: the window boundary, inclusive and exclusive.

**Done-line.** `just build`;
`just test-run ./internal/dedup 'TestContentID|TestIdentifierLine|TestDiff|TestPlan|TestRetain'`.

---
