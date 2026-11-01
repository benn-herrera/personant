### WP1 — Store: folio format, tier-transparent reads, settle (expert)

Scope:
- `internal/memops/memops_barrier.go` (or a new `memops_recall.go`): `const EmbeddingDebtCap = 16`,
  with its doc moved from `internal/turn/lru.go:9-25`. `internal/turn/lru.go:26` becomes
  `const EmbeddingDebtCap = memops.EmbeddingDebtCap` (`lru.go` already imports `memops`, line 6).
- `internal/store/relpaths.go`: a `threadFolioFileName = "folio.jsonl"` constant beside
  `threadTurnsDirName` (line 15) and `ThreadFolioRel(threadID)`.
- `internal/store/jsonl.go:33`: turn the 1 MiB literal into a named constant, which the too-large
  guard uses.
- New `internal/store/folio.go`:
  - `ThreadFolioPath`, `HotExcerptTail`;
  - exported line type `FolioLine{Name string; Content *string}` and its sort key ("Folio format"),
    written with `WriteJSONL`;
  - exported `ReadFolio(path string) ([]FolioLine, error)`: lines in file order, no re-sort; it
    returns an error naming the 1-based line on the first malformed line ("Folio format"), so a
    nil error guarantees every `Content` is non-nil and every `Name` is a valid excerpt name;
  - exported `ListExcerptEntries(paths PersonantPaths, threadID string) ([]ExcerptEntry, error)`
    with `ExcerptEntry{Number int; Name string; Tier ExcerptTier}` and two `ExcerptTier` values
    (loose, folio). It returns one entry per (name, tier), so a name in both tiers appears twice;
    sorted by (number, name), folio before loose for the same name; an error when the folio is
    malformed. It replaces `turnFileNumbers`. Readers may list the loose tier alone first, per the
    folio-access rule;
  - unexported `bindExcerpts` / `unbindExcerpts`, selecting by turn numbers (full = every number;
    partial = a set or an expanded range);
  - the unexported pure placement function;
  - `SettleExcerptTiers(paths PersonantPaths, threadID string, retired bool) (SettleOutcome,
    error)`, where `SettleOutcome` reports bound and unbound counts plus skipped
    `(name, reason)` pairs (reasons `too-large`, `conflict`, `unreadable`). An excerpt to be bound
    whose bytes fail `utf8.Valid` makes it return an error wrapping a new sentinel
    `store.ErrExcerptNotUTF8` and naming the home-relative path, checked for the whole thread
    before its first write (Design "Settle step (barrier B1b) and crash safety");
  - the four `folio.settle.*` crashpoints, registered with `crashpoint.Register` the way
    `internal/store/atomic.go:23-26` does it, firing per the crashpoint table in "Settle step
    (barrier B1b) and crash safety" (only when the step has work).
- `internal/store/thread_io.go`:
  - `ReadThreadBody`, `ReadThreadExcerpts` and `ReadDebtWindowExcerpts` become tier-transparent,
    with the folio-access rule from the per-reader table, read using actual entry names, and with
    the loose copy winning for a name in both tiers. Signatures and return semantics do not change.
  - Delete `turnFileNumbers`.
  - `AppendThreadTurn` gains no UTF-8 check: excerpt content arrives through JSON decoding, and
    bind asserts (invariant 2).
  - Correct the doc comments that describe FIFO eviction or "every retained turn-excerpt file".
- `internal/memops/memops.go`: correct the `LoadThread`, `LoadThreadExcerpts` and
  `LoadDebtWindowExcerpts` doc comments that claim FIFO eviction. Comments only; the interface does
  not change.
- `internal/store/thread_io_test.go`: lines 397-399 (`TestAppendThreadTurnRetainsPastWindow`) and
  509-511 (`TestSeedThreadSplitsBodyIntoTurns`) call `turnFileNumbers`; switch them to
  `ListExcerptEntries`.
- `internal/scenarios/crash_matrix_test.go` `crashScenarioCoverage` (line 46): add the four
  `folio.settle.*` names, each mapped to `"store.TestFolioSettleCrashConverges"`.
  `TestCrashPointCoverageGate` fails on any registered crashpoint missing from this map, so WP1's
  registrations need these entries in the same package.
- New `internal/store/folio_test.go`. Every new test is named `TestFolio…`:
  - `TestFolioRoundTrip`: a seeded generator plus fixed seeds. Content covers JSON-like text, `---`,
    `## Turn` headings, CRLF, U+2028/U+2029, NUL, `<>&`, empty files, no trailing newline,
    multi-megabyte content and non-canonical names (`2.md`). After bind then unbind, the names and
    bytes are identical. After bind alone, the three readers return what they returned before.
  - `TestFolioSettleFailsOnInvalidUTF8`: a retired thread with one loose excerpt holding invalid
    UTF-8 → `SettleExcerptTiers` returns an error matching `ErrExcerptNotUTF8` and naming that
    file; the thread's directory is byte-identical before and after (no folio, every loose file
    still present).
  - `TestFolioReadFolio`: file order is preserved; each malformed-line kind errors with its line.
  - `TestFolioSettleCrashConverges`: for each `folio.settle.*` crashpoint, arm it, crash, re-settle,
    and assert invariant 3 plus the placement result. Bind case: `retired=true`, ≥ 3 loose
    excerpts. Unbind case: a thread whose ≥ 3 excerpts are all bound, settled with
    `retired=false`.
  - `TestFolioPlacement`: a retired thread binds everything; a warm thread keeps the newest H.

Facts and references:
- Reader inventory: "Read path, per reader" above, with three `git grep` commands and their
  answers.
- `git grep -n 'turnFileNumbers\|turnFileName(' -- '*.go'`: `internal/store/relpaths.go:30`,
  `internal/store/thread_io.go` (lines 61, 62, 190, 216, 253, 265, 305, 329, 436, 564, 571, 575)
  and `internal/store/thread_io_test.go` (397, 399, 509, 511). No other file.
- `git grep -l 'EmbeddingDebtCap' -- '*.go'`: 14 files, in `internal/turn`,
  `internal/recall/measure` (one test), `internal/scenarios` and `internal/scenarios/sim`. The
  alias keeps all of them compiling unchanged. Only `internal/turn/lru.go` is edited.
- `git grep -l '"personant/internal/' -- ':(glob)internal/memops/*.go' ':!*_test.go'`:
  `internal/memops/homeformat.go` only, importing `personant/internal/version`. The non-test files
  of `memops` import only `version`, so `memops` stays a leaf, and `store` already imports
  `memops`.
- `ReadThreadBody` today, `thread_io.go:205-208`: "windowFloor := 0 / if len(nums) >
  ThreadTurnWindow { windowFloor = len(nums) - ThreadTurnWindow }". The newest-first walk of at
  most 512 entries keeps this meaning across tiers.

Done-line: `just build`;
`just test-run ./internal/store 'TestFolio|ThreadBody|ThreadExcerpts|DebtWindow|AppendThreadTurn|SeedThreadSplits'`;
`just test-run ./internal/turn 'Flush|Debt'`;
`just test-run ./internal/scenarios 'TestCrashPointCoverageGate'`; checkpoint `just test`, then
commit.

