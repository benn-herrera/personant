### Migration (format 2 → 3)

- **Registry.** Add `{From: 2, To: 3}` to `homeFormatMigrations`. It is a restamp only, set with
  `version.CurrentHomeFormat = 3`.
- **Legacy sidecar.** Readers ignore `files.json`. The first B1c removes it.
- **Why no byte-rewrite runs before `Reconcile`.** A pre-`Reconcile` rewrite would be undone by a
  torn-turn reset to daily HEAD. B1c is crash-safe roll-forward, so removal belongs there.
- **What existing homes lose:** the in-context literal of a file until that file's next read or
  write. Its content stays in primary history and in the workspace (THESIS, "Behavior vs history").
- **Verify.** Verify never reads `files.json`, so a legacy sidecar on a format-3 home before its
  first barrier is not a finding.

---

