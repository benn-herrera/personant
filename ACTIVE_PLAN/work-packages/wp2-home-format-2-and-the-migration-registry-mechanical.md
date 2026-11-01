### WP2 — Home format 2 and the migration registry (mechanical)

Migrations go through a registry: an ordered list of steps keyed by from-format, each naming the
format it produces. 1→2 is its only step and changes no bytes, because format 2 is format 1 plus
optional `threads/*/folio.jsonl`, so the step is a restamp. The registry holds no apply hook: the
first step that rewrites bytes adds one. `GateHomeFormat`'s signature does not change.
`personant init` does not migrate.

Scope:
- `internal/version/version.go`: `CurrentHomeFormat = 2` (line 39); its doc comment says format 2
  adds optional per-thread `folio.jsonl` files to format 1. `UnversionedHomeFormat` stays 1
  (line 52).
- `internal/memops/homeformat.go`:
  - Add the registry: exported `type HomeFormatMigration struct{ From, To int }` and an
    unexported package-level `homeFormatMigrations = []HomeFormatMigration{{From: 1, To: 2}}`,
    ascending by `From`. Its doc says a step lists the from-format it reads and the format it
    leaves, and that 1→2 is a restamp only. No folio or tier term (the module-placement row for
    `internal/memops`).
  - `HomeFormatDecision` (lines 41-57): add `MigratedFrom int`, documented as the on-disk format
    the home was migrated from, 0 when no migration ran. Keep the struct comparable (no slice
    field): `homeformat_test.go` compares decisions with `!=` (line 110).
  - `GateHomeFormat`'s older arm (the `default:` at lines 105-108): walk the registry from
    `onDisk`, taking the step whose `From` equals the current position until the position reaches
    `current`. When it does, return `d` with `Format = current`, `StampFormat = current`,
    `MigratedFrom = onDisk`, nil error. When a position has no step, or a step overshoots
    `current`, return `ErrHomeFormatNoMigration`, the message naming the on-disk format, `current`
    and the format with no step. An unstamped home resolves to `version.UnversionedHomeFormat`
    (lines 85-88) and then takes this arm, so its stamp becomes `current`. Under `allowNewer` the
    older arm behaves the same: the override concerns newer homes only.
  - Doc comments: `ErrHomeFormatNoMigration` (lines 30-31, "Deliberately unreachable today") → an
    older home with no registered chain of steps to the current format. The "No stamp" bullet
    (lines 62-69) → an unstamped home is taken to be `version.UnversionedHomeFormat` and falls
    through the same arms, so when current advances it takes the registered migration instead of
    being declared current; that is why `UnversionedHomeFormat` is pinned. The "Older on disk"
    bullet (lines 73-77) → migrate forward through the registry, refuse when the chain breaks.
- `internal/memops/memops.go` `StampHomeFormat` doc (lines 633-637): its two callers are the
  adopt-forward arm and the migration arm of `GateHomeFormat`, both acted on by `openHome`.
  Comment only; the interface does not change.
- `internal/memops/homeformat_test.go` `TestGateHomeFormat`:
  - The two older-home cases (lines 56-72; `onDisk: 1, current: 2`, with and without
    `allowNewer`) become "older home migrates forward" and "older home migrates forward under
    allow-newer", each `want: HomeFormatDecision{Format: 2, StampFormat: 2, MigratedFrom: 1}` with
    no `wantErr`; drop the "Unreachable in production" comment.
  - Add "older home with no registered step is refused" (`onDisk: 2, current: 3` →
    `ErrHomeFormatNoMigration`), "chain breaks partway" (`onDisk: 1, current: 3` →
    `ErrHomeFormatNoMigration`), and "unstamped home migrates forward" (`found: false, current: 2`
    → `{Format: 2, StampFormat: 2, MigratedFrom: 1}`).
  - New `TestHomeFormatMigrationChainReachesCurrent`: `homeFormatMigrations` is strictly ascending
    by `From`, every step has `To > From`, and walking it from `version.UnversionedHomeFormat`
    reaches exactly `version.CurrentHomeFormat`. A format bump that registers no step fails here.
- `cmd/main.go` `openHome`: after `LogBootstrap` (line 147), when `decision.MigratedFrom != 0`, log
  `system.home-format-migrated from=<decision.MigratedFrom> to=<decision.Format>` with
  `eventlog.Log(paths, memops.LogCategorySystem, "home-format-migrated", …)`, the pattern at
  line 167. The stamp write already runs from `decision.StampFormat` (lines 136-140).
  `system.bootstrap` then records `home-format=2`.
- `cmd/init.go`: no change; `init` does not migrate. On a format-1 home the read-only gate
  (line 32) returns a migrate decision, `init` ignores `StampFormat` as it does today
  (`cmd/init.go:28-31`), and `store.Init` leaves the existing `version.toml` alone
  (`internal/store/init.go:132`, `writeIfMissing`). The next command that opens the home through
  `openHome` restamps it and logs `system.home-format-migrated`.
