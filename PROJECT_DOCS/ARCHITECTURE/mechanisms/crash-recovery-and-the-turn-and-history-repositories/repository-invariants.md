#### Repository invariants

- **No-reset rule:** archival and the day rollover never reset the store working tree (their
  recovery is re-drive or repair); only turn recovery resets, and only ever to the turn
  repository's HEAD (≤1 turn).
- **Day-commit guard:** the day-commit is guarded by history repository HEAD being a
  day-commit-shape commit (one the day-commit step made, as opposed to a back-fill or adopt commit
  that carries the same trailer) with `Personant-Day == N`, not merely a trailered commit, so a
  re-drive after a crash never mints a second day-commit.
- **Capture-before-removal rule:** `.git-daily` is deleted only after the store working tree is
  captured to the history repository.
- **Watermark rule:** the watermark tracks the turn repository's HEAD, re-set by derived rebuild and
  by turn-repository init (the re-init at day rollover, or the init of a missing repository).
- **History-writer rule:** the history repository is written by the day rollover (its archival and
  day-commit steps, and the lifecycle tags it derives (see "Lifecycle git tags")) and by three recovery paths — case 9
  back-fill repair, case 12 adopt and archival roll-forward completion — and by nothing else; the
  checkpoint and the archive restore target the turn repository. Every such writer, day-commit and
  archival alike, validates thread registry integrity before minting, so history repository HEAD
  always passes it.
- **Ignore-block rule:** both `.git/` and `.git-daily/` are in the managed `.gitignore` block, so neither repository
  sees the other's git directory as untracked debris.

