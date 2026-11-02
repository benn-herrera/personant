### E3. What persistent storage the behavior needs

- **Composition renders only these:**
  - for each file, the current content, at the file's most recent position;
  - the N most recent changes, as diffs;
  - identifier lines.
- **Identifier lines carry their own id.** They are written into excerpts at turn close and need no
  stored content to render.
- **So storage is the current content plus the last N diffs per file.** No earlier version is ever
  rendered from storage.
- **THESIS settles not keeping more** ("Behavior vs history"): "the agent's precious memory space is
  not for filling with every revision of every repo ever worked in … the details are retrievable
  from version control. That is what it is for."
- **SPEC §1.4 does not require more.** Its promise covers content *evicted for budget*. That
  content's current version stays retained under this design ([§5.3](../5-store-format-and-retention/5.3-retention-b1c-8.md)).
- **Supporting facts:**
  - `GetFileVersion` has no production caller. `git grep -n "GetFileVersion(" -- '*.go'
    ':!*_test.go'` returns only its declaration (`memops.go:482`) and its implementation
    (`fileadapter.go:918`).
  - Barrier B2 stages with `autogit.Add(".")` (`fileadapter_barrier.go:422`), so deletions made
    before B2 are captured in primary.
  - `autogit.AddPaths` sends a recorded path that is missing on disk through "go-git's
    status-assisted delete-from-index path (rare …)" (`autogit.go:231–241`). A per-turn delete would
    therefore put a whole-tree status walk on the turn path. This is why [§8](../8-barrier-step-b1c-and-crash-safety/index.md) puts deletes at the
    barrier.
  - Incumbent content-id primitive: `plumbing.ComputeHash(plumbing.BlobObject, data)` at
    `autogit.go:603` (go-git, already a dependency). No other git-blob-id helper exists: `git grep
    -n -e ComputeHash -e "sha1\." -- '*.go'` returns only that line.
  - `hashDirTree` already recurses into subdirectories (`autogit.go:~585`), so archival tree hashes
    cover a thread subdirectory.

---

