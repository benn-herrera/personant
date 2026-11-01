### WP3 — `personant verify` folio checks (mechanical)

Verify decodes every folio on every call (SPEC §4.1, `PROJECT_DOCS/SPEC/4-user-surface/4.1-cli-command-surface.md`
line 8: "`personant verify` — full structural validation"). There is no opt-in flag.

Scope: `internal/verify/verify.go`. Next to `checkThreads` (line 208), add a folio check over every
spine thread whose `folio.jsonl` exists, using only `store.ReadFolio` and
`store.ListExcerptEntries` from WP1.
- **Errors**, each with `Path` `threads/<id>/folio.jsonl` (home-relative, the form `findingRel`
  maps to a quarantine path): `store.ReadFolio` returns an error (a malformed line; the message
  carries its line number), in which case skip the remaining checks for that thread; names not
  strictly ascending by (number, name) in `ReadFolio`'s file order; a repeated name. Through B2's
  gate these fail a live barrier and are quarantined by the next open's completion (invariant 7).
- **Warnings:** a name in both tiers (`ListExcerptEntries` returns it twice). Read the loose bytes
  (`filepath.Join(store.ThreadTurnsDir(paths, id), name)`) only for those names and compare with
  the folio line's content: identical → "settle pending"; different → "conflict; the loose copy is
  read".

Update the comments at lines 76-78 and 205-207 that say verify does not read excerpts: it reads
folios and, for names present in both tiers only, those loose excerpts.

Tests in `internal/verify/verify_test.go`, every new test named `TestFolio…`, build folios from
hand-written fixture lines (not through the store's writer), so the verify tests pin the format
independently: one per error kind, one per warning kind, and a clean folio that yields no finding.

Facts and references: `dayCommit` gates on `verify.Verify` errors (`fileadapter_barrier.go`
:384-392: "rep, verr := verify.Verify(...) … if len(rep.Errors) > 0 { if !recovering { return
fmt.Errorf(...) }"), so folio errors fail the live barrier and reach quarantine on completion.
`findingRel` turns finding paths into scoped-restore paths (`fileadapter_barrier.go:534`). Every
caller therefore pays the folio decode (`git grep -n 'VerifyOptions{' -- '*.go' ':!*_test.go'`):
B2's gate and its re-verifies (`fileadapter_barrier.go:367, 384, 465`), the archival capture's
`CheckSpineIntegrity` pre-flag (`autogit.go:777`, run from `fileadapter_archive.go:393` during
B1), recovery's stamp/adopt verify (`internal/recovery/stamp.go:278`), the port's `Verify`
(`fileadapter.go:970`, `personant verify`), and the harness invariant `VerifySpineIntegrity`
(`internal/scenarios/invariants.go:91`, per step on handwritten scenarios, day-cadenced in the
sim). The cost is linear in bound bytes per call; WP5 measures it through `barrier_duration_ms`.

Done-line: `just build`; `just test-run ./internal/verify 'TestFolio'`; checkpoint `just test`, then
commit.

