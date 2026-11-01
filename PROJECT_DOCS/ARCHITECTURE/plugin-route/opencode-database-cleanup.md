### opencode database cleanup

opencode keeps one global SQLite database. Rows before the latest checkpoint are never read again
by the runner but stay on disk (SS §4.4; SPEC §1.7). The cleanup is not built. Its rules:

1. **Only when nothing uses the database:** every opencode process, the background service
   included, has exited. Otherwise the run is skipped; skipping costs nothing.
2. **Under an exclusive SQLite lock** taken before any change.
3. **Schema-version gated:** it refuses to run on a migration set it was not built for; tables
   change between patch releases.
4. **Reference-safe:** it deletes only rows nothing still points to — fork origins, revert targets,
   blobs, `session_part`.
5. **Verified:** `PRAGMA integrity_check` and `PRAGMA foreign_key_check` pass, then `VACUUM`.
6. **Proven first on a copy:** opencode started on the cleaned copy loads sessions, renders
   scrollback, and forks and reverts from kept rows.

Whole sessions the memory system has finished with (merged sub-mind children) are removed through
the supported `session.remove` instead.

---

