## 10. ARCHITECTURE changes (same change)

- **`module-map.md`:** the `internal/dedup/` row becomes "content identifiers, write-time diffs,
  excerpt identifier lines and the composition plan; pure, dependency leaf".
- **`storage-and-record-formats/home-layout.md`:** replace the `files.json` line with `files.jsonl`
  ("tracked-file index (SPEC §3.9)") and `files/` ("current blobs and kept diffs").
- **`storage-and-record-formats/thread-directory-threads-thr-id.md`:**
  - Replace the `files.json` bullet with a "Tracked-file store" subsection containing [§5.1](5-store-format-and-retention/5.1-layout.md)–[§5.3](5-store-format-and-retention/5.3-retention-b1c-8.md) of
    this design: format, write order, retention rule.
  - Add the identifier lines to the excerpt example, between the header and `**user:**`:

    ```
    [file read #1a2b3c4d5e6f] src/a.go
    [file write #9f8e7d6c5b4a] src/a.go
    [file commit @abc1234] src/a.go
    ```

    Note that composition never parses them.
  - The "tracked-file count does not grow with thread length" claim stays true, with the bound
    stated.
- **`the-layered-working-set/index.md`:** add a "File content" bullet containing [§6](6-composition.md) of this design
  (plan, budgeted walk, expansion placement).
- **`mechanisms/crash-stability-and-the-dual-repo-substrate.md`:**
  - B1c in the step list and in the mechanism names, with its crashpoints.
  - The registry now holds 1→2 and 2→3, both restamp-only; legacy `files.json` is removed by B1c.
- **`testing-as-the-lab-bench/event-log-format.md`:**
  - `dedup` row → `pruned`, `prune-skip`, `error`.
  - `fs` row → `edit-no-path`, `write-error`.
- **`recurring-patterns/autonomic-vs-llm-controlled.md`:** replace the workspace-git bullet with
  "The service runs no git operation against the user's workspace; it reads only the repository's
  location and origin for project identity."
- **`mechanisms/closure-and-retirement.md`:** delete the `GetFileVersion` / `BlobReachable` /
  retention-constants sentence.
- **`key-abstractions.md`:** in the Thread row, "tracked-file sidecar" → "tracked-file store".
- **`the-3-0-hook-chain-load-bearing-primitive/index.md`** step 3: "Dedup decision (§3.9):
  buffered; identifier lines and store update at turn close; replacement at composition."
- **ROADMAP:** A11a closes when acceptance criterion 4 below is shown. Its "rule out after the fix"
  items stay as written.

---

