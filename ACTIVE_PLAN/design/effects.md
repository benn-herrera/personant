### Effects

- **Daily repo N.** The daily repo is reborn at B5 from the live tree. That tree now holds one
  folio per thread with bound excerpts, plus at most H loose excerpts per warm thread. Projection
  from the run homes' final state: the 9-day home's 6,772 excerpt files become about 1,418
  (1,170 loose + 248 folios; 247 of 256 threads retired). The 30-day home's 13,902 become about
  1,261. The method: the placement rule applied to `spine.jsonl` states and per-thread `turns/`
  counts. Within a day, N then grows only by that day's appends, and the next barrier resets it.
  WP5 measures the real figure.
- **`turnFileNumbers`.** It is replaced by a listing of (number, name, tier) entries. Its
  `os.ReadDir` covers at most H plus one day's appends for a warm thread and nothing for a
  folio-only thread, instead of the thread's whole history.
- **Recoverable archival.** Folios align with archival and do not reuse it. Archival removes whole
  retired-thread directories from the worktree, keeping their bytes only in primary history.
  Folios keep every byte in the worktree ("the worktree is the truth"), which recall's exhaustive
  `GrepThreads` and the fine-tier index depend on. A retired thread is normally bound before it is
  archived, so archival captures one folio instead of many loose files. Capture, tree hash and
  recovery work on the directory as they do today. A recovered thread's state is `wip`, so the next
  barrier unbinds its hot tail.
- **Crash matrix.** Five new crashpoints, listed above. Turn cells 4/5/6 are untouched because
  the turn path is unchanged. The hand-edit cell 3 and the quarantine path (invariant 7) cover a
  hand-edited folio.
- **Home format.** A folio is a layout change. A format-1 binary would read a format-2 home and
  silently miss its bound excerpts, so `version.CurrentHomeFormat` goes to 2. Migrations go
  through a registry of ordered steps keyed by from-format (SPEC §9.1 "Older on disk → refuse,
  pending a registered migration"); 1→2 is its first step. A format-1 home is already a valid
  format-2 layout (all loose, no folios), so that step only restamps; the first barrier then
  binds. An older binary refuses a format-2 home through the existing "newer on disk" arm.
  `personant init` does not migrate.
- **`personant verify` / index.** Verify decodes every folio on every call (SPEC §4.1 "full
  structural validation"; WP3). Index rebuild and check do not change.
- **Metrics.** WP0 adds per-day `turn_commit_ms` means and the daily index entry count at each
  seal to `daily.jsonl`, so before and after can be read off two run files. The
  `barrier.folio-settled` event carries per-barrier bind and unbind counts.
- **Determinism.** Invariant 5 covers it. `TestSimBarrier2Day` and the default `1d` rung under
  `just test` exercise B1b.