- `cmd/version_test.go`:
  - Line 128 (`TestAllowNewerHomeOverrideLeavesATrace`) hard-codes `binary=1`; build the expected
    string from `version.CurrentHomeFormat` (`fmt.Sprintf("system.home-format-override on-disk=99
    binary=%d", version.CurrentHomeFormat)`).
  - `TestUnversionedHomeIsAdoptedForward` (lines 138-155): the stamp after the open equals
    `version.CurrentHomeFormat` (not `UnversionedHomeFormat`), and `readHomeLogs(t, home)` contains
    `system.home-format-migrated from=1`. Update its doc comment (lines 138-139) to say an
    unversioned home is taken as format 1 and migrated forward.
- SPEC §9.1, `PROJECT_DOCS/SPEC/9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/version-lines.md`,
  lines 27-29. Replace these two bullets:

  ```
  - **Older on disk → refuse**, pending a registered migration.
  - **No stamp → adopt forward.** A home predating versioning is taken to be format 1 and stamped as
    such — always 1, even after the current format advances, so it takes its migration.
  ```

  with exactly:

  ```
  - **Older on disk → migrate forward** when this binary registers a chain of migration steps from
    that format to the current one: the home is brought to the current format and restamped, and
    the open emits a `system.home-format-migrated` event ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)) naming both formats. An older
    format with no registered chain is refused. `personant init` gates but never stamps, so it
    leaves an older home's stamp for the next open to migrate.
  - **No stamp → adopt forward.** A home predating versioning is taken to be format 1 — always 1,
    even after the current format advances — and then takes the older-on-disk path, so it is
    migrated and stamped at the current format.
  ```
- ARCHITECTURE "Event log format",
  `PROJECT_DOCS/ARCHITECTURE/testing-as-the-lab-bench/event-log-format.md` line 29 (the `system`
  row): after the `home-format-override` entry, add `` `home-format-migrated` (an older home was
  migrated forward at open by the §9.1 gate — `from=`, `to=`) ``.
- ARCHITECTURE "Crash stability and the dual-repo substrate",
  `PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md`, the
  "Mechanism names." paragraph (line 28): one sentence — `GateHomeFormat` migrates an older home
  by walking the registry `homeFormatMigrations` (steps keyed by from-format; 1→2 restamps only,
  since format 2 adds optional folios to format 1), the caller writes the stamp before Reconcile,
  and a crash between stamp and log leaves a current-format home that needs nothing further.

Facts and references:
- `git grep -l 'HomeFormatDecision' -- '*.go'`: `cmd/main.go`, `internal/eventlog/bootstrap.go`
  (doc comment only, line 21), `internal/memops/homeformat.go`,
  `internal/memops/homeformat_test.go`. The new field is additive; no file builds the struct
  positionally.
- `git grep -n 'GateHomeFormat(' -- '*.go'`: `cmd/main.go:99`, the definition
  (`homeformat.go:79`), `homeformat_test.go:98, 122`. The signature does not change.
- `git grep -n 'StampHomeFormat' -- '*.go' ':!*_test.go'`: the call at `cmd/main.go:137`, the
  implementation at `internal/memops/fileadapter/fileadapter_homeformat.go:36`, the interface doc
  at `internal/memops/memops.go:633-643`, and comments in `homeformat.go`. No signature changes.
- `git grep -n 'CurrentHomeFormat' -- '*_test.go'`: `internal/chat/commands_test.go:470`,
  `internal/eventlog/bootstrap_test.go:72, 106`,
  `internal/memops/fileadapter/fileadapter_homeformat_test.go:19-20, 77, 85`,
  `internal/store/init_test.go:491-492`, `internal/version/version_test.go:10-22`. These compare
  against the constant and need no edit.
- `git grep -n 'home-format=1\|format = 1\|format != 1\|binary=1\|UnversionedHomeFormat' --
  '*_test.go'`: `cmd/version_test.go:128` (literal `binary=1`) and `:139-154`
  (`TestUnversionedHomeIsAdoptedForward` expects `UnversionedHomeFormat`), plus
  `homeformat_test.go:25, 32` (unstamped cases at `current: 1`, still correct) and
  `internal/version/version_test.go` (pins `UnversionedHomeFormat == 1`, still correct). The two
  `cmd/version_test.go` sites are the bump's test edits.

Done-line: `just build`; `just test-run ./internal/memops 'HomeFormat'`;
`just test-run ./internal/version 'HomeFormat'`;
`just test-run ./cmd 'Version|HomeFormat|NewerHome|Unversioned'`; checkpoint `just test`, then
commit.

