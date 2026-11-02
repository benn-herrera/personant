### P1 — `dedup` primitives (expert)

**Scope.** `internal/dedup/` only: new file(s) of the coder's naming, plus `diff.go`. Constants
move: `DiffLiteralThreshold` out of `chain.go` and `LiveDiffWindow` out of `livewindow.go` into
`diff.go`, which survives P6. No other package changes. `chain.go` and `livewindow.go` stay
otherwise untouched until P6.

**Design to read:** [§2](../2-sequence-and-status.md), [§3](index.md) invariants 1, 4, 6, 8, 11, [§4](../4-acceptance-criteria.md) (`internal/dedup` row), §5.3, [§6](../6-post-sprint-review.md) step 2.

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
  `len(delta) >= DiffLiteralThreshold*len(oldContent)`. The new rule (design §9, 2.6.1 line)
  measures the diff against the **new** content's size: "a change's diff at or above this
  fraction of the new content's size is not kept".
- `dedup` is a leaf today. `go list -deps ./internal/dedup | grep personant/` lists only
  `personant/internal/dedup`. It stays a leaf apart from go-git `plumbing`.
- Constant users, all unaffected by a move inside the package:
  `git grep -l 'DiffLiteralThreshold' -- '*.go'` → `internal/dedup/chain.go`,
  `internal/dedup/chain_test.go`, `internal/dedup/livewindow.go`, `internal/store/threadfiles.go`.
  `git grep -l 'LiveDiffWindow' -- '*.go'` → `internal/dedup/livewindow.go`,
  `internal/dedup/livewindow_test.go`, `internal/memops/fileadapter/workset_compose.go`.

**Work.**
1. `ContentID([]byte) string`: the 40-hex git blob id.
2. The identifier-line renderer: one line per event, in the form given in the design's §10
   ARCHITECTURE example. `[file read #<first 12 hex of id>] <path>`,
   `[file write #<id12>] <path>`, `[file commit @<first 7 of hash>] <path>`. The path is the
   remainder of the line. Punctuation beyond that is the coder's choice (design §14).
3. `Diff(old, new string) (diff string, kept bool)`: a forward unified diff via `makeDiff`.
   `kept=false` when the line-product guard trips, or when `len(diff) >= DiffLiteralThreshold ×
   len(new)`.
4. `LiteralShareThreshold = 0.25`, named after the directive key, as the existing constants are.
5. `Plan(threads, literalMax) → expansions per (thread, turn)`, implementing design [§6](../6-post-sprint-review.md) step 2 and
   invariant 11. The literal-size limit is a byte count the caller computes. A path whose most
   recent entry has `size >= literalMax` gets no literal and no diffs. Each (project, path) gets
   at most one literal, at its entry with the latest `at`; ties go to Layer B order. Each id gets
   at most one literal. When two paths' literal candidates share an id, the id goes to the
   candidate with the later `at`; on equal `at`, to the earlier thread in Layer B order; on the
   same thread, to the path that sorts first. The other path gets no literal. Diffs: per
   (project, path), the N most recent `diff:true` changes across all threads, ordered by `at`.
   The input entry type carries the index fields of design §5.1, `size` included. It has no JSON
   tags; the store maps its record to it (P2).
6. `Retain(entries, turnCount, window) → keep set` per design §5.3: entries with
   `turn > turnCount − window`, their blob ids, and their `diff:true` change files.

**Tests.** `ContentID` against independently known git ids: the empty blob is
`e69de29bb2d1d6434b8b29ae775ad8c2e48c5391`, and `"hello\n"` is
`ce013625030ba8dba906f756967f9e9ca394464a` (`git hash-object`). Identifier line: each of the three
forms; the id truncated to 12 hex (commit hash to 7); a path containing spaces and `]` comes back
whole as the remainder of the line. `Diff` round-trips through `applyDiff`, and returns
`kept=false` at the guard and at the threshold boundary. `Plan`:
- latest position wins across threads;
- two paths sharing an id: the later `at` gets the literal; at equal `at`, the tie-break order
  above decides;
- size exactly at `literalMax` gets nothing, size `literalMax − 1` gets the literal;
- the N most recent diffs by `at`, drawn from two threads.

`Retain`: the window boundary, inclusive and exclusive.

**Done-line.** `just build`;
`just test-run ./internal/dedup 'TestContentID|TestIdentifierLine|TestDiff|TestPlan|TestRetain'`.

---

