### E3. What persistent storage the behavior needs

- **Composition renders only these:**
  - for each file, the current content, at the file's most recent position;
  - the N most recent changes, as diffs (N is `dedup.live-diff-window`, default 3);
  - identifier lines.
- **Identifier lines carry their own id.** They are written into excerpts at turn close and need no
  stored content to render.
- **So storage is the current content plus the last N diffs per file.** No earlier version is ever
  rendered from storage.
- **THESIS settles not keeping more** ("Behavior vs history"): "the agent's precious memory space is
  not for filling with every revision of every repo ever worked in … the details are retrievable
  from version control. That is what it is for."
- **SPEC §1.4 states:** "File content evicted from a request to meet a budget (and any content
  whose slot's overflow form is an entry, §3.1) is named in that request, at the version it had,
  and can be brought back on demand"
  ([SPEC §1.4](../../../PROJECT_DOCS/SPEC/1-operating-model/1.4-evicted-content.md)). How that
  applies under this design is stated in the proposed SPEC §3.9.1 and §3.9.2 text
  ([§9](../9-proposed-spec-text-replaces-the-leaves-under-project-docs-spec-3-core-algorithms-3-9-working-set-content-dedup.md)).
  In short:
  - a superseded version is not content evicted for budget;
  - an under-threshold file's current content is kept, and is what an evicted position brings
    back;
  - bringing back an over-threshold file on demand is open (ROADMAP B6a).
- **Supporting facts:**
  - `GetFileVersion` has no production caller. `git grep -n "GetFileVersion(" -- '*.go'
    ':!*_test.go'` returns only its declaration (`internal/memops/memops.go:482`) and its
    implementation (`internal/memops/fileadapter/fileadapter.go:918`).
  - Barrier step B2 stages with `autogit.Add(".")`
    (`internal/memops/fileadapter/fileadapter_barrier.go:422`), so deletions made before B2 are
    captured in primary.
  - `autogit.AddPaths` sends a recorded path that is missing on disk through "go-git's
    status-assisted delete-from-index path (rare …)" (`internal/autogit/autogit.go:231–241`). A
    per-turn delete would therefore put a whole-tree status walk on the turn path. This is why
    [§8](../8-barrier-step-b1c-and-crash-safety/index.md) puts deletes at the barrier.
  - Incumbent content-id primitive: `plumbing.ComputeHash(plumbing.BlobObject, data)` at
    `internal/autogit/autogit.go:603` (go-git, already a dependency). No other git-blob-id helper
    exists: `git grep -n -e ComputeHash -e "sha1\." -- '*.go'` returns only that line.
  - `hashDirTree` already recurses into subdirectories (`internal/autogit/autogit.go:592–597`), so
    archival tree hashes cover a thread subdirectory.
