## 10. ARCHITECTURE changes (same change)

Each item gives the text to land, in full. Paths are under `PROJECT_DOCS/ARCHITECTURE/`.

- **`module-map.md`:** the `internal/dedup/` row's description becomes:

  > content identifiers, write-time diffs, excerpt identifier lines and the composition plan; pure,
  > dependency leaf

- **`storage-and-record-formats/home-layout.md`:** the line
  `files.json                      # tracked-file sidecar (SPEC §3.9)` becomes two lines:

  ```
      files.jsonl                     # tracked-file index (SPEC §3.9)
      files/                          # current blobs and kept diffs
  ```

- **`storage-and-record-formats/thread-directory-threads-thr-id.md`:**
  - The bullet `**`files.json`** — the tracked-file sidecar (SPEC §3.9).` is replaced by:

    > - **`files.jsonl`, `files/`** — the tracked-file store (SPEC §3.9); see "Tracked-file store".

  - After the excerpt example's header line and before `**user:**`, add the identifier lines,
    so the example reads:

    ```markdown
    ## Turn 24 · 2026-05-08T03:12:00-07:00 · [trefoil, unknot, body-topology]

    [file read #1a2b3c4d5e6f] src/a.go
    [file write #9f8e7d6c5b4a] src/a.go
    [file commit @abc1234] src/a.go

    **user:** [terse prompt excerpt]

    **agent:** [terse response excerpt, topic tag stripped]
    ```

  - The "Excerpt tiers" claim "so the tracked-file count does not grow with thread length (SPEC
    §2.3)" stays as written.
  - Add a subsection after "Excerpt tiers":

    > - **Tracked-file store.** One index and its content files per thread, written at turn close
    >   for the owner's file events (`store.ApplyFileEvents`) and pruned at barrier step B1c
    >   (`store.PruneTrackedFiles`).
    >   - *Index format.* `files.jsonl`, one line per path, sorted by `path`, keys in the order
    >     `{"path","id","size","turn","at","changes":[{"turn","at","from","to","diff","size"}]}`,
    >     written through `store.WriteJSONL`. `path` is the event's path as given. `id` is the git
    >     blob id of the current bytes, `size` their length in bytes. `turn` and `at` are the owner
    >     excerpt number and timestamp of the latest read or write. `changes` holds the last
    >     `dedup.live-diff-window` id transitions, oldest first; `diff` says whether the diff was
    >     kept, and the change's `size` is the diff's length (0 when not kept).
    >   - *Content files.* `files/<id>` holds the current bytes; `files/<from>-<to>.diff` a kept
    >     forward unified diff.
    >   - *Write order.* Blob and diff files are written (`WriteFileAtomic`) before the index that
    >     names them, so the index never names an absent file; the turn path never deletes.
    >   - *Retention.* B1c keeps entries with `turn > TurnCount − ThreadTurnWindow` and the blob and
    >     `diff:true` files they name. It deletes every other file under `files/` and a legacy
    >     `files.json`, rewriting the index before deleting. The store per thread is bounded by the
    >     paths touched within the assembly window × (current size + the diff window).
    >   - *Identifier lines.* Each file event of a turn is one line in the owner's excerpt,
    >     between the header and `**user:**`. Composition never parses them; which positions
    >     expand comes from the index.

- **`the-layered-working-set/index.md`:** add a bullet after "Tool output":

  > - **File content.** `ComposeWorkingSet` loads each Layer B thread's `files.jsonl` and runs one
  >   pure `dedup.Plan` across them. The plan gives each path a literal at its most recent
  >   position and diffs at its N most recent changes. A path whose current size is at or above
  >   `dedup.literal-share-threshold` × the per-thread share gets neither, and a diff at or above
  >   `dedup.diff-literal-threshold` of the current size is skipped. Excerpts are then walked
  >   newest-first, each charged with its planned expansions from the sizes in the index. An
  >   excerpt whose text does not fit ends the walk; the newest is always taken. An expansion that does
  >   not fit renders as its identifier with a size note. Only expansions that render are read.
  >   `workset` renders them inside their excerpt. `ComposeWorkingSet` also returns a structured
  >   report of the expansions rendered.

- **`mechanisms/crash-stability-and-the-dual-repo-substrate.md`:**
  - After the B1b sentences, ending "…re-runs it after the drain while the day-commit has not
    landed.", add:

    > Barrier step B1c (`pruneTrackedFiles`, between B1b and B2) calls `store.PruneTrackedFiles`
    > per spine record in numeric id order with the record's `TurnCount`. It rewrites a thread's
    > `files.jsonl` only if an entry drops, then deletes the files no kept entry names, so a crash
    > leaves orphans, never a dangling index. Completion re-runs it under the same condition as
    > B1b. An unreadable index is skipped (`dedup.prune-skip reason=unreadable`) and fails B2's
    > verify gate, and completion's `spineQuarantineRepair` restores it from primary HEAD, as for
    > a hand-broken folio.

  - In the crashpoint list, "the four `folio.settle.*` in `store` and
    `barrier.postSettle.preDayCommit`" becomes "the four `folio.settle.*` and
    `barrier.prune.postIndex` in `store`, and `barrier.postSettle.preDayCommit` and
    `barrier.postPrune.preDayCommit`".
  - "(steps keyed by from-format; 1→2 restamps only, since format 2 adds optional folios to format
    1)" becomes "(steps keyed by from-format; 1→2 and 2→3 restamp only: format 2 adds optional
    folios, format 3 the tracked-file store, whose legacy `files.json` B1c removes)".
- **`testing-as-the-lab-bench/event-log-format.md`:**
  - the `dedup` row's actions become `pruned`, `prune-skip`, `error`;
  - the `fs` row's actions become `edit-no-path`, `write-error`.
- **`recurring-patterns/autonomic-vs-llm-controlled.md`:** the bullet "The service issues read-only
  git queries against the user's *workspace* only for the §3.9 reachability check (is a committed
  blob still reachable, `BlobReachable`)." becomes:

  > - The service reads the user's *workspace* repository only to identify the project: its root
  >   (`store.FindGitRoot`) and origin URL (`store.GetGitOriginURL`).

- **`mechanisms/closure-and-retirement.md`:** delete the sentences from "Aged-out file versions
  come back through `GetFileVersion`" through "not yet read from directives."
- **`key-abstractions.md`:** in the Thread row, "a tracked-file sidecar" → "a tracked-file store".
- **`the-3-0-hook-chain-load-bearing-primitive/index.md`**, step 3 becomes:

  > 3. **Dedup decision** (§3.9) — buffered; identifier lines and the tracked-file store update at
  >    turn close; replacement at composition.

---
